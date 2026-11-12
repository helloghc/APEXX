# @apexx/sdk

Cliente oficial de Apexx, sin dependencias (navegador, Node 18+, Deno, Bun).

```js
import { createStorefront, createAdmin, verifyExtensionSession } from "@apexx/sdk";

// Tienda (pública, como invitado): catálogo por GraphQL, carrito con precios reales y pago.
const shop = createStorefront({ baseUrl: "https://tu-dominio", store: "mi-tienda" });
const { nodes } = await shop.products({ first: 12, query: "playera" });
const cart = await shop.cart([{ sku: nodes[0].variants[0].sku, qty: 1 }], "06600");

// Gestión (solo en servidor) con la llave de una app instalada en Configuración › Apps.
const admin = createAdmin({ baseUrl: "https://tu-dominio", token: process.env.APEXX_APP_KEY });
const job = await admin.bulk.stock([{ sku: "SKU-1", location: "principal", onHand: 25 }]);
await admin.bulk.wait(job.id);

// En la página de tu app dentro del panel: verifica la sesión que recibes.
const claims = await verifyExtensionSession(new URLSearchParams(location.search).get("session"), SECRETO);
```

- Construir: `npm run build` (TypeScript a `dist/`).
- Pruebas: `APEXX_SDK_URL=http://127.0.0.1:8097 APEXX_SDK_STORE=mi-tienda npm test` (o `node --test test/*.test.mjs`).
- Tienda de ejemplo: abre `example/index.html?base=https://tu-dominio&store=mi-tienda` desde cualquier dominio.

Límites del API de la tienda: consultas de hasta 16 KB y profundidad 7, páginas de hasta 50, carritos de hasta 50 productos.
