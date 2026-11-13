import { Link } from "react-router-dom";
import { Invites } from "@/routes/invites/Invites";
import { Settings } from "@/routes/settings/Settings";
import { Card, CardContent } from "@/components/ui/card";

/**
 * MobileInvites is the Invites tab: the desktop Invites route plus
 * the Share button (navigator.share with clipboard fallback) added
 * to InvitePanel. No separate mobile copy of the invite logic.
 */
export function MobileInvites() {
  return <Invites />;
}

/**
 * MobileSettings is the Settings tab: the desktop Settings with the
 * mobile prop (no start-at-boot, no install/repair/uninstall, no
 * helper text — the Android VPN service instead; dark theme only;
 * "Animate mesh flow" switch), plus the About footer and a
 * Diagnostics & security card: the desktop Routes, Security and
 * Diagnostics pages are reachable from here (same components as
 * desktop — feature parity, Android presentation).
 */
export function MobileSettings() {
  return (
    <div className="space-y-4">
      <Settings mobile />
      <Card>
        <CardContent className="space-y-3 pt-4">
          <div>
            <h2 className="text-base font-medium">Diagnostics &amp; security</h2>
            <p className="mt-0.5 text-xs text-[var(--text-muted)]">Routes, the network secret and diagnostics — the same pages as desktop.</p>
          </div>
          <div className="space-y-1">
            {[
              { to: "/network", label: "Routes", hint: "Resolved paths and advertised routes" },
              { to: "/security", label: "Security", hint: "Firewall status and rotate the network secret" },
              { to: "/logs", label: "Diagnostics", hint: "Logs, peer probe and ping, export a bundle" },
            ].map(({ to, label, hint }) => (
              <Link key={to} to={to} className="flex min-h-12 items-center justify-between gap-3 rounded-lg px-2 py-2 transition-colors active:bg-[var(--surface-raised)]">
                <span>
                  <span className="block text-sm font-medium">{label}</span>
                  <span className="block text-xs text-[var(--text-muted)]">{hint}</span>
                </span>
                <span aria-hidden="true" className="text-[var(--text-faint)]">›</span>
              </Link>
            ))}
          </div>
        </CardContent>
      </Card>
      <Card>
        <CardContent className="pt-4">
          <Link to="/about" className="text-sm text-[var(--accent)] underline underline-offset-2">
            About Skein
          </Link>
          <p className="mt-1 text-xs text-[var(--text-muted)]">Version, diagnostics and what this build can do.</p>
        </CardContent>
      </Card>
    </div>
  );
}
