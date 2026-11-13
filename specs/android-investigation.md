# Android reliability investigation

Branch: `android`, initial revision `c4b84a6`. Preserve the untracked `skein-helper` and existing device/network configuration. No commits or pushes requested.

## Scope and ownership

- Native: Android Activity, Wails/JNI initialization, VPN permission and foreground-service lifecycle, packaging.
- Transport: Android TUN FD ownership/read/write/cancel, socket protection, dual-stack candidates, firewall/netcfg integration.
- UI: every mobile route, forms, sheets, navigation, permission and connection state, accessibility and responsive behavior; preserve desktop behavior.
- Integration: in-process daemon/bootstrap/control transport, connection lifecycle, shared service adapters, full test/build/device verification.

Existing design and implementation history: `docs/superpowers/plans/2026-11-13-android-mobile*.md`. No `specs/` cockpit existed when the survey started. Lifecycle: bug investigation on the user's existing feature branch.

## Verification plan

1. Reproduce installed-app failures on the paired physical Samsung SM-M566B (Android 16/API 36); capture logcat and screenshots. No app-data reset.
2. Audit paths end-to-end and fix confirmed root causes with narrowly scoped regression tests.
3. Run root Go tests with race detection and frontend Vitest; build TypeScript/Vite and Android native/APK. Cross-build relevant Android Go packages and check desktop compilation where toolchain permits.
4. Install APK with `adb install -r` (preserve data), launch, grant VPN consent, verify real TUN and mesh peer traffic, disconnect/reconnect, background/resume, navigation/forms and Android Back.
5. Record exact observed results and limitations below. Update changelog and graphify once code is verified.

## Initial device evidence

- ADB mDNS discovery initially empty; user supplied wireless pairing details; device now connected.
- Installed app `com.skein.app`, versionCode 1, target API 36.
- Relaunching an existing process panics: `event 'connection:changed' is already registered with data type api.Connection` at `gui/app.go:23`, called via Wails Android `nativeInit` -> `main`.
- Force-stop plus clean launch reaches the configured-but-stopped service screen. Further verification in progress.

## Findings and final verification

### Confirmed fixes

- Serialize Android VPN start/stop/revoke operations; release unused TUN handoffs
  and make packet reads interruptible during teardown and reconnect.
- Protect underlay sockets, enumerate Android addresses through the native
  network API, and retain the configured MTU for a VPN generation.
- Initialize the Go application and typed events once per process rather than
  once per Activity; preserve Android Back and permission-state handling.
- Place mobile peers on one ellipse rather than overlapping direct/relay rings.
  Card geometry has one shared definition; complete IPv4 addresses remain
  visible. Map/List/Home/badges agree about reachable relayed peers, and a
  disconnected map clears links and marks remote reachability unknown.
- Use `/system/bin/ping` on Android: the embedded runtime had no PATH, so the
  inspector failed even while shell ping and the tunnel worked.
- Implement Wails' exact `ServiceStartup(context.Context, application.ServiceOptions)`
  interface. The old `any` parameter silently prevented the event subscription
  from starting. A compile-time interface assertion protects this contract.
  Inspectors and troubleshooting prefer completed live probe events over the
  initial pending acknowledgement.

### Observed verification

- `go test -race ./...`: 31 packages passed; 7 packages have no tests.
- `npm test`: 14 files, 118 tests passed. Mobile geometry covers zero through
  four remote peers, card bounds, eight-unit separation and stable ordering;
  the desktop fan remains covered.
- After subsequent action/lifecycle fixes, focused Ping tests and
  `go test -race ./gui/services` passed. TypeScript/Vite, Android ARM64 native
  compilation, release APK and debug QA APK all built successfully.
- Both installed applications upgraded with `adb install -r`; no app-data reset.
  QA used `com.skein.app.qa`, preserving the original application's network.
  The normal release APK is `gui/bin/skein-desktop.apk`.
- Physical SM-M566B: QA node `10.144.144.140` connected to `XD_OD`
  (`10.144.144.1`), with `AviraL` (`10.144.144.89`) reachable through that peer.
  IPv4 passed in both directions to the host; phone-to-Windows and
  phone-to-host IPv6 also passed with zero observed packet loss.
- A 1,252-byte ICMP payload (1,280-byte IPv4 packet) passed unfragmented to
  the phone. An earlier 1,318-byte payload exceeded the host's current TUN MTU
  and was rejected locally; it was not evidence of Android packet loss.
- A 4 MiB TCP stream crossed the overlay to Android with identical expected
  and received SHA-256 digests.
- The real inspector Ping action returned four replies, zero loss and
  reachable/exit 0. Probe completed as `UP`, 3.5 ms, via a live daemon event.
