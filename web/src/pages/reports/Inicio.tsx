import { keepPreviousData, useQuery } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowRight,
  BarChart3,
  Boxes,
  Cable,
  CircleAlert,
  CreditCard,
  LayoutTemplate,
  PackagePlus,
  Receipt,
  ShoppingBag,
  UserPlus,
} from "lucide-react";
import type { ReactNode } from "react";
import { request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { can } from "@/perms";
import { href } from "@/router";
import { Copilot } from "@/components/Copilot";
import { ErrorBox, Page, StatusPill } from "../common";
import { reportsApi, type Alert, type Dashboard } from "./api";
import { BarList, Chart, Funnel, PREV_COLOR, SERIES_COLORS, Split, useNow } from "./charts";
import { CardSkeleton, ChartCard, customError, DeltaBadge, Empty, Filters, Kpi, KpiSkeleton, useReportParams } from "./controls";
import { ago, bucketLabel, compactMoney, money, num, rangeLabel, ratio } from "./format";

const ALERT_LINK: Record<string, { route: string[]; label: string; module: string; level: "read" | "admin" }> = {
  payments_pending: { route: ["pedidos"], label: "Ver pedidos", module: "orders", level: "read" },
  invoices_pending: { route: ["pedidos"], label: "Ver pedidos", module: "orders", level: "read" },
  webhooks_failed: { route: ["conectores"], label: "Ir a conectores", module: "settings", level: "read" },
  low_stock: { route: ["reportes", "inventario"], label: "Ver inventario", module: "reports", level: "read" },
  out_of_stock: { route: ["reportes", "inventario"], label: "Ver inventario", module: "reports", level: "read" },
  projection_lag: { route: ["operacion", "estado"], label: "Estado del sistema", module: "settings", level: "admin" },
};

function Alerts({ alerts, me }: { alerts: Alert[]; me: Me }) {
  if (alerts.length === 0) return null;
  return (
    <section aria-label="Requiere tu atención" className="grid gap-2">
      <ul className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
        {alerts.map((a) => {
          const link = ALERT_LINK[a.id];
          const Icon = a.severity === "bad" ? CircleAlert : AlertTriangle;
          return (
            <li key={a.id} className={cn("flex items-start gap-3 rounded-xl border bg-card p-3", a.severity === "bad" ? "border-bad/40" : "border-warn/40")}>
              <span className={cn("mt-0.5 grid size-8 shrink-0 place-items-center rounded-full", a.severity === "bad" ? "bg-bad-soft text-bad" : "bg-warn-soft text-warn")}>
                <Icon className="size-4" aria-hidden="true" />
              </span>
              <div className="grid min-w-0 gap-0.5">
                <span className="text-sm font-semibold">{a.title}</span>
                <span className="text-xs text-muted-foreground">{a.detail}</span>
                {link && can(me, link.module, link.level) && (
                  <a className="mt-1 inline-flex items-center gap-1 text-xs font-semibold text-primary hover:underline" href={href(...link.route)}>
                    {link.label} <ArrowRight className="size-3.5" aria-hidden="true" />
                  </a>
                )}
              </div>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

type Setup = { products: boolean; payments: boolean; shipping: boolean; published: boolean; team: boolean; domain: boolean; firstSale: boolean };

// SetupGuide walks a new store from empty to its first sale. Each step shows
// whether it is done (read from the store, not ticked by hand) and links to
// the screen that does it; it disappears once the essential steps are done.
function SetupGuide({ me }: { me: Me }) {
  const q = useQuery({ queryKey: ["setup"], queryFn: () => request<Setup>("GET", "/v1/setup"), staleTime: 30_000 });
  const s = q.data;
  if (!s) return null;
  const steps: { done: boolean; title: string; detail: string; route: string[]; cta: string; optional?: boolean; show: boolean }[] = [
    { done: s.products, title: "Agrega tu primer producto", detail: "Nombre, foto, precio y existencias. También puedes importar un CSV.", route: ["catalogo", "productos", "nuevo"], cta: "Agregar producto", show: can(me, "catalog", "edit") },
    { done: s.payments, title: "Conecta Stripe para cobrar", detail: "Los pagos llegan directo a tu cuenta. Sin este paso tus compradores no pueden pagar.", route: ["pagos"], cta: "Conectar pagos", show: can(me, "payments", "read") },
    { done: s.shipping, title: "Define cómo entregas", detail: "Zonas, tarifas, envío gratis o recoger en tienda.", route: ["configuracion", "envios"], cta: "Configurar envíos", show: can(me, "settings", "read") },
    { done: s.published, title: "Diseña y publica tu tienda", detail: "Elige un tema, ajusta colores y publica cuando te guste.", route: ["builder"], cta: "Abrir el diseñador", show: can(me, "builder", "read") },
    { done: s.domain, title: "Usa tu propio dominio", detail: "Por ejemplo www.tutienda.mx. Puedes hacerlo después.", route: ["configuracion", "dominios"], cta: "Conectar dominio", optional: true, show: can(me, "settings", "read") },
    { done: s.team, title: "Invita a tu equipo", detail: "Cada persona con su propio acceso y solo los permisos que necesita.", route: ["equipo"], cta: "Invitar", optional: true, show: can(me, "team", "admin") },
  ].filter((x) => x.show);
  const essential = steps.filter((x) => !x.optional);
  if (steps.length === 0 || (s.firstSale && essential.every((x) => x.done))) return null;
  const done = essential.filter((x) => x.done).length;
  const next = steps.find((x) => !x.done);
  return (
    <Card>
      <CardHeader>
        <CardTitle>Primeros pasos</CardTitle>
        <CardDescription>
          <span>{`${done} de ${essential.length} pasos esenciales listos.`}</span>{" "}
          <span>{s.firstSale ? "Ya tienes ventas: termina lo que falta cuando puedas." : "Al terminarlos tu tienda está lista para su primera venta."}</span>
        </CardDescription>
        <div className="mt-2 h-2 overflow-hidden rounded-full bg-muted" role="progressbar" aria-label="Avance de primeros pasos" aria-valuemin={0} aria-valuemax={essential.length} aria-valuenow={done}>
          <div className="h-full rounded-full bg-primary transition-all" style={{ width: `${essential.length ? (done / essential.length) * 100 : 0}%` }} />
        </div>
      </CardHeader>
      <CardContent>
        <ol className="grid gap-2">
          {steps.map((x) => (
            <li key={x.title} className={cn("flex flex-wrap items-center gap-3 rounded-lg border p-3", x === next && "border-primary/50 bg-accent/40")}>
              <span className={cn("grid size-6 shrink-0 place-items-center rounded-full border text-xs", x.done ? "border-primary bg-primary text-primary-foreground" : "text-muted-foreground")} aria-hidden="true">{x.done ? "✓" : ""}</span>
              <div className="min-w-0 flex-1">
                <p className={cn("text-sm font-medium", x.done && "text-muted-foreground line-through")}>
                  {x.title}
                  {x.optional && <span className="ml-2 text-xs font-normal text-muted-foreground">Opcional</span>}
                  <span className="sr-only">{x.done ? " (hecho)" : " (pendiente)"}</span>
                </p>
                {!x.done && <p className="text-xs text-muted-foreground">{x.detail}</p>}
              </div>
              {!x.done && (
                <Button size="sm" variant={x === next ? "default" : "outline"} asChild>
                  <a href={href(...x.route)}>{x.cta}</a>
                </Button>
              )}
            </li>
          ))}
        </ol>
      </CardContent>
    </Card>
  );
}

function QuickActions({ me }: { me: Me }) {
  const items: { label: string; route: string[]; icon: ReactNode; show: boolean }[] = [
    { label: "Agregar producto", route: ["catalogo"], icon: <PackagePlus />, show: can(me, "catalog", "edit") },
    { label: "Atender pedidos", route: ["pedidos"], icon: <Receipt />, show: can(me, "orders", "read") },
    { label: "Editar la tienda", route: ["builder"], icon: <LayoutTemplate />, show: can(me, "builder", "read") },
    { label: "Ver reportes", route: ["reportes"], icon: <BarChart3 />, show: can(me, "reports", "read") },
    { label: "Invitar a tu equipo", route: ["equipo"], icon: <UserPlus />, show: can(me, "team", "admin") },
    { label: "Cobros y Stripe", route: ["pagos"], icon: <CreditCard />, show: can(me, "payments", "read") },
    { label: "Conectores", route: ["conectores"], icon: <Cable />, show: can(me, "settings", "read") },
  ].filter((i) => i.show);
  return (
    <Card>
      <CardHeader>
        <CardTitle>Acciones rápidas</CardTitle>
      </CardHeader>
      <CardContent>
        {items.length === 0 ? (
          <p className="text-sm text-muted-foreground">Tu acceso todavía no incluye ninguna sección. Pide permisos a un administrador de la tienda.</p>
        ) : (
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4">
            {items.map((i) => (
              <Button key={i.label} variant="outline" className="h-auto justify-start gap-2.5 px-3 py-2.5 text-left [&_svg]:text-primary" asChild>
                <a href={href(...i.route)}>
                  {i.icon}
                  <span className="min-w-0 whitespace-normal leading-tight">{i.label}</span>
                </a>
              </Button>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function Board({ d, me }: { d: Dashboard; me: Me }) {
  const cur = d.currency;
  const labels = d.series.map((p) => bucketLabel(p.bucket, d.grain));
  const long = d.series.map((p) => bucketLabel(p.bucket, d.grain, true));
  const net = d.series.map((p) => p.net);
  const prevNet = d.series.map((_, i) => d.previousSeries[i]?.net ?? 0);
  const conv = (t: { sessions: number; purchase: number }) => (t.sessions ? (t.purchase / t.sessions) * 100 : 0);
  const nothing = d.totals.orders === 0 && d.traffic.sessions === 0 && d.previousTotals.orders === 0;
  const prevLabel = `Periodo anterior (${rangeLabel(d.previous)})`;

  return (
    <>
      <Alerts alerts={d.alerts} me={me} />

      <section aria-label="Indicadores" className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Kpi
          label={`Ventas netas (${cur})`}
          value={compactMoney(d.totals.net)}
          current={d.totals.net}
          previous={d.previousTotals.net}
          previousText={`Antes: ${compactMoney(d.previousTotals.net)}`}
          spark={net}
        />
        <Kpi
          label="Pedidos"
          value={num(d.totals.orders)}
          current={d.totals.orders}
          previous={d.previousTotals.orders}
          previousText={`Antes: ${num(d.previousTotals.orders)}`}
          spark={d.series.map((p) => p.orders)}
        />
        <Kpi
          label="Ticket promedio"
          value={money(d.totals.aov)}
          current={d.totals.aov}
          previous={d.previousTotals.aov}
          previousText={`Antes: ${money(d.previousTotals.aov)}`}
          spark={d.series.map((p) => p.aov)}
        />
        <Kpi
          label="Conversión"
          value={ratio(d.traffic.purchase, d.traffic.sessions, 2)}
          current={conv(d.traffic)}
          previous={conv(d.previousTraffic)}
          previousText={`${num(d.traffic.sessions)} sesiones · antes ${ratio(d.previousTraffic.purchase, d.previousTraffic.sessions, 2)}`}
          spark={d.grain === "day" ? d.series.map((p) => conv(p.traffic)) : d.series.map((p) => p.traffic.sessions)}
        />
      </section>

      {nothing ? (
        <Empty
          icon={<ShoppingBag />}
          title="Todavía no hay ventas ni visitas en este periodo"
          action={
            <>
              {can(me, "builder", "read") && (
                <Button asChild>
                  <a href={href("builder")}>Publicar tu tienda</a>
                </Button>
              )}
              {can(me, "catalog", "read") && (
                <Button variant="outline" asChild>
                  <a href={href("catalogo")}>Revisar el catálogo</a>
                </Button>
              )}
            </>
          }
        >
          En cuanto alguien visite tu tienda o compre, aquí verás ventas, pedidos y el recorrido de tus clientes. Prueba con otro periodo si ya vendiste antes.
        </Empty>
      ) : (
        <div className="grid gap-4 xl:grid-cols-3">
          <ChartCard
            className="xl:col-span-2"
            title={`Ventas netas (${cur})`}
            description={
              <span className="flex flex-wrap items-center gap-2">
                <span className="text-lg font-semibold text-foreground tabular-nums">{money(d.totals.net)}</span>
                <DeltaBadge current={d.totals.net} previous={d.previousTotals.net} />
                <span>frente a {money(d.previousTotals.net)}</span>
              </span>
            }
            legend={[
              { name: "Este periodo", color: SERIES_COLORS[0], line: true },
              { name: prevLabel, color: PREV_COLOR, line: true },
            ]}
            table={{
              head: [d.grain === "hour" ? "Hora" : "Día", "Ventas netas", "Pedidos", "Periodo anterior"],
              rows: d.series.map((p, i) => [long[i] ?? p.bucket, money(p.net), num(p.orders), money(prevNet[i] ?? 0)]),
            }}
          >
            <Chart
              labels={labels}
              longLabels={long}
              series={[
                { name: "Periodo anterior", values: prevNet, color: PREV_COLOR, kind: "line" },
                { name: "Este periodo", values: net, color: SERIES_COLORS[0], kind: "area" },
              ]}
              format={money}
              axisFormat={compactMoney}
              height={280}
              summary={`Ventas netas por ${d.grain === "hour" ? "hora" : "día"}: ${money(d.totals.net)} ${cur} en el periodo, ${money(d.previousTotals.net)} en el anterior.`}
              extra={(i) => [{ label: "Pedidos", value: num(d.series[i]?.orders ?? 0) }]}
            />
          </ChartCard>

          <ChartCard
            title="Recorrido de compra"
            description={`${num(d.traffic.sessions)} sesiones · ${ratio(d.traffic.purchase, d.traffic.sessions, 2)} terminó en compra`}
            actions={
              can(me, "reports", "read") && (
                <Button variant="ghost" size="sm" asChild className="text-muted-foreground">
                  <a href={href("reportes", "embudo")}>Detalle</a>
                </Button>
              )
            }
          >
            {d.traffic.sessions === 0 ? (
              <p className="text-sm text-muted-foreground">Aún no hay visitas registradas en este periodo. Las visitas se miden sin cookies y respetan a quien pide no ser rastreado.</p>
            ) : (
              <Funnel
                format={num}
                steps={[
                  { label: "Sesiones", value: d.traffic.sessions },
                  { label: "Vieron un producto", value: d.traffic.productViews },
                  { label: "Agregaron al carrito", value: d.traffic.addToCart },
                  { label: "Iniciaron el pago", value: d.traffic.checkout },
                  { label: "Compraron", value: d.traffic.purchase },
                ]}
              />
            )}
          </ChartCard>
        </div>
      )}

      <div className="grid gap-4 lg:grid-cols-2 xl:grid-cols-3">
        <Card className="min-w-0">
          <CardHeader className="flex-row items-start justify-between gap-2">
            <div className="grid gap-1">
              <CardTitle>Productos más vendidos</CardTitle>
              <CardDescription>Por ingresos ({cur})</CardDescription>
            </div>
            <Button variant="ghost" size="sm" asChild className="text-muted-foreground">
              <a href={href("reportes", "productos")}>Ver todos</a>
            </Button>
          </CardHeader>
          <CardContent>
            <BarList
              format={money}
              rows={d.topProducts
                .filter((p) => p.revenue > 0)
                .slice(0, 6)
                .map((p) => ({
                  key: p.key,
                  label: p.name,
                  value: p.revenue,
                  sub: `${num(p.units)} ${p.units === 1 ? "unidad" : "unidades"}${p.brand ? ` · ${p.brand}` : ""}`,
                  href: p.productId && can(me, "catalog", "read") ? href("catalogo", p.productId) : undefined,
                }))}
              empty={<p className="text-sm text-muted-foreground">Aún no se vendió ningún producto en este periodo.</p>}
            />
          </CardContent>
        </Card>

        <Card className="min-w-0">
          <CardHeader className="flex-row items-start justify-between gap-2">
            <div className="grid gap-1">
              <CardTitle>Pedidos recientes</CardTitle>
              <CardDescription>Los últimos que entraron</CardDescription>
            </div>
            {can(me, "orders", "read") && (
              <Button variant="ghost" size="sm" asChild className="text-muted-foreground">
                <a href={href("pedidos")}>Ver todos</a>
              </Button>
            )}
          </CardHeader>
          <CardContent>
            {d.recentOrders.length === 0 ? (
              <p className="text-sm text-muted-foreground">Todavía no hay pedidos. El primero aparecerá aquí en cuanto se pague.</p>
            ) : (
              <ul className="-my-1 divide-y">
                {d.recentOrders.slice(0, 7).map((o) => {
                  const row = (
                    <div className="flex items-center justify-between gap-3 py-2">
                      <div className="grid min-w-0 gap-0.5">
                        <span className="text-sm font-semibold tabular-nums">#{o.number}</span>
                        <span className="truncate text-xs text-muted-foreground">
                          {ago(o.createdAt)} · {o.units} {o.units === 1 ? "artículo" : "artículos"}
                        </span>
                      </div>
                      <div className="flex shrink-0 items-center gap-2.5">
                        <StatusPill status={o.status} />
                        <span className="w-24 text-right text-sm font-semibold tabular-nums">{money(o.total)}</span>
                      </div>
                    </div>
                  );
                  return (
                    <li key={o.id}>
                      {can(me, "orders", "read") ? (
                        <a href={href("pedidos", o.id)} className="-mx-2 block rounded-md px-2 hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                          {row}
                        </a>
                      ) : (
                        row
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card className="min-w-0 lg:col-span-2 xl:col-span-1">
          <CardHeader>
            <CardTitle>Resumen del periodo</CardTitle>
            <CardDescription>De dónde sale la venta neta ({cur})</CardDescription>
          </CardHeader>
          <CardContent className="grid gap-4">
            <dl className="grid gap-1.5 text-sm">
              {[
                ["Productos a precio de lista", money(d.totals.gross)],
                ["Descuentos", "−" + money(d.totals.discounts)],
                ["Envíos cobrados", "+" + money(d.totals.shipping)],
                [`Reembolsos (${num(d.totals.refundCount)})`, "−" + money(d.totals.refunds)],
              ].map(([k, v]) => (
                <div key={k} className="flex items-center justify-between gap-3">
                  <dt className="text-muted-foreground">{k}</dt>
                  <dd className="tabular-nums">{v}</dd>
                </div>
              ))}
              <div className="flex items-center justify-between gap-3 border-t pt-1.5 font-semibold">
                <dt>Ventas netas</dt>
                <dd className="tabular-nums">{money(d.totals.net)}</dd>
              </div>
              <div className="flex items-center justify-between gap-3 text-xs text-muted-foreground">
                <dt>IVA incluido</dt>
                <dd className="tabular-nums">{money(d.totals.tax)}</dd>
              </div>
            </dl>
            <div className="grid gap-2">
              <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Pedidos por tipo de cliente</span>
              <Split
                format={num}
                parts={[
                  { name: "Clientes nuevos", value: d.totals.newCustomers, color: SERIES_COLORS[0] },
                  { name: "Clientes que regresan", value: d.totals.returningCustomers, color: SERIES_COLORS[1] },
                ]}
              />
            </div>
          </CardContent>
        </Card>
      </div>
    </>
  );
}

function BoardSkeleton() {
  return (
    <div className="grid gap-5" role="status" aria-label="Cargando el panel">
      <KpiSkeleton />
      <div className="grid gap-4 xl:grid-cols-3">
        <div className="xl:col-span-2">
          <CardSkeleton height={280} />
        </div>
        <CardSkeleton height={280} />
      </div>
    </div>
  );
}

/** Inicio is the admin home: how the store is doing and what needs attention. */
export function Inicio({ me }: { me: Me }) {
  const allowed = can(me, "reports", "read");
  const [params, setParams] = useReportParams("7d");
  const now = useNow();
  const invalid = customError(params);
  const q = useQuery({
    queryKey: ["reports", "dashboard", params],
    queryFn: () => reportsApi.dashboard(params),
    enabled: allowed && !invalid,
    placeholderData: keepPreviousData,
    refetchInterval: 60_000,
  });
  const name = (me.name || me.email || "equipo").split(" ")[0];

  return (
    <Page
      title={`Hola, ${name}`}
      sub={
        allowed && q.data ? (
          <>
            Así va <strong className="font-semibold text-foreground">{me.tenant}</strong> · datos actualizados {ago(q.data.syncedAt, now)}
          </>
        ) : (
          `Tienda ${me.tenant}`
        )
      }
      actions={allowed ? <Filters params={params} onChange={setParams} channels={q.data?.channels} /> : undefined}
    >
      {allowed && <SetupGuide me={me} />}
      <Copilot />
      {allowed ? (
        <>
          {q.data && !invalid && (
            <p className="-mt-3 text-xs text-muted-foreground">
              {rangeLabel(q.data.range)} comparado con {rangeLabel(q.data.previous)} · hora del centro de México
              {q.data.currencies.length > 1 && ` · solo ${q.data.currency}; también hay ventas en ${q.data.currencies.slice(1).join(", ")}`}
            </p>
          )}
          <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
          {q.data ? (
            <div className={cn("grid gap-5 transition-opacity", q.isPlaceholderData && "opacity-60")} aria-busy={q.isFetching}>
              <Board d={q.data} me={me} />
            </div>
          ) : (
            q.isPending && !invalid && <BoardSkeleton />
          )}
        </>
      ) : (
        <Card>
          <CardContent className="flex items-start gap-3 p-5">
            <span className="grid size-10 shrink-0 place-items-center rounded-full bg-accent text-accent-foreground">
              <Boxes className="size-5" aria-hidden="true" />
            </span>
            <div className="grid gap-1 text-sm">
              <strong>Tu acceso no incluye las cifras de venta</strong>
              <span className="text-muted-foreground">Un administrador puede darte el permiso «Reportes y analítica» desde Equipo. Mientras tanto, estas son las secciones que sí puedes usar.</span>
            </div>
          </CardContent>
        </Card>
      )}
      <QuickActions me={me} />
    </Page>
  );
}
