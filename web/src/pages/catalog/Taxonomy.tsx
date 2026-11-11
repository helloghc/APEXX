import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { ArrowDown, ArrowUp, FolderTree, ListFilter, Pencil, Plus, Tag, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { href } from "@/router";
import { ErrorBox } from "../common";
import { cat, type Attribute, type Brand, type Category, type MediaItem } from "./api";
import { CatalogPage, Confirm, EmptyState, Field, MediaPicker, SkeletonRows, slug, TagInput, Thumb } from "./shared";

const ROOT = "__root";

// ---------- categories ----------

export function Categories({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "catalog", "edit");
  const list = useQuery({ queryKey: ["categories"], queryFn: cat.categories });
  const [edit, setEdit] = useState<Partial<Category> | null>(null);
  const [del, setDel] = useState<Category | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const items = list.data ?? [];
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["categories"] });
    void qc.invalidateQueries({ queryKey: ["catalog-summary"] });
  };

  async function move(c: Category, dir: -1 | 1) {
    const sibs = items.filter((x) => x.parentId === c.parentId).sort((a, b) => a.position - b.position);
    const i = sibs.findIndex((x) => x.id === c.id);
    const j = i + dir;
    if (j < 0 || j >= sibs.length) return;
    [sibs[i], sibs[j]] = [sibs[j]!, sibs[i]!];
    try {
      await cat.reorderCategories(sibs.map((s, position) => ({ id: s.id, parentId: s.parentId, position })));
      refresh();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!edit) return;
    setBusy(true);
    setErrors({});
    try {
      const body = { ...edit, handle: edit.handle || slug(edit.name ?? ""), active: edit.active ?? true, parentId: edit.parentId ?? "" };
      if (edit.id) await cat.updateCategory(edit.id, body);
      else await cat.createCategory(body);
      toast.success(edit.id ? "Categoría actualizada." : "Categoría creada.");
      setEdit(null);
      refresh();
    } catch (err) {
      const fe = fieldErrors(err);
      setErrors(Object.keys(fe).length ? fe : { name: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  }

  // A category cannot become a child of itself or of its descendants.
  const blocked = new Set<string>();
  if (edit?.id) {
    blocked.add(edit.id);
    let grew = true;
    while (grew) {
      grew = false;
      for (const c of items) if (blocked.has(c.parentId) && !blocked.has(c.id)) { blocked.add(c.id); grew = true; }
    }
  }

  return (
    <CatalogPage
      tab="categorias"
      title="Categorías"
      sub="Organiza el catálogo en un árbol. El orden se refleja en los menús y filtros de la tienda al instante."
      actions={canEdit && <Button onClick={() => { setErrors({}); setEdit({ active: true, parentId: "" }); }}><Plus aria-hidden="true" /> Nueva categoría</Button>}
    >
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      {!list.isLoading && items.length === 0 ? (
        <EmptyState icon={FolderTree} title="Aún no hay categorías" text="Crea la primera, por ejemplo “Calzado”, y luego agrega subcategorías como “Tenis”."
          action={canEdit && <Button onClick={() => setEdit({ active: true, parentId: "" })}><Plus aria-hidden="true" /> Crear categoría</Button>} />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Categoría</TableHead>
                <TableHead>Dirección</TableHead>
                <TableHead className="text-right">Productos</TableHead>
                <TableHead>Estado</TableHead>
                <TableHead className="w-40"><span className="sr-only">Acciones</span></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.isLoading ? <SkeletonRows cols={5} /> : items.map((c) => (
                <TableRow key={c.id}>
                  <TableCell>
                    <span style={{ paddingLeft: c.depth * 20 }} className="inline-flex items-center gap-2 font-medium">
                      {c.depth > 0 && <span className="text-muted-foreground" aria-hidden="true">└</span>}
                      {c.name}
                    </span>
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">/c/{c.handle}</TableCell>
                  <TableCell className="text-right tabular-nums">
                    <a className="hover:underline" href={href("catalogo") + `?category=${c.id}`}>{c.productsTotal}</a>
                  </TableCell>
                  <TableCell>{c.active ? <Badge variant="secondary">Visible</Badge> : <Badge variant="outline">Oculta</Badge>}</TableCell>
                  <TableCell>
                    {canEdit && (
                      <div className="flex justify-end">
                        <Button variant="ghost" size="icon" aria-label={`Subir ${c.name}`} onClick={() => void move(c, -1)}><ArrowUp aria-hidden="true" /></Button>
                        <Button variant="ghost" size="icon" aria-label={`Bajar ${c.name}`} onClick={() => void move(c, 1)}><ArrowDown aria-hidden="true" /></Button>
                        <Button variant="ghost" size="icon" aria-label={`Editar ${c.name}`} onClick={() => { setErrors({}); setEdit(c); }}><Pencil aria-hidden="true" /></Button>
                        <Button variant="ghost" size="icon" aria-label={`Eliminar ${c.name}`} onClick={() => setDel(c)}><Trash2 aria-hidden="true" /></Button>
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <Dialog open={!!edit} onOpenChange={(o) => !o && setEdit(null)}>
        <DialogContent>
          <form onSubmit={(e) => void submit(e)} className="grid gap-4">
            <DialogHeader>
              <DialogTitle>{edit?.id ? "Editar categoría" : "Nueva categoría"}</DialogTitle>
              <DialogDescription>Los cambios se ven en la tienda de inmediato.</DialogDescription>
            </DialogHeader>
            <Field label="Nombre" htmlFor="c-name" error={errors.name}>
              <Input id="c-name" autoFocus required maxLength={80} value={edit?.name ?? ""} onChange={(e) => setEdit({ ...edit, name: e.target.value })} />
            </Field>
            <Field label="Dentro de" htmlFor="c-parent" error={errors.parentId}>
              <Select value={edit?.parentId || ROOT} onValueChange={(v) => setEdit({ ...edit, parentId: v === ROOT ? "" : v })}>
                <SelectTrigger id="c-parent"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value={ROOT}>Nivel principal</SelectItem>
                  {items.filter((c) => !blocked.has(c.id)).map((c) => (
                    <SelectItem key={c.id} value={c.id}>{"— ".repeat(c.depth)}{c.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label="Dirección web" htmlFor="c-handle" error={errors.handle} hint={`/c/${edit?.handle || slug(edit?.name ?? "") || "…"}`}>
              <Input id="c-handle" className="font-mono text-sm" value={edit?.handle ?? ""} placeholder={slug(edit?.name ?? "")} onChange={(e) => setEdit({ ...edit, handle: slug(e.target.value) })} />
            </Field>
            <Field label="Descripción" htmlFor="c-desc">
              <Textarea id="c-desc" rows={2} value={edit?.description ?? ""} onChange={(e) => setEdit({ ...edit, description: e.target.value })} />
            </Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Título para buscadores" htmlFor="c-seot">
                <Input id="c-seot" maxLength={70} value={edit?.seoTitle ?? ""} onChange={(e) => setEdit({ ...edit, seoTitle: e.target.value })} />
              </Field>
              <Field label="Descripción para buscadores" htmlFor="c-seod">
                <Input id="c-seod" maxLength={160} value={edit?.seoDescription ?? ""} onChange={(e) => setEdit({ ...edit, seoDescription: e.target.value })} />
              </Field>
            </div>
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={edit?.active ?? true} onCheckedChange={(c) => setEdit({ ...edit, active: !!c })} /> Visible en la tienda
            </label>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setEdit(null)}>Cancelar</Button>
              <Button type="submit" disabled={busy}>{busy ? "Guardando…" : "Guardar"}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Confirm
        open={!!del}
        title={`¿Eliminar “${del?.name}”?`}
        description={del && del.productsTotal > 0 ? `Sus ${del.productsTotal} productos no se borran; solo dejan de pertenecer a esta categoría. Las subcategorías suben un nivel.` : "Las subcategorías suben un nivel."}
        confirmLabel="Eliminar"
        destructive
        onConfirm={() => {
          if (!del) return;
          cat.deleteCategory(del.id).then(() => { toast.success("Categoría eliminada."); refresh(); }).catch((e) => toast.error(errorMessage(e))).finally(() => setDel(null));
        }}
        onClose={() => setDel(null)}
      />
    </CatalogPage>
  );
}

// ---------- brands ----------

export function Brands({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "catalog", "edit");
  const list = useQuery({ queryKey: ["brands"], queryFn: cat.brands });
  const [edit, setEdit] = useState<Partial<Brand> | null>(null);
  const [logo, setLogo] = useState<MediaItem | null>(null);
  const [pickLogo, setPickLogo] = useState(false);
  const [del, setDel] = useState<Brand | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const items = list.data ?? [];
  const refresh = () => void qc.invalidateQueries({ queryKey: ["brands"] });
  const open = (b: Partial<Brand>) => { setErrors({}); setLogo(b.logo ?? null); setEdit(b); };

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!edit) return;
    setBusy(true);
    setErrors({});
    try {
      const body = { name: edit.name, handle: edit.handle || slug(edit.name ?? ""), description: edit.description ?? "", active: edit.active ?? true, logoMediaId: logo?.id ?? "" };
      if (edit.id) await cat.updateBrand(edit.id, body);
      else await cat.createBrand(body);
      toast.success(edit.id ? "Marca actualizada." : "Marca creada.");
      setEdit(null);
      refresh();
    } catch (err) {
      const fe = fieldErrors(err);
      setErrors(Object.keys(fe).length ? fe : { name: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <CatalogPage tab="marcas" title="Marcas" sub="Las marcas aparecen como filtro en la tienda y en la ficha de cada producto."
      actions={canEdit && <Button onClick={() => open({ active: true })}><Plus aria-hidden="true" /> Nueva marca</Button>}>
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      {!list.isLoading && items.length === 0 ? (
        <EmptyState icon={Tag} title="Aún no hay marcas" text="Crea las marcas que vendes para filtrar y organizar el catálogo."
          action={canEdit && <Button onClick={() => open({ active: true })}><Plus aria-hidden="true" /> Crear marca</Button>} />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Marca</TableHead>
                <TableHead>Dirección</TableHead>
                <TableHead className="text-right">Productos</TableHead>
                <TableHead>Estado</TableHead>
                <TableHead className="w-24"><span className="sr-only">Acciones</span></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.isLoading ? <SkeletonRows cols={5} /> : items.map((b) => (
                <TableRow key={b.id}>
                  <TableCell><span className="flex items-center gap-3 font-medium"><Thumb media={b.logo} size={36} /> {b.name}</span></TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{b.handle}</TableCell>
                  <TableCell className="text-right tabular-nums">{b.products}</TableCell>
                  <TableCell>{b.active ? <Badge variant="secondary">Visible</Badge> : <Badge variant="outline">Oculta</Badge>}</TableCell>
                  <TableCell>
                    {canEdit && (
                      <div className="flex justify-end">
                        <Button variant="ghost" size="icon" aria-label={`Editar ${b.name}`} onClick={() => open(b)}><Pencil aria-hidden="true" /></Button>
                        <Button variant="ghost" size="icon" aria-label={`Eliminar ${b.name}`} onClick={() => setDel(b)}><Trash2 aria-hidden="true" /></Button>
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <Dialog open={!!edit} onOpenChange={(o) => !o && setEdit(null)}>
        <DialogContent>
          <form onSubmit={(e) => void submit(e)} className="grid gap-4">
            <DialogHeader>
              <DialogTitle>{edit?.id ? "Editar marca" : "Nueva marca"}</DialogTitle>
              <DialogDescription>El logo es opcional.</DialogDescription>
            </DialogHeader>
            <div className="flex items-center gap-3">
              <Thumb media={logo} size={64} />
              <Button type="button" variant="outline" size="sm" onClick={() => setPickLogo(true)}>{logo ? "Cambiar logo" : "Elegir logo"}</Button>
              {logo && <Button type="button" variant="ghost" size="sm" onClick={() => setLogo(null)}>Quitar</Button>}
            </div>
            <Field label="Nombre" htmlFor="b-name" error={errors.name}>
              <Input id="b-name" autoFocus required maxLength={80} value={edit?.name ?? ""} onChange={(e) => setEdit({ ...edit, name: e.target.value })} />
            </Field>
            <Field label="Dirección web" htmlFor="b-handle" error={errors.handle}>
              <Input id="b-handle" className="font-mono text-sm" value={edit?.handle ?? ""} placeholder={slug(edit?.name ?? "")} onChange={(e) => setEdit({ ...edit, handle: slug(e.target.value) })} />
            </Field>
            <Field label="Descripción" htmlFor="b-desc">
              <Textarea id="b-desc" rows={2} value={edit?.description ?? ""} onChange={(e) => setEdit({ ...edit, description: e.target.value })} />
            </Field>
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={edit?.active ?? true} onCheckedChange={(c) => setEdit({ ...edit, active: !!c })} /> Visible en la tienda
            </label>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setEdit(null)}>Cancelar</Button>
              <Button type="submit" disabled={busy}>{busy ? "Guardando…" : "Guardar"}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <MediaPicker open={pickLogo} multiple={false} title="Elegir logo" onClose={() => setPickLogo(false)} onPick={(m) => { setLogo(m[0] ?? null); setPickLogo(false); }} />
      <Confirm open={!!del} title={`¿Eliminar la marca “${del?.name}”?`}
        description={del && del.products > 0 ? `Sus ${del.products} productos quedan sin marca.` : "Ningún producto la usa."} confirmLabel="Eliminar" destructive
        onConfirm={() => { if (!del) return; cat.deleteBrand(del.id).then(() => { toast.success("Marca eliminada."); refresh(); }).catch((e) => toast.error(errorMessage(e))).finally(() => setDel(null)); }}
        onClose={() => setDel(null)} />
    </CatalogPage>
  );
}

// ---------- attributes ----------

const KIND_LABEL: Record<Attribute["kind"], string> = { text: "Texto", number: "Número", boolean: "Sí / No", select: "Lista de opciones" };

export function Attributes({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "catalog", "edit");
  const list = useQuery({ queryKey: ["attributes"], queryFn: cat.attributes });
  const [edit, setEdit] = useState<Partial<Attribute> | null>(null);
  const [del, setDel] = useState<Attribute | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const items = list.data ?? [];
  const refresh = () => void qc.invalidateQueries({ queryKey: ["attributes"] });
  const fresh = (): Partial<Attribute> => ({ kind: "text", filterable: true, options: [], position: items.length });

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!edit) return;
    if (edit.kind === "select" && !(edit.options ?? []).length) {
      setErrors({ options: "Agrega al menos una opción." });
      return;
    }
    setBusy(true);
    setErrors({});
    try {
      if (edit.id) await cat.updateAttribute(edit.id, edit);
      else await cat.createAttribute(edit);
      toast.success(edit.id ? "Atributo actualizado." : "Atributo creado.");
      setEdit(null);
      refresh();
    } catch (err) {
      const fe = fieldErrors(err);
      setErrors(Object.keys(fe).length ? fe : { name: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <CatalogPage tab="atributos" title="Atributos" sub="Especificaciones de tus productos, como Material o Capacidad. Los filtrables se convierten en filtros de la tienda."
      actions={canEdit && <Button onClick={() => { setErrors({}); setEdit(fresh()); }}><Plus aria-hidden="true" /> Nuevo atributo</Button>}>
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      {!list.isLoading && items.length === 0 ? (
        <EmptyState icon={ListFilter} title="Aún no hay atributos" text="Crea atributos como “Material” o “Color” para describir productos y ofrecer filtros."
          action={canEdit && <Button onClick={() => setEdit(fresh())}><Plus aria-hidden="true" /> Crear atributo</Button>} />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Atributo</TableHead>
                <TableHead>Tipo</TableHead>
                <TableHead>Opciones</TableHead>
                <TableHead>Filtro en tienda</TableHead>
                <TableHead className="text-right">Productos</TableHead>
                <TableHead className="w-24"><span className="sr-only">Acciones</span></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.isLoading ? <SkeletonRows cols={6} /> : items.map((a) => (
                <TableRow key={a.id}>
                  <TableCell className="font-medium">{a.name}{a.unit ? <span className="text-muted-foreground"> ({a.unit})</span> : null}</TableCell>
                  <TableCell>{KIND_LABEL[a.kind]}</TableCell>
                  <TableCell className="max-w-64 truncate text-muted-foreground">{(a.options ?? []).join(", ") || "—"}</TableCell>
                  <TableCell>{a.filterable ? <Badge variant="secondary">Sí</Badge> : <span className="text-muted-foreground">No</span>}</TableCell>
                  <TableCell className="text-right tabular-nums">{a.products}</TableCell>
                  <TableCell>
                    {canEdit && (
                      <div className="flex justify-end">
                        <Button variant="ghost" size="icon" aria-label={`Editar ${a.name}`} onClick={() => { setErrors({}); setEdit(a); }}><Pencil aria-hidden="true" /></Button>
                        <Button variant="ghost" size="icon" aria-label={`Eliminar ${a.name}`} onClick={() => setDel(a)}><Trash2 aria-hidden="true" /></Button>
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <Dialog open={!!edit} onOpenChange={(o) => !o && setEdit(null)}>
        <DialogContent>
          <form onSubmit={(e) => void submit(e)} className="grid gap-4">
            <DialogHeader>
              <DialogTitle>{edit?.id ? "Editar atributo" : "Nuevo atributo"}</DialogTitle>
              <DialogDescription>Después lo llenas en cada producto.</DialogDescription>
            </DialogHeader>
            <Field label="Nombre" htmlFor="a-name" error={errors.name}>
              <Input id="a-name" autoFocus required maxLength={60} value={edit?.name ?? ""} onChange={(e) => setEdit({ ...edit, name: e.target.value })} />
            </Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Tipo" htmlFor="a-kind" error={errors.kind}>
                <Select value={edit?.kind ?? "text"} onValueChange={(v) => setEdit({ ...edit, kind: v as Attribute["kind"] })} disabled={!!edit?.id}>
                  <SelectTrigger id="a-kind"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {(Object.keys(KIND_LABEL) as Attribute["kind"][]).map((k) => <SelectItem key={k} value={k}>{KIND_LABEL[k]}</SelectItem>)}
                  </SelectContent>
                </Select>
              </Field>
              {edit?.kind === "number" && (
                <Field label="Unidad" htmlFor="a-unit" hint="Opcional: cm, kg, GB…">
                  <Input id="a-unit" maxLength={12} value={edit?.unit ?? ""} onChange={(e) => setEdit({ ...edit, unit: e.target.value })} />
                </Field>
              )}
            </div>
            {edit?.kind === "select" && (
              <Field label="Opciones" htmlFor="a-opts" error={errors.options}>
                <TagInput id="a-opts" value={edit.options ?? []} onChange={(options) => setEdit({ ...edit, options })} placeholder="Escribe una opción y Enter" />
              </Field>
            )}
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={edit?.filterable ?? false} onCheckedChange={(c) => setEdit({ ...edit, filterable: !!c })} /> Mostrar como filtro en la tienda
            </label>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setEdit(null)}>Cancelar</Button>
              <Button type="submit" disabled={busy}>{busy ? "Guardando…" : "Guardar"}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Confirm open={!!del} title={`¿Eliminar el atributo “${del?.name}”?`}
        description={del && del.products > 0 ? `Se borra su valor en ${del.products} productos.` : "Ningún producto lo usa."} confirmLabel="Eliminar" destructive
        onConfirm={() => { if (!del) return; cat.deleteAttribute(del.id).then(() => { toast.success("Atributo eliminado."); refresh(); }).catch((e) => toast.error(errorMessage(e))).finally(() => setDel(null)); }}
        onClose={() => setDel(null)} />
    </CatalogPage>
  );
}
