# Skein Desktop API contract (protocol v1)

This is the contract between the Wails Go services and the daemon. The GUI
never calls the daemon directly. The daemon serves HTTP/JSON + SSE over the
Unix socket / Windows named pipe; TCP is dev-only and keeps bearer auth.

## Transport and auth

- Linux: `unix:///run/skein/control.sock`, 0660 root-owned + per-user ACL.
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

Helper-only (not daemon API, retained verbs only): `bootstrap`, `install`,
`uninstall`, `start`, `stop`, `restart`, `status`, `grant-user`,
`set-owner`, `revoke-user`. Removed helper verbs (`init`, `join`, `import`,
`rotate-secret`, `reset`, `update-daemon`) exit 1 with the daemon endpoint
that replaces them.

## System and bootstrap endpoints (ADR-001)

| Method | Path | Role | Notes |
|---|---|---|---|
| POST | `/api/v1/bootstrap/init`, `/api/v1/bootstrap/join`, `/api/v1/bootstrap/import` | `bootstrap` | Bootstrap mode only. Token in the JSON body, never in a URL or argv. 256-bit token, SHA-256 hash stored root-owned 0600, 10-minute expiry, minted verb, 5 failed attempts burn it. Bearer/dev-TCP callers get 403 even with a valid token. On success the daemon writes config/identity as sole writer, grants the bootstrap caller `secret`, deletes the state file, and switches in-process to normal mode. |
| POST | `/api/v1/system/leave` | `mutate` | This node's own departure; gossips a version 5 tombstone before the dataplane stops. There is no peer-removal endpoint (ADR-002): a peer leaves itself, never another peer. |
| POST | `/api/v1/system/rotate-secret` | `secret` | Body `{confirm:true}`. Returns the new join string once; the service must restart (helper `restart`) before eviction takes effect, and every legitimate node must rejoin with the new secret. |
| POST | `/api/v1/system/reset` | `secret` | Body `{confirm:true}` (plus `--purge`/`--keep-identity` equivalents). Config purge is this endpoint, not the helper. |
| POST | `/api/v1/system/init` | `secret` | On a configured daemon always 409 — bootstrap is the init path. |
| POST | `/api/v1/system/join`, `/api/v1/system/import` | `mutate` / `secret` | Normal-mode counterparts of the bootstrap endpoints. |
| GET | `/api/v1/system/update/check` | `secret` | Verifies a staged signed manifest (ed25519 + SHA-256, `key_id` + signed `valid_until`, basename-only binary); downgrades are 409, never "no update". |
| POST | `/api/v1/system/update/apply` | `secret` | Detached apply process with verified rollback; `.old` + config backups are never deleted on failure. |
| POST | `/api/v1/system/grants/reload` | `secret` | Reloads the live daemon's grants without a restart (after `grant-user`/`revoke-user`). |

Roles (access file: `uid:role` on Linux, `SID:role` on Windows; legacy bare
lines map to `read`): `read` for status/peers/routes/logs/diagnostics/security/
firewall reads; `mutate` for config patches, connection, probes, peer invites,
firewall repair, and leave; `secret` for network invites, rotate, reset,
update check/apply, grants reload, and system init/import; `bootstrap` only
for the bootstrap endpoints and never granted from the access file. The
system control plane is the single Unix socket (0660, root:skein) or Windows
named pipe; a bind failure is fatal. The dev TCP listener (`api.listen` +
bearer token) is for non-system dev configs only and is refused for the
system path.

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
