import { credentials, request } from "@/api";

const enc = encodeURIComponent;
function qs(params: Record<string, string | number | undefined | null>): string {
  const p = Object.entries(params).filter(([, v]) => v !== undefined && v !== null && v !== "");
  return p.length ? "?" + p.map(([k, v]) => `${k}=${enc(String(v))}`).join("&") : "";
}

// ---------- orders ----------

export interface OLine { line: number; sku: string; title: string; qty: number; unit: number; total: number; fulfilledQty: number; returnedQty: number; refundedQty: number; cancelledQty: number }
export interface OEvent { seq: number; at: string; actor: string; type: string; from: string | null; to: string; data?: Record<string, unknown> }
export interface OPayment { provider: string; ref: string; status: string; amount: number; refunded: number }
export interface Fulfillment { id: string; number: number; status: "pending" | "shipped" | "delivered" | "cancelled"; carrier: string; trackingNumber: string; trackingUrl: string; note?: string; items: { line: number; sku: string; title: string; qty: number }[]; createdAt: string; shippedAt?: string; deliveredAt?: string }
export interface Refund { id: string; kind: string; status: string; amount: number; reason?: string; items: { line: number; sku: string; qty: number; amount: number }[]; lastError?: string; createdBy: string; createdAt: string }
export interface ReturnItem { line: number; sku: string; title: string; qty: number; reason: string; receivedQty: number; restock?: boolean }
export interface Return { id: string; orderId: string; orderNumber?: number; currency?: string; number: number; kind: string; status: "requested" | "received" | "completed" | "cancelled"; note?: string; refundAmount: number; credit: number; items: ReturnItem[]; createdAt: string; receivedAt?: string }
export interface ONote { id: string; visibility: "internal" | "customer"; body: string; author: string; authorName: string; createdAt: string }
export interface ShippingMethod { id: string; name: string; kind: string; carrier?: string; zone?: string; locationName?: string; pickupAddress?: string; instructions?: string; price: number; priceReason?: string; estimate?: { text: string } }
export interface Order {
  displayNumber?: string;
  shippingMethod?: ShippingMethod;
  taxDetail?: { regionName: string; lines: { label: string; rateBps: number; tax: number }[] };
  id: string; number: number; channel: string; status: string; currency: string;
  itemsTotal: number; discounts: number; shipping: number; taxIncluded: number; grandTotal: number;
  customer?: { email: string; name: string; phone?: string; address?: string; notes?: string };
  customerId?: string; customerName?: string; customerEmail?: string;
  paymentStatus: string; fulfillmentStatus: string; refundedTotal: number; paidTotal: number; balance: number; itemsCount?: number;
  lines: OLine[] | null; events?: OEvent[]; payment?: OPayment; invoice?: { status: string; url?: string; attempts: number; lastError?: string };
  fulfillments?: Fulfillment[]; refunds?: Refund[]; returns?: Return[]; notes?: ONote[];
  promotions?: { promotionId: string; promotionName: string; coupon?: string; amount: number; status: string }[];
  createdAt: string;
}
export interface OrderQuery { q?: string; status?: string; payment?: string; fulfillment?: string; from?: string; to?: string; customer?: string; cursor?: string; limit?: number }
export interface Quote { items: { line: number; sku: string; qty: number; amount: number }[]; itemsAmount: number; shipping: number; suggested: number; maxRefundable: number; shippingLeft: number; tax: number }
export interface OrderView { id: string; name: string; filters: OrderQuery }
export interface Draft { id: string; number: number; status: string; currency: string; items: { sku: string; title: string; qty: number; unit: number; total: number }[]; customer?: { email: string; name: string }; grandTotal: number; itemsTotal: number; shipping: number; discount: number; note: string; orderId?: string; createdAt?: string; payUrl?: string }

async function download(path: string, fallback: string) {
  const headers: Record<string, string> = {};
  if (credentials.key) headers.Authorization = `Bearer ${credentials.key}`;
  const res = await fetch(path, { headers, credentials: "same-origin" });
  if (!res.ok) throw new Error("No se pudo descargar (HTTP " + res.status + ")");
  const a = document.createElement("a");
  a.href = URL.createObjectURL(await res.blob());
  a.download = /filename="([^"]+)"/.exec(res.headers.get("Content-Disposition") ?? "")?.[1] ?? fallback;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

