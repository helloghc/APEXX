# Fase 3 — Builder visual y storefront

Estado: **editor funcional y persistente**; faltan pantallas de administración fuera del builder (catálogo, pedidos, equipo).

## Qué hay

| Pieza | Dónde | Detalle |
| --- | --- | --- |
| Núcleo del builder | `internal/builder` | Plantillas → secciones → bloques; ajustes validados con esquema; edición por parches JSON (RFC 6902); presupuestos de rendimiento por página que **bloquean** la publicación |
| Publicación atómica | `Site.Publish` / `Rollback` | Revisión inmutable con checksum, HTML generado al publicar, rollback que solo mueve un puntero |
| Persistencia | `internal/store/sites.go`, migración 003 | Borradores, revisiones y publicaciones sobreviven reinicios; si guardar falla, no se publica |
| Editor visual | `web/` (React 19 + Vite + dnd-kit), servido en `/admin/` | Arrastrar y soltar (también con teclado), vista previa real, formulario generado desde el esquema, semáforo de presupuesto, deshacer/rehacer, conflicto de edición concurrente |
| Storefront | `GET /s/{tienda}/{sitio}/{página}` | HTML estático por revisión, `ETag` = revisión, caché de borde de 1 año con purga por `Surrogate-Key`, CSP estricta |

## Evidencia

| Medición | Resultado |
| --- | --- |
| Mover un módulo + publicar (en memoria) | 0,69 ms |
| Publicar con persistencia en Postgres | 2,6 ms |
| Primer byte de la home publicada | 0,4 ms local; 744 bytes comprimida |
| Sección que rompe el presupuesto de JS | publicación bloqueada (422) con el motivo |
| Reinicio del servidor | la página publicada es idéntica byte a byte |
| Editor | 109,5 KB JS gzip; 17 pruebas Vitest, 4 de Go, 1 E2E Playwright (arrastrar, publicar, deshacer) |

## Pendiente

- Componentes de sección en TSX compilados a HTML al publicar (hoy las secciones son plantillas Go).
- Islas Preact para carrito y variantes; checkout propio con Stripe Payment Element.
- Pantallas de admin: catálogo, inventario, precios, pedidos, equipo, pagos, conectores.
- Imágenes con imgproxy (AVIF/WebP, `srcset`) y dominios propios con TLS automático.