- Physical map: no overlapping card bounds, complete `10.144.144.140`,
  consistent “2 of 2 peers up,” working peer sheets and native Back dismissal.
  List mode agrees with the relayed route; disconnect clears all live links
  and green indicators; reconnect restores the mesh.
- Home, Peers, Activity, Invites and Settings navigated without horizontal
  overflow on the 384×832 CSS-pixel WebView. Sheet content and actions scroll
  inside the viewport.
- IPv6 underlay was a direct LAN path: phone and host shared a `/64`, and the
  host route used `wlan0` without a gateway. Probe also selected private IPv4
  `192.168.0.116`; the UI's address-family label does not classify LAN vs WAN.

### Verification limits

- Three physical mesh nodes were exercised. Four remote-card configurations
  are covered by geometry tests, not a five-device physical run.
- No emulator/tablet, alternate Android vendor, maximum system font scale or
  landscape rendering claim is made.
- No claim of sustained throughput or tuned latency: packet timings varied
  on the physical phone. The selected host path was tested at MTU 1280,
  not at the maximum configurable MTU.

### Final lifecycle check

- Backgrounding the app preserved VPN traffic: two host replies, zero loss.
- Recreating the Activity in the existing process produced a new WebView
  and a working connected map without duplicate-event registration failure.
  Post-recreation traffic also returned two replies with zero loss.
- The final physical screenshot is `/tmp/skein-map-final.png`.
  `graphify update .` completed after the last code change (AST-only).

### Android–AviraL direct IPv6 check

- Android knows AviraL's IPv6 candidates, but the direct challenge received
  no answer. Its selected route remained two hops through XD_OD.
- Android's Wi-Fi IPv6 table (`1047`) contains only the local `/64` and
  link-local routes; it has no default route. Lookups for AviraL and a
  public IPv6 resolver return `Network is unreachable`.
- The same failure persists with the VPN interface absent, so it is not
  an overlay route capturing this traffic. Local IPv6 to XD_OD remains
  possible because that endpoint is on the Wi-Fi subnet. The host has a
  separate wired IPv6 default route.
- During this diagnostic, Disconnect/Resume stalled after the new live-event
  subscription was enabled. A cold QA-app restart was needed to recover;
  data was preserved. This is an unresolved lifecycle regression, distinct
  from the phone's missing external IPv6 route.

### Transport and latency fixes

- The TCP fallback was sticky: `Switch.Send` preferred any live stream and
  ignored the `forced` flag that `readUDP` clears, so a peer that recovered
  UDP stayed on TCP. Measured on the real link: AviraL `tcp` 306–437 ms
  before, `udp` 24.7 ms after the daemon update.
- Path proofs (`SendControlTo`) now use `SendDirect` (UDP only): a challenge
  carried by an existing TCP stream measured the stream, not the candidate.
- A peer on TCP re-proves its candidates every `PathProbeInterval`, so a
  recovered UDP path is adopted instead of waiting for a reconnect.
- Displayed RTT is the median of the last five control samples; routing still
  consumes the raw measurement. Phone UI over 60 s: 4 distinct values
  (22/23/27/28 ms) instead of jumping between 6 and 49 ms.
- AviraL stayed reachable for 40 consecutive samples with zero state
  changes after the fix; the earlier down/relayed flapping did not recur.
- Remaining jitter is the underlay: host↔phone over Wi-Fi measured 3.4–56 ms
  with 0% loss (asymmetric, inbound worse). That is radio power save, which
  the WifiLock option would address and which was not enabled.
- The phone's Wi-Fi still has no IPv6 default route, so AviraL stays relayed
  through XD_OD while the phone is on that network. Neither the phone nor the
  host can reach AviraL's public IPv4 `49.36.139.72:11010` (TCP timeout,
  ICMP 100% loss), so the relay is required there rather than a defect.

### Optional Wi-Fi performance lock

- Settings → Connection (Android only) carries "Keep Wi-Fi radio at full
  performance", off by default. It persists in the service's
  SharedPreferences, applies to a running tunnel immediately, and is acquired
  only while the VPN is up (`WIFI_MODE_FULL_LOW_LATENCY` on API 29+, else
  `WIFI_MODE_FULL_HIGH_PERF`).
- Verified on the physical phone: the switch rendered unchecked on a fresh
  install, survived an APK upgrade, and logcat showed
  `high-performance Wi-Fi lock acquired` / `enabled` on, `disabled` off.
- The first attempt failed with
  `SecurityException: ... does not have android.permission.WAKE_LOCK`; the
  manifest now declares it. That is a normal permission, so there is no
  runtime prompt.
