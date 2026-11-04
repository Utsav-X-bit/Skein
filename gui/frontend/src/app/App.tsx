import { HashRouter, Route, Routes } from "react-router-dom";
import { AppShell } from "@/components/layout/AppShell";
import { Overview } from "@/routes/overview/Overview";
import { Network } from "@/routes/network/Network";
import { Peers } from "@/routes/peers/Peers";
import { Invites } from "@/routes/invites/Invites";
import { Security } from "@/routes/security/Security";
import { Activity } from "@/routes/activity/Activity";
import { Logs } from "@/routes/logs/Logs";
import { Settings } from "@/routes/settings/Settings";
import { About } from "@/routes/about/About";

export function App() {
  return (
    <HashRouter>
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
    </HashRouter>
  );
}
