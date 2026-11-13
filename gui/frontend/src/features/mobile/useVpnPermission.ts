import { useCallback, useEffect, useState } from "react";
import { Events } from "@wailsio/runtime";
import { VpnService, type VpnPermissionEvent } from "@/lib/api";

export type VpnPermissionState = "granted" | "denied" | "unknown";

/**
 * useVpnPermission is the frontend end of the B1 bridge: it reads the
 * vpn_permission state and subscribes to the vpn:permission event the
 * Go side emits when the system dialog is answered. The event is the
 * only way the outcome arrives — RequestPermission returns as soon as
 * the dialog is launched.
 *
 * Android owns this permission, so it can change while the app is not
 * looking: the user grants or revokes it in system Settings, or comes
 * back from the consent dialog. The state is therefore re-read
 * whenever the app becomes visible again, not just at mount — a stale
 * "granted" made Connect start a service Android would refuse, and a
 * stale "denied" left the user on the denied screen after granting.
 */
export function useVpnPermission() {
  const [state, setState] = useState<VpnPermissionState>("unknown");

  const refresh = useCallback(async () => {
    try {
      const s = await VpnService.Status();
      setState((s.state as VpnPermissionState) || "unknown");
    } catch {
      // The bridge is unavailable (desktop): stay unknown; the
      // mobile shell never renders there.
    }
  }, []);

  useEffect(() => {
    void refresh();
    const off = Events.On("vpn:permission", (ev) => {
      const data = ev.data as VpnPermissionEvent;
      setState((data.state as VpnPermissionState) || "unknown");
      void refresh();
    });
    const onVisible = () => {
      if (document.visibilityState !== "hidden") void refresh();
    };
    document.addEventListener("visibilitychange", onVisible);
    window.addEventListener("focus", onVisible);
    return () => {
      off();
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("focus", onVisible);
    };
  }, [refresh]);

  return { state, refresh };
}
