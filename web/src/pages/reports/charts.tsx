import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { cn } from "@/lib/utils";
import { niceTicks } from "./format";
import "./charts.css";

/** Hand-built SVG charts: light, themable with tokens, keyboard reachable, each with a table alternative. */

export const SERIES_COLORS = ["var(--chart-1)", "var(--chart-2)", "var(--chart-3)", "var(--chart-4)"] as const;
export const PREV_COLOR = "var(--chart-prev)";

function useWidth<T extends HTMLElement>(): [React.RefObject<T | null>, number] {
  const ref = useRef<T | null>(null);
  const [w, setW] = useState(0);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    setW(el.clientWidth);
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver((entries) => {
      const cw = entries[0]?.contentRect.width;
      if (cw) setW(Math.round(cw));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, w];
}

export interface Series {
  name: string;
  values: number[];
  color: string;
  /** area = line with a soft wash; line = line only; bar = columns. */
  kind: "area" | "line" | "bar";
}

interface ChartProps {
  labels: string[];
  /** Longer labels for the tooltip; defaults to labels. */
  longLabels?: string[];
  series: Series[];
  /** Stack bar series on top of each other. */
  stacked?: boolean;
  format: (v: number) => string;
  axisFormat?: (v: number) => string;
  height?: number;
  /** One sentence describing the chart for screen readers. */
  summary: string;
  /** Extra tooltip rows computed per index. */
  extra?: (i: number) => { label: string; value: string }[];
}

/** Chart draws lines, areas and columns over a shared, single y axis with a hover/keyboard tooltip. */
export function Chart({ labels, longLabels, series, stacked, format, axisFormat, height = 260, summary, extra }: ChartProps) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const n = labels.length;
  const pad = { top: 12, right: 12, bottom: 26, left: 48 };
  const W = Math.max(width, 240);
  const plotW = W - pad.left - pad.right;
  const plotH = height - pad.top - pad.bottom;
  const bars = series.filter((s) => s.kind === "bar");
  const lines = series.filter((s) => s.kind !== "bar");

  const max = useMemo(() => {
    let m = 0;
    for (let i = 0; i < n; i++) {
      if (stacked) m = Math.max(m, bars.reduce((a, s) => a + Math.max(0, s.values[i] ?? 0), 0));
      else for (const s of bars) m = Math.max(m, s.values[i] ?? 0);
      for (const s of lines) m = Math.max(m, s.values[i] ?? 0);
    }
    return m;
  }, [series, n, stacked]); // eslint-disable-line react-hooks/exhaustive-deps
  const ticks = niceTicks(max, 4);
  const top = ticks[ticks.length - 1] ?? 1;
  const y = (v: number) => pad.top + plotH - (Math.max(0, v) / top) * plotH;
  const hasBars = bars.length > 0;
  const band = n > 0 ? plotW / n : plotW;
  // Bars sit in the middle of their band; line points share those centres so both kinds align.
  const x = (i: number) => (hasBars || n === 1 ? pad.left + band * (i + 0.5) : pad.left + (plotW * i) / (n - 1));
  const barW = Math.max(2, Math.min(24, band - 2));
  const fmtAxis = axisFormat ?? format;

  const every = Math.max(1, Math.ceil(n / Math.max(2, Math.floor(plotW / 64))));
  const tickIdx = labels.map((_, i) => i).filter((i) => i % every === 0 && (n - 1 - i >= every / 2 || i === n - 1));

  function onMove(clientX: number, target: SVGSVGElement) {
    if (n === 0) return;
    const r = target.getBoundingClientRect();
    const px = clientX - r.left;
    const i = hasBars || n === 1 ? Math.floor((px - pad.left) / band) : Math.round(((px - pad.left) / plotW) * (n - 1));
    setHover(Math.max(0, Math.min(n - 1, i)));
  }
  function onKey(e: KeyboardEvent) {
    if (n === 0) return;
    const cur = hover ?? n - 1;
    if (e.key === "ArrowLeft") setHover(Math.max(0, cur - 1));
    else if (e.key === "ArrowRight") setHover(Math.min(n - 1, hover === null ? n - 1 : cur + 1));
    else if (e.key === "Home") setHover(0);
    else if (e.key === "End") setHover(n - 1);
    else if (e.key === "Escape") setHover(null);
    else return;
    e.preventDefault();
  }

  const tipLeft = hover === null ? 0 : x(hover);
  const flip = tipLeft > W * 0.6;

  return (
    <div ref={ref} className="relative w-full" style={{ height }}>
      {width > 0 && (
        <svg
          width={W}
          height={height}
          role="img"
          aria-label={summary}
          tabIndex={0}
          className="block rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring"
          onMouseMove={(e) => onMove(e.clientX, e.currentTarget)}
          onMouseLeave={() => setHover(null)}
          onTouchStart={(e) => e.touches[0] && onMove(e.touches[0].clientX, e.currentTarget)}
          onTouchMove={(e) => e.touches[0] && onMove(e.touches[0].clientX, e.currentTarget)}
          onKeyDown={onKey}
          onBlur={() => setHover(null)}
        >
          {ticks.map((t) => (
            <g key={t}>
              <line x1={pad.left} x2={W - pad.right} y1={y(t)} y2={y(t)} stroke={t === 0 ? "var(--chart-axis)" : "var(--chart-grid)"} strokeWidth={1} />
              <text x={pad.left - 8} y={y(t)} dy="0.32em" textAnchor="end" fontSize={11} fill="var(--muted)" className="tabular-nums">
                {fmtAxis(t)}
              </text>
            </g>
          ))}
          {tickIdx.map((i) => (
            <text key={i} x={x(i)} y={height - 8} textAnchor={i === n - 1 && !hasBars ? "end" : i === 0 && !hasBars ? "start" : "middle"} fontSize={11} fill="var(--muted)">
              {labels[i]}
            </text>
          ))}
          {hover !== null && hasBars && (
            <rect x={pad.left + band * hover} y={pad.top} width={band} height={plotH} fill="var(--surface-2)" opacity={0.7} />
          )}
          {hasBars &&
            labels.map((_, i) => {
              let acc = 0;
              return bars.map((s, si) => {
                const v = Math.max(0, s.values[i] ?? 0);
                if (v <= 0) return null;
                const base = stacked ? acc : 0;
                acc += v;
                const y1 = y(base + v);
                const h = Math.max(1, y(base) - y1 - (stacked && base > 0 ? 2 : 0));
                const isTop = !stacked || si === bars.length - 1 || bars.slice(si + 1).every((o) => (o.values[i] ?? 0) <= 0);
                const r = isTop ? Math.min(4, barW / 2, h) : 0;
                const bx = x(i) - barW / 2;
                return (
                  <path
                    key={`${i}-${si}`}
                    d={`M${bx},${y1 + h} V${y1 + r} Q${bx},${y1} ${bx + r},${y1} H${bx + barW - r} Q${bx + barW},${y1} ${bx + barW},${y1 + r} V${y1 + h} Z`}
                    fill={s.color}
                    opacity={hover === null || hover === i ? 1 : 0.55}
                  />
                );
              });
            })}
          {lines.map((s) => {
            if (n === 0) return null;
            const pts = s.values.slice(0, n).map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`);
            const d = "M" + pts.join(" L");
            return (
              <g key={s.name}>
                {s.kind === "area" && n > 1 && (
                  <path d={`${d} L${x(n - 1).toFixed(1)},${y(0)} L${x(0).toFixed(1)},${y(0)} Z`} fill={s.color} opacity={0.1} />
                )}
                {n > 1 ? (
                  <path d={d} fill="none" stroke={s.color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
                ) : (
                  <circle cx={x(0)} cy={y(s.values[0] ?? 0)} r={4} fill={s.color} />
                )}
              </g>
            );
          })}
          {hover !== null && !hasBars && <line x1={x(hover)} x2={x(hover)} y1={pad.top} y2={pad.top + plotH} stroke="var(--chart-axis)" strokeWidth={1} />}
          {hover !== null &&
            lines.map((s) => (
              <circle key={s.name} cx={x(hover)} cy={y(s.values[hover] ?? 0)} r={4.5} fill={s.color} stroke="var(--surface)" strokeWidth={2} />
            ))}
        </svg>
      )}
      {hover !== null && width > 0 && (
        <div className="viz-tip" style={{ top: pad.top, left: flip ? undefined : tipLeft + 12, right: flip ? W - tipLeft + 12 : undefined }} role="status" aria-live="polite">
          <div className="mb-1 font-semibold">{(longLabels ?? labels)[hover]}</div>
          {series.map((s) => (
            <div key={s.name} className="flex items-center justify-between gap-3">
              <span className="flex min-w-0 items-center gap-1.5 text-muted-foreground">
                <span className="viz-swatch" data-line={s.kind === "bar" ? undefined : ""} style={{ background: s.color }} />
                <span className="truncate">{s.name}</span>
              </span>
              <span className="font-semibold tabular-nums">{format(s.values[hover] ?? 0)}</span>
            </div>
          ))}
          {extra?.(hover).map((r) => (
            <div key={r.label} className="flex items-center justify-between gap-3">
              <span className="text-muted-foreground">{r.label}</span>
              <span className="font-semibold tabular-nums">{r.value}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** Legend names the series of a chart; shown whenever there are two or more. */
export function Legend({ items }: { items: { name: string; color: string; line?: boolean }[] }) {
  return (
    <ul className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
      {items.map((s) => (
        <li key={s.name} className="flex items-center gap-1.5">
          <span className="viz-swatch" data-line={s.line ? "" : undefined} style={{ background: s.color }} />
          {s.name}
        </li>
      ))}
    </ul>
  );
}

/** Sparkline is a tiny trend line for KPI tiles; decorative (the tile states the number). */
export function Sparkline({ values, color = "var(--chart-1)", width = 96, height = 32 }: { values: number[]; color?: string; width?: number; height?: number }) {
  const id = useId();
  if (values.length < 2) return <svg width={width} height={height} aria-hidden="true" />;
  const max = Math.max(...values, 0);
  const min = Math.min(...values, 0);
  const span = max - min || 1;
  const px = (i: number) => 2 + ((width - 4) * i) / (values.length - 1);
  const py = (v: number) => 3 + (height - 6) * (1 - (v - min) / span);
  const d = "M" + values.map((v, i) => `${px(i).toFixed(1)},${py(v).toFixed(1)}`).join(" L");
  const last = values[values.length - 1] ?? 0;
  return (
    <svg width={width} height={height} aria-hidden="true" className="shrink-0">
      <defs>
        <linearGradient id={id} x1="0" x2="0" y1="0" y2="1">
          <stop offset="0" stopColor={color} stopOpacity={0.22} />
          <stop offset="1" stopColor={color} stopOpacity={0} />
        </linearGradient>
      </defs>
      <path d={`${d} L${px(values.length - 1)},${height} L${px(0)},${height} Z`} fill={`url(#${id})`} />
      <path d={d} fill="none" stroke={color} strokeWidth={1.75} strokeLinejoin="round" strokeLinecap="round" />
      <circle cx={px(values.length - 1)} cy={py(last)} r={2.5} fill={color} stroke="var(--surface)" strokeWidth={1.5} />
    </svg>
  );
}

export interface BarRow {
  key: string;
  label: ReactNode;
  value: number;
  /** Right-aligned text; defaults to format(value). */
  text?: string;
  sub?: ReactNode;
  href?: string;
}

/** BarList ranks rows with a horizontal bar behind each label. */
export function BarList({ rows, format, color = "var(--chart-1)", empty }: { rows: BarRow[]; format: (v: number) => string; color?: string; empty?: ReactNode }) {
  const max = Math.max(...rows.map((r) => r.value), 0) || 1;
  if (rows.length === 0) return <>{empty ?? <p className="text-sm text-muted-foreground">Sin datos en este periodo.</p>}</>;
  return (
    <ol className="grid gap-2.5">
      {rows.map((r) => {
        const body = (
          <>
            <div className="flex items-baseline justify-between gap-3 text-sm">
              <span className="min-w-0 truncate font-medium">{r.label}</span>
              <span className="shrink-0 font-semibold tabular-nums">{r.text ?? format(r.value)}</span>
            </div>
            <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-muted" aria-hidden="true">
              <div className="h-full rounded-full" style={{ width: `${Math.max(1.5, (r.value / max) * 100)}%`, background: color }} />
            </div>
            {r.sub && <div className="mt-1 text-xs text-muted-foreground">{r.sub}</div>}
          </>
        );
        return (
          <li key={r.key}>
            {r.href ? (
              <a href={r.href} className="-mx-2 block rounded-md px-2 py-1 hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                {body}
              </a>
            ) : (
              <div className="py-1">{body}</div>
            )}
          </li>
        );
      })}
    </ol>
  );
}

export interface FunnelStep {
  label: string;
  value: number;
  hint?: string;
}

/**
 * Funnel shows each step as a bar proportional to the first one, with the
 * share that continued from the previous step and how many were lost.
 */
export function Funnel({ steps, format }: { steps: FunnelStep[]; format: (v: number) => string }) {
  const first = steps[0]?.value ?? 0;
  return (
    <ol className="grid gap-3">
      {steps.map((s, i) => {
        const prev = i > 0 ? (steps[i - 1]?.value ?? 0) : 0;
        const share = first > 0 ? (s.value / first) * 100 : 0;
        const kept = i > 0 && prev > 0 ? (s.value / prev) * 100 : null;
        return (
          <li key={s.label}>
            <div className="flex items-baseline justify-between gap-3 text-sm">
              <span className="min-w-0 truncate">
                <span className="mr-2 inline-grid size-5 place-items-center rounded-full bg-muted text-[11px] font-semibold tabular-nums text-muted-foreground">{i + 1}</span>
                <span className="font-medium">{s.label}</span>
              </span>
              <span className="shrink-0 tabular-nums">
                <span className="font-semibold">{format(s.value)}</span>
                <span className="ml-2 inline-block w-14 text-right text-xs text-muted-foreground">{first > 0 ? share.toFixed(share < 10 ? 1 : 0) + "%" : "—"}</span>
              </span>
            </div>
            <div className="mt-1.5 h-6 overflow-hidden rounded-md bg-muted" aria-hidden="true">
              <div
                className="h-full rounded-md"
                style={{ width: `${Math.max(share, s.value > 0 ? 0.8 : 0)}%`, background: "var(--chart-1)", opacity: 1 - i * 0.14 }}
              />
            </div>
            {kept !== null && (
              <p className="mt-1 text-xs text-muted-foreground">
                {kept.toFixed(1)}% del paso anterior continuó · {format(Math.max(0, prev - s.value))} se quedaron
              </p>
            )}
          </li>
        );
      })}
    </ol>
  );
}

/** Split is a single stacked bar comparing parts of a whole, with a legend that carries the numbers. */
export function Split({ parts, format }: { parts: { name: string; value: number; color: string }[]; format: (v: number) => string }) {
  const total = parts.reduce((a, p) => a + p.value, 0);
  return (
    <div className="grid gap-3">
      <div className="flex h-3 gap-0.5 overflow-hidden rounded-full bg-muted" aria-hidden="true">
        {total > 0 &&
          parts.map((p) => (p.value > 0 ? <div key={p.name} style={{ width: `${(p.value / total) * 100}%`, background: p.color }} className="h-full first:rounded-l-full last:rounded-r-full" /> : null))}
      </div>
      <ul className="grid gap-1.5 text-sm">
        {parts.map((p) => (
          <li key={p.name} className="flex items-center justify-between gap-3">
            <span className="flex items-center gap-2">
              <span className="viz-swatch" style={{ background: p.color }} />
              {p.name}
            </span>
            <span className="tabular-nums">
              <span className="font-semibold">{format(p.value)}</span>
              <span className="ml-2 inline-block w-12 text-right text-xs text-muted-foreground">{total > 0 ? ((p.value / total) * 100).toFixed(0) + "%" : "—"}</span>
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("viz-skeleton", className)} aria-hidden="true" />;
}

/** useNow re-renders every `ms` so "hace 2 min" labels stay true. */
export function useNow(ms = 30_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(t);
  }, [ms]);
  return now;
}
