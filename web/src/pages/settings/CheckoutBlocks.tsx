import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { Loading, Section, SubPage } from "./kit";

interface Block { id?: string; kind: "notice" | "checkbox" | "text"; label: string; text?: string; required?: boolean; maxLength?: number; source?: string }
const KIND: Record<Block["kind"], string> = { notice: "Aviso", checkbox: "Casilla para aceptar", text: "Campo de texto" };

/** Checkout blocks: notices, required checkboxes and extra fields on the payment page, drawn by Apexx. */
export function CheckoutBlocks({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "settings", "admin");
  const q = useQuery({ queryKey: ["checkout-blocks"], queryFn: () => request<{ blocks: Block[] }>("GET", "/v1/settings/checkout-blocks") });
  const [blocks, setBlocks] = useState<Block[]>([]);
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (q.data) setBlocks(q.data.blocks); }, [q.data]);
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const set = (i: number, p: Partial<Block>) => setBlocks((x) => x.map((b, j) => (j === i ? { ...b, ...p } : b)));
  async function save() {
    setBusy(true); setErrs({});
    try {
      await request("PUT", "/v1/settings/checkout-blocks", { blocks });
      toast.success("Bloques guardados. Ya aparecen en el pago.");
      void qc.invalidateQueries({ queryKey: ["checkout-blocks"] });
    } catch (e) {
      setErrs(fieldErrors(e)); toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <SubPage title="Bloques del pago" sub="Agrega al pago un aviso, una casilla que el comprador debe aceptar o un dato extra (por ejemplo, una referencia para la factura). Apexx los dibuja y los valida; ningún código de terceros corre en el pago. Las respuestas quedan en el pedido.">
      {blocks.map((b, i) => (
        <Section key={i} title={b.label || `Bloque ${i + 1}`} description={b.source ? `Agregado por la app ${b.source}` : undefined}
          actions={canEdit && <Button variant="ghost" size="icon" aria-label={`Quitar ${b.label || "bloque"}`} onClick={() => setBlocks((x) => x.filter((_, j) => j !== i))}><Trash2 className="size-4" /></Button>}>
          <fieldset disabled={!canEdit} className="grid gap-3 sm:grid-cols-[12rem_1fr]" data-checkout-block>
            <div className="grid gap-1.5"><Label htmlFor={`cb-k-${i}`}>Tipo</Label><select id={`cb-k-${i}`} className="h-9 rounded-md border bg-background px-2 text-sm" value={b.kind} onChange={(e) => set(i, { kind: e.target.value as Block["kind"] })}>{Object.entries(KIND).map(([k, v]) => <option key={k} value={k}>{v}</option>)}</select></div>
            <div className="grid gap-1.5"><Label htmlFor={`cb-l-${i}`}>Título</Label><Input id={`cb-l-${i}`} value={b.label} onChange={(e) => set(i, { label: e.target.value })} />{errs[`blocks[${i}].label`] && <p className="text-xs text-bad">{errs[`blocks[${i}].label`]}</p>}</div>
            <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor={`cb-t-${i}`}>Texto de ayuda (opcional)</Label><Input id={`cb-t-${i}`} value={b.text ?? ""} onChange={(e) => set(i, { text: e.target.value })} /></div>
            {b.kind !== "notice" && <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={!!b.required} onChange={(e) => set(i, { required: e.target.checked })} /> Obligatorio</label>}
          </fieldset>
        </Section>
      ))}
      {canEdit && <div className="flex flex-wrap gap-2"><Button variant="outline" onClick={() => setBlocks((x) => [...x, { kind: "checkbox", label: "" }])}><Plus className="size-4" /> Agregar bloque</Button><Button disabled={busy} onClick={() => void save()}>{busy ? "Guardando…" : "Guardar"}</Button></div>}
    </SubPage>
  );
}
