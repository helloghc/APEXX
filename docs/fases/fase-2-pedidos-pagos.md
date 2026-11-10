# Fase 2 — Pedidos, pagos con Stripe y reversión

Estado: **completa y verificada con el proveedor sandbox**. Falta probar contra Stripe en modo test con llaves reales de una tienda.

## Flujo de compra

```
POST /v1/checkout  (Idempotency-Key obligatorio)
  1. Puerta de idempotencia: misma llave + mismo contenido → misma respuesta; otro contenido → 422
  2. Una transacción: reservar stock (filas bloqueadas, sin sobreventa) + crear pedido pending_payment
  3. Cobro en Stripe, idempotente por id de pedido ("charge-<orden>")
  4a. Aprobado → una transacción: consumir reserva, registrar ubicaciones, pagos, libro mayor, factura en cola
  4b. Rechazado → una transacción: liberar reserva, pedido cancelado
  4c. Resultado desconocido (red, Stripe caído) → pedido pendiente; el reconciliador pregunta a Stripe y lo cierra
```

## Stripe por tienda

Cada tienda conecta **su propia cuenta**:

| Modo | Cómo | Qué guarda Apexx |
| --- | --- | --- |
| `connect` (recomendado) | Stripe Connect; el comerciante autoriza su cuenta | solo `acct_…` |
| `own_keys` | el comerciante ingresa su llave `sk_…` o restringida `rk_…` | la llave **cifrada con la llave de su tienda** |

- Los datos de tarjeta nunca llegan a Apexx (Stripe.js / Payment Element) → PCI DSS SAQ A.
- Webhook de Stripe: `POST /v1/stripe/webhook/{tienda}`, firma verificada (tolerancia 5 min).
- Cortacircuitos por tienda: 5 fallas seguidas abren el circuito 30 s.
- **Facturas:** Stripe Invoicing emite la factura del pedido pagado (marcada `paid_out_of_band`) con reintentos exponenciales. **En México una factura de Stripe no es CFDI**: el CFDI 4.0 se emite con un PAC (Facturapi) como conector; ver `docs/audit/conectores.md`.

## Cancelación y reembolso

`POST /v1/orders/{id}/cancel` revierte todo:
1. Reembolso en Stripe (idempotente: "refund-<orden>"). Si falla, el pedido no cambia.
2. Una transacción: reponer el stock **en la ubicación exacta** de donde salió, marcar pago reembolsado, asientos contables inversos, anular factura pendiente, evento `order.refunded`.

## Evidencia

| Escenario | Resultado |
| --- | --- |
| Compra aprobada | 201, pedido `paid`, stock 12 → 9 |
| Misma Idempotency-Key | mismo pedido, el proveedor no recibe un segundo cobro |
| Misma llave con otro contenido | 422 `idempotency_key_reused` |
| Tarjeta rechazada (`pm_card_chargeDeclined`) | 402, pedido `cancelled`, stock intacto |
| Pedir más de lo disponible | 409 `insufficient_stock` |
| Stripe caído durante el cobro | 502 `payment_pending`; el reconciliador cierra el pedido y libera la reserva |
| Cancelar un pedido pagado | `refunded`, stock 9 → 12, reembolso total |
| Cancelar dos veces | 409 |
| Libro mayor | todas las transacciones suman 0 |
| Datos del cliente en la base | cifrados; el correo no aparece en texto plano |
| Lector de pedidos | ve el pedido **sin** datos personales |

Pruebas automáticas: `TestCheckoutLifecycleAndReversion`, `TestPendingPaymentIsReconciled` (`internal/api/integration_test.go`) y `internal/payments/stripe_test.go`.

## Pendiente

- Prueba contra Stripe en modo test con una cuenta real (requiere llaves de prueba de una tienda).
- Onboarding de Stripe Connect (Account Links) desde el admin.
- Reembolsos parciales y devoluciones por línea.
- Conector CFDI (Facturapi).
