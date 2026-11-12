/**
 * Apexx SDK: a typed client for a store's public API (catalog through
 * GraphQL, cart pricing and guest checkout) and for the management API with
 * an app key. No dependencies; works in browsers, Node 18+, Deno and Bun.
 */
export interface Money {
    amount: number;
    currency: string;
    formatted: string;
}
export interface Variant {
    sku: string;
    title: string;
    available: number;
    price: Money | null;
    compareAtPrice: Money | null;
    preorder: boolean;
}
export interface Product {
    id: string;
    handle: string;
    title: string;
    description: string;
    brand: string;
    url: string;
    available: boolean;
    price: Money | null;
    images: {
        url: string;
        alt: string;
    }[];
    variants: Variant[];
}
export interface ProductPage {
    nodes: Product[];
    endCursor: string | null;
    hasNextPage: boolean;
}
export interface Category {
    id: string;
    handle: string;
    name: string;
    parentId: string;
}
export interface CartLine {
    sku: string;
    title: string;
    qty: number;
    unit: Money;
    total: Money;
}
export interface Cart {
    lines: CartLine[];
    items: Money;
    discounts: Money;
    shipping: Money;
    total: Money;
    payLater: Money;
    messages: string[];
}
export interface CartItem {
    sku: string;
    qty: number;
}
/** ApexxError carries the API's problem (code and message in Spanish). */
export declare class ApexxError extends Error {
    status: number;
    code: string;
    details?: unknown | undefined;
    constructor(status: number, code: string, message: string, details?: unknown | undefined);
}
export interface StorefrontOptions {
    baseUrl: string;
    store: string;
    fetch?: typeof fetch;
}
/** Storefront: the public API of one store, as a guest. */
export declare function createStorefront(opts: StorefrontOptions): {
    graphql: <T>(query: string, variables?: Record<string, unknown>) => Promise<T>;
    /** One product by its handle, or null. */
    product: (handle: string) => Promise<Product | null>;
    /** A page of products (up to 50), optionally matching a search. */
    products: (o?: {
        first?: number;
        after?: string;
        query?: string;
    }) => Promise<ProductPage>;
    /** Every product, page by page. */
    allProducts(query?: string): AsyncGenerator<Product, void, unknown>;
    categories: () => Promise<Category[]>;
    /** Prices a cart exactly like the store's checkout will. */
    cart: (items: CartItem[], postalCode?: string) => Promise<Cart>;
    /** Places a guest order. Repeat with the same idempotencyKey: it never charges twice. */
    checkout: (o: {
        items: CartItem[];
        postalCode: string;
        customer: {
            email: string;
            name: string;
            phone?: string;
            address?: string;
        };
        paymentMethod: string;
        idempotencyKey: string;
        channel?: string;
        acceptedTerms?: boolean;
        blocks?: Record<string, string>;
    }) => Promise<Record<string, unknown>>;
};
export interface AdminOptions {
    baseUrl: string;
    token: string;
    fetch?: typeof fetch;
}
/** Admin: the management API with an app key (apa_...) or API key. Never use it in a browser. */
export declare function createAdmin(opts: AdminOptions): {
    request: <T>(method: string, path: string, body?: unknown, headers?: Record<string, string>) => Promise<T>;
    /** Who this key is and what it may do. */
    me: () => Promise<{
        kind: string;
        id: string;
        name: string;
        store: string;
        permissions: Record<string, string>;
    }>;
    products: {
        list: (limit?: number, cursor?: string) => Promise<{
            items: unknown[];
            nextCursor?: string;
        }>;
    };
    orders: {
        get: (id: string) => Promise<Record<string, unknown>>;
    };
    inventory: {
        set: (sku: string, location: string, onHand: number) => Promise<void>;
    };
    bulk: {
        stock: (items: {
            sku: string;
            location: string;
            onHand: number;
        }[]) => Promise<{
            id: string;
            status: string;
        }>;
        prices: (channel: string, items: {
            sku: string;
            amount: number;
            compareAt?: number;
        }[]) => Promise<{
            id: string;
            status: string;
        }>;
        get: (id: string) => Promise<{
            id: string;
            status: string;
            total: number;
            processed: number;
            failed: number;
            errors: {
                line: number;
                message: string;
            }[];
        }>;
        /** Waits until a bulk job finishes. */
        wait(id: string, everyMs?: number): Promise<{
            id: string;
            status: string;
            total: number;
            processed: number;
            failed: number;
            errors: {
                line: number;
                message: string;
            }[];
        }>;
    };
};
/** Verifies a session an app's panel page or order block received (?session=...). */
export declare function verifyExtensionSession(session: string, secret: string): Promise<{
    store: string;
    app: string;
    member: string;
    order: string;
    exp: number;
} | null>;
