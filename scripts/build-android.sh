#!/usr/bin/env bash
# Build the Skein Android release APK.
#
# Usage (on the Linux machine where the toolchain is installed):
#   ./scripts/build-android.sh
#
# Prerequisites (already set up on the owner's Linux machine):
#   - wails3 v3.1.0 at ~/go/bin/wails3
#   - Android SDK at ~/Android/Sdk
#   - NDK 30.0.16248370 at ~/Android/Sdk/ndk/30.0.16248370
#   - Java 21, Go 1.27.3
#
# Output: gui/bin/skein-desktop.apk (release APK, arm64)
# The APK is also copied to ~/skein-android-build.apk in the workspace
# when run through the agent; see the Phase 4 verification note below.
#
# Verification (Phase 4) still needs a physical phone:
#   - No device was connected via `adb` at build time.
#   - The emulator (Medium_Phone_API_37.0) exists but cannot properly
#     test a real VPN.
#   - Install on a real phone and check: app launches, onboarding works,
#     Connect shows the system VPN permission dialog, the TUN comes up,
#     a peer appears on the mesh map, Disconnect stops the VPN.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export ANDROID_HOME="${ANDROID_HOME:-$HOME/Android/Sdk}"
# Only pin the NDK when that exact version is installed: the Taskfile falls
# back to the newest NDK under $ANDROID_HOME/ndk, but an exported
# ANDROID_NDK_HOME pointing at a missing directory fails the build.
if [ -z "${ANDROID_NDK_HOME:-}" ] && [ -d "$ANDROID_HOME/ndk/30.0.16248370" ]; then
  export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/30.0.16248370"
fi
export PATH="$HOME/go/bin:$PATH"

echo "ANDROID_HOME=$ANDROID_HOME"
echo "ANDROID_NDK_HOME=${ANDROID_NDK_HOME:-<newest installed NDK>}"
wails3 doctor 2>&1 | tail -5 || true

cd "$REPO_ROOT/gui"
wails3 task android:package

APK="$REPO_ROOT/gui/bin/skein-desktop.apk"
if [ -f "$APK" ]; then
  echo "APK built: $APK ($(du -h "$APK" | cut -f1))"
  unzip -l "$APK" | grep -E "libwails|classes.dex|AndroidManifest" || true
else
  echo "ERROR: APK not found at $APK" >&2
  exit 1
fi
