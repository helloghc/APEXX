import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { Loading, Section, SubPage } from "./kit";

interface Script { id?: string; name: string; category: "necessary" | "analytics" | "marketing"; src: string; active: boolean }
const CAT: Record<Script["category"], string> = { necessary: "Necesario (siempre)", analytics: "Analítica (con permiso)", marketing: "Publicidad (con permiso)" };

/** Third-party scripts that load only with the shopper's consent, never on checkout. */
export function Scripts({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "builder", "admin");
  const q = useQuery({ queryKey: ["store-scripts"], queryFn: () => request<{ items: Script[]; bannerText: string }>("GET", "/v1/settings/scripts") });
  const [items, setItems] = useState<Script[]>([]);
  const [banner, setBanner] = useState("");
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (q.data) { setItems(q.data.items); setBanner(q.data.bannerText); } }, [q.data]);
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const set = (i: number, p: Partial<Script>) => setItems((x) => x.map((s, j) => (j === i ? { ...s, ...p } : s)));
  async function save() {
    setBusy(true); setErrs({});
    try {
      await request("PUT", "/v1/settings/scripts", { items, bannerText: banner });
      toast.success("Scripts guardados. La tienda los carga según el permiso de cada visitante.");
      void qc.invalidateQueries({ queryKey: ["store-scripts"] });
    } catch (e) {
      setErrs(fieldErrors(e)); toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <SubPage title="Scripts y cookies" sub="Agrega herramientas de terceros (analítica, píxeles de anuncios, chat). Las de analítica y publicidad cargan solo si el visitante acepta, y ninguna carga en el pago ni en la cuenta del cliente. Con Global Privacy Control la publicidad queda apagada.">
      <Section title="Aviso de cookies" description="Se muestra cuando hay scripts de analítica o publicidad.">
        <Textarea aria-label="Texto del aviso" rows={2} value={banner} disabled={!canEdit} onChange={(e) => setBanner(e.target.value)} placeholder="Usamos cookies y herramientas de terceros para medir visitas y mostrarte anuncios. Tú decides." />
      </Section>
      {items.map((s, i) => (
        <Section key={i} title={s.name || `Script ${i + 1}`} actions={canEdit && <Button variant="ghost" size="icon" aria-label={`Quitar ${s.name || "script"}`} onClick={() => setItems((x) => x.filter((_, j) => j !== i))}><Trash2 className="size-4" /></Button>}>
          <fieldset disabled={!canEdit} className="grid gap-3 sm:grid-cols-[1fr_14rem]" data-script>
            <div className="grid gap-1.5"><Label htmlFor={`sc-n-${i}`}>Nombre</Label><Input id={`sc-n-${i}`} value={s.name} onChange={(e) => set(i, { name: e.target.value })} placeholder="Píxel de anuncios" />{errs[`items[${i}].name`] && <p className="text-xs text-bad">{errs[`items[${i}].name`]}</p>}</div>
            <div className="grid gap-1.5"><Label htmlFor={`sc-c-${i}`}>Tipo</Label><select id={`sc-c-${i}`} className="h-9 rounded-md border bg-background px-2 text-sm" value={s.category} onChange={(e) => set(i, { category: e.target.value as Script["category"] })}>{Object.entries(CAT).map(([k, v]) => <option key={k} value={k}>{v}</option>)}</select></div>
            <div className="grid gap-1.5"><Label htmlFor={`sc-s-${i}`}>Dirección del script</Label><Input id={`sc-s-${i}`} className="font-mono text-xs" value={s.src} onChange={(e) => set(i, { src: e.target.value })} placeholder="https://" />{errs[`items[${i}].src`] && <p className="text-xs text-bad">{errs[`items[${i}].src`]}</p>}</div>
            <label className="flex items-center gap-2 self-end text-sm"><input type="checkbox" checked={s.active} onChange={(e) => set(i, { active: e.target.checked })} /> Activo</label>
          </fieldset>
        </Section>
      ))}
      {canEdit && <div className="flex flex-wrap gap-2"><Button variant="outline" onClick={() => setItems((x) => [...x, { name: "", category: "analytics", src: "", active: true }])}><Plus className="size-4" /> Agregar script</Button><Button disabled={busy} onClick={() => void save()}>{busy ? "Guardando…" : "Guardar"}</Button></div>}
    </SubPage>
  );
}
