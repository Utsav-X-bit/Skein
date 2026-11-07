#!/usr/bin/env bash
# install-linux.sh — one-command Skein install from a checkout or standalone.
#
# Arch / Debian 13+ / Ubuntu 24.04+ / Fedora 40+ / RHEL 10+ (systemd,
# GTK4 + WebKitGTK 6.0). Run as your normal user; sudo handles root steps.
# As root, build/clone as $SUDO_USER or --build-user, never as root.
#
# No source tree beside the script: clone the official repository at
# --source-ref (default main) into a private temp dir and build that checkout.
# --binary-dir installs prebuilt binaries and still installs runtime packages.
#
# Fresh install writes files/policy/group only. The desktop create/join
# bootstrap registers the service; this script never invents config or tokens.
# Upgrade stops the service before replacing binaries, then runs
# skein-helper install and requires running+enabled, keeping /etc/skein.
# The daemon applies firewall rules when a network connects.

set -euo pipefail

SRC_URL=https://github.com/Utsav-X-bit/Skein
SRC_REF=${SKEIN_SOURCE_REF:-main}
HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
ROOT=$(cd -- "$HERE/.." && pwd)
OS_RELEASE=${SKEIN_OS_RELEASE:-/etc/os-release}
EUID_NOW=$(id -u)

BIN_DIR="" SRC_DIR="" USER_RUN="" USER_HOME="" TMP=""
BUILD_PATH="" BUILD_USER_ARG="" DRY=0 BUILD=0 CLONE=0

log()  { printf '==> %s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }
cleanup() { [ -z "$TMP" ] || rm -rf -- "$TMP"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

usage() {
    cat <<'EOF'
Usage: install-linux.sh [--binary-dir DIR] [--source-dir DIR]
                        [--source-ref REF] [--build-user USER] [--dry-run]

  --binary-dir DIR   Install four prebuilt binaries from DIR; no build.
  --source-dir DIR   Use this Skein checkout instead of the tree beside
                     the script.
  --source-ref REF   Branch, tag, or full commit SHA to clone (default:
                     main, or $SKEIN_SOURCE_REF). No prebuilt release is used.
  --build-user USER  As root: clone/build as USER (default: $SUDO_USER).
  --dry-run          Print the detected plan; change nothing.

Supported: Arch-family, Debian 13+/Ubuntu 24.04+, Fedora 40+/RHEL 10+,
with GTK4 + WebKitGTK 6.0. systemd, polkit, acl and iproute2 are required.
On a fresh install the service is registered by the desktop's create/join
bootstrap. On upgrade it is re-registered and started; /etc/skein is kept.
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --binary-dir|--source-dir|--source-ref|--build-user)
            [ $# -ge 2 ] || die "$1 needs a value"
            case "$1" in
                --binary-dir) BIN_DIR=$2 ;;
                --source-dir) SRC_DIR=$2 ;;
                --source-ref) SRC_REF=$2 ;;
                --build-user) BUILD_USER_ARG=$2 ;;
            esac
            shift 2
            ;;
        --dry-run) DRY=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) die "unknown argument: $1 (try --help)" ;;
    esac
done

# --- source tree -----------------------------------------------------------
source_ok() { [ -f "$1/go.mod" ] && [ -d "$1/gui" ] && [ -f "$1/gui/build/appicon.png" ]; }
if [ -n "$SRC_DIR" ]; then
    [ -d "$SRC_DIR" ] || die "source directory not found: $SRC_DIR"
    ROOT=$(cd -- "$SRC_DIR" && pwd)
    source_ok "$ROOT" || die "$ROOT is not a Skein checkout"
fi
if ! source_ok "$ROOT"; then CLONE=1; fi

if [ -n "$BIN_DIR" ]; then
    [ -d "$BIN_DIR" ] || die "binary directory not found: $BIN_DIR"
    BIN_DIR=$(cd -- "$BIN_DIR" && pwd)
    BUILD=0
