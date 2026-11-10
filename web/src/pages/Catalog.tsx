import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { api, ApiError, errorMessage, fieldErrors, type Me, type Product, type Variant } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { slugify, validateProduct } from "@/forms";
import { centsToPesos, formatMoney, parsePesos } from "@/money";
import { can } from "@/perms";
import { href, navigate } from "@/router";
import { ErrorBox, Notice, Page, StatusPill } from "./common";

const CHANNEL = "mx";
const CURRENCY = "MXN";

export function Catalog({ me, productId }: { me: Me; productId?: string }) {
  if (productId) return <ProductEditor key={productId} me={me} id={productId === "nuevo" ? null : productId} />;
  return <ProductList me={me} />;
}

function ProductList({ me }: { me: Me }) {
  const [q, setQ] = useState("");
  const list = useInfiniteQuery({
    queryKey: ["products"],
    queryFn: ({ pageParam }) => api.products(pageParam),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor || undefined,
  });
  const all = useMemo(() => list.data?.pages.flatMap((p) => p.items) ?? [], [list.data]);
  const total = list.data?.pages[0]?.total ?? 0;
  const needle = q.trim().toLowerCase();
  const shown = needle
    ? all.filter(
        (p) =>
          p.title.toLowerCase().includes(needle) ||
          p.handle.toLowerCase().includes(needle) ||
          p.id.toLowerCase().includes(needle) ||
          (p.variants ?? []).some((v) => v.sku.toLowerCase().includes(needle)),
      )
    : all;

  return (
    <Page
      title="Catálogo"
      sub={`${total.toLocaleString("es-MX")} productos`}
      actions={
        can(me, "catalog", "edit") && (
          <Button asChild>
            <a href={href("catalogo", "nuevo")}>
              <Plus /> Nuevo producto
            </a>
          </Button>
        )
      }
    >
      <FormField id="cat-search" label="Buscar en los productos cargados" description={`Por nombre, dirección, ID o SKU entre los ${all.length} cargados.`} className="max-w-md">
        <Input type="search" value={q} onChange={(e) => setQ(e.target.value)} />
      </FormField>
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Producto</TableHead>
            <TableHead>ID</TableHead>
            <TableHead>Estado</TableHead>
            <TableHead className="text-right">Variantes</TableHead>
            <TableHead className="text-right">Disponible</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {shown.map((p) => (
            <TableRow key={p.id}>
              <TableCell>
                <a className="font-medium text-primary hover:underline" href={href("catalogo", p.id)}>
                  {p.title}
                </a>
                <div className="text-xs text-muted-foreground">{p.handle}</div>
              </TableCell>
              <TableCell className="font-mono text-xs">{p.id}</TableCell>
              <TableCell>
                <StatusPill status={p.status} />
              </TableCell>
              <TableCell className="text-right tabular-nums">{p.variants?.length ?? 0}</TableCell>
              <TableCell className="text-right tabular-nums">{Object.values(p.available ?? {}).reduce((x, y) => x + y, 0)}</TableCell>
            </TableRow>
          ))}
          {!list.isPending && shown.length === 0 && (
            <TableRow>
              <TableCell colSpan={5} className="py-8 text-center text-muted-foreground">
                {needle ? "Ningún producto cargado coincide. Carga más o cambia la búsqueda." : "Todavía no hay productos."}
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      {list.isPending && <p className="text-sm text-muted-foreground">Cargando productos…</p>}
      {list.hasNextPage && (
        <div>
          <Button variant="outline" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>
            {list.isFetchingNextPage ? "Cargando…" : "Cargar más"}
          </Button>
        </div>
      )}
    </Page>
  );
}

const empty: Product = {
  id: "",
  handle: "",
  title: "",
  description: "",
  brand: "",
  categories: [],
  status: "active",
  variants: [{ sku: "", title: "", weightGrams: 0 }],
};

function ProductEditor({ me, id }: { me: Me; id: string | null }) {
  const qc = useQueryClient();
  const editable = can(me, "catalog", "edit");
  const q = useQuery({ queryKey: ["product", id], queryFn: () => api.product(id!), enabled: id !== null });
  const [p, setP] = useState<Product | null>(id === null ? empty : null);
  const [version, setVersion] = useState<number | undefined>(undefined);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [saveErr, setSaveErr] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (q.data) setP({ ...q.data, variants: q.data.variants ?? [] });
  }, [q.data]);

  if (id !== null && q.isPending) return <Page title="Producto">Cargando…</Page>;
  if (id !== null && q.error)
    return (
      <Page title="Producto" sub={<a className="text-primary hover:underline" href={href("catalogo")}>← Catálogo</a>}>
        <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      </Page>
    );
  if (!p) return null;

  const set = (patch: Partial<Product>) => setP({ ...p, ...patch });
  const setVariant = (i: number, patch: Partial<Variant>) => {
    const vs = [...(p.variants ?? [])];
    vs[i] = { ...vs[i]!, ...patch };
    set({ variants: vs });
  };

  async function save() {
    if (!p) return;
    const v = validateProduct(p);
    setErrors(v);
    setSaveErr(null);
    if (Object.keys(v).length) return;
    setBusy(true);
    try {
      const r = await api.saveProduct(p, version);
      setVersion(r.version);
      toast.success(`Producto guardado (versión ${r.version})`);
      void qc.invalidateQueries({ queryKey: ["products"] });
      void qc.invalidateQueries({ queryKey: ["product", p.id] });
      if (id === null) navigate("catalogo", p.id);
    } catch (e) {
      setErrors(fieldErrors(e));
      setSaveErr(
        e instanceof ApiError && e.status === 412
          ? new Error("Otra persona cambió este producto desde que lo abriste. Recarga la página para ver la versión actual.")
          : e,
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <Page
      title={id === null ? "Nuevo producto" : p.title || "Producto"}
      sub={
        <a className="text-primary hover:underline" href={href("catalogo")}>
          ← Catálogo
        </a>
      }
    >
      <Card>
        <CardHeader>
          <CardTitle>Datos del producto</CardTitle>
          {!editable && <CardDescription>Tienes acceso de lectura al catálogo.</CardDescription>}
        </CardHeader>
        <CardContent>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void save();
            }}
            noValidate
          >
            <fieldset disabled={!editable} className="grid gap-4">
              <div className="grid gap-4 md:grid-cols-2">
                <FormField id="p-title" label="Nombre" required error={errors.title}>
                  <Input
                    value={p.title}
                    onChange={(e) => set({ title: e.target.value })}
                    onBlur={() => id === null && !p.handle && p.title && set({ handle: slugify(p.title) })}
                  />
                </FormField>
                <FormField id="p-id" label="ID" required error={errors.id} description={id === null ? "No se puede cambiar después." : undefined}>
                  <Input value={p.id} readOnly={id !== null} onChange={(e) => set({ id: e.target.value })} />
                </FormField>
              </div>
              <div className="grid gap-4 md:grid-cols-3">
                <FormField id="p-handle" label="Dirección web" required error={errors.handle} description="Aparece en la URL del producto.">
                  <Input value={p.handle} onChange={(e) => set({ handle: e.target.value })} />
                </FormField>
                <FormField id="p-brand" label="Marca">
                  <Input value={p.brand ?? ""} onChange={(e) => set({ brand: e.target.value })} />
                </FormField>
                <div className="grid gap-1.5">
                  <span className="text-sm font-medium" id="p-status-label">
                    Estado
                  </span>
                  <Select value={p.status} onValueChange={(v) => set({ status: v })} disabled={!editable}>
                    <SelectTrigger aria-labelledby="p-status-label">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="active">Activo (a la venta)</SelectItem>
                      <SelectItem value="draft">Borrador (oculto)</SelectItem>
                      <SelectItem value="archived">Archivado</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </div>
              <FormField id="p-desc" label="Descripción">
                <Textarea rows={3} value={p.description ?? ""} onChange={(e) => set({ description: e.target.value })} />
              </FormField>
              <div className="grid gap-2">
                <span className="text-sm font-medium">Variantes</span>
                {errors.variants && (
                  <p className="text-xs font-medium text-destructive" role="alert">
                    {errors.variants}
                  </p>
                )}
                {(p.variants ?? []).map((v, i) => (
                  <div key={i} className="grid items-end gap-3 rounded-lg border p-3 md:grid-cols-[1fr_1.5fr_140px_auto]">
                    <FormField id={`v-sku-${i}`} label="SKU" required>
                      <Input value={v.sku} onChange={(e) => setVariant(i, { sku: e.target.value })} />
                    </FormField>
                    <FormField id={`v-title-${i}`} label="Nombre de la variante">
                      <Input value={v.title} onChange={(e) => setVariant(i, { title: e.target.value })} />
                    </FormField>
                    <FormField id={`v-w-${i}`} label="Peso (g)">
                      <Input
                        type="number"
                        min={0}
                        value={v.weightGrams}
                        onChange={(e) => setVariant(i, { weightGrams: Math.max(0, Number(e.target.value) || 0) })}
                      />
                    </FormField>
                    {editable && (p.variants ?? []).length > 1 ? (
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        aria-label={`Quitar variante ${v.sku || i + 1}`}
                        onClick={() => set({ variants: (p.variants ?? []).filter((_, j) => j !== i) })}
                      >
                        <Trash2 />
                      </Button>
                    ) : (
                      <span />
                    )}
                  </div>
                ))}
                {editable && (
                  <div>
                    <Button type="button" variant="outline" size="sm" onClick={() => set({ variants: [...(p.variants ?? []), { sku: "", title: "", weightGrams: 0 }] })}>
                      <Plus /> Agregar variante
                    </Button>
                  </div>
                )}
              </div>
              <ErrorBox error={saveErr} />
              {editable && (
                <div>
                  <Button type="submit" disabled={busy}>
                    {busy ? "Guardando…" : "Guardar producto"}
                  </Button>
                </div>
              )}
            </fieldset>
          </form>
        </CardContent>
      </Card>

      {id !== null && (q.data?.variants ?? []).map((v) => <VariantPanel key={v.sku} me={me} variant={v} />)}
    </Page>
  );
}

