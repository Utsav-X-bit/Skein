#!/usr/bin/env bash
# packaging-assert.sh — M5 Step 11 content assertions for Skein packaging.
#
# The old failure mode was silent: NSIS shipped only the desktop exe
# and nfpm shipped only desktop files, and nothing checked. This
# script parses the installer manifests themselves (not a built
# installer — makensis/nfpm are not available on every build host)
# and fails if any required file, script, or ordering rule is
# missing. Run from CI on every run (see .github/workflows/ci.yml)
# and before any release build.
#
# It asserts manifests, not binaries: the Taskfile deps guarantee
# the binaries are built from the current tree before packaging, so
# a manifest entry that points at ./bin/skeind is a current-tree
# daemon, not a stale prebuilt one.
#
# Repo-root-relative, like collapse-gates.sh.
set -uo pipefail
cd "$(cd "$(dirname "$0")/.." && pwd)"

fail=0
note() { echo "packaging-assert: FAIL: $*"; fail=1; }
ok()   { echo "packaging-assert: ok: $*"; }

NSIS=gui/build/windows/nsis/project.nsi
NFPM=gui/build/linux/nfpm/nfpm.yaml
WIN_TASK=gui/build/windows/Taskfile.yml
LIN_TASK=gui/build/linux/Taskfile.yml
UNIT=gui/build/linux/nfpm/skein.service

# --- Windows NSIS -------------------------------------------------
[ -f "$NSIS" ] || note "missing $NSIS"
# Required files in the install section: desktop (via wails.files)
# plus every system file, each as an explicit File line. The !error
# guards in project.nsi make a missing makensis -D define a hard
# build failure; this check makes a deleted File line one too.
grep -q 'wails.files' "$NSIS" || note "NSIS: wails.files (desktop exe) missing"
for f in skeind.exe skein-helper.exe skein.exe wintun.dll wintun-LICENSE.txt; do
	grep -q "File .*\"/oname=$f\"" "$NSIS" || note "NSIS: File line for $f missing"
done
for d in ARG_SKEIND_BINARY ARG_SKEIN_HELPER_BINARY ARG_SKEIN_CLI_BINARY ARG_WINTUN_DLL ARG_WINTUN_LICENSE; do
	grep -q "!ifndef $d" "$NSIS" || note "NSIS: !error guard for $d missing"
done
# Install dir: single Skein directory, matching the GUI fallback
# C:\Program Files\Skein\skein-helper.exe. The nested
# $PROGRAMFILES64\Skein\Skein layout must not come back.
grep -q 'InstallDir "\$PROGRAMFILES64\\\${INFO_PRODUCTNAME}"' "$NSIS" \
	|| note "NSIS: machine InstallDir is not \$PROGRAMFILES64\\\${INFO_PRODUCTNAME}"
grep -q 'InstallDir "\$PROGRAMFILES64\\\${INFO_COMPANYNAME}\\\${INFO_PRODUCTNAME}"' "$NSIS" \
	&& note "NSIS: nested Skein\\Skein InstallDir present"
# Install order (complete setup): on upgrade the service must be
# stopped before the first File line (a running skeind locks its
# exe), and the service must be (re-)registered through the helper
# when a network already exists. A fresh install has no config, and
# helper install fails by design on one ("no system config: run init
# or import first") — so the helper install must be gated on an
# existing %ProgramData%\Skein\skein.toml, not run unconditionally,
# and a helper-install failure must abort instead of reporting a
# successful install with a dead service.
instsec=$(awk '/^Section$/,/^SectionEnd/' "$NSIS")
[ -n "$instsec" ] || note "NSIS install: install section not found"
stop_line=$(printf '%s' "$instsec" | grep -n 'skein-helper.exe" stop' | head -1 | cut -d: -f1)
first_file_line=$(printf '%s' "$instsec" | grep -n 'File .*"/oname=' | head -1 | cut -d: -f1)
[ -n "$stop_line" ] || note "NSIS install: helper stop before file copy missing (upgrade over a running service locks the binaries)"
if [ -n "$stop_line" ] && [ -n "$first_file_line" ] && [ "$stop_line" -gt "$first_file_line" ]; then
	note "NSIS install: helper stop runs AFTER the first File line (service must stop first)"