else
    BUILD=1
fi

# --- invoking user ---------------------------------------------------------
if [ "$EUID_NOW" -eq 0 ]; then
    if [ -n "$BUILD_USER_ARG" ]; then
        USER_RUN=$BUILD_USER_ARG
    elif [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != root ]; then
        USER_RUN=$SUDO_USER
    fi
else
    USER_RUN=$(id -un)
    if [ -n "$BUILD_USER_ARG" ] && [ "$BUILD_USER_ARG" != "$USER_RUN" ]; then
        die "--build-user is only valid when running as root"
    fi
fi

need_user() {
    [ -n "$USER_RUN" ] || die "run as your normal user, or pass --build-user USER as root"
    id -u "$USER_RUN" >/dev/null 2>&1 || die "user not found: $USER_RUN"
    [ "$(id -u "$USER_RUN")" -ne 0 ] || die "refusing to build or clone as root"
    USER_HOME=$(getent passwd "$USER_RUN" | cut -d: -f6)
    [ -n "$USER_HOME" ] && [ -d "$USER_HOME" ] || die "no home directory for $USER_RUN"
}

tmpdir() {
    if [ -n "$TMP" ]; then return 0; fi
    TMP=$(mktemp -d "${TMPDIR:-/tmp}/skein-install.XXXXXXXX") || die "mktemp failed"
    if [ "$EUID_NOW" -eq 0 ]; then
        chown "$USER_RUN":"$(id -gn "$USER_RUN")" "$TMP" || die "could not hand $TMP to $USER_RUN"
    fi
}

as_user() {
    if [ "$EUID_NOW" -ne 0 ]; then
        "$@"
    elif command -v sudo >/dev/null 2>&1; then
        sudo -u "$USER_RUN" -H -- "$@"
    else
        runuser -u "$USER_RUN" -- "$@"
    fi
}

run_build() {
    local dir=$1
    shift
    as_user env PATH="$BUILD_PATH" HOME="$USER_HOME" GOBIN="$TMP/tools/bin" \
        GOTOOLCHAIN=auto GOWORK=off GOFLAGS= npm_config_cache="$TMP/npm" \
        bash -c 'cd "$1" || exit 1; shift; exec "$@"' _ "$dir" "$@"
}

run_root() {
    if [ "$EUID_NOW" -eq 0 ]; then "$@"; else sudo -- "$@"; fi
}

# --- distro ----------------------------------------------------------------
field() {
    awk -v k="$1" 'index($0, k "=") == 1 {
        v = substr($0, length(k) + 2)
        gsub(/^"|"$/, "", v)
        print v
        exit
    }' "$OS_RELEASE"
}
[ -r "$OS_RELEASE" ] || die "cannot read $OS_RELEASE"
ID=$(field ID)
LIKE=$(field ID_LIKE)
VER=$(field VERSION_ID)
PRETTY=$(field PRETTY_NAME)
[ -n "$PRETTY" ] || PRETTY=${ID:-unknown}

TOKENS=" $ID $LIKE "
case "$TOKENS" in
    *" arch "*|*" archlinux "*|*" manjaro "*|*" endeavouros "*|*" cachyos "*|*" garuda "*) FAM=arch ;;
    *" debian "*|*" ubuntu "*|*" linuxmint "*|*" pop "*|*" elementary "*|*" zorin "*|*" raspbian "*) FAM=apt ;;
    *" fedora "*|*" rhel "*|*" centos "*|*" rocky "*|*" almalinux "*|*" nobara "*|*" bazzite "*) FAM=dnf ;;
    *) die "unsupported distribution: $PRETTY (need Arch, Debian/Ubuntu, or Fedora/RHEL family)" ;;
esac

