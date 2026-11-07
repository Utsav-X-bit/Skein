import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Dialogs } from "@wailsio/runtime";
import { FolderOpen, Fingerprint, KeyRound } from "lucide-react";
import { toast } from "sonner";
import { IdentityService, errDisplay, errKind, errText } from "@/lib/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { CopyField } from "@/components/ui/copy-field";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { ErrorState } from "@/components/ui/error-state";
import { Skeleton } from "@/components/ui/skeleton";

/**
 * IdentityPanel shows the running node's public identity and can mint an
 * identity for an offline, user-owned configuration. Only public values ever
 * cross the binding; the private key is written to disk and never returned.
 */
export function IdentityPanel() {
  const identity = useQuery({ queryKey: ["identity", "public"], queryFn: () => IdentityService.Public() });

  const [offlineOpen, setOfflineOpen] = useState(false);
  const [dir, setDir] = useState("");
  const generate = useMutation({
    mutationFn: () => IdentityService.GenerateOffline(dir),
    onError: (e) => toast.error(`${errKind(e) ? errKind(e) + ": " : ""}${errDisplay(e)}`),
  });
  const close = () => {
    if (generate.isPending) return;
    setOfflineOpen(false);
    setDir("");
    generate.reset();
  };

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2"><Fingerprint size={16} className="text-[var(--accent)]" /> Node identity</CardTitle>
          <Badge tone="info">public only</Badge>
        </CardHeader>
        <CardContent className="space-y-3">
          {identity.isLoading && <Skeleton className="h-24 w-full" />}
          {identity.isError && (
            <ErrorState
              title="Cannot read the node identity"
              message={errText(identity.error)}
              remediation={errKind(identity.error) === "Daemon offline" ? "The daemon is not reachable. Start the Skein service, then reopen this screen." : undefined}
              onRetry={() => identity.refetch()}
            />
          )}
          {identity.data && (
            <>
              {identity.data.name && (
                <div>
                  <div className="mono-label">node name</div>
                  <div className="mt-1 text-sm">{identity.data.name}</div>
                </div>
              )}
              <CopyField value={identity.data.id} label="peer id" />
              <CopyField value={identity.data.public_key} label="public key" />
              <p className="text-xs leading-relaxed text-[var(--text-muted)]">
                This is what peers see. The private key stays in the system configuration and is never shown or copied here.
              </p>
            </>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2"><KeyRound size={16} className="text-[var(--text-muted)]" /> Offline identity</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-xs leading-relaxed text-[var(--text-muted)]">
            Create an identity for a configuration you own on this device, without a daemon and without elevation. This node's live identity is part of its active configuration: it cannot be regenerated without resetting the node, and this dialog never touches it.
          </p>
          <Button size="sm" variant="secondary" onClick={() => setOfflineOpen(true)}><KeyRound size={14} /> Generate offline identity</Button>
        </CardContent>
      </Card>

      <Dialog open={offlineOpen} onOpenChange={(v) => { if (!v) close(); }}>
        <DialogContent>
          <DialogTitle>Generate an offline identity</DialogTitle>
          <DialogDescription>
            Pick a folder you own. The private key is written there as identity.key (owner-only, atomic, under the configuration lock) and is never returned to this screen; only the public id, public key and file path are shown. System configuration folders are refused, and an existing configuration in the folder is never overwritten.
          </DialogDescription>
          <div className="mt-4 space-y-3">
            <div>
              <div className="mono-label mb-1.5">target folder</div>
              <div className="flex items-stretch gap-2">
                <code className="min-w-0 flex-1 select-all overflow-x-auto rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-2 font-mono text-xs">{dir || "no folder chosen"}</code>
                <Button
                  variant="secondary"
                  onClick={async () => {
                    try {
                      const picked = await Dialogs.OpenFile({ CanChooseDirectories: true, CanChooseFiles: false, Title: "Choose a folder for the offline identity" });
                      if (typeof picked === "string" && picked) {
                        setDir(picked);
                        generate.reset();
                      }
                    } catch (e) {
                      toast.error(`Could not open the folder picker: ${errText(e)}`);
                    }
                  }}
                >
                  <FolderOpen size={15} /> Choose…
                </Button>
              </div>
            </div>

            {generate.data && (
              <div className="space-y-2 rounded-md border border-[var(--border)] p-3">
                <div className="text-xs text-[var(--success)]">Identity created. These are the public values:</div>
                <CopyField value={generate.data.peer_id} label="peer id" />
                <CopyField value={generate.data.public_key} label="public key" />
                <CopyField value={generate.data.identity_path} label="key file" />
                <p className="text-xs text-[var(--text-muted)]">
                  Point a configuration in that folder at this identity. A running system installation is not affected: importing into one goes through the node's own setup, not through this dialog.
                </p>
              </div>
            )}

            <div className="flex justify-end gap-2">
              <Button variant="secondary" onClick={close} disabled={generate.isPending}>{generate.data ? "Done" : "Cancel"}</Button>
              {!generate.data && (
                <Button onClick={() => generate.mutate()} disabled={generate.isPending || !dir}>
                  {generate.isPending ? "Generating…" : "Generate"}
                </Button>
              )}
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
