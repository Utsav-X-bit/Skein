import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Compass, Route as RouteIcon, ScrollText, Users, Zap } from "lucide-react";
import { toast } from "sonner";
import {
  ConfigService,
  ConnectionService,
  NetworkService,
  PeerService,
  ServiceService,
  errText,
  type LogEntry,
} from "@/lib/api";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { useLive } from "@/features/mesh/useDaemonEvents";
import { cn } from "@/lib/utils";
import { buildCommands, type CommandGroup } from "./palette";

const groupIcon: Record<CommandGroup, typeof Compass> = {
  navigate: Compass,
  action: Zap,
  peer: Users,
  route: RouteIcon,
  log: ScrollText,
};

// A stable empty array keeps the closed palette from re-rendering on every
// log line: zustand compares the selected value by identity.
const NoLogs: LogEntry[] = [];

/**
 * CommandPalette is a combobox over navigation, the actions the pages expose,
 * and the live peers, routes and log lines. Destructive actions (rotate, reset)
 * are not re-implemented here: they jump to the page that owns the confirm.
 */
export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();
  const qc = useQueryClient();
  const logs = useLive((s) => (open ? s.logs : NoLogs));

  const peers = useQuery({ queryKey: ["peers"], queryFn: () => PeerService.List(), enabled: open, staleTime: 5_000, retry: 0 });
  const routes = useQuery({ queryKey: ["routes"], queryFn: () => NetworkService.Routes(), enabled: open, staleTime: 5_000, retry: 0 });
  const status = useQuery({ queryKey: ["status"], queryFn: () => NetworkService.Status(), enabled: open, staleTime: 5_000, retry: 0 });
  const config = useQuery({ queryKey: ["config"], queryFn: () => ConfigService.Get(), enabled: open, staleTime: 15_000, retry: 0 });

  const service = useMutation({
    mutationFn: (verb: "start" | "stop" | "restart") =>
      verb === "start" ? ServiceService.Start() : verb === "stop" ? ServiceService.Stop() : ServiceService.Restart(),
    onSuccess: async () => {
      const next = await ServiceService.Status();
      qc.setQueryData(["service"], next);
      toast.success(`Service is now ${next.state}.`);
    },
    onError: (e) => toast.error(errText(e)),
  });
  const connection = useMutation({
    mutationFn: (verb: "connect" | "disconnect") =>
      verb === "connect" ? ConnectionService.Connect() : ConnectionService.Disconnect(),
    onSuccess: (_result, verb) => {
      toast.success(verb === "connect" ? "Mesh resumed" : "Mesh paused");
      void qc.invalidateQueries({ queryKey: ["status"] });
    },
    onError: () =>
      toast.error("The mesh state could not be changed.", {
        action: { label: "Recover connection", onClick: () => navigate("/settings?network=rejoin") },
      }),
  });
  const probe = useMutation({
    mutationFn: (peerId: string) => PeerService.Probe(peerId),
    onSuccess: (result) => {
      const rtt = result.rtt_ms !== undefined && result.rtt_ms > 0 ? ` · ${result.rtt_ms.toFixed(1)} ms` : "";
      toast(`Probe: ${result.state}${rtt}${result.error ? ` · ${result.error}` : ""}`);
    },
    onError: (e) => toast.error(errText(e)),
  });
  const ping = useMutation({
    mutationFn: (ip: string) => NetworkService.Ping(ip),
    onSuccess: (result) => {
      toast(`Ping ${result.address}: ${result.reachable ? "reachable" : "unreachable"} (exit ${result.exit_code})`);
    },
    onError: (e) => toast.error(errText(e)),
  });

  const commands = useMemo(
    () =>
      buildCommands(
        {
          peers: peers.data ?? [],
          routes: routes.data ?? [],
          logs,
          connection: status.data?.connection?.state ?? "unknown",
          departed: config.data?.departed === true,
          navigate: (to) => navigate(to),
          connect: () => connection.mutate("connect"),
          disconnect: () => connection.mutate("disconnect"),
          startService: () => service.mutate("start"),
          stopService: () => service.mutate("stop"),
          restartService: () => service.mutate("restart"),
          probe: (peerId) => probe.mutate(peerId),
          ping: (peer) => {
            if (peer.overlay_ipv4) ping.mutate(peer.overlay_ipv4);
          },
        },
        q,
      ),
    // The mutate functions are stable; the data and the query drive the list.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [peers.data, routes.data, logs, status.data, config.data, q, navigate],
  );

  const activeIndex = commands.length > 0 ? Math.min(active, commands.length - 1) : -1;

  useEffect(() => {
    if (!open) setQ("");
  }, [open]);
  useEffect(() => setActive(0), [q]);
  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>(`[data-index="${activeIndex}"]`)?.scrollIntoView({ block: "nearest" });
  }, [activeIndex, commands.length]);

  const run = (index: number) => {
    const command = commands[index];
    if (!command) return;
    onOpenChange(false);
    command.run();
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (commands.length === 0) return;
      const step = e.key === "ArrowDown" ? 1 : -1;
      setActive((i) => (Math.min(i, commands.length - 1) + step + commands.length) % commands.length);
      return;
    }
    if (e.key === "Enter") {
      e.preventDefault();
      run(activeIndex);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="p-0" aria-describedby={undefined}>
        <DialogTitle className="sr-only">Command palette</DialogTitle>
        <Input
          autoFocus
          role="combobox"
          aria-expanded
          aria-controls="palette-listbox"
          aria-autocomplete="list"
          aria-label="Search commands, peers, routes and log lines"
          aria-activedescendant={activeIndex >= 0 ? `palette-option-${activeIndex}` : undefined}
          placeholder="Jump to a page, peer, route or log line…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={onKeyDown}
          className="h-11 rounded-none border-0 border-b border-[var(--border)] bg-transparent pr-12 focus-visible:ring-0"
        />
        <div id="palette-listbox" role="listbox" aria-label="Commands" ref={listRef} className="max-h-[min(60vh,420px)] overflow-auto p-2">
          {commands.map((command, index) => {
            const Icon = groupIcon[command.group];
            return (
              <div
                key={command.id}
                id={`palette-option-${index}`}
                data-index={index}
                role="option"
                aria-selected={index === activeIndex}
                onMouseMove={() => setActive(index)}
                onClick={() => run(index)}
                className={cn(
                  "flex cursor-pointer items-center gap-3 rounded-md px-3 py-2 text-sm",
                  index === activeIndex ? "bg-[var(--surface-raised)] text-[var(--text)]" : "text-[var(--text-muted)]",
                )}
              >
                <Icon size={16} aria-hidden className="shrink-0 text-[var(--text-faint)]" />
                <span className="min-w-0 flex-1 truncate">{command.label}</span>
                <span className="shrink-0 truncate text-xs text-[var(--text-faint)]">{command.hint}</span>
              </div>
            );
          })}
          {commands.length === 0 && (
            <div className="px-3 py-8 text-center text-xs text-[var(--text-faint)]">No matching commands, peers, routes or log lines.</div>
          )}
        </div>
        <div className="flex items-center justify-between border-t border-[var(--border)] px-3 py-2 text-[11px] text-[var(--text-faint)]">
          <span>{commands.length} result{commands.length === 1 ? "" : "s"}</span>
          <span className="font-mono">↑↓ move · ↵ run · esc close</span>
        </div>
      </DialogContent>
    </Dialog>
  );
}