ge() { [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n 1)" = "$2" ]; }
if [ -n "$VER" ]; then
    case "$ID" in
        debian) ge "$VER" 13 || die "Debian $VER is unsupported (need 13+)" ;;
        ubuntu) ge "$VER" 24.04 || die "Ubuntu $VER is unsupported (need 24.04+)" ;;
        fedora) ge "$VER" 40 || die "Fedora $VER is unsupported (need 40+)" ;;
        rhel|centos|rocky|almalinux)
            [ "${VER%%.*}" -ge 10 ] 2>/dev/null || die "$PRETTY is unsupported (need RHEL 10+)" ;;
    esac
fi

case "$FAM" in
    arch)
        PM=pacman
        RT=(gtk4 webkitgtk-6.0 acl polkit iproute2)
        BT=(git go nodejs npm gcc pkgconf curl ca-certificates xz)
        GTK=gtk4 WEB=webkitgtk-6.0
        ;;
    apt)
        PM=apt-get
        RT=(libgtk-4-1 libwebkitgtk-6.0-4 acl polkitd pkexec iproute2)
        BT=(git golang-go build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev nodejs npm curl ca-certificates xz-utils)
        GTK=libgtk-4-1 WEB=libwebkitgtk-6.0-4
        ;;
    dnf)
        PM=dnf
        RT=(gtk4 webkitgtk6.0 acl polkit iproute)
        BT=(git golang gcc pkgconf-pkg-config gtk4-devel webkitgtk6.0-devel nodejs npm curl ca-certificates xz)
        GTK=gtk4 WEB=webkitgtk6.0
        ;;
esac
command -v "$PM" >/dev/null 2>&1 || die "$PRETTY detected but $PM is not installed"

PKGS=("${RT[@]}")
if [ "$BUILD" -eq 1 ]; then
    PKGS+=("${BT[@]}")
elif [ "$CLONE" -eq 1 ]; then
    PKGS+=(git ca-certificates)   # standalone + --binary-dir still needs the payload checkout
fi

# --- validation ------------------------------------------------------------
validate_payload() {
    local p="$ROOT/gui/build/linux/nfpm/org.skein.helper.policy"
    local d="$ROOT/gui/build/linux/skein-desktop.desktop"
    local i="$ROOT/gui/build/appicon.png" f
    for f in "$p" "$d" "$i"; do
        [ -f "$f" ] && [ ! -L "$f" ] || die "missing payload: $f"
    done
    [ "$(grep -c '<action id=' "$p")" -eq 1 ] \
        && grep -q 'id="org.skein.helper.control"' "$p" \
        && grep -q '<allow_any>no</allow_any>' "$p" \
        && grep -q '<allow_inactive>no</allow_inactive>' "$p" \
        && grep -q '<allow_active>auth_admin</allow_active>' "$p" \
        && grep -q 'exec.path">/usr/libexec/skein-helper<' "$p" \
        && ! grep -q 'allow_gui' "$p" \
        || die "unsafe or malformed polkit policy: $p"
    grep -q '^Exec=skein-desktop$' "$d" && grep -q '^Icon=skein-desktop$' "$d" \
        || die "desktop entry must exec/icon skein-desktop"
    [ "$(head -c 8 "$i" | od -An -tx1 | tr -d ' \n')" = 89504e470d0a1a0a ] \
        || die "icon is not a PNG: $i"
}

validate_bins() {
    local f
    for f in skeind skein-helper skein skein-desktop; do
        [ -f "$1/$f" ] && [ ! -L "$1/$f" ] && [ -s "$1/$f" ] \
            || die "missing binary: $1/$f"
        [ "$(head -c 4 "$1/$f" | od -An -tx1 | tr -d ' \n')" = 7f454c46 ] \
            || die "not an ELF binary: $1/$f"
    done
}

