import { NavLink } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Activity, ArrowLeftRight, Info, LayoutDashboard, ListTree, Radio, ScrollText, Settings, Shield, Users } from "lucide-react";
import { AppService } from "@/lib/api";
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
  { to: "/about", label: "About", icon: Info },
];

export function Sidebar() {
  // The footer version is live (AppService.Version binding), not a hardcoded
  // string; an unread version is "—" so a failed query never looks like an
  // unstamped build.
  const version = useQuery({ queryKey: ["app-version"], queryFn: () => AppService.Version() });
  const versionLabel = version.data ?? (version.isError ? "—" : "…");
  // The collapsed rail is 72 px wide: the full build string would wrap one
  // character per line, so it shows the release line and keeps the exact
  // build in the title/About page.
  const release = versionLabel.split(/[+\s]/)[0].replace(/-dev$/, "");
  const collapsedVersion = release && release !== "—" && release !== "…" ? `v${release}` : versionLabel;
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
            aria-label={collapsed ? label : undefined}
            className={({ isActive }) => cn(
              "flex h-10 items-center gap-3 rounded-md px-3 text-sm transition-colors",
              collapsed && "justify-center px-0",
              isActive ? "bg-[var(--accent-soft)] text-[var(--text)]" : "text-[var(--text-muted)] hover:bg-[var(--surface-raised)] hover:text-[var(--text)]",
            )}
          >
            <Icon size={17} aria-hidden />
            {!collapsed && <span>{label}</span>}
          </NavLink>
        ))}
      </nav>
      <div className={cn("border-t border-[var(--border)] p-3", collapsed && "text-center")} title={collapsed ? versionLabel : undefined}>
        <div className="mono-label">{collapsed ? collapsedVersion : "Skein Desktop"}</div>
        {!collapsed && <div className="mt-1 font-mono text-[10px] text-[var(--text-faint)]">{versionLabel}</div>}
        {!collapsed && <div className="mt-1 text-xs text-[var(--text-faint)]">mesh control surface</div>}
      </div>
    </aside>
  );
}
