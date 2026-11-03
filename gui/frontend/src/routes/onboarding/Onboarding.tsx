import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { ArrowRight, KeyRound, Network, Server, ShieldCheck } from "lucide-react";
import * as SetupService from "../../../bindings/github.com/Utsav-X-bit/skein/gui/services/setupservice";
import { ServiceService, errText } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { Card, CardContent } from "@/components/ui/card";
import { CopyField } from "@/components/ui/copy-field";
import { LogoMark } from "@/components/brand/LogoMark";

type Mode = "welcome" | "create" | "join" | "done";

export function Onboarding() {
  const [mode, setMode] = useState<Mode>("welcome");
  const [name, setName] = useState("");
  const [port, setPort] = useState("11010");
  const [mtu, setMtu] = useState("1280");
  const [invite, setInvite] = useState("");
  const [result, setResult] = useState<{ node_id: string; public_key: string; overlay_ipv4: string; join: string; invite: string } | null>(null);
  const [importOpen, setImportOpen] = useState(false);
  const [importPath, setImportPath] = useState("");
  const create = useMutation({
    mutationFn: () => SetupService.Init({ name, port: Number(port), mtu: Number(mtu) } as never),
    onSuccess: (r) => { setResult(r); setMode("done"); },
  });
  const join = useMutation({
    mutationFn: () => SetupService.Join(invite),
    onSuccess: (r) => { setResult(r); setMode("done"); },
  });
  const start = useMutation({
    mutationFn: () => ServiceService.Start(),
    onSuccess: () => location.reload(),
  });
  const importCfg = useMutation({
    mutationFn: () => SetupService.Import(importPath),
    onSuccess: () => location.reload(),
  });
  const resetCfg = useMutation({
    mutationFn: () => SetupService.Reset(false),
    onSuccess: () => location.reload(),
  });
  const busy = create.isPending || join.isPending || start.isPending || importCfg.isPending || resetCfg.isPending;
  const error = create.error || join.error || start.error || importCfg.error || resetCfg.error;
  const alreadyExists = !!error && errText(error).toLowerCase().includes("already exists");

  return (
    <div className="mx-auto flex min-h-full max-w-3xl flex-col justify-center py-8">
      <div className="mb-8 flex items-center gap-4"><LogoMark size={48} animated /><div><div className="mono-label">welcome to</div><h1 className="text-3xl font-medium tracking-tight">Skein</h1><p className="text-sm text-[var(--text-muted)]">A private mesh for your own devices.</p></div></div>
      {mode === "welcome" && (
        <div className="grid gap-4 sm:grid-cols-2">
          <button onClick={() => setMode("create")} className="group rounded-xl border border-[var(--border)] bg-[var(--surface)] p-6 text-left transition-colors hover:border-[var(--accent)]">
            <Network className="mb-4 text-[var(--accent)]" />
            <div className="text-lg font-medium">Create a network</div>
            <p className="mt-1 text-sm text-[var(--text-muted)]">Start a new mesh and invite your other devices.</p>
            <span className="mt-4 inline-flex items-center gap-1 text-xs text-[var(--accent)]">Create <ArrowRight size={13} /></span>
          </button>
          <button onClick={() => setMode("join")} className="group rounded-xl border border-[var(--border)] bg-[var(--surface)] p-6 text-left transition-colors hover:border-[var(--accent)]">
            <KeyRound className="mb-4 text-[var(--accent)]" />
            <div className="text-lg font-medium">Join a network</div>
            <p className="mt-1 text-sm text-[var(--text-muted)]">Paste a join string from an existing device.</p>
            <span className="mt-4 inline-flex items-center gap-1 text-xs text-[var(--accent)]">Join <ArrowRight size={13} /></span>
          </button>
          <button onClick={() => start.mutate()} disabled={busy} className="group rounded-xl border border-[var(--border)] bg-[var(--surface)] p-6 text-left transition-colors hover:border-[var(--accent)] sm:col-span-2">
            <Server className="mb-4 text-[var(--accent)]" />
            <div className="text-lg font-medium">Use an existing installation</div>
            <p className="mt-1 text-sm text-[var(--text-muted)]">The service is stopped but <code>/etc/skein</code> already exists. Start it and keep the current network.</p>
            <span className="mt-4 inline-flex items-center gap-1 text-xs text-[var(--accent)]">{start.isPending ? "Starting…" : "Start service"} <ArrowRight size={13} /></span>
          </button>
        </div>
      )}
      {mode === "create" && (
        <Card><CardContent className="space-y-4 pt-5">
          <div><Label>device name</Label><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="studio" autoFocus /></div>
          <div className="grid gap-4 sm:grid-cols-2"><div><Label>listen port</Label><Input value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" /></div><div><Label>MTU</Label><Input value={mtu} onChange={(e) => setMtu(e.target.value)} inputMode="numeric" /></div></div>
          <p className="text-xs text-[var(--text-muted)]">Skein will create a root-owned config, install the service, open the underlay port, and trust the overlay interface. You may be asked to authenticate.</p>
          {error && (
            <div className="space-y-3 rounded-md border border-[var(--danger)]/40 bg-[var(--danger)]/5 p-3 text-xs text-[var(--danger)]">
              <div>{errText(error)}</div>
              {alreadyExists && (
                <div className="flex flex-wrap gap-2">
                  <Button size="sm" variant="secondary" onClick={() => start.mutate()}>Start existing service</Button>
                  <Button size="sm" variant="secondary" onClick={() => setImportOpen(true)}>Import a user config</Button>
                  <Button size="sm" variant="danger" onClick={() => { if (confirm("Back up the existing root config and identity, then start over? The backup stays on disk.")) resetCfg.mutate(); }}>Reset existing config</Button>
                </div>
              )}
            </div>
          )}
          <div className="flex justify-between"><Button variant="ghost" onClick={() => setMode("welcome")}>Back</Button><Button onClick={() => create.mutate()} disabled={busy}>{busy ? "Setting up…" : "Create network"}</Button></div>
        </CardContent></Card>
      )}
      {mode === "join" && (
        <Card><CardContent className="space-y-4 pt-5">
          <div><Label>network join string</Label><Input value={invite} onChange={(e) => setInvite(e.target.value)} placeholder="skein:2:…" autoFocus /></div>
          <p className="text-xs text-[var(--text-muted)]">The join string contains the network secret. It is sent only to the local helper and is never logged.</p>
          {error && <div className="rounded-md border border-[var(--danger)]/40 p-3 text-xs text-[var(--danger)]">{errText(error)}</div>}
          <div className="flex justify-between"><Button variant="ghost" onClick={() => setMode("welcome")}>Back</Button><Button onClick={() => join.mutate()} disabled={busy || !invite}>{busy ? "Joining…" : "Join network"}</Button></div>
        </CardContent></Card>
      )}
      {importOpen && (
        <div className="mt-4 rounded-lg border border-[var(--border)] bg-[var(--surface)] p-4">
          <div className="mono-label">import an existing user config</div>
          <p className="mt-1 text-xs text-[var(--text-muted)]">Path to a user-mode <code>skein.toml</code> (with <code>identity.key</code> beside it). It will be copied to <code>/etc/skein</code> with the socket transport enabled.</p>
          <div className="mt-3 flex gap-2"><Input value={importPath} onChange={(e) => setImportPath(e.target.value)} placeholder="/home/you/.config/skein/skein.toml" /><Button onClick={() => importCfg.mutate()} disabled={busy || !importPath}>Import</Button></div>
        </div>
      )}
      {mode === "done" && result && (
        <Card><CardContent className="space-y-4 pt-5">
          <div className="flex items-center gap-2 text-[var(--success)]"><ShieldCheck size={18} /> Service installed and started</div>
          <div className="grid gap-3 sm:grid-cols-2"><div><div className="mono-label">node id</div><div className="mt-1 font-mono text-xs">{result.node_id}</div></div><div><div className="mono-label">overlay</div><div className="mt-1 font-mono text-xs">{result.overlay_ipv4}</div></div></div>
          {result.join && <CopyField value={result.join} label="network join string (secret)" secret />}
          {result.invite && <CopyField value={result.invite} label="peer invite" />}
          <Button onClick={() => location.reload()}>Open dashboard</Button>
        </CardContent></Card>
      )}
    </div>
  );
}
