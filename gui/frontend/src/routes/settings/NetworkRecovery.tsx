import { useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as SetupService from "../../../bindings/github.com/Utsav-X-bit/skein/gui/services/setupservice";
import type { NetworkResetResult } from "../../../bindings/github.com/Utsav-X-bit/skein/gui/services/models";
import {
  ConnectionService,
  PeerService,
  ServiceService,
  elevationCancelled,
  errDisplay,
  errText,
} from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { Input, Label } from "@/components/ui/input";
import { Onboarding } from "@/routes/onboarding/Onboarding";

// A departure tombstone is gossiped for up to 180 s; a rejoining node may not
// be accepted by an offline peer until that peer reconnects.
const REJOIN_WAIT_MS = 180_000;

function recoveryError(error: unknown): string {
  if (elevationCancelled(error)) return errDisplay(error);
  const text = errText(error);
  if (text.includes("different network")) {
    return "This invite belongs to a different network. Switch networks instead; the current configuration must be reset first.";
  }
  if (/403|forbidden|secret role|permission|access denied/i.test(text)) {
    return "Your account cannot change this network. Ask the device owner to grant access, then retry.";
  }
  if (/invite|checksum|base64/i.test(text)) {
    return "The invite could not be accepted. Ask a member of this network for a fresh invite, then paste it again.";
  }
  if (/cancel|pkexec|elevat/i.test(text)) return errDisplay(error);
  if (/daemon not connected|connect:|connection refused/i.test(text)) {
    return "The daemon could not be reached. Start the service before changing network membership.";
  }
  return "The network change did not finish. Review the live service state, then retry the current step.";
}

type RejoinPhase = "idle" | "waiting" | "observed" | "expired";
type Stage = "" | "leave" | "reset";

export function NetworkRecovery({
  departed,
  configPath,
  mobile = false,
}: {
  departed: boolean;
  configPath?: string;
  /** The mobile shell hides Settings from the setup gate; the desktop one does not. */
  mobile?: boolean;
}) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const open = params.get("network");
  const [invite, setInvite] = useState("");
  const [purge, setPurge] = useState(false);
  const [keepIdentity, setKeepIdentity] = useState(true);
  const [stage, setStage] = useState<Stage>("");
  const [outcome, setOutcome] = useState<NetworkResetResult | null>(null);
  const [left, setLeft] = useState(false);
  const [leaveNotice, setLeaveNotice] = useState("");
  const [rejoinDone, setRejoinDone] = useState(false);
  const [rejoinPhase, setRejoinPhase] = useState<RejoinPhase>("idle");
  const [rejoinUntil, setRejoinUntil] = useState(0);
  const [rejoinAt, setRejoinAt] = useState(0);
  const [now, setNow] = useState(() => Date.now());

  const setMode = (mode: string) =>
    setParams((p) => {
      p.set("network", mode);
      return p;
    });

  const refresh = () => {
    for (const key of ["status", "config", "peers", "routes", "service"]) void qc.invalidateQueries({ queryKey: [key] });
  };

  const waiting = rejoinPhase === "waiting";

  // While waiting for a peer, one bounded peers read replaces the mesh
  // sampler (which is not mounted on this screen). Unknown is not zero: a
  // failed read says so instead of claiming nobody is up.
  const peers = useQuery({
    queryKey: ["peers"],
    queryFn: () => PeerService.List(),
    enabled: waiting,
    refetchInterval: waiting ? 3_000 : false,
    staleTime: 0,
  });

  useEffect(() => {
    if (!waiting) return;
    const id = window.setInterval(() => setNow(Date.now()), 1_000);
    return () => window.clearInterval(id);
  }, [waiting]);

  // Only a read that started after the rejoin can prove a peer is up; the
  // pre-leave cache is stale by definition.
  const observedUp =
    peers.dataUpdatedAt >= rejoinAt &&
    rejoinAt > 0 &&
    (peers.data ?? []).some((peer) => peer.state === "up");

  useEffect(() => {
    if (!waiting) return;
    if (observedUp) setRejoinPhase("observed");
    else if (now >= rejoinUntil) setRejoinPhase("expired");
  }, [waiting, observedUp, now, rejoinUntil]);

  const rejoin = useMutation({
    mutationFn: async () => {
      // Rejoin is idempotent enough to call once; a failed dataplane connect
      // must not spend another invite.
      if (!rejoinDone) {
        await SetupService.Rejoin(invite.trim());
        setRejoinDone(true);
        setInvite("");
      }
      await ConnectionService.Connect();
    },
    onSuccess: () => {
      setLeft(false);
      setRejoinDone(false);
      setLeaveNotice("");
      setNow(Date.now());
      setRejoinAt(Date.now());
      setRejoinUntil(Date.now() + REJOIN_WAIT_MS);
      setRejoinPhase("waiting");
      refresh();
      setParams((p) => {
        p.delete("network");
        return p;
      });
    },
    onSettled: refresh,
  });

  const reset = useMutation({
    mutationFn: async () => {
      if (!departed && !left) {
        setStage("leave");
        const result = await PeerService.LeaveNetwork();
        setLeft(true);
        setLeaveNotice(
          result.propagated
            ? "Departure announced to reachable peers."
            : "Left locally. No peer was reachable, so offline peers learn about it when they reconnect.",
        );
      }
      setStage("reset");
      const result = await SetupService.ResetWithPurge(purge, keepIdentity);
      setOutcome(result);
      return result;
    },
    onSettled: () => {
      setStage("");
      refresh();
    },
  });

  // A completed reset leaves no network on this device, and the shell only
  // hands the window to setup outside Settings ("recovery must stay
  // reachable" keeps Settings uncovered). Staying here therefore showed a
  // dead mesh screen with no way forward; the reset's own outcome is
  // reported by the daemon's system_reset event.
  const resetDone = mobile && outcome !== null && !outcome.stop_error;
  useEffect(() => {
    if (!resetDone) return;
    setParams((p) => {
      p.delete("network");
      return p;
    });
    navigate("/", { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resetDone]);

  const stop = useMutation({
    mutationFn: () => ServiceService.Stop(),
    // Clearing the error is what unlocks the next-network step; the backup
    // path and the reset itself are kept untouched.
    onSuccess: () => setOutcome((value) => (value ? { ...value, stop_error: undefined } : value)),
    onSettled: refresh,
  });

  const busy = rejoin.isPending || reset.isPending || stop.isPending;
  const isOut = departed || left;
  const showRejoin = open === "rejoin" && !outcome;
  const failure = rejoin.error ? "rejoin" : reset.error ? "reset" : null;
  const failureText = failure === "rejoin"
    ? rejoinDone
      ? `Membership was restored, but the data plane did not connect: ${errDisplay(rejoin.error)}. Retry to resume the mesh.`
      : recoveryError(rejoin.error)
    : recoveryError(reset.error);

  const retryFailure = () => {
    if (failure === "rejoin") rejoin.mutate();
    else if (failure === "reset") reset.mutate();
  };

  const steps = [
    { key: "leave", label: "Announce departure", state: isOut ? "done" : stage === "leave" ? "active" : "todo" },
    {
      key: "reset",
      label: purge ? "Purge configuration and identity" : "Back up and reset configuration",
      state: outcome ? "done" : stage === "reset" ? "active" : "todo",
    },
    {
      key: "stop",
      label: "Verify the service stopped",
      state: outcome ? (outcome.stop_error ? "error" : "done") : "todo",
    },
    {
      key: "next",
      label: "Set up the next network",
      state: outcome && !outcome.stop_error ? "active" : "todo",
    },
  ] as const;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Network membership</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-sm text-[var(--text-muted)]">
          {isOut
            ? "This device left the network. Rejoin the same mesh with a fresh invite, or reset before switching to another."
            : "Leaving affects only this device. No peer can remove another peer."}
        </p>

        <div className="flex flex-wrap gap-2">
          {isOut && rejoinPhase === "idle" && (
            <Button variant="secondary" onClick={() => setMode("rejoin")}>
              Rejoin this network
            </Button>
          )}
          <Button variant="secondary" onClick={() => setMode("switch")}>
            {outcome ? "Continue network setup" : "Switch network / Reset"}
          </Button>
        </div>

        {leaveNotice && (
          <p className="text-sm" role="status">
            {leaveNotice}
          </p>
        )}

        {rejoinPhase !== "idle" && (
          <div
            role="status"
            aria-live="polite"
            className={`rounded-md border p-3 text-sm ${
              rejoinPhase === "observed"
                ? "border-[var(--success)]/40 bg-[var(--success)]/5"
                : rejoinPhase === "expired"
                  ? "border-[var(--warning)]/40 bg-[var(--warning)]/5"
                  : "border-[var(--border)] bg-[var(--surface-raised)]"
            }`}
          >
            <div className="font-medium">
              {rejoinPhase === "observed"
                ? "Membership restored. A peer is up; the mesh is rejoining."
                : rejoinPhase === "expired"
                  ? "Membership restored; no peer has been observed up yet."
                  : "Membership restored; waiting for peers"}
            </div>
            {rejoinPhase === "waiting" && (
              <p className="mt-1 text-xs leading-relaxed text-[var(--text-muted)]">
                A departure tombstone is retained for up to 180 seconds, so other devices may take that long to accept this one again. A peer counts as up once it authenticates with this node.
                <span className="tabular"> {Math.max(0, Math.ceil((rejoinUntil - now) / 1000))} s remaining.</span>
              </p>
            )}
            {rejoinPhase === "expired" && (
              <p className="mt-1 text-xs leading-relaxed text-[var(--text-muted)]">
                The tombstone may still be clearing on devices that were offline. A peer that reconnects learns about this rejoin then; nothing else is required on this device.
              </p>
            )}
            {peers.isError && rejoinPhase !== "observed" && (
              <p className="mt-1 text-xs text-[var(--warning)]">Peer status is unavailable: {errDisplay(peers.error)}</p>
            )}
            <div className="mt-2 flex flex-wrap gap-2">
              {rejoinPhase === "expired" && (
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => {
                    setNow(Date.now());
                    setRejoinAt(Date.now());
                    setRejoinUntil(Date.now() + REJOIN_WAIT_MS);
                    setRejoinPhase("waiting");
                  }}
                >
                  Check again
                </Button>
              )}
              <Button size="sm" variant="ghost" asChild>
                <Link to="/logs">Open Diagnostics</Link>
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setRejoinPhase("idle")}>
                Dismiss
              </Button>
            </div>
          </div>
        )}

        <Dialog
          open={!!open}
          onOpenChange={(value) => {
            if (!value && !busy) {
              setParams((p) => {
                p.delete("network");
                return p;
              });
            }
          }}
        >
          <DialogContent
            className="max-h-[90vh] overflow-auto"
            onEscapeKeyDown={(e) => {
              if (busy) e.preventDefault();
            }}
            onPointerDownOutside={(e) => {
              if (busy) e.preventDefault();
            }}
          >
            <DialogTitle>
              {outcome ? "Set up your next network" : showRejoin ? "Rejoin this network" : "Switch networks"}
            </DialogTitle>
            <DialogDescription>
              {outcome
                ? "The old network configuration has been reset. Create a new mesh, join one with a fresh invite, or import a configuration."
                : showRejoin
                  ? "A fresh invite restores membership in the same network with the current credentials. It does not switch networks."
                  : "This device announces its departure with a tombstone, closes its sessions, backs up its configuration, then resets and stops its service. Other devices stay in their network."}
            </DialogDescription>

            {!outcome && (
              <div className="mt-4 space-y-4">
                <ol className="space-y-1.5 text-xs" aria-label="Switch network steps">
                  {steps.map((step, index) => (
                    <li key={step.key} className="flex items-center gap-2">
                      <span
                        aria-hidden
                        className={`flex h-4 w-4 items-center justify-center rounded-full border text-[9px] ${
                          step.state === "done"
                            ? "border-[var(--success)] text-[var(--success)]"
                            : step.state === "error"
                              ? "border-[var(--danger)] text-[var(--danger)]"
                              : step.state === "active"
                                ? "border-[var(--accent)] text-[var(--accent)]"
                                : "border-[var(--border-strong)] text-[var(--text-faint)]"
                        }`}
                      >
                        {step.state === "done" ? "✓" : step.state === "error" ? "!" : index + 1}
                      </span>
                      <span
                        className={
                          step.state === "error"
                            ? "text-[var(--danger)]"
                            : step.state === "todo"
                              ? "text-[var(--text-faint)]"
                              : "text-[var(--text)]"
                        }
                      >
                        {step.label}
                        {step.state === "active" && <span className="text-[var(--text-muted)]"> — working…</span>}
                      </span>
                    </li>
                  ))}
                </ol>

                {showRejoin ? (
                  <div>
                    <Label htmlFor="rejoin-invite">Fresh network invite</Label>
                    {rejoinDone ? (
                      <p className="text-xs text-[var(--text-muted)]">
                        Membership was restored. The data plane did not connect; retry to resume the mesh.
                      </p>
                    ) : (
                      <Input
                        id="rejoin-invite"
                        type="password"
                        autoComplete="off"
                        value={invite}
                        disabled={busy}
                        onChange={(e) => setInvite(e.target.value)}
                        placeholder="skein:2:…"
                      />
                    )}
                    <p className="mt-1.5 text-xs text-[var(--text-muted)]">
                      The invite contains the network secret. It goes only to the local daemon over its verified control connection and is never stored by this app.
                    </p>
                  </div>
                ) : (
                  <>
                    <div className="space-y-2">
                      <label className="flex items-start gap-2 text-sm">
                        <input
                          type="checkbox"
                          className="mt-0.5"
                          checked={keepIdentity}
                          disabled={busy}
                          onChange={(e) => setKeepIdentity(e.target.checked)}
                        />
                        <span>
                          Keep this device&apos;s identity
                          <span className="block text-xs text-[var(--text-muted)]">The identity key stays in place, so peers recognise this device after the switch.</span>
                        </span>
                      </label>
                      <label className="flex items-start gap-2 text-sm">
                        <input
                          type="checkbox"
                          className="mt-0.5"
                          checked={purge}
                          disabled={busy}
                          onChange={(e) => setPurge(e.target.checked)}
                        />
                        <span>
                          Purge without a backup (irreversible)
                          <span className="block text-xs text-[var(--text-muted)]">No reset-bak directory is written; the old configuration cannot be restored by this app.</span>
                        </span>
                      </label>
                    </div>
                    <p className="break-all text-xs leading-relaxed text-[var(--text-muted)]">
                      {purge
                        ? "No new backup is created. Deleted configuration cannot be restored."
                        : `Configuration and identity are backed up beside ${configPath || "the system configuration"}, in a timestamped reset-bak directory. The exact path is shown after the reset. Restoring it needs an administrator and a service restart; the app cannot restore a root-owned backup.`}{" "}
                      {keepIdentity ? "The identity file stays in place." : "The current identity is also removed."}
                    </p>
                  </>
                )}

                {stage && (
                  <p role="status" className="text-sm">
                    {stage === "leave" ? "Announcing this device’s departure…" : "Resetting network configuration and verifying the service stopped…"}
                  </p>
                )}

                {failure && (
                  <div className="space-y-2 rounded-md border border-[var(--danger)]/40 bg-[var(--danger)]/5 p-3 text-xs text-[var(--danger)]" role="alert">
                    <p>{failureText}</p>
                    <div className="flex flex-wrap gap-2">
                      <Button size="sm" variant="secondary" disabled={busy} onClick={retryFailure}>
                        Retry
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={busy}
                        onClick={() => {
                          rejoin.reset();
                          reset.reset();
                          stop.reset();
                        }}
                      >
                        Dismiss
                      </Button>
                    </div>
                  </div>
                )}

                <div className="flex flex-wrap justify-end gap-2">
                  {showRejoin && (
                    <Button
                      variant="ghost"
                      disabled={busy}
                      onClick={() => {
                        rejoin.reset();
                        setRejoinDone(false);
                        setMode("switch");
                      }}
                    >
                      Switch to a different network
                    </Button>
                  )}
                  {!failure && (
                    <Button
                      variant={showRejoin ? "default" : "danger"}
                      disabled={busy || (showRejoin && !rejoinDone && !invite.trim())}
                      onClick={() => (showRejoin ? rejoin.mutate() : reset.mutate())}
                    >
                      {busy
                        ? "Working…"
                        : showRejoin
                          ? rejoinDone
                            ? "Resume mesh"
                            : "Confirm rejoin"
                          : isOut
                            ? "Confirm reset"
                            : "Leave and reset"}
                    </Button>
                  )}
                </div>
              </div>
            )}

            {outcome && (
              <div className="mt-4 space-y-3">
                <div className="rounded-md border border-[var(--border)] p-3 text-sm">
                  <div className="font-medium text-[var(--success)]">Network reset complete</div>
                  <div className="mt-1 break-all text-xs text-[var(--text-muted)]">
                    {outcome.backup ? `Backup saved: ${outcome.backup}` : "Purged without a new backup; this cannot be undone."}
                  </div>
                  {outcome.backup && (
                    <div className="mt-1 text-xs text-[var(--text-faint)]">
                      Restoring this backup needs an administrator and a service restart; this app cannot restore a root-owned backup.
                    </div>
                  )}
                </div>

                {outcome.firewall_error && (
                  <p role="alert" className="text-sm text-[var(--warning)]">
                    Some firewall rules could not be removed. The reset completed; check Firewall in{" "}
                    <Link className="underline" to="/security">Security</Link> before reconnecting.
                  </p>
                )}

                {outcome.stop_error ? (
                  <div className="space-y-2 rounded-md border border-[var(--warning)]/40 bg-[var(--warning)]/5 p-3 text-sm">
                    <p>
                      The configuration was reset and the backup above is kept, but the service did not stop, so it may still hold the old network.
                    </p>
                    <p className="text-xs text-[var(--text-muted)]">Retry only the stop; resetting again would replace this backup.</p>
                    <Button disabled={busy} onClick={() => stop.mutate()}>
                      {stop.isPending ? "Stopping…" : "Retry stop"}
                    </Button>
                    {stop.error && (
                      <p role="alert" className="text-xs text-[var(--danger)]">
                        {errDisplay(stop.error)}
                      </p>
                    )}
                  </div>
                ) : (
                  <Onboarding compact />
                )}
              </div>
            )}
          </DialogContent>
        </Dialog>
      </CardContent>
    </Card>
  );
}
