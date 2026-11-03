import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Command, Power, RefreshCw } from "lucide-react";
import { ConnectionService, NetworkService, errText, isOffline } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { StatusDot, type StatusTone } from "@/components/ui/status-dot";
import { useUI } from "@/lib/store";
import { cn } from "@/lib/utils";

export function CommandBar({ onPalette }: { onPalette: () => void }) {
  const qc = useQueryClient();
  const label = useUI((s) => s.networkLabel);
  const status = useQuery({ queryKey: ["status"], queryFn: () => NetworkService.Status(), refetchInterval: 2_000 });
  const toggle = useMutation({
    mutationFn: async () => {
      const state = status.data?.connection?.state;
      if (state === "paused") await ConnectionService.Connect();
      else await ConnectionService.Disconnect();
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["status"] }),
  });

  const conn = status.data?.connection?.state;
  const offline = status.isError && isOffline(status.error);
  const tone: StatusTone = offline ? "error" : conn === "connected" ? "up" : conn === "paused" ? "paused" : conn === "error" ? "error" : "down";
  const stateText = offline ? "Daemon offline" : conn === "connected" ? "Connected" : conn === "paused" ? "Paused" : conn === "error" ? "Error" : "Connecting";

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
        <Button variant="secondary" size="sm" onClick={onPalette} aria-label="Open command palette">
          <Command size={15} /> <span className="hidden sm:inline">Command</span>
          <kbd className="ml-1 rounded border border-[var(--border)] px-1 font-mono text-[10px] text-[var(--text-faint)]">⌘K</kbd>
        </Button>
        <Button
          size="sm"
          variant={conn === "paused" ? "default" : "secondary"}
          onClick={() => toggle.mutate()}
          disabled={offline || toggle.isPending || !status.data}
          title={offline ? "Start the Skein service first" : conn === "paused" ? "Resume the mesh" : "Pause the mesh"}
        >
          {toggle.isPending ? <RefreshCw size={15} className="animate-spin" /> : <Power size={15} />}
          {conn === "paused" ? "Connect" : "Disconnect"}
        </Button>
      </div>
      {toggle.isError && <div className="sr-only" role="alert">{errText(toggle.error)}</div>}
    </header>
  );
}
