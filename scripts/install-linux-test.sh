#!/usr/bin/env bash
# install-linux-test.sh — behavioral regression for scripts/install-linux.sh.
#
# Unprivileged phase (always, no network, no system changes):
#   - actual preflight rejections: missing policy, unsafe policy, non-ELF
#     payload, unsupported distro/version
#
# Privileged phase (root + running systemd only, disposable container/VM):
#   - a running dummy skein.service survives a missing-payload rejection
#     (payload validation happens before the service is stopped)
#   - a fresh install writes root-owned files/modes, creates the skein
#     group, and does not register a service without a config
#   - an upgrade stops the dummy service before replacing binaries, keeps
#     /etc/skein, and fails closed when the helper does not report success
#
# The privileged phase installs and then removes /usr/local/bin/skeind,
# /usr/local/bin/skein, /usr/local/bin/skein-desktop,
# /usr/libexec/skein-helper, the polkit policy, desktop entry and icon. Run
# it only in a disposable environment; it refuses to touch an existing Skein
# install.

set -euo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
SCRIPT=$ROOT/scripts/install-linux.sh
TMP=$(mktemp -d "${TMPDIR:-/tmp}/skein-install-test.XXXXXX")
FAIL=0

cleanup_tmp() { rm -rf -- "$TMP"; }
trap cleanup_tmp EXIT

