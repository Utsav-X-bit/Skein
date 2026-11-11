import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, KeyRound, Loader2, Network, Server, ShieldCheck, Upload } from "lucide-react";
import * as SetupService from "../../../bindings/github.com/Utsav-X-bit/skein/gui/services/setupservice";
import type { BootstrapResult } from "../../../bindings/github.com/Utsav-X-bit/skein/internal/app/models";
import { ServiceService, elevationCancelled, errDisplay, errKind, errText } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { CopyField } from "@/components/ui/copy-field";
import { Input, Label } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { LogoMark } from "@/components/brand/LogoMark";

type Mode = "welcome" | "create" | "join" | "import" | "done";
type Pending = "create" | "join" | "import" | "start" | null;

const workingText: Record<Exclude<Pending, null>, string> = {
  create: "Creating the network. The helper asks for administrator approval, then the daemon writes the configuration and starts the service.",
  join: "Joining the network. The helper asks for administrator approval, then the daemon accepts the invite and starts the service.",
  import: "Importing the configuration. The helper asks for administrator approval, then the daemon copies it to the system path and starts the service.",
  start: "Starting the existing service through the minimal helper.",
};

function PrivilegeNote() {
  return (
    <div className="rounded-md border border-[var(--border)] bg-[var(--surface-raised)] p-3 text-xs leading-relaxed text-[var(--text-muted)]">
      <span className="font-medium text-[var(--text)]">What the administrator step does: </span>
      the helper mints a single-use setup token and registers or installs the system service. The app itself never runs with administrator rights; afterwards it talks only to the daemon over its local control socket. Nothing privileged happens without your approval.
    </div>
  );
}