secure_dir() {
    local m
    [ -d "$1" ] && [ ! -L "$1" ] && [ "$(stat -c %u "$1")" = 0 ] \
        || die "unsafe privileged directory: $1"
    m=$(stat -c %a "$1")
    (( (8#$m & 8#022) == 0 )) || die "group/other-writable directory: $1"
}

secure_dest() {
    local d f
    for d in /usr/local /usr/local/bin /usr/libexec /usr/share \
        /usr/share/polkit-1/actions /usr/share/applications \
        /usr/share/icons/hicolor /usr/share/icons/hicolor/128x128/apps \
        /etc/systemd /etc/systemd/system; do
        if [ -e "$d" ]; then secure_dir "$d"; fi
    done
    for f in /usr/local/bin/skeind /usr/local/bin/skein \
        /usr/local/bin/skein-desktop /usr/libexec/skein-helper \
        /usr/share/polkit-1/actions/org.skein.helper.policy \
        /usr/share/applications/skein-desktop.desktop \
        /usr/share/icons/hicolor/128x128/apps/skein-desktop.png; do
        [ ! -L "$f" ] || die "refusing to replace symlink: $f"
    done
}

desktop_running() {
    local processes status
    if command -v pgrep >/dev/null 2>&1; then
        if pgrep -x skein-desktop >/dev/null 2>&1; then
            die "Skein Desktop is running; quit it from its tray menu and re-run"
        else
            status=$?
            [ "$status" -eq 1 ] || die "cannot inspect desktop processes; refusing to continue"
        fi
    elif command -v ps >/dev/null 2>&1; then
        processes=$(ps -eo comm=) || die "cannot inspect desktop processes; refusing to continue"
        if grep -qx '[[:space:]]*skein-desktop[[:space:]]*' <<<"$processes"; then
            die "Skein Desktop is running; quit it from its tray menu and re-run"
        fi
    else
        die "cannot check whether Skein Desktop is running; refusing to continue"
    fi
}

# --- dependencies / build --------------------------------------------------
pkg_ok() {
    case "$FAM" in
        apt) run_root apt-cache show "$1" 2>/dev/null | grep "^Package: $1$" >/dev/null \
                || run_root dpkg -s "$1" >/dev/null 2>&1 ;;
        dnf) run_root dnf -q info "$1" >/dev/null 2>&1 ;;
        arch) run_root pacman -Q "$1" >/dev/null 2>&1 \
                || run_root pacman -Si "$1" >/dev/null 2>&1 ;;
    esac
}

install_deps() {
    local p
    if [ "$FAM" = apt ]; then
        run_root apt-get update || die "apt-get update failed"
    fi
    for p in "$GTK" "$WEB"; do
        pkg_ok "$p" || die "$PRETTY does not provide $p; GTK4 + WebKitGTK 6.0 is required (Debian 13+/Ubuntu 24.04+/Fedora 40+/RHEL 10+/Arch). No security settings were changed."
    done
    log "Installing packages: ${PKGS[*]}"
    case "$FAM" in
        arch) run_root pacman -S --needed --noconfirm "${PKGS[@]}" || die "pacman failed" ;;
        apt) run_root env DEBIAN_FRONTEND=noninteractive \
                apt-get install -y --no-install-recommends "${PKGS[@]}" || die "apt-get install failed" ;;
        dnf) run_root dnf install -y "${PKGS[@]}" || die "dnf install failed" ;;
    esac
}

verify_runtime() {
    local c ld libs
    for c in pkexec setfacl ip; do
        command -v "$c" >/dev/null 2>&1 || [ -x "/usr/sbin/$c" ] || [ -x "/sbin/$c" ] \
            || die "runtime dependency missing: $c"
    done
    ld=$(command -v ldconfig || true)
    [ -n "$ld" ] || ld=/sbin/ldconfig
    libs=$("$ld" -p 2>/dev/null) || die "cannot inspect runtime libraries"
    grep -q 'libgtk-4\.so\.1' <<<"$libs" || die "GTK4 runtime missing"
    grep -q 'libwebkitgtk-6\.0\.so\.4' <<<"$libs" || die "WebKitGTK 6.0 runtime missing"
}

