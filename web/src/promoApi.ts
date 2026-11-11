/** API client for promotions, campaigns, coupons, the simulator and pricing tools. */
import { credentials, request, type Money } from "./api";

const enc = encodeURIComponent;

export type PromoType =
  | "order_percent"
  | "order_fixed"
  | "item_percent"
  | "item_fixed"
  | "free_shipping"
  | "buy_x_get_y"
  | "tiered"
  | "gift"
  | "bundle";

export interface Target {
  attributes?: Record<string, string>;
  skus?: string[];
  productIds?: string[];
  categories?: string[];
  brands?: string[];
  collections?: string[];
  excludeSkus?: string[];
}

export interface Conditions {
  channels?: string[];
  minSubtotal?: number;
  minQuantity?: number;
  startsAt?: string;
  endsAt?: string;
  maxUses?: number;
  maxUsesPerCustomer?: number;
  emailDomains?: string[];
  segments?: string[];
  paymentMethods?: string[];
  requiresCoupon?: boolean;
}

export interface Tier {
  min: number;
  bps?: number;
  amount?: number;
}

export interface BundleItem {
  sku?: string;
  productId?: string;
  qty: number;
}

export interface Promotion {
  id: string;
  name: string;
  description?: string;
  type: PromoType;
  status: "active" | "paused" | "draft" | "archived";
  campaignId?: string;
  currency: string;
  bps?: number;
  amount?: number;
  buyQty?: number;
  getQty?: number;
  getBps?: number;
  getTarget?: Target | null;
  tierBy?: "subtotal" | "quantity";
  tiers?: Tier[];
  giftSku?: string;
  giftQty?: number;
  bundleItems?: BundleItem[];
  bundlePrice?: number;
  target: Target;
  conditions: Conditions;
  stacking: { mode: "stack" | "exclusive" | "group"; group?: string; priority: number };
  maxDiscount?: number;
}

export type DisplayStatus = "activa" | "programada" | "vencida" | "pausada" | "borrador" | "archivada" | "agotada";

export interface PromotionView extends Promotion {
  displayStatus: DisplayStatus;
  summary: string;
  uses: number;
  discountTotal: number;
  coupons: number;
  campaignName?: string;
  createdAt: string;
  updatedAt: string;
}

export interface Redemption {
  orderId: string;
  orderNumber?: number;
  promotionId: string;
  promotionName: string;
  coupon?: string;
  amount: number;
  currency: string;
  status: "reserved" | "redeemed" | "released";
  createdAt: string;
}

export interface PromoResult {
  id: string;
  name: string;
  type: PromoType;
  applied: boolean;
  code: string;
  reason: string;
  discount: Money;
  shippingDiscount?: Money;
  freeShipping?: boolean;
  coupon?: string;
  lines?: { sku: string; amount: number }[];
  missing?: Money;
  capped?: boolean;
  hint?: string;
}

export interface CouponResult {
  code: string;
  applied: boolean;
  reasonCode: string;
  reason: string;
  promotionId?: string;
  promotion?: string;
  discount?: Money;
  missing?: Money;
}

export interface SimLine {
  sku: string;
  title: string;
  requestedQty: number;
  qty: number;
  unit: Money;
  compareAt?: Money;
  total: Money;
  available: number;
  discount?: Money;
  priceList?: string;
  gift?: boolean;
}

export interface Simulation {
  channel: string;
  lines: SimLine[];
  promotions: PromoResult[];
  coupons: CouponResult[];
  shipping?: { name: string; price: Money; original?: Money; estimateDays: number };
  totals: { items: Money; discounts: Money; shipping: Money; shippingDiscount: Money; taxIncluded: Money; grand: Money };
  messages: { code: string; sku?: string; text: string }[];
}

export interface TestCart {
  channel: string;
  postalCode?: string;
  items: { sku: string; qty: number }[];
  coupons?: string[];
  email?: string;
  paymentMethod?: string;
  at?: string;
}

export interface Preview {
  summary: string;
  displayStatus: DisplayStatus;
  errors: { field: string; message: string }[];
  simulation?: Simulation;
  result?: PromoResult;
}

export interface Campaign {
  id: string;
  name: string;
  description?: string;
  status: "active" | "paused";
  startsAt?: string;
  endsAt?: string;
  budgetType: "none" | "amount" | "uses";
  budgetLimit: number;
  spent: number;
  used: number;
  currency: string;
}

export interface CampaignView extends Campaign {
  displayStatus: DisplayStatus;
  promotions: number;
  createdAt: string;
  updatedAt: string;
}

