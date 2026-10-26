import type { ComponentProps, ReactNode } from "react";
import { cn } from "@kongken/ui/utils";

const CONTROL =
  "w-full min-w-0 rounded-md border border-input bg-transparent px-3 py-2 text-base shadow-xs outline-none md:text-sm focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive disabled:opacity-50";

export function Textarea({ className, ...props }: ComponentProps<"textarea">) {
  return <textarea className={cn(CONTROL, "min-h-24", className)} {...props} />;
}

export function Select({ className, ...props }: ComponentProps<"select">) {
  return <select className={cn(CONTROL, "h-9 py-1", className)} {...props} />;
}

export function Badge({ children, tone = "muted" }: { children: ReactNode; tone?: "muted" | "ok" | "bad" }) {
  const t = { muted: "bg-muted text-muted-foreground", ok: "bg-secondary text-secondary-foreground", bad: "bg-destructive/10 text-destructive" }[tone];
  return <span className={cn("inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium", t)}>{children}</span>;
}

/** The shared look of the catalogue tables: scrolls sideways on a narrow screen instead of widening the page. */
export function TableFrame({ children }: { children: ReactNode }) {
  return <div className="overflow-x-auto rounded-lg border bg-card">{children}</div>;
}
