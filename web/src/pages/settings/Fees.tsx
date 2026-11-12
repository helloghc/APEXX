import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { Plus, Trash2 } from "lucide-react";
import { errorMessage, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { centsToPesos, parsePesos, readNumber, readPesos } from "@/money";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { Loading, ReadOnlyNote, Section, SubPage } from "./kit";

interface Rule { id: string; name: string; active: boolean; kind: "fixed" | "percent"; amount?: number; bps?: number; channels?: string[]; minItems?: number; maxItems?: number; delivery?: string; groups?: string[] }
interface Row extends Rule { amountText: string; pctText: string; minText: string; maxText: string; channelsText: string; groupsText: string }
const api = {
  get: () => request<{ items: Rule[] }>("GET", "/v1/settings/fees").then((r) => r.items),
  save: (items: Rule[]) => request<{ items: Rule[] }>("PUT", "/v1/settings/fees", { items }).then((r) => r.items),
};
const toRow = (r: Rule): Row => ({ ...r, amountText: r.amount ? centsToPesos(r.amount) : "", pctText: r.bps ? String(r.bps / 100) : "", minText: r.minItems ? centsToPesos(r.minItems) : "", maxText: r.maxItems ? centsToPesos(r.maxItems) : "", channelsText: (r.channels ?? []).join(", "), groupsText: (r.groups ?? []).join(", ") });
const list = (t: string) => t.split(/[,\s]+/).map((x) => x.trim()).filter(Boolean);

/** Extra charges per order: handling, packaging, small orders. Import duties are set per market in Canales. */
export function Fees({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "settings", "admin");
  const q = useQuery({ queryKey: ["settings", "fees"], queryFn: api.get });
  const [rows, setRows] = useState<Row[]>([]);
  const [error, setError] = useState("");
  useEffect(() => { if (q.data) setRows(q.data.map(toRow)); }, [q.data]);
  const set = (i: number, p: Partial<Row>) => setRows(rows.map((r, j) => (j === i ? { ...r, ...p } : r)));
  const save = useMutation({
    mutationFn: () => api.save(rows.map((r) => ({ id: r.id, name: r.name, active: r.active, kind: r.kind, amount: r.kind === "fixed" ? parsePesos(r.amountText) ?? 0 : 0,
      bps: r.kind === "percent" ? Math.round(readNumber(r.pctText, "Porcentaje") * 100) : 0, minItems: readPesos(r.minText, "Desde (monto)"), maxItems: readPesos(r.maxText, "Monto máximo"),
      delivery: r.delivery ?? "", channels: list(r.channelsText), groups: list(r.groupsText) }))),
    onSuccess: (items) => { setError(""); qc.setQueryData(["settings", "fees"], items); toast.success("Cargos guardados. Ya se aplican en el carrito."); },
    onError: (e) => { setError(errorMessage(e)); toast.error(errorMessage(e)); },
  });
  if (q.error) return <ErrorBox error={q.error} onRetry={() => void q.refetch()} />;
  if (!q.data) return <Loading />;
  return (
    <SubPage title="Cargos extra" sub="Cargos que se suman al total del pedido, como manejo, empaque o pedido mínimo. El comprador ve cada uno con su motivo antes de pagar. Los aranceles de importación se configuran por mercado en Canales.">
      <ReadOnlyNote show={!canEdit} need="Administrador" />
      <form className="grid gap-4" onSubmit={(e: FormEvent) => { e.preventDefault(); save.mutate(); }}>
        {rows.length === 0 && <p className="text-sm text-muted-foreground">Sin cargos extra. Los pedidos cobran productos, envío e impuestos.</p>}
        {rows.map((r, i) => (
          <Section key={i} title={r.name || `Cargo ${i + 1}`} actions={canEdit && <Button type="button" variant="ghost" size="icon" aria-label={`Quitar ${r.name || "cargo"}`} onClick={() => setRows(rows.filter((_, j) => j !== i))}><Trash2 className="size-4" /></Button>}>
            <fieldset className="grid gap-3 sm:grid-cols-3" disabled={!canEdit}>
              <legend className="sr-only">{r.name || `Cargo ${i + 1}`}</legend>
              <div className="grid gap-1.5"><Label htmlFor={`fee-name-${i}`}>Nombre que ve el comprador</Label><Input id={`fee-name-${i}`} value={r.name} onChange={(e) => set(i, { name: e.target.value })} placeholder="Manejo" /></div>
              <div className="grid gap-1.5"><Label htmlFor={`fee-kind-${i}`}>Tipo</Label>
                <select id={`fee-kind-${i}`} className="h-9 rounded-md border bg-background px-2 text-sm" value={r.kind} onChange={(e) => set(i, { kind: e.target.value as Row["kind"] })}><option value="fixed">Monto fijo</option><option value="percent">Porcentaje del pedido</option></select></div>
              {r.kind === "fixed"
                ? <div className="grid gap-1.5"><Label htmlFor={`fee-amount-${i}`}>Monto (MXN)</Label><Input id={`fee-amount-${i}`} inputMode="decimal" value={r.amountText} onChange={(e) => set(i, { amountText: e.target.value })} /></div>
                : <div className="grid gap-1.5"><Label htmlFor={`fee-pct-${i}`}>Porcentaje</Label><Input id={`fee-pct-${i}`} inputMode="decimal" value={r.pctText} onChange={(e) => set(i, { pctText: e.target.value })} placeholder="1.5" /></div>}
              <div className="grid gap-1.5"><Label htmlFor={`fee-min-${i}`}>Desde un pedido de (MXN)</Label><Input id={`fee-min-${i}`} inputMode="decimal" value={r.minText} onChange={(e) => set(i, { minText: e.target.value })} placeholder="Cualquier monto" /></div>
              <div className="grid gap-1.5"><Label htmlFor={`fee-max-${i}`}>Solo si el pedido es menor a (MXN)</Label><Input id={`fee-max-${i}`} inputMode="decimal" value={r.maxText} onChange={(e) => set(i, { maxText: e.target.value })} placeholder="Sin tope" /></div>
              <div className="grid gap-1.5"><Label htmlFor={`fee-del-${i}`}>Entrega</Label>
                <select id={`fee-del-${i}`} className="h-9 rounded-md border bg-background px-2 text-sm" value={r.delivery ?? ""} onChange={(e) => set(i, { delivery: e.target.value })}><option value="">Cualquiera</option><option value="delivery">Solo a domicilio</option><option value="pickup">Solo retiro en tienda</option></select></div>
              <div className="grid gap-1.5"><Label htmlFor={`fee-ch-${i}`}>Canales (vacío = todos)</Label><Input id={`fee-ch-${i}`} className="font-mono" value={r.channelsText} onChange={(e) => set(i, { channelsText: e.target.value })} placeholder="mx, us" /></div>
              <div className="grid gap-1.5"><Label htmlFor={`fee-gr-${i}`}>Grupos de precio (vacío = todos)</Label><Input id={`fee-gr-${i}`} className="font-mono" value={r.groupsText} onChange={(e) => set(i, { groupsText: e.target.value })} placeholder="mayoreo" /></div>
              <label className="flex items-center gap-2 self-end text-sm"><input type="checkbox" checked={r.active} onChange={(e) => set(i, { active: e.target.checked })} />Activo</label>
            </fieldset>
          </Section>
        ))}
        {error && <p className="text-sm text-bad" role="alert">{error}</p>}
        {canEdit && <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" onClick={() => setRows([...rows, toRow({ id: "", name: "", active: true, kind: "fixed" })])}><Plus className="size-4" /> Agregar cargo</Button>
          <Button type="submit" disabled={save.isPending}>{save.isPending ? "Guardando…" : "Guardar cargos"}</Button>
        </div>}
      </form>
    </SubPage>
  );
}
