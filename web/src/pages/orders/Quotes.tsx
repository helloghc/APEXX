import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { FileText } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { centsToPesos, formatMoney, parsePesos, readNumber, readPesos } from "@/money";
import { can } from "@/perms";
import { href } from "@/router";
import { dateTime, ErrorBox, StatusPill } from "../common";
import { EmptyState, SkeletonRows } from "../promotions/shared";
import { download } from "../reports/api";

interface Item { sku: string; qty: number; unit?: number }
interface Event { at: string; who: string; kind: string; message?: string; total?: number }
interface Offer { id: string; items: { sku: string; title: string; qty: number; unit: number; total: number }[]; itemsTotal: number; discount: number; shipping: number; grandTotal: number; currency: string; postalCode: string }
interface Quote { id: string; number: number; status: string; channel: string; companyId?: string; customer?: { email: string; name: string; phone?: string }; items: Item[]; offer?: Offer; orderId?: string; expiresAt?: string; history: Event[]; createdAt: string; comments?: { id: string; sku: string; who: "buyer" | "store"; author: string; body: string; at: string }[] }

const STATUS: Record<string, string> = { requested: "Por responder", offered: "Oferta enviada", accepted: "Aceptada", rejected: "Rechazada", expired: "Vencida", cancelled: "Cancelada" };
const KIND: Record<string, string> = { requested: "pidió cotización", offered: "envió oferta", changes: "pidió cambios", accepted: "aceptó", rejected: "rechazó", expired: "venció", cancelled: "canceló" };

/** Quotes: buyers ask for a price, the store answers with an offer. */
export function Quotes({ me, id }: { me: Me; id?: string }) {
  if (id) return <QuoteDetail me={me} id={id} />;
  return <QuoteList />;
}

function QuoteList() {
  const [status, setStatus] = useState("requested");
  const q = useQuery({ queryKey: ["quotes", status], queryFn: () => request<{ items: Quote[] }>("GET", `/v1/quotes?status=${status}`).then((r) => r.items) });
  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap gap-2" role="tablist" aria-label="Estado de las cotizaciones">
        {["requested", "offered", "accepted", ""].map((s) => <Button key={s || "all"} size="sm" role="tab" aria-selected={status === s} variant={status === s ? "default" : "outline"} onClick={() => setStatus(s)}>{s ? STATUS[s] : "Todas"}</Button>)}
      </div>
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {q.isPending ? <SkeletonRows cols={5} /> : (q.data?.length ?? 0) === 0 ? (
        <EmptyState icon={<FileText />} title="Sin cotizaciones" text="Cuando un comprador pida una cotización desde su carrito, aparecerá aquí para que le respondas con tu precio." />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card"><Table>
          <TableHeader><TableRow><TableHead>Cotización</TableHead><TableHead>Cliente</TableHead><TableHead>Estado</TableHead><TableHead className="text-right">Oferta</TableHead><TableHead>Fecha</TableHead></TableRow></TableHeader>
          <TableBody>{q.data!.map((x) => (
            <TableRow key={x.id}>
              <TableCell><a className="font-medium text-primary hover:underline" href={href("pedidos", "cotizaciones", x.id)}>#{x.number}</a>{x.companyId && <span className="ml-2 text-xs text-muted-foreground">empresa</span>}</TableCell>
              <TableCell>{x.customer?.name ?? "—"}</TableCell>
              <TableCell>{STATUS[x.status] ?? x.status}</TableCell>
              <TableCell className="text-right tabular-nums">{x.offer ? formatMoney(x.offer.grandTotal, x.offer.currency) : "—"}</TableCell>
              <TableCell className="text-muted-foreground">{dateTime(x.createdAt)}</TableCell>
            </TableRow>
          ))}</TableBody>
        </Table></div>
      )}
    </div>
  );
}

