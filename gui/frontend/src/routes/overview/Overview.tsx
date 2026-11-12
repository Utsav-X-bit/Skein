import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ListTree } from "lucide-react";
import { ConfigService, ServiceService } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Metric } from "@/components/ui/metric";
import { ErrorState } from "@/components/ui/error-state";
import { Skeleton } from "@/components/ui/skeleton";
import { formatAge } from "@/features/mesh/model";
import { formatDuration, shortID } from "@/lib/utils";
import { useLiveSampler, useMesh } from "@/features/mesh/useMesh";
import { usePeerParam } from "@/features/mesh/hooks";
import { MeshCanvas } from "@/features/mesh/MeshCanvas";
import { RouteDrawer } from "@/features/mesh/RouteDrawer";
import { PeerInspector } from "@/features/mesh/PeerInspector";
import { LinkInspector } from "@/features/mesh/LinkInspector";
import { ThroughputStrip } from "@/features/mesh/ThroughputStrip";
import { EventStrip } from "@/features/mesh/EventStrip";

type Link = { from: string; to: string };

export function Overview() {
  useLiveSampler(true);
  const mesh = useMesh();
  const [peerId, setPeerId] = usePeerParam();
  const [link, setLink] = useState<Link | null>(null);
  const [routesOpen, setRoutesOpen] = useState(false);

  // Membership and service state are separate facts from connectivity; none
  // of them is inferred from another.
  const service = useQuery({ queryKey: ["service"], queryFn: () => ServiceService.Status(), staleTime: 5_000, retry: 0 });
  const config = useQuery({ queryKey: ["config"], queryFn: () => ConfigService.Get(), staleTime: 15_000, retry: 0 });

  const selectPeer = (id: string) => {
    setLink(null);
    setPeerId(id);
  };
  const selectLink = (next: Link) => {
    setPeerId(null);
    setLink(next);
  };
  const clearSelection = () => {
    setPeerId(null);
    setLink(null);
  };

  if (mesh.loading && !mesh.status) {
    return <div className="space-y-4">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-32" />)}</div>;
  }
  if (!mesh.status) {
    return (
      <ErrorState
        title="Daemon offline"
        message={mesh.error ?? "No status snapshot has arrived."}
        remediation="Start the Skein service or install it from Settings."
        onRetry={mesh.refetch}
      />
    );
  }

  const status = mesh.status;
  const peers = mesh.peers;
  const up = peers.filter((p) => p.state === "up").length;
  const membership = config.data ? (config.data.departed ? "departed" : "member") : "unknown";
  const connectivity = status.connection?.state ?? "unknown";
  const staleMs = mesh.staleSince ? Date.now() - mesh.staleSince : null;
  const stale = staleMs !== null && staleMs > 15_000;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <div className="mono-label">mesh</div>
          <h1 className="mt-1 text-2xl font-medium tracking-tight">{status.name || "This mesh"}</h1>
          <p className="text-sm text-[var(--text-muted)]">
            {status.overlay_ipv4}{status.overlay_ipv6 ? ` · ${status.overlay_ipv6}` : ""} · up {formatDuration(status.uptime_seconds)}
          </p>
        </div>
        <Button variant="secondary" onClick={() => setRoutesOpen(true)}>
          <ListTree size={16} /> Routes ({mesh.model.paths.length})
        </Button>
      </div>

      {stale && (
        <div className="flex flex-wrap items-center gap-3 rounded-md border border-[var(--warning)]/40 bg-[var(--warning)]/5 px-4 py-2 text-xs text-[var(--warning)]">
          <span>Last mesh update was {formatAge(staleMs ?? 0)} ago. Showing the last known snapshot, not current state.</span>
          <Button size="sm" variant="secondary" onClick={mesh.refetch}>Retry</Button>
        </div>
      )}

      <div className="rounded-lg border border-[var(--border)] bg-[var(--surface)] p-4">
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 xl:grid-cols-6">
          <Metric label="service" value={service.data?.state ?? "unknown"} />
          <Metric label="membership" value={membership} />
          <Metric label="connectivity" value={connectivity} />
          <Metric label="peers" value={`${up}/${peers.length}`} hint="up / total" />
          <Metric label="capacity" value={`${1 + peers.length} of 5`} hint="devices" />
          <Metric label="uptime" value={formatDuration(status.uptime_seconds)} />
        </div>
      </div>

      <div className="flex flex-col gap-4 min-[1100px]:flex-row">
        <div className="min-w-0 flex-1 space-y-4">
          <MeshCanvas
            model={mesh.model}
            peers={peers}
            peerRates={mesh.peerRates}
            activities={mesh.activities}
            self={status.id}
            selectedPeer={peerId}
            selectedLink={link}
            onSelectPeer={selectPeer}
            onSelectLink={selectLink}
            onEscape={clearSelection}
          />
          <ThroughputStrip />
          <EventStrip />
        </div>
        <PeerInspector
          peerId={peerId}
          status={status}
          peers={peers}
          model={mesh.model}
          peerRates={mesh.peerRates}
          lastTrafficAt={mesh.lastTrafficAt}
          onClose={clearSelection}
          onViewRoutes={(destination) => {
            setRoutesOpen(true);
            selectPeer(destination);
          }}
        />
        <LinkInspector
          link={link}
          model={mesh.model}
          peers={peers}
          activities={mesh.activities}
          onClose={() => setLink(null)}
        />
      </div>

      <RouteDrawer
        open={routesOpen}
        onClose={() => setRoutesOpen(false)}
        paths={mesh.model.paths}
        nameOf={(id) => (id === mesh.status?.id ? "this device" : mesh.peers.find((p) => p.id === id)?.name || shortID(id))}
        selected={peerId}
        onSelect={(destination) => {
          selectPeer(destination);
          setRoutesOpen(false);
        }}
      />
    </div>
  );
}
