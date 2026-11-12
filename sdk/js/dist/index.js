/**
 * Apexx SDK: a typed client for a store's public API (catalog through
 * GraphQL, cart pricing and guest checkout) and for the management API with
 * an app key. No dependencies; works in browsers, Node 18+, Deno and Bun.
 */
/** ApexxError carries the API's problem (code and message in Spanish). */
export class ApexxError extends Error {
    constructor(status, code, message, details) {
        super(message);
        this.status = status;
        this.code = code;
        this.details = details;
    }
}
const PRODUCT_FIELDS = `id handle title description brand url available price { amount currency formatted }
  images { url alt } variants { sku title available preorder price { amount currency formatted } compareAtPrice { amount currency formatted } }`;
const MONEY = "{ amount currency formatted }";
async function call(fetcher, url, init) {
    const res = await fetcher(url, init);
    const text = await res.text();
    const body = text ? JSON.parse(text) : null;
    if (!res.ok)
        throw new ApexxError(res.status, body?.code ?? "http_" + res.status, body?.detail ?? body?.title ?? res.statusText, body);
    return body;
}
/** Storefront: the public API of one store, as a guest. */
export function createStorefront(opts) {
    const f = opts.fetch ?? fetch.bind(globalThis);
    const api = `${opts.baseUrl.replace(/\/$/, "")}/s/${encodeURIComponent(opts.store)}/api`;
    async function graphql(query, variables) {
        const r = await call(f, api + "/graphql", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ query, variables }) });
        if (r.errors?.length)
            throw new ApexxError(400, "graphql", r.errors.map((e) => e.message).join("; "), r.errors);
        return r.data;
    }
    return {
        graphql,
        /** One product by its handle, or null. */
        product: async (handle) => (await graphql(`query($h:String!){ product(handle:$h){ ${PRODUCT_FIELDS} } }`, { h: handle })).product,
        /** A page of products (up to 50), optionally matching a search. */
        products: async (o = {}) => (await graphql(`query($f:Int,$a:String,$q:String){ products(first:$f, after:$a, query:$q){ nodes { ${PRODUCT_FIELDS} } endCursor hasNextPage } }`, { f: o.first ?? 20, a: o.after, q: o.query })).products,
        /** Every product, page by page. */
        async *allProducts(query) {
            let after;
            for (;;) {
                const page = await this.products({ first: 50, after, query });
                yield* page.nodes;
                if (!page.hasNextPage || !page.endCursor)
                    return;
                after = page.endCursor;
            }
        },
        categories: async () => (await graphql(`{ categories { id handle name parentId } }`)).categories,
        /** Prices a cart exactly like the store's checkout will. */
        cart: async (items, postalCode) => (await graphql(`query($i:[CartItemInput!]!,$p:String){ cart(items:$i, postalCode:$p){ lines { sku title qty unit ${MONEY} total ${MONEY} } items ${MONEY} discounts ${MONEY} shipping ${MONEY} total ${MONEY} payLater ${MONEY} messages } }`, { i: items, p: postalCode })).cart,
        /** Places a guest order. Repeat with the same idempotencyKey: it never charges twice. */
        checkout: (o) => call(f, api + "/checkout", { method: "POST", headers: { "Content-Type": "application/json", "Idempotency-Key": o.idempotencyKey },
            body: JSON.stringify({ channel: o.channel ?? "mx", items: o.items, postalCode: o.postalCode, customer: o.customer, paymentMethod: o.paymentMethod, acceptedTerms: o.acceptedTerms, blocks: o.blocks }) }),
    };
}
/** Admin: the management API with an app key (apa_...) or API key. Never use it in a browser. */
export function createAdmin(opts) {
    const f = opts.fetch ?? fetch.bind(globalThis);
    const base = opts.baseUrl.replace(/\/$/, "");
    const request = (method, path, body, headers = {}) => call(f, base + path, { method, headers: { Authorization: "Bearer " + opts.token, ...(body !== undefined ? { "Content-Type": "application/json" } : {}), ...headers }, body: body === undefined ? undefined : JSON.stringify(body) });
    return {
        request,
        /** Who this key is and what it may do. */
        me: () => request("GET", "/v1/apps/me"),
        products: { list: (limit = 50, cursor = "") => request("GET", `/v1/products?limit=${limit}&cursor=${encodeURIComponent(cursor)}`) },
        orders: { get: (id) => request("GET", `/v1/orders/${encodeURIComponent(id)}`) },
        inventory: { set: (sku, location, onHand) => request("PUT", `/v1/inventory/${encodeURIComponent(sku)}/${encodeURIComponent(location)}`, { onHand }) },
        bulk: {
            stock: (items) => request("POST", "/v1/bulk-operations", { kind: "stock", items }),
            prices: (channel, items) => request("POST", "/v1/bulk-operations", { kind: "prices", channel, items }),
            get: (id) => request("GET", `/v1/bulk-operations/${encodeURIComponent(id)}`),
            /** Waits until a bulk job finishes. */
            async wait(id, everyMs = 1000) {
                for (;;) {
                    const j = await this.get(id);
                    if (j.status === "done" || j.status === "failed")
                        return j;
                    await new Promise((r) => setTimeout(r, everyMs));
                }
            },
        },
    };
}
/** Verifies a session an app's panel page or order block received (?session=...). */
export async function verifyExtensionSession(session, secret) {
    const [body, sig] = session.split(".");
    if (!body || !sig)
        return null;
    const enc = new TextEncoder();
    const key = await crypto.subtle.importKey("raw", enc.encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
    const mac = new Uint8Array(await crypto.subtle.sign("HMAC", key, enc.encode(body)));
    const hex = Array.from(mac, (b) => b.toString(16).padStart(2, "0")).join("");
    if (hex.length !== sig.length || [...hex].reduce((d, c, i) => d | (c.charCodeAt(0) ^ sig.charCodeAt(i)), 0) !== 0)
        return null;
    const claims = JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(body.replace(/-/g, "+").replace(/_/g, "/")), (c) => c.charCodeAt(0))));
    return claims.exp * 1000 > Date.now() ? claims : null;
}
