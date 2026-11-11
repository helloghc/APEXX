import type { Edit } from "./patch";
import type { StoreTheme, Template, ThemeSettings } from "./types";

/** WCAG 2.x contrast ratio between two #RRGGBB colors (same formula as the server). */
export function contrast(a: string, b: string): number {
  const lum = (hex: string) => {
    const v = parseInt(hex.slice(1), 16);
    const ch = (c: number) => {
      const x = c / 255;
      return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4);
    };
    return 0.2126 * ch((v >> 16) & 255) + 0.7152 * ch((v >> 8) & 255) + 0.0722 * ch(v & 255);
  };
  const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p) as [number, number];
  return (x + 0.05) / (y + 0.05);
}

export const isHex = (s: string) => /^#[0-9a-fA-F]{6}$/.test(s);

/** Effective colors: theme defaults plus valid overrides. */
export function resolvedColors(base: StoreTheme | undefined, ts: ThemeSettings | undefined): Record<string, string> {
  const out = { ...(base?.colors ?? {}) };
  for (const [k, v] of Object.entries(ts?.colors ?? {})) if (k in out && isHex(v)) out[k] = v.toUpperCase();
  return out;
}

export interface ContrastProblem {
  fg: string;
  bg: string;
  ratio: number;
}

export function contrastProblems(colors: Record<string, string>, pairs: [string, string][]): ContrastProblem[] {
  const out: ContrastProblem[] = [];
  for (const [fg, bg] of pairs) {
    const a = colors[fg], b = colors[bg];
    if (!a || !b || !isHex(a) || !isHex(b)) continue;
    const ratio = contrast(a, b);
    if (ratio < 4.5) out.push({ fg, bg, ratio });
  }
  return out;
}

export const TOKEN_LABELS: Record<string, string> = {
  bg: "Fondo",
  surface: "Tarjetas",
  text: "Texto",
  muted: "Texto secundario",
  primary: "Botón",
  primaryText: "Texto del botón",
  accent: "Acento",
  accentText: "Texto sobre el acento",
  border: "Bordes",
  sale: "Precio de oferta",
};

/** setThemeValue builds an undoable edit for one field of template.theme (path like "colors/text"). */
export function setThemeValue(t: Template, baseId: string, path: string, value: string | number | undefined): Edit | null {
  const ts: ThemeSettings = t.theme ? JSON.parse(JSON.stringify(t.theme)) : { id: baseId };
  const before = JSON.stringify(t.theme ?? null);
  const parts = path.split("/");
  if (parts[0] === "colors") {
    ts.colors = { ...(ts.colors ?? {}) };
    if (value === undefined || value === "") delete ts.colors[parts[1]!];
    else ts.colors[parts[1]!] = String(value);
    if (Object.keys(ts.colors).length === 0) delete ts.colors;
  } else if (parts[0] === "display" || parts[0] === "body") {
    if (value === undefined || value === "") delete ts[parts[0]];
    else ts[parts[0]] = String(value);
  } else if (parts[0] === "radius") {
    if (value === undefined) delete ts.radius;
    else ts.radius = Number(value);
  } else return null;
  if (JSON.stringify(ts) === before) return null;
  const ops = t.theme ? [{ op: "replace" as const, path: "/theme", value: ts }] : [{ op: "add" as const, path: "/theme", value: ts }];
  const inverse = t.theme ? [{ op: "replace" as const, path: "/theme", value: t.theme }] : [{ op: "remove" as const, path: "/theme" }];
  return { label: "Cambiar estilo", ops: ops as unknown as Edit["ops"], inverse: inverse as unknown as Edit["inverse"] };
}
