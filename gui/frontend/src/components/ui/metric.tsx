import { cn } from "@/lib/utils";

export function Metric({ label, value, hint, tone, className }: { label: string; value: string; hint?: string; tone?: string; className?: string }) {
  return (
    <div className={cn("min-w-0", className)}>
      <div className="mono-label truncate">{label}</div>
      <div className={cn("tabular mt-1 text-xl font-medium tracking-tight", tone)}>{value}</div>
      {hint && <div className="mt-0.5 text-xs leading-snug text-[var(--text-faint)]">{hint}</div>}
    </div>
  );
}
