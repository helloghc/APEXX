import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Copy, Plus, Store, Truck } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "@/components/ui/sonner";
import { formatMoney, parsePesos } from "@/money";
import { can } from "@/perms";
import { href } from "@/router";
import { ProductPicker } from "../catalog/ProductPicker";
import { dateTime, ErrorBox, Notice, Page } from "../common";
import { EmptyState, SkeletonRows } from "../promotions/shared";

const enc = encodeURIComponent;
interface Seller { id: string; name: string; email: string; commissionBps: number; status: string; note: string; products: number; balance: number; sales: number; commission: number; payoutAccount: string; payoutAuto: boolean; payoutMin: number; payoutHoldDays: number; payable: number }
interface Payout { id: string; amount: number; status: string; destination: string; ref?: string; error?: string; actor: string; createdAt: string }
interface Entry { id: string; kind: string; orderId?: string; orderNumber?: number; gross: number; commission: number; net: number; note?: string; at: string }
interface Detail { seller: Seller; products: { id: string; title: string }[]; statement: Entry[] }
interface PortalProduct { id: string; title: string; status: string; variants: { sku: string; title: string; available: number; price: number }[] }
interface Line { orderId: string; number: string; at: string; line: number; sku: string; title: string; qty: number; pending: number; total: number; status: string; customer?: { name: string; phone?: string; address?: string }; postalCode?: string; shippingName?: string }

const api = {
  list: () => request<{ items: Seller[] }>("GET", "/v1/sellers").then((r) => r.items),
  get: (id: string) => request<Detail>("GET", `/v1/sellers/${enc(id)}`),
  save: (s: Partial<Seller>) => (s.id ? request<Seller>("PUT", `/v1/sellers/${enc(s.id)}`, s) : request<Seller>("POST", "/v1/sellers", s)),
  products: (id: string, productIds: string[]) => request<void>("PUT", `/v1/sellers/${enc(id)}/products`, { productIds }, { as: "none" }),
  entry: (id: string, b: { kind: string; amount: number; note: string }) => request<void>("POST", `/v1/sellers/${enc(id)}/entries`, b, { as: "none" }),
  payouts: (id: string) => request<{ items: Payout[] }>("GET", `/v1/sellers/${enc(id)}/payouts`).then((r) => r.items),
  payNow: (id: string) => request<Payout>("POST", `/v1/sellers/${enc(id)}/payouts`, {}),
  invite: (id: string, b: { email: string; name: string }) => request<{ link: string; emailed: boolean }>("POST", `/v1/sellers/${enc(id)}/invite`, b),
  me: () => request<{ seller: Seller; products: PortalProduct[]; statement: Entry[] }>("GET", "/v1/seller/me"),
  orders: (pending: boolean) => request<{ items: Line[] }>("GET", `/v1/seller/orders${pending ? "?pending=1" : ""}`).then((r) => r.items),
  ship: (orderId: string, b: { carrier: string; trackingNumber: string }) => request<unknown>("POST", `/v1/seller/orders/${enc(orderId)}/ship`, b),
};
const KIND: Record<string, string> = { sale: "Venta", refund: "Reembolso o cancelación", payout: "Pago al vendedor", adjust: "Ajuste", payout_failed: "Pago no enviado: saldo devuelto" };
const PAYOUT: Record<string, string> = { pending: "En camino", paid: "Enviado", failed: "No se pudo enviar" };
const pct = (bps: number) => `${bps / 100}%`;