export const sale = {
  orders: (f: OrderQuery) => request<{ items: Order[]; nextCursor: string; total: number }>("GET", "/v1/orders" + qs({ ...f })),
  order: (id: string) => request<Order>("GET", `/v1/orders/${enc(id)}`),
  exportOrders: (f: OrderQuery) => download("/v1/orders/export.csv" + qs({ ...f, cursor: undefined, limit: undefined }), "pedidos.csv"),
  views: () => request<{ items: OrderView[] }>("GET", "/v1/order-views").then((r) => r.items),
  saveView: (name: string, filters: OrderQuery) => request<OrderView>("POST", "/v1/order-views", { name, filters }),
  deleteView: (id: string) => request<void>("DELETE", `/v1/order-views/${enc(id)}`, undefined, { as: "none" }),
  fulfill: (id: string, b: { items: { line: number; qty: number }[]; carrier: string; trackingNumber: string; trackingUrl?: string; note?: string; pending?: boolean; notify?: boolean }) =>
    request<Order>("POST", `/v1/orders/${enc(id)}/fulfillments`, b),
  updateFulfillment: (id: string, fid: string, b: { status?: string; carrier?: string; trackingNumber?: string; trackingUrl?: string }) =>
    request<Order>("PATCH", `/v1/orders/${enc(id)}/fulfillments/${enc(fid)}`, b),
  refundQuote: (id: string, b: { lines: { line: number; qty: number }[]; shipping: number; restock: boolean }) => request<Quote>("POST", `/v1/orders/${enc(id)}/refunds/quote`, b),
  refund: (id: string, b: { lines: { line: number; qty: number }[]; shipping: number; amount?: number; reason: string; restock: boolean; notify?: boolean }, key: string) =>
    request<Refund>("POST", `/v1/orders/${enc(id)}/refunds`, b, { headers: { "Idempotency-Key": key } }),
  createReturn: (id: string, b: { items: { line: number; qty: number; reason: string }[]; note: string }) => request<Return>("POST", `/v1/orders/${enc(id)}/returns`, b),
  addNote: (id: string, body: string, visibility: "internal" | "customer") => request<ONote>("POST", `/v1/orders/${enc(id)}/notes`, { body, visibility }),
  deleteNote: (id: string, note: string) => request<void>("DELETE", `/v1/orders/${enc(id)}/notes/${enc(note)}`, undefined, { as: "none" }),
  cancel: (id: string, reason: string) => request<Order>("POST", `/v1/orders/${enc(id)}/cancel`, { reason }),

  returns: (status: string) => request<{ items: Return[]; reasons: string[] }>("GET", "/v1/returns" + qs({ status })),
  receiveReturn: (id: string, items: { line: number; restock: boolean }[], skipRefund: boolean) => request<Return>("POST", `/v1/returns/${enc(id)}/receive`, { items, skipRefund }),
  refundReturn: (id: string) => request<unknown>("POST", `/v1/returns/${enc(id)}/refund`, {}, { as: "none" }),
  cancelReturn: (id: string) => request<unknown>("POST", `/v1/returns/${enc(id)}/cancel`, {}, { as: "none" }),

  drafts: (status: string) => request<{ items: Draft[] }>("GET", "/v1/draft-orders" + qs({ status })).then((r) => r.items ?? []),
  createDraft: (b: { channel: string; items: { sku: string; qty: number }[]; customer: { email: string; name: string }; postalCode: string; shipping: number; discount: number; note: string }) =>
    request<Draft>("POST", "/v1/draft-orders", b),
  sendDraft: (id: string) => request<Draft>("POST", `/v1/draft-orders/${enc(id)}/send`, {}),
  cancelDraft: (id: string) => request<unknown>("POST", `/v1/draft-orders/${enc(id)}/cancel`, {}, { as: "none" }),
};

// ---------- customers ----------

export interface Address { id: string; name?: string; line1?: string; line2?: string; city?: string; state?: string; postalCode?: string; phone?: string; isDefault?: boolean; [k: string]: unknown }
export interface Customer {
  id: string; email: string; name: string; phone?: string; tags: string[]; marketingConsent: boolean; hasAccount: boolean; status: string;
  ordersCount: number; totalSpent: number; currency: string; firstOrderAt?: string; lastOrderAt?: string; version: number; createdAt: string; masked?: boolean;
  addresses?: Address[]; notes?: { id: string; body: string; authorName: string; createdAt: string }[]; groups?: { id: string; name: string }[];
}
export interface Condition { field: string; op: string; value: string | number | boolean }
export interface Group { id: string; name: string; description: string; kind: "manual" | "rules"; rules: { match: "all" | "any"; conditions: Condition[] }; count: number; updatedAt: string }
export interface Cart { id: string; email: string; name?: string; lines: { sku: string; title: string; qty: number; total: number }[]; itemsCount: number; total: number; currency: string; status: string; remindedAt?: string; recoveredOrderId?: string; updatedAt: string }

