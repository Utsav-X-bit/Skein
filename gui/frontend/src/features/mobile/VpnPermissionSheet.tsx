import { ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { MobileSheet } from "@/components/layout/MobileSheet";

/**
 * VpnPermissionSheet is the first-Connect explainer (design brief
 * §5.2): it runs before the system VPN dialog, says what the dialog
 * will say, why Skein needs it, what it can and cannot see, and that
 * it can be revoked anytime in system Settings. Continue launches
 * the system dialog; Not now dismisses and leaves Connect available.
 */
export function VpnPermissionSheet({
  open,
  onClose,
  onContinue,
  overlayPrefix,
}: {
  open: boolean;
  onClose: () => void;
  onContinue: () => void;
  /** Overlay network routed through the tunnel, e.g. 10.144.144.0/24. */
  overlayPrefix?: string;
}) {
  return (
    <MobileSheet open={open} onClose={onClose} title="Android will ask to set up a VPN" initialDetent="peek">
      <div className="space-y-4">
        <div className="flex items-start gap-3">
          <ShieldCheck size={20} className="mt-0.5 shrink-0 text-[var(--accent)]" />
          <p className="text-sm leading-relaxed text-[var(--text-muted)]">
            Skein creates a VPN tunnel inside this app to carry mesh traffic. Android will show its own system dialog —
            Skein cannot skip it, fake it, or style it.
          </p>
        </div>
        <ul className="list-disc space-y-2 pl-5 text-sm leading-relaxed text-[var(--text-muted)]">
          <li>Only the mesh overlay is routed through the tunnel{overlayPrefix ? ` (${overlayPrefix})` : ""}. Your normal internet traffic is untouched.</li>
          <li>Skein&rsquo;s own connection to peers bypasses the tunnel, so the mesh cannot route its own traffic into itself.</li>
          <li>Nothing leaves the phone until you say yes. You can revoke the permission anytime in system Settings → VPN.</li>
        </ul>
        <div className="space-y-2 pb-2">
          <Button size="lg" className="h-[var(--connect-h)] w-full" onClick={onContinue}>
            Continue
          </Button>
          <Button variant="secondary" size="lg" className="h-12 w-full" onClick={onClose}>
            Not now
          </Button>
        </div>
      </div>
    </MobileSheet>
  );
}