export interface Coupon {
  code: string;
  promotionId: string;
  promotionName: string;
  status: "active" | "disabled";
  state: "activo" | "desactivado" | "vencido" | "programado" | "agotado";
  startsAt?: string;
  endsAt?: string;
  maxUses: number;
  maxUsesPerCustomer: number;
  uses: number;
  batchId?: string;
  createdAt: string;
}

export interface CouponSpec {
  promotionId: string;
  startsAt?: string;
  endsAt?: string;
  maxUses: number;
  maxUsesPerCustomer: number;
}

export interface Batch {
  id: string;
  promotionId: string;
  promotionName: string;
  prefix: string;
  quantity: number;
  used: number;
  actor: string;
  createdAt: string;
}

export interface TargetProduct {
  id: string;
  title: string;
  brand?: string;
  categories?: string[];
  variants: { sku: string; title: string; price?: number; available: number }[];
}

export interface Facets {
  brands: { value: string; count: number }[];
  categories: { value: string; count: number }[];
  collections: { value: string; count: number }[];
  channels: string[];
  paymentMethods: string[];
  timezone: string;
}

export interface PriceListItem {
  sku: string;
  amount?: number;
  title?: string;
  basePrice?: number;
  finalPrice?: number;
}

export interface PriceList {
  id: string;
  name: string;
  description?: string;
  status: "active" | "paused";
  channel: string;
  customerGroup: string;
  kind: "override" | "adjust";
  adjustBps: number;
  appliesTo: "all" | "items";
  priority: number;
  startsAt?: string;
  endsAt?: string;
  currency: string;
  itemCount: number;
  displayStatus: DisplayStatus;
  summary: string;
  updatedAt: string;
}

export interface PriceListInput {
  id?: string;
  name: string;
  description?: string;
  status: "active" | "paused";
  channel: string;
  customerGroup: string;
  kind: "override" | "adjust";
  adjustBps: number;
  appliesTo: "all" | "items";
  priority: number;
  startsAt?: string;
  endsAt?: string;
  items: { sku: string; amount?: number }[];
}

export interface BulkChange {
  channel: string;
  mode: "percent" | "fixed" | "set";
  value: number;
  rounding: "none" | "peso" | "ten" | "ninety";
  compareAt: "keep" | "previous" | "clear";
  tiers: "all" | "base";
  scope: { all?: boolean; skus?: string[]; productIds?: string[]; brands?: string[]; categories?: string[] };
  hash?: string;
}

export interface BulkRow {
  sku: string;
  title?: string;
  minQty: number;
  old: number;
  new: number;
  oldCompareAt?: number;
  newCompareAt?: number;
  currency: string;
}

export interface BulkResult {
  id?: string;
  hash: string;
  skus: number;
  changed: number;
  unchanged: number;
  noPrice: number;
  increases: number;
  decreases: number;
  oldTotal: number;
  newTotal: number;
  rows: BulkRow[];
  truncated: boolean;
  applied: boolean;
}

export interface PriceChange {
  id: number;
  sku: string;
  title: string;
  channel: string;
  minQty: number;
  old: number | null;
  new: number | null;
  oldCompareAt: number | null;
  newCompareAt: number | null;
  currency: string;
  source: string;
  ref?: string;
  actor: string;
  at: string;
}

function qs(params: Record<string, string | number | undefined>): string {
  const p = Object.entries(params).filter(([, v]) => v !== undefined && v !== "");
  return p.length ? "?" + p.map(([k, v]) => `${k}=${enc(String(v))}`).join("&") : "";
}

export interface PromotionQuery {
  status?: string;
  type?: string;
  campaign?: string;
  q?: string;
  sort?: string;
  cursor?: string;
  limit?: number;
}

