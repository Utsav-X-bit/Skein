import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";
import * as SetupService from "../../../bindings/github.com/Utsav-X-bit/skein/gui/services/setupservice";
import type { NetworkResetResult } from "../../../bindings/github.com/Utsav-X-bit/skein/gui/services/models";
import { ConnectionService, PeerService, ServiceService, elevationCancelled, errText } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { Onboarding } from "@/routes/onboarding/Onboarding";

function recoveryError(error: unknown): string {
  const text = errText(error);
  if (elevationCancelled(error)) return "Administrator approval was not completed. Retry and approve only the Skein helper action.";
  if (text.includes("different network")) return "This invite belongs to a different network. Switch networks instead; your current configuration must be reset first.";
  if (/403|forbidden|secret role|permission|access denied/i.test(text)) return "Your account cannot change this network. Ask the device owner to grant access, then retry.";
  if (/invite|checksum|base64/i.test(text)) return "The invite could not be accepted. Ask a member of this network for a fresh network invite, then paste it again.";
  if (/cancel|pkexec|elevat/i.test(text)) return "Administrator approval was not completed. Retry and approve only the Skein helper action.";
  if (/daemon not connected|connect:|connection refused/i.test(text)) return "The daemon could not be reached. Start the service before changing network membership.";
  return "The network change did not finish. Review the live service state, then retry the current step.";
}