fi
printf '%s' "$instsec" | grep -q 'skein-helper.exe" install' || note "NSIS install: helper install (re-register + start) missing"
printf '%s' "$instsec" | grep -q 'Skein\\skein.toml' || note "NSIS install: helper install is not gated on an existing network config (it fails by design on a fresh install)"
printf '%s' "$instsec" | grep -q 'Abort' || note "NSIS install: no Abort on helper-install failure"
# Running-desktop precheck: a running skein-desktop.exe locks its own
# exe, so an install over it aborts mid-copy (after the service was
# stopped) and an uninstall leaves the exe behind while reporting
# success. Both entry points must probe tasklist through nsExec before
# anything is touched, must fail closed when tasklist cannot run, and
# must never kill the desktop.
probe=$(awk '/^!macro SkeinDesktopProbe/,/^!macroend/' "$NSIS")
[ -n "$probe" ] || note "NSIS: running-desktop probe macro missing"
printf '%s' "$probe" | grep -q 'tasklist' || note "NSIS: desktop probe does not run tasklist"
printf '%s' "$probe" | grep -q 'nsExec::ExecToStack' || note "NSIS: desktop probe does not use nsExec"
printf '%s' "$probe" | grep -q 'SkeinDesktopState "unknown"' \
	|| note "NSIS: desktop probe does not fail closed (no 'unknown' default)"
oninit=$(awk '/^Function \.onInit/,/^FunctionEnd/' "$NSIS")
printf '%s' "$oninit" | grep -q 'SkeinDesktopProbe' \
	|| note "NSIS install: .onInit does not run the desktop probe before the pages"
printf '%s' "$oninit" | grep -q 'SetErrorLevel' \
	|| note "NSIS install: silent desktop precheck sets no failure exit code"
printf '%s' "$oninit" | grep -q 'MessageBox' \
	|| note "NSIS install: interactive desktop precheck gives no close-desktop guidance"
grep -qi 'taskkill' "$NSIS" && note "NSIS: must never kill the desktop (fail closed and ask the user to close it)"
# Uninstall order: helper uninstall must come before the first
# RMDir in the uninstall section, and the section must ask about
# purge (moving to .bak) instead of silently deleting %ProgramData%.
unsec=$(awk '/^Section "uninstall"/,/^SectionEnd/' "$NSIS")
helper_line=$(printf '%s' "$unsec" | grep -n 'skein-helper.exe" uninstall' | head -1 | cut -d: -f1)
rmdir_line=$(printf '%s' "$unsec" | grep -n '^[[:space:]]*RMDir[[:space:]]' | head -1 | cut -d: -f1)
[ -n "$helper_line" ] || note "NSIS uninstall: helper uninstall call missing"
[ -n "$rmdir_line" ] || note "NSIS uninstall: no RMDir found (section changed?)"
if [ -n "$helper_line" ] && [ -n "$rmdir_line" ] && [ "$helper_line" -gt "$rmdir_line" ]; then
	note "NSIS uninstall: helper uninstall runs AFTER the first RMDir (service would be deleted around, not stopped first)"
fi
# The desktop probe must run before the helper uninstall: RMDir /r
# cannot delete a locked exe, so uninstalling over a running desktop
# would delete the shortcuts and uninstall key and leave the exe.
probe_line=$(printf '%s' "$unsec" | grep -n 'SkeinDesktopProbe' | head -1 | cut -d: -f1)
[ -n "$probe_line" ] || note "NSIS uninstall: desktop probe missing (RMDir cannot delete a locked exe)"
if [ -n "$probe_line" ] && [ -n "$helper_line" ] && [ "$probe_line" -gt "$helper_line" ]; then
	note "NSIS uninstall: desktop probe runs after the helper uninstall (must fail before any mutation)"
