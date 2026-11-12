import { useEffect, useState } from "react";

/**
 * useReducedMotion reports the OS reduced-motion preference live
 * (Android: Settings → Accessibility → Remove animations). The CSS
 * duration tokens already honour it via `data-motion`; JS-driven
 * motion — the SVG flow dots, sheet drag gestures — must read this
 * hook, because CSS alone cannot stop them.
 */
export function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState(
    () => typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches,
  );
  useEffect(() => {
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    const onChange = () => setReduced(mq.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);
  return reduced;
}
