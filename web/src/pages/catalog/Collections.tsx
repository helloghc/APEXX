import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { ArrowLeft, LayoutGrid, Plus, Save, Trash2, X } from "lucide-react";
import { errorMessage, fieldErrors, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { centsToPesos, formatMoney, parsePesos } from "@/money";
import { can } from "@/perms";
import { href, navigate } from "@/router";
import { ErrorBox, StatusPill } from "../common";
import { cat, type Collection, type CollectionProduct, type RuleSet } from "./api";
import { ProductPicker } from "./ProductPicker";
import { CatalogPage, Confirm, EmptyState, Field, MultiPick, Skeleton, SkeletonRows, slug, TagInput, Thumb, useDebounced } from "./shared";

export function Collections({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "catalog", "edit");
  const list = useQuery({ queryKey: ["collections"], queryFn: cat.collections });
  const [del, setDel] = useState<Collection | null>(null);
  const items = list.data ?? [];
  return (
    <CatalogPage tab="colecciones" title="Colecciones" sub="Agrupa productos a mano o con reglas que se actualizan solas, por ejemplo “Tenis de menos de $1,000”."
      actions={canEdit && <Button asChild><a href={href("catalogo", "colecciones", "nueva")}><Plus aria-hidden="true" /> Nueva colección</a></Button>}>
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      {!list.isLoading && items.length === 0 ? (
        <EmptyState icon={LayoutGrid} title="Aún no hay colecciones" text="Crea una colección para destacar productos en la página de inicio o en una campaña."
          action={canEdit && <Button asChild><a href={href("catalogo", "colecciones", "nueva")}><Plus aria-hidden="true" /> Crear colección</a></Button>} />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Colección</TableHead>
                <TableHead>Tipo</TableHead>
                <TableHead className="text-right">Productos</TableHead>
                <TableHead>Estado</TableHead>
                <TableHead className="w-16"><span className="sr-only">Acciones</span></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.isLoading ? <SkeletonRows cols={5} /> : items.map((c) => (
                <TableRow key={c.id}>
                  <TableCell>
                    <a className="font-medium hover:text-primary hover:underline" href={href("catalogo", "colecciones", c.id)}>{c.title}</a>
                    <div className="font-mono text-xs text-muted-foreground">/col/{c.handle}</div>
                  </TableCell>
                  <TableCell>{c.kind === "rules" ? "Automática" : "Manual"}</TableCell>
                  <TableCell className="text-right tabular-nums">{c.products ?? 0}</TableCell>
                  <TableCell>{c.active ? <Badge variant="secondary">Visible</Badge> : <Badge variant="outline">Oculta</Badge>}</TableCell>
                  <TableCell>
                    {canEdit && <Button variant="ghost" size="icon" aria-label={`Eliminar ${c.title}`} onClick={() => setDel(c)}><Trash2 aria-hidden="true" /></Button>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <Confirm open={!!del} title={`¿Eliminar “${del?.title}”?`} description="Los productos no se borran; solo desaparece la colección." confirmLabel="Eliminar" destructive
        onConfirm={() => { if (!del) return; cat.deleteCollection(del.id).then(() => { toast.success("Colección eliminada."); void qc.invalidateQueries({ queryKey: ["collections"] }); }).catch((e) => toast.error(errorMessage(e))).finally(() => setDel(null)); }}
        onClose={() => setDel(null)} />
    </CatalogPage>
  );
}

const EMPTY_RULES: RuleSet = { match: "all", brandIds: [], categoryIds: [], tags: [], priceMin: null, priceMax: null };

export function CollectionEditor({ me, id }: { me: Me; id: string }) {
  const isNew = id === "nueva";
  const qc = useQueryClient();
  const canEdit = can(me, "catalog", "edit");
  const detail = useQuery({ queryKey: ["collection", id], queryFn: () => cat.collection(id), enabled: !isNew });
  const categories = useQuery({ queryKey: ["categories"], queryFn: cat.categories });
  const brands = useQuery({ queryKey: ["brands"], queryFn: cat.brands });
  const tagTerms = useQuery({ queryKey: ["terms", "tags"], queryFn: () => cat.terms("tags") });

  const [c, setC] = useState<Partial<Collection>>({ kind: "manual", active: true, productIds: [], rules: EMPTY_RULES, title: "", handle: "" });
  const [manual, setManual] = useState<CollectionProduct[]>([]);
  const [priceMin, setPriceMin] = useState("");
  const [priceMax, setPriceMax] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const [picking, setPicking] = useState(false);

  useEffect(() => {
    if (!detail.data) return;
    const col = detail.data.collection;
    setC({ ...col, rules: { ...EMPTY_RULES, ...col.rules } });
    if (col.kind === "manual") setManual(detail.data.products);
    setPriceMin(col.rules?.priceMin != null ? centsToPesos(col.rules.priceMin) : "");
    setPriceMax(col.rules?.priceMax != null ? centsToPesos(col.rules.priceMax) : "");
  }, [detail.data]);

  const rules: RuleSet = { ...EMPTY_RULES, ...c.rules, priceMin: priceMin ? parsePesos(priceMin) : null, priceMax: priceMax ? parsePesos(priceMax) : null };
  const rulesKey = useDebounced(JSON.stringify(rules), 400);
  const preview = useQuery({
    queryKey: ["collection-preview", rulesKey],
    queryFn: () => cat.previewCollection({ kind: "rules", rules: JSON.parse(rulesKey) as RuleSet }),
    enabled: c.kind === "rules",
  });
  const hasRule = !!(rules.brandIds?.length || rules.categoryIds?.length || rules.tags?.length || rules.priceMin != null || rules.priceMax != null);

  async function save() {
    setErrors({});
    setFormError(null);
    if (!c.title?.trim()) {
      setErrors({ title: "Escribe el nombre de la colección." });
      return;
    }
    if (c.kind === "rules" && !hasRule) {
      setErrors({ rules: "Agrega al menos una regla." });
      return;
    }
    const body: Partial<Collection> = {
      ...c, title: c.title.trim(), handle: c.handle || slug(c.title),
      productIds: c.kind === "manual" ? manual.map((p) => p.id) : [], rules: c.kind === "rules" ? rules : EMPTY_RULES,
    };
    setSaving(true);
    try {
      const r = isNew ? await cat.createCollection(body) : await cat.updateCollection(id, body);
      toast.success(isNew ? "Colección creada." : "Colección guardada.");
      void qc.invalidateQueries({ queryKey: ["collections"] });
      if (isNew) navigate("catalogo", "colecciones", r.collection.id);
      else void qc.invalidateQueries({ queryKey: ["collection", id] });
    } catch (e) {
      setErrors(fieldErrors(e));
      setFormError(e);
    } finally {
      setSaving(false);
    }
  }

  if (!isNew && detail.isLoading) return <main className="mx-auto grid w-full max-w-5xl gap-4 px-4 py-6"><Skeleton className="h-8 w-64" /><Skeleton className="h-72 w-full" /></main>;
  if (!isNew && detail.error) return <main className="mx-auto w-full max-w-3xl px-4 py-6"><ErrorBox error={detail.error} onRetry={() => void detail.refetch()} /></main>;

  const shown = c.kind === "rules" ? (preview.data?.products ?? []) : manual;
  const total = c.kind === "rules" ? (preview.data?.total ?? 0) : manual.length;
  const setRule = (patch: Partial<RuleSet>) => setC({ ...c, rules: { ...EMPTY_RULES, ...c.rules, ...patch } });

  return (
    <main className="mx-auto grid w-full max-w-5xl content-start gap-5 px-4 py-6 md:px-8">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="grid gap-1">
          <a href={href("catalogo", "colecciones")} className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"><ArrowLeft className="size-4" aria-hidden="true" /> Colecciones</a>
          <h1 className="text-2xl font-bold tracking-tight">{isNew ? "Nueva colección" : c.title}</h1>
        </div>
        {canEdit && <Button onClick={() => void save()} disabled={saving}><Save aria-hidden="true" /> {saving ? "Guardando…" : "Guardar"}</Button>}
      </div>
      <ErrorBox error={formError} />
      <fieldset disabled={!canEdit} className="grid gap-5">
        <Card>
          <CardContent className="grid gap-4 pt-6 sm:grid-cols-2">
            <Field label="Nombre" htmlFor="col-title" error={errors.title}>
              <Input id="col-title" maxLength={120} value={c.title ?? ""} onChange={(e) => setC({ ...c, title: e.target.value })} />
            </Field>
            <Field label="Dirección web" htmlFor="col-handle" error={errors.handle} hint={`/col/${c.handle || slug(c.title ?? "") || "…"}`}>
              <Input id="col-handle" className="font-mono text-sm" value={c.handle ?? ""} placeholder={slug(c.title ?? "")} onChange={(e) => setC({ ...c, handle: slug(e.target.value) })} />
            </Field>
            <Field label="Descripción" htmlFor="col-desc" className="sm:col-span-2">
              <Textarea id="col-desc" rows={2} value={c.description ?? ""} onChange={(e) => setC({ ...c, description: e.target.value })} />
            </Field>
            <Field label="Tipo" htmlFor="col-kind" hint={c.kind === "rules" ? "Los productos entran y salen solos según las reglas." : "Tú eliges cada producto y su orden."}>
              <Select value={c.kind} onValueChange={(v) => setC({ ...c, kind: v as Collection["kind"] })} disabled={!isNew}>
                <SelectTrigger id="col-kind"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="manual">Manual</SelectItem>
                  <SelectItem value="rules">Automática por reglas</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <label className="flex items-center gap-2 self-end pb-2 text-sm">
              <Checkbox checked={c.active ?? true} onCheckedChange={(v) => setC({ ...c, active: !!v })} /> Visible en la tienda
            </label>
          </CardContent>
        </Card>

        {c.kind === "rules" && (
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Reglas</CardTitle>
              <CardDescription>
                Un producto entra si cumple{" "}
                <select aria-label="Cómo combinar las reglas" className="rounded border bg-background px-1 py-0.5 text-sm" value={rules.match} onChange={(e) => setRule({ match: e.target.value as RuleSet["match"] })}>
                  <option value="all">todas</option>
                  <option value="any">cualquiera de</option>
                </select>{" "}
                las reglas.
              </CardDescription>
            </CardHeader>
            <CardContent className="grid gap-4 sm:grid-cols-2">
              <Field label="Marcas" htmlFor="r-brands">
                <MultiPick id="r-brands" label="Cualquier marca" options={(brands.data ?? []).map((b) => ({ id: b.id, label: b.name }))} value={rules.brandIds ?? []} onChange={(v) => setRule({ brandIds: v })} empty="No hay marcas." />
              </Field>
              <Field label="Categorías" htmlFor="r-cats" hint="Incluye sus subcategorías.">
                <MultiPick id="r-cats" label="Cualquier categoría" options={(categories.data ?? []).map((x) => ({ id: x.id, label: x.name, depth: x.depth }))} value={rules.categoryIds ?? []} onChange={(v) => setRule({ categoryIds: v })} empty="No hay categorías." />
              </Field>
              <Field label="Etiquetas" htmlFor="r-tags" className="sm:col-span-2">
                <TagInput id="r-tags" value={rules.tags ?? []} onChange={(v) => setRule({ tags: v })} suggestions={(tagTerms.data ?? []).map((t) => t.name)} placeholder="oferta, verano…" />
              </Field>
              <Field label="Precio desde" htmlFor="r-min"><Input id="r-min" inputMode="decimal" placeholder="0.00" value={priceMin} onChange={(e) => setPriceMin(e.target.value)} /></Field>
              <Field label="Precio hasta" htmlFor="r-max"><Input id="r-max" inputMode="decimal" placeholder="Sin límite" value={priceMax} onChange={(e) => setPriceMax(e.target.value)} /></Field>
              {errors.rules && <p className="text-sm font-medium text-destructive sm:col-span-2" role="alert">{errors.rules}</p>}
            </CardContent>
          </Card>
        )}

        <Card>
          <CardHeader className="flex-row items-center justify-between space-y-0">
            <div className="grid gap-1">
              <CardTitle className="text-base">Productos ({total})</CardTitle>
              <CardDescription>{c.kind === "rules" ? "Vista previa en vivo de lo que cumple las reglas." : "Aparecen en este orden."}</CardDescription>
            </div>
            {c.kind === "manual" && <Button type="button" variant="outline" size="sm" onClick={() => setPicking(true)}><Plus aria-hidden="true" /> Agregar productos</Button>}
          </CardHeader>
          <CardContent>
            {c.kind === "rules" && !hasRule ? (
              <p className="text-sm text-muted-foreground">Agrega una regla para ver los productos.</p>
            ) : shown.length === 0 ? (
              <p className="text-sm text-muted-foreground">{preview.isFetching ? "Calculando…" : "Ningún producto todavía."}</p>
            ) : (
              <ul className="divide-y">
                {shown.map((p) => (
                  <li key={p.id} className="flex items-center gap-3 py-2">
                    <Thumb media={p.thumb} size={40} />
                    <div className="min-w-0 flex-1">
                      <a className="block truncate text-sm font-medium hover:underline" href={href("catalogo", "productos", p.id)}>{p.title}</a>
                      <span className="text-xs text-muted-foreground">{p.brand || "Sin marca"}{p.priceMin != null ? ` · ${formatMoney(p.priceMin, p.currency)}` : ""}</span>
                    </div>
                    <StatusPill status={p.status} />
                    {c.kind === "manual" && (
                      <Button type="button" variant="ghost" size="icon" aria-label={`Quitar ${p.title}`} onClick={() => setManual(manual.filter((x) => x.id !== p.id))}><X aria-hidden="true" /></Button>
                    )}
                  </li>
                ))}
              </ul>
            )}
            {c.kind === "rules" && total > shown.length && <p className="pt-2 text-xs text-muted-foreground">Se muestran los primeros {shown.length} de {total}.</p>}
          </CardContent>
        </Card>
      </fieldset>
      <ProductPicker open={picking} title="Agregar productos a la colección" exclude={manual.map((p) => p.id)} onClose={() => setPicking(false)}
        onPick={(ps) => { setManual([...manual, ...ps]); setPicking(false); }} />
    </main>
  );
}
