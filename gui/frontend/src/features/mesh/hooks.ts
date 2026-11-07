import { useEffect, useState, type KeyboardEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { useUI } from "@/lib/store";

/** The selected peer is URL-backed so every surface and deep link agrees. */
export function usePeerParam(): [string | null, (id: string | null) => void] {
  const [params, setParams] = useSearchParams();
  const peer = params.get("peer");
  const setPeer = (id: string | null) => {
    const next = new URLSearchParams(params);
    if (id) next.set("peer", id);
    else next.delete("peer");
    setParams(next, { replace: true });
  };
  return [peer, setPeer];
}

/** Inspector is an aside at desktop widths and a Dialog at 900×600. */
export function useWideLayout(): boolean {
  const [wide, setWide] = useState(
    () => typeof window === "undefined" || window.matchMedia("(min-width: 1100px)").matches,
  );
  useEffect(() => {
    const mq = window.matchMedia("(min-width: 1100px)");
    const onChange = () => setWide(mq.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);
  return wide;
}

/** Settings → Motion and the OS preference both gate link animation. */
export function useMotionAllowed(): boolean {
  const motion = useUI((s) => s.motion);
  const [systemReduced, setSystemReduced] = useState(
    () => typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches,
  );
  useEffect(() => {
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    const onChange = () => setSystemReduced(mq.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);
  return !(motion === "reduced" || (motion === "system" && systemReduced));
}

/**
 * rowKeyNav is the shared keyboard contract for table rows: arrows move
 * between rows in the same table, Enter selects, Escape clears.
 */
export function rowKeyNav(
  e: KeyboardEvent<HTMLElement>,
  onActivate: () => void,
  onEscape: () => void,
) {
  if (e.key === "Enter" || e.key === " ") {
    e.preventDefault();
    onActivate();
    return;
  }
  if (e.key === "Escape") {
    e.preventDefault();
    onEscape();
    return;
  }
  if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
  e.preventDefault();
  const table = e.currentTarget.closest("table");
  if (!table) return;
  const rows = Array.from(table.querySelectorAll<HTMLElement>("tbody tr[data-row]"));
  const index = rows.indexOf(e.currentTarget);
  const next = rows[index + (e.key === "ArrowDown" ? 1 : -1)];
  next?.focus();
}
