import { useMemo } from "react";
import type { Peer } from "@/lib/api";
import { displayPeerNameById } from "@/lib/utils";
import { formatBytesPerSec, routeSummary, type MeshModel, type Rate } from "@/features/mesh/model";

type Link = { from: string; to: string };

/**
 * MobileMeshList is the List mode of the home map: one card per node
 * and one row per link. The desktop MeshTable is an 8-column table
 * that horizontal-scrolls on a phone and leaves tall empty rows; cards
 * fit the width and show the same facts without scrolling sideways.
 * A node with no overlay says so instead of repeating its id, and an
 * empty link list is one short line instead of an empty header row.
 */
export function MobileMeshList({
  model,
  peers,
  peerRates,
  self,
  selectedPeer,
  selectedLink,
  onSelectPeer,
  onSelectLink,
}: {
  model: MeshModel;
  peers: Peer[];
  peerRates: Record<string, Rate>;
  self: string;
  selectedPeer: string | null;
  selectedLink: Link | null;
  onSelectPeer: (id: string) => void;
  onSelectLink: (link: Link) => void;
}) {
  const nameOf = (id: string) => (id === self ? "This device" : displayPeerNameById(id, peers));
  const peerById = useMemo(() => new Map(peers.map((p) => [p.id, p])), [peers]);
  const nodes = useMemo(() => {
    const list = [...model.nodes];
    list.sort((a, b) => (a.id === self ? -1 : b.id === self ? 1 : a.id.localeCompare(b.id)));
    return list;
  }, [model.nodes, self]);
  const peersUp = peers.filter((p) => p.state === "up").length;

  return (
    <div className="space-y-3 p-2">
      <ul className="space-y-2">
        {nodes.map((node) => {
          const isSelf = node.kind === "self";
          const peer = isSelf ? undefined : peerById.get(node.id);
          const route = model.paths.find((p) => p.destination === node.id);
          const routeText = isSelf ? "" : route ? routeSummary(route, nameOf) : node.kind === "peer" ? "no route" : "—";
          const rate = peer ? peerRates[peer.id] : undefined;
          const selected = selectedPeer === node.id;
          const dot =
            node.state === "up" || isSelf
              ? "var(--success)"
              : node.state === "degraded"
                ? "var(--relay)"
                : "var(--text-faint)";
          return (
            <li key={node.id}>
              <button
                type="button"
                onClick={() => onSelectPeer(node.id)}
                aria-pressed={selected}
                className="w-full rounded-lg border bg-[var(--surface)] p-3 text-left"
                style={{ borderColor: selected ? "var(--accent)" : "var(--border)" }}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: dot }} />
                    <span className="min-w-0 truncate font-medium">{nameOf(node.id)}</span>
                  </span>
                  <span className="shrink-0 text-xs text-[var(--text-muted)]">
                    {isSelf ? "up · this device" : node.state}
                  </span>
                </div>
                <div className="mt-1 truncate font-mono text-xs text-[var(--text-muted)]">
                  {node.overlay ?? "no overlay address yet"}
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-x-3 text-xs text-[var(--text-muted)]">
                  {isSelf ? (
                    <span>
                      {peersUp} of {peers.length} peer{peers.length === 1 ? "" : "s"} up
                    </span>
                  ) : (
                    <>
                      <span className="truncate">{routeText}</span>
                      <span className="tabular">{node.rttMs !== undefined ? `${Math.round(node.rttMs)} ms` : "—"}</span>
                      <span>{node.transport ?? "unknown"}</span>
                      <span className="tabular">
                        {rate ? `${formatBytesPerSec(rate.rxPerSec)} ↓ · ${formatBytesPerSec(rate.txPerSec)} ↑` : "no traffic"}
                      </span>
                    </>
                  )}
                </div>
              </button>
            </li>
          );
        })}
      </ul>

      <div>
        <div className="mono-label mb-1">links</div>
        {model.edges.length === 0 ? (
          <p className="text-xs text-[var(--text-muted)]">No links yet — links appear when peers connect.</p>
        ) : (
          <ul className="space-y-1">
            {model.edges.map((edge) => {
              const isSelected =
                selectedLink !== null &&
                ((selectedLink.from === edge.from && selectedLink.to === edge.to) ||
                  (selectedLink.from === edge.to && selectedLink.to === edge.from));
              return (
                <li key={edge.id}>
                  <button
                    type="button"
                    onClick={() => onSelectLink({ from: edge.from, to: edge.to })}
                    aria-pressed={isSelected}
                    className="flex w-full items-center justify-between gap-2 rounded-lg border bg-[var(--surface)] px-3 py-2 text-left text-xs"
                    style={{ borderColor: isSelected ? "var(--accent)" : "var(--border)" }}
                  >
                    <span className="min-w-0 truncate font-mono">
                      {nameOf(edge.from)} → {nameOf(edge.to)}
                    </span>
                    <span className="flex shrink-0 items-center gap-2 text-[var(--text-muted)]">
                      <span>{edge.evidence}</span>
                      {edge.rttMs !== undefined && <span className="tabular">{Math.round(edge.rttMs)} ms</span>}
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}
