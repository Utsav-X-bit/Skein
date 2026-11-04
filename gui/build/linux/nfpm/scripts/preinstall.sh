#!/bin/sh
# Skein preinstall (Step 11): create the skein group before any file
# that depends on it is used. systemd-sysusers normally creates it
# from /usr/lib/sysusers.d/skein.conf, but sysusers is not guaranteed
# to run on every distro/package format at install time, so create
# the group directly as well. Idempotent: getent succeeds on
# reinstall/upgrade and groupadd is skipped.
# The daemon runs as root; the group exists only for socket access
# (/run/skein/control.sock is 0660 root:skein). No user is created.
if getent group skein >/dev/null 2>&1; then
  exit 0
fi
if command -v groupadd >/dev/null 2>&1; then
  groupadd -r skein || true
else
  echo "Warning: groupadd not found; the skein group was not created. Run: groupadd -r skein" >&2
fi
exit 0
