import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Copy, Plus, RefreshCw, ShieldAlert } from "lucide-react";
import { InviteService, errText, type Invite } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { CopyField } from "@/components/ui/copy-field";
import { QRCodeView } from "@/components/ui/qr-code";
import { Badge } from "@/components/ui/badge";
import { toast } from "sonner";

function InvitePanel({ kind }: { kind: "peer" | "network" }) {
  const [invite, setInvite] = useState<Invite | null>(null);
  const gen = useMutation({
    mutationFn: () => kind === "peer" ? InviteService.PeerInvite() : InviteService.NetworkInvite(),
    onSuccess: (v) => setInvite(v),
    onError: (e) => toast.error(errText(e)),
  });
  return (
    <div className="space-y-4">
      <p className="text-sm text-[var(--text-muted)]">
        {kind === "peer"
          ? "A peer invite introduces this node to an existing member. It does not carry the network secret and does not grant membership."
          : "A network join string carries the network secret. Share it only with devices you trust; it is a snapshot of the addresses in use."}
      </p>
      <Button onClick={() => gen.mutate()} disabled={gen.isPending}><Plus size={16} /> {invite ? "Regenerate" : "Generate"}</Button>
      {invite && (
        <div className="space-y-4">
          {invite.warning && <div className="flex gap-2 rounded-md border border-[var(--warning)]/40 bg-[var(--warning)]/5 p-3 text-xs text-[var(--warning)]"><ShieldAlert size={16} className="shrink-0" />{invite.warning}</div>}
          <CopyField value={invite.value} label={kind === "peer" ? "peer invite" : "network join string"} secret={kind === "network"} />
          <div className="flex items-start gap-4">
            <QRCodeView value={invite.value} />
            <div className="text-xs text-[var(--text-muted)]">
              <div className="mono-label mb-1">scan to join</div>
              <p>Open Skein on the other device and paste the string, or scan this code.</p>
              <Button variant="secondary" size="sm" className="mt-2" onClick={async () => { await navigator.clipboard.writeText(invite.value); toast.success("Copied"); }}><Copy size={14} /> Copy</Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

export function Invites() {
  const [rotating, setRotating] = useState(false);
  const rotate = useMutation({
    mutationFn: () => InviteService.RotateSecret(),
    onSuccess: () => toast.success("Network secret rotated; all peers must rejoin"),
    onError: (e) => toast.error(errText(e)),
    onSettled: () => setRotating(false),
  });
  return (
    <div className="space-y-4">
      <div><h1 className="text-2xl font-medium tracking-tight">Invitations</h1><p className="text-sm text-[var(--text-muted)]">Invite devices, or rotate the network secret to revoke access.</p></div>
      <Card>
        <CardContent className="pt-4">
          <Tabs defaultValue="network">
            <TabsList><TabsTrigger value="network">Join a device</TabsTrigger><TabsTrigger value="peer">Add a peer</TabsTrigger></TabsList>
            <TabsContent value="network" className="pt-4"><InvitePanel kind="network" /></TabsContent>
            <TabsContent value="peer" className="pt-4"><InvitePanel kind="peer" /></TabsContent>
          </Tabs>
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle>Revoke access</CardTitle><Badge tone="danger">destructive</Badge></CardHeader>
        <CardContent className="space-y-3">
          <p className="text-sm text-[var(--text-muted)]">Removing a peer is local-only. The only real revocation is rotating the network secret: every device must rejoin with the new join string. The helper backs up the old config, stops the service, rotates, and restarts.</p>
          <Button variant="danger" disabled={rotating} onClick={() => { if (confirm("Rotate the network secret? Every peer will lose access until it rejoins.")) { setRotating(true); rotate.mutate(); } }}><RefreshCw size={16} /> {rotating ? "Rotating…" : "Rotate network secret"}</Button>
        </CardContent>
      </Card>
    </div>
  );
}
