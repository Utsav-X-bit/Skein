import { useState, type ReactNode } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowDownRight, ArrowUpRight, Network, Radar, Route as RouteIcon, X } from "lucide-react";
import { toast } from "sonner";
import { NetworkService, PeerService, errKind, errText, type Peer, type Status } from "@/lib/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { formatBytes, formatDuration, shortID } from "@/lib/utils";
import { useLive } from "./useDaemonEvents";
import { PeerHealth } from "./PeerHealth";
import { useWideLayout } from "./hooks";
import {
  formatAge,
  formatBytesPerSec,
  linkFamily,
  presenceOf,
  routeSummary,
  type MeshModel,
  type Rate,
} from "./model";

/**
 * InspectorFrame is the shared shell: aside at desktop widths, Dialog at
 * 900×600 so the canvas stays usable.
 */
export function InspectorFrame({
  open,
  title,
  description,
  onClose,
  children,
}: {
  open: boolean;
  title: string;
  description: string;
  onClose: () => void;
  children: ReactNode;
}) {
  const wide = useWideLayout();
  if (!open) return null;

  if (wide) {
    return (
      <aside aria-label={title} className="w-[340px] shrink-0 self-start rounded-lg border border-[var(--border)] bg-[var(--surface)]">
        <div className="flex items-start justify-between gap-3 border-b border-[var(--border)] px-4 py-3">
          <div className="min-w-0">
            <div className="truncate text-sm font-medium">{title}</div>
            <div className="truncate text-xs text-[var(--text-muted)]">{description}</div>
          </div>
          <Button variant="ghost" size="icon" onClick={onClose} aria-label={`Close ${title}`}>
            <X size={16} />
          </Button>
        </div>
        <div className="max-h-[70vh] space-y-4 overflow-auto p-4">{children}</div>
      </aside>
    );
  }

  return (
    <Dialog open onOpenChange={(value) => { if (!value) onClose(); }}>
      <DialogContent className="max-h-[85vh] w-[min(92vw,640px)] overflow-auto">
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription>{description}</DialogDescription>
        <div className="mt-4 space-y-4">{children}</div>
      </DialogContent>
    </Dialog>
  );
}

