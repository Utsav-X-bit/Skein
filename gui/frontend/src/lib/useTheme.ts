import { useEffect } from "react";
import { useUI } from "./store";

export function useTheme() {
  const { theme, motion } = useUI();
  useEffect(() => {
    const root = document.documentElement;
    root.classList.remove("dark", "light", "hc");
    root.classList.add(theme);
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const reduced = motion === "reduced" || (motion === "system" && reduce);
    root.classList.toggle("reduce-motion", reduced);
  }, [theme, motion]);
}
