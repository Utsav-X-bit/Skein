import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export function EmptyState({ icon, title, description, action, className }: { icon?: ReactNode; title: string; description?: string; action?: ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-col items-center justify-center rounded-lg border border-dashed border-[var(--border)] px-6 py-12 text-center", className)}>
      {icon && <div className="mb-3 text-[var(--text-faint)]">{icon}</div>}
      <div className="text-sm font-medium">{title}</div>
      {description && <p className="mt-1 max-w-md text-xs leading-relaxed text-[var(--text-muted)]">{description}</p>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}
