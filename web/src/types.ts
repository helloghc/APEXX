export type Json = null | boolean | number | string | Json[] | { [k: string]: Json };

export interface Schema {
  type?: string;
  title?: string;
  required?: string[];
  properties?: Record<string, Schema>;
  additionalProperties?: boolean;
  enum?: Json[];
  minLength?: number;
  maxLength?: number;
  minimum?: number;
  maximum?: number;
  default?: Json;
  "x-apexx-widget"?: string;
  /** Order of properties in the editor; JSON objects carry none of their own. */
  "x-apexx-order"?: string[];
}

export interface Budget {
  htmlBytes: number;
  jsBytes: number;
  cssBytes: number;
  requests: number;
  aboveFoldImages: number;
}

export interface SectionDefinition {
  type: string;
  version: string;
  name: string;
  category: string;
  description: string;
  hydration: string;
  enabledOn?: string[] | null;
  blocks: { allowed: string[] | null; min: number; max: number };
  budget: Budget;
  settingsSchema: Schema;
}

export interface Block {
  id: string;
  type: string;
  settings?: Record<string, Json>;
}

export interface Audience { mode: "only" | "hide"; groups: string[] }

export interface SectionInstance {
  id: string;
  type?: string;
  typeVersion?: string;
  visibility?: { devices?: string[]; schedule?: { from: string; to: string }; audience?: Audience };
  settings?: Record<string, Json>;
  blocks?: Block[];
  globalRef?: string;
  locked?: boolean;
}

export interface Template {
  id: string;
  kind: string;
  handle: string;
  name: string;
  seo: { title: string; description: string };
  regions: Record<string, SectionInstance[] | null>;
  theme?: ThemeSettings;
}

/** Site theme stored in the home template: a base theme plus overrides. */
export interface ThemeSettings {
  id: string;
  colors?: Record<string, string>;
  display?: string;
  body?: string;
  radius?: number;
}

export interface StoreTheme {
  id: string;
  name: string;
  vertical: string;
  description: string;
  colors: Record<string, string>;
  display: string;
  body: string;
  accentFont?: string;
  radius: number;
  buttonStyle: string;
  cardStyle: string;
  headerStyle: string;
  density: string;
  imageRatio: string;
  colorScheme: "light" | "dark";
}

export interface StoreFont {
  id: string;
  family: string;
  file: string;
  weights: string;
  fallback: string;
  kind: string;
}

export interface ThemesResponse {
  items: StoreTheme[];
  fonts: StoreFont[];
  tokens: string[];
  contrastPairs: [string, string][];
}

export interface Issue {
  path: string;
  code: string;
  message: string;
  blocking: boolean;
}

export interface Report {
  handle: string;
  budget: Budget;
  limit: Budget;
  issues: Issue[] | null;
  htmlBytes: number;
}

export interface Release {
  revisionId: string;
  publishedAt: string;
  publishedBy: string;
  durationMicros: number;
  rolledBackFrom?: string;
}

export interface Op {
  op: "add" | "remove" | "replace" | "move" | "copy" | "test";
  path: string;
  from?: string;
  value?: Json;
}

// Problem documents (RFC 9457) are validated at the boundary. A small check
// written by hand: a schema library for this one shape weighed 150 KB in the
// code every screen downloads first.
export interface Problem {
  type?: string;
  title?: string;
  status: number;
  code: string;
  detail?: string;
  errors?: { field: string; code: string; message: string }[];
  reports?: Record<string, unknown>;
}

const isStr = (v: unknown): v is string => typeof v === "string";
const optStr = (v: unknown) => v === undefined || isStr(v);

function parseProblem(v: unknown): Problem | null {
  if (typeof v !== "object" || v === null || Array.isArray(v)) return null;
  const o = v as Record<string, unknown>;
  if (typeof o.status !== "number" || !isStr(o.code) || !optStr(o.type) || !optStr(o.title) || !optStr(o.detail)) return null;
  const out: Problem = { status: o.status, code: o.code };
  if (o.type !== undefined) out.type = o.type as string;
  if (o.title !== undefined) out.title = o.title as string;
  if (o.detail !== undefined) out.detail = o.detail as string;
  if (o.errors !== undefined) {
    if (!Array.isArray(o.errors)) return null;
    const errs: { field: string; code: string; message: string }[] = [];
    for (const e of o.errors as unknown[]) {
      const x = e as Record<string, unknown> | null;
      if (typeof x !== "object" || x === null || !isStr(x.field) || !isStr(x.code) || !isStr(x.message)) return null;
      errs.push({ field: x.field, code: x.code, message: x.message });
    }
    out.errors = errs;
  }
  if (o.reports !== undefined) {
    if (typeof o.reports !== "object" || o.reports === null || Array.isArray(o.reports)) return null;
    out.reports = o.reports as Record<string, unknown>;
  }
  return out;
}

export const ProblemSchema = {
  safeParse(v: unknown): { success: true; data: Problem } | { success: false } {
    const data = parseProblem(v);
    return data ? { success: true, data } : { success: false };
  },
};
