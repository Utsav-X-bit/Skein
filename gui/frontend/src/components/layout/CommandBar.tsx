import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { Command, Power, RefreshCw } from "lucide-react";
import { ConfigService, ConnectionService, NetworkService, ServiceService, errText, isOffline } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { StatusDot, type StatusTone } from "@/components/ui/status-dot";
import { useUI } from "@/lib/store";
import { NotificationsPopover } from "./NotificationsPopover";
import { toast } from "sonner";

export function CommandBar({ onPalette }: { onPalette: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const label = useUI((s) => s.networkLabel);
  // Connection state arrives as a `connection:changed` event, so this is a
  // reconciliation read, not the app's live clock: one interval while visible.
  const status = useQuery({ queryKey: ["status"], queryFn: () => NetworkService.Status(), refetchInterval: 30_000 });
  const config = useQuery({ queryKey: ["config"], queryFn: () => ConfigService.Get(), retry: 0, refetchInterval: 30_000 });
  const departed = config.data?.departed === true;
  const start = useMutation({
    mutationFn: () => ServiceService.Start(),
    onError: () => toast.error("The service could not be started.", { action: { label: "Service settings", onClick: () => navigate("/settings") } }),
    onSettled: () => { for (const key of ["service", "status", "config"]) void qc.invalidateQueries({ queryKey: [key] }); },
  });
  const toggle = useMutation({
    mutationFn: async () => {
      if (departed) { navigate("/settings?network=rejoin"); return "recovery"; }
      const state = status.data?.connection?.state;
      if (state === "paused") {
        await ConnectionService.Connect();
        return "resumed";
      }
      await ConnectionService.Disconnect();
      return "paused";
    },
    onSuccess: (action) => { if (action !== "recovery") toast.success(action === "resumed" ? "Mesh resumed" : "Mesh paused"); },
    onError: () => toast.error("The mesh state could not be changed.", { action: { label: "Recover connection", onClick: () => navigate("/settings?network=rejoin") } }),
    onSettled: () => { void qc.invalidateQueries({ queryKey: ["status"] }); void qc.invalidateQueries({ queryKey: ["config"] }); },
  });

  const conn = status.data?.connection?.state;
  const offline = status.isError && isOffline(status.error);
  const tone: StatusTone = offline ? "error" : conn === "connected" ? "up" : conn === "paused" ? "paused" : conn === "error" ? "error" : "down";
  const stateText = offline ? "Daemon offline" : departed ? "Left network" : conn === "connected" ? "Connected" : conn === "paused" ? "Paused" : conn === "error" ? "Error" : "Connecting";

  return (
    <header className="flex h-14 shrink-0 items-center justify-between gap-4 border-b border-[var(--border)] bg-[var(--surface)] px-4 drag">
      <div className="flex min-w-0 items-center gap-3 no-drag">
        <StatusDot tone={tone} pulse={!offline && conn !== "paused"} />
        <div className="min-w-0">
          <div className="truncate text-sm font-medium">{label}</div>
          <div className="mono-label truncate">{stateText}{status.data?.overlay_ipv4 ? ` · ${status.data.overlay_ipv4}` : ""}</div>
        </div>
      </div>
      <div className="flex items-center gap-2 no-drag">
        <NotificationsPopover />
        <Button variant="secondary" size="sm" onClick={onPalette} aria-label="Open command palette">
          <Command size={15} /> <span className="hidden sm:inline">Command</span>
          <kbd className="ml-1 rounded border border-[var(--border)] px-1 font-mono text-[10px] text-[var(--text-faint)]">⌘K</kbd>
        </Button>
        <Button
          size="sm"
          variant={conn === "paused" ? "default" : "secondary"}
          onClick={() => offline ? start.mutate() : departed ? navigate("/settings?network=rejoin") : toggle.mutate()}
          disabled={toggle.isPending || start.isPending || (!offline && !status.data)}
          title={offline ? "Start the Skein service" : departed ? "Rejoin with a fresh invite, or switch network" : conn === "paused" ? "Resume the mesh" : "Pause the mesh"}
        >
          {toggle.isPending ? <RefreshCw size={15} className="animate-spin" /> : <Power size={15} />}
          {offline ? "Start service" : departed ? "Rejoin network" : conn === "paused" ? "Connect" : "Disconnect"}
        </Button>
      </div>
      {toggle.isError && <div className="sr-only" role="alert">{errText(toggle.error)}</div>}
    </header>
  );
}
