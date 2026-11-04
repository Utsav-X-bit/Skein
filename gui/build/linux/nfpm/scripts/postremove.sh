#!/bin/sh
# Skein postremove (Step 11): runs AFTER package files are removed.
# The shipped unit/sysusers/tmpfiles/policy files are removed by the
# package manager itself. This script only reloads systemd (so the
# deleted live unit, if any, is forgotten) and reports kept state.
# Deliberate non-deletions, stated not silent:
#   - /etc/skein (config, identity, access grants) is KEPT: uninstall
#     without purge must not destroy a node's identity. Purge it via
#     the daemon reset endpoint while the daemon runs, or remove it
#     by hand after backing it up.
#   - /var/lib/skein/backups (Step 6/7 .old + config backups) is
#     KEPT for the same reason: an upgrade/uninstall must never
#     delete the only copy that can roll a daemon back.
#   - The skein group is KEPT: it is harmless system state, and
#     deleting it would race a reinstall's socket grants.
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
fi
if [ -d /etc/skein ]; then
  echo "Skein configuration kept at /etc/skein (identity, access grants, config)."
  echo "To purge it, back it up first, then remove /etc/skein and /var/lib/skein/backups."
fi
if [ -d /var/lib/skein/backups ]; then
  echo "Skein backups kept at /var/lib/skein/backups."
fi
exit 0
