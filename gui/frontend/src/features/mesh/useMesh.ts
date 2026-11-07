import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { create } from "zustand";
import {
  NetworkService,
  PeerService,
  errText,
  type Activity,
  type Peer,
  type Route,
  type Status,
} from "@/lib/api";
import {
  buildMesh,
  computeRate,
  dedupeActivities,
  type CounterSample,
  type MeshModel,
  type Rate,
} from "./model";
import { useLive } from "./useDaemonEvents";
import { useSessionHistoryStore } from "./useSessionHistory";

export const MeshKeys = {
  status: ["status"],
  peers: ["peers"],
  routes: ["routes"],
  activities: ["activities"],
};

const LiveIntervalMs = 2_000;
const ReconcileIntervalMs = 30_000;

type Rates = {
  total: Rate | null;
  peers: Record<string, Rate>;
  sampledAt: number;
};

// Rates come from timed counter samples, never from a single snapshot: null
// means the window proves nothing and the UI must show unknown.
export const useRates = create<Rates>(() => ({ total: null, peers: {}, sampledAt: 0 }));

function useVisible(): boolean {
  const [visible, setVisible] = useState(
    () => typeof document === "undefined" || document.visibilityState !== "hidden",
  );
  useEffect(() => {
    const onChange = () => setVisible(document.visibilityState !== "hidden");
    document.addEventListener("visibilitychange", onChange);
    return () => document.removeEventListener("visibilitychange", onChange);
  }, []);
  return visible;
}

// One sampler for the whole app: status and peer counters every 2 s while a
// live surface is mounted and the window is visible, plus a 30 s reconciliation
// of the snapshots that arrive by event. Ownership is counted so two live
// surfaces still produce one timer and one rate baseline.
let samplerOwners = 0;
let stopSampler: (() => void) | null = null;

function startSampler(qc: QueryClient): () => void {
  let prevTotal: CounterSample | null = null;
  const prevPeers = new Map<string, CounterSample>();
  let stopped = false;

  // A hidden gap breaks the baseline: the first sample after it has no rate.
  useRates.setState({ total: null, peers: {}, sampledAt: 0 });

  const tick = async () => {
    let status: Status;
    let peers: Peer[];
    try {
      const [nextStatus, nextPeers] = await Promise.all([NetworkService.Status(), PeerService.List()]);
      status = nextStatus;
      peers = nextPeers ?? [];
    } catch {
      // A failed sample proves nothing; the last known rates stay on screen.
      return;
    }
    if (stopped) return;

    const t = Date.now();
    const sample: CounterSample = { t, rx: status.rx_bytes, tx: status.tx_bytes, uptime: status.uptime_seconds };
    const total = computeRate(prevTotal, sample);
    prevTotal = sample;

    const peerRates: Record<string, Rate> = {};
    const nextPeersSamples = new Map<string, CounterSample>();
    for (const peer of peers) {
      const peerSample: CounterSample = {
        t,
        rx: peer.rx_bytes,
        tx: peer.tx_bytes,
        uptime: status.uptime_seconds,
      };
      const rate = computeRate(prevPeers.get(peer.id) ?? null, peerSample);
      nextPeersSamples.set(peer.id, peerSample);
      if (rate) peerRates[peer.id] = rate;
    }
    prevPeers.clear();
    for (const [id, s] of nextPeersSamples) prevPeers.set(id, s);

    useRates.setState({ total, peers: peerRates, sampledAt: t });
    if (total) {
      useSessionHistoryStore.getState().record(status.id, status.uptime_seconds, {
        t,
        rxPerSec: total.rxPerSec,
        txPerSec: total.txPerSec,
        peersUp: peers.filter((p) => p.state === "up").length,
        peersTotal: peers.length,
      });
    }

    // The sample is also the freshest snapshot, so the whole UI reads it.
    qc.setQueryData<Status>(MeshKeys.status, status);
    qc.setQueryData<Peer[]>(MeshKeys.peers, peers);
  };

  void tick();
  const live = setInterval(() => void tick(), LiveIntervalMs);
  const reconcile = setInterval(() => {
    void qc.invalidateQueries({ queryKey: MeshKeys.routes });
    void qc.invalidateQueries({ queryKey: MeshKeys.activities });
  }, ReconcileIntervalMs);

  return () => {
    stopped = true;
    clearInterval(live);
    clearInterval(reconcile);
  };
}

