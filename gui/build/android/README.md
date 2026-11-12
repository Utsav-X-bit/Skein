# Skein for Android

Skein as a native Android app, built with Wails 3. Same Go daemon and React frontend as the desktop app, adapted for mobile.

## What is in this build

- **Mobile GUI** — Home with the mesh map + Connect button, Peers, Activity, Invites, Settings, Onboarding, VPN permission flow. Bottom navigation, touch targets ≥ 48dp, safe areas.
- **Backend** — the Go daemon compiled to `libwails.so` via the NDK. TUN comes from Android's `VpnService` (`SkeinVpnService.java`): the service builds the TUN, passes its file descriptor to Go via JNI, and Go wraps it as a `tun.Device`. The firewall is handled automatically by the VpnService (routing through the TUN); `firewall_android.go` is a no-op that reports "managed by Android VPN service". A foreground service with a persistent notification keeps the VPN alive in the background.
- **Branch:** `android`. Plan: `docs/superpowers/plans/2026-11-13-android-mobile.md`.

## Rebuild

On the Linux machine where the toolchain is installed (Wails 3 v3.1.0, Android SDK, NDK 30.0.16248370, Java 21, Go 1.27.3):

```bash
./scripts/build-android.sh
```

Or directly:

```bash
export ANDROID_HOME=$HOME/Android/Sdk
export ANDROID_NDK_HOME=$ANDROID_HOME/ndk/30.0.16248370
export PATH=$HOME/go/bin:$PATH
cd gui
wails3 task android:package
```

Output: `gui/bin/skein-desktop.apk` (release APK, arm64, ~15 MB).

For a debug install on a connected phone:

```bash
wails3 task android:run:device
```

## Install

Copy `gui/bin/skein-desktop.apk` to the phone and install it (allow "Install unknown apps" for the file manager / browser you use), or:

```bash
adb install gui/bin/skein-desktop.apk
```

## Verify (Phase 4)

**Status: BLOCKED — needs a physical phone.** No device was connected via `adb` at build time, and the Android emulator cannot properly test a real VPN.

On a real phone, check:

1. App launches; onboarding works (create a network or join with an invite).
2. Tap Connect — the system VPN permission dialog appears; tap OK.
3. The TUN comes up; the mesh map shows your peer.
4. Disconnect stops the VPN.

## What was verified at build time

- `GOOS=android go build ./...` passes (Phases 0–3).
- Release APK builds with `wails3 task android:package` and contains `lib/arm64-v8a/libwails.so`, `classes.dex`, `classes2.dex`, `AndroidManifest.xml`.
- Desktop `wails3 build` still passes (Android work is isolated to `//go:build android` files and the mobile frontend).

## Notes

- Never push the `android` branch from here — the owner pushes it himself.
- The APK is signed with the debug keystore by default (installable for testing). A release keystore can be added later for distribution.
- Android OEMs (Xiaomi/Oppo/Vivo) aggressively kill background apps even with a foreground service; the persistent notification mitigates this but does not eliminate it on all OEMs.
