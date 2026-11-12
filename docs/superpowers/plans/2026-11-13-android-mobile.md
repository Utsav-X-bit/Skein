# Skein for Android — Implementation Plan

**Branch:** `android` (worktree `~/workspace/skein-android`, off `origin/main` @ `6584b38`)
**Date:** 2026-11-13
**Status:** Plan — not yet implemented. User asked for the plan first, before any code.

---

## 1. Goal

Ship Skein as a complete Android app: a beautiful, mobile-first GUI with full feature parity to the desktop app, a working mesh VPN backend on Android, and automatic firewall/routing handling. Deliverable is a tested, verified APK plus its build scripts.

**Non-goals for v1:**
- iOS (Android only).
- Play Store submission (APK for sideload/testing; AAB later).
- Per-peer invite revocation (F-01, deferred post-M9 — same as desktop).

---

## 2. Tech stack — what we use and why

| Layer | Choice | Why this is best |
|---|---|---|
| App framework | **Wails 3 (v3.1.0)** | Already the desktop framework. Wails v3 supports Android natively: same `main.go` builds for desktop and Android, Go compiles to `libwails.so` via the NDK, Android WebView renders the frontend. No rewrite, no second codebase. The user already installed Wails 3 + Android Studio + NDK 30 on his Linux machine (`wails3 doctor` runs clean). |
| Frontend | **React + TypeScript + Vite + Tailwind CSS v4 + Radix UI** | The desktop frontend already uses this stack (`gui/frontend/`). Reuse the components, services bindings, and design tokens. Mobile gets its own layout (bottom nav, touch targets, safe areas) on top of the same routes and services — not a rewrite. |
| Backend | **Go (same daemon)** | The daemon (`internal/daemon/`, `internal/dataplane/`, `internal/link/`, `internal/control/`) is platform-independent Go. It compiles for `GOOS=android` via the NDK. Android-specific behaviour lives in files guarded by `//go:build android` (Wails rule: `android` implies the `linux` build tag, so desktop-Linux-only files must use `linux && !android`). |
| Android host | **Wails-generated Gradle project + small Java/Kotlin host** | Wails generates `build/android/` (Gradle + `MainActivity` + JNI bridge). We add a `SkeinVpnService` (extends Android `VpnService`) and a foreground-service notification there. No separate Android Studio project to maintain. |
| TUN on Android | **Android `VpnService`** | Android does not allow `/dev/net/tun` from an app (needs root). `VpnService.Builder.establish()` returns a TUN file descriptor. Go receives the FD via JNI and wraps it as a `tun.Device`. This is the only supported way to do a VPN on unrooted Android, and it is also how the firewall is handled (see §4). |
| Firewall on Android | **VpnService routing (automatic)** | Android has no ufw/iptables for apps. The VpnService itself is the firewall: when the VPN is active, the system routes traffic through the TUN. Our `firewall_android.go` is a no-op that reports "managed by VpnService" — the user asked for the firewall to be "automatically handled", and on Android that means the VpnService configuration (addresses, routes, DNS, allowed apps) does it, not a rule engine. |
| Background operation | **Foreground service + persistent notification** | Android kills background apps. A foreground service (with a "Skein VPN is active" notification) keeps the daemon and the TUN alive when the app is backgrounded. The `VpnService` itself can be the foreground service. |
| Build | **`wails3 task android:package` (release APK)** | Wails task drives Gradle + NDK. Release APK is signed with the debug keystore by default (installable for testing); a release keystore can be added later for distribution. `wails3 task android:run:device` installs a debug APK on a connected phone for testing. |

**Rejected alternatives:**
- **Flutter / React Native:** would require rewriting the frontend and re-implementing the Go backend bridge. Wails reuses both.
- **Native Kotlin UI:** same rewrite cost, and loses the shared React components and the desktop design system.
- **Root-based TUN (`/dev/net/tun`):** requires a rooted phone. Unacceptable for a product.

---

## 3. How the backend is ported for Android

The daemon's core (Noise, dataplane, control, link, mesh) is pure Go and compiles unchanged for Android. Only the platform edges need Android-specific implementations. Each edge is a small, build-tagged file — no changes to the shared core.

### 3.1 TUN — `internal/tun/tun_android.go` (new, `//go:build android`)

