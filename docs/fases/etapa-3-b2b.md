# Etapa 3 — B2B profundo (noviembre 2026)

Estado: **hecha**. Todo con pruebas automáticas y recorrido en navegador (panel en escritorio, tienda en celular).

| Función | Dónde | Pruebas |
|---|---|---|
| Reglas de cantidad (mínimo, múltiplo, máximo) por producto y grupo | ficha del producto en el panel | `qtyrules` |
| Precios por volumen en listas de precios | Promociones › Listas | `pricelists` |
| Orden de compra obligatoria, solo crédito, exención de impuestos | empresa | `b2b_integration_test` |
| Cotizaciones (pedir, ofertar, aceptar, vencer) | Pedidos › Cotizaciones, enlace del comprador | `quotes` |
| Pedido rápido por SKU y listas de recompra | `/pedido-rapido`, Mi cuenta › Mis listas | `reorder_integration_test` |
| Aprobaciones por niveles (1 a 5) con reglas por monto, categoría y sucursal; un enlace por nivel; suplentes; vencimiento; cancelación | empresa › reglas y compradores; Mi empresa | `TestB2BLevelsCapsBranchesAndPortal`, `TestApprovalNeeded` |
| Tope mensual a crédito por comprador y por sucursal | compradores, sucursales | idem |
| Sucursales con lista de precios, tope y dirección | empresa › sucursales | idem |
| Portal de la empresa: su mes, sus solicitudes; el administrador gestiona compradores | tienda › Mi cuenta › Mi empresa | idem |
| Catálogo negociado por empresa y precio de empresa en la tienda | empresa › “Solo ve su catálogo negociado” | `TestCompanyRestrictedCatalog` |
| Pago automático del crédito con tarjeta y avisos de vencimiento | Mi empresa › Pago automático; empresa › Pago automático y avisos | `TestCompanyAutopay` |

## Auditorías

- **Funcional y humana**: recorridos `qa/levels.js`, `qa/catalog.js`, `qa/autopay.js` (scratchpad). Encontraron y se corrigieron:
  una regla de nivel 2 se podía guardar sin quien apruebe ese nivel; la compra en espera dejaba el carrito lleno y lo mostraba como error;
  “Mi empresa” se desbordaba 61 px en celular; tras aprobar el nivel 1 la página de aprobación se rompía; las tarjetas ocultas seguían visibles por el CSS del tema;
  el monto de la regla salía sin separador de miles.
- **Seguridad y aislamiento**: tablas nuevas con RLS forzado; otra tienda recibe 404; el portal exige sesión, mismo origen y rol de administrador;
  el administrador no puede quitarse a sí mismo; los correos de compradores van cifrados; Apexx solo guarda tokens de la tarjeta.
- **Reversión**: rechazar en cualquier nivel no coloca pedido ni toma inventario; cancelar y vencer cierran la solicitud; apagar el catálogo negociado o el pago automático restablece todo.
- **Fallas**: sin aprobador la compra no queda esperando a nadie (409); tres rechazos apagan el cobro y avisan; una caída del proveedor se reintenta en la siguiente ronda.
- **Concurrencia**: tres aprobaciones simultáneas del último nivel colocan un solo pedido; la llave de idempotencia del cobro depende de la deuda y de los intentos.
- **Libro contable**: todas las transacciones siguen cuadradas después de cobros automáticos.

Pendiente externo: probar el cobro automático con Stripe en vivo (el adaptador usa clientes y cargos fuera de sesión en la cuenta conectada).
