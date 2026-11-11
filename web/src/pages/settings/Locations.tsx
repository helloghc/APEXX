import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ArrowRight, ArrowLeftRight, BellRing, MapPin, MoreHorizontal, Plus, Store as StoreIcon, Trash2, Warehouse } from "lucide-react";
import { errorMessage, fieldErrors, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { toast } from "@/components/ui/sonner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { slugify } from "@/forms";
import { can } from "@/perms";
import { navigate } from "@/router";
import { dateTime, ErrorBox, Notice } from "../common";
import { sapi, type Hours, type Location, type LocationRow, type StateInfo, type StockRow, type TransferLine } from "./api";
import { Confirm, EmptyState, Loading, SearchInput, SubPage, Switch, useDebounced } from "./kit";
import { WEEKDAYS } from "./logic";
import { StateSelect } from "./Store";

const KIND_LABEL = { warehouse: "Bodega", store: "Tienda física" } as const;

export function Locations({ me, tab }: { me: Me; tab?: string }) {
  const current = tab === "transferencias" || tab === "alertas" ? tab : "lista";
  const q = useQuery({ queryKey: ["settings", "locations"], queryFn: sapi.locations });
  const canAdmin = can(me, "settings", "admin");
  const canMove = can(me, "inventory", "edit");
  const seeStock = can(me, "inventory", "read");
  const [edit, setEdit] = useState<LocationRow | "new" | null>(null);
  const [transfer, setTransfer] = useState(false);
  const locs = q.data?.items ?? [];
  const registered = locs.filter((l) => l.registered);

  return (
    <SubPage
      title="Ubicaciones"
      sub="Bodegas y tiendas donde guardas inventario. Mueve existencias entre ellas y revisa qué se está agotando."
      actions={
        <>
          {canMove && registered.length >= 2 && (
            <Button variant="outline" onClick={() => setTransfer(true)}>
              <ArrowLeftRight /> Transferir stock
            </Button>
          )}
          {canAdmin && (
            <Button onClick={() => setEdit("new")}>
              <Plus /> Nueva ubicación
            </Button>
          )}
        </>
      }
    >
      <Tabs value={current} onValueChange={(v) => navigate("configuracion", "ubicaciones", ...(v === "lista" ? [] : [v]))}>
        <TabsList>
          <TabsTrigger value="lista">Ubicaciones</TabsTrigger>
          {seeStock && <TabsTrigger value="transferencias">Transferencias</TabsTrigger>}
          {seeStock && (
            <TabsTrigger value="alertas">
              Stock bajo
              {q.data && q.data.lowStock > 0 && (
                <Badge variant="warn" className="ml-1.5">
                  {q.data.lowStock}
                </Badge>
              )}
            </TabsTrigger>
          )}
        </TabsList>
      </Tabs>
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {q.isPending && <Loading />}

      {q.data && current === "lista" && (
        <LocationList rows={locs} canAdmin={canAdmin} onEdit={setEdit} onNew={() => setEdit("new")} />
      )}
      {q.data && current === "transferencias" && <TransferList locations={locs} canMove={canMove} onNew={() => setTransfer(true)} />}
      {q.data && current === "alertas" && <Alerts locations={locs} canEdit={canMove} defaultThreshold={q.data.defaultThreshold} />}

      {edit && <LocationSheet row={edit === "new" ? null : edit} states={q.data?.states ?? []} taken={locs.map((l) => l.id)} onClose={() => setEdit(null)} />}
      {transfer && <TransferDialog locations={registered} onClose={() => setTransfer(false)} />}
    </SubPage>
  );
}

function addressLine(l: Location): string {
  const a = l.address;
  return [a?.line1, a?.city, a?.postalCode].filter(Boolean).join(", ");
}

function LocationList({ rows, canAdmin, onEdit, onNew }: { rows: LocationRow[]; canAdmin: boolean; onEdit: (r: LocationRow) => void; onNew: () => void }) {
  const qc = useQueryClient();
  const [del, setDel] = useState<LocationRow | null>(null);
  const remove = useMutation({
    mutationFn: (id: string) => sapi.deleteLocation(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["settings", "locations"] });
      toast.success("Ubicación eliminada");
      setDel(null);
    },
    onError: (e) => {
      toast.error(errorMessage(e));
      setDel(null);
    },
  });
  if (rows.length === 0)
    return (
      <EmptyState
        icon={MapPin}
        title="Registra tu primera ubicación"
        text="Una ubicación es una bodega o tienda con inventario. Con su dirección y horario puedes ofrecer recolección en tienda."
        action={canAdmin && <Button onClick={onNew}>Nueva ubicación</Button>}
      />
    );
  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Ubicación</TableHead>
            <TableHead>Dirección</TableHead>
            <TableHead>Recolección</TableHead>
            <TableHead className="text-right">Existencias</TableHead>
            <TableHead>Estado</TableHead>
            {canAdmin && <TableHead className="w-10 text-right">Acciones</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((l) => {
            const Icon = l.kind === "store" ? StoreIcon : Warehouse;
            return (
              <TableRow key={l.id} data-testid={`location-${l.id}`}>
                <TableCell>
                  <div className="flex items-start gap-2.5">
                    <Icon className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                    <div>
                      {canAdmin ? (
                        <button type="button" className="cursor-pointer text-left font-medium hover:underline" onClick={() => onEdit(l)}>
                          {l.name}
                        </button>
                      ) : (
                        <span className="font-medium">{l.name}</span>
                      )}
                      <div className="font-mono text-xs text-muted-foreground">
                        {l.id} · {KIND_LABEL[l.kind] ?? l.kind}
                      </div>
                    </div>
                  </div>
                </TableCell>
                <TableCell className="max-w-64 text-sm">
                  {l.registered ? addressLine(l) || <span className="text-muted-foreground">Sin dirección</span> : <Badge variant="warn">Faltan sus datos</Badge>}
                </TableCell>
                <TableCell>
                  {l.pickupEnabled ? (
                    <div className="grid gap-0.5">
                      <Badge variant="ok" className="w-fit">
                        Disponible
                      </Badge>
                      {l.pickupOptions.length === 0 && <span className="text-xs text-muted-foreground">Falta crear la opción en Envíos</span>}
                    </div>
                  ) : (
                    <span className="text-sm text-muted-foreground">No</span>
                  )}
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {l.usage.onHand.toLocaleString("es-MX")}
                  <div className="text-xs text-muted-foreground">
                    {l.usage.skus.toLocaleString("es-MX")} SKU{l.usage.reserved > 0 && ` · ${l.usage.reserved} reservadas`}
                  </div>
                </TableCell>
                <TableCell>{l.active ? <Badge variant="ok">Activa</Badge> : <Badge variant="secondary">Inactiva</Badge>}</TableCell>
                {canAdmin && (
                  <TableCell className="text-right">
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon" aria-label={`Acciones de ${l.name}`}>
                          <MoreHorizontal />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onSelect={() => onEdit(l)}>{l.registered ? "Editar" : "Completar datos"}</DropdownMenuItem>
                        {l.registered && (
                          <DropdownMenuItem className="text-destructive" onSelect={() => setDel(l)}>
                            <Trash2 /> Eliminar
                          </DropdownMenuItem>
                        )}
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                )}
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
      <Confirm
        open={Boolean(del)}
        title={`¿Eliminar ${del?.name ?? ""}?`}
        text={
          del && del.usage.onHand > 0
            ? `Esta ubicación tiene ${del.usage.onHand.toLocaleString("es-MX")} unidades. Transfiérelas a otra ubicación antes de eliminarla.`
            : "La ubicación desaparece de inventario y de las opciones de transferencia. No se puede deshacer."
        }
        confirmLabel="Eliminar ubicación"
        busy={remove.isPending}
        onConfirm={() => del && remove.mutate(del.id)}
        onClose={() => setDel(null)}
      />
    </>
  );
}

const DEFAULT_HOURS: Record<string, Hours> = Object.fromEntries(
  WEEKDAYS.map((d) => [d.id, d.id === "sun" ? { open: "", close: "", closed: true } : { open: "09:00", close: d.id === "sat" ? "14:00" : "18:00", closed: false }]),
);

function LocationSheet({ row, states, taken, onClose }: { row: LocationRow | null; states: StateInfo[]; taken: string[]; onClose: () => void }) {
  const qc = useQueryClient();
  const creating = row === null;
  const [id, setId] = useState(row?.id ?? "");
  const [idTouched, setIdTouched] = useState(false);
  const [f, setF] = useState<Location>(() => ({
    id: row?.id ?? "",
    name: row?.registered ? row.name : "",
    kind: row?.kind ?? "warehouse",
    address: { line1: "", line2: "", city: "", state: "", postalCode: "", country: "MX", ...(row?.address ?? {}) },
    phone: row?.phone ?? "",
    hours: row?.hours && Object.keys(row.hours).length ? row.hours : DEFAULT_HOURS,
    pickupEnabled: row?.pickupEnabled ?? false,
    pickupInstructions: row?.pickupInstructions ?? "",
    active: row?.active ?? true,
  }));
  const [errors, setErrors] = useState<Record<string, string>>({});

  const save = useMutation({
    mutationFn: () => sapi.saveLocation({ ...f, id, hours: f.pickupEnabled || f.kind === "store" ? f.hours : {} }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["settings", "locations"] });
      void qc.invalidateQueries({ queryKey: ["settings", "shipping"] });
      toast.success(creating ? "Ubicación creada" : "Ubicación guardada");
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      toast.error(errorMessage(e));
    },
  });

  function submit() {
    const e: Record<string, string> = {};
    if (!f.name.trim()) e.name = "Escribe un nombre.";
    if (!/^[a-z0-9][a-z0-9-]{0,39}$/.test(id)) e.id = "Usa minúsculas, números y guiones (por ejemplo cdmx-1).";
    else if (creating && taken.includes(id)) e.id = "Ya existe una ubicación con ese identificador.";
    if (f.pickupEnabled) {
      if (!f.address.line1.trim()) e["address.line1"] = "Para ofrecer recolección escribe la dirección.";
      if (!f.address.city.trim()) e["address.city"] = "Escribe la ciudad.";
    }
    if ((f.pickupEnabled || f.address.line1 || f.address.postalCode) && !/^\d{5}$/.test(f.address.postalCode)) e["address.postalCode"] = "El código postal tiene 5 dígitos.";
    for (const d of WEEKDAYS) {
      const h = f.hours?.[d.id];
      if ((f.pickupEnabled || f.kind === "store") && h && !h.closed && !(h.open && h.close && h.open < h.close)) e[`hours.${d.id}`] = "La apertura debe ser antes del cierre.";
    }
    setErrors(e);
    if (Object.keys(e).length === 0) save.mutate();
  }

  const setAddr = (p: Partial<Location["address"]>) => setF({ ...f, address: { ...f.address, ...p } });
  const setHours = (day: string, p: Partial<Hours>) => setF({ ...f, hours: { ...f.hours, [day]: { ...(f.hours?.[day] ?? { open: "09:00", close: "18:00", closed: false }), ...p } } });
  const showHours = f.pickupEnabled || f.kind === "store";

  return (
    <Sheet open onOpenChange={(o) => !o && onClose()}>
      <SheetContent className="grid content-start gap-5 p-6">
        <SheetHeader>
          <SheetTitle>{creating ? "Nueva ubicación" : row?.registered ? `Editar ${row.name}` : `Completar ${row?.id}`}</SheetTitle>
          <SheetDescription>
            {row && !row.registered
              ? "Esta ubicación ya tiene inventario pero le faltan nombre y dirección."
              : "Dirección, horario y si los clientes pueden recoger aquí sus pedidos."}
          </SheetDescription>
        </SheetHeader>
        <form
          className="grid gap-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <FormField id="loc-name" label="Nombre" required error={errors.name}>
            <Input
              value={f.name}
              maxLength={80}
              placeholder="Bodega Centro"
              onChange={(e) => {
                setF({ ...f, name: e.target.value });
                if (creating && !idTouched) setId(slugify(e.target.value).slice(0, 40));
              }}
            />
          </FormField>
          <div className="grid gap-4 sm:grid-cols-2">
            <FormField id="loc-id" label="Identificador" required error={errors.id} description={creating ? "Se usa en inventario e integraciones. No se puede cambiar después." : "No se puede cambiar."}>
              <Input
                value={id}
                readOnly={!creating}
                className="font-mono"
                onChange={(e) => {
                  setIdTouched(true);
                  setId(e.target.value.toLowerCase());
                }}
              />
            </FormField>
            <FormField id="loc-kind" label="Tipo">
              <Select value={f.kind} onValueChange={(v) => setF({ ...f, kind: v as Location["kind"] })}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="warehouse">Bodega</SelectItem>
                  <SelectItem value="store">Tienda física</SelectItem>
                </SelectContent>
              </Select>
            </FormField>
          </div>

          <h3 className="border-t pt-4 text-sm font-semibold">Dirección</h3>
          <FormField id="loc-line1" label="Calle y número" error={errors["address.line1"]}>
            <Input value={f.address.line1} onChange={(e) => setAddr({ line1: e.target.value })} />
          </FormField>
          <div className="grid gap-4 sm:grid-cols-2">
            <FormField id="loc-line2" label="Colonia o interior">
              <Input value={f.address.line2 ?? ""} onChange={(e) => setAddr({ line2: e.target.value })} />
            </FormField>
            <FormField id="loc-city" label="Ciudad o municipio" error={errors["address.city"]}>
              <Input value={f.address.city} onChange={(e) => setAddr({ city: e.target.value })} />
            </FormField>
            <FormField id="loc-cp" label="Código postal" error={errors["address.postalCode"]}>
              <Input value={f.address.postalCode} inputMode="numeric" maxLength={5} onChange={(e) => setAddr({ postalCode: e.target.value.replace(/\D/g, "") })} />
            </FormField>
            <FormField id="loc-state" label="Estado" error={errors["address.state"]}>
              <StateSelect value={f.address.state} states={states} onChange={(v) => setAddr({ state: v })} />
            </FormField>
          </div>
          <FormField id="loc-phone" label="Teléfono (opcional)" error={errors.phone}>
            <Input type="tel" value={f.phone ?? ""} onChange={(e) => setF({ ...f, phone: e.target.value })} />
          </FormField>

          <div className="grid gap-3 border-t pt-4">
            <div className="flex items-start justify-between gap-4">
              <div className="grid gap-1">
                <span className="text-sm font-semibold">Recolección en esta ubicación</span>
                <span className="text-sm text-muted-foreground">Los clientes pueden pasar por su pedido. Después crea la opción “Recoger en tienda” en Envíos.</span>
              </div>
              <Switch checked={f.pickupEnabled} onChange={(v) => setF({ ...f, pickupEnabled: v })} label="Recolección en esta ubicación" />
            </div>
            {errors.pickupEnabled && <Notice tone="bad">{errors.pickupEnabled}</Notice>}
            {f.pickupEnabled && (
              <FormField id="loc-instr" label="Instrucciones para quien recoge" error={errors.pickupInstructions}>
                <Textarea value={f.pickupInstructions ?? ""} maxLength={500} rows={2} placeholder="Presenta tu número de pedido y una identificación." onChange={(e) => setF({ ...f, pickupInstructions: e.target.value })} />
              </FormField>
            )}
          </div>

          {showHours && (
            <fieldset className="grid gap-2 border-t pt-4">
              <legend className="sr-only">Horario</legend>
              <span className="text-sm font-semibold">Horario</span>
              {WEEKDAYS.map((d) => {
                const h = f.hours?.[d.id] ?? { open: "", close: "", closed: true };
                return (
                  <div key={d.id} className="grid gap-1">
                    <div className="grid grid-cols-[84px_1fr_1fr_auto] items-center gap-2">
                      <span className="text-sm">{d.label}</span>
                      <Input type="time" aria-label={`${d.label}: abre`} value={h.closed ? "" : h.open} disabled={h.closed} onChange={(e) => setHours(d.id, { open: e.target.value })} />
                      <Input type="time" aria-label={`${d.label}: cierra`} value={h.closed ? "" : h.close} disabled={h.closed} onChange={(e) => setHours(d.id, { close: e.target.value })} />
                      <label className="flex cursor-pointer items-center gap-1.5 text-xs text-muted-foreground">
                        <input
                          type="checkbox"
                          className="size-4 accent-[var(--accent)]"
                          checked={h.closed}
                          onChange={(e) => setHours(d.id, e.target.checked ? { closed: true } : { closed: false, open: h.open || "09:00", close: h.close || "18:00" })}
                        />
                        Cerrado
                      </label>
                    </div>
                    {errors[`hours.${d.id}`] && <p className="text-xs font-medium text-destructive">{errors[`hours.${d.id}`]}</p>}
                  </div>
                );
              })}
            </fieldset>
          )}

          <div className="flex items-start justify-between gap-4 border-t pt-4">
            <div className="grid gap-1">
              <span className="text-sm font-semibold">Ubicación activa</span>
              <span className="text-sm text-muted-foreground">Una ubicación inactiva conserva su inventario pero no ofrece recolección.</span>
            </div>
            <Switch checked={f.active} onChange={(v) => setF({ ...f, active: v })} label="Ubicación activa" />
          </div>

          <div className="flex justify-end gap-2 border-t pt-4">
            <Button type="button" variant="outline" onClick={onClose}>
              Cancelar
            </Button>
            <Button type="submit" disabled={save.isPending}>
              {save.isPending ? "Guardando…" : creating ? "Crear ubicación" : "Guardar"}
            </Button>
          </div>
        </form>
      </SheetContent>
    </Sheet>
  );
}

