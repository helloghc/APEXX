import { ApiError, credentials, request, UNAUTHORIZED_EVENT } from "@/api";

/** Types of the reports and operations API (internal/reports). Money is integer cents. */

export interface RangeInfo {
  preset: string;
  from: string;
  to: string;
  days: number;
  timezone: string;
}

export interface Sales {
  orders: number;
  units: number;
  gross: number;
  discounts: number;
  shipping: number;
  tax: number;
  refunds: number;
  refundCount: number;
  newCustomers: number;
  returningCustomers: number;
  net: number;
  aov: number;
}

export interface Traffic {
  sessions: number;
  productViews: number;
  addToCart: number;
  checkout: number;
  purchase: number;
  pageViews: number;
}

export interface Point extends Sales {
  bucket: string;
  traffic: Traffic;
}

export interface ProductRow {
  key: string;
  name: string;
  productId?: string;
  brand?: string;
  orders: number;
  units: number;
  revenue: number;
  views: number;
  visitors: number;
  cartVisitors: number;
}

export interface RecentOrder {
  id: string;
  number: number;
  status: string;
  channel: string;
  currency: string;
  total: number;
  units: number;
  createdAt: string;
}

export interface Alert {
  id: string;
  severity: "warn" | "bad";
  count: number;
  title: string;
  detail: string;
}

export type Grain = "hour" | "day" | "week" | "month";

export interface Dashboard {
  range: RangeInfo;
  previous: RangeInfo;
  grain: Grain;
  currency: string;
  currencies: string[];
  channels: string[];
  totals: Sales;
  previousTotals: Sales;
  traffic: Traffic;
  previousTraffic: Traffic;
  series: Point[];
  previousSeries: Point[];
  topProducts: ProductRow[];
  recentOrders: RecentOrder[];
  alerts: Alert[];
  syncedAt: string | null;
}

export interface SalesReport {
  range: RangeInfo;
  previous: RangeInfo;
  grain: Grain;
  currency: string;
  currencies: string[];
  channels: string[];
  totals: Sales;
  previousTotals: Sales;
  series: Point[];
  previousSeries: Point[];
  byChannel: (Sales & { channel: string })[];
  byHour: (Sales & { slot: number })[];
  byWeekday: (Sales & { slot: number })[];
}

export interface ProductsReport {
  range: RangeInfo;
  by: "product" | "sku" | "brand" | "category";
  currency: string;
  channels: string[];
  rows: ProductRow[];
  totals: ProductRow;
}

export interface CustomerRow {
  customer: string;
  orders: number;
  spent: number;
  firstOrderAt: string;
  lastOrderAt: string;
  lastOrderNumber: number;
  lastOrderId: string;
  new: boolean;
}

export interface CustomersReport {
  range: RangeInfo;
  currency: string;
  channels: string[];
  customers: number;
  newCustomers: number;
  returningCustomers: number;
  newOrders: number;
  returningOrders: number;
  newRevenue: number;
  returningRevenue: number;
  previousCustomers: number;
  previousNewCustomers: number;
  previousReturningCustomers: number;
  series: Point[];
  grain: Grain;
  frequency: number[];
  top: CustomerRow[];
}

export interface FunnelReport {
  range: RangeInfo;
  previous: RangeInfo;
  channels: string[];
  grain: Grain;
  totals: Traffic;
  previousTotals: Traffic;
  series: Point[];
  byDevice: (Traffic & { device: string })[];
  referrers: { referrer: string; sessions: number; purchases: number }[];
  topProducts: ProductRow[];
}

export interface PromoRow {
  id: string;
  name: string;
  kind: string;
  active: boolean;
  exists: boolean;
  orders: number;
  discount: number;
  revenue: number;
}

export interface PromotionsReport {
  range: RangeInfo;
  currency: string;
  channels: string[];
  grain: Grain;
  sales: Sales;
  totals: PromoRow;
  rows: PromoRow[];
  series: { bucket: string; orders: number; discount: number }[];
}

export interface StockRow {
  sku: string;
  title: string;
  productId: string;
  onHand: number;
  reserved: number;
  available: number;
  price: number;
  value: number;
  sold30: number;
  coverDays: number;
  state: "ok" | "low" | "out";
}

export interface InventoryReport {
  currency: string;
  channel: string;
  channels: string[];
  threshold: number;
  skus: number;
  units: number;
  reserved: number;
  value: number;
  lowStock: number;
  outOfStock: number;
  unpriced: number;
  rows: StockRow[];
  truncated: boolean;
}

export interface AuditEntry {
  seq: number;
  at: string;
  actor: string;
  actorName: string;
  action: string;
  target: string;
  data: unknown;
  hash: string;
}

export interface Facet {
  value: string;
  label: string;
  count: number;
}

