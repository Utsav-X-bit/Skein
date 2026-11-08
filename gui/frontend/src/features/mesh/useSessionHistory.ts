import { create } from "zustand";
import type { HistorySample } from "./model";

/** Live chart ring: 120 samples at the 2 s cadence is four minutes. */
export const HistoryLimit = 120;

type SessionHistoryState = {
  samples: HistorySample[];
  nodeId: string | null;
  uptime: number;
  record: (nodeId: string, uptime: number, sample: HistorySample) => void;
  reset: () => void;
};

// The ring is in memory only. A daemon restart (uptime goes backwards) or a
// different node means the old samples describe something else, so the ring
// starts over instead of drawing a rate across unrelated counters.
export const useSessionHistoryStore = create<SessionHistoryState>((set) => ({
  samples: [],
  nodeId: null,
  uptime: 0,
  record: (nodeId, uptime, sample) =>
    set((s) => {
      const changed = s.nodeId !== null && (s.nodeId !== nodeId || uptime < s.uptime);
      const samples = changed ? [] : s.samples;
      return { nodeId, uptime, samples: [...samples, sample].slice(-HistoryLimit) };
    }),
  reset: () => set({ samples: [], nodeId: null, uptime: 0 }),
}));

export function useSessionHistory(): HistorySample[] {
  return useSessionHistoryStore((s) => s.samples);
}
