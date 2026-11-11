/** Pure helpers of the Configuración area (unit-tested in settings.test.ts). */
import type { Bracket, MenuItem, PostalRange, Rate } from "./api";

/** parseRanges reads "01000-16999, 53000" (one per line or comma) into ranges; a single code is a range of one. */
export function parseRanges(text: string): { ranges: PostalRange[]; error: string | null } {
  const ranges: PostalRange[] = [];
  for (const raw of text.split(/[\n,;]+/)) {
    const part = raw.trim();
    if (!part) continue;
    const m = /^(\d{5})(?:\s*[-–—a]\s*(\d{5}))?$/.exec(part);
    if (!m) return { ranges: [], error: `“${part}” no es un código postal ni un rango (ejemplo: 01000-16999).` };
    const from = m[1] as string;
    const to = m[2] ?? from;
    if (from > to) return { ranges: [], error: `En “${part}” el inicio es mayor que el final.` };
    ranges.push({ from, to });
  }
  return { ranges, error: null };
}

export function formatRanges(ranges: PostalRange[]): string {
  return ranges.map((r) => (r.from === r.to ? r.from : `${r.from}-${r.to}`)).join("\n");
}

/** percentToBps converts "16", "8.5" or "16%" into basis points; null if invalid. */
export function percentToBps(input: string): number | null {
  const s = input.trim().replace(/%$/, "").replace(",", ".");
  if (!/^\d{1,2}(\.\d{1,2})?$/.test(s)) return null;
  return Math.round(Number(s) * 100);
}

export function bpsToPercent(bps: number): string {
  return String(bps / 100);
}

/** kgToGrams converts "1.5" into 1500; null if invalid. */
export function kgToGrams(input: string): number | null {
  const s = input.trim().replace(",", ".");
  if (!/^\d+(\.\d{1,3})?$/.test(s)) return null;
  return Math.round(Number(s) * 1000);
}

export function gramsToKg(g: number): string {
  return String(g / 1000);
}

/** validateBrackets mirrors the server: ascending limits, only the last one open-ended. */
export function validateBrackets(brackets: Bracket[]): string | null {
  let prev = 0;
  for (let i = 0; i < brackets.length; i++) {
    const b = brackets[i] as Bracket;
    const last = i === brackets.length - 1;
    if (b.amount < 0 || b.upTo < 0) return "Los tramos no pueden tener valores negativos.";
    if (b.upTo === 0 && !last) return "Solo el último tramo puede ser “sin límite”.";
    if (b.upTo !== 0 && b.upTo <= prev) return "Cada tramo debe terminar después del anterior.";
    if (b.upTo !== 0) prev = b.upTo;
  }
  return null;
}

function money(cents: number): string {
  const whole = String(Math.floor(cents / 100)).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  const frac = cents % 100;
  return `$${whole}${frac ? "." + String(frac).padStart(2, "0") : ""}`;
}

/** describeRate summarizes a rate in one line for tables. */
export function describeRate(r: Rate): string {
  let s: string;
  if (r.type === "flat") s = r.amount === 0 ? "Gratis" : `${money(r.amount)} fijo`;
  else if (r.type === "weight") {
    s = r.brackets.length
      ? `Por peso: ${r.brackets.map((b) => (b.upTo ? `≤${gramsToKg(b.upTo)} kg ${money(b.amount)}` : `más ${money(b.amount)}`)).join(" · ")}${r.perKg ? ` · +${money(r.perKg)}/kg extra` : ""}`
      : `${money(r.amount)} + ${money(r.perKg)} por kg`;
  } else {
    s = `Por monto: ${r.brackets.map((b) => (b.upTo ? `≤${money(b.upTo)} → ${money(b.amount)}` : `más → ${money(b.amount)}`)).join(" · ")}`;
  }
  if (r.freeOver > 0 && !(r.type === "flat" && r.amount === 0)) s += ` · gratis desde ${money(r.freeOver)}`;
  return s;
}

// ---------- menu tree ----------

export type Path = number[];

function clone(items: MenuItem[]): MenuItem[] {
  return items.map((i) => ({ ...i, children: clone(i.children ?? []) }));
}

function listAt(items: MenuItem[], parent: Path): MenuItem[] {
  let list = items;
  for (const i of parent) list = (list[i] as MenuItem).children;
  return list;
}

export function depthOf(item: MenuItem): number {
  return 1 + Math.max(0, ...(item.children ?? []).map(depthOf));
}

/** moveItem moves the item at path one place up (-1) or down (+1) among its siblings. */
export function moveItem(items: MenuItem[], path: Path, dir: -1 | 1): MenuItem[] {
  const out = clone(items);
  const list = listAt(out, path.slice(0, -1));
  const i = path[path.length - 1] as number;
  const j = i + dir;
  if (j < 0 || j >= list.length) return items;
  [list[i], list[j]] = [list[j] as MenuItem, list[i] as MenuItem];
  return out;
}