export function Onboarding({ compact = false, onBegin, onExit }: { compact?: boolean; onBegin?: () => void; onExit?: () => void }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [mode, setMode] = useState<Mode>("welcome");
  const [name, setName] = useState("");
  const [port, setPort] = useState("11010");
  const [mtu, setMtu] = useState("1280");
  const [mdns, setMdns] = useState(true);
  const [invite, setInvite] = useState("");
  const [importPath, setImportPath] = useState("");
  // The bootstrap result is memory only. Its join string is the network
  // secret: it is shown once and never written to storage, history or a URL.
  const [result, setResult] = useState<BootstrapResult | null>(null);

  const refresh = () => {
    for (const key of ["service", "config", "status", "peers", "routes"]) void qc.invalidateQueries({ queryKey: [key] });
  };

  const finish = (r: BootstrapResult) => {
    setResult(r);
    setMode("done");
    refresh();
  };

  // The shell latches setup while the user's own flow runs (onboardingGate.ts)
  // and releases it only on an explicit exit: the result screen holds the
  // once-only join string in component state, so nothing else may unmount it.

  const create = useMutation({
    mutationFn: () => SetupService.Init({ name: name.trim(), port: Number(port), mtu: Number(mtu), mdns }),
    onMutate: onBegin,
    onSuccess: finish,
  });
  const join = useMutation({
    mutationFn: () => SetupService.Join(invite.trim()),
    onMutate: onBegin,
    onSuccess: finish,
  });
  const importCfg = useMutation({
    mutationFn: () => SetupService.Import(importPath.trim()),
    onMutate: onBegin,
    onSuccess: finish,
  });
  const start = useMutation({
    mutationFn: () => ServiceService.Start(),
    onMutate: onBegin,
    onSuccess: () => {
      refresh();
      onExit?.();
      navigate("/");
    },
  });

  const resetAll = () => {
    create.reset();
    join.reset();
    importCfg.reset();
    start.reset();
  };

  const go = (next: Mode) => {
    resetAll();
    setMode(next);
  };

  const pending: Pending = create.isPending
    ? "create"
    : join.isPending
      ? "join"
      : importCfg.isPending
        ? "import"
        : start.isPending
          ? "start"
          : null;

  const failed = create.error ? "create" : join.error ? "join" : importCfg.error ? "import" : start.error ? "start" : null;
  const error = create.error ?? join.error ?? importCfg.error ?? start.error;
  const kind = error ? errKind(error) : "";

  // Exactly one next action per failure. The user is never offered a wall of
  // buttons; Back remains available as navigation, not as a remedy.
  const outcome = (() => {
    if (!failed || !error) return null;
    if (elevationCancelled(error)) {
      return { message: errDisplay(error), label: "Retry with administrator approval", action: "retry" as const };
    }
    const text = errText(error).toLowerCase();
    if (text.includes("already exists")) {
      return {
        message: "A network is already configured on this device. Its configuration and identity were left in place.",
        label: "Start the existing service",
        action: "start" as const,
      };
    }
    if (text.includes("different network")) {
      return {
        message: "This invite belongs to a different network. Switching networks needs a reset first.",
        label: "Open network settings",
        action: "settings" as const,
      };
    }
    if (kind === "Daemon offline") {
      return {
        message: "The daemon is not reachable, so setup cannot complete. Start the service, then retry.",
        label: "Start the service",
        action: "start" as const,
      };
    }
    return { message: errDisplay(error), label: "Retry", action: "retry" as const };
  })();

  const runOutcome = () => {
    if (!outcome) return;
    if (outcome.action === "settings") {
      navigate("/settings?network=switch");
      return;
    }
    if (outcome.action === "start") {
      resetAll();
      start.mutate();
      return;
    }
    if (failed === "create") create.mutate();
    else if (failed === "join") join.mutate();
    else if (failed === "import") importCfg.mutate();
    else if (failed === "start") start.mutate();
  };

  const portValid = Number.isInteger(Number(port)) && Number(port) >= 1 && Number(port) <= 65535;
  const mtuValid = Number.isInteger(Number(mtu)) && Number(mtu) >= 576 && Number(mtu) <= 65535;
  const createReady = name.trim().length > 0 && portValid && mtuValid;

  const shell = compact ? "space-y-4" : "mx-auto flex min-h-full max-w-3xl flex-col justify-center py-8";

  return (
    <div className={shell}>
      {!compact && (
        <div className="mb-8 flex items-center gap-4">
          <LogoMark size={48} animated />
          <div>
            <div className="mono-label">welcome to</div>
            <h1 className="text-3xl font-medium tracking-tight">Skein</h1>
            <p className="text-sm text-[var(--text-muted)]">A private mesh for up to five of your own devices.</p>
          </div>
        </div>
      )}

      {outcome && !pending && (
        <div className="mb-4 space-y-2 rounded-md border border-[var(--danger)]/40 bg-[var(--danger)]/5 p-3 text-xs text-[var(--danger)]" role="alert">
          <p>{outcome.message}</p>
          <Button size="sm" variant="secondary" onClick={runOutcome}>
            {outcome.label}
          </Button>
        </div>
      )}

      {pending && (
        <Card>
          <CardContent className="space-y-3 pt-5">
            <div className="flex items-center gap-2 text-sm font-medium" role="status" aria-live="polite">
              <Loader2 size={16} className="animate-spin text-[var(--accent)]" />
              Working…
            </div>
            <p className="text-sm text-[var(--text-muted)]">{workingText[pending]}</p>
            <p className="text-xs text-[var(--text-muted)]">
              Approve the administrator prompt if it appears. The service is re-read afterwards, so this screen only reports success once the OS confirms it.
            </p>
          </CardContent>
        </Card>
      )}

      {!pending && mode === "welcome" && (
        <div className="grid gap-4 sm:grid-cols-2">
          <button
            onClick={() => go("create")}
            className="group rounded-xl border border-[var(--border)] bg-[var(--surface)] p-6 text-left transition-colors hover:border-[var(--accent)]"
          >
            <Network className="mb-4 text-[var(--accent)]" />
            <div className="text-lg font-medium">Create a network</div>
            <p className="mt-1 text-sm text-[var(--text-muted)]">Start a new mesh and invite your other devices.</p>
            <span className="mt-4 inline-flex items-center gap-1 text-xs text-[var(--accent)]">
              Create <ArrowRight size={13} />
            </span>
          </button>
          <button
            onClick={() => go("join")}
            className="group rounded-xl border border-[var(--border)] bg-[var(--surface)] p-6 text-left transition-colors hover:border-[var(--accent)]"
          >
            <KeyRound className="mb-4 text-[var(--accent)]" />
            <div className="text-lg font-medium">Join a network</div>
            <p className="mt-1 text-sm text-[var(--text-muted)]">Paste a join string from an existing device.</p>
            <span className="mt-4 inline-flex items-center gap-1 text-xs text-[var(--accent)]">
              Join <ArrowRight size={13} />
            </span>
          </button>
          <button
            onClick={() => go("import")}
            className="group rounded-xl border border-[var(--border)] bg-[var(--surface)] p-6 text-left transition-colors hover:border-[var(--accent)]"
          >
            <Upload className="mb-4 text-[var(--accent)]" />
            <div className="text-lg font-medium">Import a configuration</div>
            <p className="mt-1 text-sm text-[var(--text-muted)]">Move a user-mode config into the system path.</p>
            <span className="mt-4 inline-flex items-center gap-1 text-xs text-[var(--accent)]">
              Import <ArrowRight size={13} />
            </span>
          </button>
          {!compact && (
            <button
              onClick={() => start.mutate()}
              disabled={start.isPending}
              className="group rounded-xl border border-[var(--border)] bg-[var(--surface)] p-6 text-left transition-colors hover:border-[var(--accent)]"
            >
              <Server className="mb-4 text-[var(--accent)]" />
              <div className="text-lg font-medium">Use an existing installation</div>
              <p className="mt-1 text-sm text-[var(--text-muted)]">A network is already configured here; start its service and keep it.</p>
              <span className="mt-4 inline-flex items-center gap-1 text-xs text-[var(--accent)]">
                Start service <ArrowRight size={13} />
              </span>
            </button>
          )}
        </div>
      )}

      {!pending && mode === "create" && (
        <Card>
          <CardContent className="space-y-4 pt-5">
            <div>
              <Label htmlFor="create-name">device name</Label>
              <Input id="create-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="studio" maxLength={32} autoFocus />
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div>
                <Label htmlFor="create-port">listen port</Label>
                <Input id="create-port" value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" aria-invalid={!portValid} />
              </div>
              <div>
                <Label htmlFor="create-mtu">MTU</Label>
                <Input id="create-mtu" value={mtu} onChange={(e) => setMtu(e.target.value)} inputMode="numeric" aria-invalid={!mtuValid} />
              </div>
            </div>
            {(!portValid || !mtuValid) && (
              <p className="text-xs text-[var(--warning)]">Port must be 1–65535 and MTU 576–65535.</p>
            )}
            <div className="flex items-center gap-3">
              <Switch id="create-mdns" aria-label="local discovery (mDNS)" checked={mdns} onCheckedChange={setMdns} />
              <Label htmlFor="create-mdns" className="mb-0">local discovery (mDNS)</Label>
            </div>
            <PrivilegeNote />
            <div className="flex justify-between">
              <Button variant="ghost" onClick={() => go("welcome")}>Back</Button>
              <Button onClick={() => create.mutate()} disabled={!createReady}>Create network</Button>
            </div>
          </CardContent>
        </Card>
      )}

      {!pending && mode === "join" && (
        <Card>
          <CardContent className="space-y-4 pt-5">
            <div>
              <Label htmlFor="join-invite">network join string</Label>
              <Input
                id="join-invite"
                type="password"
                autoComplete="off"
                value={invite}
                onChange={(e) => setInvite(e.target.value)}
                placeholder="skein:2:…"
                autoFocus
              />
              <p className="mt-1.5 text-xs text-[var(--text-muted)]">
                The join string contains the network secret. It goes only to the local daemon over its verified control connection and is never logged or stored by this app.
              </p>
            </div>
            <PrivilegeNote />
            <div className="flex justify-between">
              <Button variant="ghost" onClick={() => go("welcome")}>Back</Button>
              <Button onClick={() => join.mutate()} disabled={!invite.trim()}>Join network</Button>
            </div>
          </CardContent>
        </Card>
      )}

      {!pending && mode === "import" && (
        <Card>
          <CardContent className="space-y-4 pt-5">
            <div>
              <Label htmlFor="import-path">path to a user-mode config</Label>
              <Input
                id="import-path"
                value={importPath}
                onChange={(e) => setImportPath(e.target.value)}
                placeholder="/home/you/.config/skein/skein.toml"
                autoFocus
              />
              <p className="mt-1.5 text-xs text-[var(--text-muted)]">
                Point at a user-mode <code>skein.toml</code> with <code>identity.key</code> beside it. The daemon copies it to the system configuration directory over the local control transport.
              </p>
            </div>
            <PrivilegeNote />
            <div className="flex justify-between">
              <Button variant="ghost" onClick={() => go("welcome")}>Back</Button>
              <Button onClick={() => importCfg.mutate()} disabled={!importPath.trim()}>Import configuration</Button>
            </div>
          </CardContent>
        </Card>
      )}

      {!pending && mode === "done" && result && (
        <Card>
          <CardContent className="space-y-4 pt-5">
            <div className="flex items-center gap-2 text-[var(--success)]">
              <ShieldCheck size={18} /> Network ready
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <div>
                <div className="mono-label">node id</div>
                <div className="mt-1 break-all font-mono text-xs">{result.node_id}</div>
              </div>
              <div>
                <div className="mono-label">overlay</div>
                <div className="mt-1 font-mono text-xs">{result.overlay_ipv4}</div>
              </div>
              {result.service && (
                <div>
                  <div className="mono-label">service</div>
                  <div className="mt-1 font-mono text-xs">{result.service}</div>
                </div>
              )}
              {result.config_path && (
                <div className="min-w-0">
                  <div className="mono-label">config path</div>
                  <div className="mt-1 truncate font-mono text-xs" title={result.config_path}>{result.config_path}</div>
                </div>
              )}
            </div>

            {result.join && (
              <div className="space-y-1">
                <CopyField value={result.join} label="network join string (secret) — shown once" secret />
                <p className="text-xs text-[var(--text-muted)]">
                  Anyone with this string can join this network. It is shown once and is not stored by this app; copy it now if you need to add another device.
                </p>
              </div>
            )}
            {result.invite && <CopyField value={result.invite} label="peer invite" />}

            <Button onClick={() => { refresh(); onExit?.(); navigate("/"); }}>Open dashboard</Button>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
