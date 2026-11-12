import { useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { ConnectionService, ServiceService, VpnService, errText } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { useVpnPermission } from "./useVpnPermission";
import { VpnPermissionSheet } from "./VpnPermissionSheet";

const explainerKey = "skein-vpn-explainer-shown";

/**
 * ConnectButton is the sticky Home primary button (design brief
 * §3.1, §5.2). It owns the whole Connect state machine — Connect /
 * Disconnect / Resume / Reconnect — and never shares the screen with
 * a second primary button. The first Connect shows the explainer
 * sheet before the system VPN dialog; the outcome arrives as the
 * vpn:permission event, and only a grant starts the service.
 *
 * Android note: Disconnect stops the VpnService (teardown stops the
 * in-process daemon too — Phase 2), so a later Connect is a full
 * restart, not a warm resume. Resume only applies while the VPN is
 * up and the dataplane is paused.
 */
export function ConnectButton({
  connectionState,
  overlayPrefix,
  onVpnDenied,
  autoStart = false,
}: {
  connectionState: string;
  overlayPrefix?: string;
  onVpnDenied: () => void;
  /** Fire the Connect flow once on mount (onboarding "Connect now"). */
  autoStart?: boolean;
}) {
  const qc = useQueryClient();
  const vpn = useVpnPermission();
  const vpnActive = vpn.vpnActive;
  const [explainerOpen, setExplainerOpen] = useState(false);
  const [awaitingGrant, setAwaitingGrant] = useState(false);
  const [doneFlash, setDoneFlash] = useState(false);
  const doneTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const autoStarted = useRef(false);

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ["service"] });
    void qc.invalidateQueries({ queryKey: ["status"] });
  };

  const start = useMutation({
    mutationFn: () => ServiceService.Start(),
    onSuccess: () => {
      invalidate();
      flashDone();
    },
    onError: (e) => toast.error(errText(e)),
  });
  const stop = useMutation({
    mutationFn: () => ServiceService.Stop(),
    onSuccess: () => {
      invalidate();
      flashDone();
    },
    onError: (e) => toast.error(errText(e)),
  });
  const resume = useMutation({
    mutationFn: () => ConnectionService.Connect(),
    onSuccess: () => {
      invalidate();
      flashDone();
    },
    onError: (e) => toast.error(errText(e)),
  });

  const busy = start.isPending || stop.isPending || resume.isPending || awaitingGrant;

  function flashDone() {
    setDoneFlash(true);
    if (doneTimer.current) clearTimeout(doneTimer.current);
    doneTimer.current = setTimeout(() => setDoneFlash(false), 1200);
  }

  useEffect(() => () => {
    if (doneTimer.current) clearTimeout(doneTimer.current);
  }, []);

  // Onboarding "Connect now": run the Connect flow once, only while
  // disconnected. The explainer is the first thing the user sees.
  useEffect(() => {
    if (!autoStart || autoStarted.current) return;
    if (connectionState !== "disconnected" && connectionState !== "unknown") return;
    autoStarted.current = true;
    connect();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoStart, connectionState]);

  // The grant arrives asynchronously after RequestPermission: only
  // then may the service start. A denial routes to the denied state
  // screen instead of a toast.
  useEffect(() => {
    if (!awaitingGrant) return;
    if (vpn.state === "granted") {
      setAwaitingGrant(false);
      start.mutate();
    } else if (vpn.state === "denied") {
      setAwaitingGrant(false);
      onVpnDenied();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [awaitingGrant, vpn.state]);

  const requestPermission = async () => {
    try {
      await VpnService.RequestPermission();
      setAwaitingGrant(true);
    } catch (e) {
      toast.error(errText(e));
    }
  };

  const connect = () => {
    if (vpn.state === "denied") {
      onVpnDenied();
      return;
    }
    if (vpn.state === "granted") {
      start.mutate();
      return;
    }
    // Unknown: first Connect explains the dialog, later Connects go
    // straight to it (the explainer is a once-per-install courtesy).
    try {
      if (localStorage.getItem(explainerKey) !== "1") {
        setExplainerOpen(true);
        return;
      }
    } catch {
      // Storage unavailable: still explain; the sheet is cheap.
      setExplainerOpen(true);
      return;
    }
    void requestPermission();
  };

  const explainerContinue = () => {
    try {
      localStorage.setItem(explainerKey, "1");
    } catch {
      // Ignore: the explainer simply shows again next time.
    }
    setExplainerOpen(false);
    void requestPermission();
  };

  const connected = connectionState === "connected";
  const paused = connectionState === "paused";
  const errored = connectionState === "error";

  let label = "Connect";
  let action = connect;
  let variant: "default" | "secondary" = "default";
  if (connected && vpnActive) {
    label = "Disconnect";
    action = () => stop.mutate();
    variant = "secondary";
  } else if (paused && vpnActive) {
    label = "Resume";
    action = () => resume.mutate();
  } else if (errored && vpnActive) {
    label = "Reconnect";
    action = () => resume.mutate();
  } else if (errored) {
    label = "Reconnect";
    action = connect;
  }

  return (
    <>
      <Button
        size="lg"
        variant={variant}
        className="h-[var(--connect-h)] w-full text-base active:scale-[0.97]"
        disabled={busy}
        onClick={action}
        aria-live="polite"
      >
        {busy ? (
          <>
            <Loader2 size={18} className="animate-spin" />
            {awaitingGrant ? "Waiting for VPN permission…" : "Working…"}
          </>
        ) : doneFlash ? (
          <>
            <Check size={18} /> Done
          </>
        ) : (
          label
        )}
      </Button>
      <VpnPermissionSheet
        open={explainerOpen}
        onClose={() => setExplainerOpen(false)}
        onContinue={explainerContinue}
        overlayPrefix={overlayPrefix}
      />
    </>
  );
}
