import { ApiError, credentials, request, UNAUTHORIZED_EVENT } from "@/api";
import { ProblemSchema, type Problem } from "@/types";

const enc = encodeURIComponent;

// ---------- types ----------

export interface MediaVariant {
  w: number;
  h: number;
  format: string;
  key: string;
  bytes: number;
}

export interface MediaItem {
  id: string;
  filename: string;
  contentType: string;
  bytes: number;
  width: number;
  height: number;
  alt: string;
  variants: MediaVariant[];
  createdAt: string;
  url: string;
  thumb: string;
  srcset: string;
  webpSrcset: string;
}

export interface Option {
  name: string;
  values: string[];
}

export interface Variant {
  sku: string;
  title: string;
  options?: Record<string, string>;
  barcode?: string;
  weightGrams: number;
  mediaId?: string;
}

export interface ProductRow {
  id: string;
  handle: string;
  title: string;
  description?: string;
  brand?: string;
  brandId?: string;
  categories?: string[];
  attributes?: Record<string, string>;
  status: "active" | "draft" | "archived";
  variants: Variant[] | null;
  version: number;
  updatedAt: string;
  createdAt: string;
  type?: string;
  tags?: string[];
  options?: Option[];
  seoTitle?: string;
  seoDescription?: string;
  publishAt?: string | null;
  media?: string[];
  related?: { similar?: string[]; accessories?: string[] };
  available: Record<string, number>;
  stock: number;
  variantCount: number;
  priceMin: number | null;
  priceMax: number | null;
  currency: string;
  thumb: MediaItem | null;
}

export interface PriceView {
  amount: number;
  compareAt: number | null;
  currency: string;
}

export interface StockLevel {
  locationId: string;
  onHand: number;
  reserved: number;
}

export interface RelatedItem {
  id: string;
  title: string;
  status: string;
  thumb: MediaItem | null;
}

export interface ProductDetail extends ProductRow {
  pricing: Record<string, PriceView>;
  levels: Record<string, StockLevel[]>;
  locations: string[];
  mediaItems: Record<string, MediaItem>;
  relatedItems: Record<string, RelatedItem>;
}

export interface ProductBody {
  handle: string;
  title: string;
  description: string;
  status: string;
  publishAt?: string | null;
  brandId: string;
  brand?: string;
  categories: string[];
  type: string;
  tags: string[];
  options: Option[];
  variants: Variant[];
  attributes: Record<string, string>;
  seoTitle: string;
  seoDescription: string;
  media: string[];
  related: { similar: string[]; accessories: string[] };
  prices?: Record<string, { amount: number; compareAt?: number | null; currency?: string }>;
  stock?: Record<string, Record<string, number>>;
}

export interface Category {
  id: string;
  parentId: string;
  handle: string;
  name: string;
  description?: string;
  seoTitle?: string;
  seoDescription?: string;
  active: boolean;
  position: number;
  products: number;
  productsTotal: number;
  depth: number;
}

export interface Brand {
  id: string;
  handle: string;
  name: string;
  description?: string;
  logoMediaId?: string;
  active: boolean;
  products: number;
  logo: MediaItem | null;
}

export interface RuleSet {
  match: "all" | "any";
  brandIds?: string[];
  categoryIds?: string[];
  tags?: string[];
  priceMin?: number | null;
  priceMax?: number | null;
}

export interface Collection {
  id: string;
  handle: string;
  title: string;
  description?: string;
  kind: "manual" | "rules";
  productIds: string[] | null;
  rules: RuleSet;
  seoTitle?: string;
  seoDescription?: string;
  active: boolean;
  products?: number;
}

export type CollectionProduct = Pick<ProductRow, "id" | "title" | "handle" | "status" | "brand" | "priceMin" | "currency" | "stock" | "thumb">;

export interface CollectionDetail {
  collection: Collection;
  products: CollectionProduct[];
  total: number;
}

export interface Attribute {
  id: string;
  name: string;
  kind: "text" | "number" | "boolean" | "select";
  options?: string[];
  unit?: string;
  filterable: boolean;
  position: number;
  products: number;
}

export interface Term {
  name: string;
  products: number;
  managed: boolean;
}

export interface ImportError {
  row: number;
  column: string;
  code: string;
  message: string;
  handle?: string;
}

export interface ImportJob {
  id: string;
  filename: string;
  status: "preview" | "running" | "done" | "failed";
  summary: {
    rows: number;
    products: number;
    variants: number;
    creates: number;
    updates: number;
    invalid: number;
    columns: string[];
    warnings: string[];
    sample: { row: number; handle: string; title: string; variants: number; exists: boolean; invalid: boolean }[] | null;
    location: string;
  };
  errors: ImportError[];
  total: number;
  processed: number;
  created: number;
  updated: number;
  failed: number;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
}

