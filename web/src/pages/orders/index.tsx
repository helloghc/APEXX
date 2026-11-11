import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Bookmark, Download, FilePlus2, Inbox, Plus, Search, Trash2, Undo2, X } from "lucide-react";
import { errorMessage, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "@/components/ui/sonner";
import { formatMoney, parsePesos } from "@/money";
import { can } from "@/perms";
import { href, navigate } from "@/router";
import { sale, type OrderQuery, type Return } from "@/saleApi";
import { dateTime, ErrorBox, Page, StatusPill } from "../common";
import { EmptyState, SkeletonRows, SubNav, useDebounced } from "../promotions/shared";
import { OrderDetail, REASONS } from "./OrderDetail";

const ALL = "__all";
const TABS = [
  { key: "", label: "Pedidos", route: ["pedidos"] },
  { key: "devoluciones", label: "Devoluciones", route: ["pedidos", "devoluciones"] },
  { key: "borradores", label: "Pedidos borrador", route: ["pedidos", "borradores"] },
];

/** Routes: #pedidos, #pedidos/{id}, #pedidos/devoluciones, #pedidos/borradores. */
export function Orders({ me, route }: { me: Me; route: string[] }) {
  const section = route[1] ?? "";
  if (section && section !== "devoluciones" && section !== "borradores") return <OrderDetail key={section} me={me} id={section} />;
  return (
    <Page title="Pedidos" sub="Todo el ciclo del pedido: pago, envío, devoluciones y reembolsos, con historial completo.">
      <SubNav items={TABS} active={section} label="Secciones de pedidos" />
      {section === "devoluciones" ? <Returns me={me} /> : section === "borradores" ? <Drafts me={me} /> : <OrderList />}
    </Page>
  );
}

function Filter({ label, value, onChange, options }: { label: string; value: string; onChange: (v: string) => void; options: [string, string][] }) {
  return (
    <Select value={value || ALL} onValueChange={(v) => onChange(v === ALL ? "" : v)}>
      <SelectTrigger className="w-44" aria-label={label}><SelectValue /></SelectTrigger>
      <SelectContent><SelectItem value={ALL}>{label}: todos</SelectItem>{options.map(([k, v]) => <SelectItem key={k} value={k}>{v}</SelectItem>)}</SelectContent>
    </Select>
  );
}

function OrderList() {
  const qc = useQueryClient();
  const [f, setF] = useState<OrderQuery>({});
  const [q, setQ] = useState("");
  const dq = useDebounced(q.trim());
  const query: OrderQuery = { ...f, q: dq };
  const list = useInfiniteQuery({
    queryKey: ["orders", query],
    queryFn: ({ pageParam }) => sale.orders({ ...query, cursor: pageParam, limit: 50 }),
    initialPageParam: "",
    getNextPageParam: (l) => l.nextCursor || undefined,
  });
  const views = useQuery({ queryKey: ["order-views"], queryFn: sale.views });
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const total = list.data?.pages[0]?.total ?? 0;
  const filtered = !!(dq || f.status || f.payment || f.fulfillment || f.from || f.to);
  const set = (patch: OrderQuery) => setF({ ...f, ...patch });
  const saveView = () => {
    const label = [f.payment && `Pago: ${f.payment}`, f.fulfillment && `Envío: ${f.fulfillment}`, f.status && f.status, dq].filter(Boolean).join(" · ") || "Vista";
    sale.saveView(label.slice(0, 40), query).then(() => { toast.success("Vista guardada."); void qc.invalidateQueries({ queryKey: ["order-views"] }); }).catch((e) => toast.error(errorMessage(e)));
  };
  return (
    <div className="grid gap-4">
      {(views.data ?? []).length > 0 && (
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="text-muted-foreground">Vistas guardadas:</span>
          {views.data!.map((v) => (
            <span key={v.id} className="inline-flex items-center rounded-full border bg-card">
              <button type="button" className="px-3 py-1 hover:text-primary" onClick={() => { setF({ ...v.filters, q: undefined }); setQ(v.filters.q ?? ""); }}>{v.name}</button>
              <button type="button" aria-label={`Eliminar vista ${v.name}`} className="pr-2 text-muted-foreground hover:text-destructive" onClick={() => void sale.deleteView(v.id).then(() => qc.invalidateQueries({ queryKey: ["order-views"] }))}><X className="size-3" aria-hidden="true" /></button>
            </span>
          ))}
        </div>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-56 flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden="true" />
          <Input aria-label="Buscar pedidos" placeholder="Buscar por número o correo" className="pl-8" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <Filter label="Pago" value={f.payment ?? ""} onChange={(v) => set({ payment: v })} options={[["paid", "Pagado"], ["pending", "Pendiente"], ["partially_refunded", "Reembolso parcial"], ["refunded", "Reembolsado"], ["balance_due", "Saldo pendiente"]]} />
        <Filter label="Envío" value={f.fulfillment ?? ""} onChange={(v) => set({ fulfillment: v })} options={[["unfulfilled", "Sin enviar"], ["partial", "Parcial"], ["fulfilled", "Enviado"], ["delivered", "Entregado"]]} />
        <Input aria-label="Desde" type="date" className="w-40" value={f.from ?? ""} onChange={(e) => set({ from: e.target.value })} />
        <Input aria-label="Hasta" type="date" className="w-40" value={f.to ?? ""} onChange={(e) => set({ to: e.target.value })} />
        {filtered && <Button variant="ghost" onClick={() => { setF({}); setQ(""); }}>Limpiar</Button>}
        {filtered && <Button variant="outline" onClick={saveView}><Bookmark aria-hidden="true" /> Guardar vista</Button>}
        <Button variant={f.review ? "default" : "outline"} aria-pressed={!!f.review} onClick={() => setF({ ...f, review: f.review ? undefined : "1" })}>Por revisar (antifraude)</Button>
        <Button variant="outline" onClick={() => void sale.exportOrders(query).catch((e) => toast.error(errorMessage(e)))}><Download aria-hidden="true" /> Exportar CSV</Button>
      </div>
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      {!list.isLoading && items.length === 0 ? (
        <EmptyState icon={<Inbox />} title={filtered ? "Ningún pedido coincide" : "Aún no hay pedidos"} text={filtered ? "Prueba quitando algún filtro." : "Cuando alguien compre en tu tienda, el pedido aparecerá aquí al instante."} />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader><TableRow><TableHead>Pedido</TableHead><TableHead>Fecha</TableHead><TableHead>Cliente</TableHead><TableHead>Pago</TableHead><TableHead>Envío</TableHead><TableHead className="text-right">Piezas</TableHead><TableHead className="text-right">Total</TableHead></TableRow></TableHeader>
            <TableBody>
              {list.isLoading ? <SkeletonRows cols={7} /> : items.map((o) => (
                <TableRow key={o.id} className="cursor-pointer" onClick={() => navigate("pedidos", o.id)}>
                  <TableCell><a className="font-medium text-primary hover:underline" href={href("pedidos", o.id)} onClick={(e) => e.stopPropagation()}>#{o.displayNumber || o.number}</a></TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{dateTime(o.createdAt)}</TableCell>
                  <TableCell>{o.customerName || o.customerEmail || <span className="text-muted-foreground">—</span>}</TableCell>
                  <TableCell><StatusPill status={o.status === "cancelled" ? "cancelled" : o.paymentStatus || o.status} /></TableCell>
                  <TableCell>{o.status === "cancelled" || o.status === "refunded" ? <span className="text-muted-foreground">—</span> : <StatusPill status={o.fulfillmentStatus || "unfulfilled"} />}</TableCell>
                  <TableCell className="text-right tabular-nums">{o.itemsCount ?? "—"}</TableCell>
                  <TableCell className="text-right font-medium tabular-nums">{formatMoney(o.grandTotal, o.currency)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <div className="flex items-center justify-between text-sm text-muted-foreground">
        <span>{total} pedidos</span>
        {list.hasNextPage && <Button variant="outline" size="sm" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>Ver más</Button>}
      </div>
    </div>
  );
}

function Returns({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "orders", "edit");
  const [status, setStatus] = useState("");
  const list = useQuery({ queryKey: ["returns", status], queryFn: () => sale.returns(status) });
  const [receive, setReceive] = useState<Return | null>(null);
  const items = list.data?.items ?? [];
  const refresh = () => void qc.invalidateQueries({ queryKey: ["returns"] });
  const act = (p: Promise<unknown>, ok: string) => p.then(() => { toast.success(ok); refresh(); }).catch((e) => toast.error(errorMessage(e)));
  return (
    <div className="grid gap-4">
      <Filter label="Estado" value={status} onChange={setStatus} options={[["requested", "Solicitadas"], ["received", "Recibidas"], ["completed", "Completadas"], ["cancelled", "Canceladas"]]} />
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      {!list.isLoading && items.length === 0 ? (
        <EmptyState icon={<Undo2 />} title="Sin devoluciones" text="Las devoluciones se crean desde el detalle de un pedido. Al recibir el paquete decides si cada pieza vuelve al inventario." />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader><TableRow><TableHead>Devolución</TableHead><TableHead>Pedido</TableHead><TableHead>Productos</TableHead><TableHead>Estado</TableHead><TableHead className="text-right">Reembolso</TableHead><TableHead><span className="sr-only">Acciones</span></TableHead></TableRow></TableHeader>
            <TableBody>
              {list.isLoading ? <SkeletonRows cols={6} /> : items.map((r) => (
                <TableRow key={r.id}>
                  <TableCell className="font-medium">D-{r.number}<div className="text-xs font-normal text-muted-foreground">{dateTime(r.createdAt)}</div></TableCell>
                  <TableCell><a className="text-primary hover:underline" href={href("pedidos", r.orderId)}>#{r.orderNumber}</a></TableCell>
                  <TableCell className="max-w-72 text-sm">{r.items.map((i) => `${i.qty} × ${i.title}`).join(", ")}<div className="text-xs text-muted-foreground">{REASONS[r.items[0]?.reason ?? ""] ?? ""}</div></TableCell>
                  <TableCell><StatusPill status={r.status} /></TableCell>
                  <TableCell className="text-right tabular-nums">{r.refundAmount ? formatMoney(r.refundAmount, r.currency ?? "MXN") : "—"}</TableCell>
                  <TableCell>
                    {canEdit && (
                      <div className="flex justify-end gap-2">
                        {r.status === "requested" && <Button size="sm" onClick={() => setReceive(r)}>Recibir</Button>}
                        {r.status === "requested" && <Button size="sm" variant="ghost" onClick={() => void act(sale.cancelReturn(r.id), "Devolución cancelada.")}>Cancelar</Button>}
                        {r.status === "received" && <Button size="sm" onClick={() => void act(sale.refundReturn(r.id), "Reembolso enviado al cliente.")}>Reembolsar</Button>}
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {receive && <ReceiveDialog r={receive} onClose={() => setReceive(null)} onDone={refresh} />}
    </div>
  );
}

function ReceiveDialog({ r, onClose, onDone }: { r: Return; onClose: () => void; onDone: () => void }) {
  const [restock, setRestock] = useState<Record<number, boolean>>(() => Object.fromEntries(r.items.map((i) => [i.line, i.reason !== "defective"])));
  const [refundNow, setRefundNow] = useState(true);
  const [busy, setBusy] = useState(false);
  async function go() {
    setBusy(true);
    try {
      await sale.receiveReturn(r.id, r.items.map((i) => ({ line: i.line, restock: !!restock[i.line] })), !refundNow);
      toast.success(refundNow ? "Devolución recibida y reembolsada." : "Devolución recibida.");
      onDone();
      onClose();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader><DialogTitle>Recibir devolución D-{r.number}</DialogTitle><DialogDescription>Marca qué piezas están en buen estado para volver a venderse. Regresan a la misma ubicación de donde salieron.</DialogDescription></DialogHeader>
        <ul className="divide-y rounded-md border">
          {r.items.map((i) => (
            <li key={i.line} className="flex items-center justify-between gap-3 px-3 py-2 text-sm">
              <span>{i.qty} × {i.title}<span className="block text-xs text-muted-foreground">{REASONS[i.reason] ?? i.reason}</span></span>
              <label className="flex items-center gap-2"><Checkbox checked={!!restock[i.line]} onCheckedChange={(c) => setRestock({ ...restock, [i.line]: !!c })} /> Volver al inventario</label>
            </li>
          ))}
        </ul>
        <label className="flex items-center gap-2 text-sm"><Checkbox checked={refundNow} onCheckedChange={(c) => setRefundNow(!!c)} /> Reembolsar al cliente ahora</label>
        <DialogFooter><Button variant="outline" onClick={onClose}>Cerrar</Button><Button onClick={() => void go()} disabled={busy}>{busy ? "Guardando…" : "Confirmar recepción"}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Drafts({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "orders", "edit");
  const list = useQuery({ queryKey: ["drafts"], queryFn: () => sale.drafts("") });
  const [open, setOpen] = useState(false);
  const items = list.data ?? [];
  const refresh = () => void qc.invalidateQueries({ queryKey: ["drafts"] });
  const act = (p: Promise<unknown>, ok: string) => p.then(() => { toast.success(ok); refresh(); }).catch((e) => toast.error(errorMessage(e)));
  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="max-w-2xl text-sm text-muted-foreground">Arma un pedido por teléfono o WhatsApp y envíale al cliente un enlace para pagar. El stock se aparta cuando paga.</p>
        {canEdit && <Button onClick={() => setOpen(true)}><Plus aria-hidden="true" /> Nuevo pedido borrador</Button>}
      </div>
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      {!list.isLoading && items.length === 0 ? (
        <EmptyState icon={<FilePlus2 />} title="Sin pedidos borrador" text="Crea uno para vender por teléfono, WhatsApp o en mostrador." />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader><TableRow><TableHead>Borrador</TableHead><TableHead>Cliente</TableHead><TableHead>Productos</TableHead><TableHead>Estado</TableHead><TableHead className="text-right">Total</TableHead><TableHead><span className="sr-only">Acciones</span></TableHead></TableRow></TableHeader>
            <TableBody>
              {list.isLoading ? <SkeletonRows cols={6} /> : items.map((d) => (
                <TableRow key={d.id}>
                  <TableCell className="font-medium">B-{d.number}</TableCell>
                  <TableCell>{d.customer?.name || d.customer?.email || "—"}</TableCell>
                  <TableCell className="max-w-72 text-sm">{d.items.map((i) => `${i.qty} × ${i.title}`).join(", ")}</TableCell>
                  <TableCell><StatusPill status={d.status} /></TableCell>
                  <TableCell className="text-right tabular-nums">{formatMoney(d.grandTotal, d.currency)}</TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-2">
                      {d.orderId && <Button size="sm" variant="outline" asChild><a href={href("pedidos", d.orderId)}>Ver pedido</a></Button>}
                      {canEdit && (d.status === "open" || d.status === "sent") && <Button size="sm" onClick={() => void act(sale.sendDraft(d.id), "Enlace de pago enviado por correo.")}>{d.status === "sent" ? "Reenviar enlace" : "Enviar enlace de pago"}</Button>}
                      {canEdit && (d.status === "open" || d.status === "sent") && <Button size="sm" variant="ghost" aria-label={`Cancelar borrador ${d.number}`} onClick={() => void act(sale.cancelDraft(d.id), "Borrador cancelado.")}><Trash2 aria-hidden="true" /></Button>}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {open && <DraftDialog onClose={() => setOpen(false)} onDone={refresh} />}
    </div>
  );
}

function DraftDialog({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [rows, setRows] = useState([{ sku: "", qty: "1" }]);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [postal, setPostal] = useState("");
  const [shipping, setShipping] = useState("");
  const [discount, setDiscount] = useState("");
  const [note, setNote] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await sale.createDraft({
        channel: "mx", items: rows.filter((r) => r.sku.trim()).map((r) => ({ sku: r.sku.trim(), qty: Math.max(1, Number(r.qty) || 1) })),
        customer: { email: email.trim(), name: name.trim() }, postalCode: postal.trim(), shipping: parsePesos(shipping) ?? 0, discount: parsePesos(discount) ?? 0, note: note.trim(),
      });
      toast.success("Pedido borrador creado.");
      onDone();
      onClose();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-xl overflow-y-auto">
        <form onSubmit={(e) => void submit(e)} className="grid gap-4">
          <DialogHeader><DialogTitle>Nuevo pedido borrador</DialogTitle><DialogDescription>Los precios se toman del catálogo. Después podrás enviar el enlace de pago.</DialogDescription></DialogHeader>
          <div className="grid gap-2">
            <Label>Productos</Label>
            {rows.map((r, i) => (
              <div key={i} className="flex gap-2">
                <Input aria-label={`SKU ${i + 1}`} className="font-mono" placeholder="SKU" value={r.sku} onChange={(e) => setRows(rows.map((x, j) => (j === i ? { ...x, sku: e.target.value } : x)))} />
                <Input aria-label={`Cantidad ${i + 1}`} className="w-20" inputMode="numeric" value={r.qty} onChange={(e) => setRows(rows.map((x, j) => (j === i ? { ...x, qty: e.target.value } : x)))} />
                <Button type="button" variant="ghost" size="icon" aria-label="Quitar" disabled={rows.length === 1} onClick={() => setRows(rows.filter((_, j) => j !== i))}><X aria-hidden="true" /></Button>
              </div>
            ))}
            <div><Button type="button" variant="outline" size="sm" onClick={() => setRows([...rows, { sku: "", qty: "1" }])}><Plus aria-hidden="true" /> Agregar producto</Button></div>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="grid gap-1.5"><Label htmlFor="d-email">Correo del cliente</Label><Input id="d-email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} /></div>
            <div className="grid gap-1.5"><Label htmlFor="d-name">Nombre</Label><Input id="d-name" required value={name} onChange={(e) => setName(e.target.value)} /></div>
            <div className="grid gap-1.5"><Label htmlFor="d-postal">Código postal</Label><Input id="d-postal" inputMode="numeric" maxLength={5} value={postal} onChange={(e) => setPostal(e.target.value)} /></div>
            <div className="grid gap-1.5"><Label htmlFor="d-ship">Envío (MXN)</Label><Input id="d-ship" inputMode="decimal" placeholder="0.00" value={shipping} onChange={(e) => setShipping(e.target.value)} /></div>
            <div className="grid gap-1.5"><Label htmlFor="d-disc">Descuento (MXN)</Label><Input id="d-disc" inputMode="decimal" placeholder="0.00" value={discount} onChange={(e) => setDiscount(e.target.value)} /></div>
            <div className="grid gap-1.5"><Label htmlFor="d-note">Nota</Label><Input id="d-note" value={note} onChange={(e) => setNote(e.target.value)} /></div>
          </div>
          <ErrorBox error={error} />
          <DialogFooter><Button type="button" variant="outline" onClick={onClose}>Cancelar</Button><Button type="submit" disabled={busy}>{busy ? "Creando…" : "Crear borrador"}</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
