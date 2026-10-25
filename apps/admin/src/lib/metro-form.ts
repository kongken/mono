// The form model of the catalogue editors. Pure (no generated code), so node:test can cover it.

export type FieldKind = "text" | "number" | "decimal" | "textarea" | "select" | "checkbox";

export type FieldDef = {
  key: string;
  label: string;
  kind: FieldKind;
  hint?: string;
  options?: { value: string; label: string }[];
  /** Fixed after creation (a line's code). */
  lockedOnEdit?: boolean;
  /** What the server calls the field when it differs from the key in snake_case (a line's "citySlug" is "city"). */
  serverKey?: string;
};

export type Form = Record<string, string | boolean>;

/** What a field holds, as the person reads it. */
export function display(def: FieldDef, value: string | boolean | undefined): string {
  if (def.kind === "checkbox") return value === true ? "是" : "否";
  const v = String(value ?? "");
  if (def.kind === "select") return def.options?.find((o) => o.value === v)?.label ?? v;
  return v === "" ? "（空）" : v;
}

export type FieldDiff = { key: string; label: string; latest: string; mine: string };

/** The fields on which what the person typed differs from the record as it is now, in field order. */
export function diffForms(fields: readonly FieldDef[], latest: Form, mine: Form): FieldDiff[] {
  const out: FieldDiff[] = [];
  for (const f of fields) {
    const a = display(f, latest[f.key]);
    const b = display(f, mine[f.key]);
    if (a !== b) out.push({ key: f.key, label: f.label, latest: a, mine: b });
  }
  return out;
}

/** True for a plain record of strings and booleans: the only thing a restored draft may be turned into. */
export function isForm(x: unknown): x is Form {
  return typeof x === "object" && x !== null && !Array.isArray(x) && Object.values(x).every((v) => typeof v === "string" || typeof v === "boolean");
}

const snake = (k: string) => k.replace(/[A-Z]/g, (c) => `_${c.toLowerCase()}`);
const leaf = (field: string) => field.replace(/\[\d+\]/g, "").split(".").pop() ?? field;

/**
 * The reason the server gave for the field `key` of the form, if it named it. The server speaks in
 * snake_case paths ("city_slug", "lines[3].color"); the form keys are camelCase.
 */
export function problemFor(problems: readonly { field: string; reason: string }[], key: string, serverKey?: string): string | undefined {
  return problems.find((p) => leaf(p.field) === key || leaf(p.field) === snake(key) || (serverKey !== undefined && leaf(p.field) === serverKey))?.reason;
}
