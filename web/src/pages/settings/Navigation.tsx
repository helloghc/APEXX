import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ArrowDown, ArrowUp, IndentDecrease, IndentIncrease, Plus, Trash2 } from "lucide-react";
import { errorMessage, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { toast } from "@/components/ui/sonner";
import { cn } from "@/lib/utils";
import { can } from "@/perms";
import { href } from "@/router";
import { ErrorBox, Notice } from "../common";
import { sapi, type Menu, type MenuItem, type MenuItemType, type Target } from "./api";
import { Loading, ReadOnlyNote, SaveBar, Section, SubPage } from "./kit";
import { countItems, indentItem, moveItem, outdentItem, removeItem, updateItem, type Path } from "./logic";

const TYPES: { id: MenuItemType; label: string }[] = [
  { id: "home", label: "Inicio" }, { id: "catalog", label: "Todos los productos" }, { id: "category", label: "Categoría" }, { id: "product", label: "Producto" },
  { id: "page", label: "Página" }, { id: "policy", label: "Política" }, { id: "url", label: "Enlace externo" },
];
const newId = () => "mi_" + Math.random().toString(36).slice(2, 10);

/** Navigation: the menus of the storefront (header and footer), up to three levels. */
export function Navigation({ me, menuId }: { me: Me; menuId?: string }) {
  const q = useQuery({ queryKey: ["settings", "menus"], queryFn: sapi.menus });
  const canEdit = can(me, "settings", "edit");
  if (q.isPending || q.error) return <SubPage title="Navegación"><ErrorBox error={q.error} onRetry={() => void q.refetch()} />{!q.error && <Loading />}</SubPage>;
  const menus = q.data.items;
  const current = menus.find((m) => m.id === menuId) ?? menus[0];
  return (
    <SubPage title="Navegación" sub="Los menús que ven tus clientes arriba y abajo de la tienda. Los cambios se ven de inmediato, sin publicar.">
      <ReadOnlyNote show={!canEdit} need="Editor" />
      <div className="flex gap-1 border-b" role="tablist" aria-label="Menús">
        {menus.map((m) => (
          <a key={m.id} role="tab" aria-selected={m.id === current?.id} href={href("configuracion", "navegacion", m.id)}
            className={cn("-mb-px border-b-2 px-3 py-2 text-sm", m.id === current?.id ? "border-primary font-medium" : "border-transparent text-muted-foreground hover:text-foreground")}>{m.name}</a>
        ))}
      </div>
      {current ? <MenuEditor key={current.id} menu={current} targets={q.data.targets} maxDepth={q.data.maxDepth} canEdit={canEdit} /> : <Notice tone="ok">Todavía no hay menús.</Notice>}
    </SubPage>
  );
}

function MenuEditor({ menu, targets, maxDepth, canEdit }: { menu: Menu; targets: Record<string, Target[]>; maxDepth: number; canEdit: boolean }) {
  const qc = useQueryClient();
  const [items, setItems] = useState<MenuItem[]>(menu.items ?? []);
  const [saved, setSaved] = useState<MenuItem[]>(menu.items ?? []);
  const save = useMutation({
    mutationFn: () => sapi.saveMenu({ ...menu, items }),
    onSuccess: (m) => { toast.success("Menú guardado. Ya está en tu tienda."); setItems(m.items ?? []); setSaved(m.items ?? []); void qc.invalidateQueries({ queryKey: ["settings", "menus"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const add = () => setItems([...items, { id: newId(), label: "", type: "catalog", children: [] }]);
  const invalid = hasInvalid(items);
  const row = (it: MenuItem, path: Path, siblings: number) => {
    const i = path[path.length - 1] as number;
    const needsRef = ["category", "product", "page", "policy"].includes(it.type);
    const opts = targets[it.type] ?? [];
    return (
      <li key={it.id} className="grid gap-2">
        <div className="flex flex-wrap items-center gap-2 rounded-md border bg-card p-2" style={{ marginLeft: (path.length - 1) * 28 }}>
          <Input aria-label="Texto del enlace" className="w-44" value={it.label} placeholder="Texto" aria-invalid={!it.label.trim()} onChange={(e) => setItems(updateItem(items, path, { label: e.target.value }))} />
          <Select value={it.type} onValueChange={(v) => setItems(updateItem(items, path, { type: v as MenuItemType, ref: "", url: "" }))}>
            <SelectTrigger className="w-44" aria-label="Tipo de enlace"><SelectValue /></SelectTrigger>
            <SelectContent>{TYPES.map((t) => <SelectItem key={t.id} value={t.id}>{t.label}</SelectItem>)}</SelectContent>
          </Select>
          {needsRef && (
            <Select value={it.ref ?? ""} onValueChange={(v) => setItems(updateItem(items, path, { ref: v, label: it.label || opts.find((o) => o.ref === v)?.label || "" }))}>
              <SelectTrigger className="w-52" aria-label="Destino" aria-invalid={!it.ref}><SelectValue placeholder={opts.length ? "Elige…" : "No hay opciones"} /></SelectTrigger>
              <SelectContent>{opts.map((o) => <SelectItem key={o.ref} value={o.ref}>{o.label}</SelectItem>)}</SelectContent>
            </Select>
          )}
          {it.type === "url" && <Input aria-label="URL" className="w-60" value={it.url ?? ""} placeholder="https://…" aria-invalid={!/^https?:\/\/\S+$/.test(it.url ?? "")} onChange={(e) => setItems(updateItem(items, path, { url: e.target.value.trim() }))} />}
          <span className="ml-auto flex items-center">
            <Button type="button" size="icon" variant="ghost" aria-label="Subir" disabled={i === 0} onClick={() => setItems(moveItem(items, path, -1))}><ArrowUp aria-hidden="true" /></Button>
            <Button type="button" size="icon" variant="ghost" aria-label="Bajar" disabled={i === siblings - 1} onClick={() => setItems(moveItem(items, path, 1))}><ArrowDown aria-hidden="true" /></Button>
            <Button type="button" size="icon" variant="ghost" aria-label="Convertir en submenú del anterior" disabled={i === 0 || path.length >= maxDepth} onClick={() => setItems(indentItem(items, path, maxDepth))}><IndentIncrease aria-hidden="true" /></Button>
            <Button type="button" size="icon" variant="ghost" aria-label="Sacar del submenú" disabled={path.length === 1} onClick={() => setItems(outdentItem(items, path))}><IndentDecrease aria-hidden="true" /></Button>
            <Button type="button" size="icon" variant="ghost" aria-label={`Quitar ${it.label || "enlace"}`} onClick={() => setItems(removeItem(items, path))}><Trash2 aria-hidden="true" /></Button>
          </span>
        </div>
        {(it.children ?? []).length > 0 && <ul className="grid gap-2">{it.children.map((c, j) => row(c, [...path, j], it.children.length))}</ul>}
      </li>
    );
  };
  return (
    <>
      <fieldset disabled={!canEdit} className="grid min-w-0 gap-5">
        <Section title={menu.name} description={`${countItems(items)} enlaces. Usa las flechas para ordenar y la sangría para crear submenús (hasta ${maxDepth} niveles).`} actions={<Button type="button" variant="outline" onClick={add}><Plus aria-hidden="true" /> Agregar enlace</Button>}>
          {items.length === 0 ? <Notice tone="ok">Este menú está vacío. Agrega tu primer enlace.</Notice> : <ul className="grid gap-2">{items.map((it, i) => row(it, [i], items.length))}</ul>}
          {invalid && <Notice tone="warn">Completa el texto y el destino de cada enlace para poder guardar.</Notice>}
        </Section>
      </fieldset>
      <SaveBar dirty={canEdit && JSON.stringify(items) !== JSON.stringify(saved)} busy={save.isPending} onSave={() => !invalid && save.mutate()} onDiscard={() => setItems(saved)} />
    </>
  );
}

function hasInvalid(items: MenuItem[]): boolean {
  return items.some((it) => !it.label.trim() || (["category", "product", "page", "policy"].includes(it.type) && !it.ref) || (it.type === "url" && !/^https?:\/\/\S+$/.test(it.url ?? "")) || hasInvalid(it.children ?? []));
}
