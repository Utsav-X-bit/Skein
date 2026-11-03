import { AlertTriangle } from "lucide-react";
import { Button } from "./button";

export function ErrorState({ title = "Something went wrong", message, remediation, onRetry }: { title?: string; message?: string; remediation?: string; onRetry?: () => void }) {
  return (
    <div className="rounded-lg border border-[var(--danger)]/40 bg-[var(--danger)]/5 p-4">
      <div className="flex items-start gap-3">
        <AlertTriangle className="mt-0.5 shrink-0 text-[var(--danger)]" size={18} />
        <div className="min-w-0 flex-1">
          <div className="text-sm font-medium">{title}</div>
          {message && <div className="mt-1 break-words text-xs text-[var(--text-muted)]">{message}</div>}
          {remediation && <div className="mt-2 text-xs text-[var(--text-faint)]">{remediation}</div>}
        </div>
        {onRetry && <Button size="sm" variant="secondary" onClick={onRetry}>Retry</Button>}
      </div>
    </div>
  );
}
