import { ApiError, credentials, request } from "@/api";

const enc = encodeURIComponent;

// ---------- types (mirror internal/logistics and internal/sitecfg) ----------

export interface PostalRange {
  from: string;
  to: string;
}

export interface StateInfo {
  code: string;
  name: string;
}

export interface Address {
  line1: string;
  line2?: string;
  city: string;
  state: string;
  postalCode: string;
  country: string;
}

export interface Hours {
  open: string;
  close: string;
  closed: boolean;
}

export interface Location {
  id: string;
  name: string;
  kind: "warehouse" | "store";
  address: Address;
  phone?: string;
  hours?: Record<string, Hours>;
  pickupEnabled: boolean;
  pickupInstructions?: string;
  active: boolean;
  fulfillerEndpoint?: string;
}

export interface LocationRow extends Location {
  usage: { skus: number; onHand: number; reserved: number };
  registered: boolean;
  pickupOptions: string[];
}

export interface StockRow {
  sku: string;
  title: string;
  locationId: string;
  onHand: number;
  reserved: number;
  available: number;
  threshold: number;
  customThreshold: boolean;
}

export interface TransferLine {
  sku: string;
  qty: number;
}

export interface Transfer {
  id: string;
  number: number;
  from: string;
  to: string;
  note: string;
  actor: string;
  units: number;
  createdAt: string;
  lines?: TransferLine[];
}

export interface Zone {
  id: string;
  name: string;
  countries: string[];
  states: string[];
  postalRanges: PostalRange[];
  active: boolean;
}

export interface Profile {
  id: string;
  name: string;
  kind: "default" | "heavy" | "custom";
  description?: string;
}

export interface Carrier {
  id: string;
  name: string;
  trackingUrl: string;
  active: boolean;
  provider?: "" | "sandbox" | "http";
  endpoint?: string;
}

export interface Bracket {
  upTo: number;
  amount: number;
}

export interface Rate {
  type: "flat" | "weight" | "subtotal" | "live";
  amount: number;
  perKg: number;
  brackets: Bracket[];
  freeOver: number;
}

export interface ShippingOption {
  id: string;
  name: string;
  kind: "delivery" | "pickup";
  zoneId?: string;
  profileId: string;
  carrierId?: string;
  locationId?: string;
  rate: Rate;
  estimate: { minDays: number; maxDays: number; cutoff: string; weekend: "none" | "saturday" | "all" };
  maxWeightGrams: number;
  active: boolean;
  position: number;
}

export interface Holiday {
  date: string;
  name: string;
}

export interface ShippingConfig {
  zones: Zone[];
  profiles: Profile[];
  carriers: Carrier[];
  options: ShippingOption[];
  holidays: Holiday[];
  locations: Location[];
  states: StateInfo[];
  productsByProfile: Record<string, number>;
  timezone: string;
  legacy?: { name: string; base: { amount: number }; perKg: { amount: number }; freeOver: { amount: number }; estimateDays: number };
}

export interface Estimate {
  dispatch: string;
  from: string;
  to: string;
  text: string;
  notes: string[];
}

export interface Quote {
  id: string;
  name: string;
  kind: string;
  carrier?: string;
  zone?: string;
  locationName?: string;
  pickupAddress?: string;
  available: boolean;
  reason: string;
  price: number;
  priceReason: string;
  free: boolean;
  freeMissing?: number;
  estimate?: Estimate;
}

export interface TaxRegion {
  id: string;
  name: string;
  country: string;
  rateBps: number;
  states: string[];
  postalRanges: PostalRange[];
  default: boolean;
}

export interface TaxLine {
  class: string;
  label: string;
  rateBps: number;
  gross: number;
  net: number;
  tax: number;
}

export interface TaxResult {
  regionId: string;
  regionName: string;
  reason: string;
  lines: TaxLine[];
  total: number;
}

export interface ProductClassRow {
  productId: string;
  title: string;
  handle: string;
  taxClass: "general" | "exento" | "tasa0";
  shippingProfile: string;
  missing?: boolean;
}

export interface ProductHit {
  id: string;
  title: string;
  handle: string;
  skus: string[];
}

export interface Channel {
  id: string;
  name?: string;
  currency: string;
  locale?: string;
  country?: string;
  taxRegion?: string;
  priceList?: string;
  taxBps: number;
  maxQtyPerItem: number;
  shipping: unknown;
  market?: boolean;
  priceFrom?: string;
  rateMicros?: number;
  rounding?: string;
  taxExclusive?: boolean;
  language?: string;
  dutyBps?: number;
  dutyMinimis?: number;
  dutyRules?: { hsPrefix: string; bps: number }[];
}

export interface CheckoutRules {
  guestCheckout: boolean;
  phoneRequired: boolean;
  notesField: boolean;
  termsCheckbox: boolean;
  termsPolicy: string;
}

export interface StoreSettings {
  name: string;
  legalName: string;
  rfc: string;
  email: string;
  phone: string;
  address: Address;
  logoUrl: string;
  social: Record<string, string>;
  checkout: CheckoutRules;
  orderPrefix: string;
  timezone: string;
  lowStockThreshold: number;
  robots: string;
}

