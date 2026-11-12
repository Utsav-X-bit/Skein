import { useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Route as RouteIcon } from "lucide-react";
import { useMesh } from "@/features/mesh/useMesh";
import { MobileMeshMap } from "@/features/mesh/MobileMeshMap";
import { ConnectButton } from "@/features/mobile/ConnectButton";
import { MobileSheet } from "@/components/layout/MobileSheet";
import { PeerInspectorBody, useLeaveNetworkMutation } from "@/features/mesh/PeerInspector";
import { LinkInspectorBody } from "@/features/mesh/LinkInspector";
import { ThroughputStrip } from "@/features/mesh/ThroughputStrip";
import { EventStrip } from "@/features/mesh/EventStrip";
import { RouteDrawer } from "@/features/mesh/RouteDrawer";
import { StateScreen } from "@/components/layout/StateScreen";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { ConfigService, VpnService } from "@/lib/api";
import { shortID } from "@/lib/utils";

/**
 * MobileOverview is the mobile Home tab (design brief §3, §5.2):
 * status sentence, compact map with peer/link sheets, throughput +
 * event strips, Routes sheet, and the one sticky Connect button.
 * The VPN-denied state is a full screen reached from ConnectButton.
 */
export function MobileOverview() {
  const mesh = useMesh();
  const location = useLocation();
  const navigate = useNavigate();
  // Onboarding "Connect now" arrives as navigation state; consume it
  // once so returning to Home later does not re-fire the flow.
  const [autoStart] = useState(() => (location.state as { connectNow?: boolean } | null)?.connectNow === true);
  useEffect(() => {
    if (autoStart) navigate(".", { replace: true, state: {} });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoStart]);
  const [selectedPeer, setSelectedPeer] = useState<string | null>(null);
  const [selectedLink, setSelectedLink] = useState<{ from: string; to: string } | null>(null);
  const [routesOpen, setRoutesOpen] = useState(false);
  const [routesPeer, setRoutesPeer] = useState<string | null>(null);
  const [vpnDenied, setVpnDenied] = useState(false);
  const [confirmLeave, setConfirmLeave] = useState(false);
  const leave = useLeaveNetworkMutation();

  const self = mesh.status?.id ?? "";
  const connectionState = mesh.status?.connection?.state ?? "unknown";
  const cfg = useQuery({ queryKey: ["config"], queryFn: () => ConfigService.Get(), retry: 0 });
  const overlayCidr = useMemo(() => {
    // The overlay prefix comes from config; fall back to the default
    // /24 when the daemon has not reported it yet.
    const prefix = cfg.data?.overlay_ipv4_prefix;
    return prefix || "10.144.144.0/24";
  }, [cfg.data?.overlay_ipv4_prefix]);
  // Same staleness rule as desktop Overview: no snapshot for >15 s.
  const staleMs = mesh.staleSince ? Date.now() - mesh.staleSince : null;
  const stale = staleMs !== null && staleMs > 15_000;

  const selectedPeerName = useMemo(() => {
    if (!selectedPeer) return "";
    if (selectedPeer === self) return mesh.status?.name || "This device";
    return mesh.peers.find((p) => p.id === selectedPeer)?.name || shortID(selectedPeer);
  }, [selectedPeer, self, mesh.status?.name, mesh.peers]);

  const statusSentence = useMemo(() => {
    const up = mesh.peers.filter((p) => p.state === "up").length;
    const total = mesh.peers.length;
    if (connectionState === "connected") {
      return total === 0
        ? "Connected — no peers yet. Invite a device to join."
        : `Connected — ${up} of ${total} peer${total === 1 ? "" : "s"} up.`;
    }
    if (connectionState === "paused") return "Paused — the dataplane is stopped, the VPN is still up.";
    if (connectionState === "error") return `Error — ${mesh.status?.connection?.last_error ?? "couldn't connect"}.`;
    return "Disconnected — the mesh is down.";
  }, [connectionState, mesh.peers, mesh.status?.connection?.last_error]);

  if (mesh.loading && !mesh.status) {
    return (
      <StateScreen
        title="Loading mesh…"
        message="Reading the daemon state."
        primaryLabel="Retry"
        onPrimary={() => void mesh.refetch()}
      />
    );
  }

  if (vpnDenied) {
    return (
      <StateScreen
        title="VPN permission denied"
        message="Skein can't start the mesh tunnel without Android's VPN permission. Nothing was connected, and nothing was changed on the device."
        primaryLabel="Open VPN settings"
        onPrimary={() => {
          void VpnService.OpenVpnSettings();
        }}
        secondaryLabel="Try again"
        onSecondary={() => {
          setVpnDenied(false);
          void VpnService.RequestPermission();
        }}
      />
    );
  }

  return (
    <div className="space-y-4 pb-4">
      <p className="text-sm leading-relaxed text-[var(--text-muted)]" aria-live="polite">
        {statusSentence}
      </p>

      <MobileMeshMap
        model={mesh.model}
        peers={mesh.peers}
        peerRates={mesh.peerRates}
        self={self}
        selectedPeer={selectedPeer}
        selectedLink={selectedLink}
        onSelectPeer={(id) => {
          setSelectedLink(null);
          setSelectedPeer(id);
        }}
        onSelectLink={(link) => {
          setSelectedPeer(null);
          setSelectedLink(link);
        }}
        onEscape={() => {
          setSelectedPeer(null);
          setSelectedLink(null);
        }}
        stale={stale}
        staleText={staleMs !== null ? `Data may be stale (last update ${Math.round(staleMs / 1000)}s ago).` : undefined}
        onRetry={() => void mesh.refetch()}
      />

      <div className="space-y-2">
        <ThroughputStrip />
        <EventStrip />
        <div className="flex justify-end">
          <Button size="sm" variant="secondary" onClick={() => setRoutesOpen(true)}>
            <RouteIcon size={14} /> Routes
          </Button>
        </div>
      </div>

      <div className="sticky bottom-[calc(var(--nav-h)+12px)] z-20">
        <ConnectButton
          connectionState={connectionState}
          overlayPrefix={overlayCidr}
          onVpnDenied={() => setVpnDenied(true)}
          autoStart={autoStart}
        />
      </div>

      <MobileSheet open={selectedPeer !== null} onClose={() => setSelectedPeer(null)} title={selectedPeerName} initialDetent="half">
        <div className="space-y-4">
          <PeerInspectorBody
            peerId={selectedPeer}
            status={mesh.status}
            peers={mesh.peers}
            model={mesh.model}
            peerRates={mesh.peerRates}
            lastTrafficAt={mesh.lastTrafficAt}
            onViewRoutes={(dest) => {
              setSelectedPeer(null);
              setRoutesPeer(dest);
              setRoutesOpen(true);
            }}
            onLeaveRequest={() => setConfirmLeave(true)}
          />
        </div>
      </MobileSheet>

      <MobileSheet
        open={selectedLink !== null}
        onClose={() => setSelectedLink(null)}
        title={selectedLink ? `${selectedLink.from === self ? "This device" : shortID(selectedLink.from)} → ${selectedLink.to === self ? "This device" : shortID(selectedLink.to)}` : "Link"}
        initialDetent="peek"
      >
        <div className="space-y-4">
          <LinkInspectorBody link={selectedLink} model={mesh.model} peers={mesh.peers} activities={mesh.activities} />
        </div>
      </MobileSheet>

      <RouteDrawer
        open={routesOpen}
        onClose={() => { setRoutesOpen(false); setRoutesPeer(null); }}
        paths={mesh.model.paths}
        selected={routesPeer}
        onSelect={(dest) => setRoutesPeer(dest)}
        nameOf={(id) => (id === self ? "This device" : mesh.peers.find((p) => p.id === id)?.name || shortID(id))}
      />

      <Dialog open={confirmLeave} onOpenChange={setConfirmLeave}>
        <DialogContent>
          <DialogTitle>Leave this network?</DialogTitle>
          <DialogDescription>
            This device leaves the mesh itself. No other peer is removed by you, and leaving is not a ban: this device keeps its network credential until the whole network rotates its secret, so rejoining needs a fresh invite from a member.
            Leaving first announces the departure to reachable peers (their tables and routes update); peers that are offline learn it from the retained tombstone when they reconnect. The dataplane stops and sessions close.
          </DialogDescription>
          <div className="mt-4 flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setConfirmLeave(false)}>Cancel</Button>
            <Button variant="danger" onClick={() => { leave.mutate(); setConfirmLeave(false); setSelectedPeer(null); }} disabled={leave.isPending}>
              {leave.isPending ? "Leaving…" : "Leave network"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