- **Measured effect: none observed.** Host→phone ICMP, 40 packets each,
  Wi-Fi, screen awake, lock held (`WifiLock{skein-latency type=4}` in
  `dumpsys wifi`):

  | Lock | avg | max | mdev |
  |---|---|---|---|
  | on | 41.1 ms | 123.8 ms | 43.9 |
  | off | 36.0 ms | 103.3 ms | 34.1 |
  | on | 45.8 ms | 128.9 ms | 43.2 |

  The lock is held correctly but the spikes did not drop, so on this network
  they are airtime contention and scheduling, not Wi-Fi power save. The
  setting stays available and off by default; it is not a latency fix.

### Launcher icon

- The APK shipped Wails' placeholder "W" in every `mipmap-*` density. All
  densities are now generated from `gui/build/appicon.png` (the teal Skein
  mark on the black plate), with a circular `ic_launcher_round`, an adaptive
  icon for API 26+ (`mipmap-anydpi-v26` + `drawable-nodpi` foreground), and
  `ic_launcher_background` added to `values/colors.xml`.
- Verified on the device: the drawer shows the teal woven mark labelled
  "Skein" (`/tmp/skein-icon-s2.png`).

### Switch / reset network hang

- Reproduced on the physical phone: tapping "Leave and reset" left the app on
  Settings indefinitely. The reset itself completed (backup written, config
  removed, TUN gone, VpnService stopped) — the screen never moved.
- Root causes, all three required for the stuck screen:
  1. `readServiceStatus` on Android returned "running" whenever the daemon
     object existed, even after the reset removed the configuration, so the
     shell never saw a device with no network.
  2. `setupOwnsWindow` exempted `/settings` unconditionally. That is right
     mid-setup, but after a reset there is nothing on Settings to recover, so
     the app stayed on a dead "Mesh paused" screen with no way forward.
  3. `ResetWithPurge` could hang on the in-process reset response. A hung
     Wails call froze every later request, so the shell could not even poll
     its state back to setup.
- Fixes: status is `missing` when `ConfigExists()` is false; setup owns the
  window when the service is missing and the config query fails
  (`setupOwnsWindow(..., configMissing)`); the reset is bounded (15 s) and a
  timeout with no configuration left counts as done; the service stop after a
  reset is bounded at 5 s and no longer holds the result.
- Verified after the fix: "Leave and reset" reaches onboarding in about one
  second, the config and TUN are gone, and a new network can be created
  immediately. `onboardingGate` covers the new case with a regression test.

### Settings validation and persistence

- The device-name error was not a bad name: `internal/android/host.go`
  erased `API.Listen`, `Socket` and `Pipe` after loading a valid config.
  Whole-config validation then rejected every settings patch, and the same
  invalid config prevented learned peers and membership changes from saving.
  Keep the loaded config intact; `devTCPEnabled` explicitly refuses Android,
  so preserving it does not expose a TCP API.
- Config writes now return errors to mutation callers. Publish config and
  the advertised name only after a successful write; failed rotation, leave
  and rejoin no longer change live membership while claiming persistence.
- Settings sends only changed fields; the daemon also recognizes no-op
  values. Renaming no longer reports a restart just because the form echoed
  port, MTU and mDNS. Mobile restart notices say to reconnect the VPN.
- STUN hostname validation agrees between file and patch inputs, with a
  numeric port in 1–65535. Clearing the server restores the default. Custom
  servers survive reload with STUN disabled. An empty public endpoint clears
  it instead of failing validation. Clearing the node name resolves its
  hostname default immediately rather than changing behavior after restart.
- Verification: race-enabled tests passed for `internal/conf`,
  `internal/daemon`, `internal/api` and `gui/services`; seven focused frontend
  tests and the TypeScript/Vite build passed.
- ARM64 Android host and daemon test binaries passed on the physical phone.
  The host regression saves name, port, MTU, mDNS and a custom STUN server
  through the real in-process HTTP API across three daemon generations.
  Daemon tests cover hostname/endpoint inputs, no-op patches, TCP exclusion,
  and failed writes for rename, rotation, leave, rejoin and peer addition.
- Release APK rebuilt at `gui/bin/skein-desktop.apk`; installed the QA build
  in place with `adb install -r`. The real settings form saved `XD_OD Phone`
  with `Applied: name`, `Live now; no restart needed.`, and no alert. Confirmed
  the saved file, unchanged identity/network secret and preserved peers.
  MTU 1200 produced the inline IPv6 minimum message without saving; discarded
  it and left the phone connected at MTU 1280. Screenshot:
  `/tmp/skein-settings-fixed.png`.
