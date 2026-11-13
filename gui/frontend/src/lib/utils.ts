import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function shortID(id?: string) {
  if (!id) return "—";
  return id.length > 12 ? id.slice(0, 8) : id;
}

/**
 * A daemon-reported node name is only a display name when it says
 * something. Android (and a fresh Linux install) report the OS
 * hostname, which defaults to "localhost" — that is not a name, it is
 * the absence of one, so every display site maps it to a fallback
 * instead of showing it.
 */
export function isUsableName(name: string | undefined | null): name is string {
  if (!name) return false;
  const value = name.trim();
  return value !== "" && value.toLowerCase() !== "localhost";
}

/** Display name for this device: the configured name, or "This device". */
export function displayNodeName(name: string | undefined | null): string {
  return isUsableName(name) ? name.trim() : "This device";
}

/** Display name for a peer: its configured name, or its short id. */
export function displayPeerName(peer: { id: string; name?: string | null } | undefined | null): string {
  if (!peer) return "—";
  return isUsableName(peer.name) ? peer.name.trim() : shortID(peer.id);
}

/** Display name for a peer known only by id. */
export function displayPeerNameById(id: string, peers: Array<{ id: string; name?: string | null }>): string {
  const peer = peers.find((p) => p.id === id);
  return peer ? displayPeerName(peer) : shortID(id);
}

export function formatBytes(bytes?: number) {
  if (!bytes || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / Math.pow(1024, i);
  return `${value.toFixed(value >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}

export function formatRate(bytesPerSecond?: number) {
  return `${formatBytes(bytesPerSecond)}/s`;
}

export function formatDuration(seconds?: number) {
  if (!seconds || seconds <= 0) return "0s";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

export function timeAgo(ms?: number) {
  if (!ms) return "never";
  const diff = Math.max(0, Date.now() - ms);
  if (diff < 60_000) return `${Math.max(1, Math.round(diff / 1000))}s ago`;
  if (diff < 3_600_000) return `${Math.round(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.round(diff / 3_600_000)}h ago`;
  return `${Math.round(diff / 86_400_000)}d ago`;
}

/**
 * Wails delivers a Go error wrapped in transport noise:
 * "Binding call failed: Bound method returned an error: <message>".
 * The wrapper names the RPC, not the problem, and it is what a service
 * failure actually read as on the phone. Only the wrappers are
 * removed — the backend's own words come through verbatim, so a real
 * failure is never softened or hidden.
 */
const bindingNoise = ["Binding call failed: ", "Bound method returned an error: "];

export function stripBindingNoise(message: string): string {
  let text = message.trim();
  for (;;) {
    const before = text;
    for (const prefix of bindingNoise) {
      if (text.startsWith(prefix)) text = text.slice(prefix.length).trim();
    }
    if (text === before) return text;
  }
}
