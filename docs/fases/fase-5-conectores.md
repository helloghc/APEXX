# Fase 5 — Conectores: eventos firmados, reintentos y repetición

Estado: **infraestructura de conectores completa y verificada**; los conectores concretos (Mercado Libre, Google, Meta, Facturapi...) se construyen encima.

Diseño basado en `docs/audit/conectores.md` (análisis con REA y código abierto de VTEX). La causa raíz del desfase en los conectores de VTEX es que reciben "cambió el SKU X" y tienen que volver a leer un índice atrasado. En Apexx el evento **lleva el estado actual completo** del recurso.

## Contrato

- Formato **CloudEvents 1.0** con `apexxtenant` y `apexxsequence` (posición en el outbox, sirve para repetir).
- Tipos: `apexx.product.updated`, `apexx.price.updated`, `apexx.inventory.level.updated`, `apexx.promotion.updated`, `apexx.channel.updated`, `apexx.order.created|paid|fulfilled|refunded|cancelled`, `apexx.payment.failed`, `apexx.site.published`. Filtro con comodín (`apexx.order.*`).
- Encabezados: `Apexx-Event-Id`, `Apexx-Delivery-Attempt`, `Apexx-Signature: t=<unix>,v1=HMAC-SHA256(secreto, "<t>.<cuerpo>")`.
- 2xx = entregado. 5xx, 429 o error de red = reintento con espera exponencial y variación aleatoria durante 24 h. Otro 4xx = cola de fallidos.
- URLs solo `https` y públicas: se bloquean IPs privadas, loopback, link-local y el servidor de metadatos, también al conectar (protege contra DNS rebinding).

## API

| Método | Ruta | Permiso |
| --- | --- | --- |
| GET | `/v1/events?after=&limit=&types=` | configuración: lector |
| GET/POST | `/v1/webhooks` | lector / admin |
| DELETE | `/v1/webhooks/{id}` | admin |
| GET | `/v1/webhooks/{id}/deliveries` | lector |
| POST | `/v1/webhooks/{id}/replay?after=` | admin |

## Evidencia

| Prueba | Resultado |
| --- | --- |
| Firma HMAC verificada por el receptor | válida |
| Receptor responde 500 y luego 200 | reintento automático y entrega |
| Repetir desde el inicio | 1 022 eventos reencolados |
| Dos nodos, 50 cambios de precio | 50 entregados, 0 duplicados, 0 firmas inválidas |
| Feed por cursor | eventos filtrados por tipo con `next` |

## Prioridad de conectores para México

Stripe (hecho), Facturapi (CFDI 4.0), Mercado Libre, Google Merchant Center, Meta (catálogo + Conversions API), GA4/GTM desde servidor, Envia.com / Skydropx, WhatsApp Business, Klaviyo, ERP (Odoo, NetSuite, SAP B1).
