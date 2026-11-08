import type { LogEntry } from "@/lib/api";

/**
 * The daemon keeps a bounded ring of redacted log entries (1024). The GUI
 * binding `DiagnosticsService.Logs(since, limit)` returns entries only — the
 * daemon's `next` cursor is dropped before it reaches the frontend — so cursor
 * paging is not available here. The page reads the support bundle (the
 * daemon's complete retained ring) and merges it with the live `log:entry`
 * stream instead; it never invents a cursor.
 */
export const LogWindowStep = 200;

export type LogLevel = "all" | "info" | "warning" | "error";

export const LogLevels: ReadonlyArray<{ value: LogLevel; label: string }> = [
  { value: "all", label: "All" },
  { value: "info", label: "Info" },
  { value: "warning", label: "Warning" },
  { value: "error", label: "Error" },
];

/**
 * logKey identifies one occurrence. The daemon stamps milliseconds and the
 * same message can legitimately repeat, so the key is the full line: identical
 * lines in the same millisecond are indistinguishable and collapse to one.
 */
export function logKey(entry: LogEntry): string {
  return `${entry.time_ms}|${entry.level}|${entry.message}`;
}

/** mergeLogs unions the retained page with the live tail, oldest first. */
export function mergeLogs(page: LogEntry[] | null | undefined, live: LogEntry[], cap = 1000): LogEntry[] {
  const seen = new Set<string>();
  const out: LogEntry[] = [];
  for (const entry of [...(page ?? []), ...live]) {
    const key = logKey(entry);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(entry);
  }
  // Both sources are oldest-first; sorting keeps a burst that landed after the
  // page was fetched in time order. Equal timestamps keep arrival order.
  out.sort((a, b) => a.time_ms - b.time_ms);
  return out.slice(-cap);
}

/** filterLogs applies the level chip and the text filter, never a guessed level. */
export function filterLogs(rows: LogEntry[], level: LogLevel, query: string): LogEntry[] {
  const needle = query.trim().toLowerCase();
  return rows.filter(
    (row) =>
      (level === "all" || row.level === level) &&
      (!needle || row.message.toLowerCase().includes(needle) || row.level.toLowerCase().includes(needle)),
  );
}

/** logLine is the exact text a copy action puts on the clipboard. */
export function logLine(entry: LogEntry): string {
  return `${new Date(entry.time_ms).toISOString()} ${entry.level.toUpperCase()} ${entry.message}`;
}
