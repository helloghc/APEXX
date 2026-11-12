import type { QueryClient } from "@tanstack/react-query";
import { request } from "@/api";
import { cat } from "./api";

// The product editor's queries, defined once so the editor and the catalog
// list can start them ahead of time with the same keys.
//
// Without this the editor fetched the product, drew it, and only then its
// sections asked for their data: two round trips one after the other (about
// 280 ms on an 80 ms network). The sections that only need the id now start
// with the product, and the ones per variant as soon as it arrives.

const enc = encodeURIComponent;
type Any = unknown;

export const pq = {
  product: (id: string) => ({ queryKey: ["product", id], queryFn: () => cat.product(id) }),
  translations: <T>(id: string) => ({ queryKey: ["product", id, "translations"], queryFn: () => request<T>("GET", `/v1/products/${enc(id)}/translations`) }),
  customizations: <T>(id: string) => ({ queryKey: ["product", id, "customs"], queryFn: () => request<T>("GET", `/v1/products/${enc(id)}/customizations`) }),
  qtyRules: <T>(id: string) => ({ queryKey: ["product", id, "qty-rules"], queryFn: () => request<T>("GET", `/v1/products/${enc(id)}/quantity-rules`) }),
  tradeCustoms: <T>(id: string) => ({ queryKey: ["product", id, "trade-customs"], queryFn: () => request<T>("GET", `/v1/product-classes/${enc(id)}`) }),
  datasheet: <T>(id: string) => ({ queryKey: ["datasheet", id], queryFn: () => request<{ datasheet: T | null }>("GET", `/v1/products/${enc(id)}/datasheet`).then((r) => r.datasheet) }),
  suggestions: <T>(id: string) => ({ queryKey: ["suggestions", id], queryFn: () => request<{ items: T[] }>("GET", `/v1/products/${enc(id)}/suggestions`).then((r) => r.items) }),
  preorder: <T>(sku: string) => ({ queryKey: ["preorder", sku], queryFn: () => request<T>("GET", `/v1/preorders/${enc(sku)}`), enabled: !!sku }),
  digital: (sku: string) => ({ queryKey: ["digital", sku], queryFn: () => cat.digitalFiles(sku), enabled: !!sku }),
  bundle: <T>(sku: string) => ({ queryKey: ["bundle", sku], queryFn: () => request<T>("GET", `/v1/bundles/${enc(sku)}`), enabled: !!sku }),
};

/** prefetchProduct starts everything the editor of id shows; data already
 *  in the cache and fresh is not asked for again. */
export function prefetchProduct(qc: QueryClient, id: string, opts: { variants?: boolean; sections?: boolean } = {}) {
  const { variants = true, sections = true } = opts;
  const p = qc.fetchQuery({ ...pq.product(id), staleTime: 10_000 }).catch(() => null);
  if (sections) {
    for (const o of [pq.translations<Any>(id), pq.customizations<Any>(id), pq.qtyRules<Any>(id), pq.tradeCustoms<Any>(id), pq.datasheet<Any>(id), pq.suggestions<Any>(id)]) {
      void qc.prefetchQuery({ ...o, staleTime: 10_000 });
    }
  }
  if (variants) {
    void p.then((d) => {
      const sku = (d?.variants ?? []).map((v) => v.sku).filter(Boolean)[0];
      if (!sku) return;
      for (const o of [pq.preorder<Any>(sku), pq.digital(sku), pq.bundle<Any>(sku)]) void qc.prefetchQuery({ ...o, staleTime: 10_000 });
    });
  }
}

/** prefetchOnIntent returns handlers for a link to a product: when the
 *  pointer rests on it (80 ms, so sweeping across a list asks for nothing)
 *  or it gets keyboard focus, the editor's data starts loading. */
export function prefetchOnIntent(qc: QueryClient, id: string) {
  let t = 0;
  const go = () => prefetchProduct(qc, id);
  return {
    onPointerEnter: () => { t = window.setTimeout(go, 80); },
    onPointerLeave: () => window.clearTimeout(t),
    onFocus: go,
  };
}