**Problem:** `tun.Open()` calls `wgtun.CreateTUN(name, mtu)`, which opens `/dev/net/tun`. That fails on unrooted Android.

**Solution:**
- Java/Kotlin `SkeinVpnService` (in `build/android/`) builds the TUN with `VpnService.Builder`:
  - `addAddress(overlayIPv4, 24)` and `addAddress(overlayIPv6, 64)` (from the daemon config — the same overlay addresses the desktop uses).
  - `setMtu(mtu)` (from the negotiated MTU, `internal/daemon/mtu.go`).
  - `addRoute("10.144.144.0", 24)` and the IPv6 overlay prefix (so overlay traffic goes through the TUN).
  - `addDnsServer(...)` (optional; keep the system DNS unless the mesh needs otherwise).
  - `setSession("Skein")`, `allowBypass()` for the daemon's own underlay sockets (so Skein's UDP to the peer does NOT go through the TUN — this is critical, otherwise the tunnel routes its own traffic into itself).
- `establish()` returns a `ParcelFileDescriptor`. Its FD (an `int`) is passed to Go via JNI (`SkeinVpnService.getTunFd()` → Go `tun.SetAndroidFd(fd)`).
- Go wraps the FD in a `tun.Device` implementation (`tun_android.go`): `Read`/`Write` on the FD, `Name()` returns `"skein0"`, `Close()` closes the FD. The dataplane (`internal/dataplane/engine.go`) reads/writes this device exactly as it does on Linux — no changes to the dataplane.
- **MTU:** the existing automatic MTU negotiation (`internal/daemon/mtu.go`, `668bc19`) runs unchanged; the negotiated MTU is passed to the `VpnService.Builder` before `establish()`.

### 3.2 Firewall — `internal/firewall/firewall_android.go` (new, `//go:build android`)

**Problem:** `firewall_linux.go` shells out to `ufw`; `firewall_windows.go` shells out to `netsh`. Neither exists on Android.

