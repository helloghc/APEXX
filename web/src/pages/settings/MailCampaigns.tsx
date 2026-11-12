import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Plus, Send, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { formatMoney } from "@/money";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { Loading, Section, SubPage } from "./kit";

interface Camp { id?: string; name: string; groupId: string; subject: string; blocks: { heading: string; body: string; cta: string; footer: string }; link: string; status?: string; sendAt?: string; sentCount?: number; orders?: number; revenue?: number }
const STATUS: Record<string, string> = { draft: "Borrador", scheduled: "Programada", sending: "Enviando", sent: "Enviada" };
const blank: Camp = { name: "", groupId: "", subject: "", blocks: { heading: "", body: "", cta: "Ver ofertas", footer: "" }, link: "/c" };

/** Email campaigns to customers who agreed to receive news, with their attributed sales. */
export function MailCampaigns({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "customers", "edit"), canSend = can(me, "customers", "admin");
  const q = useQuery({ queryKey: ["mail-campaigns"], queryFn: () => request<{ items: Camp[] }>("GET", "/v1/email-campaigns").then((r) => r.items), refetchInterval: (x) => ((x.state.data ?? []).some((c) => c.status === "sending" || c.status === "scheduled") ? 5000 : false) });
  const groups = useQuery({ queryKey: ["customer-groups"], queryFn: () => request<{ items: { id: string; name: string; count: number }[] }>("GET", "/v1/customer-groups").then((r) => r.items) });
  const [edit, setEdit] = useState<Camp | null>(null);
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const refresh = () => void qc.invalidateQueries({ queryKey: ["mail-campaigns"] });
  async function save(andSend: boolean) {
    if (!edit) return;
    setBusy(true); setErrs({});
    try {
      const c = await request<Camp>(edit.id ? "PUT" : "POST", edit.id ? `/v1/email-campaigns/${edit.id}` : "/v1/email-campaigns", edit);
      if (andSend) {
        if (!window.confirm("¿Enviar ahora a los clientes del grupo que aceptaron recibir noticias?")) { setEdit(c); refresh(); return; }
        await request("POST", `/v1/email-campaigns/${c.id}/send`, {});
        toast.success("Campaña en camino: se envía en unos segundos."); setEdit(null);
      } else { toast.success("Borrador guardado."); setEdit(c); }
      refresh();
    } catch (e) { setErrs(fieldErrors(e)); toast.error(errorMessage(e)); } finally { setBusy(false); }
  }
  async function remove(c: Camp) {
    if (!window.confirm(`¿Borrar «${c.name}»?`)) return;
    try { await request("DELETE", `/v1/email-campaigns/${c.id}`, undefined, { as: "none" }); toast.success("Campaña borrada."); setEdit(null); refresh(); } catch (e) { toast.error(errorMessage(e)); }
  }
  const b = edit?.blocks;
  return (
    <SubPage title="Campañas de correo" sub="Escribe a un grupo de clientes. Solo reciben quienes aceptaron noticias, cada correo trae su enlace para darse de baja, y las compras que llegan desde el botón se cuentan como ventas de la campaña."
      actions={canEdit && !edit && <Button onClick={() => { setErrs({}); setEdit({ ...blank, blocks: { ...blank.blocks } }); }}><Plus className="size-4" /> Nueva campaña</Button>}>
      {edit && b && (
        <Section title={edit.id ? "Editar campaña" : "Nueva campaña"} actions={edit.id && canEdit && <Button variant="ghost" size="icon" aria-label="Borrar campaña" onClick={() => void remove(edit)}><Trash2 className="size-4" /></Button>}>
          <fieldset disabled={!canEdit} className="grid gap-3 sm:grid-cols-2" data-campaign-form>
            <div className="grid gap-1.5"><Label htmlFor="cm-name">Nombre interno</Label><Input id="cm-name" value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} />{errs.name && <p className="text-xs text-bad">{errs.name}</p>}</div>
            <div className="grid gap-1.5"><Label htmlFor="cm-group">Para</Label><select id="cm-group" className="h-9 rounded-md border bg-background px-2 text-sm" value={edit.groupId} onChange={(e) => setEdit({ ...edit, groupId: e.target.value })}><option value="">Todos los clientes con permiso</option>{(groups.data ?? []).map((g) => <option key={g.id} value={g.id}>{g.name} ({g.count})</option>)}</select></div>
            <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="cm-subject">Asunto</Label><Input id="cm-subject" value={edit.subject} onChange={(e) => setEdit({ ...edit, subject: e.target.value })} />{errs.subject && <p className="text-xs text-bad">{errs.subject}</p>}</div>
            <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="cm-heading">Título (puedes usar {"{{cliente.nombre}}"})</Label><Input id="cm-heading" value={b.heading} onChange={(e) => setEdit({ ...edit, blocks: { ...b, heading: e.target.value } })} /></div>
            <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="cm-body">Texto</Label><Textarea id="cm-body" rows={6} value={b.body} onChange={(e) => setEdit({ ...edit, blocks: { ...b, body: e.target.value } })} /></div>
            <div className="grid gap-1.5"><Label htmlFor="cm-cta">Botón</Label><Input id="cm-cta" value={b.cta} onChange={(e) => setEdit({ ...edit, blocks: { ...b, cta: e.target.value } })} /></div>
            <div className="grid gap-1.5"><Label htmlFor="cm-link">Lleva a (página de tu tienda)</Label><Input id="cm-link" className="font-mono text-xs" value={edit.link} onChange={(e) => setEdit({ ...edit, link: e.target.value })} placeholder="/c/ofertas" />{errs.link && <p className="text-xs text-bad">{errs.link}</p>}</div>
          </fieldset>
          <div className="flex flex-wrap gap-2">{canEdit && <Button variant="outline" disabled={busy} onClick={() => void save(false)}>Guardar borrador</Button>}{canSend && <Button disabled={busy} onClick={() => void save(true)}><Send className="size-4" /> Guardar y enviar</Button>}<Button variant="ghost" onClick={() => setEdit(null)}>Cerrar</Button></div>
        </Section>
      )}
      {!edit && (q.data.length === 0 ? <p className="text-sm text-muted-foreground">Aún no hay campañas.</p> : (
        <ul className="divide-y rounded-md border" data-campaigns>{q.data.map((c) => (
          <li key={c.id} className="flex flex-wrap items-center justify-between gap-2 p-3 text-sm">
            <span>{c.status === "draft" ? <button type="button" className="font-medium text-primary hover:underline" onClick={() => setEdit(c)}>{c.name}</button> : <strong>{c.name}</strong>} · {STATUS[c.status ?? "draft"]}</span>
            <span className="text-muted-foreground">{c.sentCount ?? 0} enviados · {c.orders ?? 0} pedidos · {formatMoney(c.revenue ?? 0, "MXN")} en ventas</span>
          </li>))}</ul>
      ))}
    </SubPage>
  );
}