export interface Summary {
  products: number;
  byStatus: Record<string, number>;
  outOfStock: number;
  lowStock: number;
  categories: number;
  brands: number;
  collections: number;
  attributes: number;
  media: number;
}

export interface ProductQuery {
  q?: string;
  status?: string;
  category?: string;
  brand?: string;
  stock?: string;
  sort?: string;
}

export function productParams(f: ProductQuery): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) if (v) p.set(k, v);
  return p.toString();
}

/** send posts a FormData body (uploads); errors become ApiError like every other call. */
async function send<T>(path: string, body: FormData | string, contentType?: string): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (contentType) headers["Content-Type"] = contentType;
  if (credentials.key) headers.Authorization = `Bearer ${credentials.key}`;
  const res = await fetch(path, { method: "POST", headers, credentials: "same-origin", body });
  if (!res.ok) {
    let parsed: Problem = { status: res.status, code: "http_" + res.status, title: res.statusText };
    try {
      const r = ProblemSchema.safeParse(await res.json());
      if (r.success) parsed = { ...r.data, status: r.data.status ?? res.status };
    } catch {
      /* non-JSON error body */
    }
    if (res.status === 401) window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
    throw new ApiError(parsed);
  }
  return res.json() as Promise<T>;
}

/** download fetches a file with the session and hands it to the browser. */
export async function download(path: string, fallbackName: string): Promise<void> {
  const headers: Record<string, string> = {};
  if (credentials.key) headers.Authorization = `Bearer ${credentials.key}`;
  const res = await fetch(path, { headers, credentials: "same-origin" });
  if (!res.ok) throw new ApiError({ status: res.status, code: "http_" + res.status, title: "No se pudo descargar el archivo" });
  const name = /filename="([^"]+)"/.exec(res.headers.get("Content-Disposition") ?? "")?.[1] ?? fallbackName;
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

const LIST_FIELDS =
  "id,title,handle,status,brand,brandId,categories,type,stock,variantCount,priceMin,priceMax,currency,thumb,updatedAt,publishAt,variants";

