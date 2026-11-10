import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ExternalLink, PackageCheck, Undo2 } from "lucide-react";
import { api, errorMessage, type Me, type Order } from "@/api";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/sonner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatMoney } from "@/money";
import { can } from "@/perms";
import { href } from "@/router";
import { dateTime, ErrorBox, Notice, Page, statusLabel, StatusPill } from "./common";

export function Orders({ me, orderId }: { me: Me; orderId?: string }) {
  return orderId ? <OrderDetail key={orderId} me={me} id={orderId} /> : <OrderList />;
}

function OrderList() {
  const q = useQuery({ queryKey: ["orders"], queryFn: api.orders });
  return (
    <Page title="Pedidos" sub="Los 100 pedidos más recientes.">
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Pedido</TableHead>
            <TableHead>Fecha</TableHead>
            <TableHead>Estado</TableHead>
            <TableHead className="text-right">Total</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {(q.data ?? []).map((o) => (
            <TableRow key={o.id}>
              <TableCell>
                <a className="font-medium text-primary hover:underline" href={href("pedidos", o.id)}>
                  #{o.number}
                </a>
                <div className="font-mono text-xs text-muted-foreground">{o.id}</div>
              </TableCell>
              <TableCell>{dateTime(o.createdAt)}</TableCell>
              <TableCell>
                <StatusPill status={o.status} />
              </TableCell>
              <TableCell className="text-right tabular-nums">{formatMoney(o.grandTotal, o.currency)}</TableCell>
            </TableRow>
          ))}
          {q.data && q.data.length === 0 && (
            <TableRow>
              <TableCell colSpan={4} className="py-8 text-center text-muted-foreground">
                Todavía no hay pedidos. Aparecerán aquí cuando alguien compre en la tienda.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      {q.isPending && <p className="text-sm text-muted-foreground">Cargando pedidos…</p>}
    </Page>
  );
}

const EVENT_LABEL: Record<string, string> = {
  "order.created": "Pedido creado",
  "payment.captured": "Pago cobrado",
  "payment.failed": "Pago rechazado",
  "order.fulfilled": "Marcado como enviado",
  "order.refunded": "Cancelado y reembolsado",
  "order.cancelled": "Cancelado",
  "invoice.issued": "Factura emitida",
};

function actorLabel(actor: string): string {
  if (actor.startsWith("member:")) return "Miembro del equipo";
  if (actor.startsWith("api_key:")) return "Integración (API key)";
  if (actor === "reconciler") return "Conciliación automática";
  if (actor === "invoicer") return "Facturación automática";
  if (actor === "stripe-webhook") return "Stripe";
  return actor;
}

function OrderDetail({ me, id }: { me: Me; id: string }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["order", id], queryFn: () => api.order(id) });
  const [confirm, setConfirm] = useState<"fulfill" | "cancel" | null>(null);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const canEdit = can(me, "orders", "edit");

  async function run(action: "fulfill" | "cancel") {
    setBusy(true);
    setErr(null);
    try {
      const o = action === "fulfill" ? await api.fulfill(id) : await api.cancel(id, reason.trim() || "Cancelado desde el panel");
      qc.setQueryData(["order", id], o);
      void qc.invalidateQueries({ queryKey: ["orders"] });
      toast.success(action === "fulfill" ? "Pedido marcado como enviado" : "Pedido cancelado y reembolsado");
      setConfirm(null);
      setReason("");
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  const back = (
    <a className="text-primary hover:underline" href={href("pedidos")}>
      ← Pedidos
    </a>
  );
  if (q.isPending) return <Page title="Pedido" sub={back}>Cargando…</Page>;
  if (q.error || !q.data)
    return (
      <Page title="Pedido" sub={back}>
        <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      </Page>
    );
  const o: Order = q.data;
  const canFulfill = canEdit && o.status === "paid";
  const canCancel = canEdit && (o.status === "paid" || o.status === "fulfilled" || o.status === "pending_payment");

  return (
    <Page
      title={`Pedido #${o.number}`}
      sub={
        <span className="flex flex-wrap items-center gap-2">
          {back} · {dateTime(o.createdAt)} · <StatusPill status={o.status} />
        </span>
      }
      actions={
        canEdit && (
          <>
            <Button variant="outline" disabled={!canFulfill} onClick={() => setConfirm("fulfill")}>
              <PackageCheck /> Marcar enviado
            </Button>
            <Button variant="destructive" disabled={!canCancel} onClick={() => setConfirm("cancel")}>
              <Undo2 /> Cancelar y reembolsar
            </Button>
          </>
        )
      }
    >
      {err && <Notice tone="bad">{err}</Notice>}
      <div className="grid gap-4 lg:grid-cols-[2fr_1fr]">
        <Card>
          <CardHeader>
            <CardTitle>Productos</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-3">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Producto</TableHead>
                  <TableHead className="text-right">Cant.</TableHead>
                  <TableHead className="text-right">Precio</TableHead>
                  <TableHead className="text-right">Total</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(o.lines ?? []).map((l) => (
                  <TableRow key={l.sku}>
                    <TableCell>
                      {l.title}
                      <div className="font-mono text-xs text-muted-foreground">{l.sku}</div>
                    </TableCell>
                    <TableCell className="text-right tabular-nums">{l.qty}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatMoney(l.unit, o.currency)}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatMoney(l.total, o.currency)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <dl className="ml-auto grid w-full max-w-xs grid-cols-2 gap-x-4 gap-y-1 text-sm tabular-nums">
              <dt className="text-muted-foreground">Productos</dt>
              <dd className="text-right">{formatMoney(o.itemsTotal, o.currency)}</dd>
              <dt className="text-muted-foreground">Descuentos</dt>
              <dd className="text-right">-{formatMoney(o.discounts, o.currency)}</dd>
              <dt className="text-muted-foreground">Envío</dt>
              <dd className="text-right">{formatMoney(o.shipping, o.currency)}</dd>
              <dt className="font-semibold">Total</dt>
              <dd className="text-right font-semibold">{formatMoney(o.grandTotal, o.currency)}</dd>
              <dt className="text-xs text-muted-foreground">IVA incluido</dt>
              <dd className="text-right text-xs text-muted-foreground">{formatMoney(o.taxIncluded, o.currency)}</dd>
            </dl>
          </CardContent>
        </Card>
        <div className="grid content-start gap-4">
          <Card>
            <CardHeader>
              <CardTitle>Cliente</CardTitle>
            </CardHeader>
            <CardContent className="text-sm">
              {o.customer ? (
                <div className="grid gap-0.5">
                  <strong>{o.customer.name}</strong>
                  <span>{o.customer.email}</span>
                  {o.customer.phone && <span>{o.customer.phone}</span>}
                  {o.customer.address && <span className="text-muted-foreground">{o.customer.address}</span>}
                </div>
              ) : (
                <p className="text-muted-foreground">Tu acceso a pedidos es de lectura: los datos personales del cliente están ocultos.</p>
              )}
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Pago y factura</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-2 text-sm">
              {o.payment ? (
                <div className="flex flex-wrap items-center gap-2">
                  <StatusPill status={o.payment.status} />
                  <span>{formatMoney(o.payment.amount, o.currency)}</span>
                  {o.payment.refunded > 0 && <span className="text-muted-foreground">reembolsado {formatMoney(o.payment.refunded, o.currency)}</span>}
                  <span className="w-full font-mono text-xs text-muted-foreground">
                    {o.payment.provider} · {o.payment.ref}
                  </span>
                </div>
              ) : (
                <p className="text-muted-foreground">Sin pago registrado.</p>
              )}
              {o.invoice ? (
                <div className="flex flex-wrap items-center gap-2">
                  Factura: <StatusPill status={o.invoice.status} />
                  {o.invoice.url && (
                    <a className="inline-flex items-center gap-1 text-primary hover:underline" href={o.invoice.url} target="_blank" rel="noreferrer">
                      Ver factura <ExternalLink className="size-3" />
                    </a>
                  )}
                  {o.invoice.lastError && <span className="w-full text-xs text-bad">Último error: {o.invoice.lastError}</span>}
                </div>
              ) : (
                <p className="text-muted-foreground">Sin factura.</p>
              )}
            </CardContent>
          </Card>
        </div>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Historial</CardTitle>
        </CardHeader>
        <CardContent>
          <ol className="grid gap-2">
            {(o.events ?? []).map((e) => (
              <li key={e.seq} className="border-l-2 border-primary/30 pl-3 text-sm">
                <strong>{EVENT_LABEL[e.type] ?? e.type}</strong>{" "}
                <span className="text-muted-foreground">
                  {e.from ? `${statusLabel(e.from)} → ` : ""}
                  {statusLabel(e.to)}
                </span>
                <div className="text-xs text-muted-foreground">
                  {dateTime(e.at)} · {actorLabel(e.actor)}
                </div>
              </li>
            ))}
          </ol>
        </CardContent>
      </Card>

      <AlertDialog open={confirm !== null} onOpenChange={(v) => !v && !busy && setConfirm(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirm === "cancel" ? `¿Cancelar y reembolsar el pedido #${o.number}?` : `¿Marcar el pedido #${o.number} como enviado?`}</AlertDialogTitle>
            <AlertDialogDescription>
              {confirm === "cancel"
                ? `Se reembolsarán ${formatMoney((o.payment?.amount ?? 0) - (o.payment?.refunded ?? 0), o.currency)} al cliente y las piezas vuelven al inventario. No se puede deshacer.`
                : "El cliente verá su pedido como enviado."}
            </AlertDialogDescription>
          </AlertDialogHeader>
          {confirm === "cancel" && (
            <FormField id="cancel-reason" label="Motivo (opcional)">
              <Input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Por ejemplo: el cliente se arrepintió" />
            </FormField>
          )}
          {err && <Notice tone="bad">{err}</Notice>}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>Volver</AlertDialogCancel>
            <Button variant={confirm === "cancel" ? "destructive" : "default"} disabled={busy} onClick={() => confirm && void run(confirm)}>
              {busy ? "Procesando…" : confirm === "cancel" ? "Sí, cancelar y reembolsar" : "Sí, marcar enviado"}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Page>
  );
}
