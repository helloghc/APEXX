import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Banknote, CreditCard, Minus, Plus, Printer, ScanBarcode, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { formatMoney, parsePesos } from "@/money";
import { can } from "@/perms";
import { ErrorBox, Notice, Page } from "../common";

interface Hit { sku: string; title: string; price: number; available: number; here: number }
interface Line extends Hit { qty: number }
interface SoldLine { sku: string; title: string; qty: number; unit: number; total: number }
interface Sale {
  order: { id: string; number: number; grandTotal: number; discounts: number; currency: string; lines: SoldLine[]; createdAt: string };
  change: number;
  method: "cash" | "terminal";
  location: string;
}

const LOC_KEY = "apexx.pos.location";
const read = (k: string) => { try { return localStorage.getItem(k) ?? ""; } catch { return ""; } };
const write = (k: string, v: string) => { try { localStorage.setItem(k, v); } catch { /* private mode */ } };

/** Point of sale: scan or search, charge cash or card terminal, print the ticket. */
export function PointOfSale({ me }: { me: Me }) {
  const canSell = can(me, "orders", "edit");
  const locs = useQuery({
    queryKey: ["pos", "locations"],
    queryFn: () => request<{ items: { id: string; name: string; kind: string; active: boolean }[] }>("GET", "/v1/locations").then((r) => r.items.filter((l) => l.active)),
    staleTime: 60_000,
  });
  const [loc, setLoc] = useState(read(LOC_KEY));
  const [q, setQ] = useState("");
  const [hits, setHits] = useState<Hit[]>([]);
  const [lines, setLines] = useState<Line[]>([]);
  const [discount, setDiscount] = useState("");
  const [method, setMethod] = useState<"cash" | "terminal">("cash");
  const [tendered, setTendered] = useState("");
  const [reference, setReference] = useState("");
  const [email, setEmail] = useState("");
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [sale, setSale] = useState<Sale | null>(null);
  const scan = useRef<HTMLInputElement>(null);
  const seq = useRef(0);

  useEffect(() => {
    const list = locs.data;
    if (!list?.length || list.some((l) => l.id === loc)) return;
    const first = list.find((l) => l.kind === "store") ?? list[0];
    if (first) setLoc(first.id);
  }, [locs.data, loc]);
  useEffect(() => { if (loc) write(LOC_KEY, loc); }, [loc]);

  // Search as the cashier types; a scanner types the code and presses Enter.
  useEffect(() => {
    const term = q.trim();
    if (term.length < 2) { setHits([]); return; }
    const n = ++seq.current;
    const t = setTimeout(() => {
      request<{ items: Hit[] }>("GET", `/v1/pos/lookup?q=${encodeURIComponent(term)}&location=${encodeURIComponent(loc)}`)
        .then((r) => { if (n === seq.current) setHits(r.items); })
        .catch(() => {});
    }, 120);
    return () => clearTimeout(t);
  }, [q, loc]);

  const subtotal = useMemo(() => lines.reduce((s, l) => s + l.price * l.qty, 0), [lines]);
  const off = Math.min(parsePesos(discount) ?? 0, subtotal);
  const total = subtotal - off;
  const cash = parsePesos(tendered) ?? 0;
  const change = method === "cash" ? cash - total : 0;

  function add(h: Hit) {
    setLines((ls) => {
      const i = ls.findIndex((l) => l.sku === h.sku);
      if (i < 0) return [...ls, { ...h, qty: 1 }];
      return ls.map((l, j) => (j === i ? { ...l, qty: l.qty + 1 } : l));
    });
    setQ(""); setHits([]); setSale(null);
    scan.current?.focus();
  }
  async function onScan(e: FormEvent) {
    e.preventDefault();
    const term = q.trim();
    if (!term) return;
    try {
      const r = await request<{ items: Hit[] }>("GET", `/v1/pos/lookup?q=${encodeURIComponent(term)}&location=${encodeURIComponent(loc)}`);
      const exact = r.items.find((h) => h.sku.toLowerCase() === term.toLowerCase()) ?? (r.items.length === 1 ? r.items[0] : undefined);
      if (exact) add(exact); else if (!r.items.length) toast.error(`No encontramos «${term}».`); else setHits(r.items);
    } catch (err) { toast.error(errorMessage(err)); }
  }
  const setQty = (sku: string, qty: number) => setLines((ls) => (qty <= 0 ? ls.filter((l) => l.sku !== sku) : ls.map((l) => (l.sku === sku ? { ...l, qty } : l))));

  async function charge() {
    setBusy(true); setErrs({});
    try {
      const r = await request<Sale>("POST", "/v1/pos/sales", {
        location: loc,
        items: lines.map((l) => ({ sku: l.sku, qty: l.qty })),
        discount: off,
        customer: email.trim() ? { email: email.trim() } : undefined,
        payment: { method, tendered: method === "cash" ? cash : 0, reference: method === "terminal" ? reference : "" },
      });
      setSale(r); setLines([]); setDiscount(""); setTendered(""); setReference(""); setEmail("");
      toast.success(`Venta #${r.order.number} cobrada.`);
      scan.current?.focus();
    } catch (err) { setErrs(fieldErrors(err)); toast.error(errorMessage(err)); } finally { setBusy(false); }
  }

  if (locs.isPending) return <Page title="Caja"><p className="text-sm text-muted-foreground">Cargando…</p></Page>;
  if (locs.error) return <Page title="Caja"><ErrorBox error={locs.error} onRetry={() => void locs.refetch()} /></Page>;
  if (!locs.data.length) return <Page title="Caja"><Notice tone="warn">Primero da de alta tu tienda física en Configuración → Ubicaciones.</Notice></Page>;
  const ready = canSell && lines.length > 0 && !busy && (method === "terminal" ? reference.trim() !== "" : cash >= total);

  return (
    <Page title="Caja" sub="Vende en tu tienda física: escanea el código o busca el producto, cobra en efectivo o con terminal y entrega el ticket. El inventario se descuenta primero de esta tienda.">
      <div className="pos-screen grid gap-6 lg:grid-cols-[1fr_360px]">
        <section className="space-y-4" aria-label="Productos">
          <div className="flex flex-wrap items-end gap-3">
            <div className="space-y-1">
              <Label htmlFor="pos-loc">Tienda</Label>
              <select id="pos-loc" className="block h-9 rounded-md border bg-background px-2 text-sm" value={loc} onChange={(e) => setLoc(e.target.value)}>
                {locs.data.map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
              </select>
            </div>
            <form className="min-w-60 flex-1 space-y-1" onSubmit={(e) => void onScan(e)}>
              <Label htmlFor="pos-scan">Código o nombre</Label>
              <div className="relative">
                <ScanBarcode className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden />
                <Input id="pos-scan" ref={scan} autoFocus autoComplete="off" className="pl-8" placeholder="Escanea o escribe y pulsa Enter" value={q} onChange={(e) => setQ(e.target.value)} />
              </div>
            </form>
          </div>
          {hits.length > 0 && (
            <ul className="divide-y rounded-md border" aria-label="Resultados">
              {hits.map((h) => (
                <li key={h.sku}>
                  <button type="button" className="flex w-full items-center justify-between gap-3 px-3 py-2 text-left text-sm hover:bg-muted" onClick={() => add(h)}>
                    <span><span className="font-medium">{h.title}</span> <span className="text-muted-foreground">· {h.sku}</span></span>
                    <span className="whitespace-nowrap">{formatMoney(h.price)} <span className={h.here > 0 ? "text-muted-foreground" : "text-destructive"}>· {h.here} aquí</span></span>
                  </button>
                </li>
              ))}
            </ul>
          )}
          {lines.length === 0 ? (
            <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">Escanea el primer producto para empezar la venta.</p>
          ) : (
            <table className="w-full text-sm">
              <thead className="text-left text-muted-foreground"><tr><th className="py-1">Producto</th><th>Cantidad</th><th className="text-right">Importe</th><th /></tr></thead>
              <tbody>
                {lines.map((l) => (
                  <tr key={l.sku} className="border-t">
                    <td className="py-2"><div className="font-medium">{l.title}</div><div className="text-xs text-muted-foreground">{l.sku} · {formatMoney(l.price)}{l.qty > l.here && <span className="text-destructive"> · solo {l.here} en esta tienda</span>}</div></td>
                    <td>
                      <div className="flex items-center gap-1">
                        <Button size="icon" variant="outline" className="size-8" aria-label={`Quitar una pieza de ${l.title}`} onClick={() => setQty(l.sku, l.qty - 1)}><Minus className="size-3" /></Button>
                        <span className="w-8 text-center tabular-nums">{l.qty}</span>
                        <Button size="icon" variant="outline" className="size-8" aria-label={`Agregar una pieza de ${l.title}`} onClick={() => setQty(l.sku, l.qty + 1)}><Plus className="size-3" /></Button>
                      </div>
                    </td>
                    <td className="text-right tabular-nums">{formatMoney(l.price * l.qty)}</td>
                    <td className="text-right"><Button size="icon" variant="ghost" aria-label={`Quitar ${l.title}`} onClick={() => setQty(l.sku, 0)}><Trash2 className="size-4" /></Button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>

        <aside className="space-y-4 rounded-lg border p-4" aria-label="Cobro">
          <dl className="space-y-1 text-sm">
            <div className="flex justify-between"><dt>Subtotal</dt><dd className="tabular-nums">{formatMoney(subtotal)}</dd></div>
            {off > 0 && <div className="flex justify-between"><dt>Descuento</dt><dd className="tabular-nums">−{formatMoney(off)}</dd></div>}
            <div className="flex justify-between text-lg font-semibold"><dt>Total</dt><dd className="tabular-nums">{formatMoney(total)}</dd></div>
          </dl>
          <div className="space-y-1">
            <Label htmlFor="pos-off">Descuento (pesos)</Label>
            <Input id="pos-off" inputMode="decimal" value={discount} onChange={(e) => setDiscount(e.target.value)} placeholder="0" />
          </div>
          <div className="grid grid-cols-2 gap-2" role="radiogroup" aria-label="Forma de pago">
            <Button type="button" role="radio" aria-checked={method === "cash"} variant={method === "cash" ? "default" : "outline"} onClick={() => setMethod("cash")}><Banknote className="size-4" /> Efectivo</Button>
            <Button type="button" role="radio" aria-checked={method === "terminal"} variant={method === "terminal" ? "default" : "outline"} onClick={() => setMethod("terminal")}><CreditCard className="size-4" /> Terminal</Button>
          </div>
          {method === "cash" ? (
            <div className="space-y-1">
              <Label htmlFor="pos-cash">Recibido</Label>
              <Input id="pos-cash" inputMode="decimal" value={tendered} onChange={(e) => setTendered(e.target.value)} placeholder={(total / 100).toFixed(2)} aria-invalid={!!errs["payment.tendered"]} />
              {lines.length > 0 && tendered !== "" && <p className={change < 0 ? "text-sm text-destructive" : "text-sm"}>{change < 0 ? `Faltan ${formatMoney(-change)}` : `Cambio: ${formatMoney(change)}`}</p>}
              {errs["payment.tendered"] && <p className="text-sm text-destructive">{errs["payment.tendered"]}</p>}
            </div>
          ) : (
            <div className="space-y-1">
              <Label htmlFor="pos-ref">Autorización de la terminal</Label>
              <Input id="pos-ref" value={reference} maxLength={60} onChange={(e) => setReference(e.target.value)} aria-invalid={!!errs["payment.reference"]} />
              <p className="text-xs text-muted-foreground">Cobra con tu terminal y copia aquí el número de autorización del comprobante.</p>
              {errs["payment.reference"] && <p className="text-sm text-destructive">{errs["payment.reference"]}</p>}
            </div>
          )}
          <div className="space-y-1">
            <Label htmlFor="pos-mail">Correo del cliente (opcional)</Label>
            <Input id="pos-mail" type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="Para enviarle su compra" />
          </div>
          {!canSell && <Notice tone="warn">Tu acceso permite ver la caja pero no cobrar.</Notice>}
          <Button className="w-full" size="lg" disabled={!ready} onClick={() => void charge()}>{busy ? "Cobrando…" : `Cobrar ${formatMoney(total)}`}</Button>
        </aside>
      </div>

      {sale && (
        <div className="mt-6 max-w-sm space-y-3">
          <div id="pos-ticket" className="rounded-md border bg-white p-4 font-mono text-xs text-black">
            <p className="text-center font-semibold">{sale.location}</p>
            <p className="text-center">Venta #{sale.order.number} · {new Date(sale.order.createdAt).toLocaleString("es-MX", { dateStyle: "short", timeStyle: "short" })}</p>
            <hr className="my-2 border-dashed border-black" />
            {sale.order.lines.map((l) => (
              <div key={l.sku} className="flex justify-between gap-2"><span>{l.qty} × {l.title}</span><span className="whitespace-nowrap">{formatMoney(l.total, sale.order.currency)}</span></div>
            ))}
            <hr className="my-2 border-dashed border-black" />
            {sale.order.discounts > 0 && <div className="flex justify-between"><span>Descuento</span><span>−{formatMoney(sale.order.discounts, sale.order.currency)}</span></div>}
            <div className="flex justify-between font-semibold"><span>Total</span><span>{formatMoney(sale.order.grandTotal, sale.order.currency)}</span></div>
            <div className="flex justify-between"><span>{sale.method === "cash" ? "Efectivo" : "Terminal"}</span><span>{sale.method === "cash" ? formatMoney(sale.order.grandTotal + sale.change, sale.order.currency) : ""}</span></div>
            {sale.method === "cash" && <div className="flex justify-between"><span>Cambio</span><span>{formatMoney(sale.change, sale.order.currency)}</span></div>}
            <p className="mt-2 text-center">¡Gracias por tu compra!</p>
          </div>
          <Button variant="outline" onClick={() => window.print()}><Printer className="size-4" /> Imprimir ticket</Button>
        </div>
      )}
    </Page>
  );
}