export const crm = {
  list: (f: { q?: string; tag?: string; group?: string; consent?: string; sort?: string; cursor?: string; limit?: number }) =>
    request<{ items: Customer[]; nextCursor: string; total: number }>("GET", "/v1/customers" + qs({ ...f })),
  get: (id: string) => request<Customer>("GET", `/v1/customers/${enc(id)}`),
  create: (b: { email: string; name: string; phone?: string; tags?: string[]; marketingConsent?: boolean }) => request<Customer>("POST", "/v1/customers", b),
  update: (id: string, b: Partial<Pick<Customer, "name" | "phone" | "tags" | "marketingConsent" | "status">> & { version: number }) => request<Customer>("PATCH", `/v1/customers/${enc(id)}`, b),
  orders: (id: string) => request<{ items: Order[] }>("GET", `/v1/customers/${enc(id)}/orders`),
  addNote: (id: string, body: string) => request<unknown>("POST", `/v1/customers/${enc(id)}/notes`, { body }),
  tags: () => request<{ tags: string[] | Record<string, number> | null }>("GET", "/v1/customers/tags").then((r) => (Array.isArray(r.tags) ? r.tags : Object.keys(r.tags ?? {}))),
  groups: () => request<{ items: Group[] | null }>("GET", "/v1/customer-groups").then((r) => r.items ?? []),
  saveGroup: (g: Partial<Group>) => (g.id ? request<Group>("PUT", `/v1/customer-groups/${enc(g.id)}`, g) : request<Group>("POST", "/v1/customer-groups", g)),
  deleteGroup: (id: string) => request<void>("DELETE", `/v1/customer-groups/${enc(id)}`, undefined, { as: "none" }),
  previewGroup: (rules: Group["rules"]) => request<{ count: number }>("POST", "/v1/customer-groups/preview", { rules }),
  carts: (status: string) => request<{ items: Cart[] | null; stats?: Record<string, number> }>("GET", "/v1/abandoned-carts" + qs({ status })),
  remind: (id: string) => request<unknown>("POST", `/v1/abandoned-carts/${enc(id)}/remind`, {}, { as: "none" }),
  dismiss: (id: string) => request<unknown>("POST", `/v1/abandoned-carts/${enc(id)}/dismiss`, {}, { as: "none" }),
};

// ---------- notifications ----------

export interface Blocks { heading: string; body: string; cta: string; footer: string }
export interface MailTemplate { key: string; name: string; description: string; subject: string; blocks: Blocks; variables: { name: string; label: string; sample: string }[]; optional: boolean; enabled: boolean; customized: boolean; default: { subject: string; blocks: Blocks } }
export interface MailEntry { id: string; template: string; status: string; to: string; subject: string; provider: string; attempts: number; lastError?: string; orderId?: string; createdAt: string; sentAt?: string; html?: string }
export interface MailSettings { provider: string; senderName: string; senderEmail: string; replyTo: string; brand: { storeName: string; color: string; logoUrl: string; footer: string }; cartReminderMinutes: number; smtp: { host?: string; port?: number; username?: string; password?: string }; hasSmtpPassword: boolean; sendgridKey?: string; hasSendgridKey: boolean }

export const mail = {
  templates: () => request<{ items: MailTemplate[] }>("GET", "/v1/notifications/templates").then((r) => r.items),
  save: (key: string, b: { subject?: string; blocks?: Blocks; enabled?: boolean; reset?: boolean }) => request<MailTemplate>("PUT", `/v1/notifications/templates/${enc(key)}`, b),
  preview: (key: string, b: { subject?: string; blocks?: Blocks }) => request<{ subject: string; html: string; text: string }>("POST", `/v1/notifications/templates/${enc(key)}/preview`, b),
  test: (key: string, to: string) => request<{ id: string }>("POST", `/v1/notifications/templates/${enc(key)}/test`, { to }),
  messages: (f: { status?: string; template?: string; q?: string; cursor?: string }) => request<{ items: MailEntry[] | null; nextCursor: string; stats?: Record<string, number> }>("GET", "/v1/notifications/messages" + qs({ ...f })),
  message: (id: string) => request<MailEntry>("GET", `/v1/notifications/messages/${enc(id)}`),
  retry: (id: string) => request<unknown>("POST", `/v1/notifications/messages/${enc(id)}/retry`, {}, { as: "none" }),
  settings: () => request<MailSettings>("GET", "/v1/notifications/settings"),
  saveSettings: (s: Partial<MailSettings>) => request<MailSettings>("PUT", "/v1/notifications/settings", s),
};
