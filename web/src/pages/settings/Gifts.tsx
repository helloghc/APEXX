import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { ExternalLink } from "lucide-react";
import { errorMessage, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { centsToPesos, parsePesos } from "@/money";
import { can } from "@/perms";
import { href } from "@/router";
import { ErrorBox } from "../common";
import { Loading, Section, SubPage } from "./kit";

interface Options { enabled: boolean; wrapPrice: number; messageMax: number; receipt: boolean; registries: boolean }
interface Item { sku: string; title: string; price: string; wanted: number; bought: number; available: boolean }
interface Registry { id: string; handle: string; title: string; kind: string; eventDate: string; status: "active" | "closed"; listed: boolean; hasAddress: boolean; customerId?: string; wanted: number; bought: number; createdAt: string; items?: Item[] }
const KINDS: Record<string, string> = { boda: "Boda", bebe: "Bebé", cumple: "Cumpleaños", otro: "Otra" };

/** Gifts: wrapping and card messages at checkout, gift receipts and gift registries. */
export function Gifts({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "settings", "admin");
  const canSee = can(me, "customers", "read");
  const q = useQuery({ queryKey: ["gift-options"], queryFn: () => request<Options>("GET", "/v1/gift-options") });
  const regs = useQuery({ queryKey: ["gift-registries"], enabled: canSee, queryFn: () => request<{ items: Registry[] }>("GET", "/v1/gift-registries").then((r) => r.items) });
  const [v, setV] = useState({ enabled: false, wrap: "0", max: "250", receipt: true, registries: false });
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState<{ registry: Registry; orders: string[]; url: string } | null>(null);
  useEffect(() => {
    const o = q.data;
    if (o) setV({ enabled: o.enabled, wrap: centsToPesos(o.wrapPrice), max: String(o.messageMax || 250), receipt: o.receipt, registries: o.registries });
  }, [q.data]);
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  async function save() {
    setBusy(true);
    try {
      await request("PUT", "/v1/gift-options", { enabled: v.enabled, wrapPrice: parsePesos(v.wrap) ?? 0, messageMax: Number(v.max) || 250, receipt: v.receipt, registries: v.registries });
      toast.success("Opciones de regalo guardadas.");
      void qc.invalidateQueries({ queryKey: ["gift-options"] });
    } catch (e) { toast.error(errorMessage(e)); } finally { setBusy(false); }
  }
  async function show(id: string) {
    try { setOpen(await request("GET", `/v1/gift-registries/${id}`)); } catch (e) { toast.error(errorMessage(e)); }
  }
  async function setStatus(r: Registry, status: "active" | "closed") {
    try {
      setOpen(await request("PUT", `/v1/gift-registries/${r.id}/status`, { status }));
      toast.success(status === "closed" ? "Mesa cerrada: ya no recibe regalos." : "Mesa abierta de nuevo.");
      void qc.invalidateQueries({ queryKey: ["gift-registries"] });
    } catch (e) { toast.error(errorMessage(e)); }
  }
  const day = (d: string) => (d ? new Date(d + "T12:00:00").toLocaleDateString("es-MX", { dateStyle: "medium" }) : "Sin fecha");
  return (
    <SubPage title="Regalos" sub="Tus clientes marcan su compra como regalo: envoltura con su costo, mensaje para la tarjeta y ticket sin precios. Con la mesa de regalos, cada cliente arma su lista y la comparte; sus invitados compran de ella y pueden enviar el regalo a su dirección sin verla.">
      <Section title="Al pagar">
        <fieldset disabled={!canEdit} className="grid gap-3 sm:grid-cols-2">
          <label className="flex items-center gap-2 text-sm sm:col-span-2"><input type="checkbox" checked={v.enabled} onChange={(e) => setV({ ...v, enabled: e.target.checked })} /> Ofrecer «Es para regalo» en el pago</label>
          <div className="grid gap-1.5"><Label htmlFor="gf-wrap">Precio de la envoltura ($, 0 = gratis)</Label><Input id="gf-wrap" inputMode="decimal" value={v.wrap} onChange={(e) => setV({ ...v, wrap: e.target.value })} /></div>
          <div className="grid gap-1.5"><Label htmlFor="gf-max">Largo máximo del mensaje (caracteres)</Label><Input id="gf-max" inputMode="numeric" value={v.max} onChange={(e) => setV({ ...v, max: e.target.value.replace(/\D/g, "") })} /></div>
          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={v.receipt} onChange={(e) => setV({ ...v, receipt: e.target.checked })} /> Ofrecer ticket de regalo (sin precios)</label>
        </fieldset>
      </Section>
      <Section title="Mesa de regalos" description="Bodas, baby shower, cumpleaños: tus clientes crean su mesa desde su cuenta y agregan productos desde cada ficha.">
        <fieldset disabled={!canEdit}>
          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={v.registries} onChange={(e) => setV({ ...v, registries: e.target.checked })} /> Permitir mesas de regalos en mi tienda</label>
        </fieldset>
      </Section>
      {canEdit && <div><Button disabled={busy} onClick={() => void save()}>{busy ? "Guardando…" : "Guardar"}</Button></div>}

      {canSee && (
        <Section title="Mesas de tus clientes">
          {regs.isPending ? <Loading /> : regs.error ? <ErrorBox error={regs.error} /> : regs.data.length === 0 ? (
            <p className="text-sm text-muted-foreground">Todavía no hay mesas.</p>
          ) : (
            <table className="w-full text-sm">
              <thead className="text-left text-muted-foreground"><tr><th className="py-1">Mesa</th><th>Fecha</th><th>Regalados</th><th>Estado</th><th /></tr></thead>
              <tbody>
                {regs.data.map((r) => (
                  <tr key={r.id} className="border-t">
                    <td className="py-2"><div className="font-medium">{r.title}</div><div className="text-xs text-muted-foreground">{KINDS[r.kind] ?? r.kind}{r.hasAddress ? " · con dirección de entrega" : ""}</div></td>
                    <td>{day(r.eventDate)}</td>
                    <td className="tabular-nums">{r.bought} de {r.wanted}</td>
                    <td>{r.status === "active" ? "Activa" : "Cerrada"}</td>
                    <td className="text-right"><Button variant="outline" size="sm" onClick={() => void show(r.id)}>Ver</Button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Section>
      )}
      {open && (
        <Section title={open.registry.title} description={`${KINDS[open.registry.kind] ?? ""} · ${day(open.registry.eventDate)} · ${open.registry.bought} de ${open.registry.wanted} regalados`}
          actions={<div className="flex gap-2">
            <Button asChild variant="outline" size="sm"><a href={open.url} target="_blank" rel="noopener"><ExternalLink className="size-4" /> Ver en la tienda</a></Button>
            {can(me, "customers", "edit") && <Button variant="outline" size="sm" onClick={() => void setStatus(open.registry, open.registry.status === "active" ? "closed" : "active")}>{open.registry.status === "active" ? "Cerrar mesa" : "Abrir de nuevo"}</Button>}
            <Button variant="ghost" size="sm" onClick={() => setOpen(null)}>Cerrar</Button>
          </div>}>
          <table className="w-full text-sm">
            <thead className="text-left text-muted-foreground"><tr><th className="py-1">Producto</th><th>Pide</th><th>Comprados</th><th>Precio</th></tr></thead>
            <tbody>
              {(open.registry.items ?? []).map((i) => (
                <tr key={i.sku} className="border-t"><td className="py-2">{i.title} <span className="text-xs text-muted-foreground">{i.sku}</span>{!i.available && <span className="text-xs text-bad"> · agotado</span>}</td><td>{i.wanted}</td><td>{i.bought}</td><td>{i.price}</td></tr>
              ))}
            </tbody>
          </table>
          {open.orders.length > 0 && <p className="text-sm">Pedidos de regalo: {open.orders.map((id, n) => <span key={id}>{n > 0 && ", "}<a className="text-primary underline" href={href("pedidos", id)}>{id.slice(-6)}</a></span>)}</p>}
          {open.registry.customerId && <p className="text-sm"><a className="text-primary underline" href={href("clientes", open.registry.customerId)}>Ver a quien creó la mesa</a></p>}
        </Section>
      )}
    </SubPage>
  );
}
