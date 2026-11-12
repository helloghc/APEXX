import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Plus } from "lucide-react";
import { errorMessage, request } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "@/components/ui/sonner";
import { ProductPicker } from "../catalog/ProductPicker";
import { dateTime, Notice } from "../common";

const enc = encodeURIComponent;
interface Ref { id: string; title: string; handle: string }
interface Settings { recsOff?: boolean; exclude?: string[]; mcp?: boolean }
interface State { settings: Settings; excluded: Ref[]; mcpUrl: string; index?: { builtAt: string; products: number; orders: number; terms: number } }
interface Rec extends Ref { buys: number; views: number; score: number }

const api = {
  get: () => request<State>("GET", "/v1/ai/settings"),
  save: (s: Settings) => request<State>("PUT", "/v1/ai/settings", s),
  recs: (id: string) => request<{ items: Rec[]; off: boolean; ready: boolean }>("GET", `/v1/products/${enc(id)}/recommendations`),
};

/** Recommendations learned from orders and visits, and AI assistants (MCP). */
export function AIPanel({ canEdit, canAdmin }: { canEdit: boolean; canAdmin: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["ai-settings"], queryFn: api.get });
  const [picker, setPicker] = useState<"exclude" | "explain" | null>(null);
  const [explain, setExplain] = useState<Ref | null>(null);
  const recs = useQuery({ queryKey: ["product-recs", explain?.id], queryFn: () => api.recs(explain!.id), enabled: !!explain });
  const save = useMutation({
    mutationFn: (s: Settings) => api.save(s),
    onSuccess: (r) => { toast.success("Guardado. Ya aplica en tu tienda."); qc.setQueryData(["ai-settings"], (old: State | undefined) => (old ? { ...old, ...r } : r)); void qc.invalidateQueries({ queryKey: ["product-recs"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const s = q.data;
  if (!s) return <p className="text-sm text-muted-foreground">Cargando…</p>;
  const cur: Settings = { recsOff: !!s.settings.recsOff, exclude: s.settings.exclude ?? [], mcp: !!s.settings.mcp };
  const put = (patch: Partial<Settings>) => save.mutate({ ...cur, ...patch });
  return (
    <div className="grid gap-5 xl:grid-cols-2">
      <Card data-ai-recs>
        <CardHeader><CardTitle className="text-base">Recomendaciones</CardTitle>
          <CardDescription>«Otros clientes también eligieron» en la ficha de producto y en el carrito. Se calculan con los pedidos pagados del último año y lo que se ve junto en una misma visita, y se actualizan solas en menos de un minuto. Un pedido cancelado o devuelto deja de contar. No se guarda nada que identifique a una persona.</CardDescription></CardHeader>
        <CardContent className="grid gap-3 text-sm">
          <label className="flex items-center gap-2"><input type="checkbox" checked={!cur.recsOff} disabled={!canAdmin || save.isPending} onChange={(e) => put({ recsOff: !e.target.checked })} /> Mostrar recomendaciones en la tienda</label>
          {s.index ? <p className="text-muted-foreground">Índice al {dateTime(s.index.builtAt)}: {s.index.orders} pedidos, {s.index.products} productos con recomendaciones, {s.index.terms} términos de búsqueda aprendidos.</p>
            : <p className="text-muted-foreground">El índice se arma en los próximos segundos.</p>}
          <div className="grid gap-1.5"><p className="font-medium">Nunca recomendar</p>
            <div className="flex flex-wrap gap-1.5">{s.excluded.length === 0 ? <span className="text-muted-foreground">Ningún producto excluido.</span> : s.excluded.map((p) => (
              <span key={p.id} className="inline-flex items-center gap-1 rounded-md border bg-muted px-2 py-1">{p.title}{canAdmin && <button type="button" aria-label={`Quitar ${p.title}`} className="text-muted-foreground hover:text-foreground" onClick={() => put({ exclude: cur.exclude!.filter((x) => x !== p.id) })}>×</button>}</span>))}</div>
            {canAdmin && <div><Button type="button" variant="outline" onClick={() => setPicker("exclude")}><Plus aria-hidden="true" /> Excluir producto</Button></div>}
          </div>
        </CardContent>
      </Card>
      <Card data-ai-explain>
        <CardHeader><CardTitle className="text-base">Por qué se recomienda</CardTitle><CardDescription>Elige un producto para ver qué muestra su ficha y con qué números: pedidos donde se compraron juntos y visitas donde se vieron juntos. Un pedido junto pesa como cinco visitas.</CardDescription></CardHeader>
        <CardContent className="grid gap-3 text-sm">
          <div className="flex flex-wrap items-center gap-2"><Button type="button" variant="outline" onClick={() => setPicker("explain")}>{explain ? "Cambiar producto" : "Elegir producto"}</Button>{explain && <span className="font-medium">{explain.title}</span>}</div>
          {explain && recs.data && (recs.data.items.length === 0 ? <p className="text-muted-foreground">Todavía no hay pedidos ni visitas que lo junten con otro producto.</p> : (
            <div className="overflow-auto rounded-md border"><Table>
              <TableHeader><TableRow><TableHead>Recomendado</TableHead><TableHead className="text-right">Comprados juntos</TableHead><TableHead className="text-right">Vistos juntos</TableHead></TableRow></TableHeader>
              <TableBody>{recs.data.items.map((r) => <TableRow key={r.id}><TableCell>{r.title}</TableCell><TableCell className="text-right tabular-nums">{r.buys}</TableCell><TableCell className="text-right tabular-nums">{r.views}</TableCell></TableRow>)}</TableBody></Table></div>))}
          {recs.data?.off && <Notice tone="warn">Las recomendaciones están apagadas: la tienda no las muestra.</Notice>}
        </CardContent>
      </Card>
      <Card data-ai-mcp className="xl:col-span-2">
        <CardHeader><CardTitle className="text-base">Asistentes de IA (MCP)</CardTitle>
          <CardDescription>Permite que asistentes como Claude o ChatGPT consulten tu catálogo, precios, existencias y envío, y armen un carrito para tu cliente. El asistente nunca paga: le da al cliente un enlace a tu pago de siempre, con los mismos límites y antifraude.</CardDescription></CardHeader>
        <CardContent className="grid gap-3 text-sm">
          <label className="flex items-center gap-2"><input type="checkbox" checked={!!cur.mcp} disabled={!canAdmin || save.isPending} onChange={(e) => put({ mcp: e.target.checked })} /> Abrir mi tienda a asistentes de IA {cur.mcp ? <Badge variant="ok">Activo</Badge> : <Badge variant="outline">Apagado</Badge>}</label>
          <p className="min-w-0 break-all text-muted-foreground">Dirección del servidor MCP: <code>{s.mcpUrl}</code></p>
        </CardContent>
      </Card>
      <ProductPicker open={!!picker} title={picker === "exclude" ? "Nunca recomendar" : "Ver recomendaciones de"} exclude={picker === "exclude" ? cur.exclude! : []} onClose={() => setPicker(null)}
        onPick={(ps) => {
          if (picker === "exclude") put({ exclude: [...cur.exclude!, ...ps.map((p) => p.id)] });
          else if (ps[0]) setExplain({ id: ps[0].id, title: ps[0].title, handle: "" });
          setPicker(null);
        }} />
      {!canEdit && null}
    </div>
  );
}
