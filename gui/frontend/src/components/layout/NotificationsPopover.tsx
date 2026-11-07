import { useState } from "react";
import * as PopoverPrimitive from "@radix-ui/react-popover";
import { useNavigate } from "react-router-dom";
import { Bell } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useSessionEvents, EventLimit, type SessionEvent } from "@/features/notifications/store";
import { shortID } from "@/lib/utils";

// Lifecycle events only. The wording matches the Activity timeline; nothing
// here assigns a severity the daemon did not report.
const labels: Record<string, string> = {
  peer_up: "Peer up",
  peer_down: "Peer down",
  peer_joined: "Peer joined",
  peer_discovered: "Peer discovered",
  peer_added: "Peer added",
  peer_removed: "Peer removed",
  peer_departed: "Peer departed",
  dialer_fallback: "Dialer fallback",
  "peer:leave": "Peer left the network",
  "peer:rejoined": "Peer rejoined the network",
  name_changed: "Node name changed",
  secret_rotated: "Network secret rotated",
  system_reset: "Configuration reset",
  system_imported: "Configuration imported",
  leave_dataplane_stop_failed: "Leave: dataplane stop failed",
};

/** Each event points at the one surface that owns it. */
function actionFor(event: SessionEvent): { label: string; to: string } {
  if (event.peer_id) return { label: "Open peer", to: `/peers?peer=${encodeURIComponent(event.peer_id)}` };
  if (event.type === "secret_rotated") return { label: "Open security", to: "/security" };
  if (event.type === "system_reset" || event.type === "system_imported") return { label: "Service settings", to: "/settings" };
  if (event.type === "leave_dataplane_stop_failed") return { label: "Recovery", to: "/settings?network=rejoin" };
  return { label: "Activity", to: "/activity" };
}

/**
 * NotificationsPopover is the session's event log in the chrome: bounded by the
 * store (200), in memory only, one action per event. It never persists a peer
 * invitation or a secret.
 */
export function NotificationsPopover() {
  const events = useSessionEvents((s) => s.events);
  const clear = useSessionEvents((s) => s.clear);
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [seenAt, setSeenAt] = useState(() => Date.now());
  const unread = events.filter((e) => e.time_ms > seenAt).length;

  return (
    <PopoverPrimitive.Root
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        // Opening or closing both count as "seen": events that arrive while
        // the panel is open were on screen.
        setSeenAt(Date.now());
      }}
    >
      <PopoverPrimitive.Trigger asChild>
        <Button variant="ghost" size="icon" aria-label={unread > 0 ? `Session events, ${unread} new` : "Session events"} className="relative">
          <Bell size={16} />
          {unread > 0 && (
            <span aria-hidden className="absolute right-2 top-2 h-1.5 w-1.5 rounded-full bg-[var(--accent)]" />
          )}
        </Button>
      </PopoverPrimitive.Trigger>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Content
          align="end"
          sideOffset={6}
          aria-label="Session events"
          className="z-50 w-[min(92vw,380px)] rounded-lg border border-[var(--border-strong)] bg-[var(--surface)] shadow-[var(--shadow-float)]"
        >
          <div className="flex items-center justify-between gap-3 border-b border-[var(--border)] px-3 py-2">
            <div className="text-sm font-medium">Session events</div>
            <Button variant="ghost" size="sm" onClick={clear} disabled={events.length === 0}>
              Clear
            </Button>
          </div>
          <div className="max-h-[60vh] overflow-auto">
            {events.length === 0 ? (
              <p className="px-3 py-8 text-center text-xs text-[var(--text-muted)]">No lifecycle events this session.</p>
            ) : (
              <ul className="divide-y divide-[var(--border)]">
                {[...events].reverse().map((event) => {
                  const action = actionFor(event);
                  return (
                    <li key={event.key} className="px-3 py-2">
                      <div className="flex items-center gap-2 text-xs">
                        <span className="tabular shrink-0 text-[var(--text-faint)]">
                          {new Date(event.time_ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}
                        </span>
                        <span className="min-w-0 flex-1 truncate text-[var(--text)]">{labels[event.type] ?? event.type}</span>
                        {event.peer_id && <span className="shrink-0 font-mono text-[var(--text-faint)]">{shortID(event.peer_id)}</span>}
                      </div>
                      {event.detail && <p className="mt-1 break-words text-[11px] text-[var(--text-muted)]">{event.detail}</p>}
                      <button
                        className="mt-1 text-[11px] text-[var(--accent)] underline-offset-4 hover:underline"
                        onClick={() => {
                          setOpen(false);
                          navigate(action.to);
                        }}
                      >
                        {action.label}
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
          <p className="border-t border-[var(--border)] px-3 py-2 text-[11px] text-[var(--text-faint)]">
            This session only · in memory · never recorded · keeps the newest {EventLimit} events
          </p>
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  );
}
