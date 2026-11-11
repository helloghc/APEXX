import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { ArrowLeft, ArrowRight, ExternalLink, ImagePlus, Plus, Save, Sparkles, Trash2, X } from "lucide-react";
import { ApiError, errorMessage, fieldErrors, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { centsToPesos, parsePesos } from "@/money";
import { can } from "@/perms";
import { href, navigate } from "@/router";
import { ErrorBox, Notice, StatusPill } from "../common";
import { cat, type MediaItem, type Option, type ProductBody, type ProductDetail, type RelatedItem, type Variant } from "./api";
import { ProductPicker } from "./ProductPicker";
import { Confirm, Field, MediaPicker, MultiPick, Skeleton, slug, TagInput, Thumb, Uploader } from "./shared";

const NONE = "__none";
const DEFAULT_LOCATION = "principal";

interface Draft {
  title: string;
  handle: string;
  handleTouched: boolean;
  description: string;
  status: string;
  publishAt: string;
  brandId: string;
  categories: string[];
  type: string;
  tags: string[];
  options: Option[];
  variants: Variant[];
  attributes: Record<string, string>;
  seoTitle: string;
  seoDescription: string;
  media: string[];
  similar: string[];
  accessories: string[];
  price: Record<string, string>;
  compareAt: Record<string, string>;
  stock: Record<string, Record<string, string>>;
}

function emptyDraft(): Draft {
  return {
    title: "", handle: "", handleTouched: false, description: "", status: "draft", publishAt: "", brandId: "", categories: [], type: "", tags: [],
    options: [], variants: [{ sku: "", title: "Única", weightGrams: 0 }], attributes: {}, seoTitle: "", seoDescription: "", media: [],
    similar: [], accessories: [], price: {}, compareAt: {}, stock: {},
  };
}

function fromDetail(p: ProductDetail): Draft {
  const price: Record<string, string> = {};
  const compareAt: Record<string, string> = {};
  const stock: Record<string, Record<string, string>> = {};
  for (const v of p.variants ?? []) {
    const pr = p.pricing?.[v.sku];
    if (pr) {
      price[v.sku] = centsToPesos(pr.amount);
      if (pr.compareAt != null) compareAt[v.sku] = centsToPesos(pr.compareAt);
    }
    stock[v.sku] = {};
    for (const l of p.levels?.[v.sku] ?? []) stock[v.sku]![l.locationId] = String(l.onHand);
  }
  return {
    title: p.title, handle: p.handle, handleTouched: true, description: p.description ?? "", status: p.status,
    publishAt: p.publishAt ? p.publishAt.slice(0, 16) : "", brandId: p.brandId ?? "", categories: p.categories ?? [], type: p.type ?? "",
    tags: p.tags ?? [], options: p.options ?? [], variants: p.variants ?? [], attributes: p.attributes ?? {}, seoTitle: p.seoTitle ?? "",
    seoDescription: p.seoDescription ?? "", media: p.media ?? [], similar: p.related?.similar ?? [], accessories: p.related?.accessories ?? [],
    price, compareAt, stock,
  };
}

function Section({ title, description, children, actions }: { title: string; description?: string; children: ReactNode; actions?: ReactNode }) {
  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
        <div className="grid gap-1">
          <CardTitle className="text-base">{title}</CardTitle>
          {description && <CardDescription>{description}</CardDescription>}
        </div>
        {actions}
      </CardHeader>
      <CardContent className="grid gap-4">{children}</CardContent>
    </Card>
  );
}