export function NetworkRecovery({ departed, configPath }: { departed: boolean; configPath?: string }) {
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  const open = params.get("network");
  const [invite, setInvite] = useState("");
  const [purge, setPurge] = useState(false);
  const [keepIdentity, setKeepIdentity] = useState(true);
  const [stage, setStage] = useState("");
  const [outcome, setOutcome] = useState<NetworkResetResult | null>(null);
  const [left, setLeft] = useState(false);
  const [rejoined, setRejoined] = useState(false);
  const [notice, setNotice] = useState("");
  const setMode = (mode: string) => setParams((p) => { p.set("network", mode); return p; });
  const refresh = () => { for (const key of ["status", "config", "peers", "routes", "service"]) void qc.invalidateQueries({ queryKey: [key] }); };
  const rejoin = useMutation({
    mutationFn: async () => {
      if (!rejoined) { await SetupService.Rejoin(invite.trim()); setRejoined(true); setInvite(""); }
      await ConnectionService.Connect();
    },
    onSuccess: () => { setLeft(false); setRejoined(false); setNotice("Membership restored and the mesh resumed."); refresh(); setParams((p) => { p.delete("network"); return p; }); },
    onSettled: refresh,
  });
  const reset = useMutation({
    mutationFn: async () => {
      if (!departed && !left) {
        setStage("Announcing this device’s departure…");
        const result = await PeerService.LeaveNetwork();
        setLeft(true);
        setNotice(result.propagated ? "Departure announced to reachable peers." : "Left locally. No peer was reachable to receive the departure announcement.");
      }
      setStage("Resetting network configuration…");
      const result = await SetupService.ResetWithPurge(purge, keepIdentity);
      setOutcome(result);
      setStage("");
    },
    onSettled: () => { setStage(""); refresh(); },
  });
  const stop = useMutation({
    mutationFn: () => ServiceService.Stop(),
    onSuccess: () => setOutcome((v) => v ? { ...v, stop_error: "" } : v),
    onSettled: refresh,
  });
  const busy = rejoin.isPending || reset.isPending || stop.isPending;
  const error = rejoin.error || reset.error || stop.error;
  const showRejoin = open === "rejoin" && !outcome;
  return <Card>
    <CardHeader><CardTitle>Network membership</CardTitle></CardHeader>
    <CardContent className="space-y-3">
      <p className="text-sm text-[var(--text-muted)]">{departed || left ? "This device left the network. Rejoin the same mesh with a fresh invite, or reset before switching to another." : "Leaving affects only this device. No peer can remove another peer."}</p>
      <div className="flex flex-wrap gap-2">
        {(departed || left) && <Button variant="secondary" onClick={() => setMode("rejoin")}>Rejoin this network</Button>}
        <Button variant="secondary" onClick={() => setMode("switch")}>{outcome ? "Continue network setup" : "Switch network / Reset"}</Button>
      </div>
      {notice && <p className="text-sm" role="status">{notice}</p>}
      <Dialog open={!!open} onOpenChange={(value) => { if (!value && !busy) setParams((p) => { p.delete("network"); return p; }); }}>
        <DialogContent className={outcome && !outcome.stop_error ? "max-h-[90vh] overflow-auto" : ""} onEscapeKeyDown={(e) => { if (busy) e.preventDefault(); }} onPointerDownOutside={(e) => { if (busy) e.preventDefault(); }}>
          <DialogTitle>{outcome ? "Set up your next network" : showRejoin ? "Rejoin this network" : "Switch networks"}</DialogTitle>
          <DialogDescription>{outcome ? "The old network configuration has been reset. Create a new mesh or join one with a fresh invite." : showRejoin ? "A fresh invite restores membership in the same network. It does not switch network credentials." : "This device announces its departure with a tombstone, closes its sessions, then resets and stops its service. Other devices stay in their network."}</DialogDescription>
          {!outcome && <div className="mt-4 space-y-4">
            {showRejoin ? <div><Label htmlFor="rejoin-invite">Fresh network invite</Label><Input id="rejoin-invite" type="password" autoComplete="off" value={invite} disabled={busy || rejoined} onChange={(e) => setInvite(e.target.value)} placeholder="skein:2:…" /></div> : <>
              <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={keepIdentity} disabled={busy} onChange={(e) => setKeepIdentity(e.target.checked)} />Keep this device’s identity</label>
              <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={purge} disabled={busy} onChange={(e) => setPurge(e.target.checked)} />Purge without a backup (irreversible)</label>
              <p className="break-all text-xs text-[var(--text-muted)]">{purge ? "No new backup is created. Deleted configuration cannot be restored by this app." : `Configuration and identity are backed up beside ${configPath || "the system configuration"}, in a timestamped reset-bak directory. The exact path is shown after reset. Restoring requires an administrator and a service restart; the GUI cannot restore a root-owned backup.`} {keepIdentity ? "The identity file stays in place." : "The current identity is also removed."}</p>
            </>}
            {stage && <p role="status" className="text-sm">{stage}</p>}
            <div className="flex flex-wrap justify-end gap-2">
              {showRejoin && <Button variant="secondary" disabled={busy} onClick={() => { rejoin.reset(); setMode("switch"); }}>Switch to a different network</Button>}
              <Button variant={showRejoin ? "default" : "danger"} disabled={busy || (showRejoin && !rejoined && !invite.trim())} onClick={() => showRejoin ? rejoin.mutate() : reset.mutate()}>{busy ? "Working…" : showRejoin ? rejoined ? "Resume mesh" : "Confirm rejoin" : departed || left ? "Confirm reset" : "Leave and reset"}</Button>
            </div>
          </div>}
          {outcome && <div className="mt-4 space-y-3">
            <div className="rounded-md border border-[var(--border)] p-3 text-sm break-all" role="status">{outcome.backup ? `Backup saved: ${outcome.backup}` : "Purged without a new backup."}</div>
            {outcome.firewall_error && <p role="alert" className="text-sm text-[var(--warning)]">Some firewall rules could not be removed. The network reset completed; check Firewall in Diagnostics before reconnecting.</p>}
            {outcome.stop_error ? <div><p className="mb-3 text-sm" role="alert">Reset completed, but the service is not confirmed stopped. Retry only Stop; do not reset again.</p><Button disabled={busy} onClick={() => stop.mutate()}>Retry stopping service</Button></div> : <Onboarding compact />}
          </div>}
          {error && <div className="mt-4 space-y-2 text-sm text-[var(--danger)]" role="alert"><p>{recoveryError(error)}</p><Button variant="secondary" disabled={busy} onClick={() => { rejoin.reset(); reset.reset(); stop.reset(); }}>Dismiss error</Button></div>}
        </DialogContent>
      </Dialog>
    </CardContent>
  </Card>;
}
