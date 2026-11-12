import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { errorMessage, request } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { toast } from "@/components/ui/sonner";
import { href } from "@/router";

interface Edge { id: string; rel: string; srcId: string; dstId: string; srcTitle: string; dstTitle: string; confidence: number; source: string; status: string }
interface Graph { items: Edge[]; counts: Record<string, number>; approvalRate: number }

const REL: Record<string, string> = { compatible_with: "es compatible con", accessory_of: "es accesorio de", substitute_of: "es alternativa de" };
const TABS: [string, string][] = [["proposed", "Por revisar"], ["approved", "Aprobadas"], ["rejected", "Rechazadas"]];

/** Knowledge graph: relations the store proposes, the merchant decides. */
export function GraphCard({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const [status, setStatus] = useState("proposed");
  const [sel, setSel] = useState<Set<string>>(new Set());
  const q = useQuery({ queryKey: ["ai-graph", status], queryFn: () => request<Graph>("GET", `/v1/ai/graph?status=${status}`) });
  const refresh = () => { setSel(new Set()); void qc.invalidateQueries({ queryKey: ["ai-graph"] }); };
  const decide = useMutation({
    mutationFn: (v: { ids: string[]; status: string }) => request<{ changed: number }>("POST", "/v1/ai/graph/decide", v),
    onSuccess: (r, v) => { toast.success(v.status === "approved" ? `${r.changed} aprobadas. Ya se ven en tu tienda.` : v.status === "rejected" ? `${r.changed} rechazadas. No se volverán a proponer.` : `${r.changed} regresaron a revisión.`); refresh(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const run = useMutation({
    mutationFn: () => request<{ proposed: number }>("POST", "/v1/ai/graph/run"),
    onSuccess: (r) => { toast.success(r.proposed ? `${r.proposed} relaciones nuevas por revisar.` : "No hay relaciones nuevas por ahora."); refresh(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const items = q.data?.items ?? [];
  const counts = q.data?.counts ?? {};
  const toggle = (id: string) => setSel((s) => { const n = new Set(s); if (n.has(id)) n.delete(id); else n.add(id); return n; });
  const ids = [...sel];
  return (
    <Card data-ai-graph className="xl:col-span-2">
      <CardHeader>
        <CardTitle className="text-base">Relaciones entre productos</CardTitle>
        <CardDescription>Compatibles, accesorios y alternativas que Apexx encuentra en tus textos, tus pedidos y tu catálogo. Cada una dice por qué se propone. Solo las que apruebes aparecen en la ficha, en el asistente y en los asistentes de IA externos; las rechazadas no se vuelven a proponer.</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3 text-sm">
        <div className="flex flex-wrap items-center gap-2">
          <div className="flex flex-wrap gap-2" role="tablist" aria-label="Estado">
            {TABS.map(([k, l]) => <Button key={k} type="button" role="tab" aria-selected={status === k} size="sm" variant={status === k ? "default" : "outline"} onClick={() => { setStatus(k); setSel(new Set()); }}>{l} ({counts[k] ?? 0})</Button>)}
          </div>
          {canEdit && <Button type="button" size="sm" variant="outline" disabled={run.isPending} onClick={() => run.mutate()}>{run.isPending ? "Buscando…" : "Buscar relaciones ahora"}</Button>}
          {(counts.approved ?? 0) + (counts.rejected ?? 0) > 0 && <span className="text-muted-foreground">Aprobadas de las revisadas: <span className="tabular-nums">{Math.round((q.data?.approvalRate ?? 0) * 100)}</span> %</span>}
        </div>
        {canEdit && ids.length > 0 && <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/40 p-2">
          <span><span className="tabular-nums">{ids.length}</span> elegidas</span>
          {status !== "approved" && <Button type="button" size="sm" disabled={decide.isPending} onClick={() => decide.mutate({ ids, status: "approved" })}>Aprobar</Button>}
          {status !== "rejected" && <Button type="button" size="sm" variant="outline" disabled={decide.isPending} onClick={() => decide.mutate({ ids, status: "rejected" })}>Rechazar</Button>}
          {status !== "proposed" && <Button type="button" size="sm" variant="outline" disabled={decide.isPending} onClick={() => decide.mutate({ ids, status: "proposed" })}>Regresar a revisión</Button>}
        </div>}
        {q.isLoading ? <p className="text-muted-foreground">Cargando…</p> : items.length === 0 ? <p className="text-muted-foreground">{status === "proposed" ? "No hay relaciones por revisar." : "Nada por aquí todavía."}</p> : (
          <ul className="grid gap-2">
            {items.map((e) => (
              <li key={e.id} className="flex items-start gap-3 rounded-md border p-3">
                {canEdit && <input type="checkbox" className="mt-1" aria-label={`Elegir: ${e.srcTitle} ${REL[e.rel] ?? e.rel} ${e.dstTitle}`} checked={sel.has(e.id)} onChange={() => toggle(e.id)} />}
                <div className="grid min-w-0 flex-1 gap-1">
                  <p className="break-words"><a className="font-medium text-primary hover:underline" href={href("catalogo", "productos", e.srcId)}>{e.srcTitle}</a> <span className="text-muted-foreground">{REL[e.rel] ?? e.rel}</span> <a className="font-medium text-primary hover:underline" href={href("catalogo", "productos", e.dstId)}>{e.dstTitle}</a></p>
                  <p className="text-muted-foreground">{e.source}</p>
                </div>
                <Badge variant="outline"><span className="tabular-nums">{Math.round(e.confidence * 100)}</span> %</Badge>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
