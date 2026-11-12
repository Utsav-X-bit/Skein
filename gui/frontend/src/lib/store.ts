import { create } from "zustand";
import { persist } from "zustand/middleware";

export type Theme = "dark" | "light" | "hc";
export type Motion = "system" | "reduced" | "full";

type UIState = {
  theme: Theme;
  motion: Motion;
  sidebarCollapsed: boolean;
  networkLabel: string;
  animateFlow: boolean;
  setTheme: (t: Theme) => void;
  setMotion: (m: Motion) => void;
  toggleSidebar: () => void;
  setNetworkLabel: (v: string) => void;
  setAnimateFlow: (v: boolean) => void;
};

export const useUI = create<UIState>()(
  persist(
    (set) => ({
      theme: "dark",
      motion: "system",
      sidebarCollapsed: false,
      networkLabel: "My Skein",
      animateFlow: true,
      setTheme: (theme) => set({ theme }),
      setMotion: (motion) => set({ motion }),
      toggleSidebar: () => set((s) => ({ sidebarCollapsed: !s.sidebarCollapsed })),
      setNetworkLabel: (networkLabel) => set({ networkLabel }),
      setAnimateFlow: (animateFlow) => set({ animateFlow }),
    }),
    { name: "skein-ui" },
  ),
);