export interface WorkerView {
  name: string;
  intervalSec: number;
  runs: number;
  failures: number;
  lastRun: string;
  lastOk: string;
  lastMs: number;
  lastError?: string;
  state: "ok" | "late" | "failing";
}

export interface RouteLine {
  route: string;
  requests: number;
  errors5xx: number;
  avgMs: number;
  p95Ms: number;
}

export interface SystemStatus {
  status: "ok" | "degraded" | "down";
  checkedAt: string;
  projection: {
    stats: { eventsApplied: number; lastEventId: number; lastLagMs: number; maxLagMs: number; avgLagMs: number; errors: number; tenants: number; loadedAt: string };
    eventsBehind: number;
  };
  workers: WorkerView[];
  health: {
    db: { ok: boolean; pingMs: number; version: string; poolUsed: number; poolMax: number };
    outbox: { lastId: number; last24h: number };
    analytics: { ordersSyncedAt: string | null; trafficRolledAt: string | null; ordersBehind: number; events24h: number };
    webhooks: { subscriptions: number; delivered24h: number; retrying: number; dead7d: number; oldestPendingSec: number };
    invoices: { pending: number; failed: number };
    payments: { pending: number; oldestPendingSec: number };
    stock: { low: number; out: number };
  };
  http: { requests: number; errors5xx: number; p95Ms: number; slowest: RouteLine[] };
  counters: { panics: number; bulkheadRejects: number; workerRestarts: number; breakerOpen: number };
}

/** Params shared by every report: a period and an optional channel. */
export interface ReportParams {
  range: string;
  from?: string;
  to?: string;
  channel?: string;
}

export function query(p: ReportParams, extra: Record<string, string | number | undefined> = {}): string {
  const q = new URLSearchParams();
  q.set("range", p.range);
  if (p.range === "custom") {
    if (p.from) q.set("from", p.from);
    if (p.to) q.set("to", p.to);
  }
  if (p.channel) q.set("channel", p.channel);
  for (const [k, v] of Object.entries(extra)) if (v !== undefined && v !== "") q.set(k, String(v));
  return q.toString();
}

export interface AuditQuery {
  actor?: string;
  action?: string;
  target?: string;
  from?: string;
  to?: string;
}

function auditQuery(f: AuditQuery, extra: Record<string, string> = {}): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries({ ...f, ...extra })) if (v) q.set(k, v);
  return q.toString();
}

export const reportsApi = {
  dashboard: (p: ReportParams) => request<Dashboard>("GET", `/v1/reports/dashboard?${query(p)}`),
  sales: (p: ReportParams, grain?: string) => request<SalesReport>("GET", `/v1/reports/sales?${query(p, { grain })}`),
  products: (p: ReportParams, by: string) => request<ProductsReport>("GET", `/v1/reports/products?${query(p, { by, limit: 200 })}`),
  customers: (p: ReportParams) => request<CustomersReport>("GET", `/v1/reports/customers?${query(p)}`),
  funnel: (p: ReportParams) => request<FunnelReport>("GET", `/v1/reports/funnel?${query(p)}`),
  promotions: (p: ReportParams) => request<PromotionsReport>("GET", `/v1/reports/promotions?${query(p)}`),
  inventory: (channel: string, state: string, threshold: number) =>
    request<InventoryReport>("GET", `/v1/reports/inventory?${new URLSearchParams({ channel, state, threshold: String(threshold) }).toString()}`),
  rebuild: () => request<{ orders: number; tookMs: number }>("POST", "/v1/reports/rebuild"),
  audit: (f: AuditQuery, cursor: string) =>
    request<{ items: AuditEntry[]; nextCursor: string | null }>("GET", `/v1/ops/audit?${auditQuery(f, { cursor, limit: "50" })}`),
  auditFacets: () => request<{ actors: Facet[]; actions: Facet[]; total: number }>("GET", "/v1/ops/audit/facets"),
  auditCsvPath: (f: AuditQuery) => `/v1/ops/audit?${auditQuery(f, { format: "csv" })}`,
  status: () => request<SystemStatus>("GET", "/v1/ops/status"),
};

/** download fetches a CSV with the session (or API key) and hands it to the browser as a file. */
export async function download(path: string, fallbackName: string): Promise<void> {
  const headers: Record<string, string> = { Accept: "text/csv" };
  if (credentials.key) headers.Authorization = `Bearer ${credentials.key}`;
  const res = await fetch(path, { headers, credentials: "same-origin" });
  if (!res.ok) {
    if (res.status === 401) window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
    let detail = "No se pudo exportar";
    try {
      const j = (await res.json()) as { detail?: string };
      if (j.detail) detail = j.detail;
    } catch {
      /* not JSON */
    }
    throw new ApiError({ status: res.status, code: "http_" + res.status, title: res.statusText, detail });
  }
  const name = /filename="([^"]+)"/.exec(res.headers.get("Content-Disposition") ?? "")?.[1] ?? fallbackName;
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 5000);
}
