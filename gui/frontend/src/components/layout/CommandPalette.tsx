import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Activity, LayoutDashboard, ListTree, Radio, ScrollText, Settings, Shield, Users } from "lucide-react";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

const commands = [
  { label: "Overview", to: "/", icon: LayoutDashboard },
  { label: "Network topology", to: "/network", icon: ListTree },
  { label: "Peers", to: "/peers", icon: Users },
  { label: "Invitations", to: "/invites", icon: Radio },
  { label: "Security & firewall", to: "/security", icon: Shield },
  { label: "Activity", to: "/activity", icon: Activity },
  { label: "Logs & diagnostics", to: "/logs", icon: ScrollText },
  { label: "Settings", to: "/settings", icon: Settings },
];

export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const [q, setQ] = useState("");
  const nav = useNavigate();
  const filtered = useMemo(() => commands.filter((c) => c.label.toLowerCase().includes(q.toLowerCase())), [q]);
  useEffect(() => { if (!open) setQ(""); }, [open]);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="p-2">
        <DialogTitle className="sr-only">Command palette</DialogTitle>
        <Input autoFocus placeholder="Jump to…" value={q} onChange={(e) => setQ(e.target.value)} className="border-0 bg-transparent focus-visible:ring-0" />
        <div className="mt-2 max-h-80 overflow-auto">
          {filtered.map(({ label, to, icon: Icon }) => (
            <button
              key={to}
              className={cn("flex w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm text-[var(--text-muted)] hover:bg-[var(--surface-raised)] hover:text-[var(--text)]")}
              onClick={() => { onOpenChange(false); nav(to); }}
            >
              <Icon size={16} /> {label}
            </button>
          ))}
          {filtered.length === 0 && <div className="px-3 py-6 text-center text-xs text-[var(--text-faint)]">No commands</div>}
        </div>
      </DialogContent>
    </Dialog>
  );
}
