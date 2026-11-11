import { useEffect, useState, type ReactNode } from "react";
import { Link, Outlet, useLocation } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Sidebar } from "./Sidebar";
import { CommandBar } from "./CommandBar";
import { CommandPalette } from "./CommandPalette";
import { useTheme } from "@/lib/useTheme";
import { ConfigService, NetworkService, ServiceService, errText, isOffline } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { toast } from "sonner";
import { Onboarding } from "@/routes/onboarding/Onboarding";
import { useHistoryRecorder } from "@/features/activity/useHistoryRecorder";
import { useDaemonEvents } from "@/features/mesh/useDaemonEvents";
import { nextSetupGate, setupOwnsWindow, type SetupGate } from "./onboardingGate";

/** One shell banner: a single fact and the single action that owns it. */
function ShellBanner({ tone, children, action }: { tone: "warning" | "danger"; children: ReactNode; action: ReactNode }) {
  const border = tone === "danger" ? "border-[var(--danger)]/40 bg-[var(--danger)]/5 text-[var(--danger)]" : "border-[var(--warning)]/40 bg-[var(--warning)]/5 text-[var(--warning)]";
  return (
    <div className={`flex flex-wrap items-center gap-x-3 gap-y-1 border-b px-4 py-2 text-xs ${border}`} role="status">
      <span className="min-w-0 flex-1">{children}</span>
      {action}
    </div>
  );
}

export function AppShell() {
  useTheme();
  // One writer for the whole app: opted-in history collects while the app
  // runs, not only while the Activity page is mounted.
  useHistoryRecorder();
  // The one event subscription in the app: typed snapshots write the query
  // cache, lifecycle events feed the session store. Pages never subscribe.
  useDaemonEvents();
  const location = useLocation();
  const qc = useQueryClient();
  const [palette, setPalette] = useState(false);
  // Setup owns the window from the moment the user starts it until the flow
  // closes itself (onboardingGate.ts). It must not follow the live service
  // state: the bootstrap helper registers and starts the service before the
  // GUI posts its token, so "missing" turns false mid-setup and the old gate
  // unmounted this screen — taking the once-only join string with it.
  const [setupGate, setSetupGate] = useState<SetupGate>("auto");
  const svc = useQuery({ queryKey: ["service"], queryFn: () => ServiceService.Status(), refetchInterval: 5_000 });
  const serviceMissing = svc.data?.state === "missing";
  // Observers only: the command bar owns the interval for these keys, and the
  // daemon pushes `connection:changed` / `peers:changed` on top of it.
  const config = useQuery({ queryKey: ["config"], queryFn: () => ConfigService.Get(), retry: 0 });
  const status = useQuery({ queryKey: ["status"], queryFn: () => NetworkService.Status(), retry: 0 });
  const start = useMutation({
    mutationFn: () => ServiceService.Start(),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["service"] }),
    onError: (e) => toast.error(errText(e)),
  });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); setPalette((v) => !v); }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // A finished flow re-arms once the service is installed again, so a later
  // uninstall shows setup instead of a shell whose daemon is not there.
  useEffect(() => {
    if (!svc.isSuccess) return;
    const next = nextSetupGate(setupGate, serviceMissing);
    if (next !== setupGate) setSetupGate(next);
  }, [setupGate, serviceMissing, svc.isSuccess]);

  // Step 8: do not lock stopped-service users out of the shell. The
  // old gate showed Onboarding whenever the service was not running
  // — including while the status query was still loading, and for an
  // installed-but-stopped service whose Settings/Security/Diagnostics
  // the user needs precisely then. Onboarding shows when the service is
  // known to be uninstalled, and stays while the user's own setup flow
  // runs or holds a result; while loading, show a skeleton; when
  // installed but stopped, show the shell with a banner (the pages
  // themselves handle the offline daemon).
  if (svc.isLoading) {
    return <div className="h-full bg-[var(--bg)] p-6"><div className="h-64 animate-pulse rounded-lg bg-[var(--surface-raised)]" /></div>;
  }
  if (setupOwnsWindow(setupGate, serviceMissing, location.pathname)) {
    return (
      <div className="h-full overflow-auto bg-[var(--bg)] p-6 text-[var(--text)]">
        <Onboarding onBegin={() => setSetupGate("open")} onExit={() => setSetupGate("closed")} />
      </div>
    );
  }

  // Three independent facts, never inferred from one another: membership
  // (left the network), service lifecycle, and connectivity (daemon reachable
  // / connection healthy).
  const departed = config.data?.departed === true;
  const serviceState = svc.data?.state ?? "unknown";
  const serviceDown = serviceState !== "running" && serviceState !== "missing";
  const offline = status.isError && isOffline(status.error);
  const connectionError = !offline && status.data?.connection?.state === "error";
  const connectionDetail = status.data?.connection?.last_error;

  return (
    <div className="flex h-full min-h-0 bg-[var(--bg)] text-[var(--text)]">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        <CommandBar onPalette={() => setPalette(true)} />
        {departed && (
          <ShellBanner tone="warning" action={<Link className="underline" to="/settings?network=rejoin">Rejoin network</Link>}>
            This device has left the network. Peers and routes below are the last known state.
          </ShellBanner>
        )}
        {serviceDown && (
          <ShellBanner
            tone={serviceState === "error" ? "danger" : "warning"}
            action={
              serviceState === "stopped" ? (
                <Button size="sm" variant="secondary" disabled={start.isPending} onClick={() => start.mutate()}>
                  {start.isPending ? "Starting…" : "Start service"}
                </Button>
              ) : (
                <Link className="underline" to="/settings">Service controls</Link>
              )
            }
          >
            {serviceState === "error"
              ? `Service is in an error state${svc.data?.last_error ? `: ${svc.data.last_error}` : "."}`
              : `Service is ${serviceState}. Settings and Diagnostics stay available; daemon-backed pages show the last known state.`}
          </ShellBanner>
        )}
        {offline && (
          <ShellBanner tone="danger" action={<Link className="underline" to="/settings">Service controls</Link>}>
            The daemon is not reachable. Nothing on this screen is live until it is back.
          </ShellBanner>
        )}
        {connectionError && (
          <ShellBanner tone="danger" action={<Link className="underline" to="/settings?network=rejoin">Recovery</Link>}>
            {`Connection error${connectionDetail ? `: ${connectionDetail}` : "."}`}
          </ShellBanner>
        )}
        <main className="min-h-0 flex-1 overflow-auto p-4 md:p-6">
          <Outlet />
        </main>
      </div>
      <CommandPalette open={palette} onOpenChange={setPalette} />
    </div>
  );
}
