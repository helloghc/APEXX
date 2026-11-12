import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Download, Play, Save, Trash2 } from "lucide-react";
import { errorMessage, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { download } from "./api";
import { Chart, SERIES_COLORS } from "./charts";
import { customError, Filters, useReportParams } from "./controls";
import { compactMoney, money, num } from "./format";

interface DimInfo { label: string; lines?: boolean; time?: boolean }
interface MetricInfo { label: string; money?: boolean; for: "orders" | "lines" | "both" }
interface CatalogInfo { dims: Record<string, DimInfo>; metrics: Record<string, MetricInfo>; statuses: Record<string, string>; sources: Record<string, string> }
interface Def { id?: string; name: string; dataset: "orders" | "lines"; dims: string[]; metrics: string[]; statuses: string[]; source?: string; sortBy?: string; asc?: boolean; limit?: number; chart?: "table" | "bar" | "line"; range?: string }
interface Result { columns: { key: string; label: string; kind: "dim" | "number" | "money" }[]; rows: (string | number)[][]; totals: number[]; truncated: boolean; from: string; to: string; tookMs: number }

const DIM_ORDER = ["day", "week", "month", "weekday", "hour", "product", "sku", "brand", "category", "channel", "source", "customer", "company", "shipping", "payment", "campaign", "gift", "postal", "status", "currency"];
const METRIC_ORDER = ["orders", "revenue", "net", "aov", "units", "sales", "avgprice", "customers", "items", "discounts", "shipping", "fees", "tax", "refunds", "returned"];
const BLANK: Def = { name: "", dataset: "orders", dims: ["day"], metrics: ["orders", "revenue"], statuses: ["paid", "fulfilled", "refunded"], chart: "line", limit: 200 };
const TEMPLATES: { label: string; def: Def }[] = [
  { label: "Ventas por mes y canal", def: { ...BLANK, name: "Ventas por mes y canal", dims: ["month", "channel"], metrics: ["orders", "revenue", "net"], chart: "bar" } },
  { label: "Más vendidos por marca", def: { ...BLANK, name: "Más vendidos por marca", dataset: "lines", dims: ["brand"], metrics: ["units", "sales"], sortBy: "units", chart: "bar" } },
  { label: "Clientes nuevos vs recurrentes", def: { ...BLANK, name: "Clientes nuevos vs recurrentes", dims: ["customer"], metrics: ["orders", "revenue", "aov"], chart: "bar" } },
  { label: "Caja vs tienda en línea", def: { ...BLANK, name: "Caja vs tienda en línea", dims: ["source"], metrics: ["orders", "revenue", "aov"], chart: "bar" } },
  { label: "Horas con más ventas", def: { ...BLANK, name: "Horas con más ventas", dims: ["hour"], metrics: ["orders", "revenue"], chart: "bar" } },
];

/** Custom: the store builds its own report from listed pieces, saves it and exports it. */
export function Custom({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "reports", "edit");
  const [params, setParams] = useReportParams();
  const cat = useQuery({ queryKey: ["custom-catalog"], queryFn: () => request<CatalogInfo>("GET", "/v1/reports/custom/catalog"), staleTime: Infinity });
  const saved = useQuery({ queryKey: ["custom-reports"], queryFn: () => request<{ items: Def[] }>("GET", "/v1/reports/custom").then((r) => r.items) });
  const [def, setDef] = useState<Def>(BLANK);
  const [ran, setRan] = useState<Def | null>(BLANK);
  const qs = `range=${params.range}${params.range === "custom" ? `&from=${params.from ?? ""}&to=${params.to ?? ""}` : ""}`;
  const res = useQuery({
    queryKey: ["custom-run", ran, qs],
    enabled: !!ran && !customError(params),
    placeholderData: keepPreviousData,
    queryFn: () => request<Result>("POST", `/v1/reports/custom/run?${qs}`, ran),
  });
  if (cat.isPending) return <p className="text-sm text-muted-foreground">Cargando…</p>;
  if (cat.error) return <ErrorBox error={cat.error} />;
  const c = cat.data;
  const rank = (order: string[]) => (a: [string, unknown], b: [string, unknown]) => (order.indexOf(a[0]) + 1 || 99) - (order.indexOf(b[0]) + 1 || 99);
  const dimsFor = Object.entries(c.dims).filter(([, d]) => def.dataset === "lines" || !d.lines).sort(rank(DIM_ORDER));
  const metricsFor = Object.entries(c.metrics).filter(([, m]) => m.for === "both" || m.for === def.dataset).sort(rank(METRIC_ORDER));
  const set = (p: Partial<Def>) => setDef((d) => ({ ...d, ...p }));
  const setDataset = (ds: Def["dataset"]) => set({
    dataset: ds,
    dims: def.dims.filter((k) => ds === "lines" || !c.dims[k]?.lines),
    metrics: (() => { const m = def.metrics.filter((k) => c.metrics[k] && (c.metrics[k].for === "both" || c.metrics[k].for === ds)); return m.length ? m : [ds === "lines" ? "units" : "orders"]; })(),
    sortBy: undefined,
  });
  const toggle = (list: string[], k: string, max: number) => (list.includes(k) ? list.filter((x) => x !== k) : list.length < max ? [...list, k] : list);
  async function save() {
    if (!def.name.trim()) { toast.error("Ponle un nombre al reporte."); return; }
    try {
      const out = await request<Def>(def.id ? "PUT" : "POST", def.id ? `/v1/reports/custom/${def.id}` : "/v1/reports/custom", { ...def, range: params.range === "custom" ? undefined : params.range });
      setDef(out); toast.success("Reporte guardado.");
      void qc.invalidateQueries({ queryKey: ["custom-reports"] });
    } catch (e) { toast.error(errorMessage(e)); }
  }
  async function remove() {
    if (!def.id || !window.confirm(`¿Borrar el reporte «${def.name}»?`)) return;
    try { await request("DELETE", `/v1/reports/custom/${def.id}`, undefined, { as: "none" }); setDef(BLANK); toast.success("Reporte borrado."); void qc.invalidateQueries({ queryKey: ["custom-reports"] }); } catch (e) { toast.error(errorMessage(e)); }
  }
  const open = (d: Def) => { setDef(d); setRan(d); if (d.range && d.range !== params.range) setParams({ ...params, range: d.range }); };

  return (
    <div className="grid min-w-0 gap-4 [&>*]:min-w-0">
      <div className="flex flex-wrap items-center gap-2">
        <Label htmlFor="cr-open" className="text-sm">Mis reportes</Label>
        <select id="cr-open" className="h-9 rounded-md border bg-background px-2 text-sm" value={def.id ?? ""} onChange={(e) => { const d = saved.data?.find((x) => x.id === e.target.value); open(d ?? BLANK); }}>
          <option value="">Nuevo reporte</option>
          {saved.data?.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
        </select>
        <span className="text-sm text-muted-foreground">o empieza con:</span>
        {TEMPLATES.map((t) => <Button key={t.label} size="sm" variant="outline" onClick={() => open({ ...t.def })}>{t.label}</Button>)}
      </div>

      <Card>
        <CardHeader><CardTitle className="text-base">Arma tu reporte</CardTitle></CardHeader>
        <CardContent className="grid gap-4">
          <div className="grid gap-3 sm:grid-cols-[1fr_auto]">
            <div className="grid gap-1.5"><Label htmlFor="cr-name">Nombre</Label><Input id="cr-name" value={def.name} maxLength={80} onChange={(e) => set({ name: e.target.value })} placeholder="Ej. Ventas por marca este mes" /></div>
            <fieldset className="grid gap-1.5"><legend className="text-sm font-medium">Qué contar</legend>
              <div className="flex gap-2" role="radiogroup">
                <Button type="button" role="radio" aria-checked={def.dataset === "orders"} variant={def.dataset === "orders" ? "default" : "outline"} onClick={() => setDataset("orders")}>Pedidos</Button>
                <Button type="button" role="radio" aria-checked={def.dataset === "lines"} variant={def.dataset === "lines" ? "default" : "outline"} onClick={() => setDataset("lines")}>Productos vendidos</Button>
              </div>
            </fieldset>
          </div>
          <fieldset><legend className="mb-1 text-sm font-medium">Agrupar por (hasta 2, en orden)</legend>
            <div className="flex flex-wrap gap-1.5">
              {dimsFor.map(([k, d]) => { const i = def.dims.indexOf(k); return (
                <Button key={k} size="sm" type="button" aria-pressed={i >= 0} variant={i >= 0 ? "default" : "outline"} onClick={() => set({ dims: toggle(def.dims, k, 2) })}>{i >= 0 ? `${i + 1}. ` : ""}{d.label}</Button>
              ); })}
            </div>
          </fieldset>
          <fieldset><legend className="mb-1 text-sm font-medium">Cifras (hasta 6)</legend>
            <div className="flex flex-wrap gap-1.5">
              {metricsFor.map(([k, m]) => <Button key={k} size="sm" type="button" aria-pressed={def.metrics.includes(k)} variant={def.metrics.includes(k) ? "default" : "outline"} onClick={() => set({ metrics: toggle(def.metrics, k, 6), sortBy: def.sortBy === k ? undefined : def.sortBy })}>{m.label}</Button>)}
            </div>
          </fieldset>
          <div className="grid gap-3 sm:grid-cols-4">
            <fieldset className="sm:col-span-2"><legend className="mb-1 text-sm font-medium">Pedidos que cuentan</legend>
              <div className="flex flex-wrap gap-x-3 gap-y-1">
                {Object.entries(c.statuses).map(([k, l]) => <label key={k} className="flex items-center gap-1.5 text-sm"><input type="checkbox" checked={def.statuses.includes(k)} onChange={() => set({ statuses: toggle(def.statuses, k, 9) })} /> {l}</label>)}
              </div>
            </fieldset>
            <div className="grid gap-1.5"><Label htmlFor="cr-src">Origen</Label>
              <select id="cr-src" className="h-9 rounded-md border bg-background px-2 text-sm" value={def.source ?? ""} onChange={(e) => set({ source: e.target.value || undefined })}>
                <option value="">Todos</option>{Object.entries(c.sources).map(([k, l]) => <option key={k} value={k}>{l}</option>)}
              </select></div>
            <div className="grid gap-1.5"><Label htmlFor="cr-sort">Ordenar por</Label>
              <select id="cr-sort" className="h-9 rounded-md border bg-background px-2 text-sm" value={def.sortBy ? `${def.sortBy}:${def.asc ? "asc" : "desc"}` : ""} onChange={(e) => { const [k, dir] = e.target.value.split(":"); set({ sortBy: k || undefined, asc: dir === "asc" }); }}>
                <option value="">Automático</option>
                {def.metrics.flatMap((k) => [<option key={k + "d"} value={`${k}:desc`}>{c.metrics[k]?.label} (mayor primero)</option>, <option key={k + "a"} value={`${k}:asc`}>{c.metrics[k]?.label} (menor primero)</option>])}
              </select></div>
            <div className="grid gap-1.5"><Label htmlFor="cr-chart">Mostrar</Label>
              <select id="cr-chart" className="h-9 rounded-md border bg-background px-2 text-sm" value={def.chart ?? "table"} onChange={(e) => set({ chart: e.target.value as Def["chart"] })}>
                <option value="table">Solo tabla</option><option value="bar">Barras</option><option value="line">Líneas</option>
              </select></div>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => setRan({ ...def })} disabled={!def.metrics.length || !def.statuses.length}><Play className="size-4" /> Ver reporte</Button>
            {canEdit && <Button variant="outline" onClick={() => void save()}><Save className="size-4" /> {def.id ? "Guardar cambios" : "Guardar reporte"}</Button>}
            {canEdit && def.id && <Button variant="ghost" className="text-destructive" onClick={() => void remove()}><Trash2 className="size-4" /> Borrar</Button>}
          </div>
        </CardContent>
      </Card>

      <Filters params={params} onChange={setParams}>
        <Button variant="outline" disabled={!ran} onClick={() => ran && download(`/v1/reports/custom/run?${qs}&format=csv`, "reporte.csv", ran).then(() => toast.success("Archivo CSV descargado")).catch((e: unknown) => toast.error(errorMessage(e)))}><Download className="size-4" /> Exportar CSV</Button>
      </Filters>
      {res.error ? <ErrorBox error={res.error} /> : res.data ? <ResultView r={res.data} def={ran!} /> : res.isFetching ? <p className="text-sm text-muted-foreground">Calculando…</p> : null}
    </div>
  );
}

function ResultView({ r, def }: { r: Result; def: Def }) {
  const dims = r.columns.filter((c) => c.kind === "dim");
  const mets = r.columns.filter((c) => c.kind !== "dim");
  const fmt = (v: string | number, kind: string) => (kind === "money" ? money(Number(v)) : kind === "number" ? num(Number(v)) : String(v));
  // The chart shows the first figure; a second grouping becomes one series per value.
  const chart = useMemo(() => {
    if (def.chart === "table" || !dims.length || !r.rows.length) return null;
    const m = mets[0]!, mi = dims.length;
    if (dims.length === 1) {
      return { labels: r.rows.map((x) => String(x[0])), series: [{ name: m.label, values: r.rows.map((x) => Number(x[mi])), color: SERIES_COLORS[0]!, kind: def.chart === "line" ? "area" as const : "bar" as const }], m };
    }
    const labels = [...new Set(r.rows.map((x) => String(x[0])))];
    const keys = [...new Set(r.rows.map((x) => String(x[1])))].slice(0, 6);
    const series = keys.map((k, i) => ({ name: k, color: SERIES_COLORS[i % SERIES_COLORS.length]!, kind: def.chart === "line" ? "line" as const : "bar" as const,
      values: labels.map((l) => Number(r.rows.find((x) => String(x[0]) === l && String(x[1]) === k)?.[mi] ?? 0)) }));
    return { labels, series, m };
  }, [r, def, dims, mets]);
  if (!r.rows.length && dims.length) return <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">No hay pedidos con esos filtros en el periodo.</p>;
  return (
    <Card data-custom-result>
      <CardHeader><CardTitle className="text-base">{def.name || "Reporte"} <span className="text-sm font-normal text-muted-foreground">· {r.from} a {r.to} · {r.tookMs.toFixed(0)} ms</span></CardTitle></CardHeader>
      <CardContent className="grid min-w-0 gap-4 [&>*]:min-w-0">
        {chart && <Chart labels={chart.labels} series={chart.series} stacked={def.chart === "bar" && chart.series.length > 1} format={(v) => fmt(v, chart.m.kind)} axisFormat={chart.m.kind === "money" ? compactMoney : num} summary={`${chart.m.label} por ${dims.map((d) => d.label.toLowerCase()).join(" y ")}`} />}
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead><tr className="text-left text-muted-foreground">{r.columns.map((c) => <th key={c.key} className={c.kind === "dim" ? "py-1 pr-3" : "py-1 pr-3 text-right"}>{c.label}</th>)}</tr></thead>
            <tbody>
              {r.rows.map((row, i) => <tr key={i} className="border-t">{row.map((v, j) => <td key={j} className={r.columns[j]!.kind === "dim" ? "py-1.5 pr-3" : "py-1.5 pr-3 text-right tabular-nums"}>{fmt(v, r.columns[j]!.kind)}</td>)}</tr>)}
            </tbody>
            <tfoot><tr className="border-t-2 font-semibold">{dims.map((d, i) => <td key={d.key} className="py-1.5 pr-3">{i === 0 ? "Total" : ""}</td>)}{r.totals.map((v, i) => <td key={i} className="py-1.5 pr-3 text-right tabular-nums">{fmt(v, mets[i]!.kind)}</td>)}</tr></tfoot>
          </table>
        </div>
        {r.truncated && <p className="text-xs text-muted-foreground">Se muestran las primeras {def.limit ?? 200} filas; el total incluye todas.</p>}
      </CardContent>
    </Card>
  );
}
