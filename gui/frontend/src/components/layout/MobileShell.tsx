import { useEffect, useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ChevronLeft } from "lucide-react";
import { displayNodeName } from "@/lib/utils";
import { MobileAppBar } from "./MobileAppBar";
import { BottomNav } from "./BottomNav";
import { StateScreen } from "./StateScreen";
import { MobileOnboarding } from "@/routes/onboarding/MobileOnboarding";
import { useTheme } from "@/lib/useTheme";
import { ConfigService, ConnectionService, NetworkService, ServiceService, errText, isOffline } from "@/lib/api";
import { useHistoryRecorder } from "@/features/activity/useHistoryRecorder";
import { useLiveSampler } from "@/features/mesh/useMesh";
import { useDaemonEvents } from "@/features/mesh/useDaemonEvents";
import { nextSetupGate, setupOwnsWindow, type SetupGate } from "./onboardingGate";

/**
 * MobileShell is the Android shell (design brief §3, §5.4): app bar,
 * tab content, bottom nav — and, unlike the desktop AppShell, every
 * state is a full screen with one primary button instead of a banner.
 * The tab bar is hidden during Onboarding and state screens.
 *
 * The three facts stay independent, as on desktop: membership
 * (departed), service lifecycle, and connectivity. One screen at a
 * time, in that order — departed first, then service down, then
 * paused. A connection error is not a full screen: Home owns the
 * Reconnect button for it. "No network" is the onboarding gate
 * (service missing ⇒ setup owns the window), not a state screen.
 */
export function MobileShell() {
  useTheme();
  useHistoryRecorder();
  useLiveSampler(true);
  useDaemonEvents();
  const location = useLocation();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [setupGate, setSetupGate] = useState<SetupGate>("auto");

  const svc = useQuery({ queryKey: ["service"], queryFn: () => ServiceService.Status(), refetchInterval: 5_000 });
  const serviceMissing = svc.data?.state === "missing";
  const config = useQuery({ queryKey: ["config"], queryFn: () => ConfigService.Get(), retry: 0 });
  const status = useQuery({ queryKey: ["status"], queryFn: () => NetworkService.Status(), retry: 0 });

  const start = useMutation({
    mutationFn: () => ServiceService.Start(),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["service"] }),
    onError: (e) => toast.error(errText(e)),
  });
  const resume = useMutation({
    mutationFn: () => ConnectionService.Connect(),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["status"] }),
    onError: (e) => toast.error(errText(e)),
  });
  const stop = useMutation({
    mutationFn: () => ServiceService.Stop(),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["service"] }),
    onError: (e) => toast.error(errText(e)),
  });

  // A finished flow re-arms once the service is installed again, so a later
  // uninstall shows setup instead of a shell whose daemon is not there.
  useEffect(() => {
    if (!svc.isSuccess) return;
    const next = nextSetupGate(setupGate, serviceMissing);
    if (next !== setupGate) setSetupGate(next);
  }, [setupGate, serviceMissing, svc.isSuccess]);

  if (svc.isLoading) {
    return (
      <div
        className="h-full bg-[var(--bg)] p-6"
        style={{ paddingTop: "calc(24px + var(--sat))", paddingBottom: "calc(24px + var(--sab))" }}
      >
        <div className="h-64 animate-pulse rounded-lg bg-[var(--surface-raised)]" />
      </div>
    );
  }
  if (setupOwnsWindow(setupGate, serviceMissing, location.pathname)) {
    // Onboarding has no app bar of its own, so this container owns
    // the window insets: the top padding clears the Android status
    // bar (edge-to-edge WebView starts at y=0) and the bottom
    // padding clears the gesture bar. Without the insets the step
    // pills and Back button sit on top of the status bar.
    return (
      <div
        className="h-full overflow-auto bg-[var(--bg)] px-6 text-[var(--text)]"
        style={{ paddingTop: "calc(24px + var(--sat))", paddingBottom: "calc(24px + var(--sab))" }}
      >
        <MobileOnboarding onBegin={() => setSetupGate("open")} onExit={() => setSetupGate("closed")} />
      </div>
    );
  }

  const departed = config.data?.departed === true;
  const serviceState = svc.data?.state ?? "unknown";
  const serviceDown = serviceState !== "running" && serviceState !== "missing";
  const offline = status.isError && isOffline(status.error);
  const paused = status.data?.connection?.state === "paused";
  // The app bar shows the node name (no network name exists in the
  // API) and the live connection dot.
  const appBar = (
    <MobileAppBar networkName={config.data?.name ? displayNodeName(config.data.name) : "Skein"} connected={status.data?.connection?.state === "connected"} />
  );

  if (departed) {
    return (
      <div className="flex h-full flex-col bg-[var(--bg)] text-[var(--text)]">
        {appBar}
        <main className="flex-1 overflow-auto p-4">
          <StateScreen
            title="You left this network"
            message="This device left the mesh. Peers and routes below are the last known state — nothing here is live until you rejoin."
            primaryLabel="Rejoin this network"
            onPrimary={() => navigate("/settings?network=rejoin")}
            secondaryLabel="Switch to a different network"
            onSecondary={() => navigate("/settings")}
          />
        </main>
      </div>
    );
  }

  if (serviceDown || offline) {
    return (
      <div className="flex h-full flex-col bg-[var(--bg)] text-[var(--text)]">
        {appBar}
        <main className="flex-1 overflow-auto p-4">
          <StateScreen
            title="Skein isn't running"
            message={
              serviceState === "error" && svc.data?.last_error
                ? `The VPN service is in an error state: ${svc.data.last_error}`
                : "The VPN service is stopped. The mesh, peers and activity are unavailable until it starts."
            }
            primaryLabel="Start Skein"
            onPrimary={() => start.mutate()}
            primaryBusy={start.isPending}
            secondaryLabel="Settings"
            onSecondary={() => navigate("/settings")}
          />
        </main>
      </div>
    );
  }

  if (paused) {
    return (
      <div className="flex h-full flex-col bg-[var(--bg)] text-[var(--text)]">
        {appBar}
        <main className="flex-1 overflow-auto p-4">
          <StateScreen
            title="Mesh paused"
            message="The dataplane is stopped but the VPN is still up. Resume to reconnect, or disconnect fully to stop the VPN service."
            primaryLabel="Resume"
            onPrimary={() => resume.mutate()}
            primaryBusy={resume.isPending}
            secondaryLabel="Disconnect fully"
            onSecondary={() => stop.mutate()}
          />
        </main>
      </div>
    );
  }

  // Sub-pages (Routes, Security, Diagnostics, About) are reached
  // from Settings and have no back affordance of their own — the
  // bottom nav only goes to the five tabs, and Android Back exits
  // the app. A back row above the content returns to Settings.
  const isSubPage = ["/network", "/security", "/logs", "/about"].includes(location.pathname);

  return (
    <div className="flex h-full min-h-0 flex-col bg-[var(--bg)] text-[var(--text)]">
      {appBar}
      {isSubPage && (
        <button
          type="button"
          onClick={() => navigate("/settings")}
          className="flex shrink-0 items-center gap-0.5 px-4 pb-1 pt-3 text-sm text-[var(--text-muted)]"
        >
          <ChevronLeft size={16} /> Back to Settings
        </button>
      )}
      <main className="min-h-0 flex-1 overflow-auto px-4 pb-[calc(var(--nav-h)+16px)] pt-4">
        <Outlet />
      </main>
      <BottomNav />
    </div>
  );
}
