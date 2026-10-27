import { cn } from "@kongken/ui/utils";

/** Independent filter buttons, with native keyboard activation and explicit selected states. */
export function FilterChips<T extends string>({ label, value, options, onChange }: {
  label: string;
  value: T;
  options: readonly { value: T; label: string }[];
  onChange: (value: T) => void;
}) {
  return (
    <div role="group" aria-label={label} className="flex flex-wrap gap-2">
      {options.map((option) => (
        <button key={option.value} type="button" aria-pressed={option.value === value} onClick={() => onChange(option.value)} className={cn(
          "min-h-11 rounded-full px-4 py-2 text-sm font-semibold transition-colors",
          option.value === value ? "bg-deep text-white" : "bg-surface-high text-on-variant hover:bg-surface-highest",
        )}>{option.label}</button>
      ))}
    </div>
  );
}
