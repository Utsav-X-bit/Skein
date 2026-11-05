# Skein

[![ci](https://github.com/Utsav-X-bit/Skein/actions/workflows/ci.yml/badge.svg)](https://github.com/Utsav-X-bit/Skein/actions/workflows/ci.yml)

Skein is a small, self-hosted mesh VPN for 2–5 of your own machines on
Linux and Windows — a Computer Networks project built to answer one
question end to end: **what does it actually take to get encrypted,
authenticated packets from one of my machines to another, across NATs,
across address families, with no server in the middle?**

The name is the design: a *skein* is a small coil of thread, and here
every peer is a thread woven into the same mesh. There is no hub, no
coordinator, and no account. Every peer is equal, every peer can relay
for others, and the mesh forms from the peers themselves.

```mermaid
flowchart LR
    A["n4<br/>IPv4 only<br/>10.144.144.1"]
    B["ndual<br/>IPv4 + IPv6<br/>10.144.144.2"]
    C["n6<br/>IPv6 only<br/>10.144.144.3"]
    A -- "UDP over IPv4" --> B
    B -- "UDP over IPv6" --> C
    A -. "end-to-end Noise session, relayed by ndual" .-> C
```

`ndual` forwards opaque ciphertext: it never holds the `n4 ↔ n6` session keys.

> Status: `0.1.0-dev`. The architecture below is implemented, and
> live Linux/Windows install, upgrade, and mesh verification has
> passed on `dev` (see `docs/engineering/M9-ACCEPTANCE.md`); the
> final release verdict is still open.

## Contents

- [What it is](#what-it-is)
- [How it works, fundamentally](#how-it-works-fundamentally)
- [Features](#features)
- [Skein vs Tailscale and other mesh VPNs](#skein-vs-tailscale-and-other-mesh-vpns)
- [Installation](#installation)
- [Usage](#usage)
- [Configuration](#configuration)
- [Firewall](#firewall)
- [Limitations](#limitations)
- [Future scope](#future-scope)
- [Testing](#testing)
- [Docs and layout](#docs-and-layout)

## What it is

Skein creates a private overlay network on top of the ordinary internet:

- Each machine gets a **TUN interface** (`skein0` on Linux, `Skein` on
  Windows) with a stable overlay address — IPv4 `10.144.144.0/24` and
  IPv6 ULA `fd00:6d65:7368::/64` by default — so machines talk to each
  other by overlay IP exactly as if they were on one LAN: `ssh
  user@10.144.144.2` just works, with no per-service port forwards.
- Every pair of machines negotiates its own **end-to-end encrypted
  session**. Traffic between two peers is encrypted for those two peers
  only, even when it crosses a third peer acting as a relay.
- Peers **discover and route to each other themselves** — no central
  server tells them where anyone is. Discovery, routing, NAT traversal,
  and failure recovery are all part of the protocol, not an external
  service.

It is deliberately small: 2–5 nodes, routes of at most 4 links, one
underlay port (default `11010`). That scope is what makes it feasible
to implement, audit, and explain completely — which is the point of a
networks project.

## How it works, fundamentally

The stack has four layers. Each one is a standard networking idea;
Skein is the composition of them.

### 1. The overlay: a TUN interface and two prefixes

The daemon creates a TUN device and assigns it an address from each
overlay prefix. The kernel routes overlay destinations into the TUN
device; the daemon reads those packets, encrypts them, and sends them
over the underlay (UDP, or TCP as a fallback). Incoming packets are
decrypted and written back into TUN, and the kernel delivers them to
local sockets. Applications never know the tunnel exists — they just
use overlay IPs.

MTU is 1280 by default. A 1280-byte inner packet becomes **1382 bytes
over IPv6** or **1362 bytes over IPv4** on the wire (see the frame
format below), so an underlay path with a smaller PMTU is unsupported:
there is no PMTUD or fragmentation yet.

### 2. The session: Noise IKpsk2 between every pair

Each node has an X25519 identity key; the peer ID is derived from its
public key, so a public key is self-certifying — if you know a peer's
ID, you can verify you are talking to that peer and no one else.

Pairs handshake with **`Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s`**:

- **IK**: the initiator already knows the responder's static public
  key (from an invite or gossip), so the handshake authenticates both
  sides immediately — no unauthenticated first flight.
- **psk2**: a pre-shared key — the network secret from the join string
  — is mixed into the handshake, so only members of the same network
  can complete a handshake at all. Possession of the network secret is
  membership.
- The **epoch is bound into the prologue**, sessions install only after
  an encrypted confirmation, and at one epoch the smaller attempt ID
  (a 128-bit handshake identifier) wins, so simultaneous handshakes
  converge instead of duplicating sessions.
- Sessions **rekey every hour or 2³² packets**, with 64-packet replay
  windows per key epoch. A session that goes silent is dropped, never
  encrypted into.

### 3. The frame: a 26-byte header, relay-mutable in exactly two fields

```
[type:1][to_next:8][to_final:8][from:8][ttl:1] payload
```

The header is authenticated as AEAD associated data **except**
`to_next` and `ttl` — the only two fields a relay is allowed to
change. Encrypted frames add `[epoch:4][counter:8]` and a 16-byte
authentication tag. That is the whole relay contract in one sentence: a
relay rewrites `to_next` (who to hand the frame to next) and decrements
`ttl`, and cannot read, modify, or forge anything else. A relay sees
peer IDs, frame sizes, and timing — that metadata leakage is inherent
to relaying and is listed under [Limitations](#limitations).

### 4. Discovery, routing, and NAT traversal — the mesh itself

**Discovery.** A mesh forms without every pair exchanging invites.
Peers learn each other from static config, `skein:1:` peer invites
(identity + addresses, no secret), `skein:2:` network invites (which
carry the secret), mDNS on the local link, and **authenticated STATE
gossip**: every 5 seconds peers exchange signed state carrying overlay
addresses, names, and public keys. A STUN probe (default
`stun.l.google.com:19302`, optional) learns each node's public IPv4/IPv6
so joiners dial the right place without editing configs.

**Routing.** A bounded **distance-vector protocol with full paths and
split horizon**: advertisements carry the whole path, routes longer
than 4 links are rejected, and a peer never advertises a route back to
the peer it learned it from. The route table keeps the cheapest
validated path, and tries IPv6 first when it works. When a path dies,
the session is dropped and the dialer retries UDP, then TCP on the same
port, then the relayed route.

**NAT traversal.** One side of every pair must be reachable — a public
IPv4 with a forward, a public IPv6, or the same LAN — or the pair must
be introduced by a relay:

- **Direct** — dial the peer's advertised endpoint.
- **Relayed** — both sides connect to any reachable peer (a cheap VPS
  is enough); it forwards opaque ciphertext, as in the diagram above.
- **Introductions (hole punching)** — a peer reachable only through a
  relay asks the relay, which reports both observed endpoints; both
  sides dial each other at once. This works for endpoint-independent
  NAT; symmetric NAT keeps the relayed path.
- **TCP fallback** — after two silent UDP attempts the dialer opens
  TCP to the same port (set `listen_port = 443` to run both transports
  on 443). TCP frames are written in one syscall with Nagle disabled
  so the fallback does not collapse under delayed ACKs, and a datagram
  from the peer clears the TCP preference again, so a working UDP path
  always wins.

**Liveness.** Keepalive every 15 s keeps NAT mappings open. 45 s
without authenticated traffic marks a peer down, clears its cached
endpoint, drops the session, and withdraws its routes; a configured
dialer re-handshakes immediately instead of encrypting for a dead
address. Handshake retries back off 1 s → 30 s and reset as soon as a
route appears. A changed endpoint is only adopted after a
challenge/response over the existing session, so an attacker cannot
hijack a peer by announcing a new address.

**Leaving.** There is no "remove this other peer": every peer is
equal, anyone can invite, and a peer leaves itself (`skein system
leave`) and gossips a **tombstone** so all other peers withdraw its
routes. Leaving is graceful departure, not a ban — the leaver keeps
its credential until a network-wide secret rotation, and rejoining
needs a fresh invite. Rotation evicts only after the service restarts
and every legitimate node rejoins with the new secret.

### The control plane: one privileged daemon, one minimal helper

A VPN cannot run as a normal app: creating the TUN device, assigning
overlay addresses, installing routes, and opening firewall rules need
root on Linux or Administrator/SYSTEM on Windows. Skein therefore
splits privilege exactly once (ADR-001,
[`docs/engineering/ADR-001.md`](docs/engineering/ADR-001.md)):

- **`skeind` — the daemon/service — is the only privileged actor.** It
  owns the TUN device, routes, firewall, sessions, peers, and config
  (single writer, cross-process lock), and serves the control API:
  init/join/import, rotate, reset, leave, and self-update.
- **`skein-helper` — a fixed-verb elevated helper** — keeps only
  `bootstrap`, `install`, `uninstall`, `start`, `stop`, `restart`,
  `status`, `grant-user`, `set-owner` (Windows), and `revoke-user`.
  Every verb validates its inputs and returns one JSON object; secrets
  travel on stdin or a GUI-created pipe, never argv.
- **`skein` (CLI/TUI) and the desktop GUI are always unprivileged.**
  They talk to the daemon over a root-owned Unix socket
  (`/run/skein/control.sock`, 0660, `root:skein`) or a Windows named
  pipe (`\\.\pipe\skein-control`, DACL SYSTEM/Administrators/granted
  user), never through a shell. The daemon authenticates every caller
  by kernel transport (Unix `SO_PEERCRED` uid, Windows pipe SID) and
  authorises by role from the access file: `read` (status, peers,
  routes, diagnostics), `mutate` (config patches, probes, invites,
  firewall repair, leave), `secret` (network invites, rotate, reset,
  update, grants). A bind failure is fatal — the daemon never runs
  headless with a dead control plane.
- **Bootstrap** is the one place no config, identity, or grant exists
  yet, so it cannot go through the normal role-gated API. The helper
  (elevated via pkexec/UAC/polkit) checks that no system config
  exists, generates 256 bits with `crypto/rand`, and stores only the
  SHA-256 hash — root-owned 0600, with the invoking uid/SID, a
  10-minute expiry, and the verb it was minted for. The daemon starts
  in bootstrap mode (control plane only — no TUN, no UDP port, no
  mDNS, no firewall, not a mesh member) and accepts exactly one
  `POST /api/v1/bootstrap/init|join|import` with the token in the JSON
  body; verification order is transport → uid match → constant-time
  hash → expiry → unused, and five failed attempts burn the token. On
  success the token is marked used atomically before any config work,
  the bootstrap caller is granted the `secret` role, the state file is
  deleted, and the daemon switches in-process to normal mode.

## Features

- **End-to-end `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s`** between every
  pair, with explicit nonces, 64-packet replay windows, and hourly /
  2³²-packet rekeying.
- **Dual overlay**: IPv4 `10.144.144.0/24` and IPv6 ULA
  `fd00:6d65:7368::/64`, both configurable per node.
- **One underlay port** (default `11010`), dual-stack socket with a
  paired-socket fallback, plus TCP on the same port.
- **Discovery without a server**: static peers, peer/network invites,
  mDNS, authenticated STATE gossip, STUN.
- **Bounded routing**: distance vector with full paths, split horizon,
  at most four links; relays rewrite only `to_next` and `ttl`.
- **Relay introductions** (hole punching) with relayed-path fallback.
- **One privileged daemon, one minimal helper** (ADR-001); GUI and CLI
  unprivileged; role-gated control API; single-use bootstrap tokens.
- **Automatic firewall**: the daemon opens the underlay port and
  trusts the overlay interface (ufw, firewalld, raw iptables fallback,
  or Windows Firewall), so any protocol works without extra rules.
  The desktop Security page shows the manager, owned rules, and a
  repair action.
- **Self-maintaining config**: learned peers are written back to the
  TOML atomically, so a restart keeps the mesh.
- **Named nodes**: `node.name` travels in STATE gossip and appears in
  the dashboard, `skein peers`, and the API.
- **Signed, operator-driven self-update**: staged ed25519 + SHA-256
  manifest with `key_id` and signed `valid_until`; downgrades refused;
  verified rollback restores the previous binary and config. No
  automatic checks, no version feed.
- **Mission-control dashboard**: `skein dashboard` adapts from compact
  to wide terminals, renders only known route paths, and shows
  per-link throughput, transport, RTT, jitter, and authenticated
  aggregate mesh activity (byte deltas and link performance only —
  never packet contents, overlay addresses, ports, or flow IDs).

## Skein vs Tailscale and other mesh VPNs

Tailscale is the reference point most people know: WireGuard-based,
with a **coordination server** that authenticates users (usually via
SSO), distributes keys and routes, and runs a global **DERP relay
network**, plus MagicDNS, ACLs, exit nodes, and subnet routers. It is
excellent, mature, and operated by a company. Skein makes the opposite
trade on purpose.

| | Skein | Tailscale (typical) |
|---|---|---|
| Control plane | None — peers gossip state to each other | Coordination server + account/SSO |
| Relay network | Any reachable peer of yours (e.g. a cheap VPS) | Global DERP network run by the vendor |
| Cryptography | Noise IKpsk2, custom wire format | WireGuard protocol |
| Discovery | mDNS, gossip, STUN, invites | Coordination server |
| DNS | None (use overlay IPs or your own DNS) | MagicDNS |
| Access control | Control-plane roles per local uid/SID; one shared network secret | ACLs, SSO, device approval |
| Exit nodes / subnet routes | No | Yes |
| Platforms | Linux, Windows (desktop + CLI) | Linux, Windows, macOS, iOS, Android |
| Scale | 2–5 nodes, routes ≤ 4 links | Thousands of nodes |
| Trust | Nobody but your peers; no vendor sees metadata | Vendor operates coordination + DERP |

**Where Skein wins:**

- **No account, no server, no vendor.** Nothing about your mesh —
  membership, routes, activity — is reported to anyone. A mesh works
  entirely on a LAN with no internet at all (mDNS + gossip; STUN is
  optional).
- **Relay without a third party.** Any peer you already trust can be
  the relay, and relaying is opaque by construction (two mutable
  header fields, everything else AEAD-authenticated).
- **Small and auditable.** The whole design — handshake, frame,
  routing, discovery, privilege split — fits in one README and two
  ADRs. There is no coordination protocol to trust blindly.
- **Dual-stack bridging for free.** An IPv4-only host and an
  IPv6-only host communicate through a dual-stack peer, end to end
  encrypted, with no special configuration.

**Where Skein lacks (honestly):**

- **No global relay network.** If no peer of yours is reachable from
  the internet, two symmetric-NAT peers cannot connect. Tailscale's
  DERP network solves this by existing everywhere; Skein asks you to
  provide one reachable peer.
- **No DNS, exit nodes, or subnet routing.** You address peers by
  overlay IP, and only overlay IPs are routed.
- **No per-peer revocation.** One shared secret is membership; evicting
  a peer means rotating the secret and rejoining every legitimate node.
  Per-peer invites are future work, not a feature.
- **No mobile clients, no SSO, no ACLs** beyond local control-plane
  roles.
- **Smaller scale and younger code.** Five nodes and four-link routes
  are hard limits, and the implementation has not had Tailscale's
  years of adversarial production use.

## Installation

Skein ships as five pieces:

| File | Purpose |
|---|---|
| `skeind` | Privileged daemon / system service (the only privileged actor) |
| `skein` | Unprivileged CLI, TUI dashboard, and control-API client |
| `skein-helper` | Elevated minimal helper (retained verbs only) |
| `skein-desktop` | Wails desktop control surface (unprivileged) |
| `wintun.dll` | Windows tunnel driver (Windows only) |

There are two ways to install Skein. Method 1 is the complete,
one-shot install; Method 2 is for developers who want to build from
source. Both end at the same place: create or join a network (see
[Usage](#usage)).

One thing to know before either method: **the service is registered
when you create or join your first network, not at install time.**
The helper's `install` verb needs an existing system config, and the
bootstrap flow (first network creation) is what registers the
service. So a first install only puts the files in place; an upgrade
over an existing network also re-registers and restarts the service,
so the mesh comes back up.

### Method 1 — Install the packaged build (recommended)

**Windows — setup exe.** The installer does everything: copies all
five files into `C:\Program Files\Skein`, stops a running service
first on upgrade, and — when a network already exists — re-registers
and starts the `skein` service (the daemon creates the firewall rules
at startup). On a first install there is no network yet, so there is
no service to start: open Skein and create or join a network, and
that first step registers the service.

The exe is a build artifact, not a file in the repo — build it once
from `gui/`. The packaging step builds the system binaries and
fetches Wintun for you, so you only need the desktop build first.
(The `PATH` change below lasts only for this PowerShell session.)

```powershell
$env:PATH = "$env:USERPROFILE\go\bin;C:\Program Files (x86)\NSIS;$env:PATH"
cd gui
wails3 build          # desktop -> gui\bin\skein-desktop.exe
wails3 package        # -> gui\bin\skein-desktop-amd64-installer.exe
cd ..
```

Then run it (`/S` is silent; without it you get the wizard. The
installer asks for Administrator itself.):

```powershell
.\gui\bin\skein-desktop-amd64-installer.exe /S
```

(Go 1.26.10+, Node.js, and Wails 3 v3.1.0 needed; NSIS 3.11 for the
packaging step. See Method 2 for the full prerequisite list.)

**Linux — nfpm package.** Build the package, install it, and — only
when a network already exists — re-register and start the service
with the helper. On a first install there is no config yet, so skip
the helper step: the service is registered when you create or join a
network.

```bash
# system binaries (from the repo root):
go build -trimpath -o gui/bin/skeind ./cmd/skeind
go build -trimpath -o gui/bin/skein-helper ./cmd/skein-helper
go build -trimpath -o gui/bin/skein ./cmd/skein
# desktop + package (from gui/; format is deb, rpm, or archlinux):
cd gui
wails3 build   # desktop -> gui/bin/skein-desktop
wails3 tool package -name "skein-desktop" -format archlinux \
  -config ./build/linux/nfpm/nfpm.yaml -out ./bin
cd ..
# install (Arch shown; use the matching package for your distro):
sudo pacman -U gui/bin/*.pkg.tar.zst
# only when a network already exists (upgrade/reinstall):
sudo /usr/libexec/skein-helper install
sudo /usr/libexec/skein-helper status
```

If you previously installed the binaries by hand, the package manager
refuses to overwrite them — pass `--overwrite` for the four binary
paths (`/usr/local/bin/skeind`, `/usr/local/bin/skein`,
`/usr/local/bin/skein-desktop`, `/usr/libexec/skein-helper`), e.g.
`pacman -U --overwrite '/usr/local/bin/*' --overwrite
'/usr/libexec/skein-helper' <package>`.

**Upgrading from a pre-dev install.** Config and identity are adopted
unchanged, but the old access file holds a bare uid (Linux) or SID
(Windows), which the dev code maps to the `read` role — the owner
cannot revoke, rotate, or use secret endpoints until promoted:

```bash
sudo /usr/libexec/skein-helper grant-user 1000 --role secret   # Linux
```

```powershell
& "C:\Program Files\Skein\skein-helper.exe" set-owner <your-username>   # Windows
```

The Windows form writes `<SID>:secret` and reports
`restart_required: true` — restart the service after it.

### Method 2 — Build from source

**Prerequisites**

| | Linux | Windows |
|---|---|---|
| Go | 1.26+ (`go.mod` requires 1.26.10) | 1.26+ |
| Wails 3 CLI (desktop only) | `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0` | same |
| UI toolkit | GTK4 + WebKitGTK 6.0 dev packages | WebView2 runtime (included with current Windows 10/11) |
| Tunnel driver | kernel TUN (built in) | `wintun.dll` via `scripts/fetch-wintun.*` |
| Privilege for install | root / sudo | Administrator |

```bash
# Arch
sudo pacman -S --needed go gtk4 webkitgtk-6.0

# Debian / Ubuntu
sudo apt install build-essential libgtk-4-dev libwebkitgtk-6.0-dev

# Fedora
sudo dnf install gcc gtk4-devel webkitgtk6.0-devel

export PATH="$PATH:$(go env GOPATH)/bin"
wails3 doctor
```

**Linux — build and install by hand.** Run everything in order,
from the repo root. The helper lives at `/usr/libexec/skein-helper`
— it is not on `PATH` (nor in sudo's `secure_path`), so always call
it by its full path.

```bash
git clone https://github.com/Utsav-X-bit/Skein && cd Skein
go build -trimpath -o skein ./cmd/skein
go build -trimpath -o skeind ./cmd/skeind
go build -trimpath -o skein-helper ./cmd/skein-helper
export PATH="$PATH:$(go env GOPATH)/bin"
(cd gui && wails3 build)          # -> gui/bin/skein-desktop

sudo install -m 755 skeind /usr/local/bin/skeind
sudo install -m 755 skein-helper /usr/libexec/skein-helper
sudo install -m 755 skein /usr/local/bin/skein
sudo install -m 755 skein-desktop /usr/local/bin/skein-desktop
# only when a network already exists (upgrade/reinstall):
sudo /usr/libexec/skein-helper install
sudo /usr/libexec/skein-helper status
```

Then create or join a network (see [Usage](#usage)). The firewall
rules the daemon needs are applied by the daemon itself at startup;
to apply them early (or repair them without starting the daemon), use
the firewall script:

```bash
sudo ./scripts/firewall-linux.sh [--port 11010] [--interface skein0]
sudo ./scripts/firewall-linux.sh --remove   # undo exactly those rules
```

The script detects ufw (active) → firewalld (running) → raw iptables
in that order, mirroring the daemon's own detection, and prints the
manager it used.

**Windows — build and install by hand.** Run everything in order,
from the repo root, in one PowerShell session. The Wails CLI lands in
`%USERPROFILE%\go\bin`, which is not on `PATH` by default — add it
first (this lasts only for this session), and add NSIS if you want
the installer too. The install script finds `dist\` and
`gui\bin\skein-desktop.exe` on its own — no `-SourceDir` or
`-DesktopPath` needed when you built in the usual places.

```powershell
$env:PATH = "$env:USERPROFILE\go\bin;C:\Program Files (x86)\NSIS;$env:PATH"

git clone https://github.com/Utsav-X-bit/Skein
cd Skein
go build -trimpath -o dist\skein.exe .\cmd\skein
go build -trimpath -o dist\skeind.exe .\cmd\skeind
go build -trimpath -o dist\skein-helper.exe .\cmd\skein-helper

cd gui
wails3 build          # -> gui\bin\skein-desktop.exe
cd ..

.\scripts\fetch-wintun.cmd -Dest dist

powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\install-windows.ps1
```

The script copies the binaries into `C:\Program Files\Skein`,
creates the firewall rules, and — when a network already exists —
registers and starts the service. It never touches
`%ProgramData%\Skein`, so reinstalls keep your config, identity, and
network membership. Re-running it upgrades in place. Then create or
join a network (see [Usage](#usage)).

**Git Bash note:** do not paste the PowerShell commands into Git
Bash — backslashes escape the next character, so `.\cmd\skein`
becomes `.cmd\skein`. Use forward slashes and install the Wails CLI
into `$HOME/go/bin`. Use Windows Terminal or `winpty` for the
animated TUI; MinTTY gets the static snapshot.

## Usage

### Desktop (GUI)

**First node**

1. Launch `skein-desktop` (Linux) or
   `C:\Program Files\Skein\skein-desktop.exe` (Windows).
2. Choose **Create a network**. The defaults are usually enough; set a
   name if you want peers to see it.
3. Approve the UAC/`pkexec` prompt. The helper mints a single-use
   bootstrap token and starts the daemon in bootstrap mode; the daemon
   writes the config and identity as the single writer and then runs
   normally.
4. Copy the join string shown by the app.

**Other nodes**

1. Launch the desktop and choose **Join a network**.
2. Paste the join string.
3. Approve the elevation prompt. The node bootstraps, joins, and starts
   automatically.

If `/etc/skein` (Linux) or `C:\ProgramData\Skein` (Windows) already
exists but the service is stopped, choose **Use an existing
installation** instead of creating a new network. The Overview page
shows peers and routes; Security shows the firewall manager and repair
action.

### Terminal

With the service installed, the CLI attaches over the control
socket/pipe; no `-c` or `sudo` is needed for mesh commands (service
lifecycle goes through the helper):

```bash
# First node: the helper mints a single-use bootstrap token and starts
# the daemon in bootstrap mode; the CLI posts it to the daemon bootstrap
# endpoint.
sudo skein system init [--name NAME] [--port PORT] [--mtu MTU]
# Joining node:
sudo skein system join "skein:2:…"
# Then: lifecycle via the helper, mesh operations via the daemon API:
sudo skein system start     # start the service
sudo skein system stop      # stop the service
sudo skein system restart   # restart the service
skein system leave        # leave this node's own network (see Limits)
skein system rotate       # rotate the network secret via the daemon, then helper restart
```

Windows (from `C:\Program Files\Skein`, elevated for `system` commands):

```powershell
.\skein.exe system init
.\skein.exe system join "skein:2:…"
.\skein.exe system start
```

### Everyday commands

| Command | Purpose |
|---|---|
| `skein status [--json]` | Overlay addresses, peers, uptime, counters |
| `skein peers [list\|add]` | Manage peers (no removal: a peer leaves itself) |
| `skein routes [--json]` | Reachability table |
| `skein probe <peer-id>` | Re-probe candidate endpoints |
| `skein ping <overlay-ip>` | Ping through the overlay |
| `skein invite` | Print `skein:1:<base64url>` (identity + addresses, no secret) |
| `skein invite --network` | Print the `skein:2:` join string (contains the secret) |
| `skein name [value]` | Show or set this node's name |
| `skein dashboard` | Live TUI; attaches to the service socket/pipe |
| `skein genkey` | Print the public key and node ID |
| `skein version`, `skein -v` | Version plus the commit the binary was built from |

Dashboard keys: `Tab`/`h`/`l` panels, `j`/`k` or arrows select,
`Enter` inspector, `Space` pause motion, `?` help, `q` quit.
`--no-motion` renders a static snapshot. If no daemon is answering,
the dashboard reports that and does not start one: it never spawns a
daemon. Under `sudo` the invoking user's config is used.

**Dev-only path** (no service; the system path above is the supported
one):

```bash
skein init                                  # prints a join string
sudo skein run -c ~/.config/skein/skein.toml
# in another terminal:
skein dashboard
```

Join another node with `skein join "skein:2:…"` before starting its
daemon. `skein run` is the dev path only; the service uses
`/etc/skein` (Linux) or `%ProgramData%\Skein` (Windows).

### Naming nodes

```bash
skein name "utsav-laptop"     # show or set this node's name
```

The name is advertised in STATE gossip, so every peer displays it
instead of a peer ID. A peer can also be given a local hint before
first contact:

```toml
[[peer]]
id     = "b3f64ecc491aba35"
name   = "yoga"
pubkey = "…"
```

The name a peer advertises about itself always wins over the local hint.

## Configuration

```toml
[node]
overlay_ipv4_prefix = "10.144.144.0/24"
overlay_ipv6_prefix = "fd00:6d65:7368::/64"
overlay_ipv4        = "10.144.144.2"
overlay_ipv6        = "fd00:6d65:7368::2"   # optional; requires mtu >= 1280
listen_port         = 11010
network_secret      = "<32 random bytes, base64url>"
mtu                 = 1280
interface           = "skein0"              # Windows default: Skein
name                = "utsav-laptop"        # optional, shown to peers (default: hostname)
public_addr         = "203.0.113.7:11010"   # optional: only when the external port differs

[api]
socket = "/run/skein/control.sock"          # Linux service; helper-managed
# pipe = "skein-control"                    # Windows service
listen = "127.0.0.1:11011"                  # dev-only loopback listener
token  = "<32 random bytes, base64url>"     # dev-only bearer token

[discovery]
mdns        = true
stun        = true                           # learn and advertise the public IPv4/IPv6
stun_server = "stun.l.google.com:19302"      # optional override
```

The desktop/service config is `/etc/skein/skein.toml` on Linux and
`C:\ProgramData\Skein\skein.toml` on Windows, root/Administrator-owned;
the helper writes `api.socket` or `api.pipe` and disables the dev TCP
listener. `api.listen` + `api.token` are for a user-run dev daemon
only. Unknown keys are rejected. The daemon refuses a config readable
by group or others, and the X25519 key lives in `<config
dir>/identity.key` with owner-only permissions.

Full key-by-key reference: [`docs/protocol.md`](docs/protocol.md#configuration).

## Firewall

`skein run` opens what it needs at startup, idempotently:

- **Linux** — with `ufw` active it allows `<port>/udp`, all traffic on
  the overlay interface, and the matching route rules; with `firewalld`
  it adds the port and puts the interface in the trusted zone;
  otherwise it appends `INPUT`/`OUTPUT`/`FORWARD` accepts for the
  interface and underlay port.
- **Windows** — it creates `Skein underlay` (inbound UDP on the port),
  `Skein overlay` (all inbound traffic from the overlay prefix),
  `Skein overlay out` (all outbound traffic to the overlay prefix),
  and the v6 pair — scoped by prefix, because `netsh` cannot scope a
  rule to a named interface.

The desktop Security page reports the detected manager, the owned rule
set, config/identity ownership and modes, socket ACL, update signature,
and a **Repair rules** action. A systemd sandbox must allow
`/run/ufw.lock` for `ufw status` to work; the shipped unit does.

Because the overlay interface is trusted, every protocol works without
per-service rules:

```bash
ssh user@10.144.144.2
python3 -m http.server 8000 --bind 10.144.144.1
```

The only underlay requirement is that **one side of each pair is
reachable** on the UDP port: a public IPv4 with a forward, a public
IPv6, or the same LAN. SSH and every other protocol travel inside the
tunnel and never need their own router forward.

**Manual firewall setup** (pre-seed a host, or repair after an external
firewall reset, without starting the daemon):

```bash
# Linux: detects ufw -> firewalld -> iptables, mirroring the daemon
sudo ./scripts/firewall-linux.sh [--port 11010] [--interface skein0]

# Windows: no separate step — the daemon creates the rules at startup,
# and the setup exe / install script pre-create the same set.
```

## Reaching peers behind NAT — examples

| A | B | C | Result |
|---|---|---|---|
| CGNAT v4 + public v6 | CGNAT v4 + public v6 | public v4 | A↔B **direct over IPv6**; A↔C and B↔C direct if C has IPv6, otherwise relayed through whichever node does |
| CGNAT v4 + public v6 | public v4 + public v6 | CGNAT v4 | A↔B direct over IPv6; C dials B over IPv4; A↔C relayed through B |
| CGNAT v4 + public v6 | public v4 + public v6 | CGNAT v4 + no v6 | same, with C dialing B and B forwarding |

Candidates include every interface address (v4 and v6), so the dialer
tries IPv6 first when it works; the route table then keeps the cheapest
validated path. IPv6 needs UDP `<port>` open on the listener's host
firewall, which the daemon configures automatically.

Not implemented: TLS camouflage and a global DERP-style relay network.
STUN is implemented (`discovery.stun`); the probes learn the public
IPv4/IPv6.

## Packaging

- **Linux (nfpm, `gui/build/linux/nfpm/nfpm.yaml`)**: ships
  `skein-desktop` (`/usr/local/bin/skein-desktop`), `skeind`
  (`/usr/local/bin/skeind`), `skein-helper`
  (`/usr/libexec/skein-helper`), `skein` (`/usr/local/bin/skein`),
  the systemd unit (`/usr/lib/systemd/system/skein.service`,
  `ExecStart=/usr/local/bin/skeind run -c /etc/skein/skein.toml`),
  the `skein` group (sysusers, `g skein -`), the runtime directory
  (tmpfiles, `/run/skein 0750 root skein`), and the polkit policy
  (`/usr/share/polkit-1/actions/org.skein.helper.policy`). The unit
  and the shipped template agree on `ExecStart`; upgrades adopt the
  new binary through `RegisterDaemon` without rewriting the unit, and
  `<binary>.old` plus a timestamped config backup are kept on every
  upgrade. Uninstall never purges config by default (Linux keeps
  `/etc/skein` and `/var/lib/skein/backups`); purge is the daemon
  reset endpoint, not the helper. Build it as in Installation,
  Method 1 (Linux) — the package build, install, and (on upgrade)
  service activation are there in order.
- **Windows (NSIS, `gui/build/windows/nsis/project.nsi`)**: ships the
  desktop, `skeind.exe`, `skein-helper.exe`, `skein.exe`, `wintun.dll`,
  and the Wintun licence into `C:\Program Files\Skein`. The build
  fails if any required binary is missing. Install stops a running
  service first (upgrade), then — when a network already exists —
  re-registers and starts the service through the helper; on a first
  install there is no config yet, so the service is registered by the
  bootstrap flow when the first network is created. Uninstall runs the
  helper's `uninstall` verb first (stop-then-delete) and aborts without
  deleting anything if the helper fails, so no orphaned service is left
  behind; purge moves `%ProgramData%\Skein` to `.bak` instead of
  deleting it.
- **Wintun**: version 0.14.1, SHA-256-pinned in
  `scripts/fetch-wintun.sh` (`.ps1`/`.cmd` wrappers for Windows). The
  packaged NSIS install places the DLL in the admin-only install
  directory; a portable/zip layout in a user-writable directory is not
  a blessed install.
- **Self-update**: operator-driven, never automatic. The operator
  stages a signed manifest (ed25519 + SHA-256, `key_id` + signed
  `valid_until`, basename-only binary under the staging directory);
  the daemon's `GET /api/v1/system/update/check` verifies it and
  refuses downgrades (the new version must be strictly greater — a
  genuine downgrade is a package reinstall, not an update), and `POST
  /api/v1/system/update/apply` launches a detached apply process with
  verified rollback: on failure the previous binary and config are
  restored and the `.old`/config backups are never deleted. The GUI
  About/Updates screen drives check/apply; there is no version-feed
  endpoint and no automatic check.
- Content assertions: `scripts/packaging-assert.sh` parses both
  manifests and fails if a shipped file, the install directory, the
  uninstall order, or the purge behaviour drifts;
  `scripts/collapse-gates.sh` enforces the collapse invariants (single
  writer, no daemon spawn from GUI/CLI, no removed helper verb called
  from GUI/CLI, no removed endpoint, no secret logging). Both run in CI.

## Limitations

- Two symmetric-NAT peers still need a reachable relay peer; there is
  no global relay network.
- The TCP fallback is plain TCP, not TLS; it defeats UDP blocking but
  not deep-packet inspection.
- IPv4-only ↔ IPv6-only needs a dual-stack member; no common family,
  no path.
- More than five nodes or routes longer than four links are
  unsupported.
- No PMTUD or fragmentation; a dual-stack underlay path below 1382
  bytes of PMTU is unsupported.
- One shared secret, no per-peer revocation. The join string contains
  that secret, so anyone holding it can join the network. There is no
  "remove this other peer": every peer is equal, anyone can invite,
  and a peer leaves itself and gossips a tombstone (see [How it
  works](#how-it-works-fundamentally) and `docs/protocol.md`). Leaving
  is graceful departure, not a ban — the leaver keeps its credential
  until a network-wide secret rotation, and rejoining needs a fresh
  invite. Rotation evicts only after the service restarts and every
  legitimate node rejoins with the new secret.
- The control API authenticates the caller by kernel transport and
  authorises by role; a process running as a granted uid can use only
  its role's endpoints. Relays can observe peer IDs, sizes, and timing.
- No DNS, exit nodes, subnet routing, or mobile clients (see the
  comparison above).

## Future scope

Near term, in rough order of value:

- **Per-peer invites and revocation** (the deferred F-01): replace the
  single shared secret with per-peer credentials so one peer can be
  evicted without rejoining the whole network. Needs its own ADR and
  live-mesh verification.
- **Larger meshes**: link-state routing to replace distance vector,
  lifting the 5-node / 4-link caps without losing loop-freedom.
- **PMTUD and fragmentation**: probe the underlay path MTU and
  fragment inner packets, instead of requiring a 1382-byte PMTU.
- **Camouflaged fallback**: TLS or QUIC transport for the TCP fallback
  so it survives deep-packet inspection, not just UDP blocking.
- **Global relay option**: an opt-in, self-hostable DERP-style relay
  for peers with no reachable member — without making it mandatory.
- **DNS, exit nodes, subnet routes**: overlay DNS resolution, a
  designated egress peer, and advertising LAN prefixes behind a peer.
- **Mobile clients** (Android/iOS) and a headless-first onboarding
  flow for servers.
- **Measurement**: reproducible throughput/latency/convergence
  benchmarks against WireGuard and Tailscale on the same hardware
  (see `docs/measurements.md` for the current method).

## Testing

```bash
go test -race ./...
(cd gui && wails3 build)                            # desktop compile
sudo -E env "PATH=$PATH" ./test/netns/run.sh        # v4-only ↔ v6-only bridge
SKEIN_TUN_PROBE=1 sudo -E env "PATH=$PATH" \
  go test ./internal/tun -run TestTUNWriteRead -v    # TUN kernel round-trip
```

TUI smoke: `skein dashboard` with the service running should attach
over the socket/pipe and quit with `q`. Windows: build with
`test/windows/build.ps1`, then run `test/windows/run-all.ps1 -Bin
.\dist` from an elevated PowerShell (see
[`test/windows/README.md`](test/windows/README.md)).

## Docs and layout

- [`docs/protocol.md`](docs/protocol.md) — wire format, handshake,
  routing, discovery, configuration.
- [`docs/measurements.md`](docs/measurements.md) — what was measured
  and how to reproduce it.
- [`docs/gui-api.md`](docs/gui-api.md) — desktop/daemon API contract.
- [`docs/engineering/ADR-001.md`](docs/engineering/ADR-001.md) — the
  one-daemon/one-helper collapse.
- [`docs/engineering/M9-ACCEPTANCE.md`](docs/engineering/M9-ACCEPTANCE.md)
  — the machine-verification checklist.

```
cmd/skein                 CLI, TUI dashboard, and control-API client
cmd/skeind                thin daemon wrapper
cmd/skein-helper          fixed-verb elevated helper for the desktop
gui/                      Wails desktop app, services, frontend, packaging
internal/identity         X25519 keys, peer IDs, invites
internal/conf             TOML schema, defaults, validation
internal/wire             bounded frame codec
internal/session          Noise handshakes, epochs, replay windows
internal/link             UDP/TCP transport
internal/tun, internal/netcfg   TUN creation, addresses, routes, MTU
internal/peer, internal/path    peer table, candidate probing
internal/route            bounded distance-vector routing
internal/discovery        mDNS and authenticated state gossip
internal/dataplane        packet plane, forwarding, counters
internal/control          keepalive, state sync, rekey, probes
internal/api, internal/daemon   loopback API and composition
internal/app              daemon entry points shared by both binaries
internal/cli              CLI, API client, and dashboard
internal/firewall         automatic ufw/firewalld/iptables/Windows rules
internal/service          systemd and Windows service installation
internal/pipeconn         Windows named-pipe transport
internal/activity         bounded authenticated mesh telemetry
scripts/                  install-windows.ps1, firewall-linux.sh, CI gates
test/netns, test/windows  runtime harnesses
docs/                     protocol, measurements, API contract, ADRs
```
