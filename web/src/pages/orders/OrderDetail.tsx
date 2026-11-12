import { CustomFields } from "@/components/CustomFields";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { ArrowLeft, Ban, ExternalLink, PackageCheck, PencilLine, Plus, RotateCcw, StickyNote, Trash2, Truck, Undo2 } from "lucide-react";
import { errorMessage, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { centsToPesos, formatMoney, parsePesos } from "@/money";
import { can } from "@/perms";
import { href } from "@/router";
import { sale, type OTotals, type Order, type Quote } from "@/saleApi";
import { dateTime, ErrorBox, Notice, StatusPill } from "../common";

export const REASONS: Record<string, string> = { size: "Talla o medida", defective: "Defectuoso o dañado", not_as_described: "No es como se describe", wrong_item: "Producto equivocado", changed_mind: "Cambió de opinión", arrived_late: "Llegó tarde", other: "Otro" };
const EVENT: Record<string, string> = {
  "order.created": "Pedido creado", "payment.captured": "Pago cobrado", "payment.failed": "Pago rechazado", "order.fulfilled": "Pedido enviado", "order.refunded": "Pedido reembolsado",
  "order.cancelled": "Pedido cancelado", "invoice.issued": "Factura emitida", "fulfillment.created": "Envío creado", "fulfillment.shipped": "Paquete en camino", "fulfillment.delivered": "Paquete entregado",
  "refund.created": "Reembolso", "refund.succeeded": "Reembolso completado", "return.requested": "Devolución solicitada", "return.received": "Devolución recibida", "return.completed": "Devolución completada",
  "order.edited": "Pedido editado", "note.added": "Nota agregada",
};
const CARRIERS = ["Estafeta", "DHL", "FedEx", "Paquetexpress", "99 Minutos", "Redpack", "UPS", "Otro"];

type Qty = Record<number, number>;

function QtyPicker({ lines, max, value, onChange }: { lines: Order["lines"]; max: (l: NonNullable<Order["lines"]>[number]) => number; value: Qty; onChange: (v: Qty) => void }) {
  const rows = (lines ?? []).filter((l) => max(l) > 0);
  if (!rows.length) return <p className="text-sm text-muted-foreground">No quedan piezas disponibles para esta acción.</p>;
  return (
    <ul className="divide-y rounded-md border">
      {rows.map((l) => (
        <li key={l.line} className="flex items-center gap-3 px-3 py-2 text-sm">
          <span className="min-w-0 flex-1"><span className="block truncate font-medium">{l.title}</span><span className="font-mono text-xs text-muted-foreground">{l.sku}</span></span>
          <Input aria-label={`Cantidad de ${l.title}`} className="w-20" inputMode="numeric" value={String(value[l.line] ?? 0)}
            onChange={(e) => onChange({ ...value, [l.line]: Math.max(0, Math.min(max(l), Number(e.target.value.replace(/\D/g, "")) || 0)) })} />
          <span className="w-12 text-muted-foreground">de {max(l)}</span>
        </li>
      ))}
    </ul>
  );
}
const picked = (q: Qty) => Object.entries(q).filter(([, n]) => n > 0).map(([line, qty]) => ({ line: Number(line), qty }));

export function OrderDetail({ me, id }: { me: Me; id: string }) {
  const qc = useQueryClient();
  const canEdit = can(me, "orders", "edit");
  const q = useQuery({ queryKey: ["order", id], queryFn: () => sale.order(id) });
  const [dialog, setDialog] = useState<null | "ship" | "refund" | "return" | "cancel" | "edit">(null);
  const [note, setNote] = useState("");
  const [noteVis, setNoteVis] = useState<"internal" | "customer">("internal");
  const o = q.data;
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["order", id] });
    void qc.invalidateQueries({ queryKey: ["orders"] });
  };
  const act = (p: Promise<unknown>, ok: string) => p.then(() => { toast.success(ok); refresh(); }).catch((e) => toast.error(errorMessage(e)));

  if (q.isLoading) return <main className="mx-auto w-full max-w-6xl px-4 py-6 text-sm text-muted-foreground md:px-8">Cargando pedido…</main>;
  if (q.error || !o) return <main className="mx-auto w-full max-w-3xl px-4 py-6"><ErrorBox error={q.error} onRetry={() => void q.refetch()} /></main>;
  const cur = o.currency;
  const open = o.status === "paid" || o.status === "fulfilled";
  const toShip = (o.lines ?? []).some((l) => l.qty - l.cancelledQty - l.fulfilledQty > 0);
  const refundable = Math.max(o.paidTotal ?? 0, 0) > 0;
  const returnable = (o.lines ?? []).some((l) => l.fulfilledQty - l.returnedQty > 0);
  const editable = o.status === "paid" && (o.lines ?? []).every((l) => l.fulfilledQty === 0);
  const timeline = [...(o.events ?? [])].reverse();

  return (
    <main className="mx-auto grid w-full max-w-6xl content-start gap-5 px-4 py-6 md:px-8">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="grid gap-1">
          <a href={href("pedidos")} className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"><ArrowLeft className="size-4" aria-hidden="true" /> Pedidos</a>
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-2xl font-bold tracking-tight">Pedido #{o.displayNumber || o.number}</h1>
            {o.poNumber && <span className="rounded-md border px-2 py-0.5 text-sm" data-testid="po-number">Orden de compra {o.poNumber}</span>}
            <StatusPill status={o.paymentStatus || o.status} />
            {open && <StatusPill status={o.fulfillmentStatus} />}
            {(o.status === "cancelled" || o.status === "refunded") && <StatusPill status={o.status} />}
          </div>
          <p className="text-sm text-muted-foreground">{dateTime(o.createdAt)} · canal {o.channel}{o.installments ? ` · ${o.installments} meses sin intereses` : ""}</p>
        </div>
        {canEdit && (
          <div className="flex flex-wrap gap-2">
            {open && toShip && <Button onClick={() => setDialog("ship")}><Truck aria-hidden="true" /> Enviar productos</Button>}
            {editable && <Button variant="outline" onClick={() => setDialog("edit")}><PencilLine aria-hidden="true" /> Editar productos</Button>}
            {open && returnable && <Button variant="outline" onClick={() => setDialog("return")}><Undo2 aria-hidden="true" /> Devolución</Button>}
            {open && refundable && <Button variant="outline" onClick={() => setDialog("refund")}><RotateCcw aria-hidden="true" /> Reembolsar</Button>}
            {(open || o.status === "pending_payment") && <Button variant="outline" className="text-destructive" onClick={() => setDialog("cancel")}><Ban aria-hidden="true" /> Cancelar pedido</Button>}
          </div>
        )}
      </div>

      {o.status === "pending_payment" && o.voucher && (
        <Notice tone="warn">
          <strong>Esperando el pago {o.voucher.kind === "oxxo" ? "en OXXO" : "por SPEI"}.</strong> {o.voucher.kind === "oxxo" ? `Referencia ${o.voucher.reference}` : `CLABE ${o.voucher.clabe}${o.voucher.reference ? `, referencia ${o.voucher.reference}` : ""}`}; vence {dateTime(o.voucher.expiresAt)}. El stock está apartado y el pedido se confirma solo al llegar el dinero; si vence, se cancela.
          {canEdit && <> <Button size="sm" variant="outline" className="ml-2" onClick={() => act(sale.payVoucher(o.id), "Pago simulado. El pedido quedó pagado.")}>Simular pago (modo de prueba)</Button></>}
        </Notice>
      )}
      {o.risk?.level === "review" && (
        <Notice tone="warn">
          <strong>Revisa este pedido antes de enviarlo.</strong> Tus reglas antifraude lo marcaron: {(o.risk.reasons ?? []).join("; ")}.{" "}
          {canEdit && <Button size="sm" variant="outline" className="ml-2" onClick={() => void sale.clearRisk(o.id).then(() => { toast.success("Pedido marcado como revisado."); refresh(); }).catch((e) => toast.error(errorMessage(e)))}>Ya lo revisé, está bien</Button>}
        </Notice>
      )}
      {o.risk?.level === "cleared" && <p className="text-sm text-muted-foreground">Revisado por {o.risk.clearedBy || "el equipo"} tras la alerta antifraude.</p>}
      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_340px]">
        <div className="grid content-start gap-5">
          <Card>
            <CardHeader><CardTitle className="text-base">Productos</CardTitle></CardHeader>
            <CardContent className="grid gap-4">
              <div className="overflow-x-auto rounded-md border">
                <Table>
                  <TableHeader><TableRow><TableHead>Producto</TableHead><TableHead className="text-right">Cant.</TableHead><TableHead className="text-right">Precio</TableHead><TableHead className="text-right">Total</TableHead><TableHead>Avance</TableHead></TableRow></TableHeader>
                  <TableBody>
                    {(o.lines ?? []).map((l) => (
                      <TableRow key={l.line}>
                        <TableCell><div className="font-medium">{l.title}</div><div className="font-mono text-xs text-muted-foreground">{l.sku}</div></TableCell>
                        <TableCell className="text-right tabular-nums">{l.qty}</TableCell>
                        <TableCell className="text-right tabular-nums">{formatMoney(l.unit, cur)}</TableCell>
                        <TableCell className="text-right tabular-nums">{formatMoney(l.total, cur)}</TableCell>
                        <TableCell className="text-xs text-muted-foreground">
                          {[l.fulfilledQty ? `${l.fulfilledQty} enviadas` : "", l.returnedQty ? `${l.returnedQty} devueltas` : "", l.refundedQty ? `${l.refundedQty} reembolsadas` : "", l.cancelledQty ? `${l.cancelledQty} canceladas` : ""].filter(Boolean).join(" · ") || "Sin enviar"}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              <dl className="ml-auto grid w-full max-w-xs gap-1 text-sm">
                {([["Productos", o.itemsTotal], o.discounts ? ["Descuentos", -o.discounts] : null, ["Envío", o.shipping], ...(o.feeLines ?? []).map((f) => [f.name, f.amount] as [string, number])] as ([string, number] | null)[]).filter(Boolean).map((r) => (
                  <div key={r![0]} className="flex justify-between"><dt className="text-muted-foreground">{r![0]}</dt><dd className="tabular-nums">{formatMoney(r![1], cur)}</dd></div>
                ))}
                <div className="flex justify-between border-t pt-1 font-semibold"><dt>Total</dt><dd className="tabular-nums">{formatMoney(o.grandTotal, cur)}</dd></div>
                <div className="flex justify-between text-xs text-muted-foreground"><dt>IVA incluido</dt><dd className="tabular-nums">{formatMoney(o.taxIncluded, cur)}</dd></div>
                {o.refundedTotal > 0 && <div className="flex justify-between text-warn"><dt>Reembolsado</dt><dd className="tabular-nums">−{formatMoney(o.refundedTotal, cur)}</dd></div>}
              </dl>
              {(o.promotions ?? []).length > 0 && (
                <p className="text-sm text-muted-foreground">Promociones: {o.promotions!.map((p) => `${p.promotionName}${p.coupon ? ` (cupón ${p.coupon})` : ""} −${formatMoney(p.amount, cur)}`).join(" · ")}</p>
              )}
            </CardContent>
          </Card>

          {(o.fulfillments ?? []).length > 0 && (
            <Card>
              <CardHeader><CardTitle className="text-base">Envíos</CardTitle></CardHeader>
              <CardContent className="grid gap-3">
                {o.fulfillments!.map((f) => (
                  <div key={f.id} className="grid gap-2 rounded-md border p-3 text-sm">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <span className="flex items-center gap-2 font-medium"><PackageCheck className="size-4" aria-hidden="true" /> Envío {f.number} <StatusPill status={f.status} /></span>
                      {canEdit && f.status !== "delivered" && f.status !== "cancelled" && (
                        <span className="flex gap-2">
                          {f.status === "pending" && <Button size="sm" variant="outline" onClick={() => void act(sale.updateFulfillment(id, f.id, { status: "shipped" }), "Marcado como en camino.")}>Marcar en camino</Button>}
                          {f.status === "shipped" && <Button size="sm" variant="outline" onClick={() => void act(sale.updateFulfillment(id, f.id, { status: "delivered" }), "Marcado como entregado.")}>Marcar entregado</Button>}
                        </span>
                      )}
                    </div>
                    <p className="text-muted-foreground">
                      {f.carrier || "Sin transportista"}{f.trackingNumber ? ` · guía ${f.trackingNumber}` : ""}{" "}
                      {f.trackingUrl && <a className="inline-flex items-center gap-1 text-primary underline" href={f.trackingUrl} target="_blank" rel="noreferrer">Rastrear <ExternalLink className="size-3" aria-hidden="true" /></a>}
                      {f.labelUrl?.startsWith("https://") && <> · <a className="text-primary underline" href={f.labelUrl} target="_blank" rel="noreferrer">Imprimir guía</a></>}
                      {f.labelCost ? ` · guía comprada por ${formatMoney(f.labelCost, cur)}` : ""}
                      {f.trackingStatus === "exception" && <span className="text-bad"> · la paquetería reporta un problema</span>}
                    </p>
                    <p>{f.items.map((i) => `${i.qty} × ${i.title}`).join(" · ")}</p>
                  </div>
                ))}
              </CardContent>
            </Card>
          )}

          {((o.refunds ?? []).length > 0 || (o.returns ?? []).length > 0) && (
            <Card>
              <CardHeader><CardTitle className="text-base">Reembolsos y devoluciones</CardTitle></CardHeader>
              <CardContent className="grid gap-2 text-sm">
                {(o.returns ?? []).map((r) => (
                  <div key={r.id} className="flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2">
                    <span>Devolución {r.number}{r.requestedBy === "customer" ? " (la pidió el cliente)" : ""} · {r.items.map((i) => `${i.qty} × ${i.title} (${REASONS[i.reason] ?? i.reason})`).join(", ")}{r.labelTracking ? ` · guía de regreso ${r.labelTracking}` : ""}</span>
                    <span className="flex items-center gap-2"><StatusPill status={r.status} /><a className="text-primary underline" href={href("pedidos", "devoluciones")}>Gestionar</a></span>
                  </div>
                ))}
                {(o.refunds ?? []).map((r) => (
                  <div key={r.id} className="flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2">
                    <span>{formatMoney(r.amount, cur)}{r.reason ? ` · ${r.reason}` : ""} <span className="text-muted-foreground">· {dateTime(r.createdAt)}</span></span>
                    <StatusPill status={r.status} />
                  </div>
                ))}
              </CardContent>
            </Card>
          )}

          <CustomFields resource="order" id={id} canEdit={canEdit} />
          <Card>
            <CardHeader><CardTitle className="text-base">Notas e historial</CardTitle></CardHeader>
            <CardContent className="grid gap-4">
              {canEdit && (
                <form className="grid gap-2" onSubmit={(e) => { e.preventDefault(); if (!note.trim()) return; void act(sale.addNote(id, note.trim(), noteVis), "Nota guardada.").then(() => setNote("")); }}>
                  <Textarea aria-label="Nueva nota" rows={2} placeholder="Escribe una nota…" value={note} onChange={(e) => setNote(e.target.value)} />
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <Select value={noteVis} onValueChange={(v) => setNoteVis(v as typeof noteVis)}>
                      <SelectTrigger className="w-64" aria-label="Quién la ve"><SelectValue /></SelectTrigger>
                      <SelectContent><SelectItem value="internal">Solo mi equipo</SelectItem><SelectItem value="customer">Visible para el cliente</SelectItem></SelectContent>
                    </Select>
                    <Button type="submit" variant="outline" disabled={!note.trim()}><StickyNote aria-hidden="true" /> Agregar nota</Button>
                  </div>
                </form>
              )}
              {(o.notes ?? []).map((n) => (
                <div key={n.id} className="rounded-md bg-muted/50 px-3 py-2 text-sm">
                  <p className="whitespace-pre-wrap">{n.body}</p>
                  <p className="text-xs text-muted-foreground">{n.authorName || n.author} · {dateTime(n.createdAt)} · {n.visibility === "customer" ? "visible para el cliente" : "interna"}</p>
                </div>
              ))}
              <ol className="grid gap-3 border-l-2 pl-4">
                {timeline.map((e) => (
                  <li key={e.seq} className="text-sm">
                    <span className="font-medium">{EVENT[e.type] ?? e.type}</span>
                    <span className="block text-xs text-muted-foreground">{dateTime(e.at)} · {e.actor}</span>
                  </li>
                ))}
              </ol>
            </CardContent>
          </Card>
        </div>

        <div className="grid content-start gap-5">
          <Card>
            <CardHeader><CardTitle className="text-base">Cliente</CardTitle></CardHeader>
            <CardContent className="grid gap-1 text-sm">
              {o.customer ? (
                <>
                  <span className="font-medium">{o.customer.name}</span>
                  <span>{o.customer.email}</span>
                  {o.customer.phone && <span>{o.customer.phone}</span>}
                  {o.customer.address && <span className="text-muted-foreground">{o.customer.address}</span>}
                  {o.customerId && <a className="pt-1 text-primary underline" href={href("clientes", o.customerId)}>Ver ficha del cliente</a>}
                </>
              ) : (
                <span className="text-muted-foreground">Tu acceso no incluye datos personales del cliente.</span>
              )}
            </CardContent>
          </Card>
          {o.shippingMethod && (
            <Card>
              <CardHeader><CardTitle className="text-base">Entrega</CardTitle></CardHeader>
              <CardContent className="grid gap-1 text-sm">
                <span className="font-medium">{o.shippingMethod.kind === "pickup" ? "Recolección: " : ""}{o.shippingMethod.name}{o.shippingMethod.carrier ? ` · ${o.shippingMethod.carrier}` : ""}</span>
                {o.shippingMethod.estimate?.text && <span>{o.shippingMethod.estimate.text}</span>}
                {o.shippingMethod.pickupAddress && <span className="text-muted-foreground">{o.shippingMethod.pickupAddress}</span>}
                {o.shippingMethod.priceReason && <span className="text-muted-foreground">{o.shippingMethod.priceReason}</span>}
                {o.customer?.notes && <span className="mt-1 rounded-md bg-muted p-2">Nota del cliente: {o.customer.notes}</span>}
              </CardContent>
            </Card>
          )}
          <Card>
            <CardHeader><CardTitle className="text-base">Pago y factura</CardTitle></CardHeader>
            <CardContent className="grid gap-2 text-sm">
              {o.payment ? (
                <>
                  {(o.payments && o.payments.length > 1 ? o.payments : [o.payment]).map((p, i) => (
                    <div key={i} className="grid gap-0.5">
                      <div className="flex items-center gap-2"><StatusPill status={p.status} /><span className="tabular-nums">{formatMoney(p.amount, cur)}</span><span className="text-muted-foreground">{p.provider === "gift_card" ? "Tarjeta de regalo o saldo" : p.provider === "manual" ? "Pago manual" : "Tarjeta"}</span></div>
                      <span className="break-all font-mono text-xs text-muted-foreground">{p.provider} · {p.ref}</span>
                    </div>
                  ))}
                </>
              ) : <span className="text-muted-foreground">Sin pago registrado.</span>}
              {o.balance > 0 && <Notice tone="warn">El cliente debe {formatMoney(o.balance, cur)}.</Notice>}
              {o.balance < 0 && <Notice tone="warn">La tienda debe {formatMoney(-o.balance, cur)} al cliente.</Notice>}
              {o.invoice && (
                <div className="flex items-center gap-2">Factura: <StatusPill status={o.invoice.status} />{o.invoice.url && <a className="text-primary underline" href={o.invoice.url} target="_blank" rel="noreferrer">Abrir</a>}</div>
              )}
            </CardContent>
          </Card>
        </div>
      </div>

      {dialog === "ship" && <ShipDialog order={o} onClose={() => setDialog(null)} onDone={refresh} />}
      {dialog === "refund" && <RefundDialog order={o} onClose={() => setDialog(null)} onDone={refresh} />}
      {dialog === "return" && <ReturnDialog order={o} onClose={() => setDialog(null)} onDone={refresh} />}
      {dialog === "edit" && <EditDialog order={o} onClose={() => setDialog(null)} onDone={refresh} />}
      {dialog === "cancel" && <CancelDialog order={o} onClose={() => setDialog(null)} onDone={refresh} />}
    </main>
  );
}

function Shell({ title, description, children, onClose, footer }: { title: string; description: string; children: ReactNode; onClose: () => void; footer: ReactNode }) {
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-xl overflow-y-auto">
        <DialogHeader><DialogTitle>{title}</DialogTitle><DialogDescription>{description}</DialogDescription></DialogHeader>
        <div className="grid gap-4">{children}</div>
        <DialogFooter><Button variant="outline" onClick={onClose}>Cerrar</Button>{footer}</DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
type DP = { order: Order; onClose: () => void; onDone: () => void };

function ShipDialog({ order, onClose, onDone }: DP) {
  const left = (l: NonNullable<Order["lines"]>[number]) => l.qty - l.cancelledQty - l.fulfilledQty;
  const [qty, setQty] = useState<Qty>(() => Object.fromEntries((order.lines ?? []).map((l) => [l.line, Math.max(left(l), 0)])));
  const [carrier, setCarrier] = useState("Estafeta");
  const [tracking, setTracking] = useState("");
  const [notify, setNotify] = useState(true);
  const [busy, setBusy] = useState(false);
  const connected = useQuery({ queryKey: ["connected-carriers"], queryFn: sale.connectedCarriers, staleTime: 60_000 });
  const [label, setLabel] = useState("");
  const items = picked(qty);
  async function go() {
    setBusy(true);
    try {
      await sale.fulfill(order.id, label ? { items, carrier: "", trackingNumber: "", notify, buyLabel: true, carrierId: label } : { items, carrier: carrier === "Otro" ? "" : carrier, trackingNumber: tracking.trim(), notify });
      toast.success(label ? "Guía comprada y envío creado." + (notify ? " Le avisamos al cliente." : "") : notify ? "Envío creado. Le avisamos al cliente por correo." : "Envío creado.");
      onDone();
      onClose();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Shell title="Enviar productos" description="Puedes enviar todo o solo una parte. El stock ya está apartado para este pedido." onClose={onClose}
      footer={<Button onClick={() => void go()} disabled={busy || !items.length}>{busy ? "Guardando…" : "Crear envío"}</Button>}>
      <QtyPicker lines={order.lines} max={left} value={qty} onChange={setQty} />
      {(connected.data ?? []).length > 0 && (
        <div className="grid gap-1.5"><Label htmlFor="sh-label">Guía</Label>
          <select id="sh-label" className="h-9 rounded-md border bg-background px-2 text-sm" value={label} onChange={(e) => setLabel(e.target.value)}>
            <option value="">Ya tengo la guía (la capturo)</option>
            {(connected.data ?? []).map((c) => <option key={c.id} value={c.id}>Comprar guía con {c.name}</option>)}
          </select>
        </div>
      )}
      {!label && <div className="grid gap-4 sm:grid-cols-2">
        <div className="grid gap-1.5"><Label htmlFor="sh-carrier">Transportista</Label>
          <Select value={carrier} onValueChange={setCarrier}><SelectTrigger id="sh-carrier"><SelectValue /></SelectTrigger><SelectContent>{CARRIERS.map((c) => <SelectItem key={c} value={c}>{c}</SelectItem>)}</SelectContent></Select>
        </div>
        <div className="grid gap-1.5"><Label htmlFor="sh-track">Número de guía</Label><Input id="sh-track" value={tracking} onChange={(e) => setTracking(e.target.value)} placeholder="Opcional" /></div>
      </div>}
      <label className="flex items-center gap-2 text-sm"><Checkbox checked={notify} onCheckedChange={(c) => setNotify(!!c)} /> Avisar al cliente por correo con el enlace de rastreo</label>
    </Shell>
  );
}

function RefundDialog({ order, onClose, onDone }: DP) {
  const left = (l: NonNullable<Order["lines"]>[number]) => l.qty - l.cancelledQty - l.refundedQty;
  const [qty, setQty] = useState<Qty>({});
  const [shipping, setShipping] = useState(false);
  const [restock, setRestock] = useState(true);
  const [amount, setAmount] = useState("");
  const [touched, setTouched] = useState(false);
  const [reason, setReason] = useState("");
  const [quote, setQuote] = useState<Quote | null>(null);
  const [busy, setBusy] = useState(false);
  const [toCredit, setToCredit] = useState(false);
  const key = useMemo(() => `refund-${order.id}-${Date.now()}`, [order.id]);
  const lines = picked(qty);
  const linesKey = JSON.stringify(lines) + shipping + restock;
  useEffect(() => {
    let live = true;
    sale.refundQuote(order.id, { lines, shipping: shipping ? Math.max(order.shipping, 0) : 0, restock })
      .then((q) => { if (live) { setQuote(q); if (!touched) setAmount(centsToPesos(q.suggested)); } })
      .catch(() => live && setQuote(null));
    return () => { live = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [linesKey]);
  const cents = parsePesos(amount);
  const invalid = cents == null || cents <= 0 || (quote != null && cents > quote.maxRefundable);
  async function go() {
    if (cents == null) return;
    setBusy(true);
    try {
      await sale.refund(order.id, { lines, shipping: quote?.shipping ?? 0, amount: cents, reason: reason.trim(), restock, toCredit }, key);
      toast.success(toCredit ? `Saldo a favor de ${formatMoney(cents, order.currency)} enviado al cliente por correo.` : `Reembolso de ${formatMoney(cents, order.currency)} enviado.`);
      onDone();
      onClose();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Shell title="Reembolsar" description="Elige qué productos reembolsar o escribe un monto. El dinero vuelve al mismo medio de pago, o se entrega como saldo a favor." onClose={onClose}
      footer={<Button onClick={() => void go()} disabled={busy || invalid}>{busy ? "Reembolsando…" : `Reembolsar ${cents ? formatMoney(cents, order.currency) : ""}`}</Button>}>
      <QtyPicker lines={order.lines} max={left} value={qty} onChange={setQty} />
      {order.shipping > 0 && <label className="flex items-center gap-2 text-sm"><Checkbox checked={shipping} onCheckedChange={(c) => setShipping(!!c)} /> Incluir el envío ({formatMoney(quote?.shippingLeft ?? order.shipping, order.currency)})</label>}
      <label className="flex items-center gap-2 text-sm"><Checkbox checked={restock} onCheckedChange={(c) => setRestock(!!c)} /> Devolver al inventario las piezas que aún no se envían</label>
      <label className="flex items-center gap-2 text-sm"><Checkbox checked={toCredit} onCheckedChange={(c) => setToCredit(!!c)} /> Dar saldo a favor en lugar de devolver el dinero (el cliente recibe un código por correo)</label>
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="grid gap-1.5"><Label htmlFor="rf-amount">Monto a reembolsar ({order.currency})</Label>
          <Input id="rf-amount" inputMode="decimal" value={amount} onChange={(e) => { setAmount(e.target.value); setTouched(true); }} />
          {quote && <p className="text-xs text-muted-foreground">Máximo disponible: {formatMoney(quote.maxRefundable, order.currency)}</p>}
        </div>
        <div className="grid gap-1.5"><Label htmlFor="rf-reason">Motivo</Label><Input id="rf-reason" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Lo verá el cliente en el correo" /></div>
      </div>
      {quote != null && cents != null && cents > quote.maxRefundable && <Notice tone="bad">No puedes reembolsar más de lo cobrado.</Notice>}
    </Shell>
  );
}

function ReturnDialog({ order, onClose, onDone }: DP) {
  const left = (l: NonNullable<Order["lines"]>[number]) => l.fulfilledQty - l.returnedQty;
  const [qty, setQty] = useState<Qty>({});
  const [reason, setReason] = useState("size");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [swap, setSwap] = useState(false);
  const [retLabel, setRetLabel] = useState("");
  const connected = useQuery({ queryKey: ["connected-carriers"], queryFn: sale.connectedCarriers, staleTime: 60_000 });
  const [newItems, setNewItems] = useState<NewItem[]>([]);
  const items = picked(qty).map((i) => ({ ...i, reason }));
  async function go() {
    setBusy(true);
    try {
      const r = await sale.createReturn(order.id, { items, note: note.trim(), exchange: swap ? newItems.filter((x) => x.sku && x.qty > 0) : undefined, returnLabel: !!retLabel, carrierId: retLabel || undefined });
      toast.success(swap ? `Cambio ${r.number} creado. El producto nuevo ya está apartado; se envía cuando registres la devolución.` : `Devolución ${r.number} creada. Cuando llegue el paquete, márcala como recibida.`);
      onDone();
      onClose();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Shell title="Crear devolución" description="Registra lo que el cliente va a devolver. El reembolso se hace cuando recibas el paquete." onClose={onClose}
      footer={<Button onClick={() => void go()} disabled={busy || !items.length}>{busy ? "Creando…" : "Crear devolución"}</Button>}>
      <QtyPicker lines={order.lines} max={left} value={qty} onChange={setQty} />
      <div className="grid gap-1.5"><Label htmlFor="rt-reason">Motivo</Label>
        <Select value={reason} onValueChange={setReason}><SelectTrigger id="rt-reason"><SelectValue /></SelectTrigger><SelectContent>{Object.entries(REASONS).map(([k, v]) => <SelectItem key={k} value={k}>{v}</SelectItem>)}</SelectContent></Select>
      </div>
      {(connected.data ?? []).length > 0 && (
        <div className="grid gap-1.5"><Label htmlFor="rt-label">Guía de devolución</Label>
          <select id="rt-label" className="h-9 rounded-md border bg-background px-2 text-sm" value={retLabel} onChange={(e) => setRetLabel(e.target.value)}>
            <option value="">El cliente envía por su cuenta</option>
            {(connected.data ?? []).map((c) => <option key={c.id} value={c.id}>Guía prepagada con {c.name}</option>)}
          </select>
        </div>
      )}
      <label className="flex items-center gap-2 text-sm"><Checkbox checked={swap} onCheckedChange={(c) => setSwap(!!c)} /> Cambiar por otro producto en lugar de reembolsar</label>
      {swap && <><p className="text-xs text-muted-foreground">El valor de lo devuelto paga lo nuevo. Si lo nuevo cuesta más, el pedido queda con saldo por cobrar; si cuesta menos, la diferencia se reembolsa al recibir la devolución.</p>
        <SkuLines value={newItems} onChange={setNewItems} idPrefix="ex" /></>}
      <div className="grid gap-1.5"><Label htmlFor="rt-note">Nota</Label><Textarea id="rt-note" rows={2} value={note} onChange={(e) => setNote(e.target.value)} /></div>
    </Shell>
  );
}

interface NewItem { sku: string; qty: number }

/** SkuLines: rows of SKU and quantity, with suggestions from the catalog. */
function SkuLines({ value, onChange, idPrefix }: { value: NewItem[]; onChange: (v: NewItem[]) => void; idPrefix: string }) {
  const [q, setQ] = useState("");
  const hits = useQuery({ queryKey: ["product-search", q], queryFn: () => sale.productSearch(q), enabled: q.trim().length >= 2, staleTime: 30_000 });
  const set = (i: number, p: Partial<NewItem>) => onChange(value.map((x, j) => (j === i ? { ...x, ...p } : x)));
  const options = (hits.data ?? []).flatMap((p) => p.skus.map((sku) => ({ sku, title: p.title })));
  return (
    <div className="grid gap-2">
      <datalist id={`${idPrefix}-skus`}>{options.map((o) => <option key={o.sku} value={o.sku}>{o.title}</option>)}</datalist>
      {value.map((x, i) => (
        <div key={i} className="flex items-end gap-2">
          <div className="grid flex-1 gap-1"><Label htmlFor={`${idPrefix}-sku-${i}`}>SKU</Label><Input id={`${idPrefix}-sku-${i}`} className="font-mono" list={`${idPrefix}-skus`} value={x.sku} placeholder="Escribe nombre o SKU"
            onChange={(e) => { set(i, { sku: e.target.value.trim() }); setQ(e.target.value); }} /></div>
          <div className="grid w-24 gap-1"><Label htmlFor={`${idPrefix}-qty-${i}`}>Cantidad</Label><Input id={`${idPrefix}-qty-${i}`} inputMode="numeric" value={String(x.qty)} onChange={(e) => set(i, { qty: Math.max(0, Number(e.target.value.replace(/\D/g, "")) || 0) })} /></div>
          <Button type="button" variant="ghost" size="icon" aria-label={`Quitar ${x.sku || "producto"}`} onClick={() => onChange(value.filter((_, j) => j !== i))}><Trash2 className="size-4" /></Button>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" className="justify-self-start" onClick={() => onChange([...value, { sku: "", qty: 1 }])}><Plus className="size-4" /> Agregar producto</Button>
    </div>
  );
}

/** EditDialog: take items out of a paid, unshipped order and add new ones, with a preview of the difference. */
function EditDialog({ order, onClose, onDone }: DP) {
  const left = (l: NonNullable<Order["lines"]>[number]) => l.qty - l.cancelledQty;
  const [qty, setQty] = useState<Qty>({});
  const [add, setAdd] = useState<NewItem[]>([]);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [preview, setPreview] = useState<{ before: OTotals; after: OTotals; difference: number } | null>(null);
  const [err, setErr] = useState("");
  const body = (dryRun: boolean) => ({ add: add.filter((x) => x.sku && x.qty > 0), remove: picked(qty), reason: reason.trim(), dryRun });
  const empty = !body(true).add.length && !body(true).remove.length;
  const key = JSON.stringify(body(true));
  useEffect(() => {
    setPreview(null); setErr("");
    if (empty) return;
    const t = setTimeout(() => { sale.editOrder(order.id, body(true)).then(setPreview).catch((e) => setErr(errorMessage(e))); }, 350);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);
  async function go() {
    setBusy(true);
    try {
      const r = await sale.editOrder(order.id, body(false));
      toast.success(r.difference > 0 ? `Pedido editado. El cliente debe ${formatMoney(r.difference, order.currency)}; envíale el enlace de pago.` : r.difference < 0 ? `Pedido editado. Reembolsamos ${formatMoney(-r.difference, order.currency)} al cliente.` : "Pedido editado.");
      if (r.refundError) toast.error(`El reembolso no pasó: ${r.refundError}`);
      onDone(); onClose();
    } catch (e) {
      setErr(errorMessage(e)); toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  const m = (v: number) => formatMoney(v, order.currency);
  return (
    <Shell title={`Editar productos del pedido #${order.number}`} description="Quita o agrega productos antes de enviar. Las piezas que quitas vuelven al inventario; las nuevas se apartan. El envío y el descuento se conservan." onClose={onClose}
      footer={<Button onClick={() => void go()} disabled={busy || empty || !preview}>{busy ? "Guardando…" : "Guardar cambios"}</Button>}>
      <h4 className="text-sm font-medium">Quitar</h4>
      <QtyPicker lines={order.lines} max={left} value={qty} onChange={setQty} />
      <h4 className="text-sm font-medium">Agregar</h4>
      <SkuLines value={add} onChange={setAdd} idPrefix="ed" />
      <div className="grid gap-1.5"><Label htmlFor="ed-reason">Motivo</Label><Input id="ed-reason" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Queda en el historial del pedido" /></div>
      {err && <Notice tone="bad">{err}</Notice>}
      {preview && <div className="rounded-md border p-3 text-sm" data-testid="edit-preview">
        <p>Total: {m(preview.before.grandTotal)} → <strong>{m(preview.after.grandTotal)}</strong></p>
        <p className={preview.difference > 0 ? "text-warn" : preview.difference < 0 ? "text-ok" : "text-muted-foreground"}>
          {preview.difference > 0 ? `El cliente pagará ${m(preview.difference)} más (saldo por cobrar).` : preview.difference < 0 ? `Se reembolsan ${m(-preview.difference)} al cliente.` : "Sin diferencia de precio."}</p>
      </div>}
    </Shell>
  );
}

function CancelDialog({ order, onClose, onDone }: DP) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const paid = order.status !== "pending_payment";
  async function go() {
    setBusy(true);
    try {
      await sale.cancel(order.id, reason.trim());
      toast.success(paid ? "Pedido cancelado y reembolsado. El stock volvió al inventario." : "Pedido cancelado.");
      onDone();
      onClose();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Shell title={`¿Cancelar el pedido #${order.number}?`} description={paid ? `Se reembolsan ${formatMoney(Math.max(order.paidTotal, 0), order.currency)} al cliente, el stock vuelve a su ubicación y se anulan los cupones usados. No se puede deshacer.` : "Se libera el stock apartado. No se puede deshacer."} onClose={onClose}
      footer={<Button variant="destructive" onClick={() => void go()} disabled={busy}>{busy ? "Cancelando…" : paid ? "Cancelar y reembolsar" : "Cancelar pedido"}</Button>}>
      <div className="grid gap-1.5"><Label htmlFor="cn-reason">Motivo (opcional)</Label><Input id="cn-reason" value={reason} onChange={(e) => setReason(e.target.value)} /></div>
    </Shell>
  );
}
