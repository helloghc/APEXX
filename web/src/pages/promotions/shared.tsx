import { useQuery } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { Loader2, Plus, Search, X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { TableCell, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { centsToPesos, formatMoney, parsePesos } from "@/money";
import { promoApi, type TargetProduct } from "@/promoApi";
import { href } from "@/router";
import { bpsToPercent, isoToLocal, localToIso, parsePercent, STATUS_LABEL, statusTone } from "./lib";

export function useDebounced<T>(value: T, ms = 300): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

export function StatusBadge({ status }: { status: string }) {
  return <Badge variant={statusTone(status)}>{STATUS_LABEL[status] ?? status}</Badge>;
}

/** SubNav is the second-level navigation of a section, as real links (works with back/forward). */
export function SubNav({ items, active, label }: { items: { key: string; label: string; route: string[] }[]; active: string; label: string }) {
  return (
    <nav aria-label={label} className="-mx-1 flex gap-1 overflow-x-auto border-b px-1">
      {items.map((it) => (
        <a
          key={it.key}
          href={href(...it.route)}
          aria-current={it.key === active ? "page" : undefined}
          className={cn(
            "-mb-px whitespace-nowrap border-b-2 px-3 py-2 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            it.key === active ? "border-primary text-foreground" : "border-transparent text-muted-foreground hover:text-foreground",
          )}
        >
          {it.label}
        </a>
      ))}
    </nav>
  );
}

export function Section({ title, description, children, aside }: { title: string; description?: ReactNode; children: ReactNode; aside?: ReactNode }) {
  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between gap-3">
        <div className="grid gap-1.5">
          <CardTitle>{title}</CardTitle>
          {description && <CardDescription>{description}</CardDescription>}
        </div>
        {aside}
      </CardHeader>
      <CardContent className="grid gap-4">{children}</CardContent>
    </Card>
  );
}

export function EmptyState({ icon, title, text, action }: { icon: ReactNode; title: string; text: string; action?: ReactNode }) {
  return (
    <div className="grid justify-items-center gap-3 rounded-xl border border-dashed bg-card px-6 py-12 text-center">
      <div className="grid size-11 place-items-center rounded-full bg-accent text-accent-foreground [&_svg]:size-5">{icon}</div>
      <div className="grid gap-1">
        <h2 className="text-base font-semibold">{title}</h2>
        <p className="mx-auto max-w-md text-sm text-muted-foreground">{text}</p>
      </div>
      {action}
    </div>
  );
}

export function SkeletonRows({ cols, rows = 5 }: { cols: number; rows?: number }) {
  return (
    <>
      {Array.from({ length: rows }, (_, r) => (
        <TableRow key={r} aria-hidden="true">
          {Array.from({ length: cols }, (_, c) => (
            <TableCell key={c}>
              <div className="h-4 animate-pulse rounded bg-muted" style={{ width: `${40 + ((r * 7 + c * 13) % 50)}%` }} />
            </TableCell>
          ))}
        </TableRow>
      ))}
    </>
  );
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("animate-pulse rounded-md bg-muted", className)} aria-hidden="true" />;
}

type NumKind = "money" | "percent" | "int";

const toText: Record<NumKind, (v: number | undefined) => string> = {
  money: (v) => (v ? centsToPesos(v) : ""),
  percent: (v) => bpsToPercent(v),
  int: (v) => (v ? String(v) : ""),
};
const fromText: Record<NumKind, (s: string) => number | null> = {
  money: (s) => (s.trim() === "" ? 0 : parsePesos(s)),
  percent: (s) => (s.trim() === "" ? 0 : parsePercent(s)),
  int: (s) => (s.trim() === "" ? 0 : /^\d+$/.test(s.trim()) ? Number(s.trim()) : null),
};

/**
 * NumField edits an integer value (cents, basis points or a count) as text:
 * people type "1,500" or "12.5" and the form keeps exact integers.
 */
