import { formatMoney } from "@/money";
import type { Grain, RangeInfo } from "./api";

const MONTHS = ["ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"];
const WEEKDAYS = ["lunes", "martes", "miércoles", "jueves", "viernes", "sábado", "domingo"];

export const weekdayName = (slot: number) => WEEKDAYS[slot] ?? "";

/** num renders an integer with thousands separators: 12345 → "12,345". */
export function num(n: number): string {
  const neg = n < 0;
  return (neg ? "-" : "") + String(Math.abs(Math.round(n))).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

/** money renders cents in full: 129950 → "$1,299.50". The currency goes in the column or card title. */
export function money(cents: number): string {
  return formatMoney(cents, "").trim();
}

/** compactMoney shortens big amounts for tiles and axes: 123456789 → "$1.23 M". */
export function compactMoney(cents: number): string {
  const neg = cents < 0;
  const v = Math.abs(cents) / 100;
  let s: string;
  if (v >= 1_000_000) s = trim(v / 1_000_000, v >= 10_000_000 ? 1 : 2) + " M";
  else if (v >= 10_000) s = trim(v / 1000, v >= 100_000 ? 0 : 1) + " k";
  else s = num(Math.round(v));
  return (neg ? "-$" : "$") + s;
}

function trim(v: number, digits: number): string {
  const s = v.toFixed(digits);
  return s.includes(".") ? s.replace(/\.?0+$/, "") : s;
}

/** compact shortens counts: 15300 → "15.3 k". */
export function compact(n: number): string {
  const a = Math.abs(n);
  if (a >= 1_000_000) return trim(n / 1_000_000, 1) + " M";
  if (a >= 10_000) return trim(n / 1000, 1) + " k";
  return num(n);
}

/** ratio renders part/whole as a percentage with one decimal; "—" when there is no base. */
export function ratio(part: number, whole: number, digits = 1): string {
  if (!whole) return "—";
  return ((part * 100) / whole).toFixed(digits) + "%";
}

export interface Delta {
  /** Percentage change, or null when the previous period was zero. */
  pct: number | null;
  dir: "up" | "down" | "flat";
  label: string;
}

/** delta compares a value with the previous period. */
export function delta(current: number, previous: number): Delta {
  if (!previous) {
    if (!current) return { pct: 0, dir: "flat", label: "Sin cambio" };
    return { pct: null, dir: current > 0 ? "up" : "down", label: "Nuevo" };
  }
  const pct = ((current - previous) / Math.abs(previous)) * 100;
  const dir = Math.abs(pct) < 0.05 ? "flat" : pct > 0 ? "up" : "down";
  const abs = Math.abs(pct);
  const txt = abs >= 1000 ? num(abs) : abs >= 100 ? abs.toFixed(0) : abs.toFixed(1);
  return { pct, dir, label: dir === "flat" ? "Sin cambio" : `${pct > 0 ? "+" : "−"}${txt}%` };
}

function parts(day: string): { y: number; m: number; d: number } {
  const [y = 0, m = 1, d = 1] = day.split("-").map(Number);
  return { y, m, d };
}

/** dayLabel: "2026-11-10" → "10 nov" (or "10 nov 2026"). */
export function dayLabel(day: string, withYear = false): string {
  const { y, m, d } = parts(day);
  return `${d} ${MONTHS[m - 1] ?? ""}${withYear ? " " + y : ""}`;
}

/** bucketLabel names a series bucket for axes and tooltips. */
export function bucketLabel(bucket: string, grain: Grain, long = false): string {
  switch (grain) {
    case "hour": {
      const [day = "", h = "0"] = bucket.split("T");
      const hour = `${Number(h)}:00`;
      return long ? `${dayLabel(day)}, ${hour}` : hour;
    }
    case "week":
      return (long ? "Semana del " : "") + dayLabel(bucket);
    case "month": {
      const { y, m } = parts(bucket + "-01");
      return `${MONTHS[m - 1] ?? ""} ${y}`;
    }
    default:
      return dayLabel(bucket, long);
  }
}

/** rangeLabel: "4 – 10 nov 2026", "28 oct – 3 nov 2026", "10 nov 2026". */
export function rangeLabel(r: Pick<RangeInfo, "from" | "to">): string {
  const a = parts(r.from);
  const b = parts(r.to);
  if (r.from === r.to) return dayLabel(r.to, true);
  if (a.y === b.y && a.m === b.m) return `${a.d} – ${dayLabel(r.to, true)}`;
  if (a.y === b.y) return `${dayLabel(r.from)} – ${dayLabel(r.to, true)}`;
  return `${dayLabel(r.from, true)} – ${dayLabel(r.to, true)}`;
}

/** niceTicks returns round axis values from 0 up to at least max. */
export function niceTicks(max: number, count = 4): number[] {
  if (!(max > 0)) return [0, 1];
  const rough = max / count;
  const pow = Math.pow(10, Math.floor(Math.log10(rough)));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * pow).find((s) => s >= rough) ?? rough;
  const out: number[] = [];
  for (let v = 0; v < max + step - 1e-9; v += step) out.push(Math.round(v * 1e6) / 1e6);
  return out;
}

/** ago renders an elapsed time in plain words. */
export function ago(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return "nunca";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t) || t < 86_400_000) return "nunca";
  return duration(Math.max(0, (now - t) / 1000));
}

export function duration(sec: number): string {
  if (sec < 5) return "hace un momento";
  if (sec < 90) return `hace ${Math.round(sec)} s`;
  if (sec < 5400) return `hace ${Math.round(sec / 60)} min`;
  if (sec < 129_600) return `hace ${Math.round(sec / 3600)} h`;
  return `hace ${Math.round(sec / 86_400)} días`;
}
