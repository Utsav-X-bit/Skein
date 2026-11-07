import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { Copy, Plus, RefreshCw, ShieldAlert, ShieldQuestion } from "lucide-react";
import { toast } from "sonner";
import { InviteService, errText, type Invite } from "@/lib/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { CopyField } from "@/components/ui/copy-field";
import { ErrorState } from "@/components/ui/error-state";
import { QRCodeView } from "@/components/ui/qr-code";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

/**
 * InvitePanel generates one invitation and holds it in this component's
 * memory only. Nothing here is written to history, notifications, logs or the
 * URL; switching tabs unmounts the panel, which drops the plaintext.
 */
function InvitePanel({ kind }: { kind: "peer" | "network" }) {
  const [invite, setInvite] = useState<Invite | null>(null);
  const generate = useMutation({
    mutationFn: () => (kind === "peer" ? InviteService.PeerInvite() : InviteService.NetworkInvite()),
    onSuccess: (v) => setInvite(v),
  });

  return (
    <div className="space-y-4">
      <p className="text-sm leading-relaxed text-[var(--text-muted)]">
        {kind === "peer"
          ? "A peer introduction tells an existing member how to reach this node. It carries no network secret and does not grant membership: the member still decides whether to add this node."
          : "A network join string carries the network secret. Anyone who obtains it can join this network, so share it only with a device you trust, over a channel you trust. It is a snapshot of the addresses in use at the moment it was generated."}
      </p>

      <div className="flex flex-wrap gap-2">
        <Button onClick={() => generate.mutate()} disabled={generate.isPending}>
          <Plus size={16} /> {generate.isPending ? "Generating…" : invite ? "Regenerate" : kind === "peer" ? "Generate peer introduction" : "Generate join string"}
        </Button>
        {invite && (
          <Button variant="secondary" onClick={() => setInvite(null)}>
            Dismiss (clears it from this screen)
          </Button>
        )}
      </div>

      {generate.isError && <ErrorState title="Could not generate an invitation" message={errText(generate.error)} onRetry={() => generate.mutate()} />}

      {invite && (
        <div className="space-y-4">
          {invite.warning && (
            <div className="flex gap-2 rounded-md border border-[var(--warning)]/40 bg-[var(--warning)]/5 p-3 text-xs text-[var(--warning)]">
              <ShieldAlert size={16} className="shrink-0" />
              <span>{invite.warning}</span>
            </div>
          )}
          <div className="flex flex-wrap items-center gap-2">
            <Badge tone={kind === "network" ? "danger" : "info"}>{kind === "network" ? "contains the network secret" : "public introduction"}</Badge>
            {invite.snapshot && <Badge tone="warning">address snapshot</Badge>}
          </div>
          <CopyField value={invite.value} label={kind === "peer" ? "peer introduction" : "network join string"} secret={kind === "network"} />
          <p className="text-xs leading-relaxed text-[var(--text-faint)]">
            {kind === "network"
              ? "Copying puts the secret on the clipboard, where other applications and clipboard history may read it; a screenshot or a shoulder-surf shows it too. Skein cannot prevent that, so reveal and copy it deliberately and dismiss it afterwards."
              : "This introduction contains no secret. It is still a snapshot of how to reach this node, so send it only to the device you are introducing."}
          </p>

          {/* A QR code is a scannable copy of the value, so it defeats the
              masked field above. It renders only for peer introductions,
              which carry no network secret; join strings stay copy/reveal. */}
          {kind === "peer" && (
            <div className="flex items-start gap-4">
              <QRCodeView value={invite.value} />
              <div className="text-xs text-[var(--text-muted)]">
                <div className="mono-label mb-1">scan to join</div>
                <p>Open Skein on the other device and paste the string, or scan this code.</p>
                <Button
                  variant="secondary"
                  size="sm"
                  className="mt-2"
                  onClick={async () => {
                    try {
                      await navigator.clipboard.writeText(invite.value);
                      toast.success("Copied");
                    } catch {
                      toast.error("The clipboard is not available; select the text and copy it manually.");
                    }
                  }}
                >
                  <Copy size={14} /> Copy
                </Button>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

export function Invites() {
  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-medium tracking-tight">Invitations</h1>
        <p className="text-sm text-[var(--text-muted)]">Introduce a device to the network, or rotate the credential that every member shares.</p>
      </div>

      <Card>
        <CardContent className="pt-4">
          <Tabs defaultValue="network">
            <TabsList>
              <TabsTrigger value="network">Join this network</TabsTrigger>
              <TabsTrigger value="peer">Introduce this node</TabsTrigger>
            </TabsList>
            <TabsContent value="network" className="pt-4"><InvitePanel kind="network" /></TabsContent>
            <TabsContent value="peer" className="pt-4"><InvitePanel kind="peer" /></TabsContent>
          </Tabs>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2"><ShieldQuestion size={16} className="text-[var(--text-muted)]" /> What this release does not do</CardTitle>
          <Badge tone="neutral">deferred by the owner</Badge>
        </CardHeader>
        <CardContent className="space-y-2 text-xs leading-relaxed text-[var(--text-muted)]">
          <p>Invitations have <strong className="text-[var(--text)]">no enforced expiry</strong>: a join string stays valid until the network secret is rotated.</p>
          <p>There is <strong className="text-[var(--text)]">no active-invitation inventory</strong>: Skein cannot list the invitations outstanding, because it never stores them.</p>
          <p>There is <strong className="text-[var(--text)]">no use attribution</strong>: Skein does not report who joined with which invitation, and does not infer it from a later peer event.</p>
          <p>Rotating the network secret is the only network-wide revocation. It is not per-peer revocation, and a peer that has already joined is not removed by it — it must rejoin with the new join string.</p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2"><RefreshCw size={16} className="text-[var(--danger)]" /> Revoke access</CardTitle>
          <Badge tone="danger">network-wide</Badge>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-sm leading-relaxed text-[var(--text-muted)]">
            Removing a peer from this device is local-only and does not revoke anything. The only revocation is rotating the network secret: every device must rejoin with the new join string. Rotation lives on Security, with its confirmation and the one-time new join string.
          </p>
          <Button variant="secondary" asChild>
            <Link to="/security"><RefreshCw size={16} /> Open rotation on Security</Link>
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}