ok()   { printf 'ok: %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; FAIL=1; }

expect_fail() {
    local name=$1
    shift
    local out
    if out=$("$@" 2>&1); then
        printf '%s\n' "$out" >&2
        fail "$name: expected failure, got success"
        return 0
    fi
    ok "$name"
}

# A minimal checkout copy so payload files can be removed/changed without
# touching the real tree.
REPO=$TMP/repo
mkdir -p "$REPO/scripts" "$REPO/cmd/skeind" "$REPO/gui/build/linux/nfpm" "$REPO/gui/build"
printf 'module github.com/Utsav-X-bit/skein\n\ngo 1.26.10\n' > "$REPO/go.mod"
cp -- "$SCRIPT" "$REPO/scripts/install-linux.sh"
cp -- "$ROOT/gui/build/linux/nfpm/org.skein.helper.policy" "$REPO/gui/build/linux/nfpm/"
cp -- "$ROOT/gui/build/linux/skein-desktop.desktop" "$REPO/gui/build/linux/"
cp -- "$ROOT/gui/build/appicon.png" "$REPO/gui/build/"
chmod +x "$REPO/scripts/install-linux.sh"

# Real ELF placeholders: the installer validates the magic and installs
# them, but a fresh install never executes them.
TRUE_BIN=$(type -P true || true)
[ -n "$TRUE_BIN" ] && [ -x "$TRUE_BIN" ] || { echo "FAIL: no external true binary" >&2; exit 1; }
BIN=$TMP/bin
mkdir -p "$BIN"
for b in skeind skein-helper skein skein-desktop; do
    cp -- "$TRUE_BIN" "$BIN/$b"
    chmod 0755 "$BIN/$b"
done

# --- unprivileged phase ----------------------------------------------------
printf '== unprivileged installer behavior ==\n'


rm -f -- "$REPO/gui/build/linux/nfpm/org.skein.helper.policy"
expect_fail "missing policy rejected" \
    "$REPO/scripts/install-linux.sh" --binary-dir "$BIN"
cp -- "$ROOT/gui/build/linux/nfpm/org.skein.helper.policy" "$REPO/gui/build/linux/nfpm/"

sed -i 's|<allow_any>no</allow_any>|<allow_any>yes</allow_any>|' \
    "$REPO/gui/build/linux/nfpm/org.skein.helper.policy"
expect_fail "unsafe policy rejected" \
    "$REPO/scripts/install-linux.sh" --binary-dir "$BIN"
cp -- "$ROOT/gui/build/linux/nfpm/org.skein.helper.policy" "$REPO/gui/build/linux/nfpm/"

BADBIN=$TMP/badbin
mkdir -p "$BADBIN"
for b in skein-helper skein skein-desktop; do
    cp -- "$TRUE_BIN" "$BADBIN/$b"
done
printf 'this is not an ELF binary\n' > "$BADBIN/skeind"
expect_fail "non-ELF payload rejected" \
    "$REPO/scripts/install-linux.sh" --binary-dir "$BADBIN"

printf 'ID=voidlinux\nID_LIKE=\nVERSION_ID=\nPRETTY_NAME="Void Linux"\n' > "$TMP/os-void"
expect_fail "unsupported distro rejected" \
    env SKEIN_OS_RELEASE="$TMP/os-void" "$SCRIPT" --dry-run --binary-dir "$BIN"

printf 'ID=ubuntu\nID_LIKE=debian\nVERSION_ID="22.04"\nPRETTY_NAME="Ubuntu 22.04"\n' > "$TMP/os-old"
expect_fail "old Ubuntu rejected" \
    env SKEIN_OS_RELEASE="$TMP/os-old" "$SCRIPT" --dry-run --binary-dir "$BIN"

# --- privileged phase ------------------------------------------------------
if [ "$(id -u)" -ne 0 ]; then
    printf 'skip: privileged install phase requires root\n'
    exit "$FAIL"
fi
if [ ! -d /run/systemd/system ]; then
    printf 'skip: privileged install phase requires running systemd\n'
    exit "$FAIL"
fi
if [ -e /etc/skein/skein.toml ] || systemctl cat skein.service >/dev/null 2>&1; then
    printf 'skip: a real Skein install exists; refusing to disturb it\n'
    exit "$FAIL"
fi
for f in /usr/local/bin/skeind /usr/local/bin/skein \
    /usr/local/bin/skein-desktop /usr/libexec/skein-helper \
    /usr/share/polkit-1/actions/org.skein.helper.policy \
    /usr/share/applications/skein-desktop.desktop \
    /usr/share/icons/hicolor/128x128/apps/skein-desktop.png; do
    if [ -e "$f" ]; then
        printf 'skip: %s already exists; refusing to overwrite it\n' "$f"
        exit "$FAIL"
    fi
done

printf '== privileged installer behavior ==\n'

UNIT_CREATED=0
CONFIG_CREATED=0
GROUP_EXISTED=0
APPARMOR_EXISTED=0
DEST_FILES=(
    /usr/local/bin/skeind
    /usr/local/bin/skein
    /usr/local/bin/skein-desktop
    /usr/libexec/skein-helper
    /usr/share/polkit-1/actions/org.skein.helper.policy
    /usr/share/applications/skein-desktop.desktop
    /usr/share/icons/hicolor/128x128/apps/skein-desktop.png
)
getent group skein >/dev/null 2>&1 && GROUP_EXISTED=1
[ -e /etc/apparmor.d/skein-desktop ] && APPARMOR_EXISTED=1

cleanup_priv() {
    if [ "$UNIT_CREATED" -eq 1 ]; then
        systemctl stop skein.service >/dev/null 2>&1 || true
        systemctl disable skein.service >/dev/null 2>&1 || true
        rm -f -- /etc/systemd/system/skein.service
        systemctl daemon-reload >/dev/null 2>&1 || true
    fi
    for f in "${DEST_FILES[@]}"; do
        rm -f -- "$f"
    done
    if [ "$CONFIG_CREATED" -eq 1 ]; then
        rm -rf -- /etc/skein
    fi
    if [ "$GROUP_EXISTED" -eq 0 ]; then
        groupdel skein >/dev/null 2>&1 || true
    fi
    if [ "$APPARMOR_EXISTED" -eq 0 ] && [ -e /etc/apparmor.d/skein-desktop ]; then
        apparmor_parser -R /etc/apparmor.d/skein-desktop >/dev/null 2>&1 || true
        rm -f -- /etc/apparmor.d/skein-desktop
    fi
}
trap 'cleanup_priv; cleanup_tmp' EXIT

start_dummy_service() {
    cat > /etc/systemd/system/skein.service <<'EOF'
[Unit]
Description=Skein installer regression dummy
[Service]
ExecStart=/bin/sleep 300
[Install]
WantedBy=multi-user.target
EOF
    UNIT_CREATED=1
    systemctl daemon-reload
    systemctl start skein.service
    systemctl is-active --quiet skein.service
}

# 1. Missing payload must fail before the running service is stopped.
start_dummy_service || { fail "could not start dummy service"; exit "$FAIL"; }
rm -f -- "$REPO/gui/build/linux/nfpm/org.skein.helper.policy"
expect_fail "missing payload rejected before service stop" \
    "$REPO/scripts/install-linux.sh" --binary-dir "$BIN"
if systemctl is-active --quiet skein.service; then
    ok "running service was not stopped by a rejected payload"
else
    fail "rejected payload stopped the running service"
fi
cp -- "$ROOT/gui/build/linux/nfpm/org.skein.helper.policy" "$REPO/gui/build/linux/nfpm/"

systemctl stop skein.service
systemctl disable skein.service >/dev/null 2>&1 || true
rm -f -- /etc/systemd/system/skein.service
systemctl daemon-reload
UNIT_CREATED=0

# 2. Fresh install: real files, ownership, modes, group, no service.
if out=$("$REPO/scripts/install-linux.sh" --binary-dir "$BIN" 2>&1); then
    ok "fresh install completed"
else
    printf '%s\n' "$out" >&2
    fail "fresh install failed"
fi
for f in "${DEST_FILES[@]}"; do
    [ -e "$f" ] || { fail "missing installed file: $f"; continue; }
done
[ "$(stat -c '%u:%g %a' /usr/local/bin/skeind)" = "0:0 755" ] \
    && ok "skeind is root:root 0755" \
    || fail "skeind ownership/mode is wrong"
[ "$(stat -c '%u:%g %a' /usr/libexec/skein-helper)" = "0:0 755" ] \
    && ok "skein-helper is root:root 0755" \
    || fail "skein-helper ownership/mode is wrong"
[ "$(stat -c '%u:%g %a' /usr/share/polkit-1/actions/org.skein.helper.policy)" = "0:0 644" ] \
    && ok "polkit policy is root:root 0644" \
    || fail "polkit policy ownership/mode is wrong"
cmp -s -- "$BIN/skeind" /usr/local/bin/skeind \
    && ok "skeind payload was installed byte-for-byte" \
    || fail "skeind payload differs from the installed file"
getent group skein >/dev/null 2>&1 \
    && ok "skein group exists" \
    || fail "skein group was not created"
[ ! -e /etc/skein/skein.toml ] \
    && ok "fresh install created no config" \
    || fail "fresh install invented a config"
if systemctl cat skein.service >/dev/null 2>&1; then
    fail "fresh install registered a service without a config"
else
    ok "fresh install left the service unregistered"
fi

# 3. Upgrade: stop before replacement, keep /etc/skein, fail closed when the
#    helper does not report success (the test payload is /bin/true).
start_dummy_service || { fail "could not restart dummy service"; exit "$FAIL"; }
mkdir -p /etc/skein
printf '# installer regression: config must survive\n[node]\nname = "test"\n' > /etc/skein/skein.toml
CONFIG_CREATED=1
if out=$("$REPO/scripts/install-linux.sh" --binary-dir "$BIN" 2>&1); then
    fail "upgrade unexpectedly succeeded with a non-reporting helper"
else
    ok "upgrade failed closed when the helper reported no success"
fi
if systemctl is-active --quiet skein.service; then
    fail "upgrade did not stop the service before replacement"
else
    ok "upgrade stopped the service before replacement"
fi
cmp -s -- "$BIN/skeind" /usr/local/bin/skeind \
    && ok "upgrade replaced the daemon binary" \
    || fail "upgrade did not replace the daemon binary"
grep -qF 'config must survive' /etc/skein/skein.toml \
    && ok "upgrade kept /etc/skein/skein.toml" \
    || fail "upgrade lost the config"

if [ "$FAIL" -ne 0 ]; then
    printf 'FAILED\n' >&2
    exit 1
fi
printf 'PASS\n'