clone_source() {
    need_user
    tmpdir
    ROOT="$TMP/src"
    log "Cloning $SRC_URL at $SRC_REF..."
    if [[ "$SRC_REF" =~ ^[0-9a-fA-F]{40}$ ]]; then
        as_user mkdir -p "$ROOT" \
            && as_user git -C "$ROOT" init -q \
            && as_user git -C "$ROOT" remote add origin "$SRC_URL" \
            && as_user git -C "$ROOT" fetch --depth 1 origin "$SRC_REF" \
            && as_user git -C "$ROOT" checkout --detach FETCH_HEAD \
            || die "could not clone $SRC_URL at $SRC_REF"
    else
        as_user git clone --depth 1 --branch "$SRC_REF" "$SRC_URL" "$ROOT" \
            || die "could not clone $SRC_URL at $SRC_REF"
    fi
    source_ok "$ROOT" || die "cloned checkout is not a Skein source tree"
    if [ "$BUILD" -eq 1 ]; then BIN_DIR="$ROOT/gui/bin"; fi
    log "Source commit: $(as_user git -C "$ROOT" rev-parse HEAD 2>/dev/null || printf 'unknown')"
}

ensure_node() {
    local major arch base file sum
    major=$(run_build "$ROOT" node -p 'process.versions.node.split(".")[0]' 2>/dev/null || true)
    if [[ "$major" =~ ^[0-9]+$ ]] && [ "$major" -ge 22 ] \
        && run_build "$ROOT" npm --version >/dev/null 2>&1; then
        return 0
    fi
    case "$(uname -m)" in
        x86_64|amd64) arch=x64 ;;
        aarch64|arm64) arch=arm64 ;;
        *) die "private Node.js 22 supports x86_64/arm64 only" ;;
    esac
    base=https://nodejs.org/dist/latest-v22.x
    log "Downloading private Node.js 22 (linux-$arch)..."
    run_build "$TMP" curl -fsSL --proto '=https' --proto-redir '=https' --retry 3 \
        -o SHASUMS256.txt "$base/SHASUMS256.txt" || die "could not fetch Node.js checksums"
    file=$(run_build "$TMP" awk -v a="$arch" \
        '$2 ~ ("-linux-" a "\\.tar\\.xz$") { print $2; exit }' SHASUMS256.txt)
    [ -n "$file" ] || die "no Node.js 22 linux-$arch archive"
    sum=$(run_build "$TMP" awk -v f="$file" '$2 == f { print $1; exit }' SHASUMS256.txt)
    [ -n "$sum" ] || die "no SHA-256 for $file"
    run_build "$TMP" curl -fsSL --proto '=https' --proto-redir '=https' --retry 3 \
        -o "$file" "$base/$file" || die "could not download $file"
    [ "$(run_build "$TMP" sha256sum "$file" | awk '{ print $1 }')" = "$sum" ] \
        || die "Node.js archive checksum mismatch"
    run_build "$TMP" tar -xJf "$file" || die "could not extract $file"
    BUILD_PATH="$TMP/${file%.tar.xz}/bin:$BUILD_PATH"
    major=$(run_build "$TMP" node -p 'process.versions.node.split(".")[0]') \
        || die "private Node.js did not run"
    [ "$major" -ge 22 ] || die "private Node.js is not 22+"
    run_build "$TMP" npm --version >/dev/null 2>&1 || die "private Node.js has no npm"
}

