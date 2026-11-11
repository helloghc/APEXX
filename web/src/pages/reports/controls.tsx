import { useMemo, useState, type ReactNode } from "react";
import { ArrowDown, ArrowDownRight, ArrowUp, ArrowUpRight, BarChart3, ChevronLeft, ChevronRight, ChevronsUpDown, Download, Minus, Search, Table2 } from "lucide-react";
import { errorMessage } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { toast } from "@/components/ui/sonner";
import { cn } from "@/lib/utils";
import { download, type RangeInfo, type ReportParams } from "./api";
import { Legend, Skeleton, Sparkline } from "./charts";
import { delta, rangeLabel } from "./format";

export const PRESETS: { id: string; label: string }[] = [
  { id: "today", label: "Hoy" },
  { id: "yesterday", label: "Ayer" },
  { id: "7d", label: "Últimos 7 días" },
  { id: "30d", label: "Últimos 30 días" },
  { id: "90d", label: "Últimos 90 días" },
  { id: "mtd", label: "Este mes" },
  { id: "lastmonth", label: "Mes pasado" },
  { id: "12m", label: "Últimos 12 meses" },
  { id: "ytd", label: "Este año" },
  { id: "custom", label: "Fechas a elegir" },
];

const STORE = "apexx.reports.params";

function today(): string {
  // The store day (Mexico City) regardless of where the browser is.
  return new Intl.DateTimeFormat("en-CA", { timeZone: "America/Mexico_City" }).format(new Date());
}

function shift(day: string, days: number): string {
  const d = new Date(day + "T12:00:00Z");
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}

function load(fallback: string): ReportParams {
  try {
    const raw = window.sessionStorage.getItem(STORE);
    if (raw) {
      const p = JSON.parse(raw) as ReportParams;
      if (PRESETS.some((x) => x.id === p.range)) return p;
    }
  } catch {
    /* storage blocked or corrupt: use the default */
  }
  return { range: fallback };
}

/** useReportParams keeps the chosen period and channel while the person moves between reports. */
export function useReportParams(fallback = "30d"): [ReportParams, (p: ReportParams) => void] {
  const [p, setP] = useState<ReportParams>(() => load(fallback));
  return [
    p,
    (next) => {
      setP(next);
      try {
        window.sessionStorage.setItem(STORE, JSON.stringify(next));
      } catch {
        /* storage blocked */
      }
    },
  ];
}

/** customError explains why a custom range cannot be queried yet, or null when it is valid. */
export function customError(p: ReportParams): string | null {
  if (p.range !== "custom") return null;
  if (!p.from || !p.to) return "Elige la fecha inicial y la final.";
  if (p.to < p.from) return "La fecha final es anterior a la inicial.";
  if ((new Date(p.to).getTime() - new Date(p.from).getTime()) / 86_400_000 > 730) return "El periodo máximo es de dos años.";
  return null;
}

