import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Copy, Plus, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { centsToPesos, parsePesos } from "@/money";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { Loading, Section, SubPage } from "./kit";

interface Rule { id?: string; name: string; active: boolean; minTotal?: number; maxTotal?: number; postalPrefixes?: string[]; groups?: string[]; skus?: string[]; channels?: string[]; hidePayments?: string[]; hideShipping?: string[]; message?: string }
interface Row { name: string; active: boolean; min: string; max: string; postal: string; groups: string; skus: string; channels: string; pay: string[]; ship: string; message: string; id?: string }
interface Doc { rules: Rule[]; app: { active: boolean; name: string; endpoint: string }; paymentKinds: string[]; secret?: string; contract?: string }
const PAY: Record<string, string> = { card: "Tarjeta", oxxo: "OXXO", spei: "SPEI", credit: "Crédito de empresa", gift_card: "Tarjeta de regalo" };
const list = (t: string) => t.split(/[,\s]+/).map((x) => x.trim()).filter(Boolean);
const toRow = (r: Rule): Row => ({ id: r.id, name: r.name, active: r.active, min: r.minTotal ? centsToPesos(r.minTotal) : "", max: r.maxTotal ? centsToPesos(r.maxTotal) : "", postal: (r.postalPrefixes ?? []).join(", "), groups: (r.groups ?? []).join(", "), skus: (r.skus ?? []).join(", "), channels: (r.channels ?? []).join(", "), pay: r.hidePayments ?? [], ship: (r.hideShipping ?? []).join(", "), message: r.message ?? "" });
const toRule = (r: Row): Rule => ({ id: r.id, name: r.name, active: r.active, minTotal: r.min ? parsePesos(r.min) ?? 0 : 0, maxTotal: r.max ? parsePesos(r.max) ?? 0 : 0, postalPrefixes: list(r.postal), groups: list(r.groups), skus: list(r.skus), channels: list(r.channels), hidePayments: r.pay, hideShipping: list(r.ship), message: r.message });

