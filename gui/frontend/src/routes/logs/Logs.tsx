import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Clipboard, Download, RefreshCw } from "lucide-react";
import { DiagnosticsService, errText } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ErrorState } from "@/components/ui/error-state";
import { toast } from "sonner";

export function Logs() {
  const [q, setQ] = useState("");
  const logs = useQuery({ queryKey: ["logs"], queryFn: () => DiagnosticsService.Logs(0, 500), refetchInterval: 3_000 });
  const bundle = useMutation({
    mutationFn: () => DiagnosticsService.Bundle(),
    onSuccess: async (b) => { await navigator.clipboard.writeText(JSON.stringify(b, null, 2)); toast.success("Redacted bundle copied"); },
    onError: (e) => toast.error(errText(e)),
  });
  if (logs.isError) return <ErrorState title="Cannot load logs" message={errText(logs.error)} onRetry={() => logs.refetch()} />;
  const rows = (logs.data ?? []).filter((l) => l.message.toLowerCase().includes(q.toLowerCase()));
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div><h1 className="text-2xl font-medium tracking-tight">Diagnostics</h1><p className="text-sm text-[var(--text-muted)]">Daemon logs and a redacted support bundle. Secrets and join strings never leave.</p></div>
        <div className="flex gap-2"><Button variant="secondary" onClick={() => logs.refetch()}><RefreshCw size={15} /> Refresh</Button><Button onClick={() => bundle.mutate()} disabled={bundle.isPending}><Download size={15} /> Copy bundle</Button></div>
      </div>
      <div className="flex items-center gap-2 rounded-md border border-[var(--border)] bg-[var(--surface)] px-3"><Input className="border-0 bg-transparent focus-visible:ring-0" placeholder="Filter logs…" value={q} onChange={(e) => setQ(e.target.value)} /></div>
      <Card>
        <CardHeader><CardTitle>Daemon log</CardTitle><span className="mono-label">{rows.length} lines</span></CardHeader>
        <CardContent className="max-h-[60vh] overflow-auto p-0">
          {rows.length === 0 ? <div className="p-6 text-center text-xs text-[var(--text-faint)]">No log lines</div> : (
            <div className="divide-y divide-[var(--border)] font-mono text-xs">{rows.map((l, i) => (
              <div key={`${l.time_ms}-${i}`} className="flex gap-3 px-4 py-1.5">
                <span className="shrink-0 text-[var(--text-faint)]">{new Date(l.time_ms).toLocaleTimeString()}</span>
                <span className={`shrink-0 uppercase ${l.level === "error" ? "text-[var(--danger)]" : l.level === "warning" ? "text-[var(--warning)]" : "text-[var(--text-faint)]"}`}>{l.level}</span>
                <span className="min-w-0 break-words text-[var(--text-muted)]">{l.message}</span>
              </div>
            ))}</div>
          )}
        </CardContent>
      </Card>
      <Card><CardHeader><CardTitle>Bundle contents</CardTitle><Clipboard size={15} /></CardHeader><CardContent><p className="text-sm text-[var(--text-muted)]">Logs, peer and route snapshots, redacted config, platform and protocol versions. Redactions: network secret, API token, private key, join strings.</p></CardContent></Card>
    </div>
  );
}
