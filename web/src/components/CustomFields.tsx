import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { errorMessage, fieldErrors, request } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";

export interface FieldDef { key: string; label: string; type: string; options?: string[]; required: boolean; personal: boolean; help?: string }

/** Custom fields of one record (customer, order or company), shown where the
 *  record is. Nothing is rendered while the store has no fields. */
export function CustomFields({ resource, id, canEdit }: { resource: "customer" | "order" | "company"; id: string; canEdit: boolean }) {
  const qc = useQueryClient();
  const key = ["fields", resource, id];
  const q = useQuery({ queryKey: key, queryFn: () => request<{ fields: FieldDef[]; values: Record<string, string> }>("GET", `/v1/fields/${resource}/${encodeURIComponent(id)}`) });
  const [vals, setVals] = useState<Record<string, string>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  useEffect(() => { if (q.data) setVals(q.data.values); }, [q.data]);
  const save = useMutation({
    mutationFn: () => request<void>("PUT", `/v1/fields/${resource}/${encodeURIComponent(id)}`, vals, { as: "none" }),
    onSuccess: () => { setErrors({}); toast.success("Datos guardados."); void qc.invalidateQueries({ queryKey: key }); },
    onError: (e) => { setErrors(fieldErrors(e)); toast.error(errorMessage(e)); },
  });
  if (!q.data || q.data.fields.length === 0) return null;
  const dirty = JSON.stringify(vals) !== JSON.stringify(q.data.values);
  return (
    <section className="grid gap-3 rounded-lg border bg-card p-4" aria-labelledby={`cf-${resource}`} data-testid="custom-fields">
      <div className="flex items-center justify-between gap-2"><h2 id={`cf-${resource}`} className="text-base font-semibold">Campos propios</h2>{canEdit && <Button size="sm" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>{save.isPending ? "Guardando…" : "Guardar"}</Button>}</div>
      <div className="grid gap-3 sm:grid-cols-2">
        {q.data.fields.map((f) => {
          const v = vals[f.key] ?? "";
          const set = (x: string) => setVals((p) => ({ ...p, [f.key]: x }));
          const id2 = `cf-${resource}-${f.key}`;
          const input = f.type === "select"
            ? <select id={id2} className="h-9 rounded-md border bg-card px-2 text-sm" value={v} disabled={!canEdit} onChange={(e) => set(e.target.value)}><option value="">—</option>{(f.options ?? []).map((o) => <option key={o} value={o}>{o}</option>)}</select>
            : f.type === "boolean"
              ? <select id={id2} className="h-9 rounded-md border bg-card px-2 text-sm" value={v} disabled={!canEdit} onChange={(e) => set(e.target.value)}><option value="">—</option><option value="true">Sí</option><option value="false">No</option></select>
              : f.type === "textarea"
                ? <Textarea id={id2} rows={3} value={v} disabled={!canEdit} onChange={(e) => set(e.target.value)} />
                : <Input id={id2} type={f.type === "date" ? "date" : f.type === "number" ? "text" : f.type === "email" ? "email" : f.type === "url" ? "url" : "text"} inputMode={f.type === "number" ? "decimal" : undefined} value={v} disabled={!canEdit} onChange={(e) => set(e.target.value)} />;
          return (
            <div key={f.key} className={f.type === "textarea" ? "grid gap-1.5 sm:col-span-2" : "grid gap-1.5"}>
              <label htmlFor={id2} className="text-sm font-medium">{f.label}{f.required ? " *" : ""}{f.personal ? <span className="ml-1 text-xs font-normal text-muted-foreground">· cifrado</span> : null}</label>
              {input}
              {f.help && <p className="text-xs text-muted-foreground">{f.help}</p>}
              {errors[f.key] && <p className="text-sm text-bad">{errors[f.key]}</p>}
            </div>
          );
        })}
      </div>
    </section>
  );
}
