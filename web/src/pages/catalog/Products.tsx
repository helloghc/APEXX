import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { ArrowDown, ArrowUp, ArrowUpDown, Boxes, Columns3, Download, Plus, Search, SlidersHorizontal, X } from "lucide-react";
import { errorMessage, type Me } from "@/api";
import { prefetchOnIntent } from "./productQueries";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "@/components/ui/sonner";
import { cn } from "@/lib/utils";
import { formatMoney } from "@/money";
import { can } from "@/perms";
import { href } from "@/router";
import { dateTime, ErrorBox, StatusPill } from "../common";
import { cat, download, productParams, type ProductQuery, type ProductRow } from "./api";
import { CatalogPage, Confirm, EmptyState, SkeletonRows, Thumb, useDebounced } from "./shared";

type ColumnId = "status" | "stock" | "price" | "brand" | "category" | "variants" | "updated";
const COLUMNS: { id: ColumnId; label: string; sort?: string; right?: boolean }[] = [
  { id: "status", label: "Estado", sort: "status" },
  { id: "stock", label: "Existencias", sort: "stock", right: true },
  { id: "price", label: "Precio", sort: "price", right: true },
  { id: "brand", label: "Marca" },
  { id: "category", label: "Categoría" },
  { id: "variants", label: "Variantes", right: true },
  { id: "updated", label: "Actualizado", sort: "updated" },
];
const DEFAULT_COLS: ColumnId[] = ["status", "stock", "price", "brand", "category"];
const COLS_KEY = "apexx.catalog.columns";
const FILTER_KEY = "apexx.catalog.filters";

function load<T>(key: string, fallback: T, storage: () => Storage): T {
  try {
    const raw = storage().getItem(key);
    return raw ? (JSON.parse(raw) as T) : fallback;
  } catch {
    return fallback;
  }
}
function save(key: string, value: unknown, storage: () => Storage) {
  try {
    storage().setItem(key, JSON.stringify(value));
  } catch {
    /* storage blocked: the preference lasts for this visit */
  }
}

export function stockLabel(n: number): { text: string; tone: "ok" | "warn" | "bad" } {
  if (n <= 0) return { text: "Agotado", tone: "bad" };
  if (n <= 5) return { text: `Quedan ${n}`, tone: "warn" };
  return { text: n.toLocaleString("es-MX"), tone: "ok" };
}

export function priceRange(p: Pick<ProductRow, "priceMin" | "priceMax" | "currency">): string {
  if (p.priceMin == null) return "Sin precio";
  if (p.priceMax != null && p.priceMax !== p.priceMin) return `${formatMoney(p.priceMin, p.currency).replace(` ${p.currency}`, "")} – ${formatMoney(p.priceMax, p.currency)}`;
  return formatMoney(p.priceMin, p.currency);
}

type Bulk = null | "status" | "category_add" | "category_remove" | "brand" | "price_percent" | "delete";

