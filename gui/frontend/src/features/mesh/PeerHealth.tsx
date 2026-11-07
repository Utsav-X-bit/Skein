import type { Peer } from "@/lib/api";
import { StatusDot, type StatusTone } from "@/components/ui/status-dot";
import { cn } from "@/lib/utils";
import { peerHealth, type Presence } from "./model";

const toneOf: Record<"ok" | "warn" | "down" | "unknown", StatusTone> = {
  ok: "up",
  warn: "degraded",
  down: "down",
  unknown: "info",
};

/** PeerHealth shows the same classification the table and inspector use. */
export function PeerHealth({
  peer,
  presence,
  compact = false,
  className,
}: {
  peer: Peer | null | undefined;
  presence: Presence;
  compact?: boolean;
  className?: string;
}) {
  const health = peerHealth(peer, presence);
  return (
    <div className={cn("flex min-w-0 items-center gap-2", className)}>
      <StatusDot tone={toneOf[health.tone]} pulse={health.tone === "ok" && presence === "active"} />
      <span className="shrink-0 text-sm">{health.label}</span>
      {!compact && <span className="truncate text-xs text-[var(--text-muted)]">{health.detail}</span>}
    </div>
  );
}