build() {
    need_user
    tmpdir
    BUILD_PATH="$TMP/tools/bin:$USER_HOME/go/bin:$USER_HOME/.local/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
    if [ "$EUID_NOW" -eq 0 ]; then
        as_user test -w "$ROOT" && as_user test -w "$ROOT/gui" \
            && as_user test -w "$ROOT/gui/frontend" \
            || die "$ROOT is not writable by $USER_RUN"
    else
        [ -w "$ROOT" ] && [ -w "$ROOT/gui" ] && [ -w "$ROOT/gui/frontend" ] \
            || die "$ROOT is not writable by $USER_RUN"
    fi
    as_user mkdir -p "$ROOT/gui/bin" || die "could not create gui/bin"

    local gov minor
    gov=$(run_build "$ROOT" go version 2>&1) || die "Go not found; install the distro Go package"
    minor=$(printf '%s' "$gov" | sed -n 's/^go version go1\.\([0-9]*\).*/\1/p')
    [ -n "$minor" ] && [ "$minor" -ge 21 ] || die "Go 1.21+ required (found: $gov)"

    log "Installing pinned wails3 v3.1.0..."
    run_build "$ROOT" go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0 \
        || die "could not install wails3 v3.1.0"
    case "$(run_build "$ROOT" "$TMP/tools/bin/wails3" version 2>&1)" in
        *v3.1.0*) ;;
        *) die "wails3 is not v3.1.0" ;;
    esac
    ensure_node
    log "Building skeind, skein-helper, skein and skein-desktop..."
    run_build "$ROOT/gui" "$TMP/tools/bin/wails3" task linux:build:system-binaries \
        || die "system binary build failed"
    run_build "$ROOT/gui" "$TMP/tools/bin/wails3" build || die "desktop build failed"
}

# --- install ---------------------------------------------------------------
install_file() {
    local tmp="$2.new.$$"
    run_root install -o root -g root -m "$3" "$1" "$tmp" \
        || { run_root rm -f -- "$tmp"; die "failed to stage $2"; }
    run_root mv -f -- "$tmp" "$2" || die "failed to replace $2"
}

check_file() {
    [ "$(stat -c '%u:%g %a' "$1")" = "0:0 $2" ] \
        || die "unexpected owner/mode on $1 (want 0:0 $2)"
}

install_payload() {
    log "Installing files..."
    run_root install -d -o root -g root -m 0755 /usr/libexec \
        /usr/share/polkit-1/actions /usr/share/applications \
        /usr/share/icons/hicolor/128x128/apps || die "could not create directories"
    install_file "$BIN_DIR/skeind" /usr/local/bin/skeind 755
    install_file "$BIN_DIR/skein" /usr/local/bin/skein 755
    install_file "$BIN_DIR/skein-desktop" /usr/local/bin/skein-desktop 755
    install_file "$BIN_DIR/skein-helper" /usr/libexec/skein-helper 755
    install_file "$ROOT/gui/build/linux/nfpm/org.skein.helper.policy" \
        /usr/share/polkit-1/actions/org.skein.helper.policy 644
    install_file "$ROOT/gui/build/linux/skein-desktop.desktop" \
        /usr/share/applications/skein-desktop.desktop 644
    install_file "$ROOT/gui/build/appicon.png" \
        /usr/share/icons/hicolor/128x128/apps/skein-desktop.png 644
    check_file /usr/local/bin/skeind 755
    check_file /usr/local/bin/skein 755
    check_file /usr/local/bin/skein-desktop 755
    check_file /usr/libexec/skein-helper 755
    check_file /usr/share/polkit-1/actions/org.skein.helper.policy 644
    check_file /usr/share/applications/skein-desktop.desktop 644
    check_file /usr/share/icons/hicolor/128x128/apps/skein-desktop.png 644
}