interface TierForm {
  minQty: string;
  price: string;
  compareAt: string;
}

function VariantPanel({ me, variant }: { me: Me; variant: Variant }) {
  const qc = useQueryClient();
  const canPrice = can(me, "pricing", "edit");
  const canStock = can(me, "inventory", "edit");
  const price = useQuery({ queryKey: ["price", variant.sku], queryFn: () => api.currentPrice(CHANNEL, variant.sku), enabled: can(me, "catalog", "read") });
  const stock = useQuery({ queryKey: ["stock", variant.sku], queryFn: () => api.inventory(variant.sku), enabled: can(me, "inventory", "read"), retry: false });
  const [tiers, setTiers] = useState<TierForm[]>([{ minQty: "1", price: "", compareAt: "" }]);
  const [priceMsg, setPriceMsg] = useState<{ tone: "ok" | "bad"; text: string } | null>(null);
  const [loc, setLoc] = useState("");
  const [onHand, setOnHand] = useState("");
  const [stockMsg, setStockMsg] = useState<{ tone: "ok" | "bad"; text: string } | null>(null);

  useEffect(() => {
    if (price.data) {
      setTiers([{ minQty: "1", price: centsToPesos(price.data.unit.amount), compareAt: price.data.compareAt ? centsToPesos(price.data.compareAt.amount) : "" }]);
    }
  }, [price.data]);
  useEffect(() => {
    const l = stock.data?.levels[0];
    if (l) {
      setLoc(l.locationId);
      setOnHand(String(l.onHand));
    } else if (stock.error) {
      setLoc((x) => x || "principal");
    }
  }, [stock.data, stock.error]);

  const setTier = (i: number, patch: Partial<TierForm>) => setTiers(tiers.map((x, j) => (j === i ? { ...x, ...patch } : x)));

  async function savePrices() {
    setPriceMsg(null);
    const out = [];
    for (const [i, t] of tiers.entries()) {
      const min = Number(t.minQty);
      const cents = parsePesos(t.price);
      const cmp = t.compareAt.trim() ? parsePesos(t.compareAt) : null;
      if (!Number.isInteger(min) || min < 1) return setPriceMsg({ tone: "bad", text: `Fila ${i + 1}: la cantidad mínima debe ser un entero desde 1.` });
      if (cents === null) return setPriceMsg({ tone: "bad", text: `Fila ${i + 1}: escribe un precio válido, por ejemplo 1299.50.` });
      if (t.compareAt.trim() && cmp === null) return setPriceMsg({ tone: "bad", text: `Fila ${i + 1}: el precio anterior no es válido.` });
      out.push({ minQty: min, unit: { amount: cents, currency: CURRENCY }, ...(cmp !== null ? { compareAt: { amount: cmp, currency: CURRENCY } } : {}) });
    }
    if (new Set(out.map((t) => t.minQty)).size !== out.length) return setPriceMsg({ tone: "bad", text: "Hay dos filas con la misma cantidad mínima." });
    try {
      await api.setPrices(CHANNEL, variant.sku, out);
      setPriceMsg({ tone: "ok", text: "Precios guardados. Ya se ven en la tienda." });
      void qc.invalidateQueries({ queryKey: ["price", variant.sku] });
    } catch (e) {
      setPriceMsg({ tone: "bad", text: errorMessage(e) });
    }
  }

  async function saveStock() {
    setStockMsg(null);
    const n = Number(onHand);
    if (!loc.trim()) return setStockMsg({ tone: "bad", text: "Escribe la ubicación (bodega o tienda)." });
    if (!Number.isInteger(n) || n < 0) return setStockMsg({ tone: "bad", text: "Las existencias deben ser un número entero desde 0." });
    try {
      await api.setStock(variant.sku, loc.trim(), n);
      setStockMsg({ tone: "ok", text: "Existencias guardadas." });
      void qc.invalidateQueries({ queryKey: ["stock", variant.sku] });
      void qc.invalidateQueries({ queryKey: ["products"] });
    } catch (e) {
      setStockMsg({ tone: "bad", text: errorMessage(e) });
    }
  }

  return (
    <Card aria-labelledby={`v-${variant.sku}`}>
      <CardHeader>
        <CardTitle id={`v-${variant.sku}`}>
          {variant.title || "Variante"} <span className="font-mono text-xs font-normal text-muted-foreground">{variant.sku}</span>
        </CardTitle>
      </CardHeader>
      <CardContent className="grid gap-4 lg:grid-cols-2">
        <section className="grid content-start gap-3 rounded-lg border p-4" aria-label={`Precio de ${variant.sku}`}>
          <h3 className="text-sm font-semibold">Precio en el canal México (MXN)</h3>
          <p className="text-sm text-muted-foreground">
            {price.data
              ? `Hoy: ${formatMoney(price.data.unit.amount, price.data.unit.currency)}${price.data.compareAt ? ` (antes ${formatMoney(price.data.compareAt.amount, price.data.compareAt.currency)})` : ""}`
              : price.isPending
                ? "Cargando precio…"
                : "Sin precio en este canal."}
          </p>
          <p className="text-xs text-muted-foreground">Al guardar se reemplazan todos los precios por cantidad de esta variante.</p>
          {tiers.map((t, i) => (
            <div key={i} className="grid grid-cols-2 items-end gap-2 sm:grid-cols-[90px_1fr_1fr_auto]">
              <FormField id={`t-min-${variant.sku}-${i}`} label="Desde (piezas)">
                <Input inputMode="numeric" value={t.minQty} disabled={!canPrice} onChange={(e) => setTier(i, { minQty: e.target.value })} />
              </FormField>
              <FormField id={`t-price-${variant.sku}-${i}`} label="Precio ($)">
                <Input inputMode="decimal" value={t.price} disabled={!canPrice} onChange={(e) => setTier(i, { price: e.target.value })} />
              </FormField>
              <FormField id={`t-cmp-${variant.sku}-${i}`} label="Precio anterior ($)">
                <Input inputMode="decimal" value={t.compareAt} disabled={!canPrice} onChange={(e) => setTier(i, { compareAt: e.target.value })} />
              </FormField>
              {canPrice && tiers.length > 1 ? (
                <Button variant="ghost" size="icon" aria-label={`Quitar fila ${i + 1}`} onClick={() => setTiers(tiers.filter((_, j) => j !== i))}>
                  <Trash2 />
                </Button>
              ) : (
                <span />
              )}
            </div>
          ))}
          {canPrice ? (
            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => setTiers([...tiers, { minQty: String(Number(tiers.at(-1)?.minQty ?? 1) * 10 || 10), price: "", compareAt: "" }])}
              >
                <Plus /> Precio por volumen
              </Button>
              <Button size="sm" onClick={() => void savePrices()}>
                Guardar precios
              </Button>
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">Tu acceso no permite cambiar precios.</p>
          )}
          {priceMsg && <Notice tone={priceMsg.tone}>{priceMsg.text}</Notice>}
        </section>

        <section className="grid content-start gap-3 rounded-lg border p-4" aria-label={`Existencias de ${variant.sku}`}>
          <h3 className="text-sm font-semibold">Existencias</h3>
          {stock.data ? (
            <>
              <p className="text-sm text-muted-foreground">Disponible para vender: {stock.data.available}</p>
              <ul className="grid gap-1 text-sm">
                {stock.data.levels.map((l) => (
                  <li key={l.locationId}>
                    <strong>{l.locationId}</strong>: {l.onHand} en existencia, {l.reserved} apartadas en carritos
                  </li>
                ))}
              </ul>
            </>
          ) : (
            <p className="text-sm text-muted-foreground">
              {stock.isPending && can(me, "inventory", "read") ? "Cargando…" : can(me, "inventory", "read") ? "Sin registros de inventario." : "Sin acceso al inventario."}
            </p>
          )}
          {canStock ? (
            <>
              <div className="grid grid-cols-2 gap-2">
                <FormField id={`s-loc-${variant.sku}`} label="Ubicación">
                  <Input value={loc} onChange={(e) => setLoc(e.target.value)} />
                </FormField>
                <FormField id={`s-qty-${variant.sku}`} label="Existencias">
                  <Input inputMode="numeric" value={onHand} onChange={(e) => setOnHand(e.target.value)} />
                </FormField>
              </div>
              <div>
                <Button size="sm" onClick={() => void saveStock()}>
                  Guardar existencias
                </Button>
              </div>
            </>
          ) : (
            <p className="text-xs text-muted-foreground">Tu acceso no permite cambiar existencias.</p>
          )}
          {stockMsg && <Notice tone={stockMsg.tone}>{stockMsg.text}</Notice>}
        </section>
      </CardContent>
    </Card>
  );
}
