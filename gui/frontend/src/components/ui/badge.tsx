import * as React from "react";
import { cn } from "@/lib/utils";

type Tone = "neutral" | "accent" | "relay" | "danger" | "info" | "success" | "warning";
const tones: Record<Tone, string> = {
  neutral: "border-[var(--border-strong)] text-[var(--text-muted)]",
  accent: "border-[var(--accent)] text-[var(--accent)]",
  relay: "border-[var(--relay)] text-[var(--relay)]",
  danger: "border-[var(--danger)] text-[var(--danger)]",
  info: "border-[var(--info)] text-[var(--info)]",
  success: "border-[var(--success)] text-[var(--success)]",
  warning: "border-[var(--warning)] text-[var(--warning)]",
};
export function Badge({ tone = "neutral", className, ...props }: React.HTMLAttributes<HTMLSpanElement> & { tone?: Tone }) {
  return <span className={cn("inline-flex items-center gap-1 rounded-full border px-2 py-0.5 font-mono text-[10px] uppercase tracking-[0.08em]", tones[tone], className)} {...props} />;
}