fi
printf '%s' "$unsec" | grep -q 'MessageBox MB_YESNO' || note "NSIS uninstall: purge question missing"
printf '%s' "$unsec" | grep -q 'Skein.bak' || note "NSIS uninstall: purge-to-.bak backup missing"
# $PROGRAMDATA is not an NSIS constant: makensis warning 6000
# compiles it to empty, which silently disabled the purge branch
# (M9 §2c). The uninstall section must resolve ProgramData via
# ReadEnvStr instead.
printf '%s' "$unsec" | grep -q '"\$PROGRAMDATA' && note "NSIS uninstall: \$PROGRAMDATA is not an NSIS constant (use ReadEnvStr)"
printf '%s' "$unsec" | grep -q 'ReadEnvStr.*ProgramData' || note "NSIS uninstall: ProgramData not resolved via ReadEnvStr"
printf '%s' "$unsec" | grep -q 'RMDir /r "\$PROGRAMDATA\\Skein"' \
	&& note "NSIS uninstall: silent RMDir of %ProgramData%\\Skein (purge must move to .bak, never delete)"
printf '%s' "$unsec" | grep -q 'could not stop and remove the service' \
	|| note "NSIS uninstall: no Abort on helper-uninstall failure"
# Windows Taskfile must build the system binaries and pass every
# ARG define to makensis; a define passed nowhere is a dead File.
grep -q 'task: build:system-binaries' "$WIN_TASK" || note "Windows Taskfile: create:nsis:installer does not depend on build:system-binaries"
for d in ARG_SKEIND_BINARY ARG_SKEIN_HELPER_BINARY ARG_SKEIN_CLI_BINARY ARG_WINTUN_DLL ARG_WINTUN_LICENSE; do
	grep -q "\-D$d=" "$WIN_TASK" || note "Windows Taskfile: makensis never passes -D$d"
done
grep -q 'fetch-wintun' "$WIN_TASK" || note "Windows Taskfile: Wintun fetch step missing"
grep -q 'build:system-binaries' "$WIN_TASK" || note "Windows Taskfile: build:system-binaries task missing"
# The PowerShell installer (the second, script-only install path)
# copies the same desktop exe and needs the same running-desktop
# guard, failing closed and never killing the app.
PSI=scripts/install-windows.ps1
[ -f "$PSI" ] || note "missing $PSI"
grep -q 'IMAGENAME eq skein-desktop.exe' "$PSI" || note "install-windows.ps1: running-desktop probe missing"
grep -q 'LASTEXITCODE -ne 0' "$PSI" || note "install-windows.ps1: desktop probe does not fail closed when tasklist fails"
grep -qi 'Stop-Process\|taskkill' "$PSI" && note "install-windows.ps1: must never kill the desktop (fail closed and ask the user to close it)"
# The source install ships the same Wintun payload as NSIS: DLL and licence.
grep -q 'wintun.dll", "wintun-LICENSE.txt"' "$PSI" \
	|| note "install-windows.ps1: Wintun licence is not a required install file"

# --- Linux nfpm ---------------------------------------------------
[ -f "$NFPM" ] || note "missing $NFPM"
for dst in /usr/local/bin/skeind /usr/libexec/skein-helper /usr/local/bin/skein /usr/lib/systemd/system/skein.service /usr/lib/sysusers.d/skein.conf /usr/lib/tmpfiles.d/skein.conf /usr/share/polkit-1/actions/org.skein.helper.policy /usr/local/bin/skein-desktop; do
	grep -q "dst: \"$dst\"" "$NFPM" || note "nfpm: contents entry for $dst missing"
done
for s in preinstall postinstall preremove postremove; do
	grep -q "^  $s:" "$NFPM" || note "nfpm: scripts.$s not wired (still commented out?)"
done
# No second live unit: the package must not also install a unit at
# the RegisterDaemon path /etc/systemd/system/skein.service — that
# path is generated at install time and would be clobbered or
# shadowed by a shipped file.
grep -q 'dst: "/etc/systemd/system/skein.service"' "$NFPM" \
	&& note "nfpm: ships a live unit at /etc/systemd/system/skein.service (RegisterDaemon generates that path; ship only the /usr/lib template)"
