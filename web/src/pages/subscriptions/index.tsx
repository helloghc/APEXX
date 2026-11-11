import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Plus, Repeat } from "lucide-react";
import { errorMessage, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "@/components/ui/sonner";
import { formatMoney } from "@/money";
import { can } from "@/perms";
import { href } from "@/router";
import { ProductPicker } from "../catalog/ProductPicker";
import { dateTime, ErrorBox, Notice, Page } from "../common";
import { EmptyState, SkeletonRows, SubNav } from "../promotions/shared";

const enc = encodeURIComponent;
interface Sub {
  id: string; status: string; plan: string; planLabel: string; discountBps: number; items: { sku: string; qty: number; title: string }[]; nextAt: string; cycle: number; failures: number; lastError?: string;
  customer?: { email: string; name: string }; createdAt: string; orders?: { orderId: string; number: number; total: number; status: string; cycle: number; at: string }[];
}
interface Config { enabled: boolean; discountBps: number; plans: string[]; productIds: string[] }

const api = {
  list: (status: string) => request<{ items: Sub[]; counts: Record<string, number> }>("GET", `/v1/subscriptions?status=${enc(status)}`),
  get: (id: string) => request<Sub>("GET", `/v1/subscriptions/${enc(id)}`),
  act: (id: string, action: string) => request<Sub>("POST", `/v1/subscriptions/${enc(id)}/${action}`, {}),
  config: () => request<{ config: Config; products: { id: string; title: string }[]; plans: { id: string; label: string }[] }>("GET", "/v1/subscription-config"),
  save: (c: Config) => request<{ config: Config }>("PUT", "/v1/subscription-config", c),
};
const TABS = [
  { key: "", label: "Suscripciones", route: ["suscripciones"] },
  { key: "ajustes", label: "Cómo se ofrecen", route: ["suscripciones", "ajustes"] },
];
const STATUS: [string, string][] = [["", "Todas"], ["active", "Activas"], ["paused", "En pausa"], ["past_due", "Con pago pendiente"], ["cancelled", "Canceladas"]];
const LABEL: Record<string, string> = { active: "Activa", paused: "En pausa", past_due: "Pago pendiente", cancelled: "Cancelada" };

/** Routes: #suscripciones, #suscripciones/ajustes. */
export function Subscriptions({ me, route }: { me: Me; route: string[] }) {
  return (
    <Page title="Suscripciones" sub="Compras que se repiten solas: tu cliente elige cada cuánto, se cobra a su tarjeta guardada y puede pausar o cancelar sin escribirte.">
      <SubNav items={TABS} active={route[1] ?? ""} label="Secciones" />
      {route[1] === "ajustes" ? <Settings canEdit={can(me, "orders", "admin")} /> : <List canEdit={can(me, "orders", "edit")} />}
    </Page>
  );
}

function List({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const [status, setStatus] = useState("");
  const q = useQuery({ queryKey: ["subscriptions", status], queryFn: () => api.list(status) });
  const [open, setOpen] = useState<string | null>(null);
  const items = q.data?.items ?? [];
  const counts = q.data?.counts ?? {};
  const all = Object.values(counts).reduce((a, b) => a + b, 0);
  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap gap-2" role="tablist" aria-label="Estado">
        {STATUS.map(([k, l]) => <Button key={k} role="tab" aria-selected={status === k} size="sm" variant={status === k ? "default" : "outline"} onClick={() => setStatus(k)}>{l} ({k ? counts[k] ?? 0 : all})</Button>)}
      </div>
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {!q.isLoading && items.length === 0 ? (
        <EmptyState icon={<Repeat />} title="Sin suscripciones aquí" text="Activa las suscripciones y elige qué productos las ofrecen. En la página del producto aparecerá la opción “Suscríbete”." action={<a className="text-primary underline" href={href("suscripciones", "ajustes")}>Configurar suscripciones</a>} />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader><TableRow><TableHead>Cliente</TableHead><TableHead>Productos</TableHead><TableHead>Frecuencia</TableHead><TableHead>Próxima entrega</TableHead><TableHead>Entregas</TableHead><TableHead>Estado</TableHead></TableRow></TableHeader>
            <TableBody>
              {q.isLoading ? <SkeletonRows cols={6} /> : items.map((s) => (
                <TableRow key={s.id} className="cursor-pointer" onClick={() => setOpen(s.id)}>
                  <TableCell><button type="button" className="text-left font-medium text-primary hover:underline" onClick={(e) => { e.stopPropagation(); setOpen(s.id); }}>{s.customer?.name || "Cliente"}</button><div className="text-xs text-muted-foreground">{s.customer?.email}</div></TableCell>
                  <TableCell className="max-w-64 truncate">{s.items.map((i) => `${i.qty} × ${i.title}`).join(", ")}</TableCell>
                  <TableCell>{s.planLabel}</TableCell>
                  <TableCell className="whitespace-nowrap">{s.status === "active" ? dateTime(s.nextAt) : "—"}</TableCell>
                  <TableCell className="tabular-nums">{s.cycle}</TableCell>
                  <TableCell>{LABEL[s.status] ?? s.status}{s.failures > 0 && s.status === "active" && <div className="text-xs text-warn">{s.failures} cobro(s) fallido(s)</div>}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {open && <Detail id={open} canEdit={canEdit} onClose={() => setOpen(null)} onDone={() => void qc.invalidateQueries({ queryKey: ["subscriptions"] })} />}
    </div>
  );
}

function Detail({ id, canEdit, onClose, onDone }: { id: string; canEdit: boolean; onClose: () => void; onDone: () => void }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["subscription", id], queryFn: () => api.get(id) });
  const act = useMutation({
    mutationFn: (a: string) => api.act(id, a),
    onSuccess: (_, a) => { toast.success({ pause: "Suscripción en pausa.", resume: "Suscripción reactivada.", cancel: "Suscripción cancelada.", skip: "Se saltó la próxima entrega.", charge: "La entrega se cobrará en el próximo minuto." }[a] ?? "Listo."); void qc.invalidateQueries({ queryKey: ["subscription", id] }); onDone(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const s = q.data;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader><DialogTitle>Suscripción de {s?.customer?.name ?? "cliente"}</DialogTitle><DialogDescription>{s ? `${LABEL[s.status] ?? s.status} · entrega ${s.planLabel}${s.discountBps ? ` · ${s.discountBps / 100}% de descuento` : ""} · desde ${dateTime(s.createdAt)}` : "Cargando…"}</DialogDescription></DialogHeader>
        <ErrorBox error={q.error} />
        {s && (
          <div className="grid gap-4">
            <ul className="grid gap-1 text-sm">{s.items.map((i) => <li key={i.sku}>{i.qty} × {i.title} <span className="font-mono text-xs text-muted-foreground">{i.sku}</span></li>)}</ul>
            {s.status === "active" && <p className="text-sm">Próxima entrega: <strong>{dateTime(s.nextAt)}</strong></p>}
            {s.lastError && s.status !== "cancelled" && <Notice tone="warn">Último intento de cobro: {s.lastError}{s.status === "past_due" && ". Le avisamos al cliente por correo; puede reactivarla desde su enlace."}</Notice>}
            <div className="max-h-56 overflow-auto rounded-md border">
              <Table>
                <TableHeader><TableRow><TableHead>Entrega</TableHead><TableHead>Pedido</TableHead><TableHead>Fecha</TableHead><TableHead className="text-right">Total</TableHead></TableRow></TableHeader>
                <TableBody>{(s.orders ?? []).map((o) => <TableRow key={o.orderId}><TableCell className="tabular-nums">{o.cycle}</TableCell><TableCell><a className="text-primary hover:underline" href={href("pedidos", o.orderId)}>#{o.number}</a></TableCell><TableCell className="whitespace-nowrap text-muted-foreground">{dateTime(o.at)}</TableCell><TableCell className="text-right tabular-nums">{formatMoney(o.total)}</TableCell></TableRow>)}</TableBody>
              </Table>
            </div>
            {canEdit && s.status !== "cancelled" && (
              <div className="flex flex-wrap gap-2 border-t pt-4">
                {s.status === "active" && <><Button variant="outline" disabled={act.isPending} onClick={() => act.mutate("charge")}>Entregar ahora</Button><Button variant="outline" disabled={act.isPending} onClick={() => act.mutate("skip")}>Saltar próxima entrega</Button><Button variant="outline" disabled={act.isPending} onClick={() => act.mutate("pause")}>Pausar</Button></>}
                {s.status !== "active" && <Button disabled={act.isPending} onClick={() => act.mutate("resume")}>Reactivar</Button>}
                <Button variant="outline" className="ml-auto text-destructive" disabled={act.isPending} onClick={() => window.confirm("¿Cancelar esta suscripción? No se harán más cobros.") && act.mutate("cancel")}>Cancelar suscripción</Button>
              </div>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function Settings({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["subscription-config"], queryFn: api.config });
  const [c, setC] = useState<Config | null>(null);
  const [pct, setPct] = useState("");
  const [names, setNames] = useState<Record<string, string>>({});
  const [picker, setPicker] = useState(false);
  useEffect(() => { if (q.data && !c) { setC(q.data.config); setPct(String(q.data.config.discountBps / 100)); setNames(Object.fromEntries(q.data.products.map((p) => [p.id, p.title]))); } }, [q.data, c]);
  const save = useMutation({
    mutationFn: () => api.save({ ...(c as Config), discountBps: Math.round((Number(pct.replace(",", ".")) || 0) * 100) }),
    onSuccess: () => { toast.success("Guardado. Ya aplica en tu tienda."); void qc.invalidateQueries({ queryKey: ["subscription-config"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  if (q.error) return <ErrorBox error={q.error} onRetry={() => void q.refetch()} />;
  if (!c) return <p className="text-sm text-muted-foreground">Cargando…</p>;
  return (
    <fieldset disabled={!canEdit} className="grid max-w-3xl gap-5">
      <Card>
        <CardHeader><CardTitle className="text-base">Oferta de suscripción</CardTitle><CardDescription>El primer pedido se paga en el checkout normal; las siguientes entregas se cobran solas a la tarjeta guardada en Stripe. Si un cobro falla se reintenta tres días seguidos y después se le avisa al cliente.</CardDescription></CardHeader>
        <CardContent className="grid gap-4">
          <label className="flex items-center gap-2 text-sm font-medium"><Checkbox checked={c.enabled} onCheckedChange={(v) => setC({ ...c, enabled: !!v })} /> Ofrecer suscripciones en mi tienda</label>
          <div className="grid max-w-xs gap-1.5"><Label htmlFor="sb-pct">Descuento en cada entrega (%)</Label><Input id="sb-pct" inputMode="decimal" value={pct} onChange={(e) => setPct(e.target.value)} /><p className="text-xs text-muted-foreground">0 para no dar descuento.</p></div>
          <div className="grid gap-2"><span className="text-sm font-medium">Frecuencias que puede elegir el cliente</span>
            <div className="flex flex-wrap gap-3">{(q.data?.plans ?? []).map((p) => <label key={p.id} className="flex items-center gap-2 text-sm"><Checkbox checked={c.plans.includes(p.id)} onCheckedChange={(v) => setC({ ...c, plans: v ? [...c.plans, p.id] : c.plans.filter((x) => x !== p.id) })} /> {p.label}</label>)}</div>
          </div>
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle className="text-base">Productos con suscripción</CardTitle><CardDescription>Solo estos productos muestran la opción “Suscríbete”. Ideal para consumibles: café, suplementos, alimento para mascotas, cuidado personal.</CardDescription></CardHeader>
        <CardContent className="grid gap-3">
          <div className="flex flex-wrap gap-1.5">{c.productIds.length === 0 ? <span className="text-sm text-muted-foreground">Todavía no eliges productos.</span> : c.productIds.map((id) => (
            <span key={id} className="inline-flex items-center gap-1 rounded-md border bg-muted px-2 py-1 text-sm">{names[id] ?? id}<button type="button" aria-label={`Quitar ${names[id] ?? id}`} className="text-muted-foreground hover:text-foreground" onClick={() => setC({ ...c, productIds: c.productIds.filter((x) => x !== id) })}>×</button></span>
          ))}</div>
          <div><Button type="button" variant="outline" onClick={() => setPicker(true)}><Plus aria-hidden="true" /> Agregar productos</Button></div>
        </CardContent>
      </Card>
      {canEdit ? <div><Button onClick={() => save.mutate()} disabled={save.isPending}>{save.isPending ? "Guardando…" : "Guardar"}</Button></div> : <Notice tone="warn">Solo un administrador de pedidos puede cambiar esto.</Notice>}
      <ProductPicker open={picker} title="Productos con suscripción" exclude={c.productIds} onClose={() => setPicker(false)} onPick={(ps) => { setNames({ ...names, ...Object.fromEntries(ps.map((p) => [p.id, p.title])) }); setC({ ...c, productIds: [...c.productIds, ...ps.map((p) => p.id)] }); setPicker(false); }} />
    </fieldset>
  );
}
