import { useId, type ComponentProps, type ReactNode } from "react";
import { cn } from "@kongken/ui/utils";
import { Input } from "@kongken/ui/input";
import { CircleNotch, WarningCircle, CheckCircle } from "@phosphor-icons/react";

export function Spinner({ label = "加载中" }: { label?: string }) {
  return (
    <div role="status" className="flex items-center gap-2 py-8 text-sm text-muted-foreground">
      <CircleNotch className="size-4 animate-spin" aria-hidden /> {label}
    </div>
  );
}

export function Alert({ kind = "error", children, className }: { kind?: "error" | "info" | "success"; children: ReactNode; className?: string }) {
  const Icon = kind === "success" ? CheckCircle : WarningCircle;
  const tone =
    kind === "error"
      ? "border-destructive/40 bg-destructive/5 text-destructive"
      : kind === "success"
        ? "border-emerald-600/40 bg-emerald-50 text-emerald-800"
        : "border-primary/30 bg-accent text-accent-foreground";
  return (
    <div role={kind === "error" ? "alert" : "status"} className={cn("flex items-start gap-2 rounded-md border px-3 py-2 text-sm", tone, className)}>
      <Icon className="mt-0.5 size-4 shrink-0" aria-hidden />
      <div className="min-w-0 break-words">{children}</div>
    </div>
  );
}

export function Badge({ children, tone = "muted" }: { children: ReactNode; tone?: "muted" | "ok" | "warn" | "bad" }) {
  const t = { muted: "bg-muted text-muted-foreground", ok: "bg-emerald-100 text-emerald-800", warn: "bg-amber-100 text-amber-800", bad: "bg-red-100 text-red-800" }[tone];
  return <span className={cn("inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium", t)}>{children}</span>;
}

const control =
  "w-full min-w-0 rounded-md border border-input bg-transparent px-3 py-2 text-base shadow-xs outline-none md:text-sm focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive disabled:opacity-50";

export function Textarea({ className, ...props }: ComponentProps<"textarea">) {
  return <textarea className={cn(control, "min-h-24", className)} {...props} />;
}

export function Select({ className, ...props }: ComponentProps<"select">) {
  return <select className={cn(control, "h-9 py-1", className)} {...props} />;
}

/** A labelled control; the label is tied to the control for assistive technology. */
export function Field({
  label,
  hint,
  error,
  children,
}: {
  label: string;
  hint?: string;
  error?: string;
  children: (props: { id: string; "aria-invalid"?: true; "aria-describedby"?: string }) => ReactNode;
}) {
  const id = useId();
  const describedBy = error || hint ? `${id}-d` : undefined;
  return (
    <div className="grid gap-1">
      <label htmlFor={id} className="text-sm font-medium">
        {label}
      </label>
      {children({ id, ...(error ? { "aria-invalid": true as const } : {}), "aria-describedby": describedBy })}
      {error ? (
        <p id={describedBy} className="text-xs text-destructive">
          {error}
        </p>
      ) : hint ? (
        <p id={describedBy} className="text-xs text-muted-foreground">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

export function TextField({ label, hint, error, ...props }: { label: string; hint?: string; error?: string } & ComponentProps<"input">) {
  return <Field label={label} hint={hint} error={error}>{(a) => <Input {...a} {...props} />}</Field>;
}

export function Stat({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="rounded-lg border bg-card px-4 py-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 text-xl font-semibold tabular-nums">{value}</div>
    </div>
  );
}

export function PageTitle({ children, sub }: { children: ReactNode; sub?: ReactNode }) {
  return (
    <header className="mb-5">
      <h1 className="text-2xl font-semibold tracking-tight">{children}</h1>
      {sub ? <p className="mt-1 text-sm text-muted-foreground">{sub}</p> : null}
    </header>
  );
}
