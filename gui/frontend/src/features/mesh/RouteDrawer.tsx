import { useEffect, useRef } from "react";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { routeSummary, type MeshPath } from "./model";

/** RouteDrawer is the route list without leaving the map. */
export function RouteDrawer({
  open,
  onClose,
  paths,
  selected,
  onSelect,
  nameOf,
}: {
  open: boolean;
  onClose: () => void;
  paths: MeshPath[];
  selected: string | null;
  onSelect: (destination: string) => void;
  nameOf: (id: string) => string;
}) {
  const closeRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    closeRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-40">
      <div className="absolute inset-0 bg-black/40" onClick={onClose} aria-hidden />
      <aside
        role="dialog"
        aria-modal="true"
        aria-label="Routes"
        className="absolute right-0 top-0 flex h-full w-[min(92vw,420px)] flex-col border-l border-[var(--border-strong)] bg-[var(--surface)] shadow-2xl"
      >
        <div className="flex items-center justify-between border-b border-[var(--border)] px-4 py-3">
          <div>
            <div className="mono-label">routes</div>
            <div className="text-sm font-medium">{paths.length} destinations</div>
          </div>
          <Button ref={closeRef} variant="ghost" size="icon" onClick={onClose} aria-label="Close routes">
            <X size={16} />
          </Button>
        </div>
        <div className="min-h-0 flex-1 overflow-auto">
          {paths.length === 0 ? (
            <div className="px-4 py-8 text-center text-xs text-[var(--text-muted)]">
              No routes yet. Routes appear when peers authenticate and advertise a path.
            </div>
          ) : (
            <ul className="divide-y divide-[var(--border)]">
              {paths.map((p) => {
                const chain = p.path.length > 0 ? ["you", ...p.path.map(nameOf)] : ["you", nameOf(p.destination)];
                return (
                  <li key={p.destination}>
                    <button
                      type="button"
                      onClick={() => onSelect(p.destination)}
                      className={`w-full px-4 py-3 text-left hover:bg-[var(--surface-raised)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--ring)] ${selected === p.destination ? "bg-[var(--accent-soft)]" : ""}`}
                    >
                      <div className="flex items-center justify-between gap-3">
                        <span className="font-mono text-xs">{nameOf(p.destination)}</span>
                        <span className="tabular text-xs text-[var(--text-muted)]">{p.costMs.toFixed(1)} ms</span>
                      </div>
                      <div className="mt-1 text-[11px] text-[var(--text-muted)]">
                        {routeSummary(p, nameOf)} · {p.hops} {p.hops === 1 ? "hop" : "hops"}
                      </div>
                      <div className="mt-1 truncate font-mono text-[11px] text-[var(--text-faint)]">{chain.join(" → ")}</div>
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </aside>
    </div>
  );
}
