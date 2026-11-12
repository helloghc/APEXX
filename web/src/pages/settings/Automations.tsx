import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Plus, RotateCcw, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { centsToPesos, parsePesos } from "@/money";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { Loading, Section, SubPage } from "./kit";

interface Action { kind: "note" | "tag_customer" | "webhook"; text?: string; tag?: string; url?: string }
interface Auto { id?: string; name: string; trigger: string; conditions: { minTotal?: number; channel?: string; skus?: string[] }; actions: Action[]; active: boolean; stats?: Record<string, number> }
interface Run { id: string; orderId: string; event: string; status: string; attempts: number; log: { action: string; result: string; at: string }[]; lastError?: string; createdAt: string }
const KIND: Record<Action["kind"], string> = { note: "Agregar nota interna al pedido", tag_customer: "Etiquetar al cliente", webhook: "Avisar a otro sistema (webhook)" };
const STATUS: Record<string, string> = { pending: "En espera", done: "Hecha", skipped: "No aplicó", failed: "Falló" };
const sel = "h-9 w-full rounded-md border bg-background px-2 text-sm";

/** Automations: "when X happens to an order, do Y", with history and retries. */
export function Automations({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "settings", "admin");
  const q = useQuery({ queryKey: ["automations"], queryFn: () => request<{ items: Auto[]; triggers: Record<string, string> }>("GET", "/v1/automations") });
  const [edit, setEdit] = useState<(Auto & { minText: string; skuText: string }) | null>(null);
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [open, setOpen] = useState("");
  const [busy, setBusy] = useState(false);
  const runs = useQuery({ queryKey: ["automation-runs", open], queryFn: () => request<{ items: Run[] }>("GET", `/v1/automations/${open}/runs`).then((r) => r.items), enabled: !!open, refetchInterval: 5000 });
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const triggers = q.data.triggers;
  const refresh = () => void qc.invalidateQueries({ queryKey: ["automations"] });
  const start = (a?: Auto) => { setErrs({}); setEdit(a ? { ...a, minText: a.conditions.minTotal ? centsToPesos(a.conditions.minTotal) : "", skuText: (a.conditions.skus ?? []).join(", ") } : { name: "", trigger: "payment.captured", conditions: {}, actions: [{ kind: "note", text: "" }], active: true, minText: "", skuText: "" }); };
  async function save() {
    if (!edit) return;
    setBusy(true); setErrs({});
    const { minText, skuText, stats: _s, ...a } = edit;
    const body = { ...a, conditions: { ...a.conditions, minTotal: minText ? parsePesos(minText) ?? 0 : 0, skus: skuText.split(/[,\s]+/).filter(Boolean) } };
    try {
      await request(a.id ? "PUT" : "POST", a.id ? `/v1/automations/${a.id}` : "/v1/automations", body);
      toast.success(a.active ? "Automatización activa: corre con los siguientes pedidos." : "Automatización guardada en pausa.");
      setEdit(null); refresh();
    } catch (e) {
      setErrs(fieldErrors(e)); toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  async function remove(a: Auto) {
    if (!window.confirm(`¿Borrar «${a.name}»? Su historial se conserva.`)) return;
    try { await request("DELETE", `/v1/automations/${a.id}`, undefined, { as: "none" }); toast.success("Automatización borrada."); refresh(); } catch (e) { toast.error(errorMessage(e)); }
  }
  async function retry(r: Run) {
    try { await request("POST", `/v1/automations/runs/${r.id}/retry`, undefined, { as: "none" }); toast.success("Se reintenta en unos segundos."); void runs.refetch(); } catch (e) { toast.error(errorMessage(e)); }
  }
  const setA = (i: number, p: Partial<Action>) => edit && setEdit({ ...edit, actions: edit.actions.map((x, j) => (j === i ? { ...x, ...p } : x)) });
  return (
    <SubPage title="Automatizaciones" sub="Cuando pase algo con un pedido, Apexx hace el resto: anotar, etiquetar al cliente o avisar a otro sistema. Cada ejecución queda en el historial y, si algo falla, se reintenta desde el paso que falló."
      actions={canEdit && !edit && <Button onClick={() => start()}><Plus className="size-4" /> Nueva automatización</Button>}>
      {edit && (
        <Section title={edit.id ? "Editar automatización" : "Nueva automatización"}>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="grid gap-1.5"><Label htmlFor="au-name">Nombre</Label><Input id="au-name" value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} placeholder="Clientes VIP" />{errs.name && <p className="text-xs text-bad">{errs.name}</p>}</div>
            <div className="grid gap-1.5"><Label htmlFor="au-trigger">Cuando</Label><select id="au-trigger" className={sel} value={edit.trigger} onChange={(e) => setEdit({ ...edit, trigger: e.target.value })}>{Object.entries(triggers).map(([k, v]) => <option key={k} value={k}>{v}</option>)}</select></div>
          </div>
          <fieldset className="grid gap-3 rounded-md border p-3 sm:grid-cols-3"><legend className="px-1 text-sm font-medium">Solo si (opcional)</legend>
            <div className="grid gap-1.5"><Label htmlFor="au-min">Total mínimo</Label><Input id="au-min" inputMode="decimal" value={edit.minText} onChange={(e) => setEdit({ ...edit, minText: e.target.value })} placeholder="0.00" /></div>
            <div className="grid gap-1.5"><Label htmlFor="au-ch">Canal</Label><Input id="au-ch" className="font-mono" value={edit.conditions.channel ?? ""} onChange={(e) => setEdit({ ...edit, conditions: { ...edit.conditions, channel: e.target.value.trim() } })} placeholder="Todos" /></div>
            <div className="grid gap-1.5"><Label htmlFor="au-sku">Con alguno de estos SKU</Label><Input id="au-sku" className="font-mono" value={edit.skuText} onChange={(e) => setEdit({ ...edit, skuText: e.target.value })} placeholder="Cualquiera" /></div>
          </fieldset>
          <div className="grid gap-2"><p className="text-sm font-medium">Haz esto, en orden</p>
            {edit.actions.map((x, i) => (
              <div key={i} className="grid gap-2 rounded-md border p-3 sm:grid-cols-[16rem_1fr_auto] sm:items-end" data-auto-action>
                <div className="grid gap-1.5"><Label htmlFor={`au-k-${i}`}>Acción {i + 1}</Label><select id={`au-k-${i}`} className={sel} value={x.kind} onChange={(e) => setA(i, { kind: e.target.value as Action["kind"] })}>{Object.entries(KIND).map(([k, v]) => <option key={k} value={k}>{v}</option>)}</select></div>
                {x.kind === "note" && <div className="grid gap-1.5"><Label htmlFor={`au-t-${i}`}>Nota (puedes usar {"{{pedido.numero}}"} y {"{{pedido.total}}"})</Label><Input id={`au-t-${i}`} value={x.text ?? ""} onChange={(e) => setA(i, { text: e.target.value })} />{errs[`actions[${i}].text`] && <p className="text-xs text-bad">{errs[`actions[${i}].text`]}</p>}</div>}
                {x.kind === "tag_customer" && <div className="grid gap-1.5"><Label htmlFor={`au-g-${i}`}>Etiqueta</Label><Input id={`au-g-${i}`} value={x.tag ?? ""} onChange={(e) => setA(i, { tag: e.target.value })} placeholder="vip" />{errs[`actions[${i}].tag`] && <p className="text-xs text-bad">{errs[`actions[${i}].tag`]}</p>}</div>}
                {x.kind === "webhook" && <div className="grid gap-1.5"><Label htmlFor={`au-u-${i}`}>Dirección (https). Va firmada y sin datos personales del comprador.</Label><Input id={`au-u-${i}`} value={x.url ?? ""} onChange={(e) => setA(i, { url: e.target.value })} placeholder="https://" />{errs[`actions[${i}].url`] && <p className="text-xs text-bad">{errs[`actions[${i}].url`]}</p>}</div>}
                <Button type="button" variant="ghost" size="icon" aria-label={`Quitar acción ${i + 1}`} disabled={edit.actions.length === 1} onClick={() => setEdit({ ...edit, actions: edit.actions.filter((_, j) => j !== i) })}><Trash2 className="size-4" /></Button>
              </div>
            ))}
            {edit.actions.length < 10 && <div><Button type="button" variant="outline" size="sm" onClick={() => setEdit({ ...edit, actions: [...edit.actions, { kind: "tag_customer", tag: "" }] })}><Plus className="size-4" /> Agregar acción</Button></div>}
          </div>
          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={edit.active} onChange={(e) => setEdit({ ...edit, active: e.target.checked })} /> Activa</label>
          <div className="flex gap-2"><Button disabled={busy} onClick={() => void save()}>{busy ? "Guardando…" : "Guardar"}</Button><Button variant="ghost" onClick={() => setEdit(null)}>Cancelar</Button></div>
        </Section>
      )}
      {q.data.items.length === 0 && !edit && <p className="text-sm text-muted-foreground">Aún no hay automatizaciones.</p>}
      {q.data.items.map((a) => (
        <Section key={a.id} title={`${a.name}${a.active ? "" : " (en pausa)"}`} description={`${triggers[a.trigger] ?? a.trigger} → ${a.actions.map((x) => KIND[x.kind]?.toLowerCase()).join(", ")}. ${Object.entries(a.stats ?? {}).map(([k, v]) => `${STATUS[k] ?? k}: ${v}`).join(" · ") || "Sin ejecuciones aún"}`}
          actions={<div className="flex gap-2"><Button variant="outline" size="sm" onClick={() => setOpen(open === a.id ? "" : a.id!)}>{open === a.id ? "Ocultar historial" : "Historial"}</Button>{canEdit && <Button variant="outline" size="sm" onClick={() => start(a)}>Editar</Button>}{canEdit && <Button variant="ghost" size="icon" aria-label={`Borrar ${a.name}`} onClick={() => void remove(a)}><Trash2 className="size-4" /></Button>}</div>}>
          {open === a.id && (
            <div className="grid gap-2 text-sm" data-auto-runs>
              {(runs.data ?? []).length === 0 && <p className="text-muted-foreground">Sin ejecuciones.</p>}
              {(runs.data ?? []).map((r) => (
                <div key={r.id} className="flex flex-wrap items-start justify-between gap-2 rounded-md border p-2">
                  <div className="min-w-0"><strong>{STATUS[r.status] ?? r.status}</strong> · pedido <a className="text-primary hover:underline" href={`#pedidos/${r.orderId}`}>{r.orderId.slice(-6)}</a> · {new Date(r.createdAt).toLocaleString("es-MX", { dateStyle: "short", timeStyle: "short" })}{r.attempts > 1 ? ` · ${r.attempts} intentos` : ""}
                    <ul className="text-muted-foreground">{r.log.map((l, i) => <li key={i}>{l.result}</li>)}</ul>{r.lastError && r.status !== "done" && <p className="text-bad">{r.lastError}</p>}</div>
                  {canEdit && r.status === "failed" && <Button size="sm" variant="outline" onClick={() => void retry(r)}><RotateCcw className="size-4" /> Reintentar</Button>}
                </div>
              ))}
            </div>
          )}
        </Section>
      ))}
    </SubPage>
  );
}