/** indentItem makes the item a child of its previous sibling, if depth allows. */
export function indentItem(items: MenuItem[], path: Path, maxDepth: number): MenuItem[] {
  const i = path[path.length - 1] as number;
  if (i === 0) return items;
  const out = clone(items);
  const list = listAt(out, path.slice(0, -1));
  const item = list[i] as MenuItem;
  if (path.length + depthOf(item) > maxDepth) return items;
  list.splice(i, 1);
  (list[i - 1] as MenuItem).children.push(item);
  return out;
}

/** outdentItem moves the item out of its parent, right after it. */
export function outdentItem(items: MenuItem[], path: Path): MenuItem[] {
  if (path.length < 2) return items;
  const out = clone(items);
  const parentPath = path.slice(0, -1);
  const list = listAt(out, parentPath);
  const [item] = list.splice(path[path.length - 1] as number, 1);
  const grand = listAt(out, parentPath.slice(0, -1));
  grand.splice((parentPath[parentPath.length - 1] as number) + 1, 0, item as MenuItem);
  return out;
}

export function removeItem(items: MenuItem[], path: Path): MenuItem[] {
  const out = clone(items);
  listAt(out, path.slice(0, -1)).splice(path[path.length - 1] as number, 1);
  return out;
}

export function updateItem(items: MenuItem[], path: Path, patch: Partial<MenuItem>): MenuItem[] {
  const out = clone(items);
  const list = listAt(out, path.slice(0, -1));
  const i = path[path.length - 1] as number;
  list[i] = { ...(list[i] as MenuItem), ...patch };
  return out;
}

export function countItems(items: MenuItem[]): number {
  return items.reduce((n, i) => n + 1 + countItems(i.children ?? []), 0);
}

// ---------- redirects ----------

/** normalizePath mirrors the server: leading slash, no trailing slash, no query. */
export function normalizePath(p: string): string {
  let s = p.trim().replace(/[?#].*$/, "");
  if (!s.startsWith("/")) s = "/" + s;
  if (s.length > 1) s = s.replace(/\/+$/, "");
  return s || "/";
}

export function validateRedirect(from: string, target: string): Record<string, string> {
  const e: Record<string, string> = {};
  const f = from.trim();
  if (!f || f === "/") e.from = "Escribe la ruta anterior, por ejemplo /tenis-rojos.";
  else if (f.includes("://")) e.from = "Escribe solo la ruta dentro de tu tienda, sin el dominio.";
  else if (/\s/.test(f)) e.from = "La ruta no puede tener espacios.";
  const t = target.trim();
  if (!t) e.target = "Escribe a dónde debe llevar.";
  else if (!t.startsWith("/") && !/^https?:\/\/[^\s/]+/.test(t)) e.target = "Usa una ruta que empiece con / o una URL https completa.";
  else if (/\s/.test(t)) e.target = "El destino no puede tener espacios.";
  else if (!e.from && t.startsWith("/") && normalizePath(t) === normalizePath(f)) e.target = "El destino es la misma ruta de origen.";
  return e;
}

/** parseRedirectLines reads pasted "origen,destino" or tab-separated lines (a header row is skipped). */
export function parseRedirectLines(text: string): { items: { from: string; target: string; code: number }[]; errors: string[] } {
  const items: { from: string; target: string; code: number }[] = [];
  const errors: string[] = [];
  text.split(/\r?\n/).forEach((raw, i) => {
    const line = raw.trim();
    if (!line) return;
    const cols = line.split(/\s*[,;\t]\s*/);
    if (i === 0 && /^(origen|from|desde)$/i.test(cols[0] ?? "")) return;
    if (cols.length < 2 || !cols[0] || !cols[1]) {
      errors.push(`Línea ${i + 1}: usa el formato /ruta-anterior,/ruta-nueva`);
      return;
    }
    const v = validateRedirect(cols[0], cols[1]);
    const first = v.from ?? v.target;
    if (first) {
      errors.push(`Línea ${i + 1}: ${first}`);
      return;
    }
    items.push({ from: normalizePath(cols[0]), target: cols[1], code: cols[2] === "302" ? 302 : 301 });
  });
  return { items, errors };
}

// ---------- store ----------

export function validateRfc(rfc: string): string | null {
  const s = rfc.trim().toUpperCase();
  if (!s) return null;
  return /^[A-ZÑ&]{3,4}\d{6}[A-Z0-9]{3}$/.test(s) ? null : "El RFC tiene 12 caracteres (empresa) o 13 (persona física).";
}

export function validateHostname(h: string): string | null {
  const s = h
    .trim()
    .toLowerCase()
    .replace(/^https?:\/\//, "")
    .replace(/\/.*$/, "");
  if (!s) return "Escribe tu dominio, por ejemplo www.mitienda.mx.";
  if (!/^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,24}$/.test(s)) return "Ese dominio no es válido. Ejemplo: www.mitienda.mx.";
  return null;
}

export const WEEKDAYS: { id: string; label: string }[] = [
  { id: "mon", label: "Lunes" },
  { id: "tue", label: "Martes" },
  { id: "wed", label: "Miércoles" },
  { id: "thu", label: "Jueves" },
  { id: "fri", label: "Viernes" },
  { id: "sat", label: "Sábado" },
  { id: "sun", label: "Domingo" },
];
