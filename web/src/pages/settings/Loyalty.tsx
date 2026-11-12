import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { centsToPesos, readNumber, readPesos } from "@/money";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { Loading, Section, SubPage } from "./kit";

interface Tier { name: string; minSpend: number; multiplierPct: number }
interface Program { active: boolean; earnPer: number; pointValue: number; minRedeem: number; tiers: Tier[] }

/** Loyalty program: points on what customers pay, tiers that multiply them, redemption into store credit. */
export function Loyalty({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "settings", "admin");
  const q = useQuery({ queryKey: ["loyalty-program"], queryFn: () => request<Program>("GET", "/v1/settings/loyalty") });
  const [v, setV] = useState({ active: false, earn: "10", value: "0.10", min: "100", tiers: [] as { name: string; spend: string; mult: string }[] });
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    const p = q.data;
    if (p) setV({ active: p.active, earn: p.earnPer ? centsToPesos(p.earnPer) : "10", value: p.pointValue ? centsToPesos(p.pointValue) : "0.10", min: String(p.minRedeem || 100), tiers: p.tiers.map((t) => ({ name: t.name, spend: centsToPesos(t.minSpend), mult: String(t.multiplierPct / 100) })) });
  }, [q.data]);
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  async function save() {
    setBusy(true); setErrs({});
    try {
      await request("PUT", "/v1/settings/loyalty", { active: v.active, earnPer: readPesos(v.earn, "1 punto por cada ($)"), pointValue: readPesos(v.value, "Cada punto vale ($)"), minRedeem: readNumber(v.min, "Mínimo para canjear (puntos)"),
        tiers: v.tiers.map((t) => ({ name: t.name, minSpend: readPesos(t.spend, "Desde compras de ($)"), multiplierPct: Math.round(readNumber(t.mult, "Multiplica por", 1) * 100) })) });
      toast.success(v.active ? "Programa de puntos activo: los siguientes pedidos pagados ya suman." : "Programa guardado en pausa.");
      void qc.invalidateQueries({ queryKey: ["loyalty-program"] });
    } catch (e) {
      setErrs(fieldErrors(e)); toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  const setT = (i: number, p: Partial<(typeof v.tiers)[number]>) => setV({ ...v, tiers: v.tiers.map((t, j) => (j === i ? { ...t, ...p } : t)) });
  return (
    <SubPage title="Puntos de lealtad" sub="Tus clientes ganan puntos por lo que pagan y los canjean por saldo a favor. Un reembolso quita exactamente los puntos de lo devuelto y una cancelación todos los del pedido. Cada movimiento queda en el libro contable.">
      <Section title="Programa">
        <fieldset disabled={!canEdit} className="grid gap-3 sm:grid-cols-3">
          <div className="grid gap-1.5"><Label htmlFor="ly-earn">1 punto por cada ($)</Label><Input id="ly-earn" inputMode="decimal" value={v.earn} onChange={(e) => setV({ ...v, earn: e.target.value })} />{errs.earnPer && <p className="text-xs text-bad">{errs.earnPer}</p>}</div>
          <div className="grid gap-1.5"><Label htmlFor="ly-value">Cada punto vale ($)</Label><Input id="ly-value" inputMode="decimal" value={v.value} onChange={(e) => setV({ ...v, value: e.target.value })} /></div>
          <div className="grid gap-1.5"><Label htmlFor="ly-min">Mínimo para canjear (puntos)</Label><Input id="ly-min" inputMode="numeric" value={v.min} onChange={(e) => setV({ ...v, min: e.target.value.replace(/\\D/g, "") })} /></div>
          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={v.active} onChange={(e) => setV({ ...v, active: e.target.checked })} /> Activo</label>
        </fieldset>
      </Section>
      <Section title="Niveles" description="Multiplican los puntos de quien ya compró cierta cantidad en total.">
        {v.tiers.map((t, i) => (
          <div key={i} className="grid gap-2 sm:grid-cols-[1fr_10rem_8rem_auto] sm:items-end" data-tier>
            <div className="grid gap-1"><Label htmlFor={`ly-tn-${i}`}>Nombre</Label><Input id={`ly-tn-${i}`} disabled={!canEdit} value={t.name} onChange={(e) => setT(i, { name: e.target.value })} placeholder="Oro" /></div>
            <div className="grid gap-1"><Label htmlFor={`ly-ts-${i}`}>Desde compras de ($)</Label><Input id={`ly-ts-${i}`} disabled={!canEdit} inputMode="decimal" value={t.spend} onChange={(e) => setT(i, { spend: e.target.value })} /></div>
            <div className="grid gap-1"><Label htmlFor={`ly-tm-${i}`}>Multiplica por</Label><Input id={`ly-tm-${i}`} disabled={!canEdit} inputMode="decimal" value={t.mult} onChange={(e) => setT(i, { mult: e.target.value })} /></div>
            {canEdit && <Button variant="ghost" size="icon" aria-label={`Quitar nivel ${t.name}`} onClick={() => setV({ ...v, tiers: v.tiers.filter((_, j) => j !== i) })}><Trash2 className="size-4" /></Button>}
          </div>
        ))}
        {errs.tiers && <p className="text-sm text-bad">{errs.tiers}</p>}
        {canEdit && <div><Button variant="outline" size="sm" onClick={() => setV({ ...v, tiers: [...v.tiers, { name: "", spend: "", mult: "2" }] })}><Plus className="size-4" /> Agregar nivel</Button></div>}
      </Section>
      {canEdit && <div><Button disabled={busy} onClick={() => void save()}>{busy ? "Guardando…" : "Guardar programa"}</Button></div>}
    </SubPage>
  );
}
