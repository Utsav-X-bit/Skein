import { Button } from "@/components/ui/button";

/**
 * StateScreen is the full-screen state template (design brief §5.1):
 * one title, one plain-language explanation, one primary button, an
 * optional secondary. Every state — daemon down, no network, paused,
 * departed, VPN-permission-denied — is an instance of this, never a
 * banner plus a paragraph.
 */
export function StateScreen({
  title,
  message,
  lastKnown,
  primaryLabel,
  onPrimary,
  primaryBusy,
  secondaryLabel,
  onSecondary,
}: {
  title: string;
  message: string;
  lastKnown?: string;
  primaryLabel: string;
  onPrimary: () => void;
  primaryBusy?: boolean;
  secondaryLabel?: string;
  onSecondary?: () => void;
}) {
  return (
    <div className="flex min-h-full flex-col items-center justify-center px-6 py-10 text-center">
      <h1 className="text-xl font-semibold">{title}</h1>
      <p className="mt-3 max-w-sm text-[15px] leading-relaxed text-[var(--text-muted)]">{message}</p>
      {lastKnown && <p className="mt-2 text-xs text-[var(--text-faint)]">Last known: {lastKnown}</p>}
      <Button size="lg" className="mt-8 h-[var(--connect-h)] w-full max-w-sm" disabled={primaryBusy} onClick={onPrimary}>
        {primaryBusy ? "Working…" : primaryLabel}
      </Button>
      {secondaryLabel && onSecondary && (
        <Button variant="secondary" size="lg" className="mt-3 h-12 w-full max-w-sm" onClick={onSecondary}>
          {secondaryLabel}
        </Button>
      )}
    </div>
  );
}
