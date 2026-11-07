import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { HistoryService, NetworkService, PeerService, type Peer, type Status } from "@/lib/api";
import type { HistorySample } from "../../../bindings/github.com/Utsav-X-bit/skein/gui/services/models";
import { computeRate, type CounterSample } from "@/features/mesh/model";

/** Opted-in history samples at the 5 s cadence the store advertises. */
export const RecordIntervalMs = 5_000;

/** One key, so the Activity toggle and the app-wide writer share the flag. */
export const HistoryKey = ["history"] as const;
export const historySummaryKey = [...HistoryKey, "summary"] as const;

/** RecorderState is the last accepted sample plus why the next one may be dropped. */
export type RecorderState = {
  /** Node the baseline belongs to; null until a sample is accepted. */
  node: string | null;
  uptime: number;
  /** Epoch ms of the last accepted sample; null before the first. */
  t: number | null;
  /** A hidden window broke the counters; the next sample must not bridge it. */
  gap: boolean;
};

export const emptyRecorderState: RecorderState = { node: null, uptime: 0, t: null, gap: false };

export type RecorderInput = {
  enabled: boolean;
  hidden: boolean;
  node: string;
  uptime: number;
  t: number;
  intervalMs: number;
};

export type RecorderStep = { state: RecorderState; record: boolean };

/**
 * stepRecorder is the whole recording policy, pure so it is testable without
 * timers: nothing is recorded while disabled or hidden, a node change or an
 * uptime decrease (daemon restart) drops the sample that would bridge the
 * discontinuity, and a sample closer than the interval is skipped.
 */
export function stepRecorder(prev: RecorderState, input: RecorderInput): RecorderStep {
  if (!input.enabled) return { state: emptyRecorderState, record: false };
  if (input.hidden) return { state: { ...prev, gap: true }, record: false };
  const next: RecorderState = { node: input.node, uptime: input.uptime, t: input.t, gap: false };
  // A restart or a different node breaks the counters the rate was measured
  // on: keep the timestamp but never bridge the discontinuity.
  if (prev.node !== input.node || input.uptime < prev.uptime) return { state: next, record: false };
  if (prev.gap || prev.t === null) return { state: next, record: false };
  if (input.t - prev.t < input.intervalMs) return { state: prev, record: false };
  return { state: next, record: true };
}

/**
 * useHistoryRecorder is the one writer of persistent history. It samples
 * status and peers on its own 5 s cadence — independently of the live 2 s
 * session ring, which stays a live-surface read path — so history collects
 * while the app runs, not only while Activity is mounted. Mount it once.
 */
export function useHistoryRecorder() {
  const summaryQ = useQuery({
    queryKey: historySummaryKey,
    queryFn: () => HistoryService.Summary(),
    staleTime: 30_000,
    retry: 0,
    // The shell mounts once, so a store that could not be read at launch
    // (daemon still starting) would never answer. Retry slowly until it does;
    // after that the Activity toggle invalidates this key itself.
    refetchInterval: (query) => (query.state.data ? false : 60_000),
  });
  const enabled = summaryQ.data?.enabled === true;

  useEffect(() => {
    if (!enabled) return;
    let stopped = false;
    let state = emptyRecorderState;
    let prev: CounterSample | null = null;

    const tick = async () => {
      if (document.visibilityState === "hidden") {
        // Nothing is sampled while hidden, so the counters are stale by the
        // time the window returns: drop the first sample after the gap.
        state = stepRecorder(state, { enabled, hidden: true, node: "", uptime: 0, t: Date.now(), intervalMs: RecordIntervalMs }).state;
        prev = null;
        return;
      }
      let status: Status;
      let peers: Peer[];
      try {
        const [nextStatus, nextPeers] = await Promise.all([NetworkService.Status(), PeerService.List()]);
        status = nextStatus;
        peers = nextPeers ?? [];
      } catch {
        // A failed sample proves nothing; the next tick retries.
        return;
      }
      if (stopped) return;

      const t = Date.now();
      const sample: CounterSample = { t, rx: status.rx_bytes, tx: status.tx_bytes, uptime: status.uptime_seconds };
      const rate = computeRate(prev, sample);
      prev = sample;
      const step = stepRecorder(state, {
        enabled,
        hidden: false,
        node: status.id,
        uptime: status.uptime_seconds,
        t,
        intervalMs: RecordIntervalMs,
      });
      state = step.state;
      if (!step.record || !rate) return;

      void HistoryService.Record({
        t,
        rx_per_sec: rate.rxPerSec,
        tx_per_sec: rate.txPerSec,
        peers_up: peers.filter((p) => p.state === "up").length,
        peers_total: peers.length,
      } satisfies HistorySample).catch(() => {
        // Best effort: the next sample retries and a failed append is not
        // worth interrupting the user for.
      });
    };

    void tick();
    const id = setInterval(() => void tick(), RecordIntervalMs);
    return () => {
      stopped = true;
      clearInterval(id);
    };
  }, [enabled]);
}
