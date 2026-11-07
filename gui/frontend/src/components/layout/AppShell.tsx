import { useEffect, useState } from "react";
import { Link, Outlet, useLocation } from "react-router-dom";
import { Sidebar } from "./Sidebar";
import { CommandBar } from "./CommandBar";
import { CommandPalette } from "./CommandPalette";
import { useTheme } from "@/lib/useTheme";
import { useQuery } from "@tanstack/react-query";
import { ServiceService } from "@/lib/api";
import { Onboarding } from "@/routes/onboarding/Onboarding";
import { useDaemonEvents } from "@/features/mesh/useDaemonEvents";

export function AppShell() {
  useTheme();
  // The one event subscription in the app: typed snapshots write the query
  // cache, lifecycle events feed the session store. Pages never subscribe.
  useDaemonEvents();
  const location = useLocation();
  const [palette, setPalette] = useState(false);
  const svc = useQuery({ queryKey: ["service"], queryFn: () => ServiceService.Status(), refetchInterval: 5_000 });
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); setPalette((v) => !v); }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  // Step 8: do not lock stopped-service users out of the shell. The
  // old gate showed Onboarding whenever the service was not running
  // — including while the status query was still loading, and for an
  // installed-but-stopped service whose Settings/Security/Diagnostics
  // the user needs precisely then. Onboarding shows only when the
  // service is known to be uninstalled; while loading, show a
  // skeleton; when installed but stopped, show the shell with a
  // banner (the pages themselves handle the offline daemon).
  if (svc.isLoading) {
    return <div className="h-full bg-[var(--bg)] p-6"><div className="h-64 animate-pulse rounded-lg bg-[var(--surface-raised)]" /></div>;
  }
  if (svc.data?.state === "missing" && location.pathname !== "/settings") {
    return (
      <div className="h-full overflow-auto bg-[var(--bg)] p-6 text-[var(--text)]">
        <Onboarding />
      </div>
    );
  }
  const stopped = svc.data && svc.data.state !== "running";
  return (
    <div className="flex h-full min-h-0 bg-[var(--bg)] text-[var(--text)]">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        <CommandBar onPalette={() => setPalette(true)} />
        {stopped && (
          <div className="border-b border-[var(--warning)]/40 bg-[var(--warning)]/5 px-4 py-2 text-xs text-[var(--warning)]">
            Service is {svc.data?.state ?? "not running"}. <Link className="underline" to="/settings">Open service controls</Link>
          </div>
        )}
        <main className="min-h-0 flex-1 overflow-auto p-4 md:p-6">
          <Outlet />
        </main>
      </div>
      <CommandPalette open={palette} onOpenChange={setPalette} />
    </div>
  );
}
