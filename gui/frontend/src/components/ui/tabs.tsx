import * as React from "react";
import * as TabsPrimitive from "@radix-ui/react-tabs";
import { cn } from "@/lib/utils";

export const Tabs = TabsPrimitive.Root;
export const TabsList = React.forwardRef<React.ElementRef<typeof TabsPrimitive.List>, React.ComponentPropsWithoutRef<typeof TabsPrimitive.List>>(
  ({ className, ...props }, ref) => (
    <TabsPrimitive.List ref={ref} className={cn("inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--surface)] p-1", className)} {...props} />
  ),
);
TabsList.displayName = "TabsList";
export const TabsTrigger = React.forwardRef<React.ElementRef<typeof TabsPrimitive.Trigger>, React.ComponentPropsWithoutRef<typeof TabsPrimitive.Trigger>>(
  ({ className, ...props }, ref) => (
    <TabsPrimitive.Trigger ref={ref} className={cn("rounded px-3 py-1.5 text-xs text-[var(--text-muted)] transition-colors hover:text-[var(--text)] data-[state=active]:bg-[var(--surface-raised)] data-[state=active]:text-[var(--text)]", className)} {...props} />
  ),
);
TabsTrigger.displayName = "TabsTrigger";
export const TabsContent = TabsPrimitive.Content;
