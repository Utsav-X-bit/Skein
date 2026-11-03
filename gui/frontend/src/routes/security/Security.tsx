import { useMutation, useQuery } from "@tanstack/react-query";
import { ShieldCheck, Wrench } from "lucide-react";
import { DiagnosticsService, FirewallService, errText } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { ErrorState } from "@/components/ui/error-state";
import { toast } from "sonner";

export function Security() {
  const fw = useQuery({ queryKey: ["firewall"], queryFn: () => FirewallService.Status(), refetchInterval: 5_000 });
  const sec = useQuery({ queryKey: ["security"], queryFn: () => DiagnosticsService.Security(), refetchInterval: 10_000 });
  const repair = useMutation({ mutationFn: () => FirewallService.Repair(), onSuccess: () => { toast.success("Firewall rules repaired"); fw.refetch(); }, onError: (e) => toast.error(errText(e)) });
  if (fw.isError) return <ErrorState title="Cannot read firewall" message={errText(fw.error)} onRetry={() => fw.refetch()} />;
  return (
    <div className="space-y-4">
      <div><h1 className="text-2xl font-medium tracking-tight">Security</h1><p className="text-sm text-[var(--text-muted)]">Skein-managed firewall rules and local permission checks.</p></div>
      <Card>
        <CardHeader><CardTitle>Firewall</CardTitle><Badge tone={fw.data?.state === "ok" ? "success" : fw.data?.state === "error" ? "danger" : "neutral"}>{fw.data?.state ?? "loading"}</Badge></CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-3">
            <div><div className="mono-label">manager</div><div className="mt-1 text-sm">{fw.data?.manager || "not detected"}</div></div>
            <div><div className="mono-label">rules</div><div className="mt-1 text-sm">{fw.data?.rules?.length ?? 0} managed</div></div>
            <div><div className="mono-label">last check</div><div className="mt-1 text-sm">{fw.data?.last_check_ms ? new Date(fw.data.last_check_ms).toLocaleTimeString() : "—"}</div></div>
          </div>
          {fw.data?.last_error && <div className="rounded-md border border-[var(--danger)]/40 p-3 text-xs text-[var(--danger)]">{fw.data.last_error}</div>}
          <Button variant="secondary" onClick={() => repair.mutate()} disabled={repair.isPending}><Wrench size={15} /> {repair.isPending ? "Repairing…" : "Repair rules"}</Button>
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle>Local checks</CardTitle><ShieldCheck size={16} className="text-[var(--accent)]" /></CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-2">
          {[
            ["config owner", sec.data?.config_owner], ["config mode", sec.data?.config_mode],
            ["identity owner", sec.data?.identity_owner], ["identity mode", sec.data?.identity_mode],
            ["control socket", sec.data?.socket_path], ["update signature", sec.data?.update_signature],
          ].map(([k, v]) => <div key={k}><div className="mono-label">{k}</div><div className="mt-1 truncate font-mono text-xs">{v || "—"}</div></div>)}
        </CardContent>
      </Card>
      <Card><CardHeader><CardTitle>Arbitrary OS rules</CardTitle></CardHeader><CardContent><p className="text-sm text-[var(--text-muted)]">Skein exposes only the rules it manages. It never exposes a shell or an arbitrary firewall-rule editor.</p></CardContent></Card>
    </div>
  );
}
