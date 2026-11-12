import { HashRouter, Navigate, Route, Routes } from "react-router-dom";
import { Toaster } from "sonner";
import { AppShell } from "@/components/layout/AppShell";
import { MobileShell } from "@/components/layout/MobileShell";
import { useIsMobile } from "@/lib/isMobile";
import { Overview } from "@/routes/overview/Overview";
import { MobileOverview } from "@/routes/overview/MobileOverview";
import { Network } from "@/routes/network/Network";
import { Peers } from "@/routes/peers/Peers";
import { MobilePeers } from "@/routes/peers/MobilePeers";
import { Invites } from "@/routes/invites/Invites";
import { MobileInvites, MobileSettings } from "@/routes/mobile";
import { Security } from "@/routes/security/Security";
import { Activity } from "@/routes/activity/Activity";
import { Logs } from "@/routes/logs/Logs";
import { Settings } from "@/routes/settings/Settings";
import { About } from "@/routes/about/About";

export function App() {
  const isMobile = useIsMobile();

  return (
    <HashRouter>
      {isMobile ? (
        <Routes>
          <Route element={<MobileShell />}>
            <Route index element={<MobileOverview />} />
            <Route path="peers" element={<MobilePeers />} />
            <Route path="activity" element={<Activity />} />
            <Route path="invites" element={<MobileInvites />} />
            <Route path="settings" element={<MobileSettings />} />
            <Route path="about" element={<About />} />
            {/* Desktop-only pages have no mobile tab; never leave a blank screen. */}
            <Route path="network" element={<Navigate to="/" replace />} />
            <Route path="security" element={<Navigate to="/" replace />} />
            <Route path="logs" element={<Navigate to="/" replace />} />
          </Route>
        </Routes>
      ) : (
        <Routes>
          <Route element={<AppShell />}>
            <Route index element={<Overview />} />
            <Route path="network" element={<Network />} />
            <Route path="peers" element={<Peers />} />
            <Route path="invites" element={<Invites />} />
            <Route path="security" element={<Security />} />
            <Route path="activity" element={<Activity />} />
            <Route path="logs" element={<Logs />} />
            <Route path="settings" element={<Settings />} />
            <Route path="about" element={<About />} />
          </Route>
        </Routes>
      )}
      {/* Mobile: bottom-center, lifted above the bottom nav; desktop: bottom-right as before. */}
      <Toaster
        theme="dark"
        position={isMobile ? "bottom-center" : "bottom-right"}
        closeButton
        style={isMobile ? { bottom: "calc(var(--nav-h) + 12px)" } : undefined}
      />
    </HashRouter>
  );
}
