import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { ArrowDownRight, ArrowUpRight, History, ListOrdered, Pencil, Plus, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { centsToPesos, formatMoney, parsePesos } from "@/money";
import { can } from "@/perms";
import { promoApi, type BulkChange, type BulkResult, type PriceList, type PriceListInput } from "@/promoApi";
import { ErrorBox, Notice } from "../common";
import { bpsToPercent, parsePercent, storeDateTime, windowText } from "./lib";
import { ChipInput, DateTimeField, EmptyState, Field, SkeletonRows, StatusBadge } from "./shared";

// ---------- price lists ----------

export function PriceLists({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "pricing", "edit");
  const list = useQuery({ queryKey: ["price-lists"], queryFn: promoApi.priceLists });
  const [edit, setEdit] = useState<string | null>(null);
  const items = list.data ?? [];
  const refresh = () => void qc.invalidateQueries({ queryKey: ["price-lists"] });
  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="max-w-2xl text-sm text-muted-foreground">Una lista cambia los precios de un canal durante un periodo, sin tocar el precio base. Al terminar, los precios vuelven solos.</p>
        {canEdit && <Button onClick={() => setEdit("new")}><Plus aria-hidden="true" /> Nueva lista de precios</Button>}
      </div>
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      {!list.isLoading && items.length === 0 ? (
        <EmptyState icon={<ListOrdered />} title="Aún no hay listas de precios" text="Úsalas para rebajas programadas, por ejemplo −15% en todo el canal durante el Buen Fin, o precios fijos para algunos productos." />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader>
              <TableRow><TableHead>Lista</TableHead><TableHead>Qué hace</TableHead><TableHead>Estado</TableHead><TableHead>Vigencia</TableHead><TableHead className="text-right">Prioridad</TableHead><TableHead className="w-24"><span className="sr-only">Acciones</span></TableHead></TableRow>
            </TableHeader>
            <TableBody>
              {list.isLoading ? <SkeletonRows cols={6} /> : items.map((l) => (
                <TableRow key={l.id}>
                  <TableCell className="font-medium">{l.name}<div className="text-xs font-normal text-muted-foreground">Canal {l.channel}</div></TableCell>
                  <TableCell className="max-w-80 text-muted-foreground">{l.summary}</TableCell>
                  <TableCell><StatusBadge status={l.displayStatus} /></TableCell>
                  <TableCell className="text-muted-foreground">{windowText(l.startsAt, l.endsAt)}</TableCell>
                  <TableCell className="text-right tabular-nums">{l.priority}</TableCell>
                  <TableCell>
                    <div className="flex justify-end">
                      <Button variant="ghost" size="icon" aria-label={`Editar ${l.name}`} onClick={() => setEdit(l.id)}><Pencil aria-hidden="true" /></Button>
                      {can(me, "pricing", "admin") && (
                        <Button variant="ghost" size="icon" aria-label={`Eliminar ${l.name}`}
                          onClick={() => void promoApi.removePriceList(l.id).then(() => { toast.success("Lista eliminada. Los precios volvieron a su valor base."); refresh(); }).catch((e) => toast.error(errorMessage(e)))}>
                          <Trash2 aria-hidden="true" />
                        </Button>
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {edit && <PriceListDialog id={edit} canEdit={canEdit} onClose={() => setEdit(null)} onSaved={refresh} />}
    </div>
  );
}

function blankList(): PriceListInput {
  return { name: "", status: "active", channel: "mx", customerGroup: "", kind: "adjust", adjustBps: -1000, appliesTo: "all", priority: 0, items: [] };
}

function PriceListDialog({ id, canEdit, onClose, onSaved }: { id: string; canEdit: boolean; onClose: () => void; onSaved: () => void }) {
  const isNew = id === "new";
  const detail = useQuery({ queryKey: ["price-list", id], queryFn: () => promoApi.priceList(id), enabled: !isNew });
  const [l, setL] = useState<PriceListInput>(blankList);
  const [percent, setPercent] = useState("-10");
  const [rows, setRows] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!detail.data) return;
    const d = detail.data.list;
    setL({ id: d.id, name: d.name, description: d.description, status: d.status, channel: d.channel, customerGroup: d.customerGroup, kind: d.kind, adjustBps: d.adjustBps, appliesTo: d.appliesTo, priority: d.priority, startsAt: d.startsAt, endsAt: d.endsAt, items: [] });
    setPercent(bpsToPercent(d.adjustBps));
    setRows(detail.data.items.map((i) => (d.kind === "override" ? `${i.sku}, ${i.amount != null ? centsToPesos(i.amount) : ""}` : i.sku)).join("\n"));
  }, [detail.data]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const errs: Record<string, string> = {};
    const items: PriceListInput["items"] = [];
    rows.split("\n").map((r) => r.trim()).filter(Boolean).forEach((r, i) => {
      const [sku = "", price = ""] = r.split(/[,\t;]/).map((x) => x.trim());
      if (l.kind === "override") {
        const amount = parsePesos(price);
        if (!sku || amount == null) errs.items = `Línea ${i + 1}: escribe “SKU, precio”.`;
        else items.push({ sku, amount });
      } else if (sku) items.push({ sku });
    });
    let adjustBps = 0;
    if (l.kind === "adjust") {
      const b = parsePercent(percent.replace("-", ""));
      if (b == null || b === 0) errs.adjustBps = "Escribe un porcentaje, por ejemplo -15.";
      else adjustBps = percent.trim().startsWith("-") ? -b : b;
    }
    if (!l.name.trim()) errs.name = "Escribe un nombre.";
    if ((l.kind === "override" || l.appliesTo === "items") && items.length === 0 && !errs.items) errs.items = "Agrega al menos un SKU.";
    setErrors(errs);
    if (Object.keys(errs).length) return;
    setBusy(true);
    try {
      const body = { ...l, name: l.name.trim(), adjustBps, items, appliesTo: l.kind === "override" ? "items" as const : l.appliesTo };
      if (isNew) await promoApi.createPriceList(body);
      else await promoApi.savePriceList(id, body);
      toast.success("Lista guardada. Los precios ya se actualizaron en la tienda.");
      onSaved();
      onClose();
    } catch (err) {
      const fe = fieldErrors(err);
      setErrors(Object.keys(fe).length ? fe : { form: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-2xl overflow-y-auto">
        <form onSubmit={(e) => void submit(e)} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{isNew ? "Nueva lista de precios" : "Editar lista de precios"}</DialogTitle>
            <DialogDescription>No modifica el precio base: se aplica encima mientras esté vigente.</DialogDescription>
          </DialogHeader>
          <fieldset disabled={!canEdit} className="grid gap-4">
            <Field label="Nombre" error={errors.name}><Input aria-label="Nombre" autoFocus maxLength={80} value={l.name} onChange={(e) => setL({ ...l, name: e.target.value })} placeholder="Buen Fin 2026" /></Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Tipo">
                <Select value={l.kind} onValueChange={(v) => setL({ ...l, kind: v as PriceListInput["kind"] })}>
                  <SelectTrigger aria-label="Tipo"><SelectValue /></SelectTrigger>
                  <SelectContent><SelectItem value="adjust">Ajuste en porcentaje</SelectItem><SelectItem value="override">Precios fijos por SKU</SelectItem></SelectContent>
                </Select>
              </Field>
              {l.kind === "adjust" ? (
                <Field label="Porcentaje" hint="Negativo baja el precio: -15 = 15% menos." error={errors.adjustBps}><Input aria-label="Porcentaje" inputMode="decimal" value={percent} onChange={(e) => setPercent(e.target.value)} /></Field>
              ) : <span />}
              {l.kind === "adjust" && (
                <Field label="Aplica a">
                  <Select value={l.appliesTo} onValueChange={(v) => setL({ ...l, appliesTo: v as PriceListInput["appliesTo"] })}>
                    <SelectTrigger aria-label="Aplica a"><SelectValue /></SelectTrigger>
                    <SelectContent><SelectItem value="all">Todos los productos del canal</SelectItem><SelectItem value="items">Solo algunos SKUs</SelectItem></SelectContent>
                  </Select>
                </Field>
              )}
              <Field label="Estado">
                <Select value={l.status} onValueChange={(v) => setL({ ...l, status: v as PriceListInput["status"] })}>
                  <SelectTrigger aria-label="Estado"><SelectValue /></SelectTrigger>
                  <SelectContent><SelectItem value="active">Activa</SelectItem><SelectItem value="paused">Pausada</SelectItem></SelectContent>
                </Select>
              </Field>
              <Field label="Empieza" hint="Opcional"><DateTimeField value={l.startsAt} onChange={(v) => setL({ ...l, startsAt: v })} /></Field>
              <Field label="Termina" hint="Opcional" error={errors.endsAt}><DateTimeField value={l.endsAt} onChange={(v) => setL({ ...l, endsAt: v })} /></Field>
              <Field label="Prioridad" hint="Si dos listas coinciden, gana la mayor."><Input aria-label="Prioridad" inputMode="numeric" value={String(l.priority)} onChange={(e) => setL({ ...l, priority: Number(e.target.value.replace(/[^\d-]/g, "")) || 0 })} /></Field>
            </div>
            {(l.kind === "override" || l.appliesTo === "items") && (
              <Field label={l.kind === "override" ? "SKUs y precios" : "SKUs"} hint={l.kind === "override" ? "Una línea por SKU: SKU, precio. Puedes pegar desde una hoja de cálculo." : "Un SKU por línea."} error={errors.items}>
                <Textarea aria-label="SKUs" rows={6} className="font-mono text-sm" value={rows} onChange={(e) => setRows(e.target.value)} placeholder={l.kind === "override" ? "SKU-000001-1, 999.00" : "SKU-000001-1"} />
              </Field>
            )}
          </fieldset>
          {errors.form && <p className="text-sm font-medium text-destructive" role="alert">{errors.form}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>Cancelar</Button>
            {canEdit && <Button type="submit" disabled={busy}>{busy ? "Guardando…" : "Guardar"}</Button>}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// ---------- bulk price change ----------

export function BulkPrices({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canApply = can(me, "pricing", "admin");
  const facets = useQuery({ queryKey: ["promo-facets"], queryFn: promoApi.facets });
  const [mode, setMode] = useState<BulkChange["mode"]>("percent");
  const [value, setValue] = useState("-10");
  const [rounding, setRounding] = useState<BulkChange["rounding"]>("none");
  const [compareAt, setCompareAt] = useState<BulkChange["compareAt"]>("previous");
  const [scope, setScope] = useState<"all" | "brands" | "categories" | "skus">("all");
  const [brands, setBrands] = useState<string[]>([]);
  const [categories, setCategories] = useState<string[]>([]);
  const [skus, setSkus] = useState("");
  const [result, setResult] = useState<BulkResult | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  function change(): BulkChange | null {
    const neg = value.trim().startsWith("-");
    const raw = value.replace("-", "").trim();
    const n = mode === "percent" ? parsePercent(raw) : parsePesos(raw);
    if (n == null || (n === 0 && mode !== "set")) {
      setError(new Error(mode === "percent" ? "Escribe un porcentaje, por ejemplo -10." : "Escribe un monto válido."));
      return null;
    }
    return {
      channel: "mx", mode, value: neg && mode !== "set" ? -n : n, rounding, compareAt, tiers: "all",
      scope: scope === "all" ? { all: true } : scope === "brands" ? { brands } : scope === "categories" ? { categories } : { skus: skus.split(/[\s,;]+/).filter(Boolean) },
    };
  }
  async function preview() {
    const c = change();
    if (!c) return;
    setBusy(true);
    setError(null);
    try {
      setResult(await promoApi.bulkPreview(c));
    } catch (e) {
      setError(e);
      setResult(null);
    } finally {
      setBusy(false);
    }
  }
  async function apply() {
    const c = change();
    if (!c || !result) return;
    setBusy(true);
    try {
      const r = await promoApi.bulkApply({ ...c, hash: result.hash });
      setResult(r);
      toast.success(`${r.changed} precios actualizados. Ya se ven en la tienda.`);
      void qc.invalidateQueries({ queryKey: ["price-history"] });
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }
  const dirty = () => setResult(null);
  return (
    <div className="grid gap-4">
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Cambio masivo de precios</CardTitle>
          <CardDescription>Cambia el precio base de muchos productos a la vez. Primero ves la vista previa; nada cambia hasta que confirmes.</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 md:grid-cols-4">
          <Field label="Cambio">
            <Select value={mode} onValueChange={(v) => { setMode(v as BulkChange["mode"]); dirty(); }}>
              <SelectTrigger aria-label="Cambio"><SelectValue /></SelectTrigger>
              <SelectContent><SelectItem value="percent">Porcentaje</SelectItem><SelectItem value="fixed">Sumar o restar monto</SelectItem><SelectItem value="set">Fijar precio</SelectItem></SelectContent>
            </Select>
          </Field>
          <Field label={mode === "percent" ? "Porcentaje" : "Monto (MXN)"} hint={mode === "set" ? undefined : "Negativo para bajar."}>
            <Input aria-label="Valor" inputMode="decimal" value={value} onChange={(e) => { setValue(e.target.value); dirty(); }} />
          </Field>
          <Field label="Redondeo">
            <Select value={rounding} onValueChange={(v) => { setRounding(v as BulkChange["rounding"]); dirty(); }}>
              <SelectTrigger aria-label="Redondeo"><SelectValue /></SelectTrigger>
              <SelectContent><SelectItem value="none">Sin redondeo</SelectItem><SelectItem value="peso">Al peso</SelectItem><SelectItem value="ten">A la decena</SelectItem><SelectItem value="ninety">Terminar en .90</SelectItem></SelectContent>
            </Select>
          </Field>
          <Field label="Precio tachado">
            <Select value={compareAt} onValueChange={(v) => { setCompareAt(v as BulkChange["compareAt"]); dirty(); }}>
              <SelectTrigger aria-label="Precio tachado"><SelectValue /></SelectTrigger>
              <SelectContent><SelectItem value="previous">Usar el precio anterior</SelectItem><SelectItem value="keep">Dejar como está</SelectItem><SelectItem value="clear">Quitar</SelectItem></SelectContent>
            </Select>
          </Field>
          <Field label="Productos" className="md:col-span-1">
            <Select value={scope} onValueChange={(v) => { setScope(v as typeof scope); dirty(); }}>
              <SelectTrigger aria-label="Productos"><SelectValue /></SelectTrigger>
              <SelectContent><SelectItem value="all">Todo el catálogo</SelectItem><SelectItem value="brands">Por marca</SelectItem><SelectItem value="categories">Por categoría</SelectItem><SelectItem value="skus">Lista de SKUs</SelectItem></SelectContent>
            </Select>
          </Field>
          {scope === "brands" && <Field label="Marcas" className="md:col-span-3"><ChipInput values={brands} onChange={(v) => { setBrands(v); dirty(); }} suggestions={(facets.data?.brands ?? []).map((b) => b.value)} placeholder="Escribe una marca y Enter" /></Field>}
          {scope === "categories" && <Field label="Categorías" className="md:col-span-3"><ChipInput values={categories} onChange={(v) => { setCategories(v); dirty(); }} suggestions={(facets.data?.categories ?? []).map((b) => b.value)} placeholder="Escribe una categoría y Enter" /></Field>}
          {scope === "skus" && <Field label="SKUs" className="md:col-span-3"><Textarea aria-label="SKUs" rows={3} className="font-mono text-sm" value={skus} onChange={(e) => { setSkus(e.target.value); dirty(); }} placeholder="Separados por coma, espacio o salto de línea" /></Field>}
          <div className="md:col-span-4"><Button onClick={() => void preview()} disabled={busy}>{busy && !result ? "Calculando…" : "Ver vista previa"}</Button></div>
        </CardContent>
      </Card>
      <ErrorBox error={error} />
      {result && (
        <Card>
          <CardHeader className="flex-row flex-wrap items-center justify-between gap-3 space-y-0">
            <div className="grid gap-1">
              <CardTitle className="text-base">{result.applied ? "Cambio aplicado" : "Vista previa"}</CardTitle>
              <CardDescription>
                {result.changed} precios cambian en {result.skus} SKUs · {result.increases} suben, {result.decreases} bajan{result.unchanged ? ` · ${result.unchanged} quedan igual` : ""}{result.noPrice ? ` · ${result.noPrice} sin precio` : ""}
              </CardDescription>
            </div>
            {!result.applied && (canApply
              ? <Button onClick={() => void apply()} disabled={busy || result.changed === 0}>{busy ? "Aplicando…" : `Aplicar a ${result.changed} precios`}</Button>
              : <Notice tone="warn">Aplicar cambios masivos requiere acceso de administrador en precios.</Notice>)}
          </CardHeader>
          <CardContent>
            {result.applied && <Notice tone="ok" className="mb-3">Listo. Cada cambio quedó en el historial y puedes revertirlo con un cambio inverso.</Notice>}
            <div className="max-h-96 overflow-auto rounded-md border">
              <Table>
                <TableHeader><TableRow><TableHead>Producto</TableHead><TableHead className="text-right">Antes</TableHead><TableHead className="text-right">Después</TableHead><TableHead className="text-right">Diferencia</TableHead><TableHead className="text-right">Tachado</TableHead></TableRow></TableHeader>
                <TableBody>
                  {result.rows.map((r) => {
                    const diff = r.new - r.old;
                    return (
                      <TableRow key={r.sku + r.minQty}>
                        <TableCell><div className="font-medium">{r.title ?? r.sku}</div><div className="font-mono text-xs text-muted-foreground">{r.sku}{r.minQty > 1 ? ` · desde ${r.minQty} pzas` : ""}</div></TableCell>
                        <TableCell className="text-right tabular-nums text-muted-foreground">{formatMoney(r.old, r.currency)}</TableCell>
                        <TableCell className="text-right font-medium tabular-nums">{formatMoney(r.new, r.currency)}</TableCell>
                        <TableCell className={`text-right tabular-nums ${diff < 0 ? "text-ok" : diff > 0 ? "text-warn" : ""}`}>
                          {diff < 0 ? <ArrowDownRight className="inline size-4" aria-hidden="true" /> : diff > 0 ? <ArrowUpRight className="inline size-4" aria-hidden="true" /> : null}
                          {r.old ? ` ${((diff / r.old) * 100).toFixed(1)}%` : ""}
                        </TableCell>
                        <TableCell className="text-right tabular-nums text-muted-foreground">{r.newCompareAt ? formatMoney(r.newCompareAt, r.currency) : "—"}</TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
            {result.truncated && <p className="pt-2 text-xs text-muted-foreground">Se muestran las primeras {result.rows.length} filas; el cambio aplica a todas.</p>}
          </CardContent>
        </Card>
      )}
    </div>
  );
}

// ---------- price history ----------

const SOURCE: Record<string, string> = { manual: "Edición manual", bulk: "Cambio masivo", import: "Importación", api: "API", seed: "Carga inicial", product: "Editor de producto" };

export function PriceHistory() {
  const [sku, setSku] = useState("");
  const [applied, setApplied] = useState("");
  const h = useQuery({ queryKey: ["price-history", applied], queryFn: () => promoApi.history({ sku: applied, limit: 100 }) });
  const items = h.data?.items ?? [];
  return (
    <div className="grid gap-4">
      <form className="flex flex-wrap items-end gap-2" onSubmit={(e) => { e.preventDefault(); setApplied(sku.trim()); }}>
        <Field label="SKU" className="min-w-64"><Input aria-label="SKU" className="font-mono" value={sku} onChange={(e) => setSku(e.target.value)} placeholder="Todos los SKUs" /></Field>
        <Button type="submit" variant="outline">Buscar</Button>
      </form>
      {applied && h.data?.current && (
        <Notice tone="ok">
          <strong>{h.data.title ?? applied}</strong>: precio vigente {formatMoney(h.data.current.unit.amount, h.data.current.unit.currency)}
          {h.data.current.priceListName ? ` por la lista “${h.data.current.priceListName}”` : ""}.
        </Notice>
      )}
      <ErrorBox error={h.error} onRetry={() => void h.refetch()} />
      {!h.isLoading && items.length === 0 ? (
        <EmptyState icon={<History />} title="Sin cambios registrados" text="Cada cambio de precio queda aquí con quién lo hizo, cuándo y desde dónde." />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader><TableRow><TableHead>Cuándo</TableHead><TableHead>Producto</TableHead><TableHead className="text-right">Antes</TableHead><TableHead className="text-right">Después</TableHead><TableHead>Origen</TableHead><TableHead>Quién</TableHead></TableRow></TableHeader>
            <TableBody>
              {h.isLoading ? <SkeletonRows cols={6} /> : items.map((c) => (
                <TableRow key={c.id}>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{storeDateTime(c.at)}</TableCell>
                  <TableCell><div className="font-medium">{c.title || c.sku}</div><div className="font-mono text-xs text-muted-foreground">{c.sku}{c.minQty > 1 ? ` · desde ${c.minQty}` : ""}</div></TableCell>
                  <TableCell className="text-right tabular-nums text-muted-foreground">{c.old != null ? formatMoney(c.old, c.currency) : "—"}</TableCell>
                  <TableCell className="text-right font-medium tabular-nums">{c.new != null ? formatMoney(c.new, c.currency) : "Sin precio"}</TableCell>
                  <TableCell>{SOURCE[c.source] ?? c.source}</TableCell>
                  <TableCell className="text-muted-foreground">{c.actor}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}

export type { PriceList };