/** PeerInspector exposes only fields the daemon reports and actions it supports. */
export function PeerInspector({
  peerId,
  status,
  peers,
  model,
  peerRates,
  lastTrafficAt,
  onClose,
  onViewRoutes,
}: {
  peerId: string | null;
  status: Status | null;
  peers: Peer[];
  model: MeshModel;
  peerRates: Record<string, Rate>;
  lastTrafficAt: Record<string, number>;
  onClose: () => void;
  onViewRoutes: (destination: string) => void;
}) {
  const qc = useQueryClient();
  const [confirmLeave, setConfirmLeave] = useState(false);
  const self = status?.id ?? "";
  const isSelf = peerId !== null && peerId === self;
  const peer = peerId ? peers.find((p) => p.id === peerId) ?? null : null;
  const node = peerId ? model.nodes.find((n) => n.id === peerId) ?? null : null;
  const rate = peerId ? peerRates[peerId] : undefined;
  const routes = peerId ? model.paths.filter((p) => p.destination === peerId || p.path.includes(peerId)) : [];
  const selected = peerId ? model.paths.find((p) => p.destination === peerId) : undefined;
  const presence = presenceOf(peer, peerId ? lastTrafficAt[peerId] : undefined, Date.now(), selected !== undefined && !selected.direct);
  const nameOf = (id: string) => peers.find((p) => p.id === id)?.name || shortID(id);
  const liveProbe = useLive((s) => (peerId ? s.probes[peerId] : undefined));

  const ping = useMutation({
    mutationFn: (ip: string) => NetworkService.Ping(ip),
    onError: (e) => toast.error(errText(e)),
  });
  const probe = useMutation({
    mutationFn: (id: string) => PeerService.Probe(id),
    onError: (e) => toast.error(errText(e)),
  });
  const leave = useMutation({
    mutationFn: () => PeerService.LeaveNetwork(),
    onSuccess: (r) => {
      toast.success(
        r.propagated
          ? "Left this network. Departure announced to reachable peers."
          : "Left locally. No peer was reachable to receive the announcement.",
        { action: { label: "Rejoin / Switch", onClick: () => { location.hash = "/settings?network=rejoin"; } } },
      );
      for (const key of ["peers", "routes", "status", "config"]) void qc.invalidateQueries({ queryKey: [key] });
    },
    onError: (e) => toast.error(`${errKind(e) ? errKind(e) + ": " : ""}${errText(e)}`),
    onSettled: () => setConfirmLeave(false),
  });

  if (!peerId) return null;

  const overlay = peer?.overlay_ipv4 || peer?.overlay_ipv6 || (isSelf ? status?.overlay_ipv4 : undefined);
  const probeResult = probe.data ?? liveProbe;
  const title = isSelf ? status?.name || "This device" : peer?.name || node?.label || shortID(peerId);
  // The badge describes the selected route, not the remote machine's power.
  const routeBadge = selected
    ? selected.direct
      ? peer?.direct && peer.state === "up"
        ? { tone: "accent" as const, label: "direct" }
        : { tone: "warning" as const, label: "no link" }
      : { tone: "relay" as const, label: "relay" }
    : peer
      ? peer.state === "up"
        ? { tone: "warning" as const, label: "no route" }
        : { tone: "neutral" as const, label: "no route" }
      : { tone: "neutral" as const, label: "unknown" };
  const family = linkFamily(peer);
  const directLink = peer?.direct
    ? peer.state === "up"
      ? `up${family ? ` · ${family}` : ""}${peer.rtt_ms > 0 ? ` · ${Math.round(peer.rtt_ms)} ms` : ""}`
      : `${peer.state}${family ? ` · ${family}` : ""}`
    : "not observed";
  const selectedText = selected
    ? selected.direct && !(peer?.direct && peer.state === "up")
      ? peer?.direct
        ? `direct · link ${peer.state}`
        : "direct · link not observed"
      : routeSummary(selected, nameOf)
    : "no route";

  return (
    <>
      <InspectorFrame
        open
        title={title}
        description={isSelf ? "This device" : "Peer inspector"}
        onClose={onClose}
      >
        {isSelf ? (
          <>
            <div className="space-y-1">
              <div className="mono-label">identity</div>
              <div className="font-mono text-xs break-all">{status?.id ?? "—"}</div>
              <div className="text-xs text-[var(--text-muted)]">{status?.overlay_ipv4 ?? "—"}{status?.overlay_ipv6 ? ` · ${status.overlay_ipv6}` : ""}</div>
            </div>
            <div className="grid grid-cols-2 gap-3 text-sm">
              <div><div className="mono-label">uptime</div><div className="tabular">{formatDuration(status?.uptime_seconds)}</div></div>
              <div><div className="mono-label">connection</div><div>{status?.connection?.state ?? "unknown"}</div></div>
              <div><div className="mono-label">peers up</div><div className="tabular">{peers.filter((p) => p.state === "up").length}/{peers.length}</div></div>
              <div><div className="mono-label">capacity</div><div className="tabular">{1 + peers.length} of 5</div></div>
            </div>
            <div>
              <div className="mono-label mb-1">membership</div>
              <Button variant="danger" size="sm" onClick={() => setConfirmLeave(true)} disabled={leave.isPending}>
                Leave network
              </Button>
              <p className="mt-2 text-xs text-[var(--text-muted)]">
                Leaves the mesh from this device only. It is not a ban and does not remove any other peer.
              </p>
            </div>
          </>
        ) : (
          <>
            <div className="flex items-center justify-between gap-3">
              <PeerHealth peer={peer} presence={presence} />
              <Badge tone={routeBadge.tone}>{routeBadge.label}</Badge>
            </div>

            <div className="space-y-1">
              <div className="mono-label">identity</div>
              <div className="font-mono text-xs break-all">{peerId}</div>
              <div className="text-xs text-[var(--text-muted)]">
                {peer?.overlay_ipv4 ?? "—"}{peer?.overlay_ipv6 ? ` · ${peer.overlay_ipv6}` : ""}
              </div>
            </div>

            <div className="grid grid-cols-2 gap-3 text-sm">
              <div><div className="mono-label">direct link</div><div className="text-xs">{directLink}</div></div>
              <div><div className="mono-label">selected route</div><div className="text-xs">{selectedText}</div></div>
              {selected && !selected.direct && peer?.direct && (
                <div className="col-span-2 text-xs text-[var(--text-muted)]">
                  The direct link is up but the selected route uses {nameOf(selected.nextHop)}.
                </div>
              )}
            </div>

            <div className="grid grid-cols-2 gap-3 text-sm">
              <div><div className="mono-label">state</div><div>{peer?.state ?? node?.state ?? "unknown"}</div></div>
              <div><div className="mono-label">transport</div><div>{peer?.transport || "unknown"}</div></div>
              <div className="col-span-2"><div className="mono-label">transport rx</div><div className="break-words text-xs">{peer?.transport_rx || "—"}</div></div>
              <div className="col-span-2"><div className="mono-label">endpoint</div><div className="break-words text-xs">{peer?.endpoint || "—"}</div></div>
              <div className="col-span-2">
                <div className="mono-label">candidates</div>
                {peer?.candidates && peer.candidates.length > 0 ? (
                  <ul className="space-y-0.5 text-xs">{peer.candidates.map((c) => <li key={c} className="break-all font-mono">{c}</li>)}</ul>
                ) : (
                  <div className="text-xs text-[var(--text-muted)]">none reported</div>
                )}
              </div>
            </div>

            <div className="grid grid-cols-2 gap-3 text-sm">
              <div><div className="mono-label">received</div><div className="tabular">{formatBytes(peer?.rx_bytes)}</div></div>
              <div><div className="mono-label">sent</div><div className="tabular">{formatBytes(peer?.tx_bytes)}</div></div>
              <div className="col-span-2">
                <div className="mono-label">rate</div>
                <div className="tabular flex flex-wrap items-center gap-3 text-xs">
                  <span className="inline-flex items-center gap-1"><ArrowDownRight size={13} />{rate ? formatBytesPerSec(rate.rxPerSec) : "—"}</span>
                  <span className="inline-flex items-center gap-1"><ArrowUpRight size={13} />{rate ? formatBytesPerSec(rate.txPerSec) : "—"}</span>
                </div>
              </div>
              <div><div className="mono-label">last seen</div><div>{peer && peer.last_seen_ms > 0 ? `${formatAge(peer.last_seen_ms)} ago` : "unknown"}</div></div>
              <div><div className="mono-label">session start</div><div className="text-xs">{peer?.session_start_ms ? new Date(peer.session_start_ms).toLocaleString() : "not observed"}</div></div>
              <div><div className="mono-label">last handshake</div><div className="text-xs">{peer?.last_handshake_ms ? new Date(peer.last_handshake_ms).toLocaleString() : "not observed"}</div></div>
            </div>

            <div>
              <div className="mono-label mb-1">routes via this peer</div>
              {routes.length === 0 ? (
                <div className="text-xs text-[var(--text-muted)]">No routes through this peer.</div>
              ) : (
                <ul className="space-y-1 text-xs">
                  {routes.map((p) => (
                    <li key={p.destination} className="flex items-center justify-between gap-2">
                      <span className="font-mono">{shortID(p.destination)} · {routeSummary(p, shortID)}</span>
                      <span className="tabular text-[var(--text-muted)]">{p.hops} hops · {p.costMs.toFixed(1)} ms</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>

            <div className="flex flex-wrap gap-2">
              <Button size="sm" variant="secondary" onClick={() => probe.mutate(peerId)} disabled={probe.isPending}>
                <Radar size={14} /> {probe.isPending ? "Probing…" : "Probe"}
              </Button>
              <Button size="sm" variant="secondary" onClick={() => overlay && ping.mutate(overlay)} disabled={!overlay || ping.isPending}>
                <Network size={14} /> {ping.isPending ? "Pinging…" : "Ping"}
              </Button>
              <Button size="sm" variant="secondary" onClick={() => onViewRoutes(peerId)}>
                <RouteIcon size={14} /> View routes
              </Button>
            </div>

            {probeResult && (
              <div className="rounded-md border border-[var(--border)] p-2 text-xs">
                <div className="flex items-center gap-2">
                  <span className="mono-label">probe</span>
                  <Badge tone={probeResult.state === "up" ? "success" : probeResult.state === "degraded" ? "warning" : "danger"}>{probeResult.state}</Badge>
                  {probeResult.rtt_ms !== undefined && probeResult.rtt_ms > 0 && <span className="tabular">{probeResult.rtt_ms.toFixed(1)} ms</span>}
                </div>
                {probeResult.error && <div className="mt-1 text-[var(--danger)]">{probeResult.error}</div>}
              </div>
            )}

            {ping.data && (
              <div className="rounded-md border border-[var(--border)] p-2 text-xs">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="mono-label">ping</span>
                  <Badge tone={ping.data.reachable ? "success" : "danger"}>{ping.data.reachable ? "reachable" : "unreachable"}</Badge>
                  <span className="tabular text-[var(--text-muted)]">exit {ping.data.exit_code}</span>
                  <span className="font-mono text-[var(--text-faint)]">{ping.data.address}</span>
                </div>
                <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-words font-mono text-[11px] text-[var(--text-muted)]">
                  {ping.data.output || "(no output)"}
                </pre>
              </div>
            )}
          </>
        )}
      </InspectorFrame>

      <Dialog open={confirmLeave} onOpenChange={setConfirmLeave}>
        <DialogContent>
          <DialogTitle>Leave this network?</DialogTitle>
          <DialogDescription>
            This device leaves the mesh itself. No other peer is removed by you, and leaving is not a ban: this device keeps its network credential until the whole network rotates its secret, so rejoining needs a fresh invite from a member.
            Leaving first announces the departure to reachable peers (their tables and routes update); peers that are offline learn it from the retained tombstone when they reconnect. The dataplane stops and sessions close.
          </DialogDescription>
          <div className="mt-4 flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setConfirmLeave(false)}>Cancel</Button>
            <Button variant="danger" onClick={() => leave.mutate()} disabled={leave.isPending}>
              {leave.isPending ? "Leaving…" : "Leave network"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}
