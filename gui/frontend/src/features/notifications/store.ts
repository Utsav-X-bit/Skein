import { create } from "zustand";
import type { DaemonEvent } from "@/lib/api";

/** Session events are kept in memory only; nothing here is persisted. */
export const EventLimit = 200;

export type SessionEvent = DaemonEvent & { key: string };

type SessionEventState = {
  events: SessionEvent[];
  push: (ev: DaemonEvent) => void;
  clear: () => void;
};

// A lifecycle event can be replayed after a reconnect; the same
// type|peer|time is one occurrence, not two notifications.
export function eventKey(ev: DaemonEvent): string {
  return `${ev.type}|${ev.peer_id ?? ""}|${ev.time_ms}`;
}

export const useSessionEvents = create<SessionEventState>((set) => ({
  events: [],
  push: (ev) =>
    set((s) => {
      const key = eventKey(ev);
      if (s.events.some((e) => e.key === key)) return s;
      return { events: [...s.events, { ...ev, key }].slice(-EventLimit) };
    }),
  clear: () => set({ events: [] }),
}));
