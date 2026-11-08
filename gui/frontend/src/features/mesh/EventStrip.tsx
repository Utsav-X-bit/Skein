import { Link } from "react-router-dom";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { shortID } from "@/lib/utils";
import { useSessionEvents } from "@/features/notifications/store";

const labels: Record<string, string> = {
  peer_up: "Peer up",
  peer_joined: "Peer joined",
  peer_discovered: "Peer discovered",
  peer_added: "Peer added",
  peer_removed: "Peer removed",
  "peer:leave": "Peer left the network",
  "peer:rejoined": "Peer rejoined the network",
  name_changed: "Node name changed",
  secret_rotated: "Network secret rotated",
  system_reset: "Configuration reset",
  system_imported: "Configuration imported",
  leave_dataplane_stop_failed: "Leave: dataplane stop failed",
};

function deepLink(type: string, peerId?: string): string {
  if (peerId) return `/peers?peer=${encodeURIComponent(peerId)}`;
  if (type === "system_reset" || type === "system_imported" || type === "secret_rotated") return "/settings";
  return "/activity";
}

/** EventStrip lists real lifecycle events with the surface that owns them. */
export function EventStrip({ limit = 8 }: { limit?: number }) {
  const events = useSessionEvents((s) => s.events);
  const recent = events.slice(-limit).reverse();
  return (
    <Card>
      <CardHeader>
        <CardTitle>Recent events</CardTitle>
        <Link to="/activity" className="text-xs text-[var(--accent)]">Activity</Link>
      </CardHeader>
      <CardContent className="p-0">
        {recent.length === 0 ? (
          <div className="px-4 py-6 text-center text-xs text-[var(--text-muted)]">No lifecycle events this session.</div>
        ) : (
          <ul className="divide-y divide-[var(--border)]">
            {recent.map((ev) => (
              <li key={ev.key}>
                <Link
                  to={deepLink(ev.type, ev.peer_id)}
                  className="flex items-center gap-3 px-4 py-2 text-xs hover:bg-[var(--surface-raised)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--ring)]"
                >
                  <span className="tabular shrink-0 text-[var(--text-faint)]">
                    {new Date(ev.time_ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}
                  </span>
                  <span className="min-w-0 flex-1 truncate text-[var(--text)]">{labels[ev.type] ?? ev.type}</span>
                  {ev.peer_id && <span className="shrink-0 font-mono text-[var(--text-faint)]">{shortID(ev.peer_id)}</span>}
                </Link>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