# Vendor unit must match the generated unit's load-bearing lines.
grep -q 'ExecStart=/usr/local/bin/skeind run -c /etc/skein/skein.toml' "$UNIT" \
	|| note "vendor unit: ExecStart is not the canonical skeind path/config"
grep -q 'Restart=on-failure' "$UNIT" || note "vendor unit: Restart=on-failure missing"
grep -q 'Restart=on-failure' internal/service/service_linux.go \
	|| note "generated unit (unitFor): Restart=on-failure missing"
# sysusers / tmpfiles content.
grep -q '^g skein' gui/build/linux/nfpm/sysusers.d/skein.conf \
	|| note "sysusers: 'g skein' group entry missing"
grep -q '^d /run/skein 0750 root skein' gui/build/linux/nfpm/tmpfiles.d/skein.conf \
	|| note "tmpfiles: '/run/skein 0750 root skein' entry missing"
# pkexec selects the policy by executable path; it has no --action flag.
# A single unambiguous path-pinned action must remain admin-only, local-only,
# and uncached. Validate the policy structure, not incidental source spelling.
POLICY=gui/build/linux/nfpm/org.skein.helper.policy
if command -v python3 >/dev/null 2>&1; then
	python3 - "$POLICY" <<'PY' || note "polkit: unsafe or ambiguous helper authorization policy"
import sys
import xml.etree.ElementTree as ET
actions = ET.parse(sys.argv[1]).getroot().findall("action")
assert len(actions) == 1
action = actions[0]
assert action.get("id") == "org.skein.helper.control"
for name, value in {"allow_any": "no", "allow_inactive": "no", "allow_active": "auth_admin"}.items():
    assert action.findtext("defaults/" + name) == value
paths = [a.text for a in action.findall("annotate") if a.get("key") == "org.freedesktop.policykit.exec.path"]
assert paths == ["/usr/libexec/skein-helper"]
assert all(a.get("key") != "org.freedesktop.policykit.exec.allow_gui" for a in action.findall("annotate"))
PY
else
	note "polkit: python3 required to verify authorization invariants"
fi
# Lifecycle scripts: present, executable, syntax-clean, and with the
# right ordering guarantees (preremove runs helper uninstall and
# aborts on failure; postinstall never enables/starts the service;
# postremove never deletes /etc/skein).
for s in preinstall postinstall preremove postremove; do
	f=gui/build/linux/nfpm/scripts/$s.sh
	[ -x "$f" ] || note "script $f is not executable"
	sh -n "$f" || note "script $f fails sh -n"
done
grep -q 'skein-helper uninstall' gui/build/linux/nfpm/scripts/preremove.sh \
	|| note "preremove: helper uninstall call missing"
grep -q 'systemctl enable\|systemctl start' gui/build/linux/nfpm/scripts/postinstall.sh \
	&& note "postinstall: must not enable or start the service (first start is GUI bootstrap)"
grep -q 'rm -rf /etc/skein\|rm -r /etc/skein' gui/build/linux/nfpm/scripts/postremove.sh \
	&& note "postremove: must never delete /etc/skein (uninstall without purge keeps config)"
# Linux Taskfile: deb/rpm/aur all build the system binaries first.
# (sed range up to the next task header, minus that header line —
# an awk range would end on the start line itself, since the start
# line also matches the "next task" pattern.)
for t in create:deb create:rpm create:aur; do
	sed -n "/^  $t:/,/^  [a-zA-Z].*:/p" "$LIN_TASK" | head -n -1 | grep -q 'task: build:system-binaries' \
		|| note "Linux Taskfile: $t does not depend on build:system-binaries"
done

if [ "$fail" -ne 0 ]; then
	echo "packaging-assert: FAILED"
	exit 1
fi
ok "all packaging content assertions pass"
exit 0
