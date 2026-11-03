import { useEffect, useState } from "react";
import { Outlet } from "react-router-dom";
import { Sidebar } from "./Sidebar";
import { CommandBar } from "./CommandBar";
import { CommandPalette } from "./CommandPalette";
import { useTheme } from "@/lib/useTheme";
import { useQuery } from "@tanstack/react-query";
import { NetworkService, ServiceService } from "@/lib/api";
import { Onboarding } from "@/routes/onboarding/Onboarding";

export function AppShell() {
  useTheme();
  const [palette, setPalette] = useState(false);
  const svc = useQuery({ queryKey: ["service"], queryFn: () => ServiceService.Status(), refetchInterval: 5_000 });
  const status = useQuery({ queryKey: ["status"], queryFn: () => NetworkService.Status(), refetchInterval: 2_000, retry: 0 });
  const needsSetup = svc.data?.state === "missing" && status.isError;
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); setPalette((v) => !v); }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  return (
    <div className="flex h-full min-h-0 bg-[var(--bg)] text-[var(--text)]">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        <CommandBar onPalette={() => setPalette(true)} />
        <main className="min-h-0 flex-1 overflow-auto p-4 md:p-6">
          {needsSetup ? <Onboarding /> : <Outlet />}
        </main>
      </div>
      <CommandPalette open={palette} onOpenChange={setPalette} />
    </div>
  );
}
