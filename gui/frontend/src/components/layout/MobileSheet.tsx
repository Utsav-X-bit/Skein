import { useEffect, useRef, useState, type ReactNode } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";
import { useReducedMotion } from "@/lib/useReducedMotion";

export type SheetDetent = "peek" | "half" | "full";

const detentHeight: Record<SheetDetent, string> = {
  peek: "40vh",
  half: "70vh",
  full: "92vh",
};

const detentOrder: SheetDetent[] = ["peek", "half", "full"];

/**
 * MobileSheet is the bottom sheet of the design brief (§3.3): three
 * detents, a drag handle, scrim-tap and drag-down dismiss, and a
 * ≤150ms opacity fade instead of a slide when reduced motion is on.
 * Built on Radix Dialog for focus trapping and Escape handling; the
 * sheet positioning is custom because the shared DialogContent is a
 * centred desktop modal.
 */
export function MobileSheet({
  open,
  onClose,
  title,
  description,
  initialDetent = "half",
  children,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: string;
  initialDetent?: SheetDetent;
  children: ReactNode;
}) {
  const reduced = useReducedMotion();
  const [detent, setDetent] = useState<SheetDetent>(initialDetent);
  const [dragY, setDragY] = useState(0);
  const dragStart = useRef<number | null>(null);

  useEffect(() => {
    if (open) {
      setDetent(initialDetent);
      setDragY(0);
    }
  }, [open, initialDetent]);

  const onHandlePointerDown = (e: React.PointerEvent) => {
    // Dragging is direct manipulation, not animation: it stays available
    // under reduced motion, where it is otherwise the only way to resize
    // the sheet without hunting for a 6px dot.
    dragStart.current = e.clientY;
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
  };
  const onHandlePointerMove = (e: React.PointerEvent) => {
    if (dragStart.current === null) return;
    setDragY(Math.max(0, e.clientY - dragStart.current));
  };
  const onHandlePointerUp = (e: React.PointerEvent) => {
    if (dragStart.current === null) return;
    const dy = e.clientY - dragStart.current;
    dragStart.current = null;
    setDragY(0);
    if (dy > 80) {
      onClose();
      return;
    }
    if (dy < -60) {
      const idx = detentOrder.indexOf(detent);
      if (idx < detentOrder.length - 1) setDetent(detentOrder[idx + 1]);
    }
  };

  return (
    <Dialog.Root open={open} onOpenChange={(v) => { if (!v) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="mobile-sheet-overlay fixed inset-0 z-50 bg-black/60" />
        <Dialog.Content
          className={cn(
            "mobile-sheet fixed inset-x-0 bottom-0 z-50 flex flex-col rounded-t-[var(--sheet-radius)] border border-[var(--border-strong)] border-b-0 bg-[var(--surface)] shadow-2xl outline-none",
          )}
          style={{
            height: detentHeight[detent],
            paddingBottom: "env(safe-area-inset-bottom, 0px)",
            transform: dragY > 0 ? `translateY(${dragY}px)` : undefined,
            transition: reduced ? undefined : dragY > 0 ? "none" : "height 220ms ease-out, transform 180ms ease-in",
          }}
        >
          <div
            className="flex shrink-0 touch-none flex-col items-center pb-1 pt-2"
            onPointerDown={onHandlePointerDown}
            onPointerMove={onHandlePointerMove}
            onPointerUp={onHandlePointerUp}
          >
            <div className="h-1.5 w-10 rounded-full bg-[var(--border-strong)]" aria-hidden />
            <div className="mt-2 flex w-full items-start justify-between gap-3 px-4">
              <div className="min-w-0">
                <Dialog.Title className="truncate text-base font-medium">{title}</Dialog.Title>
                {description ? (
                  <Dialog.Description className="truncate text-xs text-[var(--text-muted)]">{description}</Dialog.Description>
                ) : (
                  <Dialog.Description className="sr-only">{title}</Dialog.Description>
                )}
              </div>
              <button
                type="button"
                onClick={onClose}
                aria-label={`Close ${title}`}
                className="flex h-12 w-12 shrink-0 items-center justify-center rounded-full text-[var(--text-muted)] hover:bg-[var(--surface-raised)] hover:text-[var(--text)]"
              >
                <X size={20} />
              </button>
            </div>
          </div>
          {/* Outside the drag container: a pointerdown on a size button
              must resize the sheet, not start a drag. */}
          <div className="flex shrink-0 items-center justify-center" role="group" aria-label="Sheet size">
            {detentOrder.map((d) => (
              <button
                key={d}
                type="button"
                aria-label={`Sheet size ${d}`}
                aria-pressed={detent === d}
                onClick={() => setDetent(d)}
                className="flex h-12 w-10 items-center justify-center"
              >
                <span
                  className={cn(
                    "h-1.5 rounded-full transition-colors",
                    detent === d ? "w-6 bg-[var(--accent)]" : "w-3 bg-[var(--border-strong)]",
                  )}
                />
              </button>
            ))}
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">{children}</div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
