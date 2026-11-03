import { NavLink } from "react-router-dom";
import { Activity, ArrowLeftRight, LayoutDashboard, ListTree, Radio, ScrollText, Settings, Shield, Users } from "lucide-react";
import { LogoMark } from "@/components/brand/LogoMark";
import { Wordmark } from "@/components/brand/Wordmark";
import { cn } from "@/lib/utils";
import { useUI } from "@/lib/store";

const items = [
  { to: "/", label: "Overview", icon: LayoutDashboard, end: true },
  { to: "/network", label: "Network", icon: ListTree },
  { to: "/peers", label: "Peers", icon: Users },
  { to: "/invites", label: "Invitations", icon: Radio },
  { to: "/security", label: "Security", icon: Shield },
  { to: "/activity", label: "Activity", icon: Activity },
  { to: "/logs", label: "Diagnostics", icon: ScrollText },
  { to: "/settings", label: "Settings", icon: Settings },
];

export function Sidebar() {
  const collapsed = useUI((s) => s.sidebarCollapsed);
  const toggle = useUI((s) => s.toggleSidebar);
  return (
    <aside className={cn("flex h-full shrink-0 flex-col border-r border-[var(--border)] bg-[var(--surface)] transition-[width] duration-200", collapsed ? "w-[72px]" : "w-[232px]")}>
      <div className={cn("flex h-14 items-center border-b border-[var(--border)] px-4", collapsed ? "justify-center" : "gap-3")}>
        <button onClick={toggle} className="rounded p-1 text-[var(--text-muted)] hover:text-[var(--text)]" aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}>
          <ArrowLeftRight size={16} className={collapsed ? "rotate-180" : ""} />
        </button>
        {!collapsed && <><LogoMark size={56} /><Wordmark /></>}
      </div>
      <nav className="flex-1 space-y-1 p-2" aria-label="Primary">
        {items.map(({ to, label, icon: Icon, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            title={collapsed ? label : undefined}
            className={({ isActive }) => cn(
              "flex h-10 items-center gap-3 rounded-md px-3 text-sm transition-colors",
              collapsed && "justify-center px-0",
              isActive ? "bg-[var(--accent-soft)] text-[var(--text)]" : "text-[var(--text-muted)] hover:bg-[var(--surface-raised)] hover:text-[var(--text)]",
            )}
          >
            <Icon size={17} />
            {!collapsed && <span>{label}</span>}
          </NavLink>
        ))}
      </nav>
      <div className={cn("border-t border-[var(--border)] p-3", collapsed && "text-center")}>
        <div className="mono-label">{collapsed ? "v0.1" : "Skein Desktop"}</div>
        {!collapsed && <div className="mt-1 text-xs text-[var(--text-faint)]">mesh control surface</div>}
      </div>
    </aside>
  );
}
