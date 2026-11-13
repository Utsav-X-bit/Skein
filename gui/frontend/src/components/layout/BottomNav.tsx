import { useEffect, useState } from "react";
import { NavLink, useLocation } from "react-router-dom";
import { Activity, LayoutDashboard, Radio, Settings, Users } from "lucide-react";
import { cn } from "@/lib/utils";
import { useMesh } from "@/features/mesh/useMesh";
import { useSessionEvents } from "@/features/notifications/store";

const tabs = [
  { to: "/", label: "Home", icon: LayoutDashboard, end: true },
  { to: "/peers", label: "Peers", icon: Users, end: false },
  { to: "/activity", label: "Activity", icon: Activity, end: false },
  { to: "/invites", label: "Invites", icon: Radio, end: false },
  { to: "/settings", label: "Settings", icon: Settings, end: false },
] as const;

// Error-kind lifecycle events: a dot on Activity means something
// went wrong since the user last looked at the feed.
const errorTypes = new Set(["peer_down", "peer_departed", "dialer_fallback", "leave_dataplane_stop_failed"]);

/**
 * BottomNav is the mobile tab bar (design brief §2): five tabs, all
 * labels visible, 64px tall plus the gesture-bar inset, active tab
 * in accent with an accent-soft pill. Peers badges the offline
 * count; Activity dots when an unread error event exists. Both
 * clear on opening the tab.
 */
export function BottomNav() {
  const location = useLocation();
  const mesh = useMesh();
  const events = useSessionEvents((s) => s.events);
  const [activitySeenAt, setActivitySeenAt] = useState(() => Date.now());
  const [peersSeenAt, setPeersSeenAt] = useState(0);

  useEffect(() => {
    if (location.pathname === "/activity") setActivitySeenAt(Date.now());
    if (location.pathname === "/peers") setPeersSeenAt(Date.now());
  }, [location.pathname]);

  const offlineCount = mesh.model.nodes.filter((node) => node.kind === "peer" && node.state !== "up").length;
  const peersBadge = location.pathname === "/peers" ? 0 : offlineCount;
  // Peers badge also covers "a new peer joined since last view":
  // a join is visible as a peer whose first-seen is after the last
  // visit; the mesh snapshot has no first-seen, so the offline count
  // is the badge and joins surface on Home's event strip instead.
  void peersSeenAt;
  const activityDot = events.some((e) => errorTypes.has(e.type) && e.time_ms > activitySeenAt);

  return (
    <nav
      aria-label="Primary"
      className="fixed inset-x-0 bottom-0 z-40 border-t border-[var(--border)] bg-[var(--surface)]"
      style={{ height: "var(--nav-h)", paddingBottom: "env(safe-area-inset-bottom, 0px)" }}
    >
      <div className="grid h-[64px] grid-cols-5">
        {tabs.map(({ to, label, icon: Icon, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            aria-label={label}
            className={({ isActive }) =>
              cn(
                "relative flex min-h-[48px] flex-col items-center justify-center gap-0.5 text-[11px] font-semibold transition-colors active:bg-[var(--surface-raised)]",
                isActive ? "text-[var(--accent)]" : "text-[var(--text-muted)]",
              )
            }
          >
            {({ isActive }) => (
              <>
                <span
                  className={cn(
                    "flex h-8 w-14 items-center justify-center rounded-full",
                    isActive && "bg-[var(--accent-soft)]",
                  )}
                >
                  <Icon size={20} />
                </span>
                <span className="leading-none">{label}</span>
                {label === "Peers" && peersBadge > 0 && (
                  <span className="absolute right-3 top-1 flex h-5 min-w-5 items-center justify-center rounded-full bg-[var(--danger)] px-1 text-[10px] font-bold text-white">
                    {peersBadge}
                  </span>
                )}
                {label === "Activity" && activityDot && (
                  <span className="absolute right-4 top-2 h-2.5 w-2.5 rounded-full bg-[var(--danger)]" aria-label="Unread error" />
                )}
              </>
            )}
          </NavLink>
        ))}
      </div>
    </nav>
  );
}
