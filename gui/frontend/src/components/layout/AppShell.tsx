import { useEffect, useState } from "react";
import { Link, Outlet, useLocation } from "react-router-dom";
import { Sidebar } from "./Sidebar";
import { CommandBar } from "./CommandBar";
import { CommandPalette } from "./CommandPalette";
import { useTheme } from "@/lib/useTheme";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { NetworkService, ServiceService } from "@/lib/api";
import { Onboarding } from "@/routes/onboarding/Onboarding";
import { Events } from "@wailsio/runtime";
import { toast } from "sonner";

export function AppShell() {
  useTheme();
  const location = useLocation();
  const [palette, setPalette] = useState(false);
  const qc = useQueryClient();
  const svc = useQuery({ queryKey: ["service"], queryFn: () => ServiceService.Status(), refetchInterval: 5_000 });
  // Kept: it populates the shared ["status"] cache for CommandBar/Overview.
  useQuery({ queryKey: ["status"], queryFn: () => NetworkService.Status(), refetchInterval: 2_000, retry: 0 });
  // Step 8: subscribe the frontend to the daemon live-event pipeline.
  // The Go side already re-emits the SSE stream as Wails events
  // (gui/services/events.go); until now nothing listened, so probe
  // results — which arrive only as peer:probe events — could never
  // reach the UI. Each event invalidates the query it describes;
  // peer:probe additionally toasts the result, since the probe POST
  // itself only returns "pending".
  useEffect(() => {
    const subs = [
      Events.On("connection:changed", () => { void qc.invalidateQueries({ queryKey: ["status"] }); void qc.invalidateQueries({ queryKey: ["config"] }); }),
      Events.On("peers:changed", () => { void qc.invalidateQueries({ queryKey: ["peers"] }); void qc.invalidateQueries({ queryKey: ["config"] }); }),
      Events.On("routes:changed", () => qc.invalidateQueries({ queryKey: ["routes"] })),
      Events.On("activity:sample", () => qc.invalidateQueries({ queryKey: ["activities"] })),
      Events.On("log:entry", () => qc.invalidateQueries({ queryKey: ["logs"] })),
      Events.On("service:changed", () => qc.invalidateQueries({ queryKey: ["service"] })),
      Events.On("peer:probe", (event) => {
        const d = (event as { data?: { peer_id?: string; state?: string; rtt_ms?: number } }).data;
        if (d?.peer_id) {
          toast.info(`Probe ${d.peer_id} ${d.state ?? ""}${d.rtt_ms ? ` · ${d.rtt_ms.toFixed(1)} ms` : ""}`.trim());
        }
        qc.invalidateQueries({ queryKey: ["peers"] });
      }),
    ];
    return () => { for (const off of subs) off(); };
  }, [qc]);
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
