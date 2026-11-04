#!/bin/sh

# Update desktop database for .desktop file changes
# This makes the application appear in application menus and registers its capabilities.
if command -v update-desktop-database >/dev/null 2>&1; then
  echo "Updating desktop database..."
  update-desktop-database -q /usr/share/applications
else
  echo "Warning: update-desktop-database command not found. Desktop file may not be immediately recognized." >&2
fi

# Update MIME database for custom URL schemes (x-scheme-handler)
# This ensures the system knows how to handle your custom protocols.
if command -v update-mime-database >/dev/null 2>&1; then
  echo "Updating MIME database..."
  update-mime-database -n /usr/share/mime
else
  echo "Warning: update-mime-database command not found. Custom URL schemes may not be immediately recognized." >&2
fi

# Step 11: apply the shipped sysusers/tmpfiles entries now (the
# package manager may not run them on every format), and reload
# systemd so the vendor unit template is known. Deliberately NOT
# enabling or starting the service: first start happens via GUI
# bootstrap (helper bootstrap verb mints the token, then the daemon
# starts into bootstrap mode). Starting an unconfigured daemon here
# would exit 0 harmlessly, but it would also be a service the user
# never asked to start.
if command -v systemd-sysusers >/dev/null 2>&1; then
  systemd-sysusers /usr/lib/sysusers.d/skein.conf || true
fi
if command -v systemd-tmpfiles >/dev/null 2>&1; then
  systemd-tmpfiles --create /usr/lib/tmpfiles.d/skein.conf || true
fi
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
fi

exit 0
