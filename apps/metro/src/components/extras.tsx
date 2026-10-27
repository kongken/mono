import { useId, type ComponentProps, type ReactNode } from "react";
import { cn } from "@kongken/ui/utils";
import { Input } from "@kongken/ui/input";
import { CircleNotch, WarningCircle, CheckCircle } from "@phosphor-icons/react";

export function Spinner({ label = "加载中" }: { label?: string }) {
  return (
    <div role="status" className="flex items-center gap-2 py-10 text-sm text-on-variant">
      <CircleNotch className="size-4 animate-spin" aria-hidden /> {label}
    </div>
  );
}

export function Alert({ kind = "error", children, className }: { kind?: "error" | "info" | "success"; children: ReactNode; className?: string }) {
  const Icon = kind === "success" ? CheckCircle : WarningCircle;
  const tone =
    kind === "error"
      ? "bg-error-container text-on-error-container"
      : kind === "success"
        ? "bg-brand-container text-on-brand-container"
        : "bg-surface-high text-ink";
  return (
    <div role={kind === "error" ? "alert" : "status"} className={cn("flex items-start gap-2 rounded-lg px-3 py-2.5 text-sm", tone, className)}>
      <Icon className="mt-0.5 size-4 shrink-0" aria-hidden />
      <div className="min-w-0 break-words">{children}</div>
    </div>
  );
}

export function Badge({ children, tone = "muted", className }: { children: ReactNode; tone?: "muted" | "ok" | "warn" | "bad" | "info"; className?: string }) {
  const t = {
    muted: "bg-surface-high text-on-variant",
    ok: "bg-brand-container text-on-brand-container",
    warn: "bg-amber-100 text-amber-900",
    bad: "bg-error-container text-on-error-container",
    info: "bg-surface-mid text-ink",
  }[tone];
  return <span className={cn("inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-code text-[11px] leading-4 font-medium", t, className)}>{children}</span>;
}

/** A small mono caption in capitals, the archive's way of labelling things. */
export function Eyebrow({ children, className }: { children: ReactNode; className?: string }) {
  return <span className={cn("font-code text-[11px] leading-4 font-medium tracking-wider text-on-variant uppercase", className)}>{children}</span>;
}

const control =
  "w-full min-w-0 rounded-lg border border-input bg-surface-lowest px-3 py-2 text-base outline-none md:text-sm focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/30 aria-invalid:border-destructive disabled:opacity-50";

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
    <div className="grid gap-1.5">
      <label htmlFor={id} className="text-sm font-semibold text-on-surface">
        {label}
      </label>
      {children({ id, ...(error ? { "aria-invalid": true as const } : {}), "aria-describedby": describedBy })}
      {error ? (
        <p id={describedBy} className="text-xs text-destructive">
          {error}
        </p>
      ) : hint ? (
        <p id={describedBy} className="text-xs text-on-variant">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

export function TextField({ label, hint, error, ...props }: { label: string; hint?: string; error?: string } & ComponentProps<"input">) {
  return <Field label={label} hint={hint} error={error}>{(a) => <Input {...a} className="h-10 rounded-lg bg-surface-lowest" {...props} />}</Field>;
}

/** One figure of a statistics row: a mono caption, the value in the display face and an optional note. */
export function Stat({ label, value, unit, note, icon, className }: { label: string; value: ReactNode; unit?: string; note?: ReactNode; icon?: ReactNode; className?: string }) {
  return (
    <div className={cn("rounded-xl bg-surface-lowest p-4 shadow-card", className)}>
      <div className="flex items-center justify-between gap-2">
        <Eyebrow>{label}</Eyebrow>
        {icon ? <span className="text-outline [&_svg]:size-[18px]">{icon}</span> : null}
      </div>
      <div className="mt-2 flex items-baseline gap-1.5">
        <span className="num font-headline text-3xl leading-none font-bold tracking-tight text-ink sm:text-4xl">{value}</span>
        {unit ? <span className="text-sm font-semibold text-on-variant">{unit}</span> : null}
      </div>
      {note ? <div className="mt-2 text-xs text-on-variant">{note}</div> : null}
    </div>
  );
}

export function PageTitle({ children, sub, eyebrow, aside }: { children: ReactNode; sub?: ReactNode; eyebrow?: ReactNode; aside?: ReactNode }) {
  return (
    <header className="mb-6 flex flex-wrap items-end justify-between gap-4">
      <div className="min-w-0">
        {eyebrow ? <div className="mb-1.5">{eyebrow}</div> : null}
        <h1 className="font-headline text-3xl font-bold tracking-tight text-ink sm:text-[2rem]">{children}</h1>
        {sub ? <p className="mt-1.5 text-sm text-on-variant">{sub}</p> : null}
      </div>
      {aside}
    </header>
  );
}

/** A white card section with the archive's heading: used for grouped content on the personal and public pages. */
export function Panel({ className, ...props }: ComponentProps<"section">) {
  return <section className={cn("rounded-xl bg-surface-lowest shadow-card", className)} {...props} />;
}

/** A line's own colour as a rounded badge with its number; light colours get dark text. */
export function LineBadge({ color, label, className }: { color: string; label: string; className?: string }) {
  return (
    <span
      className={cn("inline-flex min-w-9 items-center justify-center rounded-full px-2 py-0.5 font-code text-xs font-bold", className)}
      style={{ backgroundColor: color, color: readableOn(color) }}
    >
      {label}
    </span>
  );
}

/** Black or white, whichever reads better on `#RRGGBB`; white when the colour cannot be parsed. */
export function readableOn(hex: string): string {
  const m = /^#([0-9a-f]{6})$/i.exec(hex);
  if (!m) return "#ffffff";
  const n = parseInt(m[1], 16);
  const lin = (c: number) => {
    const v = c / 255;
    return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
  };
  const l = 0.2126 * lin((n >> 16) & 255) + 0.7152 * lin((n >> 8) & 255) + 0.0722 * lin(n & 255);
  return l > 0.4 ? "#121c2a" : "#ffffff";
}
