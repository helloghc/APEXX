import { keepPreviousData, useQuery, type UseQueryResult } from "@tanstack/react-query";
import { Boxes, Filter as FilterIcon, LineChart, Megaphone, Package, SlidersHorizontal, Users } from "lucide-react";
import { Custom } from "./Custom";
import { useState, type ReactNode } from "react";
import type { Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { can } from "@/perms";
import { href } from "@/router";
import { dateTime, ErrorBox, Page } from "../common";
import { query, reportsApi, type CustomerRow, type Point, type ProductRow, type PromoRow, type ReportParams, type StockRow } from "./api";
import { BarList, Chart, Funnel, PREV_COLOR, SERIES_COLORS, Split } from "./charts";
import { CardSkeleton, ChartCard, customError, DataTable, Empty, ExportButton, Filters, Kpi, KpiSkeleton, useReportParams, type Column } from "./controls";
import { bucketLabel, compact, compactMoney, money, num, rangeLabel, ratio, weekdayName } from "./format";

export const REPORTS = [
  { id: "ventas", label: "Ventas", icon: LineChart },
  { id: "productos", label: "Productos", icon: Package },
  { id: "clientes", label: "Clientes", icon: Users },
  { id: "embudo", label: "Embudo", icon: FilterIcon },
  { id: "promociones", label: "Promociones", icon: Megaphone },
  { id: "inventario", label: "Inventario", icon: Boxes },
  { id: "personalizados", label: "Personalizados", icon: SlidersHorizontal },
] as const;

export function SubNav({ base, items, active, label }: { base: string; items: readonly { id: string; label: string; icon?: typeof Boxes }[]; active: string; label: string }) {
  return (
    <nav aria-label={label} className="-mx-4 overflow-x-auto px-4 md:mx-0 md:px-0">
      <ul className="flex w-max gap-1 rounded-lg bg-muted p-1">
        {items.map((r) => {
          const Icon = r.icon;
          const on = r.id === active;
          return (
            <li key={r.id}>
              <a
                href={href(base, r.id)}
                aria-current={on ? "page" : undefined}
                className={cn(
                  "inline-flex items-center gap-1.5 whitespace-nowrap rounded-md px-3 py-1.5 text-sm font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                  on ? "bg-card text-foreground shadow-xs" : "text-muted-foreground hover:text-foreground",
                )}
              >
                {Icon && <Icon className="size-4" aria-hidden="true" />}
                {r.label}
              </a>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}

/** Body renders the loading, error and stale states every report shares. */
function Body<T>({ q, invalid, children, kpis = 4 }: { q: UseQueryResult<T>; invalid: boolean; children: (d: T) => ReactNode; kpis?: number }) {
  return (
    <>
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {q.data ? (
        <div className={cn("grid gap-4 transition-opacity", q.isPlaceholderData && "opacity-60")} aria-busy={q.isFetching}>
          {children(q.data)}
        </div>
      ) : (
        q.isPending &&
        !invalid && (
          <div className="grid gap-4" role="status" aria-label="Cargando el reporte">
            {kpis > 0 && <KpiSkeleton n={kpis} />}
            <CardSkeleton />
          </div>
        )
      )}
    </>
  );
}

const grainName: Record<string, string> = { hour: "Hora", day: "Día", week: "Semana", month: "Mes" };

function seriesLabels(series: { bucket: string }[], grain: "hour" | "day" | "week" | "month") {
  return { labels: series.map((p) => bucketLabel(p.bucket, grain)), long: series.map((p) => bucketLabel(p.bucket, grain, true)) };
}

// ---------- Ventas ----------

function Ventas() {
  const [params, setParams] = useReportParams();
  const [grain, setGrain] = useState("auto");
  const invalid = Boolean(customError(params));
  const g = grain === "auto" ? undefined : grain;
  const q = useQuery({ queryKey: ["reports", "sales", params, grain], queryFn: () => reportsApi.sales(params, g), enabled: !invalid, placeholderData: keepPreviousData });
  const d = q.data;
  return (
    <>
      <Filters params={params} onChange={setParams} channels={d?.channels} info={d?.range}>
        <Select value={grain} onValueChange={setGrain}>
          <SelectTrigger className="w-[10.5rem]" aria-label="Agrupar por">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="auto">Agrupar: automático</SelectItem>
            <SelectItem value="hour">Por hora</SelectItem>
            <SelectItem value="day">Por día</SelectItem>
            <SelectItem value="week">Por semana</SelectItem>
            <SelectItem value="month">Por mes</SelectItem>
          </SelectContent>
        </Select>
        <div className="ml-auto">
          <ExportButton path={`/v1/reports/sales?${query(params, { grain: g, format: "csv" })}`} name="apexx-ventas.csv" disabled={invalid} />
        </div>
      </Filters>
      <Body q={q} invalid={invalid} kpis={8}>
        {(d) => {
          const { labels, long } = seriesLabels(d.series, d.grain);
          const t = d.totals;
          const p = d.previousTotals;
          const cur = d.currency;
          const cols: Column<Point>[] = [
            { id: "bucket", header: grainName[d.grain] ?? "Periodo", cell: (r) => <span className="whitespace-nowrap font-medium">{bucketLabel(r.bucket, d.grain, true)}</span>, sort: (r) => r.bucket, footer: "Total" },
            { id: "orders", header: "Pedidos", num: true, cell: (r) => num(r.orders), sort: (r) => r.orders, footer: num(t.orders) },
            { id: "units", header: "Unidades", num: true, wide: true, cell: (r) => num(r.units), sort: (r) => r.units, footer: num(t.units) },
            { id: "gross", header: "Brutas", num: true, cell: (r) => money(r.gross), sort: (r) => r.gross, footer: money(t.gross) },
            { id: "discounts", header: "Descuentos", num: true, wide: true, cell: (r) => money(r.discounts), sort: (r) => r.discounts, footer: money(t.discounts) },
            { id: "shipping", header: "Envío", num: true, wide: true, cell: (r) => money(r.shipping), sort: (r) => r.shipping, footer: money(t.shipping) },
            { id: "refunds", header: "Reembolsos", num: true, cell: (r) => money(r.refunds), sort: (r) => r.refunds, footer: money(t.refunds) },
            { id: "net", header: "Netas", num: true, cell: (r) => <strong>{money(r.net)}</strong>, sort: (r) => r.net, footer: money(t.net) },
            { id: "tax", header: "IVA incluido", num: true, wide: true, cell: (r) => money(r.tax), sort: (r) => r.tax, footer: money(t.tax) },
            { id: "aov", header: "Ticket prom.", num: true, wide: true, cell: (r) => money(r.aov), sort: (r) => r.aov, footer: money(t.aov) },
          ];
          const hasSales = t.orders > 0 || t.refunds > 0;
          return (
            <>
              <section aria-label="Indicadores de venta" className="grid grid-cols-2 gap-3 lg:grid-cols-4">
                <Kpi label={`Ventas brutas (${cur})`} value={compactMoney(t.gross)} current={t.gross} previous={p.gross} previousText={`Antes: ${compactMoney(p.gross)}`} spark={d.series.map((x) => x.gross)} />
                <Kpi label="Descuentos" value={compactMoney(t.discounts)} current={t.discounts} previous={p.discounts} invert previousText={`${ratio(t.discounts, t.gross)} de las ventas brutas`} />
                <Kpi label="Reembolsos" value={compactMoney(t.refunds)} current={t.refunds} previous={p.refunds} invert previousText={`${num(t.refundCount)} ${t.refundCount === 1 ? "pedido devuelto" : "pedidos devueltos"}`} />
                <Kpi label={`Ventas netas (${cur})`} value={compactMoney(t.net)} current={t.net} previous={p.net} previousText={`Antes: ${compactMoney(p.net)}`} spark={d.series.map((x) => x.net)} />
                <Kpi label="Pedidos" value={num(t.orders)} current={t.orders} previous={p.orders} previousText={`Antes: ${num(p.orders)}`} />
                <Kpi label="Ticket promedio" value={money(t.aov)} current={t.aov} previous={p.aov} previousText={`Antes: ${money(p.aov)}`} />
                <Kpi label="Unidades vendidas" value={num(t.units)} current={t.units} previous={p.units} previousText={t.orders ? `${(t.units / t.orders).toFixed(1)} por pedido` : ""} />
                <Kpi label="IVA incluido" value={compactMoney(t.tax)} hint="Ya forma parte de los importes" />
              </section>
              {!hasSales ? (
                <Empty icon={<LineChart />} title="Sin ventas en este periodo">
                  No se pagó ni se reembolsó ningún pedido entre el {rangeLabel(d.range)}. Elige otro periodo para ver tu historial.
                </Empty>
              ) : (
                <>
                  <ChartCard
                    title={`Ventas netas por ${(grainName[d.grain] ?? "día").toLowerCase()} (${cur})`}
                    description="Productos − descuentos + envío − reembolsos. Los reembolsos cuentan el día en que se devolvió el dinero."
                    legend={[
                      { name: "Este periodo", color: SERIES_COLORS[0], line: true },
                      { name: `Periodo anterior (${rangeLabel(d.previous)})`, color: PREV_COLOR, line: true },
                    ]}
                    table={{ head: [grainName[d.grain] ?? "Periodo", "Netas", "Pedidos", "Periodo anterior"], rows: d.series.map((x, i) => [long[i] ?? "", money(x.net), num(x.orders), money(d.previousSeries[i]?.net ?? 0)]) }}
                  >
                    <Chart
                      labels={labels}
                      longLabels={long}
                      series={[
                        { name: "Periodo anterior", values: d.series.map((_, i) => d.previousSeries[i]?.net ?? 0), color: PREV_COLOR, kind: "line" },
                        { name: "Este periodo", values: d.series.map((x) => x.net), color: SERIES_COLORS[0], kind: "area" },
                      ]}
                      format={money}
                      axisFormat={compactMoney}
                      height={300}
                      summary={`Ventas netas: ${money(t.net)} ${cur} en ${t.orders} pedidos; periodo anterior ${money(p.net)}.`}
                      extra={(i) => [
                        { label: "Pedidos", value: num(d.series[i]?.orders ?? 0) },
                        { label: "Reembolsos", value: money(d.series[i]?.refunds ?? 0) },
                      ]}
                    />
                  </ChartCard>
                  <div className="grid gap-4 lg:grid-cols-3">
                    <ChartCard
                      title="Por hora del día"
                      description="Ventas brutas según la hora en que se pagó"
                      table={{ head: ["Hora", "Brutas", "Pedidos"], rows: d.byHour.map((h) => [`${h.slot}:00`, money(h.gross), num(h.orders)]) }}
                    >
                      <Chart
                        labels={d.byHour.map((h) => `${h.slot} h`)}
                        longLabels={d.byHour.map((h) => `De ${h.slot}:00 a ${h.slot}:59`)}
                        series={[{ name: "Ventas brutas", values: d.byHour.map((h) => h.gross), color: SERIES_COLORS[0], kind: "bar" }]}
                        format={money}
                        axisFormat={compactMoney}
                        height={220}
                        summary="Ventas brutas por hora del día"
                        extra={(i) => [{ label: "Pedidos", value: num(d.byHour[i]?.orders ?? 0) }]}
                      />
                    </ChartCard>
                    <ChartCard
                      title="Por día de la semana"
                      description="Ventas brutas según el día en que se pagó"
                      table={{ head: ["Día", "Brutas", "Pedidos"], rows: d.byWeekday.map((h) => [weekdayName(h.slot), money(h.gross), num(h.orders)]) }}
                    >
                      <Chart
                        labels={d.byWeekday.map((h) => weekdayName(h.slot).slice(0, 3))}
                        longLabels={d.byWeekday.map((h) => weekdayName(h.slot))}
                        series={[{ name: "Ventas brutas", values: d.byWeekday.map((h) => h.gross), color: SERIES_COLORS[0], kind: "bar" }]}
                        format={money}
                        axisFormat={compactMoney}
                        height={220}
                        summary="Ventas brutas por día de la semana"
                        extra={(i) => [{ label: "Pedidos", value: num(d.byWeekday[i]?.orders ?? 0) }]}
                      />
                    </ChartCard>
                    <Card className="min-w-0">
                      <CardHeader>
                        <CardTitle>Por canal</CardTitle>
                        <CardDescription>Ventas netas de cada canal ({cur})</CardDescription>
                      </CardHeader>
                      <CardContent>
                        <BarList format={money} rows={d.byChannel.map((c) => ({ key: c.channel, label: `Canal ${c.channel}`, value: Math.max(0, c.net), text: money(c.net), sub: `${num(c.orders)} pedidos · ticket ${money(c.aov)}` }))} />
                      </CardContent>
                    </Card>
                  </div>
                  <DataTable caption="Ventas por periodo" rows={d.series} columns={cols} rowKey={(r) => r.bucket} pageSize={31} initialSort={{ id: "bucket", desc: true }} />
                </>
              )}
            </>
          );
        }}
      </Body>
    </>
  );
}

// ---------- Productos ----------

const BY = [
  { id: "product", label: "Producto" },
  { id: "sku", label: "SKU (variante)" },
  { id: "brand", label: "Marca" },
  { id: "category", label: "Categoría" },
];

function Productos({ me }: { me: Me }) {
  const [params, setParams] = useReportParams();
  const [by, setBy] = useState("product");
  const invalid = Boolean(customError(params));
  const q = useQuery({ queryKey: ["reports", "products", params, by], queryFn: () => reportsApi.products(params, by), enabled: !invalid, placeholderData: keepPreviousData });
  const d = q.data;
  const byLabel = BY.find((b) => b.id === by)?.label ?? "Producto";
  return (
    <>
      <Filters params={params} onChange={setParams} channels={d?.channels} info={d?.range}>
        <Select value={by} onValueChange={setBy}>
          <SelectTrigger className="w-[11.5rem]" aria-label="Agrupar por">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {BY.map((b) => (
              <SelectItem key={b.id} value={b.id}>
                Por {b.label.toLowerCase()}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <div className="ml-auto">
          <ExportButton path={`/v1/reports/products?${query(params, { by, format: "csv" })}`} name="apexx-productos.csv" disabled={invalid} />
        </div>
      </Filters>
      <Body q={q} invalid={invalid} kpis={3}>
        {(d) => {
          const sold = d.rows.filter((r) => r.revenue > 0);
          const traffic = d.by === "product";
          const cols: Column<ProductRow>[] = [
            {
              id: "name",
              header: byLabel,
              sort: (r) => r.name,
              cell: (r) =>
                r.productId && can(me, "catalog", "read") ? (
                  <a className="font-medium text-primary hover:underline" href={href("catalogo", r.productId)}>
                    {r.name}
                  </a>
                ) : (
                  <span className="font-medium">{r.name}</span>
                ),
              footer: "Total",
            },
            ...(d.by === "product" || d.by === "sku" ? [{ id: "brand", header: "Marca", wide: true, cell: (r: ProductRow) => r.brand || "—", sort: (r: ProductRow) => r.brand ?? "" }] : []),
            ...(d.by === "sku" ? [{ id: "key", header: "SKU", wide: true, cell: (r: ProductRow) => <code className="font-mono text-xs">{r.key}</code>, sort: (r: ProductRow) => r.key }] : []),
            { id: "units", header: "Unidades", num: true, cell: (r) => num(r.units), sort: (r) => r.units, footer: num(d.totals.units) },
            { id: "revenue", header: `Ingresos (${d.currency})`, num: true, cell: (r) => <strong>{money(r.revenue)}</strong>, sort: (r) => r.revenue, footer: money(d.totals.revenue) },
            { id: "share", header: "% del total", num: true, wide: true, cell: (r) => ratio(r.revenue, d.totals.revenue), sort: (r) => r.revenue },
            ...(traffic
              ? [
                  { id: "views", header: "Vistas", num: true, wide: true, cell: (r: ProductRow) => num(r.views), sort: (r: ProductRow) => r.views },
                  {
                    id: "cart",
                    header: "Vista → carrito",
                    num: true,
                    wide: true,
                    cell: (r: ProductRow) => ratio(r.cartVisitors, r.visitors),
                    sort: (r: ProductRow) => (r.visitors ? r.cartVisitors / r.visitors : -1),
                  },
                ]
              : []),
          ];
          return d.rows.length === 0 ? (
            <Empty icon={<Package />} title="Nada vendido en este periodo">
              Cuando se pague un pedido verás aquí qué productos, marcas y categorías venden más.
            </Empty>
          ) : (
            <>
              <section aria-label="Indicadores de producto" className="grid grid-cols-2 gap-3 lg:grid-cols-3">
                <Kpi label={`Ingresos por productos (${d.currency})`} value={compactMoney(d.totals.revenue)} hint="A precio de venta, antes de descuentos de pedido" />
                <Kpi label="Unidades vendidas" value={num(d.totals.units)} hint={`${num(sold.length)} ${byLabel.toLowerCase()}(s) con venta`} />
                <Kpi label={`Mayor venta`} value={sold[0] ? ratio(sold[0].revenue, d.totals.revenue) : "—"} hint={sold[0] ? `${sold[0].name}` : ""} />
              </section>
              <ChartCard
                title={`Los 10 con más ingresos, por ${byLabel.toLowerCase()}`}
                description={d.by === "category" ? "Un producto en dos categorías cuenta en ambas." : undefined}
                table={{ head: [byLabel, "Unidades", "Ingresos"], rows: sold.slice(0, 10).map((r) => [r.name, num(r.units), money(r.revenue)]) }}
              >
                <BarList
                  format={money}
                  rows={sold.slice(0, 10).map((r) => ({ key: r.key, label: r.name, value: r.revenue, sub: `${num(r.units)} unidades · ${ratio(r.revenue, d.totals.revenue)} del total` }))}
                />
              </ChartCard>
              <DataTable
                caption={`Ventas por ${byLabel.toLowerCase()}`}
                rows={d.rows}
                columns={cols}
                rowKey={(r) => r.key}
                search={(r) => `${r.name} ${r.key} ${r.brand ?? ""}`}
                searchLabel={`Buscar ${byLabel.toLowerCase()}`}
                initialSort={{ id: "revenue", desc: true }}
              />
            </>
          );
        }}
      </Body>
    </>
  );
}

// ---------- Clientes ----------

function Clientes({ me }: { me: Me }) {
  const [params, setParams] = useReportParams();
  const invalid = Boolean(customError(params));
  const q = useQuery({ queryKey: ["reports", "customers", params], queryFn: () => reportsApi.customers(params), enabled: !invalid, placeholderData: keepPreviousData });
  const d = q.data;
  return (
    <>
      <Filters params={params} onChange={setParams} channels={d?.channels} info={d?.range}>
        <div className="ml-auto">
          <ExportButton path={`/v1/reports/customers?${query(params, { format: "csv" })}`} name="apexx-clientes.csv" disabled={invalid} />
        </div>
      </Filters>
      <Body q={q} invalid={invalid}>
        {(d) => {
          const { labels, long } = seriesLabels(d.series, d.grain);
          const freqLabels = ["1 pedido", "2 pedidos", "3 pedidos", "4 pedidos", "5 o más"];
          const cols: Column<CustomerRow>[] = [
            { id: "customer", header: "Cliente", cell: (r) => <code className="font-mono text-xs">{r.customer}</code> },
            { id: "kind", header: "Tipo", cell: (r) => <Badge variant={r.new ? "ok" : "secondary"}>{r.new ? "Nuevo" : "Recurrente"}</Badge>, sort: (r) => (r.new ? 0 : 1) },
            { id: "orders", header: "Pedidos", num: true, cell: (r) => num(r.orders), sort: (r) => r.orders },
            { id: "spent", header: `Gastado (${d.currency})`, num: true, cell: (r) => <strong>{money(r.spent)}</strong>, sort: (r) => r.spent },
            { id: "first", header: "Primer pedido del periodo", wide: true, cell: (r) => dateTime(r.firstOrderAt), sort: (r) => r.firstOrderAt },
            {
              id: "last",
              header: "Último pedido",
              cell: (r) =>
                can(me, "orders", "read") ? (
                  <a className="text-primary hover:underline" href={href("pedidos", r.lastOrderId)}>
                    #{r.lastOrderNumber}
                  </a>
                ) : (
                  `#${r.lastOrderNumber}`
                ),
              sort: (r) => r.lastOrderNumber,
            },
          ];
          return d.customers === 0 ? (
            <Empty icon={<Users />} title="Sin clientes en este periodo">
              Aquí verás cuántas personas compraron por primera vez y cuántas regresaron.
            </Empty>
          ) : (
            <>
              <section aria-label="Indicadores de clientes" className="grid grid-cols-2 gap-3 lg:grid-cols-4">
                <Kpi label="Clientes que compraron" value={num(d.customers)} current={d.customers} previous={d.previousCustomers} previousText={`Antes: ${num(d.previousCustomers)}`} />
                <Kpi label="Clientes nuevos" value={num(d.newCustomers)} current={d.newCustomers} previous={d.previousNewCustomers} previousText="Su primera compra fue en este periodo" />
                <Kpi label="Clientes que regresaron" value={num(d.returningCustomers)} current={d.returningCustomers} previous={d.previousReturningCustomers} previousText="Ya habían comprado antes" />
                <Kpi label="Pedidos de recompra" value={ratio(d.returningOrders, d.newOrders + d.returningOrders)} hint={`${num(d.returningOrders)} de ${num(d.newOrders + d.returningOrders)} pedidos`} />
              </section>
              <div className="grid gap-4 lg:grid-cols-3">
                <ChartCard
                  className="lg:col-span-2"
                  title="Pedidos de clientes nuevos y recurrentes"
                  legend={[
                    { name: "Clientes nuevos", color: SERIES_COLORS[0] },
                    { name: "Clientes que regresan", color: SERIES_COLORS[1] },
                  ]}
                  table={{ head: [grainName[d.grain] ?? "Periodo", "Nuevos", "Recurrentes"], rows: d.series.map((p, i) => [long[i] ?? "", num(p.newCustomers), num(p.returningCustomers)]) }}
                >
                  <Chart
                    labels={labels}
                    longLabels={long}
                    stacked
                    series={[
                      { name: "Clientes nuevos", values: d.series.map((p) => p.newCustomers), color: SERIES_COLORS[0], kind: "bar" },
                      { name: "Clientes que regresan", values: d.series.map((p) => p.returningCustomers), color: SERIES_COLORS[1], kind: "bar" },
                    ]}
                    format={num}
                    axisFormat={compact}
                    summary={`Pedidos por tipo de cliente: ${d.newOrders} de clientes nuevos y ${d.returningOrders} de clientes que regresan.`}
                  />
                </ChartCard>
                <div className="grid content-start gap-4">
                  <Card>
                    <CardHeader>
                      <CardTitle>Ventas por tipo de cliente</CardTitle>
                      <CardDescription>Total de sus pedidos ({d.currency})</CardDescription>
                    </CardHeader>
                    <CardContent>
                      <Split
                        format={compactMoney}
                        parts={[
                          { name: "Nuevos", value: d.newRevenue, color: SERIES_COLORS[0] },
                          { name: "Recurrentes", value: d.returningRevenue, color: SERIES_COLORS[1] },
                        ]}
                      />
                    </CardContent>
                  </Card>
                  <Card>
                    <CardHeader>
                      <CardTitle>Frecuencia de compra</CardTitle>
                      <CardDescription>Clientes según sus pedidos de toda la historia</CardDescription>
                    </CardHeader>
                    <CardContent>
                      <BarList format={num} rows={d.frequency.map((v, i) => ({ key: String(i), label: freqLabels[i] ?? "", value: v }))} />
                    </CardContent>
                  </Card>
                </div>
              </div>
              <div className="grid gap-2">
                <h2 className="text-base font-semibold">Clientes que más compraron</h2>
                <p className="text-sm text-muted-foreground">
                  Los reportes no guardan nombres ni correos: cada cliente es un código. Abre su último pedido para ver quién es, si tu acceso a Pedidos lo permite.
                </p>
              </div>
              <DataTable caption="Clientes que más compraron" rows={d.top} columns={cols} rowKey={(r) => r.customer} initialSort={{ id: "spent", desc: true }} pageSize={25} />
            </>
          );
        }}
      </Body>
    </>
  );
}

// ---------- Embudo ----------

const DEVICE: Record<string, string> = { mobile: "Celular", desktop: "Computadora", tablet: "Tableta" };

function Embudo({ me }: { me: Me }) {
  const [params, setParams] = useReportParams();
  const invalid = Boolean(customError(params));
  const q = useQuery({ queryKey: ["reports", "funnel", params], queryFn: () => reportsApi.funnel(params), enabled: !invalid, placeholderData: keepPreviousData });
  const d = q.data;
  return (
    <>
      <Filters params={params} onChange={setParams} channels={d?.channels} info={d?.range}>
        <div className="ml-auto">
          <ExportButton path={`/v1/reports/funnel?${query(params, { format: "csv" })}`} name="apexx-embudo.csv" disabled={invalid} />
        </div>
      </Filters>
      <Body q={q} invalid={invalid}>
        {(d) => {
          const t = d.totals;
          const p = d.previousTotals;
          const { labels, long } = seriesLabels(d.series, d.grain);
          const conv = (x: { sessions: number; purchase: number }) => (x.sessions ? (x.purchase / x.sessions) * 100 : 0);
          return t.sessions === 0 && p.sessions === 0 ? (
            <Empty icon={<FilterIcon />} title="Aún no hay visitas registradas">
              Las visitas a tu tienda se miden sin cookies ni servicios externos, y se omiten las de quien activó «No rastrear». Se guardan 90 días. Publica tu tienda y comparte el enlace para ver el recorrido de compra.
            </Empty>
          ) : (
            <>
              <section aria-label="Indicadores del embudo" className="grid grid-cols-2 gap-3 lg:grid-cols-4">
                <Kpi label="Sesiones" value={num(t.sessions)} current={t.sessions} previous={p.sessions} previousText={`Antes: ${num(p.sessions)}`} spark={d.series.map((x) => x.traffic.sessions)} />
                <Kpi label="Conversión a compra" value={ratio(t.purchase, t.sessions, 2)} current={conv(t)} previous={conv(p)} previousText={`Antes: ${ratio(p.purchase, p.sessions, 2)}`} />
                <Kpi label="Carritos que terminan en compra" value={ratio(t.purchase, t.addToCart)} current={t.addToCart ? t.purchase / t.addToCart : 0} previous={p.addToCart ? p.purchase / p.addToCart : 0} previousText={`${num(t.addToCart)} sesiones con carrito`} />
                <Kpi label="Páginas por sesión" value={t.sessions ? (t.pageViews / t.sessions).toFixed(1) : "—"} hint={`${num(t.pageViews)} páginas vistas`} />
              </section>
              <div className="grid gap-4 lg:grid-cols-5">
                <ChartCard
                  className="lg:col-span-3"
                  title="Recorrido de compra"
                  description="Sesiones que llegaron al menos hasta cada paso. Una sesión es una persona durante un día."
                  table={{
                    head: ["Paso", "Sesiones", "% del total"],
                    rows: [
                      ["Sesiones", num(t.sessions), "100%"],
                      ["Vieron un producto", num(t.productViews), ratio(t.productViews, t.sessions)],
                      ["Agregaron al carrito", num(t.addToCart), ratio(t.addToCart, t.sessions)],
                      ["Iniciaron el pago", num(t.checkout), ratio(t.checkout, t.sessions)],
                      ["Compraron", num(t.purchase), ratio(t.purchase, t.sessions)],
                    ],
                  }}
                >
                  <Funnel
                    format={num}
                    steps={[
                      { label: "Sesiones", value: t.sessions },
                      { label: "Vieron un producto", value: t.productViews },
                      { label: "Agregaron al carrito", value: t.addToCart },
                      { label: "Iniciaron el pago", value: t.checkout },
                      { label: "Compraron", value: t.purchase },
                    ]}
                  />
                </ChartCard>
                <div className="grid content-start gap-4 lg:col-span-2">
                  <Card>
                    <CardHeader>
                      <CardTitle>Por dispositivo</CardTitle>
                    </CardHeader>
                    <CardContent>
                      <BarList
                        format={num}
                        rows={d.byDevice.map((x) => ({ key: x.device, label: DEVICE[x.device] ?? x.device, value: x.sessions, sub: `${ratio(x.purchase, x.sessions, 2)} compra · ${ratio(x.addToCart, x.sessions)} agrega al carrito` }))}
                      />
                    </CardContent>
                  </Card>
                  <Card>
                    <CardHeader>
                      <CardTitle>De dónde llegan</CardTitle>
                      <CardDescription>Sitio de origen de la sesión (solo el dominio)</CardDescription>
                    </CardHeader>
                    <CardContent>
                      <BarList
                        format={num}
                        color={SERIES_COLORS[2]}
                        rows={d.referrers.slice(0, 8).map((x) => ({ key: x.referrer || "direct", label: x.referrer || "Directo o sin origen", value: x.sessions, sub: `${num(x.purchases)} compras · ${ratio(x.purchases, x.sessions, 2)}` }))}
                      />
                    </CardContent>
                  </Card>
                </div>
              </div>
              <ChartCard
                title={`Sesiones por ${(grainName[d.grain] ?? "día").toLowerCase()}`}
                table={{ head: [grainName[d.grain] ?? "Periodo", "Sesiones", "Compras", "Conversión"], rows: d.series.map((x, i) => [long[i] ?? "", num(x.traffic.sessions), num(x.traffic.purchase), ratio(x.traffic.purchase, x.traffic.sessions, 2)]) }}
              >
                <Chart
                  labels={labels}
                  longLabels={long}
                  series={[{ name: "Sesiones", values: d.series.map((x) => x.traffic.sessions), color: SERIES_COLORS[0], kind: "bar" }]}
                  format={num}
                  axisFormat={compact}
                  height={240}
                  summary={`Sesiones por periodo: ${t.sessions} en total, ${t.purchase} con compra.`}
                  extra={(i) => [
                    { label: "Compras", value: num(d.series[i]?.traffic.purchase ?? 0) },
                    { label: "Conversión", value: ratio(d.series[i]?.traffic.purchase ?? 0, d.series[i]?.traffic.sessions ?? 0, 2) },
                  ]}
                />
              </ChartCard>
              <h2 className="text-base font-semibold">Productos más vistos</h2>
              <DataTable
                caption="Productos más vistos"
                rows={d.topProducts}
                rowKey={(r) => r.key}
                initialSort={{ id: "views", desc: true }}
                columns={[
                  {
                    id: "name",
                    header: "Producto",
                    cell: (r) =>
                      can(me, "catalog", "read") ? (
                        <a className="font-medium text-primary hover:underline" href={href("catalogo", r.key)}>
                          {r.name}
                        </a>
                      ) : (
                        r.name
                      ),
                    sort: (r) => r.name,
                  },
                  { id: "views", header: "Vistas", num: true, cell: (r) => num(r.views), sort: (r) => r.views },
                  { id: "visitors", header: "Sesiones que lo vieron", num: true, wide: true, cell: (r) => num(r.visitors), sort: (r) => r.visitors },
                  { id: "cart", header: "Lo agregaron", num: true, cell: (r) => num(r.cartVisitors), sort: (r) => r.cartVisitors },
                  { id: "rate", header: "Vista → carrito", num: true, cell: (r) => ratio(r.cartVisitors, r.visitors), sort: (r) => (r.visitors ? r.cartVisitors / r.visitors : -1) },
                ]}
                empty="Nadie abrió una página de producto en este periodo."
              />
            </>
          );
        }}
      </Body>
    </>
  );
}

// ---------- Promociones ----------

const KIND: Record<string, string> = { percent_over_threshold: "Porcentaje desde un mínimo", buy_x_pay_y: "Lleva X, paga Y" };

function Promociones() {
  const [params, setParams] = useReportParams();
  const invalid = Boolean(customError(params));
  const q = useQuery({ queryKey: ["reports", "promotions", params], queryFn: () => reportsApi.promotions(params), enabled: !invalid, placeholderData: keepPreviousData });
  const d = q.data;
  return (
    <>
      <Filters params={params} onChange={setParams} channels={d?.channels} info={d?.range}>
        <div className="ml-auto">
          <ExportButton path={`/v1/reports/promotions?${query(params, { format: "csv" })}`} name="apexx-promociones.csv" disabled={invalid} />
        </div>
      </Filters>
      <Body q={q} invalid={invalid}>
        {(d) => {
          const { labels, long } = seriesLabels(d.series, d.grain);
          const t = d.totals;
          const cols: Column<PromoRow>[] = [
            {
              id: "name",
              header: "Promoción",
              sort: (r) => r.name,
              cell: (r) => (
                <div className="grid gap-0.5">
                  <span className="font-medium">{r.name}</span>
                  <span className="text-xs text-muted-foreground">
                    <code className="font-mono">{r.id}</code>
                    {KIND[r.kind] ? ` · ${KIND[r.kind]}` : ""}
                  </span>
                </div>
              ),
              footer: "Total",
            },
            {
              id: "state",
              header: "Estado",
              wide: true,
              cell: (r) => (!r.exists ? <Badge variant="secondary">Ya no existe</Badge> : r.active ? <Badge variant="ok">Activa</Badge> : <Badge variant="warn">Pausada</Badge>),
              sort: (r) => (r.active ? 0 : 1),
            },
            { id: "orders", header: "Pedidos", num: true, cell: (r) => num(r.orders), sort: (r) => r.orders, footer: num(t.orders) },
            { id: "share", header: "% de pedidos", num: true, wide: true, cell: (r) => ratio(r.orders, d.sales.orders), sort: (r) => r.orders },
            { id: "discount", header: `Descuento (${d.currency})`, num: true, cell: (r) => <strong>{money(r.discount)}</strong>, sort: (r) => r.discount, footer: money(t.discount) },
            { id: "avg", header: "Desc. promedio", num: true, wide: true, cell: (r) => (r.orders ? money(Math.round(r.discount / r.orders)) : "—"), sort: (r) => (r.orders ? r.discount / r.orders : 0) },
            { id: "revenue", header: "Ventas de esos pedidos", num: true, cell: (r) => money(r.revenue), sort: (r) => r.revenue, footer: money(t.revenue) },
            { id: "roi", header: "Venta por $1 de descuento", num: true, wide: true, cell: (r) => (r.discount ? "$" + (r.revenue / r.discount).toFixed(2) : "—"), sort: (r) => (r.discount ? r.revenue / r.discount : 0) },
          ];
          return d.rows.length === 0 ? (
            <Empty icon={<Megaphone />} title="No hay promociones todavía">
              Cuando crees una promoción y alguien la use, aquí verás cuánto descuento dio y cuánta venta la acompañó.
            </Empty>
          ) : (
            <>
              <section aria-label="Indicadores de promociones" className="grid grid-cols-2 gap-3 lg:grid-cols-4">
                <Kpi label={`Descuento otorgado (${d.currency})`} value={compactMoney(t.discount)} hint={`${ratio(t.discount, d.sales.gross)} de las ventas brutas`} />
                <Kpi label="Pedidos con promoción" value={num(t.orders)} hint={`${ratio(t.orders, d.sales.orders)} de ${num(d.sales.orders)} pedidos`} />
                <Kpi label="Ventas con promoción" value={compactMoney(t.revenue)} hint="Total de los pedidos que usaron alguna" />
                <Kpi label="Descuento promedio" value={t.orders ? money(Math.round(t.discount / t.orders)) : "—"} hint="Por pedido con promoción" />
              </section>
              {t.orders > 0 && (
                <ChartCard
                  title={`Descuento otorgado por ${(grainName[d.grain] ?? "día").toLowerCase()} (${d.currency})`}
                  table={{ head: [grainName[d.grain] ?? "Periodo", "Descuento", "Pedidos"], rows: d.series.map((p, i) => [long[i] ?? "", money(p.discount), num(p.orders)]) }}
                >
                  <Chart
                    labels={labels}
                    longLabels={long}
                    series={[{ name: "Descuento", values: d.series.map((p) => p.discount), color: SERIES_COLORS[1], kind: "bar" }]}
                    format={money}
                    axisFormat={compactMoney}
                    height={240}
                    summary={`Descuento por periodo: ${money(t.discount)} ${d.currency} en ${t.orders} pedidos.`}
                    extra={(i) => [{ label: "Pedidos con promoción", value: num(d.series[i]?.orders ?? 0) }]}
                  />
                </ChartCard>
              )}
              <DataTable caption="Impacto por promoción" rows={d.rows} columns={cols} rowKey={(r) => r.id} search={(r) => `${r.name} ${r.id}`} searchLabel="Buscar promoción" initialSort={{ id: "discount", desc: true }} />
            </>
          );
        }}
      </Body>
    </>
  );
}

// ---------- Inventario ----------

const STATE: Record<string, { label: string; tone: "ok" | "warn" | "bad" }> = {
  ok: { label: "Con existencias", tone: "ok" },
  low: { label: "Pocas", tone: "warn" },
  out: { label: "Agotado", tone: "bad" },
};

function Inventario({ me }: { me: Me }) {
  const [state, setState] = useState("all");
  const [channel, setChannel] = useState("");
  const [thresholdText, setThresholdText] = useState("5");
  const threshold = /^\d{1,6}$/.test(thresholdText.trim()) ? Number(thresholdText) : null;
  const st = state === "all" ? "" : state;
  const q = useQuery({
    queryKey: ["reports", "inventory", channel, st, threshold],
    queryFn: () => reportsApi.inventory(channel, st, threshold ?? 5),
    enabled: threshold !== null,
    placeholderData: keepPreviousData,
  });
  const d = q.data;
  const fake: ReportParams = { range: "30d", channel };
  const csv = new URLSearchParams({ channel, state: st, threshold: String(threshold ?? 5), format: "csv" }).toString();
  return (
    <>
      <div className="grid gap-2">
        <div className="flex flex-wrap items-end gap-2">
          <Select value={state} onValueChange={setState}>
            <SelectTrigger className="w-[12.5rem]" aria-label="Estado de existencias">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">Todos los SKU</SelectItem>
              <SelectItem value="low">Pocas existencias</SelectItem>
              <SelectItem value="out">Agotados</SelectItem>
              <SelectItem value="ok">Con existencias</SelectItem>
            </SelectContent>
          </Select>
          <div className="flex items-center gap-2">
            <label htmlFor="inv-threshold" className="text-sm text-muted-foreground">
              «Pocas» es hasta
            </label>
            <Input id="inv-threshold" inputMode="numeric" className="w-20" value={thresholdText} onChange={(e) => setThresholdText(e.target.value)} aria-invalid={threshold === null} aria-describedby="inv-threshold-help" />
            <span className="text-sm text-muted-foreground">unidades</span>
          </div>
          {d && d.channels.length > 1 && (
            <Filters hideRange params={{ ...fake, channel: channel || d.channel }} onChange={(p) => setChannel(p.channel ?? "")} channels={d.channels} />
          )}
          <div className="ml-auto">
            <ExportButton path={`/v1/reports/inventory?${csv}`} name="apexx-inventario.csv" disabled={threshold === null} />
          </div>
        </div>
        <p id="inv-threshold-help" className={cn("text-xs", threshold === null ? "text-bad" : "text-muted-foreground")} role={threshold === null ? "alert" : undefined}>
          {threshold === null ? "Escribe un número entero de unidades, por ejemplo 5." : `Existencias de este momento${d ? ` · valor a precio de venta del canal ${d.channel}` : ""}`}
        </p>
      </div>
      <Body q={q} invalid={threshold === null}>
        {(d) => {
          const cols: Column<StockRow>[] = [
            {
              id: "title",
              header: "Producto",
              sort: (r) => r.title,
              cell: (r) => (
                <div className="grid gap-0.5">
                  {r.productId && can(me, "catalog", "read") ? (
                    <a className="font-medium text-primary hover:underline" href={href("catalogo", r.productId)}>
                      {r.title || r.sku}
                    </a>
                  ) : (
                    <span className="font-medium">{r.title || r.sku}</span>
                  )}
                  <code className="font-mono text-xs text-muted-foreground">{r.sku}</code>
                </div>
              ),
            },
            { id: "state", header: "Estado", cell: (r) => <Badge variant={STATE[r.state]?.tone ?? "secondary"}>{STATE[r.state]?.label ?? r.state}</Badge>, sort: (r) => r.available },
            { id: "available", header: "Disponibles", num: true, cell: (r) => <strong>{num(r.available)}</strong>, sort: (r) => r.available },
            { id: "reserved", header: "Reservadas", num: true, wide: true, cell: (r) => num(r.reserved), sort: (r) => r.reserved },
            { id: "onHand", header: "En almacén", num: true, wide: true, cell: (r) => num(r.onHand), sort: (r) => r.onHand },
            { id: "sold30", header: "Vendidas (30 d)", num: true, cell: (r) => num(r.sold30), sort: (r) => r.sold30 },
            { id: "cover", header: "Alcanza para", num: true, wide: true, cell: (r) => (r.coverDays < 0 ? "—" : r.coverDays === 0 ? "menos de 1 día" : `${num(r.coverDays)} días`), sort: (r) => (r.coverDays < 0 ? 1e9 : r.coverDays) },
            { id: "price", header: `Precio (${d.currency})`, num: true, wide: true, cell: (r) => (r.price ? money(r.price) : "Sin precio"), sort: (r) => r.price },
            { id: "value", header: "Valor", num: true, cell: (r) => money(r.value), sort: (r) => r.value },
          ];
          return d.skus === 0 ? (
            <Empty icon={<Boxes />} title="Aún no registras existencias">
              Registra las existencias de tus productos en Catálogo para ver el valor de tu inventario y lo que está por agotarse.
            </Empty>
          ) : (
            <>
              <section aria-label="Indicadores de inventario" className="grid grid-cols-2 gap-3 lg:grid-cols-4">
                <Kpi label={`Valor del inventario (${d.currency})`} value={compactMoney(d.value)} hint={d.unpriced ? `${num(d.unpriced)} SKU sin precio no suman` : "A precio de venta"} />
                <Kpi label="Unidades en almacén" value={num(d.units)} hint={`${num(d.skus)} SKU · ${num(d.reserved)} reservadas`} />
                <Kpi label="Con pocas existencias" value={num(d.lowStock)} hint={`${d.threshold} unidades o menos disponibles`} />
                <Kpi label="Agotados" value={num(d.outOfStock)} hint="No se pueden vender ahora" />
              </section>
              <Card>
                <CardHeader>
                  <CardTitle>Estado de los SKU</CardTitle>
                </CardHeader>
                <CardContent>
                  <Split
                    format={num}
                    parts={[
                      { name: "Con existencias", value: d.skus - d.lowStock - d.outOfStock, color: SERIES_COLORS[2] },
                      { name: "Pocas existencias", value: d.lowStock, color: SERIES_COLORS[3] },
                      { name: "Agotados", value: d.outOfStock, color: SERIES_COLORS[1] },
                    ]}
                  />
                </CardContent>
              </Card>
              {d.truncated && <p className="text-sm text-muted-foreground">Se muestran los primeros {num(d.rows.length)} SKU con menos existencias. Exporta el CSV para verlos todos.</p>}
              <DataTable
                caption="Existencias por SKU"
                rows={d.rows}
                columns={cols}
                rowKey={(r) => r.sku}
                search={(r) => `${r.title} ${r.sku}`}
                searchLabel="Buscar producto o SKU"
                initialSort={{ id: "available", desc: false }}
                empty="Ningún SKU está en ese estado."
              />
            </>
          );
        }}
      </Body>
    </>
  );
}

/** Reports hosts the six reports under #reportes/<id>. */
export function Reports({ me, report }: { me: Me; report?: string }) {
  const active = REPORTS.find((r) => r.id === report)?.id ?? "ventas";
  return (
    <Page title="Reportes" sub="Ventas, productos, clientes y existencias de tu tienda. Las cifras se actualizan solas en segundos.">
      <SubNav base="reportes" items={REPORTS} active={active} label="Reportes" />
      {active === "ventas" && <Ventas />}
      {active === "productos" && <Productos me={me} />}
      {active === "clientes" && <Clientes me={me} />}
      {active === "embudo" && <Embudo me={me} />}
      {active === "promociones" && <Promociones />}
      {active === "inventario" && <Inventario me={me} />}
      {active === "personalizados" && <Custom me={me} />}
    </Page>
  );
}