export const promoApi = {
  list: (f: PromotionQuery) =>
    request<{ items: PromotionView[]; total: number; counts: Record<string, number>; nextCursor: string }>("GET", "/v1/promotions" + qs({ ...f })),
  get: (id: string) => request<{ promotion: PromotionView; redemptions: Redemption[] }>("GET", `/v1/promotions/${enc(id)}`),
  create: (p: Partial<Promotion>) => request<PromotionView>("POST", "/v1/promotions", p),
  save: (id: string, p: Partial<Promotion>) => request<PromotionView>("PUT", `/v1/promotions/${enc(id)}`, p),
  remove: (id: string) => request<{ deleted: boolean; archived: boolean }>("DELETE", `/v1/promotions/${enc(id)}`),
  duplicate: (id: string) => request<PromotionView>("POST", `/v1/promotions/${enc(id)}/duplicate`),
  bulk: (ids: string[], action: "activate" | "pause" | "archive" | "draft") =>
    request<{ changed: number }>("POST", "/v1/promotions/bulk", { ids, action }),
  preview: (promotion: Partial<Promotion>, cart?: TestCart) => request<Preview>("POST", "/v1/promotions/preview", { promotion, cart }),
  simulate: (cart: TestCart) => request<{ result: Simulation; at: string; atText: string }>("POST", "/v1/promotions/simulate", cart),
  targets: (q: string, channel = "mx") => request<{ items: TargetProduct[] }>("GET", "/v1/promotions/targets" + qs({ q, channel })).then((r) => r.items),
  resolveTargets: (ids: string[], skus: string[], channel = "mx") =>
    ids.length + skus.length === 0
      ? Promise.resolve([] as TargetProduct[])
      : request<{ items: TargetProduct[] }>("GET", "/v1/promotions/targets" + qs({ ids: ids.join(","), skus: skus.join(","), channel })).then((r) => r.items),
  facets: () => request<Facets>("GET", "/v1/promotions/facets"),

  campaigns: () => request<{ items: CampaignView[] }>("GET", "/v1/campaigns").then((r) => r.items),
  campaign: (id: string) => request<{ campaign: CampaignView; promotions: PromotionView[] }>("GET", `/v1/campaigns/${enc(id)}`),
  createCampaign: (c: Partial<Campaign>) => request<CampaignView>("POST", "/v1/campaigns", c),
  saveCampaign: (id: string, c: Partial<Campaign>) => request<CampaignView>("PUT", `/v1/campaigns/${enc(id)}`, c),
  removeCampaign: (id: string) => request<void>("DELETE", `/v1/campaigns/${enc(id)}`, undefined, { as: "none" }),

  coupons: (f: { promotion?: string; batch?: string; q?: string; state?: string; cursor?: string; limit?: number }) =>
    request<{ items: Coupon[]; total: number; nextCursor: string }>("GET", "/v1/coupons" + qs({ ...f })),
  createCoupon: (code: string, spec: CouponSpec) => request<{ code: string }>("POST", "/v1/coupons", { code, ...spec }),
  generateCoupons: (g: CouponSpec & { prefix: string; length: number; quantity: number }) => request<Batch>("POST", "/v1/coupons/generate", g),
  couponsBulk: (codes: string[], action: "enable" | "disable" | "delete") => request<{ changed: number }>("POST", "/v1/coupons/bulk", { codes, action }),
  batches: () => request<{ items: Batch[] }>("GET", "/v1/coupons/batches").then((r) => r.items),
  /** exportCoupons downloads the CSV with the session (or API key) and hands it to the browser. */
  exportCoupons: async (f: { promotion?: string; batch?: string; state?: string }) => {
    const headers: Record<string, string> = {};
    if (credentials.key) headers.Authorization = `Bearer ${credentials.key}`;
    const res = await fetch("/v1/coupons/export" + qs({ ...f }), { headers, credentials: "same-origin" });
    if (!res.ok) throw new Error("No se pudo exportar (HTTP " + res.status + ")");
    const blob = await res.blob();
    const name = /filename="([^"]+)"/.exec(res.headers.get("Content-Disposition") ?? "")?.[1] ?? "cupones.csv";
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = name;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
    return name;
  },

  priceLists: () => request<{ items: PriceList[] }>("GET", "/v1/price-lists").then((r) => r.items),
  priceList: (id: string) => request<{ list: PriceList; items: PriceListItem[] }>("GET", `/v1/price-lists/${enc(id)}`),
  createPriceList: (l: PriceListInput) => request<{ list: PriceList; items: PriceListItem[] }>("POST", "/v1/price-lists", l),
  savePriceList: (id: string, l: PriceListInput) => request<{ list: PriceList; items: PriceListItem[] }>("PUT", `/v1/price-lists/${enc(id)}`, l),
  removePriceList: (id: string) => request<void>("DELETE", `/v1/price-lists/${enc(id)}`, undefined, { as: "none" }),
  bulkPreview: (c: BulkChange) => request<BulkResult>("POST", "/v1/pricing/bulk/preview", c),
  bulkApply: (c: BulkChange) => request<BulkResult>("POST", "/v1/pricing/bulk/apply", c),
  history: (f: { sku?: string; channel?: string; ref?: string; before?: number; limit?: number }) =>
    request<{ items: PriceChange[]; nextBefore: number; current?: { unit: Money; compareAt?: Money; priceListName?: string }; title?: string }>(
      "GET",
      "/v1/pricing/history" + qs({ ...f }),
    ),
};
