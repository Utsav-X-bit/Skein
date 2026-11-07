import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Events } from "@wailsio/runtime";
import { toast } from "sonner";
import { create } from "zustand";
import {
  lifecycleEvents,
  type Activity,
  type Connection,
  type DaemonEvent,
  type LifecycleEvent,
  type LogEntry,
  type Peer,
  type ProbeResult,
  type Route,
  type ServiceStatus,
  type Status,
} from "@/lib/api";
import { useSessionEvents } from "@/features/notifications/store";
import { dedupeActivities } from "./model";

export const LogLimit = 500;
export const ActivityLimit = 500;

type LiveState = {
  logs: LogEntry[];
  activities: Activity[];
  probes: Record<string, ProbeResult>;
  /** Epoch ms of the newest sample per peer id; absent means no traffic seen. */
  lastTrafficAt: Record<string, number>;
};

// Snapshot events replace the query cache; the stores below hold the streams
// that have no snapshot endpoint (log tail, live activity, probe results).
export const useLive = create<LiveState>(() => ({
  logs: [],
  activities: [],
  probes: {},
  lastTrafficAt: {},
}));

function pushLog(entry: LogEntry) {
  useLive.setState((s) => ({ logs: [...s.logs, entry].slice(-LogLimit) }));
}

function pushActivity(row: Activity) {
  useLive.setState((s) => {
    const lastTrafficAt = { ...s.lastTrafficAt };
    for (const id of [row.origin, row.destination]) {
      if (!id) continue;
      lastTrafficAt[id] = Math.max(lastTrafficAt[id] ?? 0, row.observed_ms);
    }
    return {
      activities: dedupeActivities([...s.activities, row]).slice(-ActivityLimit),
      lastTrafficAt,
    };
  });
}

function setProbe(result: ProbeResult) {
  useLive.setState((s) => ({ probes: { ...s.probes, [result.peer_id]: result } }));
}

const peerLifecycle: Partial<Record<LifecycleEvent, true>> = {
  peer_up: true,
  peer_joined: true,
  peer_discovered: true,
  peer_added: true,
  peer_removed: true,
  "peer:leave": true,
  "peer:rejoined": true,
};

// Only events that change what the user is doing are worth an interruption;
// the rest live in the session feed.
const toastText: Partial<Record<LifecycleEvent, (ev: DaemonEvent) => string>> = {
  "peer:leave": () => "Left the network",
  "peer:rejoined": () => "Rejoined the network",
  secret_rotated: () => "Network secret rotated — restart the service to apply it",
  system_reset: () => "Network configuration reset",
  system_imported: () => "Configuration imported",
  leave_dataplane_stop_failed: (ev) =>
    `Left the network, but the data plane did not stop${ev.detail ? `: ${ev.detail}` : ""}`,
};

/**
 * useDaemonEvents is the app's only Wails event subscription. Mount it once
 * (AppShell); typed snapshot events write the query cache directly so no page
 * needs its own listener or poll.
 */
export function useDaemonEvents() {
  const qc = useQueryClient();

  useEffect(() => {
    const subs: Array<() => void> = [];
    const on = <T,>(name: string, fn: (data: T) => void) => {
      subs.push(
        Events.On(name, (ev) => {
          fn(ev.data as T);
        }),
      );
    };
    const refresh = (...keys: string[][]) => {
      for (const key of keys) void qc.invalidateQueries({ queryKey: key });
    };

    on<Peer[]>("peers:changed", (peers) => {
      qc.setQueryData<Peer[]>(["peers"], peers ?? []);
      refresh(["config"]);
    });
    on<Route[]>("routes:changed", (routes) => qc.setQueryData<Route[]>(["routes"], routes ?? []));
    on<ServiceStatus>("service:changed", (svc) => qc.setQueryData<ServiceStatus>(["service"], svc));
    on<Connection>("connection:changed", (connection) => {
      qc.setQueryData<Status>(["status"], (prev) => (prev ? { ...prev, connection } : undefined));
    });
    on<ProbeResult>("peer:probe", setProbe);
    on<LogEntry>("log:entry", pushLog);
    on<Activity>("activity:sample", pushActivity);

    for (const name of lifecycleEvents) {
      on<DaemonEvent>(name, (ev) => {
        const event: DaemonEvent = { ...ev, type: ev.type || name };
        useSessionEvents.getState().push(event);
        if (peerLifecycle[name]) refresh(["peers"], ["status"]);
        if (name === "name_changed" || name === "secret_rotated") refresh(["status"], ["config"]);
        if (name === "system_reset" || name === "system_imported") {
          refresh(["status"], ["peers"], ["routes"], ["config"]);
        }
        const text = toastText[name];
        if (text) {
          const message = text(event);
          if (name === "leave_dataplane_stop_failed") toast.warning(message);
          else toast.info(message);
        }
      });
    }

    return () => {
      for (const off of subs) off();
    };
  }, [qc]);
}
