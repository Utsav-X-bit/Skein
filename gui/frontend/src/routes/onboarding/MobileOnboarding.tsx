import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, ArrowRight, KeyRound, Loader2, Network, Server, ShieldCheck, Upload } from "lucide-react";
import { toast } from "sonner";
import { SetupService, errDisplay, errText } from "@/lib/api";
import type { BootstrapResult } from "../../../bindings/github.com/Utsav-X-bit/skein/internal/app/models";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { CopyField } from "@/components/ui/copy-field";
import { Input, Label } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { LogoMark } from "@/components/brand/LogoMark";

type Mode = "welcome" | "create" | "join" | "import" | "done";
type Pending = "create" | "join" | "import" | null;

const workingText: Record<Exclude<Pending, null>, string> = {
  create: "Creating the network. The daemon writes the configuration and starts the VPN service.",
  join: "Joining the network. The daemon accepts the invite and starts the VPN service.",
  import: "Importing the configuration. The daemon copies it into the app files and starts the VPN service.",
};

function VpnNote() {
  return (
    <div className="rounded-md border border-[var(--border)] bg-[var(--surface-raised)] p-3 text-xs leading-relaxed text-[var(--text-muted)]">
      <span className="font-medium text-[var(--text)]">Skein runs its VPN inside this app. </span>
      Android will ask you to allow it when you first connect — Skein cannot skip that dialog, and nothing is connected until you say yes.
      You can revoke the permission anytime in system Settings → VPN.
    </div>
  );
}

function StepDots({ step, total }: { step: number; total: number }) {
  return (
    <div className="flex items-center gap-1.5" aria-label={`Step ${step} of ${total}`}>
      {Array.from({ length: total }, (_, i) => (
        <span
          key={i}
          className="h-1.5 w-6 rounded-full"
          style={{ background: i < step ? "var(--accent)" : "var(--border-strong)" }}
        />
      ))}
    </div>
  );
}

/**
 * MobileOnboarding is the first-run flow (design brief §5.1): welcome
 * → create | join | import → done, in the mobile design language.
 * It is NOT the desktop Onboarding: there is no helper or administrator
 * copy — the VPN note replaces it — and the bootstrap server is
 * prepared with SetupService.EnsureBootstrap(verb) before Init/Join/
 * Import so the in-process bootstrap serves the token; abandoning a
 * step cancels it. The join string on Done is memory-only, shown once.
 */
