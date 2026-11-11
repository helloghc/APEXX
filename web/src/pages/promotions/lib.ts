import { centsToPesos, parsePesos } from "@/money";
import type { Promotion, PromoType } from "@/promoApi";

/** Every schedule is read and written in the store timezone, whatever the browser's. */
export const STORE_TZ = "America/Mexico_City";

export const TYPE_LABEL: Record<PromoType, string> = {
  order_percent: "% en el pedido",
  order_fixed: "Monto en el pedido",
  item_percent: "% en productos",
  item_fixed: "Monto por pieza",
  free_shipping: "Envío gratis",
  buy_x_get_y: "Compra X, lleva Y",
  tiered: "Escalonada",
  gift: "Regalo",
  bundle: "Paquete",
};

export const TYPE_HELP: Record<PromoType, string> = {
  order_percent: "Un porcentaje menos sobre todo el pedido.",
  order_fixed: "Un monto fijo menos sobre todo el pedido.",
  item_percent: "Un porcentaje menos solo en los productos que elijas.",
  item_fixed: "Un monto menos por cada pieza de los productos que elijas.",
  free_shipping: "El envío no cuesta cuando se cumplen las condiciones.",
  buy_x_get_y: "3x2, 2x1 o “compra tenis y lleva calcetas a mitad de precio”.",
  tiered: "Más descuento mientras más se compra, por monto o por piezas.",
  gift: "Un producto sin costo al cumplir las condiciones.",
  bundle: "Un precio especial al llevar un conjunto de productos.",
};

export const STATUS_LABEL: Record<string, string> = {
  activa: "Activa",
  programada: "Programada",
  vencida: "Vencida",
  pausada: "Pausada",
  borrador: "Borrador",
  archivada: "Archivada",
  agotada: "Agotada",
  activo: "Activo",
  desactivado: "Desactivado",
  vencido: "Vencido",
  programado: "Programado",
  agotado: "Agotado",
};

export function statusTone(status: string): "ok" | "warn" | "bad" | "secondary" | "default" {
  switch (status) {
    case "activa":
    case "activo":
      return "ok";
    case "programada":
    case "programado":
      return "default";
    case "pausada":
    case "borrador":
      return "warn";
    case "vencida":
    case "vencido":
    case "agotada":
    case "agotado":
    case "desactivado":
      return "bad";
    default:
      return "secondary";
  }
}

/** tzOffsetMinutes returns how many minutes `zone` is ahead of UTC at instant `utcMs`. */
function tzOffsetMinutes(utcMs: number, zone: string): number {
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone: zone,
    hourCycle: "h23",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).formatToParts(new Date(utcMs));
  const n = (t: string) => Number(parts.find((p) => p.type === t)?.value ?? 0);
  const asUtc = Date.UTC(n("year"), n("month") - 1, n("day"), n("hour"), n("minute"), n("second"));
  return Math.round((asUtc - utcMs) / 60000);
}

/** localToIso turns "2026-11-13T00:00" (wall clock in the store timezone) into an ISO instant, or undefined when empty/invalid. */
export function localToIso(local: string, zone = STORE_TZ): string | undefined {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(local.trim());
  if (!m) return undefined;
  const wall = Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4]), Number(m[5]));
  // Two passes settle the offset even next to a daylight-saving change.
  let utc = wall - tzOffsetMinutes(wall, zone) * 60000;
  utc = wall - tzOffsetMinutes(utc, zone) * 60000;
  return new Date(utc).toISOString().replace(".000Z", "Z");
}

/** isoToLocal turns an ISO instant into the "YYYY-MM-DDTHH:MM" wall clock of the store timezone. */
export function isoToLocal(iso: string | undefined | null, zone = STORE_TZ): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const d = new Date(t + tzOffsetMinutes(t, zone) * 60000);
  return d.toISOString().slice(0, 16);
}