export function NumField({
  kind,
  value,
  onChange,
  className,
  placeholder,
  disabled,
  ...rest
}: {
  kind: NumKind;
  value: number | undefined;
  onChange: (v: number) => void;
  className?: string;
  placeholder?: string;
  disabled?: boolean;
  id?: string;
  "aria-label"?: string;
  "aria-invalid"?: boolean;
  "aria-describedby"?: string;
}) {
  const [text, setText] = useState(() => toText[kind](value));
  const [bad, setBad] = useState(false);
  const focused = useRef(false);
  useEffect(() => {
    if (!focused.current) setText(toText[kind](value));
  }, [value, kind]);
  const affix = kind === "money" ? "$" : kind === "percent" ? "%" : "";
  return (
    <div className={cn("relative", className)}>
      {kind === "money" && <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-sm text-muted-foreground">{affix}</span>}
      <Input
        {...rest}
        inputMode={kind === "int" ? "numeric" : "decimal"}
        value={text}
        disabled={disabled}
        placeholder={placeholder ?? (kind === "money" ? "0.00" : kind === "percent" ? "0" : "")}
        aria-invalid={rest["aria-invalid"] || bad || undefined}
        className={cn(kind === "money" && "pl-6", kind === "percent" && "pr-7", "tabular-nums")}
        onFocus={() => (focused.current = true)}
        onBlur={() => {
          focused.current = false;
          const v = fromText[kind](text);
          if (v !== null) setText(toText[kind](v));
        }}
        onChange={(e) => {
          setText(e.target.value);
          const v = fromText[kind](e.target.value);
          setBad(v === null);
          if (v !== null) onChange(v);
        }}
      />
      {kind === "percent" && <span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-sm text-muted-foreground">%</span>}
    </div>
  );
}

/** DateTimeField edits an ISO instant as wall-clock time of the store (America/Mexico_City). */
export function DateTimeField({
  value,
  onChange,
  ...rest
}: {
  value: string | undefined;
  onChange: (iso: string | undefined) => void;
  id?: string;
  "aria-invalid"?: boolean;
  "aria-describedby"?: string;
}) {
  return (
    <div className="flex gap-1">
      <Input {...rest} type="datetime-local" value={isoToLocal(value)} onChange={(e) => onChange(localToIso(e.target.value))} />
      {value && (
        <Button type="button" variant="ghost" size="icon" aria-label="Quitar fecha" onClick={() => onChange(undefined)}>
          <X />
        </Button>
      )}
    </div>
  );
}

/** ChipInput edits a list of short values: type and press Enter (or comma); click a chip to remove it. */
export function ChipInput({
  values,
  onChange,
  placeholder,
  suggestions,
  transform,
  ...rest
}: {
  values: string[];
  onChange: (v: string[]) => void;
  placeholder?: string;
  suggestions?: string[];
  transform?: (s: string) => string;
  id?: string;
  "aria-invalid"?: boolean;
  "aria-describedby"?: string;
}) {
  const [text, setText] = useState("");
  const listId = useId();
  function add(raw: string) {
    const parts = raw
      .split(",")
      .map((s) => (transform ? transform(s.trim()) : s.trim()))
      .filter(Boolean);
    if (parts.length) onChange([...new Set([...values, ...parts])]);
    setText("");
  }
  function onKey(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      add(text);
    } else if (e.key === "Backspace" && text === "" && values.length) {
      onChange(values.slice(0, -1));
    }
  }
  const open = (suggestions ?? []).filter((s) => !values.includes(s));
  return (
    <div className="grid gap-2">
      {values.length > 0 && (
        <ul className="flex flex-wrap gap-1.5" aria-label="Valores elegidos">
          {values.map((v) => (
            <li key={v}>
              <Badge variant="secondary" className="gap-1 py-1 pl-2.5 pr-1">
                {v}
                <button
                  type="button"
                  className="grid size-4 cursor-pointer place-items-center rounded-full hover:bg-foreground/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  aria-label={`Quitar ${v}`}
                  onClick={() => onChange(values.filter((x) => x !== v))}
                >
                  <X className="size-3" />
                </button>
              </Badge>
            </li>
          ))}
        </ul>
      )}
      <Input
        {...rest}
        value={text}
        list={open.length ? listId : undefined}
        placeholder={placeholder}
        onChange={(e) => {
          const v = e.target.value;
          // Picking a suggestion from the list adds it right away.
          if (open.includes(v)) add(v);
          else setText(v);
        }}
        onKeyDown={onKey}
        onBlur={() => text.trim() && add(text)}
      />
      {open.length > 0 && (
        <datalist id={listId}>
          {open.map((s) => (
            <option key={s} value={s} />
          ))}
        </datalist>
      )}
    </div>
  );
}

export interface Picked {
  kind: "product" | "sku";
  id: string;
  title: string;
  price?: number;
}

/**
 * ProductPicker searches the catalog (typo tolerant, also by SKU) and returns
 * whole products or individual variants.
 */
export function ProductPicker({
  open,
  onOpenChange,
  mode,
  title,
  description,
  exclude,
  single,
  onPick,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  mode: "product" | "sku";
  title: string;
  description?: string;
  exclude?: string[];
  single?: boolean;
  onPick: (items: Picked[]) => void;
}) {
  const [text, setText] = useState("");
  const [sel, setSel] = useState<Record<string, Picked>>({});
  const q = useDebounced(text, 250);
  const res = useQuery({ queryKey: ["targets", q], queryFn: () => promoApi.targets(q), enabled: open, staleTime: 30_000 });
  useEffect(() => {
    if (open) {
      setSel({});
      setText("");
    }
  }, [open]);
  const rows: Picked[] = [];
  for (const p of res.data ?? []) {
    if (mode === "product") rows.push({ kind: "product", id: p.id, title: p.title, price: p.variants[0]?.price });
    else for (const v of p.variants) rows.push({ kind: "sku", id: v.sku, title: `${p.title} · ${v.title}`.trim(), price: v.price });
  }
  const shown = rows.filter((r) => !(exclude ?? []).includes(r.id));
  const n = Object.keys(sel).length;
  function toggle(r: Picked) {
    if (single) {
      onPick([r]);
      onOpenChange(false);
      return;
    }
    setSel((s) => {
      const c = { ...s };
      if (c[r.id]) delete c[r.id];
      else c[r.id] = r;
      return c;
    });
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description ?? "Busca por nombre, marca o SKU."}</DialogDescription>
        </DialogHeader>
        <div className="relative">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
          <Input autoFocus className="pl-9" placeholder="Buscar en el catálogo" aria-label="Buscar en el catálogo" value={text} onChange={(e) => setText(e.target.value)} />
        </div>
        <div className="max-h-[46vh] min-h-40 overflow-y-auto rounded-md border" role="group" aria-label="Resultados">
          {res.isPending && (
            <p className="flex items-center gap-2 p-4 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" /> Buscando…
            </p>
          )}
          {!res.isPending && shown.length === 0 && (
            <p className="p-4 text-sm text-muted-foreground">{rows.length ? "Todo lo que coincide ya está agregado." : "Sin resultados. Revisa la ortografía o prueba con el SKU."}</p>
          )}
          <ul>
            {shown.map((r) => (
              <li key={r.id} className="border-b last:border-0">
                <label className="flex cursor-pointer items-center gap-3 px-3 py-2 text-sm hover:bg-muted/60">
                  {!single && <Checkbox checked={Boolean(sel[r.id])} onCheckedChange={() => toggle(r)} />}
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">{r.title}</span>
                    <span className="block font-mono text-xs text-muted-foreground">{r.id}</span>
                  </span>
                  {r.price !== undefined && <span className="shrink-0 tabular-nums text-muted-foreground">{formatMoney(r.price)}</span>}
                  {single && (
                    <Button type="button" size="sm" variant="outline" onClick={() => toggle(r)}>
                      Elegir
                    </Button>
                  )}
                </label>
              </li>
            ))}
          </ul>
        </div>
        {!single && (
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancelar
            </Button>
            <Button
              type="button"
              disabled={n === 0}
              onClick={() => {
                onPick(Object.values(sel));
                onOpenChange(false);
              }}
            >
              <Plus /> Agregar {n > 0 ? n : ""}
            </Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  );
}

/** useTitles resolves product ids and SKUs into titles for display. */
export function useTitles(productIds: string[], skus: string[]) {
  const key = [...productIds].sort().join(",") + "|" + [...skus].sort().join(",");
  const q = useQuery({
    queryKey: ["target-titles", key],
    queryFn: () => promoApi.resolveTargets(productIds, skus),
    staleTime: 60_000,
    enabled: productIds.length + skus.length > 0,
  });
  const products: Record<string, TargetProduct> = {};
  const variants: Record<string, { title: string; price?: number }> = {};
  for (const p of q.data ?? []) {
    products[p.id] = p;
    for (const v of p.variants) variants[v.sku] = { title: `${p.title} · ${v.title}`.trim(), price: v.price };
  }
  return { products, variants, loading: q.isPending && productIds.length + skus.length > 0 };
}

/** PickedList shows selected products/SKUs with their titles and a remove button. */
export function PickedList({ items, onRemove, empty }: { items: { id: string; title?: string; hint?: ReactNode }[]; onRemove: (id: string) => void; empty?: string }) {
  if (items.length === 0) return empty ? <p className="text-sm text-muted-foreground">{empty}</p> : null;
  return (
    <ul className="divide-y rounded-md border">
      {items.map((it) => (
        <li key={it.id} className="flex items-center gap-3 px-3 py-2 text-sm">
          <span className="min-w-0 flex-1">
            <span className="block truncate font-medium">{it.title || it.id}</span>
            <span className="block font-mono text-xs text-muted-foreground">{it.id}</span>
          </span>
          {it.hint}
          <Button type="button" variant="ghost" size="icon" className="size-7" aria-label={`Quitar ${it.title || it.id}`} onClick={() => onRemove(it.id)}>
            <X />
          </Button>
        </li>
      ))}
    </ul>
  );
}

/** Field is a label + control + hint/error for controls that are not a single input. */
export function Field({ label, hint, error, children, className }: { label: string; hint?: ReactNode; error?: string; children: ReactNode; className?: string }) {
  return (
    <div className={cn("grid gap-1.5", className)} role="group" aria-label={label}>
      <span className="text-sm font-medium leading-none">{label}</span>
      {children}
      {error ? (
        <p className="text-xs font-medium text-destructive" role="alert">
          {error}
        </p>
      ) : (
        hint && <p className="text-xs text-muted-foreground">{hint}</p>
      )}
    </div>
  );
}

/** Choice is a radio card group (used for promotion type, stacking mode…). */
export function Choice<T extends string>({
  value,
  onChange,
  options,
  label,
  columns = "sm:grid-cols-3",
  disabled,
}: {
  value: T;
  onChange: (v: T) => void;
  options: { value: T; label: string; help?: string }[];
  label: string;
  columns?: string;
  disabled?: boolean;
}) {
  const name = useId();
  return (
    <div role="radiogroup" aria-label={label} className={cn("grid gap-2", columns)}>
      {options.map((o) => (
        <label
          key={o.value}
          className={cn(
            "flex cursor-pointer gap-2.5 rounded-lg border bg-card p-3 text-sm transition-colors has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring",
            value === o.value ? "border-primary bg-accent/40" : "hover:bg-muted/50",
            disabled && "cursor-not-allowed opacity-60",
          )}
        >
          <input
            type="radio"
            name={name}
            className="mt-0.5 size-4 shrink-0 accent-[var(--accent)]"
            checked={value === o.value}
            disabled={disabled}
            onChange={() => onChange(o.value)}
          />
          <span className="grid gap-0.5">
            <span className="font-medium leading-tight">{o.label}</span>
            {o.help && <span className="text-xs text-muted-foreground">{o.help}</span>}
          </span>
        </label>
      ))}
    </div>
  );
}