/**
 * useLiveSampler starts the shared sampler while a live surface is mounted.
 * It stops (and the rate baseline is dropped) when the document is hidden.
 */
export function useLiveSampler(enabled: boolean) {
  const qc = useQueryClient();
  const visible = useVisible();
  const active = enabled && visible;

  useEffect(() => {
    if (!active) return;
    samplerOwners += 1;
    stopSampler ??= startSampler(qc);
    return () => {
      samplerOwners -= 1;
      if (samplerOwners === 0) {
        stopSampler?.();
        stopSampler = null;
      }
    };
  }, [active, qc]);
}

export type MeshData = {
  status: Status | null;
  peers: Peer[];
  routes: Route[];
  activities: Activity[];
  model: MeshModel;
  rate: Rate | null;
  peerRates: Record<string, Rate>;
  lastTrafficAt: Record<string, number>;
  /** Epoch ms of the last successful snapshot; null until one has arrived. */
  staleSince: number | null;
  loading: boolean;
  error: string | null;
};

/**
 * useMesh is the one read path for mesh data: the snapshot queries plus the
 * event-fed stores, merged into the pure model. Nothing here polls; the shared
 * sampler is what refreshes counters.
 */
export function useMesh(): MeshData {
  const statusQ = useQuery({
    queryKey: MeshKeys.status,
    queryFn: () => NetworkService.Status(),
    staleTime: 5_000,
    retry: 0,
  });
  const peersQ = useQuery({
    queryKey: MeshKeys.peers,
    queryFn: () => PeerService.List(),
    staleTime: 5_000,
    retry: 0,
  });
  const routesQ = useQuery({
    queryKey: MeshKeys.routes,
    queryFn: () => NetworkService.Routes(),
    staleTime: 5_000,
    retry: 0,
  });
  const activitiesQ = useQuery({
    queryKey: MeshKeys.activities,
    queryFn: () => NetworkService.Activities(),
    staleTime: 5_000,
    retry: 0,
  });

  const rates = useRates();
  const liveActivities = useLive((s) => s.activities);
  const lastTrafficAt = useLive((s) => s.lastTrafficAt);

  const peers = peersQ.data ?? [];
  const routes = routesQ.data ?? [];
  const activities = useMemo(
    () => dedupeActivities([...(activitiesQ.data ?? []), ...liveActivities]),
    [activitiesQ.data, liveActivities],
  );
  const self = statusQ.data?.id ?? "";
  const model = useMemo(
    () => buildMesh(self, peers, routes, activities, Date.now()),
    [self, peers, routes, activities, rates.sampledAt],
  );

  // Event-driven cache writes count as successful snapshots, so this is the
  // freshest moment the UI knows the mesh, not a fetch time.
  const staleSince =
    Math.max(statusQ.dataUpdatedAt, peersQ.dataUpdatedAt, routesQ.dataUpdatedAt, activitiesQ.dataUpdatedAt) || null;

  const failure = statusQ.error ?? peersQ.error ?? routesQ.error ?? activitiesQ.error;

  return {
    status: statusQ.data ?? null,
    peers,
    routes,
    activities,
    model,
    rate: rates.total,
    peerRates: rates.peers,
    lastTrafficAt,
    staleSince,
    loading: statusQ.isLoading || peersQ.isLoading || routesQ.isLoading || activitiesQ.isLoading,
    error: failure ? errText(failure) : null,
  };
}
