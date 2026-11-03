# Skein Desktop API contract (protocol v1)

This is the contract between the Wails Go services and the daemon. The GUI
never calls the daemon directly. The daemon serves HTTP/JSON + SSE over the
Unix socket / Windows named pipe; TCP is dev-only and keeps bearer auth.

## Transport and auth

- Linux: `unix:///run/skein/control.sock`, 0660 root:skein + per-user ACL.
  Caller uid from `SO_PEERCRED`; socket DACL authorizes.
- Windows: `\\.\pipe\skein-control`, DACL SYSTEM/Administrators/granted user;
  server impersonates client and checks token SID; client verifies server PID
  image/token.
- UDS/pipe requests do not send `Authorization`. TCP dev requests use
  `Authorization: Bearer <base64url(token)>`.
- Body limit 16 KiB; strict JSON; unknown fields rejected.
- Mutations are audited (caller uid/SID, action, target, result) without
  secrets; per-caller rate limit.

## Error envelope

```json
{"code":"validation_failed","message":"Port must be 1-65535","remediation":"Choose another port","details":{"field":"listen_port"},"request_id":"..."}
```

Codes: `daemon_unavailable`, `permission_denied`, `not_found`, `conflict`,
`validation_failed`, `restart_required`, `unsupported`, `rate_limited`,
`internal`.

## Types

```ts
type Capabilities = { app_version: string; protocol: number; platform: string; features: Record<string, boolean> }
type Identity = { id: string; public_key: string; name?: string }
type Connection = { state: "connected"|"paused"|"connecting"|"error"; paused_reason?: string; last_error?: string; since_ms: number }
type Status = { id; name?; overlay_ipv4; overlay_ipv6?; uptime_seconds; version; listen_port; peers; rx_bytes; tx_bytes; connection: Connection; health: "ok"|"degraded"|"offline"|"error" }
type Peer = { id; name?; state; path?; rtt_ms; overlay_ipv4?; overlay_ipv6?; endpoint?; transport?; transport_rx?; candidates?; dialer; direct; rx_bytes; tx_bytes; last_seen_ms }
type Route = { destination; next_hop; cost_ms; hops; path? }
type Activity = { origin; destination; seq; rx_bytes; tx_bytes; rtt_ms; jitter_ms; transport; direct; observed_ms; expires_ms; path? }
type ConfigView = { overlay_ipv4_prefix; overlay_ipv6_prefix; overlay_ipv4; overlay_ipv6?; listen_port; mtu; interface; api_listen; api_socket?; api_pipe?; mdns; stun; stun_server; public_addr?; config_path?; protocol; peers: string[]; network_secret:"redacted"; api_token:"redacted" }
type ConfigPatch = { name?; listen_port?; mtu?; interface?; public_addr?; mdns?; stun?; stun_server? }
type ConfigPatchResult = { restart_required: boolean; applied: string[] }
type Invite = { kind:"peer"|"network"; value: string; warning?: string; snapshot?: boolean }
type LogEntry = { time_ms; level; message }
type FirewallRule = { name; direction; action; protocol; port?; interface? }
type FirewallStatus = { manager; state; rules: FirewallRule[]; last_check_ms; last_error? }
type SecurityStatus = { config_path; config_owner; config_mode; identity_owner; identity_mode; socket_path?; allowed_uids?; update_signature; firewall_manager }
type ServiceStatus = { state:"running"|"stopped"|"starting"|"error"; manager; installed; version?; last_error? }
type ProbeResult = { peer_id; state:"pending"|"up"|"timeout"|"error"; rtt_ms?; error? }
type DiagnosticBundle = { generated_ms; version; protocol; platform; logs: LogEntry[]; peers: Peer[]; routes: Route[]; config: ConfigView; redactions: string[] }
type Event = { id?: number; type; peer_id?; detail?; time_ms }
```

## Endpoints

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/v1/capabilities` | — | `Capabilities` |
| GET | `/api/v1/status` | — | `Status` |
| GET | `/api/v1/identity` | — | `Identity` |
| GET | `/api/v1/config` | — | `ConfigView` |
| PATCH | `/api/v1/config` | `ConfigPatch` | `ConfigPatchResult` |
| POST | `/api/v1/connection` | `{action:"connect"\|"disconnect"}` | `Connection` |
| GET | `/api/v1/peers` | — | `Peer[]` |
| POST | `/api/v1/peers` | `{invite}` | 204 |
| DELETE | `/api/v1/peers/{id}` | — | 204 |
| POST | `/api/v1/peers/{id}/probe` | — | `{state:"pending"}`; result via `peer:probe` event |
| POST | `/api/v1/name` | `{name}` | 204 (deprecated; removed protocol v2) |
| GET | `/api/v1/routes` | — | `Route[]` |
| GET | `/api/v1/activities` | — | `Activity[]` |
| POST | `/api/v1/invites/peer` | — | `Invite` |
| POST | `/api/v1/invites/network` | — | `Invite` |
| GET | `/api/v1/logs?since=&limit=` | — | `{entries: LogEntry[], next: number}` |
| GET | `/api/v1/events` | — | SSE; `Last-Event-ID` replay |
| GET | `/api/v1/diagnostics` | — | `DiagnosticBundle` |
| GET | `/api/v1/security` | — | `SecurityStatus` |
| GET | `/api/v1/firewall` | — | `FirewallStatus` |
| POST | `/api/v1/firewall/repair` | — | `FirewallStatus` |

Helper-only (not daemon API): `init`, `join`, `import`, `install`, `uninstall`,
`start`, `stop`, `restart`, `status`, `rotate-secret`, `reset`, `grant-user`,
`set-owner`, `update-daemon`.

## Config patch allowlist

Allowed: `node.name`, `node.listen_port`, `node.mtu`, `node.interface`,
`node.public_addr`, `discovery.mdns`, `discovery.stun`,
`discovery.stun_server`. Rejected: `api.*`, `node.network_secret`,
`node.overlay_*`, peers, identity, unknown fields. Restart required for port,
MTU, interface, public address, mDNS/STUN server; name and STUN toggle apply
live where safe. Merge under the same lock as peer persistence; preflight and
rollback on restart failure.

## Events

`connection:changed` (`Connection`), `peers:changed` (`Peer[]`),
`routes:changed` (`Route[]`), `activity:sample` (`Activity`), `log:entry`
(`LogEntry`), `service:changed` (`ServiceStatus`), `update:available`
(`{version,url}`), `peer:probe` (`ProbeResult`). SSE ids are monotonic; a
bounded replay ring serves `Last-Event-ID`. Slow consumers are disconnected.

## State derivation

- Service state: GUI `ServiceService.Status()` reads SCM/systemd directly.
- Connection state: daemon.
- Health: service stopped/starting/error → offline (error for service error);
  connection paused → offline; connection error → error; connected with any
  peer degraded/down → degraded; else ok.
- `connecting` is only the transient interval of a connect/disconnect call.