/** storeDateTime shows an instant in the store timezone: "13 nov 2026, 00:00". */
export function storeDateTime(iso: string | undefined | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString("es-MX", { timeZone: STORE_TZ, day: "numeric", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
}

export function storeDate(iso: string | undefined | null): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleDateString("es-MX", { timeZone: STORE_TZ, day: "numeric", month: "short", year: "numeric" });
}

export function windowText(from?: string | null, to?: string | null): string {
  if (from && to) return `${storeDate(from)} – ${storeDate(to)}`;
  if (from) return `Desde ${storeDate(from)}`;
  if (to) return `Hasta ${storeDate(to)}`;
  return "Sin fecha de fin";
}

/** bpsToPercent renders 1250 → "12.5". */
export function bpsToPercent(bps: number | undefined): string {
  if (!bps) return "";
  return String(bps / 100);
}

/** parsePercent reads "12.5" or "12,5%" into basis points, or null. */
export function parsePercent(input: string): number | null {
  const s = input.trim().replace("%", "").replace(",", ".");
  if (!/^\d+(\.\d{1,2})?$/.test(s)) return null;
  return Math.round(Number(s) * 100);
}

export function pesosOrEmpty(cents: number | undefined): string {
  return cents ? centsToPesos(cents) : "";
}

export function parsePesosOrZero(s: string): number | null {
  if (s.trim() === "") return 0;
  return parsePesos(s);
}

export function parseIntOrZero(s: string): number | null {
  if (s.trim() === "") return 0;
  return /^\d+$/.test(s.trim()) ? Number(s.trim()) : null;
}

/** emptyPromotion is the starting point of the builder for a type. */
export function emptyPromotion(type: PromoType): Promotion {
  const p: Promotion = {
    id: "",
    name: "",
    type,
    status: "draft",
    currency: "MXN",
    target: {},
    conditions: {},
    stacking: { mode: "stack", priority: 0 },
  };
  if (type === "buy_x_get_y") Object.assign(p, { buyQty: 2, getQty: 1, getBps: 10000 });
  if (type === "tiered") Object.assign(p, { tierBy: "subtotal", tiers: [{ min: 0, bps: 0 }] });
  if (type === "gift") Object.assign(p, { giftQty: 1 });
  if (type === "bundle") Object.assign(p, { bundleItems: [] });
  return p;
}

/** validatePromotion mirrors the API rules people hit most, so the form can point at the field before saving. */
export function validatePromotion(p: Promotion): Record<string, string> {
  const e: Record<string, string> = {};
  if (!p.name.trim()) e.name = "Escribe un nombre para reconocer la promoción.";
  const pct = (v: number | undefined) => v !== undefined && v >= 1 && v <= 10000;
  switch (p.type) {
    case "order_percent":
    case "item_percent":
      if (!pct(p.bps)) e.bps = "Escribe un porcentaje entre 0.01 y 100.";
      break;
    case "order_fixed":
    case "item_fixed":
      if (!p.amount || p.amount < 1) e.amount = "Escribe un monto mayor a cero.";
      break;
    case "buy_x_get_y":
      if (!p.buyQty || p.buyQty < 1) e.buyQty = "Indica cuántas piezas se compran.";
      if (!p.getQty || p.getQty < 1) e.getQty = "Indica cuántas piezas llevan el beneficio.";
      if (!pct(p.getBps)) e.getBps = "Escribe un porcentaje entre 0.01 y 100.";
      break;
    case "tiered": {
      const ts = p.tiers ?? [];
      if (ts.length === 0) e.tiers = "Agrega al menos un escalón.";
      else if (ts.some((t) => !t.min || t.min < 1)) e.tiers = "Cada escalón necesita un mínimo mayor a cero.";
      else if (ts.some((t) => !(t.bps && t.bps > 0) && !(t.amount && t.amount > 0))) e.tiers = "Cada escalón necesita su descuento.";
      else if (new Set(ts.map((t) => t.min)).size !== ts.length) e.tiers = "Hay dos escalones con el mismo mínimo.";
      break;
    }
    case "gift":
      if (!p.giftSku) e.giftSku = "Elige el producto de regalo.";
      break;
    case "bundle": {
      const items = p.bundleItems ?? [];
      if (items.reduce((a, b) => a + (b.qty || 0), 0) < 2) e.bundleItems = "Un paquete necesita al menos 2 piezas en total.";
      if (!p.bundlePrice || p.bundlePrice < 1) e.bundlePrice = "Escribe el precio del paquete.";
      break;
    }
  }
  const c = p.conditions;
  if (c.startsAt && c.endsAt && Date.parse(c.endsAt) <= Date.parse(c.startsAt)) e.endsAt = "La fecha de fin debe ser posterior a la de inicio.";
  if (p.stacking.mode === "group" && !(p.stacking.group ?? "").trim()) e.group = "Escribe el nombre del grupo.";
  return e;
}

/** cleanPromotion drops empty optional values so the API receives a canonical body. */
export function cleanPromotion(p: Promotion): Partial<Promotion> {
  const list = (xs?: string[]) => (xs && xs.length ? xs : undefined);
  const target = (t: Promotion["target"]) => ({
    skus: list(t.skus),
    productIds: list(t.productIds),
    categories: list(t.categories),
    brands: list(t.brands),
    collections: list(t.collections),
    excludeSkus: list(t.excludeSkus),
  });
  const c = p.conditions;
  const getTarget = p.getTarget && Object.values(target(p.getTarget)).some(Boolean) ? target(p.getTarget) : undefined;
  return {
    id: p.id || undefined,
    name: p.name.trim(),
    description: p.description?.trim() || undefined,
    type: p.type,
    status: p.status,
    campaignId: p.campaignId || undefined,
    currency: p.currency || "MXN",
    bps: p.bps || undefined,
    amount: p.amount || undefined,
    buyQty: p.buyQty || undefined,
    getQty: p.getQty || undefined,
    getBps: p.getBps || undefined,
    getTarget,
    tierBy: p.type === "tiered" ? p.tierBy : undefined,
    tiers: p.type === "tiered" ? p.tiers : undefined,
    giftSku: p.giftSku || undefined,
    giftQty: p.giftQty || undefined,
    bundleItems: p.type === "bundle" ? p.bundleItems : undefined,
    bundlePrice: p.bundlePrice || undefined,
    target: target(p.target),
    conditions: {
      channels: list(c.channels),
      minSubtotal: c.minSubtotal || undefined,
      minQuantity: c.minQuantity || undefined,
      startsAt: c.startsAt || undefined,
      endsAt: c.endsAt || undefined,
      maxUses: c.maxUses || undefined,
      maxUsesPerCustomer: c.maxUsesPerCustomer || undefined,
      emailDomains: list(c.emailDomains),
      segments: list(c.segments),
      paymentMethods: list(c.paymentMethods),
      requiresCoupon: c.requiresCoupon || undefined,
    },
    stacking: { mode: p.stacking.mode, group: p.stacking.mode === "group" ? p.stacking.group?.trim() : undefined, priority: p.stacking.priority || 0 },
    maxDiscount: p.maxDiscount || undefined,
  };
}