export function MobileOnboarding({ onBegin, onExit }: { onBegin?: () => void; onExit?: () => void }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [mode, setMode] = useState<Mode>("welcome");
  const [name, setName] = useState("");
  const [port, setPort] = useState("11010");
  const [mtu, setMtu] = useState("1280");
  const [mdns, setMdns] = useState(true);
  const [invite, setInvite] = useState("");
  const [importPath, setImportPath] = useState("");
  const [result, setResult] = useState<BootstrapResult | null>(null);

  // Prepare the in-process bootstrap server for the chosen verb before
  // the matching Init/Join/Import runs; the token is single-use and
  // bound to the verb, so switching modes cancels the old one.
  useEffect(() => {
    if (mode !== "create" && mode !== "join" && mode !== "import") return;
    const verb = mode === "create" ? "init" : mode;
    SetupService.EnsureBootstrap(verb).catch((e: unknown) => toast.error(errText(e)));
    return () => {
      SetupService.CancelBootstrap().catch(() => undefined);
    };
  }, [mode]);

  const refresh = () => {
    for (const key of ["service", "config", "status", "peers", "routes"]) void qc.invalidateQueries({ queryKey: [key] });
  };

  const finish = (r: BootstrapResult) => {
    setResult(r);
    setMode("done");
    refresh();
  };

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

  const resetAll = () => {
    create.reset();
    join.reset();
    importCfg.reset();
  };

  const go = (next: Mode) => {
    resetAll();
    setMode(next);
  };

  const back = () => {
    go("welcome");
  };

  const pending: Pending = create.isPending ? "create" : join.isPending ? "join" : importCfg.isPending ? "import" : null;
  const failed = create.error ? "create" : join.error ? "join" : importCfg.error ? "import" : null;
  const error = create.error ?? join.error ?? importCfg.error;

  const step = mode === "welcome" ? 1 : mode === "done" ? 3 : 2;

  return (
    <div className="mx-auto w-full max-w-md space-y-6">
      {/* Top app bar (Material small top app bar, 56dp): navigation
          icon on the left, step indicator on the right. The
          container already adds the status-bar inset above this
          row, so the bar reads as a deliberate header instead of
          pills floating under the status bar. Back is a full 48dp
          touch target. */}
      <div className="flex min-h-14 items-center justify-between gap-3">
        {mode !== "welcome" && mode !== "done" ? (
          <Button variant="ghost" size="sm" className="h-12 min-w-12 px-3" onClick={back} aria-label="Back to welcome">
            <ArrowLeft size={18} /> Back
          </Button>
        ) : (
          <span aria-hidden="true" className="w-12" />
        )}
        <StepDots step={step} total={3} />
      </div>

      {mode === "welcome" && (
        <>
          <div className="space-y-3">
            <LogoMark size={44} />
            <h1 className="text-balance text-2xl font-medium tracking-tight">Welcome to Skein</h1>
            <p className="text-sm leading-relaxed text-[var(--text-muted)]">
              A private mesh VPN for your devices. Create a network, join one with an invite, or import a configuration you already have.
            </p>
          </div>
          <VpnNote />
          <div className="space-y-2">
            <Button size="lg" className="h-[var(--connect-h)] w-full" onClick={() => go("create")}>
              <Network size={17} /> Create a network <ArrowRight size={16} />
            </Button>
            <Button size="lg" variant="secondary" className="h-[var(--connect-h)] w-full" onClick={() => go("join")}>
              <KeyRound size={17} /> Join with an invite
            </Button>
            <Button size="lg" variant="secondary" className="h-[var(--connect-h)] w-full" onClick={() => go("import")}>
              <Upload size={17} /> Import a configuration
            </Button>
          </div>
        </>
      )}

      {mode === "create" && (
        <Card>
          <CardContent className="space-y-4 pt-4">
            <div>
              <h2 className="text-lg font-medium">Create a network</h2>
              <p className="text-sm text-[var(--text-muted)]">This device becomes the first member. You will get a join string to share.</p>
            </div>
            <div>
              <Label htmlFor="m-name">network name</Label>
              <Input id="m-name" value={name} maxLength={32} onChange={(e) => setName(e.target.value)} placeholder="my-mesh" className="h-12" />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <Label htmlFor="m-port">listen port</Label>
                <Input id="m-port" value={port} inputMode="numeric" onChange={(e) => setPort(e.target.value)} className="h-12" />
              </div>
              <div>
                <Label htmlFor="m-mtu">MTU</Label>
                <Input id="m-mtu" value={mtu} inputMode="numeric" onChange={(e) => setMtu(e.target.value)} className="h-12" />
              </div>
            </div>
            <div className="flex items-center gap-3">
              <Switch id="m-mdns" aria-label="local discovery (mDNS)" checked={mdns} onCheckedChange={setMdns} />
              <Label htmlFor="m-mdns" className="mb-0">local discovery (mDNS)</Label>
            </div>
            {pending && <p className="text-xs text-[var(--text-muted)]" role="status"><Loader2 size={13} className="mr-1 inline animate-spin" />{workingText[pending]}</p>}
            {failed && (
              <div role="alert" className="rounded-md border border-[var(--danger)]/40 bg-[var(--danger)]/5 p-3 text-xs text-[var(--danger)]">
                {errDisplay(error)}
              </div>
            )}
            <Button size="lg" className="h-[var(--connect-h)] w-full" disabled={pending !== null || !name.trim()} onClick={() => create.mutate()}>
              <Server size={16} /> {pending === "create" ? "Creating…" : "Create network"}
            </Button>
          </CardContent>
        </Card>
      )}

      {mode === "join" && (
        <Card>
          <CardContent className="space-y-4 pt-4">
            <div>
              <h2 className="text-lg font-medium">Join with an invite</h2>
              <p className="text-sm text-[var(--text-muted)]">Paste the network join string from a member. It carries the network secret — Skein never stores it.</p>
            </div>
            <div>
              <Label htmlFor="m-invite">join string</Label>
              <Input
                id="m-invite"
                value={invite}
                onChange={(e) => setInvite(e.target.value)}
                placeholder="skein:2:…"
                autoCapitalize="off"
                autoCorrect="off"
                className="h-12 font-mono text-xs"
              />
            </div>
            {pending && <p className="text-xs text-[var(--text-muted)]" role="status"><Loader2 size={13} className="mr-1 inline animate-spin" />{workingText[pending]}</p>}
            {failed && (
              <div role="alert" className="rounded-md border border-[var(--danger)]/40 bg-[var(--danger)]/5 p-3 text-xs text-[var(--danger)]">
                {errDisplay(error)}
              </div>
            )}
            <Button size="lg" className="h-[var(--connect-h)] w-full" disabled={pending !== null || !invite.trim()} onClick={() => join.mutate()}>
              <KeyRound size={16} /> {pending === "join" ? "Joining…" : "Join network"}
            </Button>
          </CardContent>
        </Card>
      )}

      {mode === "import" && (
        <Card>
          <CardContent className="space-y-4 pt-4">
            <div>
              <h2 className="text-lg font-medium">Import a configuration</h2>
              <p className="text-sm text-[var(--text-muted)]">Import a skein.toml you already have, by path inside the app&apos;s files.</p>
            </div>
            <div>
              <Label htmlFor="m-import">configuration path</Label>
              <Input id="m-import" value={importPath} onChange={(e) => setImportPath(e.target.value)} placeholder="skein.toml" className="h-12 font-mono text-xs" />
            </div>
            {pending && <p className="text-xs text-[var(--text-muted)]" role="status"><Loader2 size={13} className="mr-1 inline animate-spin" />{workingText[pending]}</p>}
            {failed && (
              <div role="alert" className="rounded-md border border-[var(--danger)]/40 bg-[var(--danger)]/5 p-3 text-xs text-[var(--danger)]">
                {errDisplay(error)}
              </div>
            )}
            <Button size="lg" className="h-[var(--connect-h)] w-full" disabled={pending !== null || !importPath.trim()} onClick={() => importCfg.mutate()}>
              <Upload size={16} /> {pending === "import" ? "Importing…" : "Import configuration"}
            </Button>
          </CardContent>
        </Card>
      )}

      {mode === "done" && (
        <>
          <div className="space-y-2">
            <ShieldCheck size={28} className="text-[var(--success)]" />
            <h1 className="text-2xl font-medium tracking-tight">You&apos;re in</h1>
            <p className="text-sm leading-relaxed text-[var(--text-muted)]">
              The network is set up on this device. Share the join string below to add another device — it is shown once and never stored.
            </p>
          </div>
          {result?.join && (
            <CopyField value={result.join} label="network join string" secret />
          )}
          <VpnNote />
          <div className="space-y-2">
            <Button
              size="lg"
              className="h-[var(--connect-h)] w-full"
              onClick={() => {
                onExit?.();
                navigate("/", { state: { connectNow: true } });
              }}
            >
              Connect now
            </Button>
            <Button
              size="lg"
              variant="secondary"
              className="h-12 w-full"
              onClick={() => {
                onExit?.();
                navigate("/");
              }}
            >
              Explore first
            </Button>
          </div>
        </>
      )}
    </div>
  );
}