apparmor_userns() {
    [ -r /sys/module/apparmor/parameters/enabled ] || return 0
    [ "$(cat /sys/module/apparmor/parameters/enabled)" = Y ] || return 0
    [ "$(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns 2>/dev/null || printf 0)" = 1 ] \
        || return 0
    if [ -e /etc/apparmor.d/skein-desktop ]; then return 0; fi

    local p abi f
    for p in apparmor_parser /sbin/apparmor_parser /usr/sbin/apparmor_parser; do
        if command -v "$p" >/dev/null 2>&1; then break; fi
        p=""
    done
    if [ -z "$p" ]; then
        warn "Ubuntu restricts unprivileged userns and apparmor_parser is missing. Install apparmor and grant 'userns,' to /usr/local/bin/skein-desktop with a narrow profile; do NOT disable the global restriction."
        return 0
    fi
    if [ -e /etc/apparmor.d ]; then secure_dir /etc/apparmor.d; fi
    abi=""
    if [ -d /etc/apparmor.d/abi ]; then
        abi=$(ls /etc/apparmor.d/abi 2>/dev/null | sort -V | tail -n 1)
    fi
    f=$(mktemp) || die "mktemp failed"
    {
        printf '# Narrow userns grant for the Skein desktop only.\n'
        if [ -n "$abi" ]; then printf 'abi <abi/%s>,\n' "$abi"; fi
        printf 'include <tunables/global>\n\n'
        printf 'profile skein-desktop /usr/local/bin/skein-desktop flags=(unconfined) {\n'
        printf '  userns,\n  include if exists <local/skein-desktop>\n}\n'
    } > "$f"
    if run_root install -o root -g root -m 0644 "$f" /etc/apparmor.d/skein-desktop; then
        rm -f -- "$f"
        if run_root "$p" -r /etc/apparmor.d/skein-desktop; then
            log "Installed a narrow AppArmor userns profile for the desktop."
        else
            run_root rm -f -- /etc/apparmor.d/skein-desktop
            warn "the narrow AppArmor profile could not be loaded and was removed; the desktop may fail to start."
        fi
    else
        rm -f -- "$f"
        warn "could not install the narrow AppArmor profile; the desktop may fail to start."
    fi
}

print_plan() {
    local src user_desc
    if [ "$CLONE" -eq 1 ]; then
        src="clone $SRC_URL at $SRC_REF (private temp)"
        user_desc=${USER_RUN:-<none: pass --build-user>}
    else
        src=$ROOT
        if [ "$BUILD" -eq 1 ]; then user_desc=${USER_RUN:-<none: pass --build-user>}; else user_desc=n/a; fi
    fi
    printf '%s\n' \
        "Skein installer — DRY RUN, nothing will be changed" \
        "" \
        "  distro:       $PRETTY" \
        "  family:       $FAM ($PM)" \
        "  source:       $src" \
        "  install:      $([ "$BUILD" -eq 1 ] && echo 'build from source' || echo "prebuilt $BIN_DIR")" \
        "  build user:   $user_desc" \
        "  packages:     ${PKGS[*]}"
    case "$FAM" in
        arch) printf '  package cmd:  pacman -S --needed --noconfirm %s\n' "${PKGS[*]}" ;;
        apt)  printf '  package cmd:  apt-get update && apt-get install -y --no-install-recommends %s\n' "${PKGS[*]}" ;;
        dnf)  printf '  package cmd:  dnf install -y %s\n' "${PKGS[*]}" ;;
    esac
    printf '%s\n' \
        "" \
        "  installs:     /usr/local/bin/{skeind,skein,skein-desktop}" \
        "                /usr/libexec/skein-helper" \
        "                /usr/share/polkit-1/actions/org.skein.helper.policy" \
        "                /usr/share/applications/skein-desktop.desktop" \
        "                /usr/share/icons/hicolor/128x128/apps/skein-desktop.png" \
        "                group skein (no user is added)" \
        "" \
        "  service:      stop an existing unit before replacing binaries;" \
        "                with /etc/skein/skein.toml: helper install, then" \
        "                require running+enabled; without it: fresh install" \
        "                only (desktop create/join registers the service)" \
        "  firewall:     applied by the daemon when a network connects" \
        "" \
        "No package manager, service, or file is touched by --dry-run."
}

# --- run -------------------------------------------------------------------
# A local tree can be validated before any package or service work.
if [ "$CLONE" -eq 0 ]; then
    validate_payload
    [ "$BUILD" -eq 1 ] || validate_bins "$BIN_DIR"
fi

if [ "$DRY" -eq 1 ]; then
    if [ "$CLONE" -eq 1 ]; then
        warn "no Skein source tree found; a real install clones $SRC_URL at $SRC_REF"
    fi
    [ -d /run/systemd/system ] || warn "systemd is not running; a real install would be refused"
    print_plan
    exit 0
