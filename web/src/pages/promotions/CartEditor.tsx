import { useState } from "react";
import { CheckCircle2, CircleSlash, Gift, Minus, Plus, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { formatMoney } from "@/money";
import type { PromoResult, Simulation } from "@/promoApi";
import { href } from "@/router";
import { TYPE_LABEL } from "./lib";
import { ProductPicker } from "./shared";

export interface CartItem {
  sku: string;
  qty: number;
  title: string;
  price?: number;
}

/** CartEditor builds a test cart: add variants from the catalog and set quantities. */
export function CartEditor({ items, onChange, compact }: { items: CartItem[]; onChange: (items: CartItem[]) => void; compact?: boolean }) {
  const [pick, setPick] = useState(false);
  const setQty = (sku: string, qty: number) => onChange(items.map((i) => (i.sku === sku ? { ...i, qty: Math.max(1, Math.min(999, qty)) } : i)));
  return (
    <div className="grid gap-2">
      {items.length === 0 ? (
        <p className="rounded-md border border-dashed px-3 py-4 text-center text-sm text-muted-foreground">El carrito de prueba está vacío.</p>
      ) : (
        <ul className="divide-y rounded-md border bg-card">
          {items.map((it) => (
            <li key={it.sku} className="flex items-center gap-2 px-3 py-2 text-sm">
              <span className="min-w-0 flex-1">
                <span className="block truncate font-medium">{it.title}</span>
                <span className="block font-mono text-xs text-muted-foreground">
                  {it.sku}
                  {it.price !== undefined && !compact ? ` · ${formatMoney(it.price)}` : ""}
                </span>
              </span>
              <span className="flex items-center rounded-md border">
                <Button type="button" variant="ghost" size="icon" className="size-7" aria-label={`Quitar una pieza de ${it.title}`} onClick={() => setQty(it.sku, it.qty - 1)}>
                  <Minus />
                </Button>
                <input
                  className="w-9 bg-transparent text-center text-sm tabular-nums focus-visible:outline-none"
                  inputMode="numeric"
                  aria-label={`Cantidad de ${it.title}`}
                  value={it.qty}
                  onChange={(e) => setQty(it.sku, Number(e.target.value.replace(/\D/g, "")) || 1)}
                />
                <Button type="button" variant="ghost" size="icon" className="size-7" aria-label={`Agregar una pieza de ${it.title}`} onClick={() => setQty(it.sku, it.qty + 1)}>
                  <Plus />
                </Button>
              </span>
              <Button type="button" variant="ghost" size="icon" className="size-7" aria-label={`Quitar ${it.title}`} onClick={() => onChange(items.filter((i) => i.sku !== it.sku))}>
                <Trash2 />
              </Button>
            </li>
          ))}
        </ul>
      )}
      <div>
        <Button type="button" variant="outline" size="sm" onClick={() => setPick(true)}>
          <Plus /> Agregar producto
        </Button>
      </div>
      <ProductPicker
        open={pick}
        onOpenChange={setPick}
        mode="sku"
        title="Agregar al carrito de prueba"
        exclude={items.map((i) => i.sku)}
        onPick={(ps) => onChange([...items, ...ps.map((p) => ({ sku: p.id, qty: 1, title: p.title, price: p.price }))])}
      />
    </div>
  );
}

/** PromoOutcome is one promotion explained: applied or not, why, and what to do next. */
export function PromoOutcome({ r, link, highlight }: { r: PromoResult; link?: boolean; highlight?: boolean }) {
  const disc = r.discount.amount + (r.shippingDiscount?.amount ?? 0);
  return (
    <li className={cn("flex gap-3 px-3 py-2.5 text-sm", highlight && "bg-accent/40")}>
      {r.applied ? (
        <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-ok" aria-hidden="true" />
      ) : (
        <CircleSlash className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      )}
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
          {link ? (
            <a className="font-medium text-primary hover:underline" href={href("promociones", "p", r.id)}>
              {r.name}
            </a>
          ) : (
            <span className="font-medium">{r.name}</span>
          )}
          <Badge variant={r.applied ? "ok" : "secondary"}>{r.applied ? "Aplica" : "No aplica"}</Badge>
          <span className="text-xs text-muted-foreground">{TYPE_LABEL[r.type] ?? r.type}</span>
          {r.coupon && <Badge variant="outline">Cupón {r.coupon}</Badge>}
        </div>
        <p className={r.applied ? "" : "text-muted-foreground"}>{r.reason}</p>
        {r.hint && <p className="text-xs text-muted-foreground">{r.hint}</p>}
      </div>
      {r.applied && disc > 0 && <span className="shrink-0 font-medium tabular-nums text-ok">−{formatMoney(disc, r.discount.currency)}</span>}
      {r.applied && disc === 0 && r.freeShipping && <span className="shrink-0 text-xs text-muted-foreground">Envío</span>}
    </li>
  );
}

/** SimulationTotals shows lines and totals of a simulated cart. */
export function SimulationTotals({ sim, showLines = true }: { sim: Simulation; showLines?: boolean }) {
  const t = sim.totals;
  const row = (label: string, value: string, cls = "") => (
    <div className={cn("flex justify-between gap-4", cls)}>
      <dt>{label}</dt>
      <dd className="tabular-nums">{value}</dd>
    </div>
  );
  return (
    <div className="grid gap-3 text-sm">
      {showLines && (
        <ul className="divide-y rounded-md border bg-card">
          {sim.lines.map((l, i) => (
            <li key={l.sku + i} className="flex items-start gap-3 px-3 py-2">
              <span className="min-w-0 flex-1">
                <span className="flex items-center gap-1.5 font-medium">
                  {l.gift && <Gift className="size-3.5 text-ok" aria-hidden="true" />}
                  <span className="truncate">
                    {l.qty} × {l.title}
                  </span>
                </span>
                <span className="block text-xs text-muted-foreground">
                  {l.gift ? "Regalo de una promoción" : `${formatMoney(l.unit.amount, l.unit.currency)} c/u`}
                  {l.priceList ? ` · lista “${l.priceList}”` : ""}
                  {l.qty < l.requestedQty ? ` · pediste ${l.requestedQty}, hay ${l.available}` : ""}
                </span>
              </span>
              <span className="shrink-0 text-right tabular-nums">
                {l.gift ? "Gratis" : formatMoney(l.total.amount, l.total.currency)}
                {l.discount && <span className="block text-xs text-ok">−{formatMoney(l.discount.amount, l.discount.currency)}</span>}
              </span>
            </li>
          ))}
        </ul>
      )}
      {sim.messages.length > 0 && (
        <ul className="list-disc pl-5 text-xs text-warn">
          {sim.messages.map((m, i) => (
            <li key={i}>
              {m.sku ? `${m.sku}: ` : ""}
              {m.text}
            </li>
          ))}
        </ul>
      )}
      <dl className="grid gap-1">
        {row("Productos", formatMoney(t.items.amount, t.items.currency))}
        {t.discounts.amount > 0 && row("Descuentos", "−" + formatMoney(t.discounts.amount, t.discounts.currency), "text-ok")}
        {row(
          "Envío",
          sim.shipping
            ? (t.shippingDiscount.amount > 0 && sim.shipping.original ? `${formatMoney(sim.shipping.original.amount)} → ` : "") +
                (t.shipping.amount === 0 ? "Gratis" : formatMoney(t.shipping.amount, t.shipping.currency))
            : "Falta código postal",
        )}
        {row("Total", formatMoney(t.grand.amount, t.grand.currency), "border-t pt-1.5 text-base font-semibold")}
        {row("IVA incluido", formatMoney(t.taxIncluded.amount, t.taxIncluded.currency), "text-xs text-muted-foreground")}
      </dl>
    </div>
  );
}