function Statement({ rows, linkOrders }: { rows: Entry[]; linkOrders: boolean }) {
  if (rows.length === 0) return <p className="text-sm text-muted-foreground">Todavía no hay movimientos.</p>;
  return (
    <div className="max-h-80 overflow-auto rounded-md border">
      <Table>
        <TableHeader><TableRow><TableHead>Fecha</TableHead><TableHead>Movimiento</TableHead><TableHead className="text-right">Venta</TableHead><TableHead className="text-right">Comisión</TableHead><TableHead className="text-right">Para el vendedor</TableHead></TableRow></TableHeader>
        <TableBody>{rows.map((e) => (
          <TableRow key={e.id}>
            <TableCell className="whitespace-nowrap text-muted-foreground">{dateTime(e.at)}</TableCell>
            <TableCell>{KIND[e.kind] ?? e.kind}{e.orderNumber ? <> · {linkOrders && e.orderId ? <a className="text-primary hover:underline" href={href("pedidos", e.orderId)}>pedido #{e.orderNumber}</a> : `pedido #${e.orderNumber}`}</> : null}{e.note && <span className="text-muted-foreground"> · {e.note}</span>}</TableCell>
            <TableCell className="text-right tabular-nums">{e.gross ? formatMoney(e.gross) : "—"}</TableCell>
            <TableCell className="text-right tabular-nums">{e.commission ? formatMoney(e.commission) : "—"}</TableCell>
            <TableCell className={`text-right tabular-nums ${e.net < 0 ? "text-bad" : ""}`}>{formatMoney(e.net)}</TableCell>
          </TableRow>
        ))}</TableBody>
      </Table>
    </div>
  );
}

/** Route #marketplace (list) and #marketplace/<id> (seller). */
export function Marketplace({ me, route }: { me: Me; route: string[] }) {
  const isAdmin = can(me, "marketplace", "admin");
  if (route[1]) return <SellerPage id={route[1]} isAdmin={isAdmin} canEdit={can(me, "marketplace", "edit")} />;
  return <SellerList isAdmin={isAdmin} />;
}

function SellerList({ isAdmin }: { isAdmin: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["sellers"], queryFn: api.list });
  const [create, setCreate] = useState(false);
  const items = q.data ?? [];
  const owed = items.reduce((a, s) => a + Math.max(s.balance, 0), 0);
  return (
    <Page title="Marketplace" sub="Otros vendedores ofrecen sus productos en tu tienda. Tú cobras al cliente, te quedas tu comisión y llevas la cuenta de lo que le debes a cada uno."
      actions={isAdmin && <Button onClick={() => setCreate(true)}><Plus aria-hidden="true" /> Nuevo vendedor</Button>}>
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {items.length > 0 && (
        <div className="grid gap-3 sm:grid-cols-3">{[["Vendedores", String(items.length)], ["Ventas de vendedores", formatMoney(items.reduce((a, s) => a + s.sales, 0))], ["Por pagar a vendedores", formatMoney(owed)]].map(([l, v]) => (
          <Card key={l}><CardContent className="grid gap-1 pt-5"><span className="text-sm text-muted-foreground">{l}</span><span className="text-xl font-semibold tabular-nums">{v}</span></CardContent></Card>
        ))}</div>
      )}
      {!q.isLoading && items.length === 0 ? (
        <EmptyState icon={<Store />} title="Todavía no tienes vendedores" text="Da de alta a un vendedor, asígnale sus productos y define tu comisión. Sus ventas aparecerán aquí automáticamente." />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader><TableRow><TableHead>Vendedor</TableHead><TableHead>Comisión</TableHead><TableHead className="text-right">Productos</TableHead><TableHead className="text-right">Ventas</TableHead><TableHead className="text-right">Tu comisión</TableHead><TableHead className="text-right">Le debes</TableHead><TableHead>Estado</TableHead></TableRow></TableHeader>
            <TableBody>{q.isLoading ? <SkeletonRows cols={7} /> : items.map((s) => (
              <TableRow key={s.id}>
                <TableCell><a className="font-medium text-primary hover:underline" href={href("marketplace", s.id)}>{s.name}</a><div className="text-xs text-muted-foreground">{s.email}</div></TableCell>
                <TableCell>{pct(s.commissionBps)}</TableCell><TableCell className="text-right tabular-nums">{s.products}</TableCell>
                <TableCell className="text-right tabular-nums">{formatMoney(s.sales)}</TableCell><TableCell className="text-right tabular-nums">{formatMoney(s.commission)}</TableCell>
                <TableCell className={`text-right font-medium tabular-nums ${s.balance < 0 ? "text-bad" : ""}`}>{formatMoney(s.balance)}</TableCell>
                <TableCell>{s.status === "active" ? <Badge variant="ok">Activo</Badge> : <Badge variant="outline">En pausa</Badge>}</TableCell>
              </TableRow>
            ))}</TableBody>
          </Table>
        </div>
      )}
      {create && <SellerDialog onClose={() => setCreate(false)} onDone={() => void qc.invalidateQueries({ queryKey: ["sellers"] })} />}
    </Page>
  );
}

function SellerDialog({ seller, onClose, onDone }: { seller?: Seller; onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState(seller?.name ?? "");
  const [email, setEmail] = useState(seller?.email ?? "");
  const [rate, setRate] = useState(String((seller?.commissionBps ?? 1500) / 100));
  const [status, setStatus] = useState(seller?.status ?? "active");
  const [note, setNote] = useState(seller?.note ?? "");
  const [account, setAccount] = useState(seller?.payoutAccount ?? "");
  const [auto, setAuto] = useState(seller?.payoutAuto ?? false);
  const [min, setMin] = useState(String((seller?.payoutMin ?? 0) / 100));
  const [hold, setHold] = useState(String(seller?.payoutHoldDays ?? 7));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => api.save({ id: seller?.id, name, email, commissionBps: Math.round((Number(rate.replace(",", ".")) || 0) * 100), status, note,
      payoutAccount: account.trim(), payoutAuto: auto, payoutMin: Math.round((Number(min.replace(",", ".")) || 0) * 100), payoutHoldDays: Math.max(0, Math.round(Number(hold) || 0)) }),
    onSuccess: () => { toast.success("Vendedor guardado."); onDone(); onClose(); },
    onError: (e) => { setErrors(fieldErrors(e)); toast.error(errorMessage(e)); },
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader><DialogTitle>{seller ? "Editar vendedor" : "Nuevo vendedor"}</DialogTitle><DialogDescription>La comisión nueva aplica a las ventas futuras; lo ya vendido conserva la comisión con la que se vendió.</DialogDescription></DialogHeader>
        <form id="seller-form" className="grid gap-4 sm:grid-cols-2" onSubmit={(e: FormEvent) => { e.preventDefault(); save.mutate(); }}>
          <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="sl-name">Nombre comercial</Label><Input id="sl-name" value={name} onChange={(e) => setName(e.target.value)} aria-invalid={!!errors.name} />{errors.name && <p className="text-sm text-bad">{errors.name}</p>}</div>
          <div className="grid gap-1.5"><Label htmlFor="sl-email">Correo de contacto</Label><Input id="sl-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} />{errors.email && <p className="text-sm text-bad">{errors.email}</p>}</div>
          <div className="grid gap-1.5"><Label htmlFor="sl-rate">Tu comisión (%)</Label><Input id="sl-rate" inputMode="decimal" value={rate} onChange={(e) => setRate(e.target.value)} />{errors.commissionBps && <p className="text-sm text-bad">{errors.commissionBps}</p>}</div>
          {seller && <div className="grid gap-1.5"><span className="text-sm font-medium">Estado</span><Select value={status} onValueChange={setStatus}><SelectTrigger aria-label="Estado"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="active">Activo</SelectItem><SelectItem value="paused">En pausa (sin acceso al portal)</SelectItem></SelectContent></Select></div>}
          <fieldset className="grid gap-4 rounded-md border p-3 sm:col-span-2 sm:grid-cols-2">
            <legend className="px-1 text-sm font-medium">Pago al vendedor</legend>
            <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="sl-acct">Cuenta de Stripe del vendedor</Label><Input id="sl-acct" value={account} onChange={(e) => setAccount(e.target.value)} placeholder="acct_…" autoComplete="off" aria-invalid={!!errors.payoutAccount} /><p className="text-sm text-muted-foreground">Con ella puedes enviarle su dinero desde aquí. Sin ella, registra a mano la transferencia que hagas por fuera.</p>{errors.payoutAccount && <p className="text-sm text-bad">{errors.payoutAccount}</p>}</div>
            <div className="grid gap-1.5"><Label htmlFor="sl-hold">Días de espera por venta</Label><Input id="sl-hold" inputMode="numeric" value={hold} onChange={(e) => setHold(e.target.value)} aria-invalid={!!errors.payoutHoldDays} /><p className="text-sm text-muted-foreground">Así una devolución sale de su saldo y no de tu bolsillo.</p>{errors.payoutHoldDays && <p className="text-sm text-bad">{errors.payoutHoldDays}</p>}</div>
            <div className="grid gap-1.5"><Label htmlFor="sl-min">Pago mínimo (MXN)</Label><Input id="sl-min" inputMode="decimal" value={min} onChange={(e) => setMin(e.target.value)} aria-invalid={!!errors.payoutMin} />{errors.payoutMin && <p className="text-sm text-bad">{errors.payoutMin}</p>}</div>
            <label className="flex items-start gap-2 text-sm sm:col-span-2"><input type="checkbox" className="mt-1" checked={auto} onChange={(e) => setAuto(e.target.checked)} /><span>Pagarle en automático cuando su saldo disponible alcance el mínimo</span></label>
          </fieldset>
          <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="sl-note">Nota interna (cuenta bancaria, contrato…)</Label><Input id="sl-note" value={note} onChange={(e) => setNote(e.target.value)} /></div>
        </form>
        <DialogFooter><Button variant="outline" onClick={onClose}>Cancelar</Button><Button type="submit" form="seller-form" disabled={save.isPending}>{save.isPending ? "Guardando…" : "Guardar"}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function SellerPage({ id, isAdmin, canEdit }: { id: string; isAdmin: boolean; canEdit: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["seller", id], queryFn: () => api.get(id) });
  const [edit, setEdit] = useState(false);
  const [picker, setPicker] = useState(false);
  const [pay, setPay] = useState<"payout" | "adjust" | null>(null);
  const [invite, setInvite] = useState(false);
  const refresh = () => { void qc.invalidateQueries({ queryKey: ["seller", id] }); void qc.invalidateQueries({ queryKey: ["sellers"] }); };
  const payouts = useQuery({ queryKey: ["seller", id, "payouts"], queryFn: () => api.payouts(id) });
  const payNow = useMutation({
    mutationFn: () => api.payNow(id),
    onSuccess: (p) => { if (p.status === "failed") toast.error("Stripe no aceptó el envío. El saldo regresó al vendedor."); else toast.success(p.status === "paid" ? `Enviamos ${formatMoney(p.amount)} al vendedor.` : "El pago quedó en camino; lo reintentamos solos."); refresh(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const setProducts = useMutation({ mutationFn: (ids: string[]) => api.products(id, ids), onSuccess: () => { toast.success("Productos del vendedor actualizados."); refresh(); }, onError: (e) => toast.error(errorMessage(e)) });
  if (q.error || !q.data) return <Page title="Vendedor"><ErrorBox error={q.error} onRetry={() => void q.refetch()} />{!q.error && <p className="text-sm text-muted-foreground">Cargando…</p>}</Page>;
  const { seller: s, products, statement } = q.data;
  return (
    <Page title={s.name} sub={<><a className="hover:underline" href={href("marketplace")}>← Marketplace</a> · comisión {pct(s.commissionBps)} · {s.status === "active" ? "activo" : "en pausa"}</>}
      actions={isAdmin && <><Button variant="outline" onClick={() => setInvite(true)}>Dar acceso al portal</Button><Button variant="outline" onClick={() => setEdit(true)}>Editar</Button></>}>
      <div className="grid gap-3 sm:grid-cols-3">{[["Ventas", formatMoney(s.sales)], ["Tu comisión", formatMoney(s.commission)], ["Le debes", formatMoney(s.balance)]].map(([l, v]) => (
        <Card key={l}><CardContent className="grid gap-1 pt-5"><span className="text-sm text-muted-foreground">{l}</span><span className="text-xl font-semibold tabular-nums">{v}</span></CardContent></Card>
      ))}</div>
      <Card>
        <CardHeader className="flex-row flex-wrap items-start justify-between gap-3"><div className="grid gap-1.5"><CardTitle className="text-base">Estado de cuenta</CardTitle><CardDescription>Cada venta pagada suma; los reembolsos y cancelaciones restan en la misma proporción.</CardDescription></div>
          {isAdmin && <div className="flex gap-2"><Button variant="outline" onClick={() => setPay("adjust")}>Ajuste</Button><Button variant="outline" onClick={() => setPay("payout")} disabled={s.balance <= 0}>Registrar pago hecho por fuera</Button>{s.payoutAccount && <Button onClick={() => payNow.mutate()} disabled={s.payable <= 0 || payNow.isPending}>{payNow.isPending ? "Enviando…" : `Pagar ahora ${formatMoney(s.payable)}`}</Button>}</div>}</CardHeader>
        <CardContent className="grid gap-4">
          <p className="text-sm text-muted-foreground" data-testid="payout-summary">{s.payoutAccount ? <>Disponible para pagar hoy: <strong className="text-foreground">{formatMoney(s.payable)}</strong> · cada venta espera {s.payoutHoldDays} días · {s.payoutAuto ? `pago automático desde ${formatMoney(s.payoutMin)}` : "pago automático apagado"}</> : "Este vendedor no tiene cuenta de Stripe: edítalo para poder pagarle desde aquí."}</p>
          {(payouts.data?.length ?? 0) > 0 && (
            <div className="overflow-x-auto rounded-md border"><Table><TableHeader><TableRow><TableHead>Pago enviado</TableHead><TableHead>Estado</TableHead><TableHead>Referencia</TableHead><TableHead className="text-right">Monto</TableHead></TableRow></TableHeader>
              <TableBody>{payouts.data!.map((p) => (<TableRow key={p.id}><TableCell>{new Date(p.createdAt).toLocaleString("es-MX", { dateStyle: "medium", timeStyle: "short" })}</TableCell><TableCell>{PAYOUT[p.status] ?? p.status}{p.status === "failed" && p.error ? <span className="block text-sm text-muted-foreground">{p.error}</span> : null}</TableCell><TableCell className="font-mono text-sm">{p.ref || "—"}</TableCell><TableCell className="text-right tabular-nums">{formatMoney(p.amount)}</TableCell></TableRow>))}</TableBody></Table></div>
          )}
          <Statement rows={statement} linkOrders />
        </CardContent>
      </Card>
      <Card>
        <CardHeader className="flex-row flex-wrap items-start justify-between gap-3"><div className="grid gap-1.5"><CardTitle className="text-base">Productos del vendedor ({products.length})</CardTitle><CardDescription>En la tienda aparecen como “Vendido y enviado por {s.name}”. El vendedor ve sus pedidos en su portal y registra la guía.</CardDescription></div>
          {canEdit && <Button variant="outline" onClick={() => setPicker(true)}><Plus aria-hidden="true" /> Asignar productos</Button>}</CardHeader>
        <CardContent>
          {products.length === 0 ? <p className="text-sm text-muted-foreground">Sin productos asignados.</p> : (
            <ul className="grid divide-y rounded-md border">{products.map((p) => (
              <li key={p.id} className="flex items-center justify-between gap-3 px-4 py-2 text-sm"><a className="text-primary hover:underline" href={href("catalogo", "productos", p.id)}>{p.title}</a>
                {canEdit && <Button size="sm" variant="ghost" disabled={setProducts.isPending} onClick={() => setProducts.mutate(products.filter((x) => x.id !== p.id).map((x) => x.id))}>Quitar</Button>}</li>
            ))}</ul>
          )}
        </CardContent>
      </Card>
      {edit && <SellerDialog seller={s} onClose={() => setEdit(false)} onDone={refresh} />}
      {pay && <EntryDialog id={id} kind={pay} balance={s.balance} onClose={() => setPay(null)} onDone={refresh} />}
      {invite && <InviteDialog id={id} email={s.email} onClose={() => setInvite(false)} />}
      <ProductPicker open={picker} title={`Productos de ${s.name}`} exclude={products.map((p) => p.id)} onClose={() => setPicker(false)} onPick={(ps) => { setPicker(false); setProducts.mutate([...products.map((p) => p.id), ...ps.map((p) => p.id)]); }} />
    </Page>
  );
}

function EntryDialog({ id, kind, balance, onClose, onDone }: { id: string; kind: "payout" | "adjust"; balance: number; onClose: () => void; onDone: () => void }) {
  const [amount, setAmount] = useState(kind === "payout" ? (balance / 100).toFixed(2) : "");
  const [note, setNote] = useState("");
  const neg = amount.trim().startsWith("-");
  const cents = parsePesos(amount.replace(/^-/, ""));
  const save = useMutation({ mutationFn: () => api.entry(id, { kind, amount: (neg ? -1 : 1) * (cents ?? 0), note }), onSuccess: () => { toast.success(kind === "payout" ? "Pago registrado." : "Ajuste registrado."); onDone(); onClose(); }, onError: (e) => toast.error(errorMessage(e)) });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader><DialogTitle>{kind === "payout" ? "Registrar pago al vendedor" : "Ajuste al estado de cuenta"}</DialogTitle><DialogDescription>{kind === "payout" ? `Anota la transferencia que le hiciste. Le debes ${formatMoney(balance)}.` : "Usa un monto positivo a favor del vendedor o negativo en su contra (por ejemplo -150)."}</DialogDescription></DialogHeader>
        <form id="entry-form" className="grid gap-4" onSubmit={(e: FormEvent) => { e.preventDefault(); save.mutate(); }}>
          <div className="grid gap-1.5"><Label htmlFor="en-amount">Monto (MXN)</Label><Input id="en-amount" inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} /></div>
          <div className="grid gap-1.5"><Label htmlFor="en-note">{kind === "payout" ? "Referencia de la transferencia" : "Motivo"}</Label><Input id="en-note" value={note} onChange={(e) => setNote(e.target.value)} /></div>
        </form>
        <DialogFooter><Button variant="outline" onClick={onClose}>Cancelar</Button><Button type="submit" form="entry-form" disabled={save.isPending || !cents || !note.trim()}>Guardar</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function InviteDialog({ id, email: initial, onClose }: { id: string; email: string; onClose: () => void }) {
  const [email, setEmail] = useState(initial);
  const [name, setName] = useState("");
  const [res, setRes] = useState<{ link: string; emailed: boolean } | null>(null);
  const send = useMutation({ mutationFn: () => api.invite(id, { email, name }), onSuccess: setRes, onError: (e) => toast.error(errorMessage(e)) });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader><DialogTitle>Acceso al portal de vendedor</DialogTitle><DialogDescription>{res ? (res.emailed ? "Le enviamos el enlace por correo. También puedes copiarlo." : "Comparte este enlace con la persona.") : "La persona solo verá los productos, pedidos y estado de cuenta de este vendedor. Nada más de tu tienda."}</DialogDescription></DialogHeader>
        {res ? <div className="flex items-center gap-2"><Input readOnly value={res.link} aria-label="Enlace de invitación" className="font-mono text-xs" data-testid="seller-invite-link" /><Button variant="outline" onClick={() => void navigator.clipboard.writeText(res.link).then(() => toast.success("Enlace copiado."))}><Copy aria-hidden="true" /> Copiar</Button></div> : (
          <form id="sinv-form" className="grid gap-4" onSubmit={(e: FormEvent) => { e.preventDefault(); send.mutate(); }}>
            <div className="grid gap-1.5"><Label htmlFor="si-email">Correo</Label><Input id="si-email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} /></div>
            <div className="grid gap-1.5"><Label htmlFor="si-name">Nombre</Label><Input id="si-name" value={name} onChange={(e) => setName(e.target.value)} /></div>
          </form>
        )}
        <DialogFooter>{res ? <Button onClick={onClose}>Listo</Button> : <><Button variant="outline" onClick={onClose}>Cancelar</Button><Button type="submit" form="sinv-form" disabled={send.isPending}>Enviar invitación</Button></>}</DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** The whole admin for a person who works for a seller. */
export function SellerPortal() {
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ["seller-me"], queryFn: api.me });
  const orders = useQuery({ queryKey: ["seller-orders"], queryFn: () => api.orders(true), refetchInterval: 30_000 });
  const [ship, setShip] = useState<Line | null>(null);
  if (me.error) return <Page title="Portal de vendedor"><ErrorBox error={me.error} onRetry={() => void me.refetch()} /></Page>;
  if (!me.data) return <Page title="Portal de vendedor"><p className="text-sm text-muted-foreground">Cargando…</p></Page>;
  const { seller: s, products, statement } = me.data;
  const pending = orders.data ?? [];
  return (
    <Page title={s.name} sub={`Portal de vendedor · la tienda cobra ${pct(s.commissionBps)} de comisión por venta`}>
      <div className="grid gap-3 sm:grid-cols-3">{[["Por enviar", String(pending.length)], ["Ventas", formatMoney(s.sales)], ["Saldo a tu favor", formatMoney(s.balance)]].map(([l, v]) => (
        <Card key={l}><CardContent className="grid gap-1 pt-5"><span className="text-sm text-muted-foreground">{l}</span><span className="text-xl font-semibold tabular-nums">{v}</span></CardContent></Card>
      ))}</div>
      <Card>
        <CardHeader><CardTitle className="text-base">Pedidos por enviar</CardTitle><CardDescription>Empaca, envía y registra la guía: le avisamos al cliente con su enlace de rastreo.</CardDescription></CardHeader>
        <CardContent>
          <ErrorBox error={orders.error} onRetry={() => void orders.refetch()} />
          {pending.length === 0 ? <p className="text-sm text-muted-foreground">No tienes pedidos pendientes. ¡Todo enviado!</p> : (
            <ul className="grid gap-3">{pending.map((l) => (
              <li key={l.orderId + l.line} className="grid gap-2 rounded-md border p-3 sm:grid-cols-[1fr_auto] sm:items-center">
                <div className="grid gap-0.5 text-sm"><strong>Pedido #{l.number} · {l.pending} × {l.title}</strong><span className="font-mono text-xs text-muted-foreground">{l.sku} · {dateTime(l.at)}</span>
                  {l.customer && <span>Enviar a: {l.customer.name}{l.customer.phone ? ` · ${l.customer.phone}` : ""} · {l.customer.address} · CP {l.postalCode}</span>}{l.shippingName && <span className="text-muted-foreground">Entrega elegida: {l.shippingName}</span>}</div>
                <Button onClick={() => setShip(l)}><Truck aria-hidden="true" /> Registrar envío</Button>
              </li>
            ))}</ul>
          )}
        </CardContent>
      </Card>
      <Card><CardHeader><CardTitle className="text-base">Tu estado de cuenta</CardTitle><CardDescription>Lo que has vendido, la comisión de la tienda y los pagos que has recibido.</CardDescription></CardHeader><CardContent><Statement rows={statement} linkOrders={false} /></CardContent></Card>
      <Card>
        <CardHeader><CardTitle className="text-base">Tus productos</CardTitle><CardDescription>Para cambiar precios, existencias o fotos, escribe a la tienda.</CardDescription></CardHeader>
        <CardContent>
          {products.length === 0 ? <Notice tone="warn">La tienda todavía no te asigna productos.</Notice> : (
            <div className="overflow-x-auto rounded-md border"><Table><TableHeader><TableRow><TableHead>Producto</TableHead><TableHead>SKU</TableHead><TableHead className="text-right">Precio</TableHead><TableHead className="text-right">Disponibles</TableHead></TableRow></TableHeader>
              <TableBody>{products.flatMap((p) => p.variants.map((v) => <TableRow key={v.sku}><TableCell>{p.title} {v.title}{p.status !== "active" && <Badge variant="outline" className="ml-2">No publicado</Badge>}</TableCell><TableCell className="font-mono text-xs">{v.sku}</TableCell><TableCell className="text-right tabular-nums">{formatMoney(v.price)}</TableCell><TableCell className="text-right tabular-nums">{v.available === 0 ? <Badge variant="warn">Agotado</Badge> : v.available}</TableCell></TableRow>))}</TableBody></Table></div>
          )}
        </CardContent>
      </Card>
      {ship && <ShipDialog line={ship} onClose={() => setShip(null)} onDone={() => { void qc.invalidateQueries({ queryKey: ["seller-orders"] }); }} />}
    </Page>
  );
}

function ShipDialog({ line, onClose, onDone }: { line: Line; onClose: () => void; onDone: () => void }) {
  const [carrier, setCarrier] = useState("Estafeta");
  const [tracking, setTracking] = useState("");
  const send = useMutation({ mutationFn: () => api.ship(line.orderId, { carrier, trackingNumber: tracking.trim() }), onSuccess: () => { toast.success("Envío registrado. Le avisamos al cliente."); onDone(); onClose(); }, onError: (e) => toast.error(errorMessage(e)) });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader><DialogTitle>Enviar pedido #{line.number}</DialogTitle><DialogDescription>Se marcan como enviados todos tus productos de este pedido.</DialogDescription></DialogHeader>
        <form id="sship-form" className="grid gap-4" onSubmit={(e: FormEvent) => { e.preventDefault(); send.mutate(); }}>
          <div className="grid gap-1.5"><span className="text-sm font-medium">Paquetería</span><Select value={carrier} onValueChange={setCarrier}><SelectTrigger aria-label="Paquetería"><SelectValue /></SelectTrigger><SelectContent>{["Estafeta", "DHL", "FedEx", "Paquetexpress", "Redpack", "Correos de México", "Otra"].map((c) => <SelectItem key={c} value={c}>{c}</SelectItem>)}</SelectContent></Select></div>
          <div className="grid gap-1.5"><Label htmlFor="ss-track">Número de guía</Label><Input id="ss-track" value={tracking} onChange={(e) => setTracking(e.target.value)} required /></div>
        </form>
        <DialogFooter><Button variant="outline" onClick={onClose}>Cancelar</Button><Button type="submit" form="sship-form" disabled={send.isPending || !tracking.trim()}>Registrar envío</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
