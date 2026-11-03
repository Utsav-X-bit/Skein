import * as React from "react";
import * as SwitchPrimitive from "@radix-ui/react-switch";
import { cn } from "@/lib/utils";

export const Switch = React.forwardRef<React.ElementRef<typeof SwitchPrimitive.Root>, React.ComponentPropsWithoutRef<typeof SwitchPrimitive.Root>>(
  ({ className, ...props }, ref) => (
    <SwitchPrimitive.Root ref={ref} className={cn("peer inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border border-[var(--border-strong)] bg-[var(--surface-raised)] transition-colors data-[state=checked]:bg-[var(--accent)]", className)} {...props}>
      <SwitchPrimitive.Thumb className="pointer-events-none block h-4 w-4 translate-x-1 rounded-full bg-[var(--text)] transition-transform data-[state=checked]:translate-x-6 data-[state=checked]:bg-[var(--bg)]" />
    </SwitchPrimitive.Root>
  ),
);
Switch.displayName = "Switch";