/** Checkout rules: hide payment methods or delivery options for some carts, and optionally ask the store's app. */
export function CheckoutRules({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "settings", "admin");
  const q = useQuery({ queryKey: ["checkout-rules"], queryFn: () => request<Doc>("GET", "/v1/settings/checkout-rules") });
  const [rows, setRows] = useState<Row[]>([]);
  const [app, setApp] = useState({ active: false, name: "", endpoint: "" });
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (q.data) { setRows(q.data.rules.map(toRow)); setApp(q.data.app); } }, [q.data]);
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const set = (i: number, p: Partial<Row>) => setRows((x) => x.map((r, j) => (j === i ? { ...r, ...p } : r)));
  async function save() {
    setBusy(true); setErrs({});
    try {
      await request("PUT", "/v1/settings/checkout-rules", { rules: rows.map(toRule), app });
      toast.success("Reglas guardadas. El carrito ya las aplica.");
      void qc.invalidateQueries({ queryKey: ["checkout-rules"] });
    } catch (e) {
      setErrs(fieldErrors(e)); toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <SubPage title="Reglas de pago y envío" sub="Oculta formas de pago u opciones de entrega para algunos pedidos, por ejemplo «sin OXXO arriba de $10,000» o «sin envío exprés a ciertos códigos postales». Una app tuya también puede decidir; si tarda más de 600 ms o falla, se ignora y el pago sigue.">
      {rows.map((r, i) => (
        <Section key={i} title={r.name || `Regla ${i + 1}`} actions={canEdit && <Button variant="ghost" size="icon" aria-label={`Quitar ${r.name || "regla"}`} onClick={() => setRows((x) => x.filter((_, j) => j !== i))}><Trash2 className="size-4" /></Button>}>
          <fieldset disabled={!canEdit} className="grid gap-3 sm:grid-cols-3" data-rule>
            <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor={`cr-n-${i}`}>Nombre</Label><Input id={`cr-n-${i}`} value={r.name} onChange={(e) => set(i, { name: e.target.value })} />{errs[`rules[${i}].name`] && <p className="text-xs text-bad">{errs[`rules[${i}].name`]}</p>}</div>
            <label className="flex items-center gap-2 self-end text-sm"><input type="checkbox" checked={r.active} onChange={(e) => set(i, { active: e.target.checked })} /> Activa</label>
            <div className="grid gap-1.5"><Label htmlFor={`cr-min-${i}`}>Si el subtotal es desde</Label><Input id={`cr-min-${i}`} inputMode="decimal" value={r.min} onChange={(e) => set(i, { min: e.target.value })} placeholder="Cualquiera" /></div>
            <div className="grid gap-1.5"><Label htmlFor={`cr-max-${i}`}>hasta</Label><Input id={`cr-max-${i}`} inputMode="decimal" value={r.max} onChange={(e) => set(i, { max: e.target.value })} placeholder="Sin tope" /></div>
            <div className="grid gap-1.5"><Label htmlFor={`cr-cp-${i}`}>Códigos postales que empiezan con</Label><Input id={`cr-cp-${i}`} className="font-mono" value={r.postal} onChange={(e) => set(i, { postal: e.target.value })} placeholder="Todos" /></div>
            <div className="grid gap-1.5"><Label htmlFor={`cr-sku-${i}`}>Con alguno de estos SKU</Label><Input id={`cr-sku-${i}`} className="font-mono" value={r.skus} onChange={(e) => set(i, { skus: e.target.value })} placeholder="Cualquiera" /></div>
            <div className="grid gap-1.5"><Label htmlFor={`cr-g-${i}`}>Grupos de precio</Label><Input id={`cr-g-${i}`} className="font-mono" value={r.groups} onChange={(e) => set(i, { groups: e.target.value })} placeholder="Todos" /></div>
            <div className="grid gap-1.5"><Label htmlFor={`cr-ch-${i}`}>Canales</Label><Input id={`cr-ch-${i}`} className="font-mono" value={r.channels} onChange={(e) => set(i, { channels: e.target.value })} placeholder="Todos" /></div>
            <div className="grid gap-1.5 sm:col-span-3"><span className="text-sm font-medium">Entonces oculta estas formas de pago</span>
              <div className="flex flex-wrap gap-3 text-sm">{q.data.paymentKinds.map((k) => <label key={k} className="flex items-center gap-1.5"><input type="checkbox" checked={r.pay.includes(k)} onChange={(e) => set(i, { pay: e.target.checked ? [...r.pay, k] : r.pay.filter((x) => x !== k) })} /> {PAY[k] ?? k}</label>)}</div></div>
            <div className="grid gap-1.5"><Label htmlFor={`cr-sh-${i}`}>y estas opciones de entrega (id)</Label><Input id={`cr-sh-${i}`} className="font-mono" value={r.ship} onChange={(e) => set(i, { ship: e.target.value })} placeholder="express" /></div>
            <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor={`cr-m-${i}`}>Mensaje para el comprador (opcional)</Label><Input id={`cr-m-${i}`} value={r.message} onChange={(e) => set(i, { message: e.target.value })} /></div>
            {errs[`rules[${i}]`] && <p className="text-sm text-bad sm:col-span-3">{errs[`rules[${i}]`]}</p>}
          </fieldset>
        </Section>
      ))}
      {canEdit && <div><Button variant="outline" onClick={() => setRows((x) => [...x, { name: "", active: true, min: "", max: "", postal: "", groups: "", skus: "", channels: "", pay: [], ship: "", message: "" }])}><Plus className="size-4" /> Agregar regla</Button></div>}
      <Section title="App de reglas (opcional)" description="Tu sistema recibe cada carrito sin datos personales y responde qué ocultar. Tiene 600 ms; si no responde a tiempo, el carrito sigue con las reglas de arriba.">
        <fieldset disabled={!canEdit} className="grid gap-3 sm:grid-cols-3">
          <div className="grid gap-1.5"><Label htmlFor="cr-app-n">Nombre</Label><Input id="cr-app-n" value={app.name} onChange={(e) => setApp({ ...app, name: e.target.value })} /></div>
          <div className="grid gap-1.5"><Label htmlFor="cr-app-u">Dirección</Label><Input id="cr-app-u" value={app.endpoint} onChange={(e) => setApp({ ...app, endpoint: e.target.value })} placeholder="https://" />{errs["app.endpoint"] && <p className="text-xs text-bad">{errs["app.endpoint"]}</p>}</div>
          <label className="flex items-center gap-2 self-end text-sm"><input type="checkbox" checked={app.active} onChange={(e) => setApp({ ...app, active: e.target.checked })} /> Activa</label>
        </fieldset>
        {q.data.contract && <p className="text-xs text-muted-foreground">{q.data.contract}</p>}
        {q.data.secret && <div className="flex flex-wrap items-center gap-2 text-xs"><span>Secreto de firma:</span><code className="break-all rounded bg-muted px-2 py-1">{q.data.secret}</code><Button size="sm" variant="ghost" onClick={() => { void navigator.clipboard?.writeText(q.data.secret!); toast.success("Secreto copiado."); }}><Copy className="size-4" /></Button></div>}
      </Section>
      {canEdit && <div><Button disabled={busy} onClick={() => void save()}>{busy ? "Guardando…" : "Guardar reglas"}</Button></div>}
    </SubPage>
  );
}
