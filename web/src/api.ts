import type { Op, Problem, Release, Report, SectionDefinition, Template, ThemesResponse } from "./types";
import { ProblemSchema } from "./types";

const KEY = "apexx.apiKey";

function store(): Storage | null {
  try {
    return window.sessionStorage;
  } catch {
    return null;
  }
}

/**
 * Authentication uses the HttpOnly session cookie set by POST /v1/auth/login.
 * An API key can still be set as an advanced option (integrations, tests);
 * when present it is sent as a bearer token and takes precedence.
 */
export const credentials = {
  get key(): string {
    return store()?.getItem(KEY) ?? "";
  },
  save(key: string) {
    try {
      if (key) store()?.setItem(KEY, key);
      else store()?.removeItem(KEY);
    } catch {
      /* storage blocked: the cookie session still works */
    }
  },
};

export class ApiError extends Error {
  constructor(public problem: Problem) {
    super(problem.detail || problem.title || problem.code);
  }
  get status(): number {
    return this.problem.status ?? 0;
  }
}

/** Fired on any 401 so the app can show the login screen. */
export const UNAUTHORIZED_EVENT = "apexx:unauthorized";

export async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  opts: { as?: "json" | "text" | "none"; headers?: Record<string, string> } = {},
): Promise<T> {
  const as = opts.as ?? "json";
  const headers: Record<string, string> = { Accept: as === "text" ? "text/html" : "application/json", ...opts.headers };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (credentials.key) headers.Authorization = `Bearer ${credentials.key}`;
  const res = await fetch(path, {
    method,
    headers,
    credentials: "same-origin",
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    let parsed: Problem = { status: res.status, code: "http_" + res.status, title: res.statusText };
    try {
      const r = ProblemSchema.safeParse(await res.json());
      if (r.success) parsed = { ...r.data, status: r.data.status ?? res.status };
    } catch {
      /* non-JSON error body */
    }
    if (res.status === 401 && !path.startsWith("/v1/auth/login")) {
      window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
    }
    throw new ApiError(parsed);
  }
  if (as === "none" || res.status === 204) return undefined as T;
  return (as === "json" ? res.json() : res.text()) as Promise<T>;
}

const enc = encodeURIComponent;

// ---------- types of the admin API ----------

export type Level = "none" | "read" | "edit" | "admin";
export type Permissions = Record<string, Level>;

export interface Me {
  tenant: string;
  kind: "member" | "api_key";
  id: string;
  name: string;
  email?: string;
  owner: boolean;
  permissions: Permissions;
  twoStep?: boolean;
}

export interface Money {
  amount: number;
  currency: string;
}

export interface Variant {
  sku: string;
  title: string;
  options?: Record<string, string>;
  barcode?: string;
  weightGrams: number;
}

export interface Product {
  id: string;
  handle: string;
  title: string;
  description?: string;
  brand?: string;
  categories?: string[];
  status: string;
  variants: Variant[] | null;
  available?: Record<string, number>;
  updatedAt?: string;
}

export interface StockLevel {
  locationId: string;
  onHand: number;
  reserved: number;
}

export interface Snapshot {
  sku: string;
  available: number;
  levels: StockLevel[];
  updatedAt: string;
}

export interface Tier {
  minQty: number;
  unit: Money;
  compareAt?: Money | null;
}

export interface OrderLine {
  sku: string;
  title: string;
  qty: number;
  unit: number;
  total: number;
}

export interface OrderEvent {
  seq: number;
  at: string;
  actor: string;
  type: string;
  from: string | null;
  to: string;
  data: unknown;
}

export interface Order {
  id: string;
  number: number;
  channel: string;
  status: string;
  currency: string;
  itemsTotal: number;
  discounts: number;
  shipping: number;
  taxIncluded: number;
  grandTotal: number;
  customer?: { email: string; name: string; phone?: string; address?: string };
  lines: OrderLine[] | null;
  events?: OrderEvent[] | null;
  payment?: { provider: string; ref: string; status: string; amount: number; refunded: number };
  invoice?: { status: string; url?: string; attempts: number; lastError?: string };
  createdAt: string;
}

export interface Member {
  id: string;
  email: string;
  name: string;
  owner: boolean;
  status: "active" | "disabled";
  permissions: Permissions;
  createdAt: string;
  lastLoginAt: string | null;
  twoStep?: boolean;
}

export interface PaymentAccount {
  connected: boolean;
  mode?: "connect" | "own_keys";
  accountId?: string;
  publishableKey?: string;
  hasSecretKey?: boolean;
  hasWebhookSecret?: boolean;
  feeBps?: number;
}

export interface Subscription {
  id: string;
  url: string;
  types: string[] | null;
  active: boolean;
  createdAt: string;
}

export interface Delivery {
  eventId: number;
  type: string;
  subject: string;
  status: "pending" | "delivered" | "dead";
  attempts: number;
  lastStatus: number;
  lastError?: string;
  nextAttempt: string;
  deliveredAt?: string | null;
}

export interface CloudEvent {
  id: string;
  type: string;
  subject: string;
  time: string;
  apexxsequence: number;
  data: unknown;
}

export interface Freshness {
  eventsApplied: number;
  lastEventId: number;
  lastLagMs: number;
  maxLagMs: number;
  avgLagMs: number;
  errors: number;
  tenants: number;
  loadedAt: string;
}

export interface PatchResult {
  template: Template;
  report: Report;
  version: number;
}

export const api = {
  // session
  login: (store: string, email: string, password: string, code?: string) =>
    request<{ token: string; member: Member }>("POST", "/v1/auth/login", { store, email, password, code: code || undefined }),
  logout: () => request<void>("POST", "/v1/auth/logout", undefined, { as: "none" }),
  me: () => request<Me>("GET", "/v1/me"),

  // builder
  sections: () => request<{ items: SectionDefinition[] }>("GET", "/v1/builder/sections").then((r) => r.items),
  themes: () => request<ThemesResponse>("GET", "/v1/builder/themes"),
  applyTheme: (site: string, themeId: string, keepContent: boolean) =>
    request<{ template: Template; version: number; previousRevisionId: string }>("POST", `/v1/sites/${enc(site)}/theme`, { themeId, keepContent }),
  restoreRevision: (site: string, revisionId: string) =>
    request<{ template: Template; version: number }>("POST", `/v1/sites/${enc(site)}/restore`, { revisionId }),
  draft: (site: string, handle: string) =>
    request<{ template: Template; version: number }>("GET", `/v1/sites/${enc(site)}/templates/${enc(handle)}/draft`),
  patch: (site: string, handle: string, ops: Op[], ifVersion: number) =>
    request<PatchResult>("PATCH", `/v1/sites/${enc(site)}/templates/${enc(handle)}/draft`, { ops, ifVersion }),
  preview: (site: string, handle: string) =>
    request<string>("GET", `/v1/sites/${enc(site)}/templates/${enc(handle)}/preview`, undefined, { as: "text" }),
  publish: (site: string, message: string) =>
    request<{ release: Release; reports: Record<string, Report> }>(
      "POST",
      `/v1/sites/${enc(site)}/publish?message=${enc(message)}`,
    ),
  rollback: (site: string) => request<{ release: Release }>("POST", `/v1/sites/${enc(site)}/rollback`),
  releases: (site: string) =>
    request<{ items: Release[] | null }>("GET", `/v1/sites/${enc(site)}/releases`).then((r) => r.items ?? []),

  // operations
  freshness: () => request<Freshness>("GET", "/v1/system/freshness"),
  auditVerify: () => request<{ intact: boolean; firstBrokenSeq: number }>("GET", "/v1/audit/verify"),

  // catalog
  products: (cursor: string, limit = 50) =>
    request<{ items: Product[]; nextCursor: string; total: number }>(
      "GET",
      `/v1/products?limit=${limit}&cursor=${enc(cursor)}&fields=id,title,handle,status,variants,available,brand`,
    ),
  product: (id: string) => request<Product>("GET", `/v1/products/${enc(id)}`),
  saveProduct: (p: Product, version?: number) =>
    request<{ id: string; version: number }>(
      "PUT",
      `/v1/products/${enc(p.id)}`,
      {
        handle: p.handle,
        title: p.title,
        description: p.description ?? "",
        brand: p.brand ?? "",
        categories: p.categories ?? [],
        status: p.status,
        variants: p.variants ?? [],
      },
      { headers: version ? { "If-Match": `"${version}"` } : {} },
    ),
  inventory: (sku: string) => request<Snapshot>("GET", `/v1/inventory/${enc(sku)}`),
  setStock: (sku: string, location: string, onHand: number) =>
    request<void>("PUT", `/v1/inventory/${enc(sku)}/${enc(location)}`, { onHand }, { as: "none" }),
  setPrices: (channel: string, sku: string, tiers: Tier[]) =>
    request<void>("PUT", `/v1/prices/${enc(channel)}/${enc(sku)}`, { tiers }, { as: "none" }),
  /** There is no GET for price tiers yet; the simulation returns the current unit price for qty 1. */
  currentPrice: (channel: string, sku: string) =>
    request<{ lines: { sku: string; unit: Money; compareAt?: Money | null; qty: number }[] }>(
      "POST",
      "/v1/carts/simulate",
      { channel, items: [{ sku, qty: 1 }] },
    ).then((r) => r.lines.find((l) => l.sku === sku && l.qty > 0) ?? null),

  // orders
  orders: () => request<{ items: Order[] | null }>("GET", "/v1/orders").then((r) => r.items ?? []),
  order: (id: string) => request<Order>("GET", `/v1/orders/${enc(id)}`),
  fulfill: (id: string) => request<Order>("POST", `/v1/orders/${enc(id)}/fulfill`),
  cancel: (id: string, reason: string) => request<Order>("POST", `/v1/orders/${enc(id)}/cancel`, { reason }),

  // team
  members: () =>
    request<{ items: Member[] | null; modules: string[] }>("GET", "/v1/team/members").then((r) => ({
      items: r.items ?? [],
      modules: r.modules,
    })),
  createMember: (m: { email: string; name: string; password: string; permissions: Permissions }) =>
    request<Member>("POST", "/v1/team/members", m),
  updateMember: (id: string, patch: { permissions?: Permissions; status?: string }) =>
    request<Member>("PATCH", `/v1/team/members/${enc(id)}`, patch),
  createApiKey: (name: string, role: string) =>
    request<{ key: string; role: string }>("POST", "/v1/team/api-keys", { name, role }),

  // payments
  paymentAccount: () => request<PaymentAccount>("GET", "/v1/payments/account"),
  savePaymentAccount: (a: {
    mode: "connect" | "own_keys";
    accountId?: string;
    publishableKey?: string;
    secretKey?: string;
    webhookSecret?: string;
  }) => request<PaymentAccount>("PUT", "/v1/payments/account", a),

  // connectors
  webhooks: () => request<{ items: Subscription[] | null }>("GET", "/v1/webhooks").then((r) => r.items ?? []),
  createWebhook: (url: string, types: string[]) =>
    request<{ subscription: Subscription; secret: string }>("POST", "/v1/webhooks", { url, types }),
  disableWebhook: (id: string) => request<void>("DELETE", `/v1/webhooks/${enc(id)}`, undefined, { as: "none" }),
  deliveries: (id: string) =>
    request<{ items: Delivery[] | null }>("GET", `/v1/webhooks/${enc(id)}/deliveries`).then((r) => r.items ?? []),
  replay: (id: string, after: number) =>
    request<{ requeued: number }>("POST", `/v1/webhooks/${enc(id)}/replay?after=${after}`),
  events: (after: number, limit: number, types: string[] = []) =>
    request<{ items: CloudEvent[] | null; next: number }>(
      "GET",
      `/v1/events?after=${after}&limit=${limit}${types.length ? `&types=${enc(types.join(","))}` : ""}`,
    ).then((r) => ({ items: r.items ?? [], next: r.next })),
};

/** message turns any error into a short Spanish sentence for the UI. */
export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) {
    const p = e.problem;
    if (p.status === 401) return "Tu sesión terminó. Vuelve a iniciar sesión.";
    if (p.status === 403) return p.detail || "Tu acceso no permite esta acción. Pide permiso a un administrador de la tienda.";
    const fields = p.errors?.map((f) => f.message).join("; ");
    return [p.detail || p.title || p.code, fields].filter(Boolean).join(" · ");
  }
  if (e instanceof TypeError) return "No hay conexión con el servidor. Revisa tu red e intenta de nuevo.";
  return e instanceof Error ? e.message : String(e);
}

/** fieldErrors maps problem+json errors[] by field name. */
export function fieldErrors(e: unknown): Record<string, string> {
  const out: Record<string, string> = {};
  if (e instanceof ApiError) for (const f of e.problem.errors ?? []) out[f.field] = f.message;
  return out;
}
