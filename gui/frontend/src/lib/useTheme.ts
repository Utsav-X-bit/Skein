import { useEffect } from "react";
import { useUI } from "./store";

export function useTheme() {
  const { theme, motion } = useUI();
  useEffect(() => {
    const root = document.documentElement;
    root.classList.remove("dark", "light", "hc");
    root.classList.add(theme);
    // tokens.css drives the duration tokens off this attribute; it
    // was never set, so reduced-motion durations never applied.
    root.setAttribute("data-motion", motion);
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const reduced = motion === "reduced" || (motion === "system" && reduce);
    root.classList.toggle("reduce-motion", reduced);
  }, [theme, motion]);
}
