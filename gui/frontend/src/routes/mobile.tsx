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
 * "Animate mesh flow" switch), plus the About footer.
 */
export function MobileSettings() {
  return (
    <div className="space-y-4">
      <Settings mobile />
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
