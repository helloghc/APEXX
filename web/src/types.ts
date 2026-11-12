import { z } from "zod";

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

// Problem documents (RFC 9457) are validated at the boundary.
export const ProblemSchema = z.object({
  type: z.string().optional(),
  title: z.string().optional(),
  status: z.number(),
  code: z.string(),
  detail: z.string().optional(),
  errors: z.array(z.object({ field: z.string(), code: z.string(), message: z.string() })).optional(),
  reports: z.record(z.unknown()).optional(),
});
export type Problem = z.infer<typeof ProblemSchema>;
