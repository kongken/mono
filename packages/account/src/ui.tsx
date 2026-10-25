import { useCallback, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { CheckCircle, Eye, EyeSlash, Info, WarningCircle } from "@phosphor-icons/react";
import { Input } from "@kongken/ui/input";
import { cn } from "@kongken/ui/utils";

/** A labelled control; the label, hint and error are tied to it for assistive technology. */
export function Field({
  label,
  hint,
  error,
  children,
}: {
  label: string;
  hint?: string;
  error?: string | null;
  children: (props: { id: string; "aria-invalid"?: true; "aria-describedby"?: string }) => ReactNode;
}) {
  const id = useId();
  const describedBy = error || hint ? `${id}-d` : undefined;
  return (
    <div className="grid gap-1.5">
      <label htmlFor={id} className="text-sm font-medium text-foreground">
        {label}
      </label>
      {children({ id, ...(error ? { "aria-invalid": true as const } : {}), "aria-describedby": describedBy })}
      {error ? (
        <p id={describedBy} className="text-xs leading-relaxed text-destructive">
          {error}
        </p>
      ) : hint ? (
        <p id={describedBy} className="text-xs leading-relaxed text-muted-foreground">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

export function TextField({
  label,
  hint,
  error,
  ...input
}: { label: string; hint?: string; error?: string | null } & Omit<React.ComponentProps<"input">, "id">) {
  return <Field label={label} hint={hint} error={error}>{(a) => <Input {...a} {...input} />}</Field>;
}

/** A password field with a show/hide switch. Never trims, never stores. */
export function PasswordField({
  label,
  hint,
  error,
  autoComplete,
  ...input
}: {
  label: string;
  hint?: string;
  error?: string | null;
  autoComplete: "current-password" | "new-password";
} & Omit<React.ComponentProps<"input">, "id" | "type" | "autoComplete">) {
  const [shown, setShown] = useState(false);
  return (
    <Field label={label} hint={hint} error={error}>
      {(a) => (
        <div className="relative">
          <Input
            {...a}
            {...input}
            type={shown ? "text" : "password"}
            autoComplete={autoComplete}
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
            className="pr-11"
          />
          <button
            type="button"
            aria-label={shown ? "隐藏密码" : "显示密码"}
            aria-pressed={shown}
            onClick={() => setShown((v) => !v)}
            className="absolute inset-y-0 right-0 flex w-10 items-center justify-center rounded-r-md text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50"
          >
            {shown ? <EyeSlash size={18} aria-hidden /> : <Eye size={18} aria-hidden />}
          </button>
        </div>
      )}
    </Field>
  );
}

export function Notice({
  kind = "info",
  children,
  className,
}: {
  kind?: "error" | "info" | "success";
  children: ReactNode;
  className?: string;
}) {
  const Icon = kind === "success" ? CheckCircle : kind === "error" ? WarningCircle : Info;
  const tone =
    kind === "error"
      ? "border-destructive/40 bg-destructive/5 text-destructive"
      : kind === "success"
        ? "border-primary/30 bg-secondary text-secondary-foreground"
        : "border-border bg-muted text-foreground";
  return (
    <div
      role={kind === "error" ? "alert" : "status"}
      className={cn("flex items-start gap-2 rounded-md border px-3 py-2 text-sm leading-relaxed", tone, className)}
    >
      <Icon className="mt-0.5 size-4 shrink-0" aria-hidden />
      <div className="min-w-0 break-words">{children}</div>
    </div>
  );
}

/** A text button that reads as a link, for switching between forms. */
export function LinkButton({ className, ...props }: React.ComponentProps<"button">) {
  return (
    <button
      type="button"
      className={cn(
        "rounded-sm text-sm text-primary underline-offset-4 outline-none hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50",
        className,
      )}
      {...props}
    />
  );
}

/** Counts down from `start(n)`; used for the resend cooldown of mail requests. */
export function useCooldown() {
  const [until, setUntil] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (until <= Date.now()) return;
    setNow(Date.now());
    const id = window.setInterval(() => {
      const t = Date.now();
      setNow(t);
      if (t >= until) window.clearInterval(id);
    }, 500);
    return () => window.clearInterval(id);
  }, [until]);
  const start = useCallback((seconds: number) => setUntil(Date.now() + seconds * 1000), []);
  return { seconds: Math.max(0, Math.ceil((until - now) / 1000)), start };
}

/** Runs one submit at a time; a second click while it runs is ignored. */
export function useRunner() {
  const [pending, setPending] = useState(false);
  const busy = useRef(false);
  const run = useCallback(async <T,>(action: () => Promise<T>): Promise<T | undefined> => {
    if (busy.current) return undefined;
    busy.current = true;
    setPending(true);
    try {
      return await action();
    } finally {
      busy.current = false;
      setPending(false);
    }
  }, []);
  return { pending, run };
}

/** Moves focus to the first field marked invalid whenever `tick` changes. */
export function useFocusFirstInvalid(form: React.RefObject<HTMLFormElement | null>, tick: number) {
  useEffect(() => {
    if (tick === 0) return;
    form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus();
  }, [form, tick]);
}

export const FORM_CLASS = "grid gap-4";