export function ProductEditor({ me, id }: { me: Me; id: string }) {
  const isNew = id === "nuevo";
  const qc = useQueryClient();
  const canEdit = can(me, "catalog", "edit");
  const detail = useQuery({ queryKey: ["product", id], queryFn: () => cat.product(id), enabled: !isNew });
  const categories = useQuery({ queryKey: ["categories"], queryFn: cat.categories });
  const brands = useQuery({ queryKey: ["brands"], queryFn: cat.brands });
  const attributes = useQuery({ queryKey: ["attributes"], queryFn: cat.attributes });
  const tagTerms = useQuery({ queryKey: ["terms", "tags"], queryFn: () => cat.terms("tags") });
  const typeTerms = useQuery({ queryKey: ["terms", "types"], queryFn: () => cat.terms("types") });

  const [d, setD] = useState<Draft>(emptyDraft);
  const [version, setVersion] = useState(0);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<unknown>(null);
  const [mediaItems, setMediaItems] = useState<Record<string, MediaItem>>({});
  const [relatedItems, setRelatedItems] = useState<Record<string, RelatedItem>>({});
  const [picker, setPicker] = useState<null | "media" | "similar" | "accessories">(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  useEffect(() => {
    if (detail.data) {
      setD(fromDetail(detail.data));
      setVersion(detail.data.version);
      setMediaItems(detail.data.mediaItems ?? {});
      setRelatedItems(detail.data.relatedItems ?? {});
      setDirty(false);
    }
  }, [detail.data]);

  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const locations = useMemo(() => {
    const l = detail.data?.locations ?? [];
    return l.length ? l : [DEFAULT_LOCATION];
  }, [detail.data]);
  const currency = detail.data?.currency || "MXN";

  function set<K extends keyof Draft>(k: K, v: Draft[K]) {
    setD((p) => ({ ...p, [k]: v }));
    setDirty(true);
  }
  function setVariant(i: number, patch: Partial<Variant>) {
    setD((p) => {
      const variants = p.variants.map((v, j) => (j === i ? { ...v, ...patch } : v));
      const next = { ...p, variants };
      const old = p.variants[i]!.sku;
      if (patch.sku !== undefined && patch.sku !== old) {
        // Keep price and stock attached to the row when its SKU is renamed.
        for (const key of ["price", "compareAt", "stock"] as const) {
          const m = { ...(next[key] as Record<string, unknown>) };
          if (old in m) {
            m[patch.sku] = m[old];
            delete m[old];
          }
          (next[key] as Record<string, unknown>) = m;
        }
      }
      return next;
    });
    setDirty(true);
  }

  async function generate() {
    const opts = d.options.filter((o) => o.name.trim() && o.values.length);
    if (!opts.length) {
      toast.error("Agrega al menos una opción con valores, por ejemplo Talla: 25, 26, 27.");
      return;
    }
    try {
      const base = (d.handle || slug(d.title) || "sku").toUpperCase();
      const r = await cat.generate(base, opts, d.variants.filter((v) => v.sku));
      setD((p) => ({ ...p, options: r.options, variants: r.variants }));
      setDirty(true);
      toast.success(`${r.variants.length} variantes: ${r.created.length} nuevas${r.removed.length ? `, ${r.removed.length} quitadas` : ""}.`);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  async function save() {
    const errs: Record<string, string> = {};
    if (!d.title.trim()) errs.title = "Escribe el nombre del producto.";
    const handle = d.handle || slug(d.title);
    if (!handle) errs.handle = "Escribe la dirección web del producto.";
    const prices: ProductBody["prices"] = {};
    const stock: ProductBody["stock"] = {};
    const seen = new Set<string>();
    d.variants.forEach((v, i) => {
      if (!v.sku.trim()) errs[`variants[${i}].sku`] = "Falta el SKU.";
      else if (seen.has(v.sku)) errs[`variants[${i}].sku`] = "SKU repetido.";
      seen.add(v.sku);
      const raw = d.price[v.sku]?.trim();
      if (raw) {
        const amount = parsePesos(raw);
        if (amount == null) errs[`price.${v.sku}`] = "Precio inválido.";
        else {
          const cmpRaw = d.compareAt[v.sku]?.trim();
          const cmp = cmpRaw ? parsePesos(cmpRaw) : null;
          if (cmpRaw && cmp == null) errs[`compare.${v.sku}`] = "Precio inválido.";
          else if (cmp != null && cmp <= amount) errs[`compare.${v.sku}`] = "Debe ser mayor al precio.";
          prices[v.sku] = { amount, compareAt: cmp, currency };
        }
      } else if (d.status === "active") errs[`price.${v.sku}`] = "Un producto activo necesita precio.";
      for (const loc of locations) {
        const s = d.stock[v.sku]?.[loc]?.trim();
        if (s === undefined || s === "") continue;
        if (!/^\d+$/.test(s)) errs[`stock.${v.sku}.${loc}`] = "Número entero.";
        else (stock[v.sku] ??= {})[loc] = Number(s);
      }
    });
    setErrors(errs);
    setFormError(null);
    if (Object.keys(errs).length) {
      toast.error("Revisa los campos marcados antes de guardar.");
      return;
    }
    const body: ProductBody = {
      handle, title: d.title.trim(), description: d.description, status: d.status,
      publishAt: d.publishAt ? new Date(d.publishAt).toISOString() : null, brandId: d.brandId, categories: d.categories, type: d.type.trim(),
      tags: d.tags, options: d.options.filter((o) => o.name.trim()), variants: d.variants, attributes: d.attributes, seoTitle: d.seoTitle,
      seoDescription: d.seoDescription, media: d.media, related: { similar: d.similar, accessories: d.accessories }, prices, stock,
    };
    setSaving(true);
    try {
      const r = isNew ? await cat.createProduct(body) : await cat.saveProduct(id, body, version);
      setDirty(false);
      toast.success(isNew ? "Producto creado." : "Cambios guardados. Ya se ven en la tienda.");
      void qc.invalidateQueries({ queryKey: ["products"] });
      void qc.invalidateQueries({ queryKey: ["catalog-summary"] });
      if (isNew) navigate("catalogo", "productos", r.id);
      else void qc.invalidateQueries({ queryKey: ["product", id] });
    } catch (e) {
      if (e instanceof ApiError && e.problem.status === 412) {
        setFormError(new Error("Otra persona guardó este producto mientras lo editabas. Recarga la página para ver su versión antes de volver a guardar."));
      } else {
        setErrors(fieldErrors(e));
        setFormError(e);
      }
    } finally {
      setSaving(false);
    }
  }

  async function remove() {
    try {
      await cat.deleteProduct(id);
      toast.success("Producto eliminado.");
      void qc.invalidateQueries({ queryKey: ["products"] });
      navigate("catalogo");
    } catch (e) {
      toast.error(errorMessage(e));
    }
    setConfirmDelete(false);
  }

  if (!isNew && detail.isLoading) {
    return (
      <main className="mx-auto grid w-full max-w-6xl gap-4 px-4 py-6 md:px-8">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-64 w-full" />
        <Skeleton className="h-64 w-full" />
      </main>
    );
  }
  if (!isNew && detail.error) {
    return (
      <main className="mx-auto w-full max-w-3xl px-4 py-6">
        <ErrorBox error={detail.error} onRetry={() => void detail.refetch()} />
      </main>
    );
  }

  const catOptions = (categories.data ?? []).map((c) => ({ id: c.id, label: c.name, depth: c.depth }));
  const multi = d.variants.length > 1 || d.options.length > 0;

  return (
    <main className="mx-auto grid w-full max-w-6xl content-start gap-5 px-4 py-6 md:px-8">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="grid min-w-0 gap-1">
          <a href={href("catalogo")} className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
            <ArrowLeft className="size-4" aria-hidden="true" /> Productos
          </a>
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="truncate text-2xl font-bold tracking-tight">{isNew ? "Nuevo producto" : d.title || "Producto"}</h1>
            {!isNew && <StatusPill status={d.status} />}
            {dirty && <span className="text-sm text-warn">Cambios sin guardar</span>}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {!isNew && d.status === "active" && (
            <Button variant="outline" asChild>
              <a href={`/s/${me.tenant}/main/p/${d.handle}`} target="_blank" rel="noreferrer">
                <ExternalLink aria-hidden="true" /> Ver en la tienda
              </a>
            </Button>
          )}
          {!isNew && can(me, "catalog", "admin") && (
            <Button variant="outline" onClick={() => setConfirmDelete(true)}>
              <Trash2 aria-hidden="true" /> Eliminar
            </Button>
          )}
          {canEdit && (
            <Button onClick={() => void save()} disabled={saving || (!dirty && !isNew)}>
              <Save aria-hidden="true" /> {saving ? "Guardando…" : "Guardar"}
            </Button>
          )}
        </div>
      </div>

      {!canEdit && <Notice tone="warn">Tu acceso al catálogo es de solo lectura.</Notice>}
      <ErrorBox error={formError} />

      <fieldset disabled={!canEdit} className="grid min-w-0 gap-5 lg:grid-cols-[minmax(0,1fr)_340px]">
        <div className="grid min-w-0 content-start gap-5">
          <Section title="Información general">
            <Field label="Nombre" htmlFor="p-title" error={errors.title}>
              <Input
                id="p-title"
                value={d.title}
                maxLength={200}
                onChange={(e) => {
                  const title = e.target.value;
                  setD((p) => ({ ...p, title, handle: p.handleTouched ? p.handle : slug(title) }));
                  setDirty(true);
                }}
              />
            </Field>
            <Field label="Descripción" htmlFor="p-desc" hint="Cuéntale al comprador qué es, para quién y de qué está hecho.">
              <Textarea id="p-desc" rows={5} value={d.description} onChange={(e) => set("description", e.target.value)} />
            </Field>
          </Section>

          <Section
            title="Imágenes"
            description="La primera es la principal. Se generan tamaños optimizados automáticamente."
            actions={
              <Button type="button" variant="outline" size="sm" onClick={() => setPicker("media")}>
                <ImagePlus aria-hidden="true" /> Elegir de la biblioteca
              </Button>
            }
          >
            {d.media.length > 0 && (
              <ul className="grid grid-cols-[repeat(auto-fill,minmax(112px,1fr))] gap-3">
                {d.media.map((mid, i) => (
                  <li key={mid} className="group relative grid gap-1">
                    <Thumb media={mediaItems[mid]} size={112} className="w-full" />
                    {i === 0 && <span className="absolute left-1 top-1 rounded bg-foreground/80 px-1.5 py-0.5 text-[11px] font-medium text-background">Principal</span>}
                    <div className="flex items-center justify-between">
                      <div className="flex">
                        <Button type="button" variant="ghost" size="icon" className="size-7" disabled={i === 0} aria-label="Mover antes"
                          onClick={() => set("media", d.media.map((x, j) => (j === i - 1 ? mid : j === i ? d.media[i - 1]! : x)))}>
                          <ArrowLeft aria-hidden="true" />
                        </Button>
                        <Button type="button" variant="ghost" size="icon" className="size-7" disabled={i === d.media.length - 1} aria-label="Mover después"
                          onClick={() => set("media", d.media.map((x, j) => (j === i + 1 ? mid : j === i ? d.media[i + 1]! : x)))}>
                          <ArrowRight aria-hidden="true" />
                        </Button>
                      </div>
                      <Button type="button" variant="ghost" size="icon" className="size-7" aria-label="Quitar imagen" onClick={() => set("media", d.media.filter((x) => x !== mid))}>
                        <X aria-hidden="true" />
                      </Button>
                    </div>
                  </li>
                ))}
              </ul>
            )}
            <Uploader
              compact={d.media.length > 0}
              onUploaded={(ms) => {
                setMediaItems((p) => ({ ...p, ...Object.fromEntries(ms.map((m) => [m.id, m])) }));
                set("media", [...d.media, ...ms.map((m) => m.id).filter((x) => !d.media.includes(x))]);
              }}
            />
          </Section>

          <Section
            title="Opciones y variantes"
            description="Define opciones como Talla o Color y genera todas las combinaciones. Cada variante tiene su SKU, precio y existencias."
          >
            <div className="grid gap-3">
              {d.options.map((o, i) => (
                <div key={i} className="grid gap-2 rounded-md border p-3 sm:grid-cols-[180px_minmax(0,1fr)_auto] sm:items-start">
                  <Input aria-label={`Nombre de la opción ${i + 1}`} placeholder="Talla" value={o.name} maxLength={40}
                    onChange={(e) => set("options", d.options.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} />
                  <TagInput ariaLabel={`Valores de ${o.name || "la opción"}`} placeholder="Escribe un valor y Enter" value={o.values}
                    onChange={(values) => set("options", d.options.map((x, j) => (j === i ? { ...x, values } : x)))} />
                  <Button type="button" variant="ghost" size="icon" aria-label="Quitar opción" onClick={() => set("options", d.options.filter((_, j) => j !== i))}>
                    <Trash2 aria-hidden="true" />
                  </Button>
                </div>
              ))}
              <div className="flex flex-wrap gap-2">
                {d.options.length < 3 && (
                  <Button type="button" variant="outline" size="sm" onClick={() => set("options", [...d.options, { name: "", values: [] }])}>
                    <Plus aria-hidden="true" /> Agregar opción
                  </Button>
                )}
                {d.options.length > 0 && (
                  <Button type="button" size="sm" onClick={() => void generate()}>
                    <Sparkles aria-hidden="true" /> Generar variantes
                  </Button>
                )}
              </div>
            </div>

            <div className="overflow-x-auto rounded-md border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="min-w-36">Variante</TableHead>
                    <TableHead className="min-w-60">SKU</TableHead>
                    <TableHead className="min-w-28">Precio ({currency})</TableHead>
                    <TableHead className="min-w-28">Precio antes</TableHead>
                    {locations.map((l) => (
                      <TableHead key={l} className="min-w-24">Existencias{locations.length > 1 ? ` · ${l}` : ""}</TableHead>
                    ))}
                    <TableHead className="min-w-36">Código de barras</TableHead>
                    <TableHead className="min-w-24">Peso (g)</TableHead>
                    {d.media.length > 0 && <TableHead className="min-w-32">Imagen</TableHead>}
                    {multi && <TableHead><span className="sr-only">Acciones</span></TableHead>}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {d.variants.map((v, i) => {
                    const cell = (key: string) => (errors[key] ? "border-destructive" : "");
                    return (
                      <TableRow key={i}>
                        <TableCell>
                          <Input aria-label="Nombre de la variante" value={v.title} onChange={(e) => setVariant(i, { title: e.target.value })} />
                        </TableCell>
                        <TableCell>
                          <Input aria-label="SKU" className={`font-mono text-xs ${cell(`variants[${i}].sku`)}`} value={v.sku} title={errors[`variants[${i}].sku`]}
                            onChange={(e) => setVariant(i, { sku: e.target.value.trim() })} />
                        </TableCell>
                        <TableCell>
                          <Input aria-label="Precio" inputMode="decimal" placeholder="0.00" className={cell(`price.${v.sku}`)} title={errors[`price.${v.sku}`]}
                            value={d.price[v.sku] ?? ""} onChange={(e) => set("price", { ...d.price, [v.sku]: e.target.value })} />
                        </TableCell>
                        <TableCell>
                          <Input aria-label="Precio antes" inputMode="decimal" placeholder="Opcional" className={cell(`compare.${v.sku}`)} title={errors[`compare.${v.sku}`]}
                            value={d.compareAt[v.sku] ?? ""} onChange={(e) => set("compareAt", { ...d.compareAt, [v.sku]: e.target.value })} />
                        </TableCell>
                        {locations.map((l) => (
                          <TableCell key={l}>
                            <Input aria-label={`Existencias en ${l}`} inputMode="numeric" placeholder="0" className={cell(`stock.${v.sku}.${l}`)}
                              value={d.stock[v.sku]?.[l] ?? ""}
                              onChange={(e) => set("stock", { ...d.stock, [v.sku]: { ...(d.stock[v.sku] ?? {}), [l]: e.target.value } })} />
                          </TableCell>
                        ))}
                        <TableCell>
                          <Input aria-label="Código de barras" value={v.barcode ?? ""} onChange={(e) => setVariant(i, { barcode: e.target.value })} />
                        </TableCell>
                        <TableCell>
                          <Input aria-label="Peso en gramos" inputMode="numeric" value={String(v.weightGrams || "")}
                            onChange={(e) => setVariant(i, { weightGrams: Number(e.target.value.replace(/\D/g, "")) || 0 })} />
                        </TableCell>
                        {d.media.length > 0 && (
                          <TableCell>
                            <select aria-label="Imagen de la variante" className="h-9 w-full rounded-md border bg-background px-2 text-sm" value={v.mediaId ?? ""}
                              onChange={(e) => setVariant(i, { mediaId: e.target.value || undefined })}>
                              <option value="">La principal</option>
                              {d.media.map((mid, k) => (
                                <option key={mid} value={mid}>Imagen {k + 1}</option>
                              ))}
                            </select>
                          </TableCell>
                        )}
                        {multi && (
                          <TableCell>
                            <Button type="button" variant="ghost" size="icon" aria-label={`Quitar variante ${v.title}`} disabled={d.variants.length === 1}
                              onClick={() => set("variants", d.variants.filter((_, j) => j !== i))}>
                              <Trash2 aria-hidden="true" />
                            </Button>
                          </TableCell>
                        )}
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
            {Object.keys(errors).some((k) => /^(price|compare|stock|variants)/.test(k)) && (
              <Notice tone="bad">
                {Object.entries(errors).filter(([k]) => /^(price|compare|stock|variants)/.test(k)).map(([k, m]) => `${k.replace(/^[a-z]+[.[]/, "").replace(/[\]]/g, "")}: ${m}`).slice(0, 4).join(" · ")}
              </Notice>
            )}
            <div>
              <Button type="button" variant="outline" size="sm" onClick={() => set("variants", [...d.variants, { sku: "", title: "", weightGrams: 0 }])}>
                <Plus aria-hidden="true" /> Agregar variante a mano
              </Button>
            </div>
          </Section>

          {(attributes.data?.length ?? 0) > 0 && (
            <Section title="Atributos" description="Las especificaciones marcadas como filtrables aparecen como filtros en la tienda.">
              <div className="grid gap-4 sm:grid-cols-2">
                {attributes.data!.map((a) => {
                  const val = d.attributes[a.id] ?? "";
                  const setAttr = (v: string) => {
                    const next = { ...d.attributes };
                    if (v === "") delete next[a.id];
                    else next[a.id] = v;
                    set("attributes", next);
                  };
                  const label = a.name + (a.unit ? ` (${a.unit})` : "");
                  if (a.kind === "boolean")
                    return (
                      <label key={a.id} className="flex items-center gap-2 text-sm">
                        <Checkbox checked={val === "true"} onCheckedChange={(c) => setAttr(c ? "true" : "")} /> {label}
                      </label>
                    );
                  if (a.kind === "select")
                    return (
                      <Field key={a.id} label={label} htmlFor={`a-${a.id}`}>
                        <Select value={val || NONE} onValueChange={(v) => setAttr(v === NONE ? "" : v)}>
                          <SelectTrigger id={`a-${a.id}`}><SelectValue /></SelectTrigger>
                          <SelectContent>
                            <SelectItem value={NONE}>Sin definir</SelectItem>
                            {(a.options ?? []).map((o) => (
                              <SelectItem key={o} value={o}>{o}</SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </Field>
                    );
                  return (
                    <Field key={a.id} label={label} htmlFor={`a-${a.id}`}>
                      <Input id={`a-${a.id}`} inputMode={a.kind === "number" ? "decimal" : undefined} value={val} onChange={(e) => setAttr(e.target.value)} />
                    </Field>
                  );
                })}
              </div>
            </Section>
          )}

          <Section title="Productos relacionados" description="Se muestran en la ficha del producto.">
            {(["similar", "accessories"] as const).map((kind) => (
              <div key={kind} className="grid gap-2">
                <div className="flex items-center justify-between">
                  <span className="text-sm font-medium">{kind === "similar" ? "Similares" : "Accesorios"}</span>
                  <Button type="button" variant="outline" size="sm" onClick={() => setPicker(kind)}>
                    <Plus aria-hidden="true" /> Agregar
                  </Button>
                </div>
                {d[kind].length === 0 ? (
                  <p className="text-sm text-muted-foreground">Ninguno todavía.</p>
                ) : (
                  <ul className="flex flex-wrap gap-2">
                    {d[kind].map((pid) => (
                      <li key={pid} className="flex items-center gap-2 rounded-md border py-1 pl-1 pr-2 text-sm">
                        <Thumb media={relatedItems[pid]?.thumb} size={28} />
                        <span className="max-w-48 truncate">{relatedItems[pid]?.title ?? pid}</span>
                        <button type="button" aria-label={`Quitar ${relatedItems[pid]?.title ?? pid}`} className="text-muted-foreground hover:text-foreground"
                          onClick={() => set(kind, d[kind].filter((x) => x !== pid))}>
                          <X className="size-4" aria-hidden="true" />
                        </button>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            ))}
          </Section>

          <Section title="Buscadores (SEO)" description="Cómo aparece el producto en Google. Si lo dejas vacío se usa el nombre y la descripción.">
            <Field label="Título" htmlFor="p-seot" hint={`${d.seoTitle.length}/70`}>
              <Input id="p-seot" maxLength={70} value={d.seoTitle} placeholder={d.title} onChange={(e) => set("seoTitle", e.target.value)} />
            </Field>
            <Field label="Descripción" htmlFor="p-seod" hint={`${d.seoDescription.length}/160`}>
              <Textarea id="p-seod" rows={2} maxLength={160} value={d.seoDescription} onChange={(e) => set("seoDescription", e.target.value)} />
            </Field>
            <Field label="Dirección web" htmlFor="p-handle" error={errors.handle} hint={`/p/${d.handle || "…"}`}>
              <Input id="p-handle" className="font-mono text-sm" value={d.handle}
                onChange={(e) => setD((p) => ({ ...p, handle: slug(e.target.value), handleTouched: true }))} onBlur={() => setDirty(true)} />
            </Field>
            <div className="rounded-md border bg-muted/40 p-3 text-sm">
              <div className="truncate text-base text-primary">{d.seoTitle || d.title || "Título del producto"}</div>
              <div className="truncate text-xs text-ok">tutienda.mx/p/{d.handle || "producto"}</div>
              <div className="line-clamp-2 text-muted-foreground">{d.seoDescription || d.description || "Descripción del producto."}</div>
            </div>
          </Section>
        </div>

        <div className="grid min-w-0 content-start gap-5">
          <Section title="Estado">
            <Field label="Visibilidad" htmlFor="p-status" hint={d.status === "active" ? "Visible en la tienda." : d.status === "draft" ? "Solo tu equipo lo ve." : "Oculto y fuera de los listados."}>
              <Select value={d.status} onValueChange={(v) => set("status", v)}>
                <SelectTrigger id="p-status"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="draft">Borrador</SelectItem>
                  <SelectItem value="active">Activo</SelectItem>
                  <SelectItem value="archived">Archivado</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            {d.status === "draft" && (
              <Field label="Publicar automáticamente" htmlFor="p-pub" hint="Opcional. A esa hora pasa a activo.">
                <Input id="p-pub" type="datetime-local" value={d.publishAt} onChange={(e) => set("publishAt", e.target.value)} />
              </Field>
            )}
          </Section>

          <Section title="Organización">
            <Field label="Marca" htmlFor="p-brand">
              <Select value={d.brandId || NONE} onValueChange={(v) => set("brandId", v === NONE ? "" : v)}>
                <SelectTrigger id="p-brand"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value={NONE}>Sin marca</SelectItem>
                  {(brands.data ?? []).map((b) => (
                    <SelectItem key={b.id} value={b.id}>{b.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label="Categorías" htmlFor="p-cats">
              <MultiPick id="p-cats" label="Elegir categorías" options={catOptions} value={d.categories} onChange={(v) => set("categories", v)} empty="Aún no hay categorías." />
            </Field>
            <Field label="Tipo de producto" htmlFor="p-type">
              <Input id="p-type" list="p-types" value={d.type} maxLength={60} onChange={(e) => set("type", e.target.value)} />
              <datalist id="p-types">
                {(typeTerms.data ?? []).map((t) => (
                  <option key={t.name} value={t.name} />
                ))}
              </datalist>
            </Field>
            <Field label="Etiquetas" htmlFor="p-tags" hint="Sirven para colecciones automáticas y búsqueda.">
              <TagInput id="p-tags" value={d.tags} onChange={(v) => set("tags", v)} suggestions={(tagTerms.data ?? []).map((t) => t.name)} placeholder="verano, oferta…" />
            </Field>
          </Section>
        </div>
      </fieldset>

      <MediaPicker
        open={picker === "media"}
        exclude={d.media}
        onClose={() => setPicker(null)}
        onPick={(ms) => {
          setMediaItems((p) => ({ ...p, ...Object.fromEntries(ms.map((m) => [m.id, m])) }));
          set("media", [...d.media, ...ms.map((m) => m.id)]);
          setPicker(null);
        }}
      />
      <ProductPicker
        open={picker === "similar" || picker === "accessories"}
        title={picker === "accessories" ? "Agregar accesorios" : "Agregar productos similares"}
        exclude={[id, ...(picker === "accessories" ? d.accessories : d.similar)]}
        onClose={() => setPicker(null)}
        onPick={(ps) => {
          const kind = picker === "accessories" ? "accessories" : "similar";
          setRelatedItems((p) => ({ ...p, ...Object.fromEntries(ps.map((x) => [x.id, { id: x.id, title: x.title, status: x.status, thumb: x.thumb }])) }));
          set(kind, [...d[kind], ...ps.map((x) => x.id)]);
          setPicker(null);
        }}
      />
      <Confirm
        open={confirmDelete}
        title="¿Eliminar este producto?"
        description="Se quita de la tienda y de las colecciones. Los pedidos anteriores conservan sus datos. Esta acción no se puede deshacer."
        confirmLabel="Eliminar producto"
        destructive
        onConfirm={() => void remove()}
        onClose={() => setConfirmDelete(false)}
      />
    </main>
  );
}
