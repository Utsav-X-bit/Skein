#!/bin/sh
# Skein preremove (Step 11): runs BEFORE package files are removed,
# so the minimal helper is still present. Order matters:
#   1. helper uninstall stops the service (systemctl disable --now),
#      removes its firewall rules, and deletes the service
#      registration (/etc/systemd/system/skein.service).
#   2. Only then does the package manager remove the binaries.
# If the helper fails, removal aborts (non-zero exit): deleting
# files around a running service would leave an orphaned service
# pointing at a deleted binary. The one tolerated case is a missing
# helper binary (package already half-removed by hand): then there
# is nothing to run, and removal proceeds with a warning.
# Config is NEVER purged here: /etc/skein and /var/lib/skein/backups
# survive removal. Purge is the daemon reset endpoint, or a manual
# removal after the backup note printed by postremove.sh.
if [ -x /usr/libexec/skein-helper ]; then
  /usr/libexec/skein-helper uninstall
  rc=$?
  if [ $rc -ne 0 ]; then
    echo "Skein: helper uninstall failed (exit $rc); aborting package removal so no orphaned service is left behind." >&2
    exit $rc
  fi
else
  echo "Warning: /usr/libexec/skein-helper not found; skipping service uninstall. Check 'systemctl status skein' after removal." >&2
fi
exit 0