export function Products({ me }: { me: Me }) {
  const qc = useQueryClient();
  const editable = can(me, "catalog", "edit");
  const [filters, setFilters] = useState<ProductQuery>(() => load<ProductQuery>(FILTER_KEY, { sort: "-updated" }, () => window.sessionStorage));
  const [search, setSearch] = useState(filters.q ?? "");
  const q = useDebounced(search.trim(), 300);
  const [cols, setCols] = useState<ColumnId[]>(() => load<ColumnId[]>(COLS_KEY, DEFAULT_COLS, () => window.localStorage));
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulk, setBulk] = useState<Bulk>(null);
  const [showFilters, setShowFilters] = useState(false);

  const query: ProductQuery = useMemo(() => ({ ...filters, q }), [filters, q]);
  useEffect(() => save(FILTER_KEY, query, () => window.sessionStorage), [query]);
  useEffect(() => save(COLS_KEY, cols, () => window.localStorage), [cols]);
  useEffect(() => setSelected(new Set()), [query]);

  const list = useInfiniteQuery({
    queryKey: ["products", query],
    queryFn: ({ pageParam }) => cat.products(query, pageParam),
    initialPageParam: "",
    getNextPageParam: (l) => l.nextCursor || undefined,
    placeholderData: (prev) => prev,
  });
  const categories = useQuery({ queryKey: ["categories"], queryFn: cat.categories });
  const brands = useQuery({ queryKey: ["brands"], queryFn: cat.brands });
  const summary = useQuery({ queryKey: ["catalog-summary"], queryFn: cat.summary });

  const rows = useMemo(() => list.data?.pages.flatMap((p) => p.items) ?? [], [list.data]);
  const total = list.data?.pages[0]?.total ?? 0;
  const catalogTotal = list.data?.pages[0]?.catalogTotal ?? summary.data?.products ?? 0;
  const catName = useMemo(() => new Map((categories.data ?? []).map((c) => [c.id, c.name])), [categories.data]);
  const activeFilters = (["status", "category", "brand", "stock"] as const).filter((k) => filters[k]);
  const filtered = activeFilters.length > 0 || q !== "";

  const setFilter = (k: keyof ProductQuery, v: string) => setFilters((f) => ({ ...f, [k]: v === "all" ? undefined : v }));
  const clear = () => {
    setFilters((f) => ({ sort: f.sort }));
    setSearch("");
  };
  const sortBy = (key: string) => setFilters((f) => ({ ...f, sort: f.sort === key ? `-${key}` : f.sort === `-${key}` ? key : key === "updated" || key === "price" || key === "stock" ? `-${key}` : key }));
  const sortIcon = (key: string) => (filters.sort === key ? <ArrowUp className="size-3.5" /> : filters.sort === `-${key}` ? <ArrowDown className="size-3.5" /> : <ArrowUpDown className="size-3.5 opacity-40" />);
  const ariaSort = (key: string) => (filters.sort === key ? "ascending" : filters.sort === `-${key}` ? "descending" : "none");

  const allSelected = rows.length > 0 && rows.every((r) => selected.has(r.id));
  const toggleAll = () => setSelected(allSelected ? new Set() : new Set(rows.map((r) => r.id)));
  const toggle = (id: string) =>
    setSelected((cur) => {
      const n = new Set(cur);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["products"] });
    void qc.invalidateQueries({ queryKey: ["catalog-summary"] });
    void qc.invalidateQueries({ queryKey: ["categories"] });
    void qc.invalidateQueries({ queryKey: ["brands"] });
  };

  const filterControls = (
    <>
      <Select value={filters.status ?? "all"} onValueChange={(v) => setFilter("status", v)}>
        <SelectTrigger className="w-full sm:w-40" aria-label="Filtrar por estado">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="all">Todos los estados</SelectItem>
          <SelectItem value="active">Activos</SelectItem>
          <SelectItem value="draft">Borradores</SelectItem>
          <SelectItem value="archived">Archivados</SelectItem>
        </SelectContent>
      </Select>
      <Select value={filters.category ?? "all"} onValueChange={(v) => setFilter("category", v)}>
        <SelectTrigger className="w-full sm:w-48" aria-label="Filtrar por categoría">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="all">Todas las categorías</SelectItem>
          <SelectItem value="none">Sin categoría</SelectItem>
          {(categories.data ?? []).map((c) => (
            <SelectItem key={c.id} value={c.id}>
              {"  ".repeat(c.depth)}
              {c.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Select value={filters.brand ?? "all"} onValueChange={(v) => setFilter("brand", v)}>
        <SelectTrigger className="w-full sm:w-40" aria-label="Filtrar por marca">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="all">Todas las marcas</SelectItem>
          <SelectItem value="none">Sin marca</SelectItem>
          {(brands.data ?? []).map((b) => (
            <SelectItem key={b.id} value={b.id}>
              {b.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Select value={filters.stock ?? "all"} onValueChange={(v) => setFilter("stock", v)}>
        <SelectTrigger className="w-full sm:w-44" aria-label="Filtrar por existencias">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="all">Cualquier existencia</SelectItem>
          <SelectItem value="in">Con existencias</SelectItem>
          <SelectItem value="low">Por agotarse (1 a 5)</SelectItem>
          <SelectItem value="out">Agotados</SelectItem>
        </SelectContent>
      </Select>
    </>
  );

  const s = summary.data;
  return (
    <CatalogPage
      tab=""
      wide
      title="Catálogo"
      sub={
        s ? (
          <span>
            {s.products.toLocaleString("es-MX")} productos · {(s.byStatus.active ?? 0).toLocaleString("es-MX")} activos · {(s.byStatus.draft ?? 0).toLocaleString("es-MX")} borradores
            {s.outOfStock > 0 && <> · {s.outOfStock.toLocaleString("es-MX")} agotados</>}
          </span>
        ) : (
          "Productos, variantes, precios y existencias"
        )
      }
      actions={
        <>
          <Button variant="outline" onClick={() => void download(`/v1/catalog/export.csv?${productParams(query)}`, "productos.csv").catch((e) => toast.error(errorMessage(e)))}>
            <Download /> Exportar
          </Button>
          {editable && (
            <Button asChild>
              <a href={href("catalogo", "productos", "nuevo")}>
                <Plus /> Nuevo producto
              </a>
            </Button>
          )}
        </>
      }
    >
      <div className="grid gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative min-w-0 flex-1 basis-64">
            <Search className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden="true" />
            <Input
              type="search"
              className="pl-8"
              placeholder="Buscar por nombre, SKU, marca o código de barras"
              aria-label="Buscar productos"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <div className="hidden flex-wrap items-center gap-2 lg:flex">{filterControls}</div>
          <Button variant="outline" className="lg:hidden" onClick={() => setShowFilters((v) => !v)} aria-expanded={showFilters}>
            <SlidersHorizontal /> Filtros{activeFilters.length > 0 && ` (${activeFilters.length})`}
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" aria-label="Elegir columnas">
                <Columns3 /> <span className="hidden sm:inline">Columnas</span>
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuLabel>Columnas visibles</DropdownMenuLabel>
              <DropdownMenuSeparator />
              {COLUMNS.map((c) => (
                <DropdownMenuCheckboxItem
                  key={c.id}
                  checked={cols.includes(c.id)}
                  onSelect={(e) => e.preventDefault()}
                  onCheckedChange={(on) => setCols((cur) => (on ? COLUMNS.map((x) => x.id).filter((x) => cur.includes(x) || x === c.id) : cur.filter((x) => x !== c.id)))}
                >
                  {c.label}
                </DropdownMenuCheckboxItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
        {showFilters && <div className="grid gap-2 sm:grid-cols-2 lg:hidden">{filterControls}</div>}
        {filtered && (
          <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
            <span aria-live="polite">
              {total.toLocaleString("es-MX")} de {catalogTotal.toLocaleString("es-MX")} productos
            </span>
            <Button variant="ghost" size="sm" onClick={clear}>
              <X /> Quitar filtros
            </Button>
          </div>
        )}
      </div>

      {selected.size > 0 && editable && (
        <div className="sticky top-2 z-10 flex flex-wrap items-center gap-2 rounded-xl border bg-card px-3 py-2 shadow-md" role="region" aria-label="Acciones para la selección">
          <strong className="text-sm">{selected.size} seleccionados</strong>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button size="sm">Editar selección</Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              <DropdownMenuItem onSelect={() => setBulk("status")}>Cambiar estado…</DropdownMenuItem>
              <DropdownMenuItem onSelect={() => setBulk("category_add")}>Agregar a una categoría…</DropdownMenuItem>
              <DropdownMenuItem onSelect={() => setBulk("category_remove")}>Quitar de una categoría…</DropdownMenuItem>
              <DropdownMenuItem onSelect={() => setBulk("brand")}>Cambiar marca…</DropdownMenuItem>
              {can(me, "pricing", "edit") && <DropdownMenuItem onSelect={() => setBulk("price_percent")}>Subir o bajar precios (%)…</DropdownMenuItem>}
              {can(me, "catalog", "admin") && <DropdownMenuItem className="text-destructive" onSelect={() => setBulk("delete")}>Eliminar…</DropdownMenuItem>}
            </DropdownMenuContent>
          </DropdownMenu>
          <Button
            size="sm"
            variant="outline"
            onClick={() => void download(`/v1/catalog/export.csv?ids=${encodeURIComponent([...selected].join(","))}`, "productos.csv").catch((e) => toast.error(errorMessage(e)))}
          >
            <Download /> Exportar selección
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
            Deseleccionar
          </Button>
        </div>
      )}

      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />

      {!list.isPending && rows.length === 0 && !list.error ? (
        filtered ? (
          <EmptyState icon={Search} title="Ningún producto coincide" text="Prueba con otra palabra o quita algún filtro." action={<Button variant="outline" onClick={clear}>Quitar filtros</Button>} />
        ) : (
          <EmptyState
            icon={Boxes}
            title="Todavía no hay productos"
            text="Crea tu primer producto o importa tu catálogo desde un archivo CSV."
            action={
              editable && (
                <div className="flex flex-wrap justify-center gap-2">
                  <Button asChild>
                    <a href={href("catalogo", "productos", "nuevo")}>
                      <Plus /> Nuevo producto
                    </a>
                  </Button>
                  <Button asChild variant="outline">
                    <a href={href("catalogo", "importar")}>Importar CSV</a>
                  </Button>
                </div>
              )
            }
          />
        )
      ) : (
        <Table className={cn(list.isFetching && !list.isFetchingNextPage && "opacity-70 transition-opacity")}>
          <TableHeader>
            <TableRow>
              {editable && (
                <TableHead className="w-10">
                  <Checkbox checked={allSelected} onCheckedChange={toggleAll} aria-label="Seleccionar todos los productos cargados" />
                </TableHead>
              )}
              <TableHead aria-sort={ariaSort("title")}>
                <button type="button" className="inline-flex items-center gap-1 hover:text-foreground" onClick={() => sortBy("title")}>
                  Producto {sortIcon("title")}
                </button>
              </TableHead>
              {COLUMNS.filter((c) => cols.includes(c.id)).map((c) => (
                <TableHead key={c.id} className={cn(c.right && "text-right", "whitespace-nowrap")} aria-sort={c.sort ? ariaSort(c.sort) : undefined}>
                  {c.sort ? (
                    <button type="button" className="inline-flex items-center gap-1 hover:text-foreground" onClick={() => sortBy(c.sort!)}>
                      {c.label} {sortIcon(c.sort)}
                    </button>
                  ) : (
                    c.label
                  )}
                </TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {list.isPending && <SkeletonRows cols={cols.length + (editable ? 2 : 1)} rows={8} />}
            {rows.map((p) => {
              const st = stockLabel(p.stock);
              const names = (p.categories ?? []).map((c) => catName.get(c) ?? c);
              return (
                <TableRow key={p.id} data-state={selected.has(p.id) ? "selected" : undefined} className="data-[state=selected]:bg-accent/50">
                  {editable && (
                    <TableCell className="align-middle">
                      <Checkbox checked={selected.has(p.id)} onCheckedChange={() => toggle(p.id)} aria-label={`Seleccionar ${p.title}`} />
                    </TableCell>
                  )}
                  <TableCell>
                    <div className="flex min-w-48 items-center gap-3">
                      <Thumb media={p.thumb} />
                      <div className="min-w-0">
                        <a className="font-medium text-foreground hover:text-primary hover:underline" href={href("catalogo", "productos", p.id)} {...prefetchOnIntent(qc, p.id)}>
                          {p.title}
                        </a>
                        <div className="truncate text-xs text-muted-foreground">
                          {p.variantCount === 1 ? p.variants?.[0]?.sku : `${p.variantCount} variantes`}
                          {p.type ? ` · ${p.type}` : ""}
                        </div>
                      </div>
                    </div>
                  </TableCell>
                  {cols.includes("status") && (
                    <TableCell className="align-middle">
                      <StatusPill status={p.status} />
                      {p.publishAt && <div className="mt-1 text-xs text-muted-foreground">Se publica {dateTime(p.publishAt)}</div>}
                    </TableCell>
                  )}
                  {cols.includes("stock") && (
                    <TableCell className="text-right align-middle tabular-nums">
                      {st.tone === "ok" ? st.text : <Badge variant={st.tone}>{st.text}</Badge>}
                    </TableCell>
                  )}
                  {cols.includes("price") && <TableCell className="whitespace-nowrap text-right align-middle tabular-nums">{priceRange(p)}</TableCell>}
                  {cols.includes("brand") && <TableCell className="align-middle">{p.brand || <span className="text-muted-foreground">—</span>}</TableCell>}
                  {cols.includes("category") && (
                    <TableCell className="align-middle">
                      {names.length ? (
                        <span title={names.join(", ")}>
                          {names[0]}
                          {names.length > 1 && <span className="text-muted-foreground"> +{names.length - 1}</span>}
                        </span>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </TableCell>
                  )}
                  {cols.includes("variants") && <TableCell className="text-right align-middle tabular-nums">{p.variantCount}</TableCell>}
                  {cols.includes("updated") && <TableCell className="whitespace-nowrap align-middle text-muted-foreground">{dateTime(p.updatedAt)}</TableCell>}
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      )}

      {rows.length > 0 && (
        <div className="flex flex-wrap items-center justify-between gap-3 text-sm text-muted-foreground">
          <span>
            Mostrando {rows.length.toLocaleString("es-MX")} de {total.toLocaleString("es-MX")}
          </span>
          {list.hasNextPage && (
            <Button variant="outline" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>
              {list.isFetchingNextPage ? "Cargando…" : "Cargar más"}
            </Button>
          )}
        </div>
      )}

      <BulkDialog
        action={bulk}
        ids={[...selected]}
        categories={(categories.data ?? []).map((c) => ({ id: c.id, label: "  ".repeat(c.depth) + c.name }))}
        brands={(brands.data ?? []).map((b) => ({ id: b.id, label: b.name }))}
        onClose={() => setBulk(null)}
        onDone={() => {
          setBulk(null);
          setSelected(new Set());
          refresh();
        }}
      />
    </CatalogPage>
  );
}

function BulkDialog({
  action,
  ids,
  categories,
  brands,
  onClose,
  onDone,
}: {
  action: Bulk;
  ids: string[];
  categories: { id: string; label: string }[];
  brands: { id: string; label: string }[];
  onClose: () => void;
  onDone: () => void;
}) {
  const [status, setStatus] = useState("active");
  const [categoryId, setCategoryId] = useState("");
  const [brandId, setBrandId] = useState("none");
  const [direction, setDirection] = useState<"down" | "up">("down");
  const [percent, setPercent] = useState("10");
  const [keepOld, setKeepOld] = useState(true);
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => {
    setErr(null);
    setConfirm(false);
  }, [action]);

  const n = ids.length;
  const pct = Number(percent.replace(",", "."));
  const pctOk = Number.isFinite(pct) && pct > 0 && (direction === "down" ? pct <= 90 : pct <= 1000);
  const titles: Record<Exclude<Bulk, null>, string> = {
    status: "Cambiar estado",
    category_add: "Agregar a una categoría",
    category_remove: "Quitar de una categoría",
    brand: "Cambiar marca",
    price_percent: "Subir o bajar precios",
    delete: "Eliminar productos",
  };
  const valid = action === "status" || action === "brand" || (action === "price_percent" ? pctOk : categoryId !== "");

  async function run() {
    if (!action) return;
    setBusy(true);
    setErr(null);
    try {
      const r = await cat.bulk({
        ids,
        action,
        status: action === "status" ? status : undefined,
        categoryId: action === "category_add" || action === "category_remove" ? categoryId : undefined,
        brandId: action === "brand" ? (brandId === "none" ? "" : brandId) : undefined,
        percent: action === "price_percent" ? (direction === "down" ? -pct : pct) : undefined,
        keepOldPrice: action === "price_percent" ? direction === "down" && keepOld : undefined,
      });
      toast.success(
        action === "delete"
          ? `${r.changed} ${r.changed === 1 ? "producto eliminado" : "productos eliminados"}`
          : action === "price_percent"
          ? `Precios actualizados en ${r.changed} ${r.changed === 1 ? "variante" : "variantes"}`
          : r.changed === 0
            ? "Nada que cambiar: la selección ya estaba así"
            : `${r.changed} ${r.changed === 1 ? "producto actualizado" : "productos actualizados"}`,
      );
      onDone();
    } catch (e) {
      setErr(errorMessage(e));
      setConfirm(false);
      if (action === "delete") toast.error(errorMessage(e)); // no options dialog to show it in
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <Dialog open={action !== null && action !== "delete" && !confirm} onOpenChange={(v) => !v && !busy && onClose()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{action ? titles[action] : ""}</DialogTitle>
            <DialogDescription>
              Se aplica a {n} {n === 1 ? "producto seleccionado" : "productos seleccionados"}.
            </DialogDescription>
          </DialogHeader>
          {action === "status" && (
            <div className="grid gap-1.5">
              <Label id="bulk-status">Nuevo estado</Label>
              <Select value={status} onValueChange={setStatus}>
                <SelectTrigger aria-labelledby="bulk-status">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="active">Activo (a la venta)</SelectItem>
                  <SelectItem value="draft">Borrador (oculto)</SelectItem>
                  <SelectItem value="archived">Archivado</SelectItem>
                </SelectContent>
              </Select>
            </div>
          )}
          {(action === "category_add" || action === "category_remove") && (
            <div className="grid gap-1.5">
              <Label id="bulk-cat">Categoría</Label>
              {categories.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  Todavía no hay categorías.{" "}
                  <a className="text-primary underline" href={href("catalogo", "categorias")}>
                    Crea la primera
                  </a>
                  .
                </p>
              ) : (
                <Select value={categoryId} onValueChange={setCategoryId}>
                  <SelectTrigger aria-labelledby="bulk-cat">
                    <SelectValue placeholder="Elige una categoría" />
                  </SelectTrigger>
                  <SelectContent>
                    {categories.map((c) => (
                      <SelectItem key={c.id} value={c.id}>
                        {c.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </div>
          )}
          {action === "brand" && (
            <div className="grid gap-1.5">
              <Label id="bulk-brand">Marca</Label>
              <Select value={brandId} onValueChange={setBrandId}>
                <SelectTrigger aria-labelledby="bulk-brand">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">Sin marca</SelectItem>
                  {brands.map((b) => (
                    <SelectItem key={b.id} value={b.id}>
                      {b.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
          {action === "price_percent" && (
            <div className="grid gap-3">
              <div className="grid grid-cols-[1fr_120px] gap-3">
                <div className="grid gap-1.5">
                  <Label id="bulk-dir">Cambio</Label>
                  <Select value={direction} onValueChange={(v) => setDirection(v as "down" | "up")}>
                    <SelectTrigger aria-labelledby="bulk-dir">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="down">Bajar precios</SelectItem>
                      <SelectItem value="up">Subir precios</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="grid gap-1.5">
                  <Label htmlFor="bulk-pct">Porcentaje</Label>
                  <Input id="bulk-pct" inputMode="decimal" value={percent} onChange={(e) => setPercent(e.target.value)} aria-invalid={!pctOk || undefined} />
                </div>
              </div>
              {!pctOk && <p className="text-xs font-medium text-destructive">Escribe un porcentaje mayor que 0{direction === "down" ? " y hasta 90" : " y hasta 1000"}.</p>}
              {direction === "down" && (
                <label className="flex items-start gap-2 text-sm">
                  <Checkbox checked={keepOld} onCheckedChange={(v) => setKeepOld(v === true)} className="mt-0.5" />
                  <span>
                    Mostrar el precio actual como “precio anterior” (tachado) en la tienda
                    <span className="block text-xs text-muted-foreground">Solo en las variantes que todavía no tienen precio anterior.</span>
                  </span>
                </label>
              )}
              {pctOk && (
                <p className="rounded-md bg-muted px-3 py-2 text-sm">
                  Ejemplo: un precio de $1,000.00 queda en <strong>{formatMoney(Math.round(100000 * (1 + (direction === "down" ? -pct : pct) / 100)))}</strong>.
                </p>
              )}
            </div>
          )}
          {err && (
            <p className="text-sm font-medium text-destructive" role="alert">
              {err}
            </p>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={onClose} disabled={busy}>
              Cancelar
            </Button>
            <Button disabled={!valid || busy} onClick={() => (action === "price_percent" || (action === "status" && status === "archived") ? setConfirm(true) : void run())}>
              {busy ? "Aplicando…" : "Aplicar"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Confirm
        open={confirm || action === "delete"}
        busy={busy}
        title={action === "delete" ? `¿Eliminar ${n} ${n === 1 ? "producto" : "productos"}?` : action === "price_percent" ? `¿${direction === "down" ? "Bajar" : "Subir"} ${percent}% los precios de ${n} ${n === 1 ? "producto" : "productos"}?` : `¿Archivar ${n} ${n === 1 ? "producto" : "productos"}?`}
        description={
          action === "delete"
            ? "Se borran con sus variantes, precios y existencias, y dejan de verse en la tienda. No se puede deshacer: si solo quieres ocultarlos, mejor archívalos."
            : action === "price_percent"
            ? "El cambio se aplica a todas sus variantes y se ve en la tienda de inmediato. Queda registrado en la auditoría."
            : "Dejarán de verse en la tienda. Puedes volver a activarlos cuando quieras."
        }
        confirmLabel={action === "delete" ? "Eliminar" : action === "price_percent" ? "Cambiar precios" : "Archivar"}
        destructive={action !== "price_percent"}
        onConfirm={() => void run()}
        onClose={() => (action === "delete" ? onClose() : setConfirm(false))}
      />
    </>
  );
}