export function Filters({
  params,
  onChange,
  channels,
  info,
  children,
  hideRange,
}: {
  params: ReportParams;
  onChange: (p: ReportParams) => void;
  channels?: string[];
  info?: RangeInfo;
  children?: ReactNode;
  hideRange?: boolean;
}) {
  const err = customError(params);
  return (
    <div className="grid gap-2">
      <div className="flex flex-wrap items-end gap-2">
        {!hideRange && (
          <div className="grid gap-1">
            <label className="sr-only" htmlFor="rp-range">
              Periodo
            </label>
            <Select
              value={params.range}
              onValueChange={(range) => {
                if (range === "custom") {
                  const to = params.to ?? info?.to ?? today();
                  onChange({ ...params, range, from: params.from ?? info?.from ?? shift(to, -29), to });
                } else onChange({ ...params, range });
              }}
            >
              <SelectTrigger id="rp-range" className="w-[12.5rem]" aria-label="Periodo">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PRESETS.map((p) => (
                  <SelectItem key={p.id} value={p.id}>
                    {p.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}
        {!hideRange && params.range === "custom" && (
          <div className="flex items-center gap-2">
            <Input type="date" aria-label="Desde" className="w-[9.5rem]" value={params.from ?? ""} max={params.to || today()} onChange={(e) => onChange({ ...params, from: e.target.value })} />
            <span className="text-muted-foreground" aria-hidden="true">
              –
            </span>
            <Input type="date" aria-label="Hasta" className="w-[9.5rem]" value={params.to ?? ""} min={params.from} max={today()} onChange={(e) => onChange({ ...params, to: e.target.value })} />
          </div>
        )}
        {channels && channels.length > 1 && (
          <Select value={params.channel || "__all"} onValueChange={(v) => onChange({ ...params, channel: v === "__all" ? "" : v })}>
            <SelectTrigger className="w-[11rem]" aria-label="Canal de venta">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__all">Todos los canales</SelectItem>
              {channels.map((c) => (
                <SelectItem key={c} value={c}>
                  Canal {c}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        {children}
      </div>
      {err ? (
        <p className="text-xs text-bad" role="alert">
          {err}
        </p>
      ) : (
        info &&
        !hideRange && (
          <p className="text-xs text-muted-foreground">
            {rangeLabel(info)} · {info.days === 1 ? "1 día" : `${info.days} días`} · hora del centro de México
          </p>
        )
      )}
    </div>
  );
}

export function ExportButton({ path, name, label = "Exportar CSV", disabled }: { path: string; name: string; label?: string; disabled?: boolean }) {
  const [busy, setBusy] = useState(false);
  return (
    <Button
      variant="outline"
      disabled={busy || disabled}
      onClick={() => {
        setBusy(true);
        download(path, name)
          .then(() => toast.success("Archivo CSV descargado"))
          .catch((e: unknown) => toast.error(`No se pudo exportar: ${errorMessage(e)}`))
          .finally(() => setBusy(false));
      }}
    >
      <Download /> {busy ? "Exportando…" : label}
    </Button>
  );
}

export function DeltaBadge({ current, previous, invert, className }: { current: number; previous: number; invert?: boolean; className?: string }) {
  const d = delta(current, previous);
  const good = d.dir === "flat" ? null : (d.dir === "up") !== Boolean(invert);
  const Icon = d.dir === "up" ? ArrowUpRight : d.dir === "down" ? ArrowDownRight : Minus;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-0.5 rounded-full px-1.5 py-0.5 text-xs font-semibold tabular-nums",
        good === null ? "bg-muted text-muted-foreground" : good ? "bg-ok-soft text-ok" : "bg-bad-soft text-bad",
        className,
      )}
    >
      <Icon className="size-3.5" aria-hidden="true" />
      {d.label}
      <span className="sr-only"> respecto al periodo anterior</span>
    </span>
  );
}

export function Kpi({
  label,
  value,
  current,
  previous,
  previousText,
  spark,
  invert,
  hint,
  color,
}: {
  label: string;
  value: string;
  current?: number;
  previous?: number;
  previousText?: string;
  spark?: number[];
  invert?: boolean;
  hint?: string;
  color?: string;
}) {
  return (
    <Card className="min-w-0">
      <CardContent className="grid gap-2 p-4">
        <div className="flex items-start justify-between gap-2">
          <span className="text-sm text-muted-foreground">{label}</span>
          {current !== undefined && previous !== undefined && <DeltaBadge current={current} previous={previous} invert={invert} />}
        </div>
        <div className="flex items-end justify-between gap-3">
          <span className="font-display truncate text-[1.65rem] font-bold leading-none tracking-tight tabular-nums" data-testid={`kpi-${label}`}>
            {value}
          </span>
          {spark && spark.length > 1 && <Sparkline values={spark} color={color} />}
        </div>
        <span className="min-h-4 text-xs text-muted-foreground">{previousText ?? hint ?? ""}</span>
      </CardContent>
    </Card>
  );
}

export function KpiSkeleton({ n = 4 }: { n?: number }) {
  return (
    <div className="grid grid-cols-2 gap-3 lg:grid-cols-4" aria-hidden="true">
      {Array.from({ length: n }, (_, i) => (
        <Card key={i}>
          <CardContent className="grid gap-3 p-4">
            <Skeleton className="h-4 w-24" />
            <Skeleton className="h-7 w-32" />
            <Skeleton className="h-3 w-20" />
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

export function CardSkeleton({ height = 260 }: { height?: number }) {
  return (
    <Card aria-hidden="true">
      <CardContent className="grid gap-3 p-5">
        <Skeleton className="h-4 w-40" />
        <Skeleton className="w-full" />
        <div style={{ height }} className="viz-skeleton" />
      </CardContent>
    </Card>
  );
}

export interface TableAlt {
  head: string[];
  rows: (string | number)[][];
}

/** ChartCard frames a chart with its title, legend and a switch to read the same data as a table. */
export function ChartCard({
  title,
  description,
  legend,
  table,
  children,
  className,
  actions,
}: {
  title: string;
  description?: ReactNode;
  legend?: { name: string; color: string; line?: boolean }[];
  table?: TableAlt;
  children: ReactNode;
  className?: string;
  actions?: ReactNode;
}) {
  const [asTable, setAsTable] = useState(false);
  return (
    <Card className={cn("min-w-0", className)}>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-2">
        <div className="grid min-w-0 gap-1">
          <CardTitle>{title}</CardTitle>
          {description && <CardDescription>{description}</CardDescription>}
        </div>
        <div className="flex items-center gap-2">
          {actions}
          {table && (
            <Button variant="ghost" size="sm" aria-pressed={asTable} onClick={() => setAsTable(!asTable)} className="text-muted-foreground">
              {asTable ? <BarChart3 /> : <Table2 />}
              {asTable ? "Ver gráfica" : "Ver tabla"}
            </Button>
          )}
        </div>
      </CardHeader>
      <CardContent className="grid gap-3">
        {asTable && table ? (
          <div className="max-h-80 overflow-auto rounded-lg border">
            <table className="w-full text-sm">
              <thead className="sticky top-0 bg-muted text-xs text-muted-foreground">
                <tr>
                  {table.head.map((h, i) => (
                    <th key={h} scope="col" className={cn("px-3 py-2 font-semibold", i === 0 ? "text-left" : "text-right")}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {table.rows.map((r, ri) => (
                  <tr key={ri} className="border-t">
                    {r.map((c, i) => (
                      <td key={i} className={cn("px-3 py-1.5 tabular-nums", i === 0 ? "text-left" : "text-right")}>
                        {c}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <>
            {legend && legend.length > 1 && <Legend items={legend} />}
            {children}
          </>
        )}
      </CardContent>
    </Card>
  );
}

export function Empty({ icon, title, children, action }: { icon?: ReactNode; title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="grid justify-items-center gap-2 rounded-xl border border-dashed bg-card px-6 py-10 text-center">
      {icon && <div className="grid size-11 place-items-center rounded-full bg-accent text-accent-foreground [&_svg]:size-5">{icon}</div>}
      <h3 className="text-base font-semibold">{title}</h3>
      {children && <div className="max-w-md text-sm text-muted-foreground">{children}</div>}
      {action && <div className="mt-2 flex flex-wrap justify-center gap-2">{action}</div>}
    </div>
  );
}

export interface Column<T> {
  id: string;
  header: string;
  /** Right-align numbers. */
  num?: boolean;
  cell: (row: T) => ReactNode;
  /** Enables sorting by this column. */
  sort?: (row: T) => number | string;
  /** Hide on narrow screens. */
  wide?: boolean;
  footer?: ReactNode;
}

/** DataTable: client-side search, sorting and pagination over a report's rows. */
export function DataTable<T>({
  rows,
  columns,
  rowKey,
  initialSort,
  search,
  searchLabel = "Buscar",
  pageSize = 25,
  empty,
  caption,
  toolbar,
}: {
  rows: T[];
  columns: Column<T>[];
  rowKey: (row: T) => string;
  initialSort?: { id: string; desc: boolean };
  search?: (row: T) => string;
  searchLabel?: string;
  pageSize?: number;
  empty?: ReactNode;
  caption: string;
  toolbar?: ReactNode;
}) {
  const [sort, setSort] = useState(initialSort ?? null);
  const [q, setQ] = useState("");
  const [page, setPage] = useState(0);
  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    let out = needle && search ? rows.filter((r) => search(r).toLowerCase().includes(needle)) : rows.slice();
    const col = sort && columns.find((c) => c.id === sort.id);
    if (col?.sort) {
      const get = col.sort;
      out = out.sort((a, b) => {
        const x = get(a);
        const y = get(b);
        const c = typeof x === "number" && typeof y === "number" ? x - y : String(x).localeCompare(String(y), "es");
        return sort?.desc ? -c : c;
      });
    }
    return out;
  }, [rows, q, sort, columns, search]);
  const pages = Math.max(1, Math.ceil(filtered.length / pageSize));
  const cur = Math.min(page, pages - 1);
  const slice = filtered.slice(cur * pageSize, (cur + 1) * pageSize);
  const hasFooter = columns.some((c) => c.footer !== undefined);

  return (
    <div className="grid gap-3">
      {(search || toolbar) && (
        <div className="flex flex-wrap items-center gap-2">
          {search && (
            <div className="relative w-full max-w-xs">
              <Search className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden="true" />
              <Input
                type="search"
                className="pl-8"
                placeholder={searchLabel}
                aria-label={searchLabel}
                value={q}
                onChange={(e) => {
                  setQ(e.target.value);
                  setPage(0);
                }}
              />
            </div>
          )}
          {toolbar}
          <span className="ml-auto text-xs text-muted-foreground" aria-live="polite">
            {filtered.length === rows.length ? `${rows.length} filas` : `${filtered.length} de ${rows.length} filas`}
          </span>
        </div>
      )}
      <div className="relative w-full overflow-x-auto rounded-xl border bg-card">
        <table className="w-full text-sm">
          <caption className="sr-only">{caption}</caption>
          <thead className="bg-muted text-xs text-muted-foreground">
            <tr className="border-b">
              {columns.map((c) => {
                const active = sort?.id === c.id;
                return (
                  <th
                    key={c.id}
                    scope="col"
                    aria-sort={active ? (sort?.desc ? "descending" : "ascending") : undefined}
                    className={cn("h-10 whitespace-nowrap px-3 font-semibold", c.num ? "text-right" : "text-left", c.wide && "hidden md:table-cell")}
                  >
                    {c.sort ? (
                      <button
                        type="button"
                        className={cn("inline-flex cursor-pointer items-center gap-1 rounded hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring", active && "text-foreground")}
                        onClick={() => {
                          setSort(active ? { id: c.id, desc: !sort?.desc } : { id: c.id, desc: Boolean(c.num) });
                          setPage(0);
                        }}
                      >
                        {c.header}
                        {active ? sort?.desc ? <ArrowDown className="size-3.5" /> : <ArrowUp className="size-3.5" /> : <ChevronsUpDown className="size-3.5 opacity-50" />}
                      </button>
                    ) : (
                      c.header
                    )}
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody>
            {slice.map((r) => (
              <tr key={rowKey(r)} className="border-b transition-colors last:border-0 hover:bg-muted/50">
                {columns.map((c) => (
                  <td key={c.id} className={cn("px-3 py-2.5 align-middle", c.num && "text-right tabular-nums", c.wide && "hidden md:table-cell")}>
                    {c.cell(r)}
                  </td>
                ))}
              </tr>
            ))}
            {slice.length === 0 && (
              <tr>
                <td colSpan={columns.length} className="px-3 py-10 text-center text-sm text-muted-foreground">
                  {rows.length === 0 ? (empty ?? "Sin datos en este periodo.") : "Ninguna fila coincide con la búsqueda."}
                </td>
              </tr>
            )}
          </tbody>
          {hasFooter && slice.length > 0 && (
            <tfoot className="border-t bg-muted/60 font-semibold">
              <tr>
                {columns.map((c) => (
                  <td key={c.id} className={cn("px-3 py-2.5", c.num && "text-right tabular-nums", c.wide && "hidden md:table-cell")}>
                    {c.footer}
                  </td>
                ))}
              </tr>
            </tfoot>
          )}
        </table>
      </div>
      {pages > 1 && (
        <nav className="flex items-center justify-end gap-2 text-sm" aria-label="Paginación">
          <span className="text-muted-foreground">
            Página {cur + 1} de {pages}
          </span>
          <Button variant="outline" size="sm" disabled={cur === 0} onClick={() => setPage(cur - 1)}>
            <ChevronLeft /> Anterior
          </Button>
          <Button variant="outline" size="sm" disabled={cur >= pages - 1} onClick={() => setPage(cur + 1)}>
            Siguiente <ChevronRight />
          </Button>
        </nav>
      )}
    </div>
  );
}