export interface Policy {
  slug: string;
  title: string;
  body: string;
  published: boolean;
  updatedAt: string;
}

export type MenuItemType = "home" | "catalog" | "category" | "product" | "page" | "policy" | "url";

export interface MenuItem {
  id: string;
  label: string;
  type: MenuItemType;
  ref?: string;
  url?: string;
  newTab?: boolean;
  children: MenuItem[];
}

export interface Menu {
  id: string;
  name: string;
  items: MenuItem[];
  updatedAt?: string;
}

export interface Target {
  ref: string;
  label: string;
}

export interface Redirect {
  id: string;
  from: string;
  target: string;
  code: 301 | 302;
  active: boolean;
  createdAt: string;
}

export interface Domain {
  id: string;
  hostname: string;
  site: string;
  market?: string;
  status: "pending" | "verified" | "failed";
  primary: boolean;
  lastError?: string;
  lastCheckedAt: string | null;
  verifiedAt: string | null;
  createdAt: string;
  txtHost: string;
  txtValue: string;
  cnameTarget: string;
}

// ---------- client ----------

const noBody = { as: "none" as const };

export const sapi = {
  // locations and inventory
  locations: () =>
    request<{ items: LocationRow[]; states: StateInfo[]; lowStock: number; defaultThreshold: number }>("GET", "/v1/locations"),
  saveLocation: (l: Location) => {
    const { id, ...body } = l;
    return request<Location>("PUT", `/v1/locations/${enc(id)}`, body);
  },
  deleteLocation: (id: string) => request<void>("DELETE", `/v1/locations/${enc(id)}`, undefined, noBody),
  locationStock: (id: string, q: string, cursor = "") =>
    request<{ items: StockRow[]; nextCursor: string }>("GET", `/v1/locations/${enc(id)}/stock?q=${enc(q)}&cursor=${enc(cursor)}&limit=20`),
  transfers: (before = 0) => request<{ items: Transfer[]; nextBefore: number }>("GET", `/v1/inventory-transfers?before=${before}`),
  transfer: (id: string) => request<Transfer>("GET", `/v1/inventory-transfers/${enc(id)}`),
  createTransfer: (t: { from: string; to: string; note: string; lines: TransferLine[] }) =>
    request<Transfer>("POST", "/v1/inventory-transfers", t),
  alerts: (location: string, cursor = "") =>
    request<{ items: StockRow[]; nextCursor: string; total: number; defaultThreshold: number }>(
      "GET",
      `/v1/inventory-alerts?location=${enc(location)}&cursor=${enc(cursor)}`,
    ),
  setThreshold: (sku: string, threshold: number | null, location = "") =>
    request<void>("PUT", `/v1/inventory-thresholds/${enc(sku)}`, { threshold, location }, noBody),

  // shipping
  shipping: () => request<ShippingConfig>("GET", "/v1/shipping"),
  shippingDefaults: () => request<{ added: number }>("POST", "/v1/shipping/defaults"),
  saveShipping: <T extends { id: string }>(kind: "zones" | "profiles" | "carriers" | "options", doc: T) =>
    doc.id ? request<T>("PUT", `/v1/shipping/${kind}/${enc(doc.id)}`, doc) : request<T>("POST", `/v1/shipping/${kind}`, doc),
  deleteShipping: (kind: string, id: string) => request<void>("DELETE", `/v1/shipping/${kind}/${enc(id)}`, undefined, noBody),
  saveHoliday: (h: Holiday) => request<Holiday>("PUT", `/v1/shipping/holidays/${enc(h.date)}`, { name: h.name }),
  deleteHoliday: (date: string) => request<void>("DELETE", `/v1/shipping/holidays/${enc(date)}`, undefined, noBody),
  holidayDefaults: (year: number) => request<{ items: Holiday[] }>("POST", `/v1/shipping/holidays/defaults?year=${year}`),
  quote: (q: { postalCode: string; subtotal: number; weightGrams: number; profile: string; at?: string }) =>
    request<{ quotes: Quote[]; default: string; state: string; at: string }>("POST", "/v1/shipping/quote", q),

  // taxes
  taxRegions: () => request<{ items: TaxRegion[]; states: StateInfo[]; legacyBps?: number }>("GET", "/v1/tax/regions"),
  saveTaxRegion: (r: TaxRegion) =>
    r.id ? request<TaxRegion>("PUT", `/v1/tax/regions/${enc(r.id)}`, r) : request<TaxRegion>("POST", "/v1/tax/regions", r),
  deleteTaxRegion: (id: string) => request<void>("DELETE", `/v1/tax/regions/${enc(id)}`, undefined, noBody),
  taxDefaults: () => request<{ added: number }>("POST", "/v1/tax/defaults"),
  taxPreview: (p: { postalCode: string; amount: number; class: string }) => request<TaxResult>("POST", "/v1/tax/preview", p),
  productClasses: () => request<{ items: ProductClassRow[] }>("GET", "/v1/product-classes").then((r) => r.items),
  setProductClass: (id: string, c: { taxClass: string; shippingProfile: string }) =>
    request<unknown>("PUT", `/v1/product-classes/${enc(id)}`, c),
  productSearch: (q: string) => request<{ items: ProductHit[] }>("GET", `/v1/product-search?q=${enc(q)}`).then((r) => r.items),

  // channels
  channels: () => request<{ items: Channel[]; taxRegions: TaxRegion[]; main: string }>("GET", "/v1/channels"),
  saveChannel: (c: Channel) => {
    const { id, ...body } = c;
    return request<void>("PUT", `/v1/channels/${enc(id)}`, body, noBody);
  },
  deleteChannel: (id: string) => request<void>("DELETE", `/v1/channels/${enc(id)}`, undefined, noBody),

  // store
  settings: () =>
    request<{ settings: StoreSettings; states: StateInfo[]; socialNetworks: string[]; storefront: string; updatedAt?: string }>(
      "GET",
      "/v1/store/settings",
    ),
  saveSettings: (s: StoreSettings) => request<{ settings: StoreSettings }>("PUT", "/v1/store/settings", s),
  saveCheckout: (checkout: CheckoutRules, orderPrefix: string) =>
    request<{ settings: StoreSettings }>("PUT", "/v1/store/checkout", { checkout, orderPrefix }),
  deleteLogo: () => request<{ settings: StoreSettings }>("DELETE", "/v1/store/logo"),
  /** The logo travels as the raw image, not as JSON. */
  uploadLogo: async (file: File): Promise<{ settings: StoreSettings }> => {
    const headers: Record<string, string> = { "Content-Type": file.type || "application/octet-stream", Accept: "application/json" };
    if (credentials.key) headers.Authorization = `Bearer ${credentials.key}`;
    const res = await fetch("/v1/store/logo", { method: "POST", headers, credentials: "same-origin", body: file });
    const body = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    if (!res.ok) throw new ApiError({ status: res.status, code: String(body.code ?? "http_" + res.status), title: res.statusText, ...body });
    return body as unknown as { settings: StoreSettings };
  },

  // policies
  policies: () => request<{ items: Policy[]; standard: { slug: string; title: string }[]; base: string }>("GET", "/v1/policies"),
  savePolicy: (p: { slug: string; title: string; body: string; published: boolean }) =>
    request<Policy>("PUT", `/v1/policies/${enc(p.slug)}`, { title: p.title, body: p.body, published: p.published }),
  deletePolicy: (slug: string) => request<void>("DELETE", `/v1/policies/${enc(slug)}`, undefined, noBody),

  // menus
  menus: () => request<{ items: Menu[]; targets: Record<string, Target[]>; maxDepth: number }>("GET", "/v1/menus"),
  saveMenu: (m: Menu) => request<Menu>("PUT", `/v1/menus/${enc(m.id)}`, { name: m.name, items: m.items }),
  deleteMenu: (id: string) => request<void>("DELETE", `/v1/menus/${enc(id)}`, undefined, noBody),

  // storefront languages
  languages: () => request<LanguagesResponse>("GET", "/v1/settings/languages"),
  saveLanguages: (config: LanguageConfig) => request<{ config: LanguageConfig }>("PUT", "/v1/settings/languages", config),

  // redirects
  redirects: (q: string, cursor = "") =>
    request<{ items: Redirect[]; nextCursor: string; total: number }>("GET", `/v1/redirects?q=${enc(q)}&cursor=${enc(cursor)}&limit=50`),
  createRedirect: (r: { from: string; target: string; code: number; active: boolean }) => request<Redirect>("POST", "/v1/redirects", r),
  importRedirects: (items: { from: string; target: string; code: number }[]) =>
    request<{ items: Redirect[] }>("POST", "/v1/redirects", { items }),
  updateRedirect: (r: Redirect) =>
    request<Redirect>("PUT", `/v1/redirects/${enc(r.id)}`, { from: r.from, target: r.target, code: r.code, active: r.active }),
  deleteRedirects: (ids: string[]) => request<{ deleted: number }>("POST", "/v1/redirects/delete", { ids }),
  testRedirect: (path: string) =>
    request<{ path: string; redirects: boolean; hops: { from: string; target: string; code: number }[]; final: string }>(
      "GET",
      `/v1/redirects/test?path=${enc(path)}`,
    ),

  // domains
  domains: () => request<{ items: Domain[]; cnameTarget: string; defaultUrl: string }>("GET", "/v1/domains"),
  createDomain: (hostname: string, site = "main", market = "") => request<Domain>("POST", "/v1/domains", { hostname, site, market }),
  verifyDomain: (id: string) => request<Domain>("POST", `/v1/domains/${enc(id)}/verify`),
  primaryDomain: (id: string) => request<unknown>("POST", `/v1/domains/${enc(id)}/primary`),
  deleteDomain: (id: string) => request<void>("DELETE", `/v1/domains/${enc(id)}`, undefined, noBody),
};

/** Storefront languages: which ones are on and the store's own translations. */
export interface LanguageConfig {
  enabled: string[];
  own: Record<string, Record<string, string>>;
}
export interface LanguagesResponse {
  available: { code: string; name: string; tag: string }[];
  default: string;
  config: LanguageConfig;
}