function QuoteDetail({ me, id }: { me: Me; id: string }) {
  const qc = useQueryClient();
  const canEdit = can(me, "orders", "edit");
  const q = useQuery({ queryKey: ["quote", id], queryFn: () => request<Quote>("GET", `/v1/quotes/${encodeURIComponent(id)}`) });
  const titles = useQuery({ queryKey: ["quote", id, "titles"], enabled: !!q.data, queryFn: () => request<{ lines: { sku: string; title: string; unit: { amount: number } }[] }>("POST", "/v1/carts/simulate", { channel: q.data!.channel, items: q.data!.items.map((i) => ({ sku: i.sku, qty: i.qty })) }).catch(() => ({ lines: [] })) });
  const [prices, setPrices] = useState<Record<string, string>>({});
  const [ship, setShip] = useState("0");
  const [disc, setDisc] = useState("0");
  const [days, setDays] = useState("7");
  const [msg, setMsg] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  useEffect(() => {
    const d = q.data;
    if (!d) return;
    if (d.offer) {
      setPrices(Object.fromEntries(d.offer.items.map((i) => [i.sku, centsToPesos(i.unit)])));
      setShip(centsToPesos(d.offer.shipping)); setDisc(centsToPesos(d.offer.discount));
    } else if (titles.data) {
      setPrices(Object.fromEntries(titles.data.lines.map((l) => [l.sku, centsToPesos(l.unit.amount)])));
    }
  }, [q.data, titles.data]);
  const offer = useMutation({
    mutationFn: () => request<Quote>("POST", `/v1/quotes/${encodeURIComponent(id)}/offer`, {
      items: q.data!.items.map((i) => ({ sku: i.sku, qty: i.qty, unit: parsePesos(prices[i.sku] ?? "") ?? undefined })),
      shipping: readPesos(ship, "Envío (MXN)"), discount: readPesos(disc, "Descuento"), validDays: readNumber(days, "Vigencia (días)"), message: msg, postalCode: q.data!.offer?.postalCode ?? "",
    }),
    onSuccess: () => { toast.success("Oferta enviada al cliente por correo."); setErrors({}); setMsg(""); void qc.invalidateQueries({ queryKey: ["quote", id] }); void qc.invalidateQueries({ queryKey: ["quotes"] }); },
    onError: (e) => { setErrors(fieldErrors(e)); toast.error(errorMessage(e)); },
  });
  const cancel = useMutation({
    mutationFn: () => request<void>("POST", `/v1/quotes/${encodeURIComponent(id)}/cancel`, {}, { as: "none" }),
    onSuccess: () => { toast.success("Cotización cancelada."); void qc.invalidateQueries({ queryKey: ["quote", id] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  if (q.error) return <ErrorBox error={q.error} onRetry={() => void q.refetch()} />;
  if (!q.data) return <p className="text-sm text-muted-foreground">Cargando cotización…</p>;
  const d = q.data;
  const open = d.status === "requested" || d.status === "offered";
  const title = (sku: string) => titles.data?.lines.find((l) => l.sku === sku)?.title ?? d.offer?.items.find((i) => i.sku === sku)?.title ?? sku;
  const subtotal = d.items.reduce((a, i) => a + (parsePesos(prices[i.sku] ?? "") ?? 0) * i.qty, 0);
  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center gap-2"><a className="text-sm text-muted-foreground hover:underline" href={href("pedidos", "cotizaciones")}>← Cotizaciones</a><h2 className="text-xl font-semibold">Cotización #{d.number}</h2><StatusPill status={d.status} /><span className="text-sm text-muted-foreground">{STATUS[d.status]}</span>{d.orderId && <a className="text-sm text-primary hover:underline" href={href("pedidos", d.orderId)}>Ver pedido</a>}</div>
      <div className="grid gap-4 lg:grid-cols-[1fr_320px]">
        <Card><CardHeader><CardTitle className="text-base">{open && canEdit ? "Tu oferta" : "Productos"}</CardTitle></CardHeader>
          <CardContent className="grid gap-3">
            <div className="overflow-x-auto"><Table>
              <TableHeader><TableRow><TableHead>Producto</TableHead><TableHead className="text-right">Cantidad</TableHead><TableHead className="text-right">Precio unitario</TableHead></TableRow></TableHeader>
              <TableBody>{d.items.map((i, n) => (
                <TableRow key={i.sku}><TableCell>{title(i.sku)}<span className="block font-mono text-xs text-muted-foreground">{i.sku}</span></TableCell><TableCell className="text-right tabular-nums">{i.qty}</TableCell>
                  <TableCell className="text-right">{open && canEdit ? <Input aria-label={`Precio de ${title(i.sku)}`} className="ml-auto w-32 text-right" inputMode="decimal" value={prices[i.sku] ?? ""} onChange={(e) => setPrices((p) => ({ ...p, [i.sku]: e.target.value }))} /> : d.offer ? formatMoney(d.offer.items.find((x) => x.sku === i.sku)?.unit ?? 0, d.offer.currency) : "—"}{errors[`items[${n}].unit`] && <span className="block text-sm text-bad">{errors[`items[${n}].unit`]}</span>}</TableCell></TableRow>
              ))}</TableBody>
            </Table></div>
            {open && canEdit && <>
              <div className="grid gap-3 sm:grid-cols-3">
                <div className="grid gap-1.5"><Label htmlFor="qo-ship">Envío (MXN)</Label><Input id="qo-ship" inputMode="decimal" value={ship} onChange={(e) => setShip(e.target.value)} /></div>
                <div className="grid gap-1.5"><Label htmlFor="qo-disc">Descuento (MXN)</Label><Input id="qo-disc" inputMode="decimal" value={disc} onChange={(e) => setDisc(e.target.value)} /></div>
                <div className="grid gap-1.5"><Label htmlFor="qo-days">Vigencia (días)</Label><Input id="qo-days" inputMode="numeric" value={days} onChange={(e) => setDays(e.target.value.replace(/\D/g, ""))} />{errors.validDays && <span className="text-sm text-bad">{errors.validDays}</span>}</div>
              </div>
              <div className="grid gap-1.5"><Label htmlFor="qo-msg">Mensaje para el cliente (opcional)</Label><Textarea id="qo-msg" rows={2} value={msg} onChange={(e) => setMsg(e.target.value)} /></div>
              <p className="text-sm">Total de la oferta: <strong className="tabular-nums">{formatMoney(subtotal - (parsePesos(disc) ?? 0) + (parsePesos(ship) ?? 0))}</strong></p>
              <div className="flex flex-wrap gap-2"><Button onClick={() => offer.mutate()} disabled={offer.isPending}>{offer.isPending ? "Enviando…" : d.status === "offered" ? "Enviar nueva oferta" : "Enviar oferta"}</Button><Button variant="outline" onClick={() => cancel.mutate()} disabled={cancel.isPending}>Cancelar cotización</Button></div>
              <p className="text-xs text-muted-foreground">El cliente recibe un correo con un enlace para aceptar, pedir cambios o rechazar. Si es una empresa con crédito, al aceptar el pedido se coloca a su crédito; si no, paga con tarjeta desde el enlace. Las existencias se apartan al pagar.</p>
            </>}
            {d.offer && !open && <p className="text-sm">Total ofrecido: <strong>{formatMoney(d.offer.grandTotal, d.offer.currency)}</strong></p>}
          </CardContent>
        </Card>
        <div className="grid content-start gap-4">
          <Card><CardHeader><CardTitle className="text-base">Cliente</CardTitle></CardHeader><CardContent className="grid gap-1 text-sm">
            <span className="font-medium">{d.customer?.name ?? "Datos ocultos"}</span>{d.customer && <><span>{d.customer.email}</span>{d.customer.phone && <span>{d.customer.phone}</span>}</>}
            {d.companyId && <a className="text-primary hover:underline" href={href("empresas", d.companyId)}>Ver empresa</a>}
            {d.expiresAt && d.status === "offered" && <span className="text-muted-foreground">Vigente hasta {dateTime(d.expiresAt)}</span>}
          </CardContent></Card>
          <Button variant="outline" onClick={() => void download(`/v1/quotes/${encodeURIComponent(d.id)}/pdf`, `cotizacion-${d.number}.pdf`).catch((e: unknown) => toast.error(errorMessage(e)))}>Descargar PDF</Button>
          <LineComments quote={d} canEdit={canEdit && (d.status === "requested" || d.status === "offered")} titles={Object.fromEntries((titles.data?.lines ?? []).map((l) => [l.sku, l.title]))} onDone={() => void qc.invalidateQueries({ queryKey: ["quote", id] })} />
          <Card><CardHeader><CardTitle className="text-base">Historial</CardTitle></CardHeader><CardContent><ol className="grid gap-2 text-sm">
            {d.history.map((h, i) => <li key={i}><span className="text-muted-foreground">{dateTime(h.at)}</span> · {h.who === "buyer" ? "Cliente" : "Tienda"} {KIND[h.kind] ?? h.kind}{h.total ? ` (${formatMoney(h.total)})` : ""}{h.message && <span className="block rounded bg-muted px-2 py-1">“{h.message}”</span>}</li>)}
          </ol></CardContent></Card>
        </div>
      </div>
    </div>
  );
}

/** LineComments: questions and answers about one product of the quote, from either side. */
function LineComments({ quote, canEdit, titles, onDone }: { quote: Quote; canEdit: boolean; titles: Record<string, string>; onDone: () => void }) {
  const [sku, setSku] = useState(quote.items[0]?.sku ?? "");
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(false);
  async function send() {
    setBusy(true);
    try { await request("POST", `/v1/quotes/${encodeURIComponent(quote.id)}/comments`, { sku, body }); setBody(""); onDone(); } catch (e) { toast.error(errorMessage(e)); } finally { setBusy(false); }
  }
  const list = quote.comments ?? [];
  return (
    <Card data-line-comments><CardHeader><CardTitle className="text-base">Comentarios por producto</CardTitle></CardHeader>
      <CardContent className="grid gap-3 text-sm">
        {list.length === 0 ? <p className="text-muted-foreground">Sin comentarios.</p> : <ol className="grid gap-2">{list.map((c) => (
          <li key={c.id}><span className="text-muted-foreground">{dateTime(c.at)} · {titles[c.sku] ?? c.sku}</span><span className="block rounded bg-muted px-2 py-1"><strong>{c.who === "buyer" ? "Cliente" : "Tienda"}{c.author ? ` (${c.author})` : ""}:</strong> {c.body}</span></li>
        ))}</ol>}
        {canEdit && <div className="grid gap-2">
          <select aria-label="Producto" className="h-9 rounded-md border bg-background px-2" value={sku} onChange={(e) => setSku(e.target.value)}>{quote.items.map((i) => <option key={i.sku} value={i.sku}>{titles[i.sku] ?? i.sku}</option>)}</select>
          <Textarea aria-label="Comentario" rows={2} maxLength={1000} value={body} onChange={(e) => setBody(e.target.value)} placeholder="Responde sobre este producto" />
          <Button size="sm" variant="outline" disabled={busy || !body.trim()} onClick={() => void send()}>Comentar</Button>
        </div>}
      </CardContent></Card>
  );
}
