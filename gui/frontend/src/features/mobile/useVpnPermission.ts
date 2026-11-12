import { useCallback, useEffect, useState } from "react";
import { Events } from "@wailsio/runtime";
import { VpnService, type VpnPermissionEvent } from "@/lib/api";

export type VpnPermissionState = "granted" | "denied" | "unknown";

/**
 * useVpnPermission is the frontend end of the B1 bridge: it reads
 * the vpn_permission state and subscribes to the vpn:permission
 * event the Go side emits when the system dialog is answered. The
 * event is the only way the outcome arrives — RequestPermission
 * returns as soon as the dialog is launched.
 */
export function useVpnPermission() {
  const [state, setState] = useState<VpnPermissionState>("unknown");
  const [vpnActive, setVpnActive] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const s = await VpnService.Status();
      setState((s.state as VpnPermissionState) || "unknown");
      setVpnActive(s.vpn_active);
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
    return () => {
      off();
    };
  }, [refresh]);

  return { state, vpnActive, refresh };
}
