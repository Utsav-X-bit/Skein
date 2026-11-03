import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { Button } from "./button";
import { cn } from "@/lib/utils";

export function CopyField({ value, label, className, secret = false }: { value: string; label?: string; className?: string; secret?: boolean }) {
  const [copied, setCopied] = useState(false);
  const [revealed, setRevealed] = useState(!secret);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch { /* clipboard may be denied; the field stays selectable */ }
  };
  const shown = revealed ? value : "•".repeat(Math.min(48, value.length));
  return (
    <div className={cn("space-y-1.5", className)}>
      {label && <div className="mono-label">{label}</div>}
      <div className="flex items-stretch gap-2">
        <code className="min-w-0 flex-1 select-all overflow-x-auto rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-2 font-mono text-xs text-[var(--text)]">{shown}</code>
        {secret && <Button variant="secondary" size="sm" onClick={() => setRevealed((v) => !v)}>{revealed ? "Hide" : "Reveal"}</Button>}
        <Button variant="secondary" size="icon" aria-label="Copy" onClick={copy}>{copied ? <Check size={16} /> : <Copy size={16} />}</Button>
      </div>
    </div>
  );
}