export const cat = {
  summary: () => request<Summary>("GET", "/v1/catalog/summary"),

  products: (f: ProductQuery, cursor: string, limit = 50) =>
    request<{ items: ProductRow[]; nextCursor: string; total: number; catalogTotal: number }>(
      "GET",
      `/v1/products?${productParams(f)}&limit=${limit}&cursor=${enc(cursor)}&fields=${LIST_FIELDS}`,
    ),
  product: (id: string) => request<ProductDetail>("GET", `/v1/products/${enc(id)}`),
  createProduct: (b: ProductBody) => request<{ id: string; version: number }>("POST", "/v1/products", b),
  saveProduct: (id: string, b: ProductBody, version: number) =>
    request<{ id: string; version: number }>("PUT", `/v1/products/${enc(id)}`, b, { headers: { "If-Match": `"${version}"` } }),
  deleteProduct: (id: string) => request<void>("DELETE", `/v1/products/${enc(id)}`, undefined, { as: "none" }),
  bulk: (b: { ids: string[]; action: string; status?: string; categoryId?: string; brandId?: string; percent?: number; keepOldPrice?: boolean }) =>
    request<{ selected: number; changed: number }>("POST", "/v1/products/bulk", b),
  generate: (skuBase: string, options: Option[], variants: Variant[]) =>
    request<{ options: Option[]; variants: Variant[]; created: string[]; removed: string[] }>("POST", "/v1/catalog/variants/generate", {
      skuBase,
      options,
      variants,
    }),

  categories: () => request<{ items: Category[] }>("GET", "/v1/catalog/categories").then((r) => r.items),
  createCategory: (c: Partial<Category>) => request<Category>("POST", "/v1/catalog/categories", c),
  updateCategory: (id: string, c: Partial<Category>) => request<Category>("PUT", `/v1/catalog/categories/${enc(id)}`, c),
  deleteCategory: (id: string) => request<void>("DELETE", `/v1/catalog/categories/${enc(id)}`, undefined, { as: "none" }),
  reorderCategories: (moves: { id: string; parentId: string; position: number }[]) =>
    request<void>("POST", "/v1/catalog/categories/reorder", { moves }, { as: "none" }),

  brands: () => request<{ items: Brand[] }>("GET", "/v1/catalog/brands").then((r) => r.items),
  createBrand: (b: Partial<Brand>) => request<Brand>("POST", "/v1/catalog/brands", b),
  updateBrand: (id: string, b: Partial<Brand>) => request<Brand>("PUT", `/v1/catalog/brands/${enc(id)}`, b),
  deleteBrand: (id: string) => request<void>("DELETE", `/v1/catalog/brands/${enc(id)}`, undefined, { as: "none" }),

  collections: () => request<{ items: Collection[] }>("GET", "/v1/catalog/collections").then((r) => r.items),
  collection: (id: string) => request<CollectionDetail>("GET", `/v1/catalog/collections/${enc(id)}`),
  previewCollection: (c: Partial<Collection>) => request<CollectionDetail>("POST", "/v1/catalog/collections/preview", c),
  createCollection: (c: Partial<Collection>) => request<CollectionDetail>("POST", "/v1/catalog/collections", c),
  updateCollection: (id: string, c: Partial<Collection>) => request<CollectionDetail>("PUT", `/v1/catalog/collections/${enc(id)}`, c),
  deleteCollection: (id: string) => request<void>("DELETE", `/v1/catalog/collections/${enc(id)}`, undefined, { as: "none" }),

  attributes: () => request<{ items: Attribute[] }>("GET", "/v1/catalog/attributes").then((r) => r.items),
  createAttribute: (a: Partial<Attribute>) => request<Attribute>("POST", "/v1/catalog/attributes", a),
  updateAttribute: (id: string, a: Partial<Attribute>) => request<Attribute>("PUT", `/v1/catalog/attributes/${enc(id)}`, a),
  deleteAttribute: (id: string) => request<void>("DELETE", `/v1/catalog/attributes/${enc(id)}`, undefined, { as: "none" }),

  terms: (kind: "types" | "tags") => request<{ items: Term[] }>("GET", `/v1/catalog/terms/${kind}`).then((r) => r.items),
  addTerm: (kind: "types" | "tags", name: string) => request<{ name: string }>("POST", `/v1/catalog/terms/${kind}`, { name }),
  deleteTerm: (kind: "types" | "tags", name: string) =>
    request<{ products: number }>("DELETE", `/v1/catalog/terms/${kind}?name=${enc(name)}`),

  media: (q: string, cursor: string, limit = 48) =>
    request<{ items: MediaItem[]; nextCursor: string; total: number }>("GET", `/v1/catalog/media?q=${enc(q)}&cursor=${enc(cursor)}&limit=${limit}`),
  mediaDetail: (id: string) =>
    request<{ media: MediaItem; usedBy: { products: string[] | null; brands: string[] | null } }>("GET", `/v1/catalog/media/${enc(id)}`),
  uploadMedia: (file: File, alt: string) => {
    const fd = new FormData();
    fd.append("alt", alt);
    fd.append("file", file);
    return send<{ media: MediaItem; duplicate: boolean }>("/v1/catalog/media", fd);
  },
  setAlt: (id: string, alt: string) => request<{ media: MediaItem }>("PATCH", `/v1/catalog/media/${enc(id)}`, { alt }),
  deleteMedia: (id: string, force: boolean) =>
    request<void>("DELETE", `/v1/catalog/media/${enc(id)}${force ? "?force=true" : ""}`, undefined, { as: "none" }),

  imports: () => request<{ items: ImportJob[] }>("GET", "/v1/catalog/imports").then((r) => r.items),
  importJob: (id: string) => request<ImportJob>("GET", `/v1/catalog/imports/${enc(id)}`),
  previewImport: (file: File) => {
    const fd = new FormData();
    fd.append("file", file);
    return send<ImportJob>("/v1/catalog/imports", fd);
  },
  applyImport: (id: string) => request<ImportJob>("POST", `/v1/catalog/imports/${enc(id)}/apply`),

  digitalFiles: (sku: string) => request<{ items: DigitalFile[] }>("GET", `/v1/digital/${enc(sku)}`).then((r) => r.items),
  uploadDigital: (sku: string, file: File, maxDownloads: number, linkDays: number) => {
    const fd = new FormData();
    fd.append("file", file);
    fd.append("maxDownloads", String(maxDownloads));
    fd.append("linkDays", String(linkDays));
    return send<DigitalFile>(`/v1/digital/${enc(sku)}`, fd);
  },
  updateDigital: (f: DigitalFile) =>
    request<DigitalFile>("PATCH", `/v1/digital/${enc(f.sku)}/${enc(f.id)}`, { filename: f.filename, maxDownloads: f.maxDownloads, linkDays: f.linkDays }),
  deleteDigital: (sku: string, id: string) => request<void>("DELETE", `/v1/digital/${enc(sku)}/${enc(id)}`, undefined, { as: "none" }),
};

export interface DigitalFile { id: string; sku: string; filename: string; contentType: string; size: number; maxDownloads: number; linkDays: number; createdAt: string }