// ---------- transfers ----------

function TransferList({ locations, canMove, onNew }: { locations: LocationRow[]; canMove: boolean; onNew: () => void }) {
  const q = useInfiniteQuery({
    queryKey: ["settings", "transfers"],
    queryFn: ({ pageParam }) => sapi.transfers(pageParam),
    initialPageParam: 0,
    getNextPageParam: (last) => last.nextBefore || undefined,
  });
  const [open, setOpen] = useState<string | null>(null);
  const nameOf = (id: string) => locations.find((l) => l.id === id)?.name ?? id;
  const rows = q.data?.pages.flatMap((p) => p.items) ?? [];
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} onRetry={() => void q.refetch()} />;
  if (rows.length === 0)
    return (
      <EmptyState
        icon={ArrowLeftRight}
        title="Todavía no hay transferencias"
        text="Una transferencia mueve unidades de una ubicación a otra en un solo paso. Queda registrada con quién la hizo y cuándo."
        action={canMove && locations.filter((l) => l.registered).length >= 2 ? <Button onClick={onNew}>Transferir stock</Button> : undefined}
      />
    );
  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Transferencia</TableHead>
            <TableHead>Ruta</TableHead>
            <TableHead className="text-right">Unidades</TableHead>
            <TableHead>Nota</TableHead>
            <TableHead>Hecha por</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((t) => (
            <TableRow key={t.id}>
              <TableCell>
                <button type="button" className="cursor-pointer font-medium hover:underline" onClick={() => setOpen(t.id)}>
                  T-{t.number}
                </button>
                <div className="text-xs text-muted-foreground">{dateTime(t.createdAt)}</div>
              </TableCell>
              <TableCell>
                <span className="inline-flex flex-wrap items-center gap-1.5 text-sm">
                  {nameOf(t.from)} <ArrowRight className="size-3.5 text-muted-foreground" aria-label="a" /> {nameOf(t.to)}
                </span>
              </TableCell>
              <TableCell className="text-right tabular-nums">{t.units.toLocaleString("es-MX")}</TableCell>
              <TableCell className="max-w-56 truncate text-sm text-muted-foreground">{t.note || "—"}</TableCell>
              <TableCell className="font-mono text-xs text-muted-foreground">{t.actor}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {q.hasNextPage && (
        <div>
          <Button variant="outline" onClick={() => void q.fetchNextPage()} disabled={q.isFetchingNextPage}>
            {q.isFetchingNextPage ? "Cargando…" : "Cargar más"}
          </Button>
        </div>
      )}
      {open && <TransferDetail id={open} nameOf={nameOf} onClose={() => setOpen(null)} />}
    </>
  );
}

