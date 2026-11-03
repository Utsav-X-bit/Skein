import { cn } from "@/lib/utils";

export type StatusTone = "up" | "down" | "degraded" | "paused" | "error" | "info";

const colors: Record<StatusTone, string> = {
  up: "bg-[var(--success)]",
  down: "bg-[var(--text-faint)]",
  degraded: "bg-[var(--relay)]",
  paused: "bg-[var(--warning)]",
  error: "bg-[var(--danger)]",
  info: "bg-[var(--info)]",
};

export function StatusDot({ tone = "down", pulse = false, className }: { tone?: StatusTone; pulse?: boolean; className?: string }) {
  return <span aria-hidden className={cn("inline-block h-2 w-2 rounded-full", colors[tone], pulse && "animate-pulse-dot", className)} />;
}
