import { useState } from "react";
import { Bell } from "lucide-react";
import { LogoMark } from "@/components/brand/LogoMark";
import { useSessionEvents } from "@/features/notifications/store";
import { MobileSheet } from "./MobileSheet";

/**
 * MobileAppBar replaces the desktop CommandBar (§2.3): Skein mark +
 * network name on the left, connection dot + notifications bell on
 * the right. The bell opens the session notifications as a sheet;
 * the badge is the unread count.
 */
export function MobileAppBar({ networkName, connected }: { networkName: string; connected: boolean }) {
  const events = useSessionEvents((s) => s.events);
  const [sheetOpen, setSheetOpen] = useState(false);
  // The badge counts events since the sheet was last opened. It used to
  // be the session total, which never cleared — a permanent red badge
  // that told the user nothing.
  const [seen, setSeen] = useState(0);
  const unread = Math.max(0, events.length - seen);

  return (
    <>
      <header
        className="sticky top-0 z-40 flex items-center justify-between gap-3 border-b border-[var(--border)] bg-[var(--bg)]/95 px-4 backdrop-blur"
        style={{ height: "calc(56px + env(safe-area-inset-top, 0px))", paddingTop: "env(safe-area-inset-top, 0px)" }}
      >
        <div className="flex min-w-0 items-center gap-2">
          <LogoMark size={32} />
          <span className="truncate text-[15px] font-semibold">{networkName || "Skein"}</span>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <span
            aria-label={connected ? "Connected" : "Not connected"}
            className="h-2.5 w-2.5 rounded-full"
            style={{ background: connected ? "var(--success)" : "var(--text-faint)" }}
          />
          <button
            type="button"
            aria-label={`Notifications, ${unread} unread`}
            onClick={() => {
              setSeen(events.length);
              setSheetOpen(true);
            }}
            className="relative flex h-12 w-12 items-center justify-center rounded-full text-[var(--text-muted)] hover:bg-[var(--surface-raised)] hover:text-[var(--text)]"
          >
            <Bell size={20} />
            {unread > 0 && (
              <span className="absolute right-1.5 top-1.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-[var(--danger)] px-1 text-[9px] font-bold text-white">
                {unread > 99 ? "99+" : unread}
              </span>
            )}
          </button>
        </div>
      </header>
      <MobileSheet open={sheetOpen} onClose={() => setSheetOpen(false)} title="Notifications" initialDetent="half">
        {events.length === 0 ? (
          <p className="py-6 text-center text-sm text-[var(--text-muted)]">Nothing yet — mesh events appear here as they happen.</p>
        ) : (
          <ul className="space-y-2">
            {[...events].reverse().map((e) => (
              <li key={e.key} className="rounded-lg border border-[var(--border)] bg-[var(--surface-raised)] px-3 py-2 text-sm">
                <div className="font-medium">{e.type.replaceAll("_", " ")}</div>
                {e.detail && <div className="mt-0.5 text-xs text-[var(--text-muted)]">{e.detail}</div>}
                <div className="mt-0.5 font-mono text-[11px] text-[var(--text-faint)]">
                  {new Date(e.time_ms).toLocaleTimeString()}
                </div>
              </li>
            ))}
          </ul>
        )}
      </MobileSheet>
    </>
  );
}