function TransferDetail({ id, nameOf, onClose }: { id: string; nameOf: (id: string) => string; onClose: () => void }) {
  const q = useQuery({ queryKey: ["settings", "transfer", id], queryFn: () => sapi.transfer(id) });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Transferencia {q.data ? `T-${q.data.number}` : ""}</DialogTitle>
          <DialogDescription>
            {q.data ? `${nameOf(q.data.from)} → ${nameOf(q.data.to)} · ${dateTime(q.data.createdAt)}` : "Cargando…"}
          </DialogDescription>
        </DialogHeader>
        <ErrorBox error={q.error} />
        {q.data && (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>SKU</TableHead>
                  <TableHead className="text-right">Unidades</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(q.data.lines ?? []).map((l) => (
                  <TableRow key={l.sku}>
                    <TableCell className="font-mono text-xs">{l.sku}</TableCell>
                    <TableCell className="text-right tabular-nums">{l.qty}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            {q.data.note && <p className="text-sm text-muted-foreground">Nota: {q.data.note}</p>}
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

function TransferDialog({ locations, onClose }: { locations: LocationRow[]; onClose: () => void }) {
  const qc = useQueryClient();
  const [from, setFrom] = useState(locations[0]?.id ?? "");
  const [to, setTo] = useState(locations[1]?.id ?? "");
  const [note, setNote] = useState("");
  const [search, setSearch] = useState("");
  const term = useDebounced(search);
  const [lines, setLines] = useState<(TransferLine & { title: string; available: number })[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const stock = useQuery({ queryKey: ["settings", "locstock", from, term], queryFn: () => sapi.locationStock(from, term), enabled: Boolean(from) });

  const create = useMutation({
    mutationFn: () => sapi.createTransfer({ from, to, note: note.trim(), lines: lines.map((l) => ({ sku: l.sku, qty: l.qty })) }),
    onSuccess: (t) => {
      void qc.invalidateQueries({ queryKey: ["settings", "transfers"] });
      void qc.invalidateQueries({ queryKey: ["settings", "locations"] });
      void qc.invalidateQueries({ queryKey: ["settings", "locstock"] });
      void qc.invalidateQueries({ queryKey: ["settings", "alerts"] });
      toast.success(`Transferencia T-${t.number} hecha: ${t.units} unidades`);
      onClose();
    },
    onError: (e) => setErr(errorMessage(e)),
  });

  function add(r: StockRow) {
    if (lines.some((l) => l.sku === r.sku)) return;
    setLines([...lines, { sku: r.sku, qty: 1, title: r.title, available: r.available }]);
  }
  const over = lines.find((l) => l.qty > l.available || l.qty < 1);
  const total = lines.reduce((a, l) => a + (l.qty > 0 ? l.qty : 0), 0);
  const options = (stock.data?.items ?? []).filter((r) => r.available > 0 && !lines.some((l) => l.sku === r.sku));

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="w-[min(680px,calc(100vw-32px))]">
        <DialogHeader>
          <DialogTitle>Transferir stock</DialogTitle>
          <DialogDescription>Las unidades salen del origen y entran al destino al mismo tiempo. Las reservadas por pedidos en curso no se pueden mover.</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <div className="grid items-end gap-3 sm:grid-cols-[1fr_auto_1fr]">
            <FormField id="tr-from" label="Origen">
              <Select
                value={from}
                onValueChange={(v) => {
                  setFrom(v);
                  setLines([]);
                  if (v === to) setTo(locations.find((l) => l.id !== v)?.id ?? "");
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {locations.map((l) => (
                    <SelectItem key={l.id} value={l.id}>
                      {l.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </FormField>
            <ArrowRight className="mb-2.5 hidden size-4 text-muted-foreground sm:block" aria-hidden="true" />
            <FormField id="tr-to" label="Destino">
              <Select value={to} onValueChange={setTo}>
                <SelectTrigger>
                  <SelectValue placeholder="Elige el destino" />
                </SelectTrigger>
                <SelectContent>
                  {locations
                    .filter((l) => l.id !== from)
                    .map((l) => (
                      <SelectItem key={l.id} value={l.id}>
                        {l.name}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
            </FormField>
          </div>

          <div className="grid gap-2">
            <SearchInput value={search} onChange={setSearch} label="Buscar SKU en el origen" placeholder="Buscar SKU en el origen" />
            <ul className="grid max-h-40 gap-px overflow-y-auto rounded-lg border bg-card" aria-label="Productos en el origen">
              {stock.isPending && <li className="px-3 py-2 text-sm text-muted-foreground">Buscando…</li>}
              {!stock.isPending && options.length === 0 && (
                <li className="px-3 py-2 text-sm text-muted-foreground">{term ? "Ningún SKU con unidades disponibles coincide." : "No hay más SKU con unidades disponibles aquí."}</li>
              )}
              {options.map((r) => (
                <li key={r.sku} className="flex items-center justify-between gap-3 border-b px-3 py-1.5 last:border-0">
                  <div className="min-w-0">
                    <div className="truncate text-sm">{r.title || r.sku}</div>
                    <div className="font-mono text-xs text-muted-foreground">
                      {r.sku} · {r.available} disponibles
                    </div>
                  </div>
                  <Button type="button" variant="outline" size="sm" onClick={() => add(r)}>
                    Agregar
                  </Button>
                </li>
              ))}
            </ul>
          </div>

          {lines.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Producto</TableHead>
                  <TableHead className="w-28 text-right">Unidades</TableHead>
                  <TableHead className="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {lines.map((l) => (
                  <TableRow key={l.sku}>
                    <TableCell>
                      <div className="text-sm">{l.title || l.sku}</div>
                      <div className="font-mono text-xs text-muted-foreground">
                        {l.sku} · máx. {l.available}
                      </div>
                    </TableCell>
                    <TableCell>
                      <Input
                        type="number"
                        min={1}
                        max={l.available}
                        aria-label={`Unidades de ${l.sku}`}
                        aria-invalid={l.qty > l.available || l.qty < 1}
                        className="text-right tabular-nums"
                        value={l.qty || ""}
                        onChange={(e) => setLines(lines.map((x) => (x.sku === l.sku ? { ...x, qty: Math.trunc(Number(e.target.value) || 0) } : x)))}
                      />
                    </TableCell>
                    <TableCell>
                      <Button type="button" variant="ghost" size="icon" aria-label={`Quitar ${l.sku}`} onClick={() => setLines(lines.filter((x) => x.sku !== l.sku))}>
                        <Trash2 />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          {over && <Notice tone="bad">{over.qty < 1 ? `Escribe cuántas unidades de ${over.sku} mover.` : `Solo hay ${over.available} unidades disponibles de ${over.sku}.`}</Notice>}
          <FormField id="tr-note" label="Nota (opcional)">
            <Input value={note} maxLength={300} placeholder="Reabasto de temporada" onChange={(e) => setNote(e.target.value)} />
          </FormField>
          {err && <Notice tone="bad">{err}</Notice>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancelar
          </Button>
          <Button disabled={create.isPending || lines.length === 0 || Boolean(over) || !to} onClick={() => create.mutate()}>
            {create.isPending ? "Transfiriendo…" : total > 0 ? `Transferir ${total} unidades` : "Transferir"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------- low stock ----------

function Alerts({ locations, canEdit, defaultThreshold }: { locations: LocationRow[]; canEdit: boolean; defaultThreshold: number }) {
  const qc = useQueryClient();
  const [loc, setLoc] = useState("");
  const q = useInfiniteQuery({
    queryKey: ["settings", "alerts", loc],
    queryFn: ({ pageParam }) => sapi.alerts(loc, pageParam),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor || undefined,
  });
  const [edit, setEdit] = useState<StockRow | null>(null);
  const [value, setValue] = useState("");
  const rows = q.data?.pages.flatMap((p) => p.items) ?? [];
  const total = q.data?.pages[0]?.total ?? 0;
  const nameOf = (id: string) => locations.find((l) => l.id === id)?.name ?? id;

  const save = useMutation({
    mutationFn: (v: number | null) => sapi.setThreshold((edit as StockRow).sku, v),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["settings", "alerts"] });
      void qc.invalidateQueries({ queryKey: ["settings", "locations"] });
      toast.success("Umbral actualizado");
      setEdit(null);
    },
    onError: (e) => toast.error(errorMessage(e)),
  });

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="w-full max-w-60">
          <Select value={loc || "_"} onValueChange={(v) => setLoc(v === "_" ? "" : v)}>
            <SelectTrigger aria-label="Filtrar por ubicación">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="_">Todas las ubicaciones</SelectItem>
              {locations.map((l) => (
                <SelectItem key={l.id} value={l.id}>
                  {l.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <p className="text-sm text-muted-foreground">
          {total.toLocaleString("es-MX")} en alerta · umbral general: {defaultThreshold} unidades
        </p>
      </div>
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {q.isPending && <Loading />}
      {!q.isPending && rows.length === 0 && !q.error && (
        <EmptyState
          icon={BellRing}
          title="Nada por agotarse"
          text={`Aquí aparecen los SKU con ${defaultThreshold} unidades disponibles o menos (o el umbral propio que les pongas). El umbral general se cambia en Tienda → Operación.`}
        />
      )}
      {rows.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Producto</TableHead>
              <TableHead>Ubicación</TableHead>
              <TableHead className="text-right">Disponibles</TableHead>
              <TableHead className="text-right">Reservadas</TableHead>
              <TableHead className="text-right">Umbral</TableHead>
              {canEdit && <TableHead className="text-right">Acciones</TableHead>}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((r) => (
              <TableRow key={r.sku + "|" + r.locationId}>
                <TableCell>
                  <div className="text-sm font-medium">{r.title || r.sku}</div>
                  <div className="font-mono text-xs text-muted-foreground">{r.sku}</div>
                </TableCell>
                <TableCell className="text-sm">{nameOf(r.locationId)}</TableCell>
                <TableCell className="text-right">
                  <Badge variant={r.available <= 0 ? "bad" : "warn"} className="tabular-nums">
                    {r.available <= 0 ? "Agotado" : r.available}
                  </Badge>
                </TableCell>
                <TableCell className="text-right tabular-nums text-muted-foreground">{r.reserved}</TableCell>
                <TableCell className="text-right tabular-nums">
                  {r.threshold}
                  {r.customThreshold && <div className="text-xs text-muted-foreground">propio</div>}
                </TableCell>
                {canEdit && (
                  <TableCell className="text-right">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        setEdit(r);
                        setValue(String(r.threshold));
                      }}
                    >
                      Cambiar umbral
                    </Button>
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {q.hasNextPage && (
        <div>
          <Button variant="outline" onClick={() => void q.fetchNextPage()} disabled={q.isFetchingNextPage}>
            {q.isFetchingNextPage ? "Cargando…" : "Cargar más"}
          </Button>
        </div>
      )}
      {edit && (
        <Dialog open onOpenChange={(o) => !o && setEdit(null)}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Umbral de {edit.sku}</DialogTitle>
              <DialogDescription>El SKU entra en alerta cuando sus unidades disponibles en una ubicación llegan a este número o menos.</DialogDescription>
            </DialogHeader>
            <FormField id="th-value" label="Avisar con" description={`Umbral general de la tienda: ${defaultThreshold}. Con 0 este SKU nunca avisa.`}>
              <Input type="number" min={0} value={value} onChange={(e) => setValue(e.target.value)} />
            </FormField>
            <DialogFooter>
              {edit.customThreshold && (
                <Button variant="ghost" disabled={save.isPending} onClick={() => save.mutate(null)}>
                  Usar el umbral general
                </Button>
              )}
              <Button disabled={save.isPending || !/^\d+$/.test(value)} onClick={() => save.mutate(Number(value))}>
                Guardar umbral
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </>
  );
}