**Solution:**
- `firewall_android.go` implements the same `firewall` package interface (`ensure`, `remove`, `status`, `repair`, `Manager`, `expectedRules`) but every operation is a no-op that succeeds.
- `status()` returns `State: "managed"` with a note that routing is handled by the Android VpnService. The GUI's Diagnostics / Settings screen shows "Firewall: managed by Android VPN service" instead of ufw/netsh rules.
- The actual "firewall" behaviour — which traffic goes through the TUN — is configured in the `VpnService.Builder` (`addRoute` for the overlay prefix, `allowBypass` for the daemon's own sockets, `addDisallowedApplication` if the user ever wants to exclude an app). This is automatic: the user does nothing, the VpnService handles it when the VPN starts.

### 3.3 Daemon lifecycle on Android

**Problem:** on desktop the daemon is a privileged system service (root/SYSTEM) with a control API on a Unix socket / named pipe, and the GUI is an unprivileged client. On Android there is no system service and no root.

**Solution:**
- The daemon runs **in-process** inside the Android app (inside `libwails.so`, loaded by the Wails host). It is started when the app starts the VPN (after the user grants VPN permission) and stopped when the VPN is disconnected.
- The GUI services layer (`gui/services/`) currently talks to the daemon over the control socket (`/run/skein/control.sock` / named pipe). On Android, the socket path does not exist. Two options, in order of preference:
  1. **In-process control API (preferred):** add an `//go:build android` variant of the services client that calls the daemon's control handlers directly in-process (same Go package, no socket). This keeps the frontend bindings unchanged — the frontend still calls `ConnectionService`, `PeerService`, etc., and the Go side routes to the in-process daemon instead of the socket.
  2. **Abstract Unix socket:** if (1) proves too invasive, use an Android abstract socket (`@skein-control`) or a socket in the app's private files dir. This keeps the socket protocol but changes the path. Only if (1) is blocked.
- **Roles / IPC auth:** on Android the GUI and the daemon are the same process and the same UID, so the socket-based role check (`none < read < mutate < secret`) does not apply the same way. The in-process path bypasses the transport check (the caller is the app itself). The daemon still enforces that only the app can call it (no external socket is exposed).
- **Config storage:** on Android the daemon config lives in the app's private files dir (`Context.getFilesDir()`), not `/etc/skein`. The configstore path is overridden via an Android-specific `SKEIN_STATE_FILE` / config-dir env or a build-tagged `configDir()`.

### 3.4 VPN permission and foreground service

- **Permission flow (mandatory Android UX):** the first time the user taps "Connect", the app calls `VpnService.prepare(context)`. If it returns an Intent, the app launches the system VPN consent dialog ("Skein wants to set up a VPN connection"). Only after the user taps "OK" does the app call `startService` for the `SkeinVpnService`. This is a system dialog — it cannot be skipped or faked.
- **Foreground service:** `SkeinVpnService` calls `startForeground()` with a persistent notification ("Skein VPN — Connected to <network>", with peer count and a Disconnect action). This keeps the process alive in the background. The notification is required by Android; without it the system kills the VPN.
- **Permissions in `AndroidManifest.xml`:** `BIND_VPN_SERVICE`, `FOREGROUND_SERVICE`, `FOREGROUND_SERVICE_SPECIAL_USE`, `INTERNET`, `ACCESS_NETWORK_STATE`, `POST_NOTIFICATIONS` (for the foreground notification on Android 13+).

### 3.5 What stays unchanged

- **Noise / dataplane / control / link / mesh:** no changes. They are pure Go and already compile for Android (`android` implies `linux` build tag — verify each `linux`-tagged file compiles for Android, and guard desktop-Linux-only files with `linux && !android`).
- **The control API surface:** the same endpoints (`/api/v1/status`, `/peers`, `/routes`, `/activities`, `/invites`, `/system/*`, `/firewall`, etc.). The frontend calls the same `gui/services` bindings.
- **The 5-node limit, equal peers, anyone-can-invite, no-peer-removes-another, tombstone leave:** all enforced by the daemon, unchanged.

---

## 4. Mobile GUI design

**Design language (from the desktop GUI redesign prompt, adapted for mobile):**
- Dark theme first, mesh/network aesthetic, calm and precise. No decoration that competes with the data.
- The mesh map is the home screen, but adapted for a phone: a compact topology (this node centred, peers around it, links with RTT + direct/relay) that fits a portrait screen, with tap-to-expand peer detail. The full desktop map (with animated flow on every link) is too dense for mobile; the mobile map shows the same data with fewer simultaneous animations and a list fallback.
- Every feature reachable in ≤ 2 taps. No dead ends: every state (daemon down, no network, paused, departed, no peers, VPN permission not granted) has a clear next action as a **button**, never as a paragraph.
- Touch targets ≥ 48dp. Bottom navigation (thumb-reachable). Safe areas respected (notch / gesture bar). `prefers-reduced-motion` honoured.

**Screens (bottom nav):**
1. **Home (mesh map + status):** connection status (big, plain-language: "Connected to My-Net — 2 peers, 15 ms"), the compact mesh map, a "what just happened" strip, and the Connect/Disconnect button. The Connect button triggers the VPN permission flow on first use.
2. **Peers:** live list with presence (active / idle / offline), direct/relay badge, RTT, live up/down with direction arrows. Tap a peer → detail sheet (endpoints, last handshake, bytes, routes, ping/probe actions). No "remove peer" (by design).
3. **Activity:** human-readable feed (peer joined/left, handshake, route learned, invite used, secret rotated) + a live throughput chart (last 60 samples).
4. **Invites:** create a network invite / peer invite, copy to clipboard, share via Android share sheet, expiry shown, list active invites.
5. **Settings:** Node (name, port, MTU), Service (start/stop/restart — on Android these map to the VpnService start/stop), Network switching wizard (leave → reset/purge → create or join), Security (rotate secret, roles), Diagnostics (logs, firewall status = "managed by Android VPN service", diagnostics bundle export), About (version, update check).

**Onboarding (first run):** create a network or join with an invite, in the same design language. The VPN permission dialog appears when the user first connects (system dialog, not ours).

**Mobile-specific UX:**
- **Connect is the primary action.** A large, thumb-reachable Connect/Disconnect button on Home. The first tap explains the VPN permission dialog before the system shows it (so the user is not surprised).
- **Notifications:** the foreground-service notification shows connection state; in-app toasts carry actions ("Peer left — View").
- **Offline / background:** the app keeps working when backgrounded (foreground service). Reopening the app shows the live state, not a stale screen.
- **Empty states:** no peers → "Invite your first peer" with the invite action inline. No network → the onboarding choice. VPN permission denied → a screen that explains how to re-enable it in Settings, with a button that opens the system VPN settings.

---

## 5. Phases and order

| Phase | What | Exit criteria |
|---|---|---|
| **0 — Explore + set up** | Verify toolchain on the user's Linux machine (`wails3 doctor`, Android SDK/NDK, `GOOS=android go build ./...` smoke test). Initialise the Android project (`wails3 task android:init` or the equivalent) so `build/android/` exists. Confirm the desktop GUI still builds. | `wails3 doctor` clean; `build/android/` exists; desktop `wails3 build` still passes. |
| **1 — Design** | Mobile design doc (this plan + a screen-by-screen brief). Present to the user, get agreement on the home-screen map treatment and the bottom-nav structure before building. | User approves the design. |
| **2 — Backend port** | `tun_android.go` (VpnService FD wrapper), `firewall_android.go` (no-op + "managed by VpnService"), daemon in-process control API (Android variant of the services client), config-dir override, `SkeinVpnService` (Java/Kotlin) + foreground notification + `AndroidManifest.xml` permissions. `GOOS=android go build ./...` passes. | `GOOS=android go build ./...` passes; `go vet` passes; existing tests pass on desktop. |
| **3 — Mobile GUI** | Mobile layout (bottom nav, safe areas, touch targets), Home mesh map (mobile), Peers / Activity / Invites / Settings (mobile), Onboarding, VPN permission flow, empty states, toasts. Reuse desktop services bindings; regenerate bindings after any Go service change. | Frontend `npm run build` passes; `vitest` passes; GUI renders in the Android WebView. |
| **4 — Build + verify** | `wails3 task android:package` (release APK, arm64). Install on a connected phone or emulator (`android:run:device` / `android:deploy-device`). Verify: app launches, onboarding works, VPN permission dialog appears, Connect establishes the TUN, a peer can join, the mesh map shows the peer, disconnect stops the VPN. Record each as VERIFIED / PARTIALLY VERIFIED / BLOCKED / FAILED. | APK installs and launches; Connect → TUN up → peer visible on the map; Disconnect → VPN stops. Each step recorded with evidence. |
| **5 — Deliver** | Final APK + build scripts (`scripts/build-android.sh`, Taskfile android tasks) + a short README (how to rebuild, how to install, what was verified). Commit on the `android` branch. **Never push** — the owner pushes himself. | APK + scripts + README on the `android` branch; verification log attached. |

---

## 6. Risks and open questions

1. **VpnService FD → Go:** passing a ParcelFileDescriptor FD to Go via JNI is the critical integration. If the FD cannot be used as a TUN device from Go (e.g. the WireGuard `tun` package rejects it), the fallback is to implement a minimal `tun.Device` that reads/writes the FD directly with `syscall.Read`/`Write` (no WireGuard dependency for the Android path).
2. **In-process control API vs socket:** the preferred in-process path (option 1 in §3.3) touches the services layer. If it proves too invasive, fall back to an abstract socket. Decide during Phase 2, do not block Phase 1 on it.
3. **Background kill:** Android OEMs (especially Xiaomi/Oppo/Vivo) aggressively kill background apps even with a foreground service. Document this as a known limitation; the foreground notification mitigates it but does not eliminate it on all OEMs.
4. **Emulator vs real device:** the Android emulator cannot easily test a real VPN (the emulator's own networking interferes). Verification should be on a physical phone where possible; the emulator is a fallback for UI-only checks.
5. **The desktop GUI redesign is still in progress on main.** The mobile GUI reuses the desktop routes/components, so if main moves, the `android` branch will need a rebase. Keep the mobile changes isolated (mobile layout + Android backend files) so rebasing is cheap.

---

## 7. Deliverables

- The `android` branch with: mobile GUI, Android backend port (`tun_android.go`, `firewall_android.go`, in-process control API, `SkeinVpnService`, manifest permissions), build scripts, and a README.
- A release APK (`bin/Skein.apk` or equivalent), built with `wails3 task android:package` on the user's Linux machine, installed and verified on a real phone or emulator.
- A verification log (each Phase 4 check as VERIFIED / PARTIALLY VERIFIED / BLOCKED / FAILED, with evidence).
- No push — the owner pushes the branch himself.
