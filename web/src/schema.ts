import type { Issue, Json, Schema } from "./types";

export type FieldKind = "text" | "textarea" | "number" | "slider" | "select" | "checkbox" | "unsupported";

export interface FieldSpec {
  key: string;
  label: string;
  kind: FieldKind;
  required: boolean;
  options: Json[];
  min?: number;
  max?: number;
  maxLength?: number;
  minLength?: number;
  integer: boolean;
  defaultValue?: Json;
  widget?: string;
}

/** fieldsFor maps a section's settings schema to form fields, in the order the server gives (x-apexx-order), then any others. */
export function fieldsFor(schema: Schema | undefined): FieldSpec[] {
  if (!schema?.properties) return [];
  const required = new Set(schema.required ?? []);
  const props = schema.properties;
  const order = (schema["x-apexx-order"] ?? []).filter((k) => k in props);
  const keys = [...order, ...Object.keys(props).filter((k) => !order.includes(k))];
  return keys.map((key) => [key, props[key]!] as const).map(([key, p]) => {
    const base: FieldSpec = {
      key,
      label: p.title || key,
      kind: "unsupported",
      required: required.has(key),
      options: p.enum ?? [],
      min: p.minimum,
      max: p.maximum,
      maxLength: p.maxLength,
      minLength: p.minLength,
      integer: p.type === "integer",
      defaultValue: p.default,
      widget: p["x-apexx-widget"],
    };
    const widget = p["x-apexx-widget"];
    if (p.enum && p.enum.length > 0) base.kind = "select";
    else if (p.type === "boolean") base.kind = "checkbox";
    else if (p.type === "number" || p.type === "integer")
      base.kind = widget === "slider" && p.minimum !== undefined && p.maximum !== undefined ? "slider" : "number";
    else if (p.type === "string") base.kind = widget === "textarea" || (p.maxLength ?? 0) > 200 ? "textarea" : "text";
    return base;
  });
}

/** coerce turns a raw form value into the JSON value sent to the API. undefined means "remove the setting". */
export function coerce(field: FieldSpec, raw: string | boolean): Json | undefined {
  switch (field.kind) {
    case "checkbox":
      return Boolean(raw);
    case "number":
    case "slider": {
      if (raw === "" || raw === null) return undefined;
      const n = Number(raw);
      if (Number.isNaN(n)) return undefined;
      return field.integer ? Math.round(n) : n;
    }
    case "select": {
      const hit = field.options.find((o) => String(o) === String(raw));
      return hit === undefined ? undefined : hit;
    }
    default:
      return raw === "" && !field.required ? undefined : String(raw);
  }
}

/** localError gives instant feedback before the server report arrives; the server stays the authority. */
export function localError(field: FieldSpec, value: Json | undefined): string | null {
  if (value === undefined || value === "") return field.required ? "Este campo es obligatorio" : null;
  if (typeof value === "string") {
    const n = [...value].length;
    if (field.maxLength !== undefined && n > field.maxLength) return `Máximo ${field.maxLength} caracteres`;
    if (field.minLength !== undefined && n < field.minLength) return `Mínimo ${field.minLength} caracteres`;
  }
  if (typeof value === "number") {
    if (field.min !== undefined && value < field.min) return `Mínimo ${field.min}`;
    if (field.max !== undefined && value > field.max) return `Máximo ${field.max}`;
  }
  return null;
}

export interface SectionIssues {
  section: Issue[];
  fields: Record<string, Issue[]>;
}

/** groupIssues maps report issue paths (/regions/main/2/settings/alt) to sections and fields. */
export function groupIssues(issues: Issue[] | null | undefined, region = "main") {
  const bySection = new Map<number, SectionIssues>();
  const page: Issue[] = [];
  const re = new RegExp(`^/regions/${region}/(\\d+)(?:/settings/([^/]+))?`);
  for (const issue of issues ?? []) {
    const m = re.exec(issue.path);
    if (!m) {
      page.push(issue);
      continue;
    }
    const idx = Number(m[1]);
    const entry = bySection.get(idx) ?? { section: [], fields: {} };
    const key = m[2];
    if (key) (entry.fields[key] ??= []).push(issue);
    else entry.section.push(issue);
    bySection.set(idx, entry);
  }
  return { bySection, page };
}
