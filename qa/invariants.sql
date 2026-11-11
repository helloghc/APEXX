select 'asientos descuadrados', count(*) from (select tx_id from ledger_entries where tenant_id=:'tenant' group by tx_id having sum(amount)<>0) x
union all select 'pedidos pagados sin pago registrado', count(*) from orders o where tenant_id=:'tenant' and status='paid' and not exists (select 1 from payments p where p.tenant_id=o.tenant_id and p.order_id=o.id and p.status in ('captured','refunded'))
union all select 'pagos que no suman el total del pedido', count(*) from orders o where tenant_id=:'tenant' and status='paid' and source='checkout' and grand_total <> (select coalesce(sum(amount),0) from payments p where p.tenant_id=o.tenant_id and p.order_id=o.id and p.status in ('captured','refunded'))
union all select 'pedidos con dos cobros', count(*) from (select order_id from payments where tenant_id=:'tenant' and status='captured' and provider='sandbox' group by order_id having count(*)>1) x
union all select 'reservas colgadas de pedidos ya resueltos', count(*) from reservations r join orders o on o.tenant_id=r.tenant_id and o.id=r.holder where r.tenant_id=:'tenant' and o.status<>'pending_payment'
union all select 'tarjetas de regalo con saldo distinto a sus movimientos', count(*) from gift_cards g where tenant_id=:'tenant' and balance <> (select coalesce(sum(amount),0) from gift_card_tx t where t.tenant_id=g.tenant_id and t.card_id=g.id)
union all select 'existencias negativas', count(*) from stock_levels where tenant_id=:'tenant' and on_hand < 0
union all select 'pedidos pendientes de pago', count(*) from orders where tenant_id=:'tenant' and status='pending_payment';
