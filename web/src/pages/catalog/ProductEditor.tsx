import { Datasheet } from "./Datasheet";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { ArrowLeft, ArrowRight, ExternalLink, ImagePlus, Plus, Save, Sparkles, Trash2, X } from "lucide-react";
import { ApiError, errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { centsToPesos, parsePesos, readNumber } from "@/money";
import { can } from "@/perms";
import { href, navigate } from "@/router";
import { ErrorBox, Notice, StatusPill } from "../common";
import { cat, type DigitalFile, type MediaItem, type Option, type ProductBody, type ProductDetail, type RelatedItem, type Variant } from "./api";
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
  const bundles = useQuery({ queryKey: ["bundles"], queryFn: () => request<{ items: { sku: string }[] }>("GET", "/v1/bundles"), enabled: !isNew });
  const kitSkus = useMemo(() => new Set((bundles.data?.items ?? []).map((b) => b.sku)), [bundles.data]);

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
        if (kitSkus.has(v.sku)) break;
        const s = d.stock[v.sku]?.[loc]?.trim();
        if (s === undefined || s === "") continue;
        if (!/^\d+$/.test(s)) errs[`stock.${v.sku}.${loc}`] = "Número entero.";
        else (stock[v.sku] ??= {})[loc] = Number(s);
      }
    });
    setErrors(errs);
    setFormError(null);
    if (Object.keys(errs).length) {
      const first = Object.values(errs)[0];
      toast.error(`Revisa los campos marcados antes de guardar: ${first}`);
      // After React paints the marks, take the cursor to the first one.
      requestAnimationFrame(() => {
        const el = document.querySelector<HTMLElement>("main [aria-invalid=true]");
        el?.scrollIntoView({ block: "center" });
        el?.focus();
      });
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
                onBlur={() => {
                  // A simple product gets a SKU from its name; the merchant can change it.
                  const only = d.variants.length === 1 ? d.variants[0] : undefined;
                  if (only && !only.sku && d.title.trim()) setVariant(0, { sku: slug(d.title).toUpperCase().slice(0, 40) });
                }}
              />
            </Field>
            <Field label="Descripción" htmlFor="p-desc" hint="Cuéntale al comprador qué es, para quién y de qué está hecho.">
              <Textarea id="p-desc" rows={5} value={d.description} onChange={(e) => set("description", e.target.value)} />
            </Field>
          </Section>

          {!isNew && <Section title="Ficha técnica (PDF)" description="El texto del PDF aparece en la ficha y responde preguntas en «Pregúntale a la tienda».">
            <Datasheet productId={id} canEdit={canEdit} />
          </Section>}

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
                    const bad = (key: string) => (errors[key] ? { "aria-invalid": true as const, "aria-description": errors[key] } : {});
                    return (
                      <TableRow key={i}>
                        <TableCell>
                          <Input aria-label="Nombre de la variante" value={v.title} onChange={(e) => setVariant(i, { title: e.target.value })} />
                        </TableCell>
                        <TableCell>
                          <Input aria-label="SKU" {...bad(`variants[${i}].sku`)} className={`font-mono text-xs ${cell(`variants[${i}].sku`)}`} value={v.sku} title={errors[`variants[${i}].sku`]}
                            onChange={(e) => setVariant(i, { sku: e.target.value.trim() })} />
                        </TableCell>
                        <TableCell>
                          <Input aria-label="Precio" {...bad(`price.${v.sku}`)} inputMode="decimal" placeholder="0.00" className={cell(`price.${v.sku}`)} title={errors[`price.${v.sku}`]}
                            value={d.price[v.sku] ?? ""} onChange={(e) => set("price", { ...d.price, [v.sku]: e.target.value })} />
                        </TableCell>
                        <TableCell>
                          <Input aria-label="Precio antes" {...bad(`compare.${v.sku}`)} inputMode="decimal" placeholder="Opcional" className={cell(`compare.${v.sku}`)} title={errors[`compare.${v.sku}`]}
                            value={d.compareAt[v.sku] ?? ""} onChange={(e) => set("compareAt", { ...d.compareAt, [v.sku]: e.target.value })} />
                        </TableCell>
                        {locations.map((l) => (
                          <TableCell key={l}>
                            <Input aria-label={`Existencias en ${l}`} inputMode="numeric" placeholder={kitSkus.has(v.sku) ? "Kit" : "0"} {...bad(`stock.${v.sku}.${l}`)} className={cell(`stock.${v.sku}.${l}`)}
                              disabled={kitSkus.has(v.sku)} title={kitSkus.has(v.sku) ? "Es un kit: sus existencias salen de sus componentes" : undefined}
                              value={kitSkus.has(v.sku) ? "" : d.stock[v.sku]?.[l] ?? ""}
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
          {!isNew && d.variants.length > 0 && <PreorderEditor skus={d.variants.map((v) => v.sku).filter(Boolean)} canEdit={canEdit} canCollect={can(me, "orders", "edit")} />}
          {!isNew && d.variants.length > 0 && <DigitalFiles skus={d.variants.map((v) => v.sku).filter(Boolean)} canEdit={canEdit} />}
          {!isNew && d.variants.length > 0 && <KitEditor skus={d.variants.map((v) => v.sku).filter(Boolean)} canEdit={can(me, "settings", "admin")} />}
          {!isNew && <Personalization id={id} canEdit={canEdit} />}
          {!isNew && <QtyRules id={id} canEdit={canEdit} />}
          {!isNew && <Customs id={id} canEdit={can(me, "settings", "admin")} />}
          {!isNew && <Translations id={id} canEdit={canEdit} />}
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

const LANG_NAMES: Record<string, string> = { en: "English", fr: "Français", pt: "Português", id: "Bahasa Indonesia", ja: "日本語" };
const TEXT_FIELDS: [string, string][] = [["title", "Nombre"], ["description", "Descripción"], ["seoTitle", "Título para buscadores"], ["seoDescription", "Descripción para buscadores"]];

/** Translations of the product, written next to it. Each one remembers the
 *  original it translates, so a change of the original is pointed out. */
function Translations({ id, canEdit }: { id: string; canEdit: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["product", id, "translations"], queryFn: () => request<{ languages: string[]; source: Record<string, string>; texts: Record<string, Record<string, { text: string; outdated: boolean }>> }>("GET", `/v1/products/${encodeURIComponent(id)}/translations`) });
  const [lang, setLang] = useState("");
  const [draft, setDraft] = useState<Record<string, Record<string, string>>>({});
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!q.data) return;
    setLang((l) => l || q.data.languages[0] || "");
    setDraft(Object.fromEntries(Object.entries(q.data.texts).map(([l, f]) => [l, Object.fromEntries(Object.entries(f).map(([k, v]) => [k, v.text]))])));
  }, [q.data]);
  if (!q.data) return null;
  const { languages, source, texts } = q.data;
  if (languages.length === 0) {
    return <Section title="Traducciones" description="Tu tienda solo está en español."><p className="text-sm text-muted-foreground">Activa otros idiomas en <a className="text-primary hover:underline" href={href("configuracion", "idiomas")}>Configuración → Idiomas</a> para traducir este producto.</p></Section>;
  }
  async function save() {
    setBusy(true);
    try {
      await request<void>("PUT", `/v1/products/${encodeURIComponent(id)}/translations`, draft, { as: "none" });
      toast.success("Traducciones guardadas. Ya se ven en la tienda.");
      void qc.invalidateQueries({ queryKey: ["product", id, "translations"] });
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Section title="Traducciones" description="Cómo se ve este producto en los otros idiomas de tu tienda." actions={canEdit && <Button size="sm" onClick={() => void save()} disabled={busy}>{busy ? "Guardando…" : "Guardar traducciones"}</Button>}>
      <div className="flex flex-wrap gap-2" role="tablist" aria-label="Idioma">
        {languages.map((l) => {
          const missing = TEXT_FIELDS.some(([f]) => source[f] && !(draft[l]?.[f] ?? "").trim());
          const old = TEXT_FIELDS.some(([f]) => texts[l]?.[f]?.outdated);
          return <Button key={l} type="button" size="sm" role="tab" aria-selected={lang === l} variant={lang === l ? "default" : "outline"} onClick={() => setLang(l)}>{LANG_NAMES[l] ?? l}{old ? " · revisar" : missing ? " · incompleto" : ""}</Button>;
        })}
      </div>
      {TEXT_FIELDS.filter(([f]) => source[f]).map(([f, label]) => (
        <Field key={f} label={label} htmlFor={`tr-${lang}-${f}`} hint={texts[lang]?.[f]?.outdated ? "El original cambió desde que se tradujo; revísalo." : undefined}>
          {f === "description"
            ? <Textarea id={`tr-${lang}-${f}`} rows={3} value={draft[lang]?.[f] ?? ""} placeholder={source[f]} disabled={!canEdit} onChange={(e) => setDraft((d) => ({ ...d, [lang]: { ...d[lang], [f]: e.target.value } }))} />
            : <Input id={`tr-${lang}-${f}`} value={draft[lang]?.[f] ?? ""} placeholder={source[f]} disabled={!canEdit} onChange={(e) => setDraft((d) => ({ ...d, [lang]: { ...d[lang], [f]: e.target.value } }))} />}
        </Field>
      ))}
    </Section>
  );
}

interface QtyRule { group: string; min: number; max: number; step: number }

interface CustomChoice { id?: string; label: string; price: number }
interface CustomField { id?: string; label: string; kind: "text" | "select" | "checkbox"; required?: boolean; maxLength?: number; price?: number; choices?: CustomChoice[]; help?: string }

/** Personalization: engraving (text), finish (list) or gift wrap (checkbox),
 *  each one able to add to the price. The cart validates and charges it. */
function Personalization({ id, canEdit }: { id: string; canEdit: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["product", id, "customs"], queryFn: () => request<{ fields: CustomField[] }>("GET", `/v1/products/${encodeURIComponent(id)}/customizations`) });
  const [fields, setFields] = useState<CustomField[]>([]);
  const [busy, setBusy] = useState(false);
  const [errs, setErrs] = useState<Record<string, string>>({});
  useEffect(() => { if (q.data) setFields(q.data.fields ?? []); }, [q.data]);
  if (!q.data) return null;
  const set = (i: number, p: Partial<CustomField>) => setFields((x) => x.map((f, j) => (j === i ? { ...f, ...p } : f)));
  const pesos = (v: string) => Math.round((parseFloat(v.replace(/[^\d.]/g, "")) || 0) * 100);
  const dirty = JSON.stringify(fields) !== JSON.stringify(q.data.fields ?? []);
  async function save() {
    setBusy(true); setErrs({});
    try {
      const r = await request<{ fields: CustomField[] }>("PUT", `/v1/products/${encodeURIComponent(id)}/customizations`, { fields });
      setFields(r.fields ?? []);
      qc.setQueryData(["product", id, "customs"], { fields: r.fields ?? [] });
      toast.success("Personalización guardada. La tienda ya la muestra.");
    } catch (e) {
      setErrs(fieldErrors(e)); toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Section title="Personalización" description="Lo que el comprador puede pedir a su gusto: un grabado, un acabado o envoltura de regalo. Cada opción puede sumar al precio."
      actions={canEdit && <Button size="sm" disabled={!dirty || busy} onClick={() => void save()}>{busy ? "Guardando…" : "Guardar"}</Button>}>
      {fields.length === 0 && <p className="text-sm text-muted-foreground">Sin personalización.</p>}
      {fields.map((f, i) => (
        <div key={i} className="grid gap-2 rounded-md border p-3" data-custom-field>
          <div className="grid gap-2 sm:grid-cols-[1fr_9rem_7rem_auto] sm:items-end">
            <Field label="Nombre" htmlFor={`cz-l-${i}`} hint={errs[`fields[${i}].label`]}><Input id={`cz-l-${i}`} value={f.label} disabled={!canEdit} placeholder="Grabado" onChange={(e) => set(i, { label: e.target.value })} /></Field>
            <Field label="Tipo" htmlFor={`cz-k-${i}`}>
              <select id={`cz-k-${i}`} className="h-9 w-full rounded-md border bg-background px-2 text-sm" value={f.kind} disabled={!canEdit}
                onChange={(e) => { const kind = e.target.value as CustomField["kind"]; set(i, { kind, choices: kind === "select" ? (f.choices?.length ? f.choices : [{ label: "", price: 0 }, { label: "", price: 0 }]) : undefined }); }}>
                <option value="text">Texto</option><option value="select">Lista</option><option value="checkbox">Casilla</option>
              </select>
            </Field>
            {f.kind !== "select" ? (
              <Field label="Suma al precio" htmlFor={`cz-p-${i}`}><Input id={`cz-p-${i}`} inputMode="decimal" defaultValue={f.price ? centsToPesos(f.price) : ""} disabled={!canEdit} placeholder="0" onChange={(e) => set(i, { price: pesos(e.target.value) })} /></Field>
            ) : <span />}
            {canEdit && <Button type="button" size="icon" variant="ghost" aria-label={`Quitar ${f.label || "personalización"}`} onClick={() => setFields((x) => x.filter((_, j) => j !== i))}><Trash2 aria-hidden="true" /></Button>}
          </div>
          <div className="flex flex-wrap items-center gap-4 text-sm">
            {f.kind !== "checkbox" && <label className="flex items-center gap-2"><Checkbox checked={!!f.required} disabled={!canEdit} onCheckedChange={(v) => set(i, { required: v === true })} /> Obligatorio</label>}
            {f.kind === "text" && <label className="flex items-center gap-2">Hasta <Input aria-label="Caracteres máximos" className="h-8 w-20" inputMode="numeric" value={f.maxLength || ""} placeholder="40" disabled={!canEdit} onChange={(e) => set(i, { maxLength: Number(e.target.value.replace(/\D/g, "")) || 0 })} /> caracteres</label>}
          </div>
          {f.kind === "select" && (
            <div className="grid gap-2">
              {(f.choices ?? []).map((c, j) => (
                <div key={j} className="grid grid-cols-[1fr_7rem_auto] items-center gap-2">
                  <Input aria-label={`Opción ${j + 1}`} value={c.label} disabled={!canEdit} placeholder={`Opción ${j + 1}`} onChange={(e) => set(i, { choices: f.choices!.map((x, k) => (k === j ? { ...x, label: e.target.value } : x)) })} />
                  <Input aria-label={`Suma de la opción ${j + 1}`} inputMode="decimal" defaultValue={c.price ? centsToPesos(c.price) : ""} placeholder="+0" disabled={!canEdit} onChange={(e) => set(i, { choices: f.choices!.map((x, k) => (k === j ? { ...x, price: pesos(e.target.value) } : x)) })} />
                  {canEdit && <Button type="button" size="icon" variant="ghost" aria-label={`Quitar opción ${j + 1}`} disabled={(f.choices ?? []).length <= 2} onClick={() => set(i, { choices: f.choices!.filter((_, k) => k !== j) })}><X aria-hidden="true" /></Button>}
                </div>
              ))}
              {canEdit && <div><Button type="button" variant="outline" size="sm" onClick={() => set(i, { choices: [...(f.choices ?? []), { label: "", price: 0 }] })}><Plus aria-hidden="true" /> Agregar opción</Button></div>}
              {errs[`fields[${i}].choices`] && <p className="text-sm text-bad">{errs[`fields[${i}].choices`]}</p>}
            </div>
          )}
        </div>
      ))}
      {canEdit && fields.length < 10 && <div><Button type="button" variant="outline" size="sm" onClick={() => setFields((x) => [...x, { label: "", kind: "text", maxLength: 40 }])}><Plus aria-hidden="true" /> Agregar personalización</Button></div>}
    </Section>
  );
}

interface PreorderData { sku: string; shipsOn: string; depositBps: number; maxUnits: number; sold: number; active: boolean; note: string }

/** Preorder: sell before the stock arrives, from a pool of units, with an
 *  optional deposit; the balance is charged later with one click. */
function PreorderEditor({ skus, canEdit, canCollect }: { skus: string[]; canEdit: boolean; canCollect: boolean }) {
  const qc = useQueryClient();
  const [sku, setSku] = useState(skus[0] ?? "");
  useEffect(() => { if (!skus.includes(sku)) setSku(skus[0] ?? ""); }, [skus, sku]);
  const q = useQuery({ queryKey: ["preorder", sku], queryFn: () => request<{ configured: boolean; preorder?: PreorderData; ordersOwing?: number }>("GET", `/v1/preorders/${encodeURIComponent(sku)}`), enabled: !!sku });
  const empty = { shipsOn: "", deposit: "100", max: "", note: "", active: true };
  const [v, setV] = useState(empty);
  const [busy, setBusy] = useState(false);
  const [errs, setErrs] = useState<Record<string, string>>({});
  useEffect(() => {
    const p = q.data?.preorder;
    setV(p ? { shipsOn: p.shipsOn, deposit: String(p.depositBps / 100), max: p.maxUnits ? String(p.maxUnits) : "", note: p.note, active: p.active } : empty);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q.data]);
  if (!q.data) return null;
  const p = q.data.preorder;
  const refresh = () => { void qc.invalidateQueries({ queryKey: ["preorder", sku] }); void qc.invalidateQueries({ queryKey: ["product"] }); };
  async function save(active: boolean) {
    setBusy(true); setErrs({});
    try {
      await request("PUT", `/v1/preorders/${encodeURIComponent(sku)}`, { shipsOn: v.shipsOn, depositBps: Math.round(readNumber(v.deposit, "Anticipo (%)", 100) * 100), maxUnits: readNumber(v.max, "Piezas disponibles"), note: v.note, active });
      toast.success(active ? "Preventa activa: la tienda ya la ofrece." : "Preventa pausada: se vende de nuevo con existencias.");
      refresh();
    } catch (e) {
      setErrs(fieldErrors(e)); toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  async function end() {
    setBusy(true);
    try {
      await request("DELETE", `/v1/preorders/${encodeURIComponent(sku)}`, undefined, { as: "none" });
      toast.success("Preventa terminada. Los pedidos apartados se conservan.");
      refresh();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  async function collect() {
    setBusy(true);
    try {
      const r = await request<{ sent: number; failed: number }>("POST", `/v1/preorders/${encodeURIComponent(sku)}/collect`);
      toast.success(`Enviamos el enlace de pago a ${r.sent} ${r.sent === 1 ? "comprador" : "compradores"}${r.failed ? ` (${r.failed} sin enviar)` : ""}.`);
      refresh();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Section title="Preventa" description="Vende antes de tener existencias. Las piezas apartadas no tocan tu inventario; puedes cobrar un anticipo y enviar el enlace para pagar el resto a todos los compradores cuando llegue el producto."
      actions={canEdit && (
        <div className="flex gap-2">
          {p?.active && <Button size="sm" variant="outline" disabled={busy} onClick={() => void end()}>Terminar</Button>}
          <Button size="sm" disabled={busy || !v.shipsOn} onClick={() => void save(true)}>{p?.active ? "Guardar" : "Activar preventa"}</Button>
        </div>
      )}>
      {skus.length > 1 && (
        <Field label="Variante" htmlFor="po-sku">
          <select id="po-sku" className="h-9 w-full rounded-md border bg-background px-2 text-sm" value={sku} onChange={(e) => setSku(e.target.value)}>
            {skus.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
        </Field>
      )}
      {p?.active && <Notice tone="ok">En preventa: {p.sold} {p.sold === 1 ? "pieza apartada" : "piezas apartadas"}{p.maxUnits ? ` de ${p.maxUnits}` : ""}.</Notice>}
      <div className="grid gap-3 sm:grid-cols-3">
        <Field label="Se envía a partir del" htmlFor="po-ships" hint={errs.shipsOn}><Input id="po-ships" type="date" value={v.shipsOn} disabled={!canEdit} onChange={(e) => setV({ ...v, shipsOn: e.target.value })} /></Field>
        <Field label="Anticipo (%)" htmlFor="po-dep" hint={errs.depositBps || "100 = se paga todo al apartar"}><Input id="po-dep" inputMode="numeric" value={v.deposit} disabled={!canEdit} onChange={(e) => setV({ ...v, deposit: e.target.value.replace(/[^\d]/g, "") })} /></Field>
        <Field label="Piezas disponibles" htmlFor="po-max" hint={errs.maxUnits || "Vacío = sin límite"}><Input id="po-max" inputMode="numeric" value={v.max} disabled={!canEdit} onChange={(e) => setV({ ...v, max: e.target.value.replace(/\D/g, "") })} /></Field>
      </div>
      {!!q.data.ordersOwing && canCollect && (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border p-3 text-sm">
          <span>{q.data.ordersOwing} {q.data.ordersOwing === 1 ? "pedido debe" : "pedidos deben"} el resto del pago.</span>
          <Button size="sm" variant="outline" disabled={busy} onClick={() => void collect()}>Enviar enlace de pago a todos</Button>
        </div>
      )}
    </Section>
  );
}

const fileSize = (n: number) => (n >= 1048576 ? `${(n / 1048576).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`);

/** Digital product: files delivered after payment by signed links, with a
 *  download limit and an expiry. Products without weight need no shipping. */
function DigitalFiles({ skus, canEdit }: { skus: string[]; canEdit: boolean }) {
  const qc = useQueryClient();
  const [sku, setSku] = useState(skus[0] ?? "");
  useEffect(() => { if (!skus.includes(sku)) setSku(skus[0] ?? ""); }, [skus, sku]);
  const q = useQuery({ queryKey: ["digital", sku], queryFn: () => cat.digitalFiles(sku), enabled: !!sku });
  const [busy, setBusy] = useState(false);
  const [maxD, setMaxD] = useState("5");
  const [days, setDays] = useState("30");
  const [edits, setEdits] = useState<Record<string, DigitalFile>>({});
  const [confirm, setConfirm] = useState<DigitalFile | null>(null);
  if (!q.data) return null;
  const refresh = () => void qc.invalidateQueries({ queryKey: ["digital", sku] });
  async function upload(f: File | undefined) {
    if (!f) return;
    if (f.size > 100 * 1048576) { toast.error("El archivo pasa de 100 MB."); return; }
    setBusy(true);
    try {
      await cat.uploadDigital(sku, f, Number(maxD) || 5, Number(days) || 30);
      toast.success(`${f.name} listo. Se entrega al confirmarse cada pago.`);
      refresh();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  async function saveEdit(f: DigitalFile) {
    try {
      await cat.updateDigital(f);
      setEdits(({ [f.id]: _, ...rest }) => rest);
      toast.success("Archivo actualizado. Los enlaces ya entregados conservan sus límites.");
      refresh();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }
  async function remove(f: DigitalFile) {
    try {
      await cat.deleteDigital(f.sku, f.id);
      toast.success("Archivo quitado. Sus enlaces dejaron de funcionar.");
      refresh();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setConfirm(null);
    }
  }
  const files = q.data;
  return (
    <Section title="Archivos digitales" description="Libros, plantillas, música o software. El comprador recibe un enlace de descarga en la página de gracias, en su correo y en su cuenta en cuanto se confirma el pago. Si cancelas o reembolsas el pedido, el enlace deja de funcionar. Si el producto no pesa nada, no se cobra envío.">
      {skus.length > 1 && (
        <Field label="Variante" htmlFor="dg-sku">
          <select id="dg-sku" className="h-9 w-full rounded-md border bg-background px-2 text-sm" value={sku} onChange={(e) => setSku(e.target.value)}>
            {skus.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
        </Field>
      )}
      {files.length === 0 && <p className="text-sm text-muted-foreground">Sin archivos: es un producto físico.</p>}
      {files.map((f0) => {
        const f = edits[f0.id] ?? f0;
        const set = (p: Partial<DigitalFile>) => setEdits((x) => ({ ...x, [f0.id]: { ...f, ...p } }));
        return (
          <div key={f0.id} className="grid gap-2 rounded-md border p-3 sm:grid-cols-[1fr_7rem_7rem_auto] sm:items-end" data-digital-file>
            <Field label="Nombre del archivo" htmlFor={`dg-n-${f0.id}`} hint={`${fileSize(f0.size)} · ${f0.contentType}`}>
              <Input id={`dg-n-${f0.id}`} value={f.filename} disabled={!canEdit} onChange={(e) => set({ filename: e.target.value })} />
            </Field>
            <Field label="Descargas" htmlFor={`dg-m-${f0.id}`}><Input id={`dg-m-${f0.id}`} inputMode="numeric" value={f.maxDownloads || ""} disabled={!canEdit} onChange={(e) => set({ maxDownloads: Number(e.target.value.replace(/\D/g, "")) || 0 })} /></Field>
            <Field label="Días del enlace" htmlFor={`dg-d-${f0.id}`}><Input id={`dg-d-${f0.id}`} inputMode="numeric" value={f.linkDays || ""} disabled={!canEdit} onChange={(e) => set({ linkDays: Number(e.target.value.replace(/\D/g, "")) || 0 })} /></Field>
            {canEdit && (
              <div className="flex gap-1">
                {edits[f0.id] && <Button type="button" size="sm" onClick={() => void saveEdit(f)}>Guardar</Button>}
                <Button type="button" size="icon" variant="ghost" aria-label={`Quitar ${f0.filename}`} onClick={() => setConfirm(f0)}><Trash2 aria-hidden="true" /></Button>
              </div>
            )}
          </div>
        );
      })}
      {canEdit && files.length < 20 && (
        <div className="grid gap-2 rounded-md border border-dashed p-3 sm:grid-cols-[7rem_7rem_1fr] sm:items-end">
          <Field label="Descargas" htmlFor="dg-new-m" hint="Por compra"><Input id="dg-new-m" inputMode="numeric" value={maxD} onChange={(e) => setMaxD(e.target.value.replace(/\D/g, ""))} /></Field>
          <Field label="Días del enlace" htmlFor="dg-new-d"><Input id="dg-new-d" inputMode="numeric" value={days} onChange={(e) => setDays(e.target.value.replace(/\D/g, ""))} /></Field>
          <label className="inline-flex h-9 cursor-pointer items-center justify-center gap-2 rounded-md border px-3 text-sm font-medium hover:bg-accent">
            <Plus aria-hidden="true" className="size-4" /> {busy ? "Subiendo…" : "Subir archivo (hasta 100 MB)"}
            <input type="file" className="sr-only" disabled={busy} data-digital-upload onChange={(e) => { void upload(e.target.files?.[0]); e.target.value = ""; }} />
          </label>
        </div>
      )}
      <Confirm open={!!confirm} title={`¿Quitar ${confirm?.filename ?? "el archivo"}?`} description="Los enlaces que ya recibieron los compradores dejarán de funcionar." confirmLabel="Quitar" destructive onConfirm={() => { if (confirm) void remove(confirm); }} onClose={() => setConfirm(null)} />
    </Section>
  );
}

interface KitPart { sku: string; qty: number; title?: string; available?: number }
interface KitView { sku: string; components: KitPart[]; available: number }

/** Kit (bundle): this SKU is sold as a set of other SKUs. It has no stock of
 *  its own: it is available while every component is, and each sale,
 *  cancellation or return moves the components' stock. */
function KitEditor({ skus, canEdit }: { skus: string[]; canEdit: boolean }) {
  const qc = useQueryClient();
  const [sku, setSku] = useState(skus[0] ?? "");
  useEffect(() => { if (!skus.includes(sku)) setSku(skus[0] ?? ""); }, [skus, sku]);
  const q = useQuery({ queryKey: ["bundle", sku], queryFn: () => request<KitView>("GET", `/v1/bundles/${encodeURIComponent(sku)}`), enabled: !!sku });
  const [parts, setParts] = useState<KitPart[]>([]);
  const [busy, setBusy] = useState(false);
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [err, setErr] = useState("");
  useEffect(() => { if (q.data) setParts(q.data.components); }, [q.data]);
  if (!q.data) return null;
  const clean = (ps: KitPart[]) => ps.map((p) => ({ sku: p.sku.trim(), qty: p.qty }));
  const dirty = JSON.stringify(clean(parts)) !== JSON.stringify(clean(q.data.components));
  const setPart = (i: number, p: Partial<KitPart>) => setParts((x) => x.map((y, j) => (j === i ? { ...y, ...p } : y)));
  async function save() {
    setBusy(true); setErrs({}); setErr("");
    try {
      const body = clean(parts).filter((p) => p.sku);
      const r = body.length
        ? await request<KitView>("PUT", `/v1/bundles/${encodeURIComponent(sku)}`, { components: body })
        : (await request("DELETE", `/v1/bundles/${encodeURIComponent(sku)}`), { sku, components: [], available: 0 });
      qc.setQueryData(["bundle", sku], r);
      setParts(r.components);
      toast.success(body.length ? "Kit guardado. Su disponibilidad ya sale de sus componentes." : "Ya no es kit: vuelve a llevar sus propias existencias.");
      void qc.invalidateQueries({ queryKey: ["bundles"] });
    } catch (e) {
      setErrs(fieldErrors(e)); setErr(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  const isKit = q.data.components.length > 0;
  return (
    <Section title="Kit" description="Vende este producto como un conjunto de otros productos. No lleva existencias propias: se puede vender mientras haya de todos sus componentes, y cada venta, cancelación o devolución mueve las existencias de cada uno."
      actions={canEdit && <Button size="sm" disabled={!dirty || busy} onClick={() => void save()}>{busy ? "Guardando…" : "Guardar"}</Button>}>
      {skus.length > 1 && (
        <Field label="Variante" htmlFor="kit-sku">
          <select id="kit-sku" className="h-9 w-full rounded-md border bg-background px-2 text-sm" value={sku} onChange={(e) => setSku(e.target.value)}>
            {skus.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
        </Field>
      )}
      {isKit ? <Notice tone={q.data.available > 0 ? "ok" : "warn"}>Se pueden vender <strong>{q.data.available}</strong> kits con las existencias actuales.</Notice>
        : parts.length === 0 && <p className="text-sm text-muted-foreground">No es un kit: se vende con sus propias existencias.</p>}
      {parts.map((p, i) => (
        <div key={i} className="grid gap-2 rounded-md border p-3 sm:grid-cols-[1fr_7rem_auto] sm:items-end">
          <Field label="SKU del componente" htmlFor={`kit-c-${i}`} hint={errs[`components[${i}].sku`] || (p.title ? `${p.title} · ${p.available ?? 0} disponibles` : undefined)}>
            <Input id={`kit-c-${i}`} className="font-mono" value={p.sku} disabled={!canEdit} onChange={(e) => setPart(i, { sku: e.target.value, title: undefined })} />
          </Field>
          <Field label="Piezas por kit" htmlFor={`kit-q-${i}`} hint={errs[`components[${i}].qty`]}>
            <Input id={`kit-q-${i}`} inputMode="numeric" value={p.qty || ""} disabled={!canEdit} onChange={(e) => setPart(i, { qty: Number(e.target.value.replace(/\D/g, "")) || 0 })} />
          </Field>
          {canEdit && <Button type="button" size="icon" variant="ghost" aria-label={`Quitar ${p.sku || "componente"}`} onClick={() => setParts((x) => x.filter((_, j) => j !== i))}><Trash2 aria-hidden="true" /></Button>}
        </div>
      ))}
      {err && <p className="text-sm text-bad" role="alert">{err}</p>}
      {canEdit && parts.length < 30 && <div><Button type="button" variant="outline" size="sm" onClick={() => setParts((x) => [...x, { sku: "", qty: 1 }])}><Plus aria-hidden="true" /> Agregar componente</Button></div>}
    </Section>
  );
}

/** Quantity rules: minimum, maximum and multiples per order, for everyone or
 *  for the companies of one price group. The cart enforces them. */
interface CustomsData { hsCode?: string; origin?: string }

/** Customs data for import duties: tariff code and country of origin. */
function Customs({ id, canEdit }: { id: string; canEdit: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["product", id, "trade-customs"], queryFn: () => request<CustomsData>("GET", `/v1/product-classes/${encodeURIComponent(id)}`) });
  const [v, setV] = useState<CustomsData>({});
  const [busy, setBusy] = useState(false);
  const [errs, setErrs] = useState<Record<string, string>>({});
  useEffect(() => { if (q.data) setV({ hsCode: q.data.hsCode ?? "", origin: q.data.origin ?? "" }); }, [q.data]);
  if (!q.data) return null;
  const dirty = (v.hsCode ?? "") !== (q.data.hsCode ?? "") || (v.origin ?? "") !== (q.data.origin ?? "");
  async function save() {
    setBusy(true); setErrs({});
    try {
      await request("PUT", `/v1/product-classes/${encodeURIComponent(id)}`, { hsCode: v.hsCode ?? "", origin: (v.origin ?? "").toUpperCase() });
      toast.success("Datos aduanales guardados. Los aranceles ya se calculan con ellos.");
      void qc.invalidateQueries({ queryKey: ["product", id, "trade-customs"] });
    } catch (e) {
      setErrs(fieldErrors(e)); toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Section title="Aduanas" description="Para vender en otros países: con el código arancelario y el país de origen, Apexx calcula los impuestos de importación y el comprador los paga al comprar, no al recibir." actions={canEdit && <Button size="sm" disabled={!dirty || busy} onClick={() => void save()}>{busy ? "Guardando…" : "Guardar"}</Button>}>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Código arancelario (HS)" htmlFor="cu-hs" hint={errs.hsCode || "De 6 a 10 dígitos, por ejemplo 6109.10"}><Input id="cu-hs" className="font-mono" value={v.hsCode ?? ""} disabled={!canEdit} onChange={(e) => setV({ ...v, hsCode: e.target.value })} /></Field>
        <Field label="País de origen" htmlFor="cu-origin" hint={errs.origin || "Dos letras: MX, CN, US"}><Input id="cu-origin" className="font-mono uppercase" maxLength={2} value={v.origin ?? ""} disabled={!canEdit} onChange={(e) => setV({ ...v, origin: e.target.value.toUpperCase() })} /></Field>
      </div>
    </Section>
  );
}

function QtyRules({ id, canEdit }: { id: string; canEdit: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["product", id, "qty-rules"], queryFn: () => request<{ rules: QtyRule[] }>("GET", `/v1/products/${encodeURIComponent(id)}/quantity-rules`) });
  const [rules, setRules] = useState<QtyRule[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => { if (q.data) setRules(q.data.rules); }, [q.data]);
  if (!q.data) return null;
  const set = (i: number, p: Partial<QtyRule>) => setRules((r) => r.map((x, j) => (j === i ? { ...x, ...p } : x)));
  const num = (v: string) => Math.max(0, Number(v.replace(/\D/g, "")) || 0);
  async function save() {
    setBusy(true); setErr("");
    try {
      const r = await request<{ rules: QtyRule[] }>("PUT", `/v1/products/${encodeURIComponent(id)}/quantity-rules`, { rules });
      setRules(r.rules);
      toast.success("Reglas de cantidad guardadas. El carrito ya las aplica.");
      void qc.invalidateQueries({ queryKey: ["product", id, "qty-rules"] });
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  const dirty = JSON.stringify(rules) !== JSON.stringify(q.data.rules);
  return (
    <Section title="Cantidades por pedido" description="Por ejemplo, venta por caja de 12 o un mínimo de 6 piezas. Puedes poner una regla distinta para las empresas de un grupo de precios." actions={canEdit && <Button size="sm" disabled={!dirty || busy} onClick={() => void save()}>{busy ? "Guardando…" : "Guardar"}</Button>}>
      {rules.length === 0 && <p className="text-sm text-muted-foreground">Sin reglas: se puede comprar cualquier cantidad.</p>}
      {rules.map((r, i) => (
        <div key={i} className="grid gap-2 rounded-md border p-3 sm:grid-cols-4 sm:items-end">
          <Field label="Para" htmlFor={`qr-g-${i}`}><Input id={`qr-g-${i}`} value={r.group} disabled={!canEdit} placeholder="Todos" onChange={(e) => set(i, { group: e.target.value.trim() })} /></Field>
          <Field label="Mínimo" htmlFor={`qr-min-${i}`}><Input id={`qr-min-${i}`} inputMode="numeric" value={r.min || ""} disabled={!canEdit} placeholder="1" onChange={(e) => set(i, { min: num(e.target.value) })} /></Field>
          <Field label="Múltiplos de" htmlFor={`qr-step-${i}`}><Input id={`qr-step-${i}`} inputMode="numeric" value={r.step || ""} disabled={!canEdit} placeholder="1" onChange={(e) => set(i, { step: num(e.target.value) })} /></Field>
          <div className="flex items-end gap-2"><Field label="Máximo" htmlFor={`qr-max-${i}`}><Input id={`qr-max-${i}`} inputMode="numeric" value={r.max || ""} disabled={!canEdit} placeholder="Sin límite" onChange={(e) => set(i, { max: num(e.target.value) })} /></Field>{canEdit && <Button type="button" size="icon" variant="ghost" aria-label="Quitar regla" onClick={() => setRules((x) => x.filter((_, j) => j !== i))}><Trash2 aria-hidden="true" /></Button>}</div>
        </div>
      ))}
      {err && <p className="text-sm text-bad" role="alert">{err}</p>}
      {canEdit && <div><Button type="button" variant="outline" size="sm" onClick={() => setRules((x) => [...x, { group: x.some((y) => y.group === "") ? "mayoreo" : "", min: 0, max: 0, step: 0 }])}><Plus aria-hidden="true" /> Agregar regla</Button></div>}
      <p className="text-xs text-muted-foreground">“Para” vacío aplica a todos. Escribe el grupo de precios de una empresa (Empresas → grupo de precios) para una regla solo suya.</p>
    </Section>
  );
}
