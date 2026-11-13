import { useMemo } from "react";
import type { Peer } from "@/lib/api";
import { displayPeerNameById } from "@/lib/utils";
import { rowKeyNav } from "./hooks";
import { formatBytesPerSec, routeSummary, type MeshModel, type Rate } from "./model";

type Link = { from: string; to: string };

/** MeshTable is the keyboard-complete equivalent of the canvas. */
export function MeshTable({
  model,
  peers,
  peerRates,
  selectedPeer,
  selectedLink,
  onSelectPeer,
  onSelectLink,
  onEscape,
  self,
}: {
  model: MeshModel;
  peers: Peer[];
  peerRates: Record<string, Rate>;
  selectedPeer: string | null;
  selectedLink: Link | null;
  onSelectPeer: (id: string) => void;
  onSelectLink: (link: Link) => void;
  onEscape: () => void;
  self: string;
}) {
  const facts = useMemo(() => {
    const direct = new Set(model.edges.filter((e) => e.from === self && e.evidence === "direct").map((e) => e.to));
    const observed = new Set(model.edges.filter((e) => e.evidence === "observed").flatMap((e) => [e.from, e.to]));
    const advertised = new Set(model.edges.filter((e) => e.evidence === "advertised").flatMap((e) => [e.from, e.to]));
    const routed = new Set(model.edges.filter((e) => e.evidence === "route").flatMap((e) => [e.from, e.to]));
    return { direct, observed, advertised, routed };
  }, [model.edges, self]);

  const nameOf = (id: string) => (id === self ? "This device" : displayPeerNameById(id, peers));

  return (
    <div className="space-y-4">
      <div className="overflow-x-auto rounded-lg border border-[var(--border)]">
        <table className="w-full text-left text-sm" aria-label="Mesh nodes">
          <thead className="border-b border-[var(--border)] text-[var(--text-faint)]">
            <tr>
              {["Node", "Overlay", "State", "Evidence", "Selected route", "RTT", "Transport", "Rate"].map((h) => (
                <th key={h} className="mono-label px-3 py-2 font-normal">{h}</th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-[var(--border)]">
            {model.nodes.map((node) => {
              const evidence = node.kind === "self"
                ? "this device"
                : facts.direct.has(node.id)
                  ? "direct"
                  : facts.observed.has(node.id)
                    ? "observed"
                    : facts.advertised.has(node.id)
                      ? "advertised"
                      : facts.routed.has(node.id)
                        ? "route"
                        : "unknown";
              const rate = peerRates[node.id];
              const route = model.paths.find((p) => p.destination === node.id);
              const routeText = node.kind === "self" ? "—" : route ? routeSummary(route, nameOf) : node.kind === "peer" ? "no route" : "—";
              return (
                <tr
                  key={node.id}
                  data-row
                  tabIndex={0}
                  aria-selected={selectedPeer === node.id}
                  onClick={() => onSelectPeer(node.id)}
                  onKeyDown={(e) => rowKeyNav(e, () => onSelectPeer(node.id), onEscape)}
                  className={`cursor-pointer hover:bg-[var(--surface-raised)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--ring)] ${selectedPeer === node.id ? "bg-[var(--accent-soft)]" : ""}`}
                >
                  <td className="max-w-[180px] truncate px-3 py-2">{node.kind === "self" ? node.label : nameOf(node.id)}</td>
                  <td className="px-3 py-2 font-mono text-xs">{node.overlay ?? "—"}</td>
                  <td className="px-3 py-2">{node.kind === "self" ? "up" : node.state}</td>
                  <td className="px-3 py-2 text-xs text-[var(--text-muted)]">{evidence}</td>
                  <td className="px-3 py-2 text-xs">{routeText}</td>
                  <td className="tabular px-3 py-2">{node.rttMs !== undefined ? `${Math.round(node.rttMs)} ms` : "—"}</td>
                  <td className="px-3 py-2 text-xs text-[var(--text-muted)]">{node.transport ?? "unknown"}</td>
                  <td className="tabular px-3 py-2 text-xs">
                    {rate ? `↓ ${formatBytesPerSec(rate.rxPerSec)} ↑ ${formatBytesPerSec(rate.txPerSec)}` : "—"}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      <div className="overflow-x-auto rounded-lg border border-[var(--border)]">
        <table className="w-full text-left text-sm" aria-label="Mesh links">
          <thead className="border-b border-[var(--border)] text-[var(--text-faint)]">
            <tr>
              {["Link", "Evidence", "Path", "RTT"].map((h) => (
                <th key={h} className="mono-label px-3 py-2 font-normal">{h}</th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-[var(--border)]">
            {model.edges.map((edge) => {
              const selected = selectedLink?.from === edge.from && selectedLink?.to === edge.to;
              return (
                <tr
                  key={edge.id}
                  data-row
                  tabIndex={0}
                  aria-selected={selected}
                  onClick={() => onSelectLink({ from: edge.from, to: edge.to })}
                  onKeyDown={(e) => rowKeyNav(e, () => onSelectLink({ from: edge.from, to: edge.to }), onEscape)}
                  className={`cursor-pointer hover:bg-[var(--surface-raised)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--ring)] ${selected ? "bg-[var(--accent-soft)]" : ""}`}
                >
                  <td className="px-3 py-2 font-mono text-xs">{nameOf(edge.from)} → {nameOf(edge.to)}</td>
                  <td className="px-3 py-2 text-xs text-[var(--text-muted)]">{edge.evidence}{edge.fresh ? "" : " · stale"}</td>
                  <td className="max-w-[240px] truncate px-3 py-2 text-xs text-[var(--text-muted)]">{edge.path ? edge.path.map(nameOf).join(" → ") : "—"}</td>
                  <td className="tabular px-3 py-2">{edge.rttMs !== undefined ? `${Math.round(edge.rttMs)} ms` : "—"}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
