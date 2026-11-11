import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { ArrowDown, ArrowUp, Plus, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/sonner";
import type { FieldDef } from "@/components/CustomFields";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { Loading, ReadOnlyNote, Section, SubPage } from "./kit";

const RESOURCES: [string, string, string][] = [["customer", "Clientes", "Por ejemplo talla preferida, fecha de cumpleaños o número de socio."], ["order", "Pedidos", "Por ejemplo número de orden de compra, mensaje de regalo o vendedor que atendió."], ["company", "Empresas", "Por ejemplo giro, número de proveedor o contacto de cuentas por pagar."]];
const TYPES: [string, string][] = [["text", "Texto corto"], ["textarea", "Texto largo"], ["number", "Número"], ["boolean", "Sí o no"], ["date", "Fecha"], ["select", "Lista de opciones"], ["url", "Dirección web"], ["email", "Correo"]];
const slug = (s: string) => s.toLowerCase().normalize("NFD").replace(/[̀-ͯ]/g, "").replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "").replace(/^([0-9])/, "c_$1").slice(0, 40);

/** Custom fields: the store adds its own fields to customers, orders and companies. */
export function Fields({ me }: { me: Me }) {
  const canEdit = can(me, "settings", "admin");
  return (
    <SubPage title="Campos propios" sub="Agrega los datos que tu negocio necesita a clientes, pedidos y empresas. Aparecen en la ficha de cada uno; los de clientes que no sean personales sirven para armar segmentos.">
      <ReadOnlyNote show={!canEdit} need="Administrador" />
      {RESOURCES.map(([r, title, hint]) => <ResourceFields key={r} resource={r} title={title} hint={hint} canEdit={canEdit} />)}
    </SubPage>
  );
}

function ResourceFields({ resource, title, hint, canEdit }: { resource: string; title: string; hint: string; canEdit: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["fields", resource], queryFn: () => request<{ fields: FieldDef[] }>("GET", `/v1/fields/${resource}`) });
  const [list, setList] = useState<FieldDef[]>([]);
  const [opts, setOpts] = useState<Record<number, string>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  useEffect(() => { if (q.data) { setList(q.data.fields); setOpts(Object.fromEntries(q.data.fields.map((f, i) => [i, (f.options ?? []).join(", ")]))); } }, [q.data]);
  const save = useMutation({
    mutationFn: () => request<{ fields: FieldDef[] }>("PUT", `/v1/fields/${resource}`, { fields: list.map((f, i) => ({ ...f, key: f.key || slug(f.label), options: f.type === "select" ? (opts[i] ?? "").split(",").map((x) => x.trim()).filter(Boolean) : undefined })) }),
    onSuccess: () => { setErrors({}); toast.success(`Campos de ${title.toLowerCase()} guardados.`); void qc.invalidateQueries({ queryKey: ["fields", resource] }); },
    onError: (e) => { setErrors(fieldErrors(e)); toast.error(errorMessage(e)); },
  });
  if (q.error) return <ErrorBox error={q.error} onRetry={() => void q.refetch()} />;
  if (!q.data) return <Loading />;
  const upd = (i: number, p: Partial<FieldDef>) => setList((l) => l.map((f, j) => (j === i ? { ...f, ...p } : f)));
  const move = (i: number, d: number) => setList((l) => { const n = [...l]; const [x] = n.splice(i, 1); n.splice(i + d, 0, x!); return n; });
  const dirty = JSON.stringify(list) !== JSON.stringify(q.data.fields) || list.some((f, i) => f.type === "select" && (opts[i] ?? "") !== (q.data.fields[i]?.options ?? []).join(", "));
  return (
    <Section title={title} actions={canEdit && <Button size="sm" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>{save.isPending ? "Guardando…" : "Guardar"}</Button>}>
      <p className="text-sm text-muted-foreground">{hint}</p>
      {list.length === 0 && <p className="text-sm text-muted-foreground">Sin campos todavía.</p>}
      <ul className="grid gap-3">
        {list.map((f, i) => (
          <li key={i} className="grid gap-2 rounded-md border p-3 sm:grid-cols-[1fr_1fr_auto]">
            <div className="grid gap-1"><label className="text-xs font-medium" htmlFor={`fd-${resource}-${i}-l`}>Nombre</label><Input id={`fd-${resource}-${i}-l`} value={f.label} disabled={!canEdit} onChange={(e) => upd(i, { label: e.target.value })} placeholder="Talla preferida" />
              <span className="font-mono text-xs text-muted-foreground">clave: {f.key || slug(f.label) || "…"}</span>{errors[`fields.${i}.label`] && <span className="text-sm text-bad">{errors[`fields.${i}.label`]}</span>}{errors[`fields.${i}.key`] && <span className="text-sm text-bad">{errors[`fields.${i}.key`]}</span>}</div>
            <div className="grid gap-1"><label className="text-xs font-medium" htmlFor={`fd-${resource}-${i}-t`}>Tipo</label><select id={`fd-${resource}-${i}-t`} className="h-9 rounded-md border bg-card px-2 text-sm" value={f.type} disabled={!canEdit} onChange={(e) => upd(i, { type: e.target.value })}>{TYPES.map((t) => <option key={t[0]} value={t[0]}>{t[1]}</option>)}</select>
              {f.type === "select" && <><Input aria-label="Opciones separadas por coma" value={opts[i] ?? ""} disabled={!canEdit} onChange={(e) => setOpts((o) => ({ ...o, [i]: e.target.value }))} placeholder="CH, M, G" />{errors[`fields.${i}.options`] && <span className="text-sm text-bad">{errors[`fields.${i}.options`]}</span>}</>}</div>
            <div className="flex items-start gap-1">{canEdit && <><Button type="button" size="icon" variant="ghost" aria-label={`Subir ${f.label}`} disabled={i === 0} onClick={() => move(i, -1)}><ArrowUp aria-hidden="true" /></Button><Button type="button" size="icon" variant="ghost" aria-label={`Bajar ${f.label}`} disabled={i === list.length - 1} onClick={() => move(i, 1)}><ArrowDown aria-hidden="true" /></Button><Button type="button" size="icon" variant="ghost" aria-label={`Quitar ${f.label}`} onClick={() => setList((l) => l.filter((_, j) => j !== i))}><Trash2 aria-hidden="true" /></Button></>}</div>
            <div className="flex flex-wrap gap-4 text-sm sm:col-span-3">
              <label className="flex items-center gap-2"><input type="checkbox" checked={f.required} disabled={!canEdit} onChange={(e) => upd(i, { required: e.target.checked })} /> Obligatorio</label>
              <label className="flex items-center gap-2"><input type="checkbox" checked={f.personal} disabled={!canEdit} onChange={(e) => upd(i, { personal: e.target.checked })} /> Dato personal (se guarda cifrado; no sirve para segmentos)</label>
            </div>
          </li>
        ))}
      </ul>
      {canEdit && <div><Button type="button" variant="outline" size="sm" onClick={() => setList((l) => [...l, { key: "", label: "", type: "text", required: false, personal: false }])}><Plus aria-hidden="true" /> Agregar campo</Button></div>}
    </Section>
  );
}