fi

[ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1 \
    || die "systemd is required (this installer supports systemd distros only)"
desktop_running

if [ "$EUID_NOW" -ne 0 ]; then
    command -v sudo >/dev/null 2>&1 || die "sudo is required for the privileged steps"
    log "Requesting sudo..."
    sudo -n true 2>/dev/null || sudo -v || die "sudo authentication failed"
fi
if [ "$CLONE" -eq 1 ] || [ "$BUILD" -eq 1 ]; then need_user; fi

install_deps
verify_runtime
if [ "$CLONE" -eq 1 ]; then clone_source; fi
validate_payload
if [ "$BUILD" -eq 1 ]; then
    BIN_DIR="$ROOT/gui/bin"
    build
fi
validate_bins "$BIN_DIR"
secure_dest
desktop_running

# A long build can outlive the sudo timestamp.
if [ "$EUID_NOW" -ne 0 ]; then
    sudo -n true 2>/dev/null || sudo -v || die "sudo authentication failed"
fi

if run_root test -f /etc/skein/skein.toml; then HAD_CONFIG=1; else HAD_CONFIG=0; fi
if systemctl cat skein.service >/dev/null 2>&1 \
    || systemctl is-active --quiet skein.service 2>/dev/null; then
    log "Stopping skein.service..."
    run_root systemctl stop skein.service \
        || die "could not stop the service; no files were replaced"
fi
if ! getent group skein >/dev/null 2>&1; then
    log "Creating the skein group..."
    run_root groupadd -r skein || die "could not create the skein group"
fi

install_payload
apparmor_userns

if [ "$HAD_CONFIG" -eq 1 ]; then
    log "Existing config found; re-registering and starting the service..."
    out=$(run_root /usr/libexec/skein-helper install 2>&1) || {
        printf '%s\n' "$out" >&2
        die "skein-helper install failed; config/identity were kept. Fix the error, then run: sudo /usr/libexec/skein-helper install"
    }
    printf '%s\n' "$out"
    case "$out" in
        *'"ok":true'*) ;;
        *) die "skein-helper install did not report success" ;;
    esac
    st=$(run_root /usr/libexec/skein-helper status 2>&1 || true)
    if ! grep -q '"state":"running"' <<<"$st" || ! grep -q '"enabled":true' <<<"$st"; then
        run_root systemctl enable skein.service >/dev/null 2>&1 || true
        st=$(run_root /usr/libexec/skein-helper status 2>&1 || true)
    fi
    if ! grep -q '"state":"running"' <<<"$st" || ! grep -q '"enabled":true' <<<"$st"; then
        printf '%s\n' "$st" >&2
        run_root systemctl status skein.service --no-pager >&2 || true
        command -v journalctl >/dev/null 2>&1 \
            && run_root journalctl -u skein.service -n 20 --no-pager >&2 || true
        die "the service did not reach running+enabled; config/identity were kept"
    fi
fi

RUN_AS=${USER_RUN:-your normal user}
if [ "$HAD_CONFIG" -eq 1 ]; then
    printf '\nSkein upgraded; the service is running and enabled.\nConfig, identity and access grants in /etc/skein were kept.\n\nLaunch the desktop as %s (never root): skein-desktop\nFirewall rules are applied by the daemon when a network connects.\n' "$RUN_AS"
else
    printf '\nSkein installed. No network config exists yet, so the service is not\nregistered: that is the bootstrap step, not this installer.\n\nLaunch the desktop as %s (never root): skein-desktop\nChoose "Create a network" or "Join a network". That bootstrap registers\nand starts the service; this installer creates no config or token.\n\nThe skein group is only for control-socket access; the helper grants your\nuser a per-user socket ACL. Firewall rules are applied by the daemon when\na network connects.\n' "$RUN_AS"
fi
