# Plan de fases pendientes (trabajo directo, sin agentes)

Regla: una fase a la vez; cada fase incluye backend, admin (shadcn/ui), tienda si aplica, pruebas, capturas y subida a git.
Avances parciales de agentes detenidos: `.claude/worktrees/agent-*` (local, no está en git). Mapa de paneles: `docs/vtex-paneles.md`.

| Fase | Contenido | Estado |
| --- | --- | --- |
| A | Catálogo completo: categorías, marcas, colecciones, variantes, atributos, medios, CSV, filtros en tienda | **Hecha**: backend, admin (productos, editor con variantes, categorías, colecciones, marcas, atributos, medios, importar/exportar), filtros y páginas de categoría/colección en tienda |
| B | Promociones, cupones, campañas, listas de precios, simulador | **Hecha**: motor con 9 tipos y explicación, campañas, cupones (uno y en lote), listas de precios, cambio masivo con vista previa, historial, simulador, cupón en el carrito |
| C | Clientes, cuentas de comprador, envíos con guía, reembolsos parciales, devoluciones, edición de pedidos | Pendiente (parcial en abc54900c45f7ea4e) |
| D | Correos transaccionales, bitácora, carritos abandonados, newsletter | Pendiente |
| E | Almacenes, zonas y tarifas de envío, transportistas, retiro, impuestos | Pendiente (parcial en a50473b7393f14fc8) |
| F | Configuración de tienda, políticas, menús, redirecciones, sitemap, dominios | Pendiente |
| G | Panel de inicio, embudo, reportes, registro de actividad | Pendiente (parcial en ad803f007f615fa88) |
| H | Búsqueda (sinónimos, merchandising, autocompletar), reseñas, listas de deseos | Pendiente (a47cc77522d03c7ac) |
| I | Tarjetas de regalo, saldo a favor, suscripciones, MSI, antifraude | Pendiente (adf27b287ce6404eb) |
| J | 2FA, sesiones, roles, invitaciones, idiomas, multi-tienda, feeds | Pendiente (ab055a356f0a6ca3d) |
| K | Marketplace y portal de vendedores | Pendiente (afe5348adcea826bb) |
| L | B2B y entidades personalizadas | Pendiente (a64632a04f566059b) |
| Cierre | Auditorías completas (flujos, reversión, uso humano comprador y plataforma, resiliencia, seguridad) | Pendiente |

Entorno local: Postgres en 127.0.0.1:5432 (arrancar con pg_ctl -D /var/lib/apexx-pg/data), variables en el scratchpad `dev.env`, tienda de QA con `apexx qa-setup`.
