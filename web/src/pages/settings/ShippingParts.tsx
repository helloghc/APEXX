import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { CalendarDays, Map as MapIcon, Plus, Trash2, Truck } from "lucide-react";
import { errorMessage, fieldErrors } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { toast } from "@/components/ui/sonner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { Notice } from "../common";
import { sapi, type Carrier, type Holiday, type PostalRange, type Profile, type ShippingConfig, type StateInfo, type Zone } from "./api";
import { Chips, Confirm, EmptyState, Section, Switch } from "./kit";
import { formatRanges, parseRanges } from "./logic";
import { ProductClasses } from "./ProductClasses";

function useRefresh() {
  const qc = useQueryClient();
  return () => void qc.invalidateQueries({ queryKey: ["settings", "shipping"] });
}

/** AreaFields edits "whole country" or a list of states and postal-code ranges (zones and tax regions). */
export function AreaFields({
  states,
  value,
  onChange,
  text,
  onText,
  error,
  wholeLabel = "Todo México",
}: {
  states: StateInfo[];
  value: { states: string[]; whole: boolean };
  onChange: (v: { states: string[]; whole: boolean }) => void;
  text: string;
  onText: (t: string) => void;
  error?: string;
  wholeLabel?: string;
}) {
  const [filter, setFilter] = useState("");
  const shown = states.filter((s) => s.name.toLowerCase().includes(filter.trim().toLowerCase()));
  return (
    <div className="grid gap-4">
      <FormField id="area-mode" label="Cobertura">
        <Select value={value.whole ? "whole" : "parts"} onValueChange={(v) => onChange({ ...value, whole: v === "whole" })}>
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="whole">{wholeLabel}</SelectItem>
            <SelectItem value="parts">Estados o códigos postales</SelectItem>
          </SelectContent>
        </Select>
      </FormField>
      {!value.whole && (
        <>
          <div className="grid gap-2">
            <span className="text-sm font-medium">Estados</span>
            <Chips
              items={value.states.map((c) => ({ key: c, label: states.find((s) => s.code === c)?.name ?? c }))}
              onRemove={(k) => onChange({ ...value, states: value.states.filter((s) => s !== k) })}
            />
            <Input aria-label="Filtrar estados" placeholder="Filtrar estados" value={filter} onChange={(e) => setFilter(e.target.value)} />
            <ul className="grid max-h-40 grid-cols-1 gap-x-4 overflow-y-auto rounded-lg border bg-card p-2 sm:grid-cols-2" aria-label="Estados">
              {shown.map((s) => (
                <li key={s.code}>
                  <label className="flex cursor-pointer items-center gap-2 rounded px-1.5 py-1 text-sm hover:bg-muted">
                    <input
                      type="checkbox"
                      className="size-4 accent-[var(--accent)]"
                      checked={value.states.includes(s.code)}
                      onChange={(e) => onChange({ ...value, states: e.target.checked ? [...value.states, s.code] : value.states.filter((x) => x !== s.code) })}
                    />
                    {s.name}
                  </label>
                </li>
              ))}
              {shown.length === 0 && <li className="px-1.5 py-1 text-sm text-muted-foreground">Ningún estado coincide.</li>}
            </ul>
          </div>
          <FormField
            id="area-ranges"
            label="Códigos postales"
            error={error}
            description="Un rango por línea (01000-16999) o códigos sueltos. Se suman a los estados elegidos; un rango le gana a un estado."
          >
            <Textarea rows={4} className="font-mono text-xs" value={text} placeholder={"64000-67999\n66260"} onChange={(e) => onText(e.target.value)} />
          </FormField>
        </>
      )}
    </div>
  );
}

export function coverage(states: StateInfo[], z: { states: string[]; postalRanges: PostalRange[] }, whole = "Todo México"): string {
  if (z.states.length === 0 && z.postalRanges.length === 0) return whole;
  const parts: string[] = [];
  if (z.states.length) {
    const names = z.states.map((c) => states.find((s) => s.code === c)?.name ?? c);
    parts.push(names.length > 3 ? `${names.slice(0, 3).join(", ")} y ${names.length - 3} más` : names.join(", "));
  }
  if (z.postalRanges.length) parts.push(z.postalRanges.length === 1 ? `CP ${formatRanges(z.postalRanges)}` : `${z.postalRanges.length} rangos de CP`);
  return parts.join(" · ");
}

// ---------- zones ----------

export function ZonesTab({ cfg, canEdit }: { cfg: ShippingConfig; canEdit: boolean }) {
  const refresh = useRefresh();
  const [edit, setEdit] = useState<Zone | "new" | null>(null);
  const [del, setDel] = useState<Zone | null>(null);
  const remove = useMutation({
    mutationFn: (z: Zone) => sapi.deleteShipping("zones", z.id),
    onSuccess: () => {
      refresh();
      toast.success("Zona eliminada");
      setDel(null);
    },
    onError: (e) => {
      toast.error(errorMessage(e));
      setDel(null);
    },
  });
  const uses = (id: string) => cfg.options.filter((o) => o.zoneId === id).length;

  return (
    <>
      {cfg.zones.length === 0 ? (
        <EmptyState
          icon={MapIcon}
          title="Crea tu primera zona"
          text="Una zona es un área de entrega: todo el país, algunos estados o rangos de códigos postales. Cada zona tiene sus propias opciones y precios."
          action={canEdit && <Button onClick={() => setEdit("new")}>Nueva zona</Button>}
        />
      ) : (
        <>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="text-sm text-muted-foreground">Si un código postal cae en varias zonas, se usa la más específica.</p>
            {canEdit && (
              <Button onClick={() => setEdit("new")}>
                <Plus /> Nueva zona
              </Button>
            )}
          </div>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Zona</TableHead>
                <TableHead>Cobertura</TableHead>
                <TableHead className="text-right">Opciones</TableHead>
                <TableHead>Estado</TableHead>
                {canEdit && <TableHead className="text-right">Acciones</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {cfg.zones.map((z) => (
                <TableRow key={z.id} data-testid={`zone-${z.id}`}>
                  <TableCell className="font-medium">{z.name}</TableCell>
                  <TableCell className="max-w-80 text-sm">{coverage(cfg.states, z)}</TableCell>
                  <TableCell className="text-right tabular-nums">{uses(z.id)}</TableCell>
                  <TableCell>{z.active ? <Badge variant="ok">Activa</Badge> : <Badge variant="secondary">Inactiva</Badge>}</TableCell>
                  {canEdit && (
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-1">
                        <Button variant="outline" size="sm" onClick={() => setEdit(z)}>
                          Editar
                        </Button>
                        <Button variant="ghost" size="icon" aria-label={`Eliminar ${z.name}`} onClick={() => setDel(z)}>
                          <Trash2 />
                        </Button>
                      </div>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </>
      )}
      {edit && <ZoneDialog zone={edit === "new" ? null : edit} states={cfg.states} onClose={() => setEdit(null)} />}
      <Confirm
        open={Boolean(del)}
        title={`¿Eliminar la zona ${del?.name ?? ""}?`}
        text={del && uses(del.id) > 0 ? `Tiene ${uses(del.id)} opciones de entrega. Elimínalas o cámbialas de zona primero.` : "Los compradores de esta zona se quedarán con las opciones de la zona siguiente más general, si existe."}
        confirmLabel="Eliminar zona"
        busy={remove.isPending}
        onConfirm={() => del && remove.mutate(del)}
        onClose={() => setDel(null)}
      />
    </>
  );
}

function ZoneDialog({ zone, states, onClose }: { zone: Zone | null; states: StateInfo[]; onClose: () => void }) {
  const refresh = useRefresh();
  const [name, setName] = useState(zone?.name ?? "");
  const [active, setActive] = useState(zone?.active ?? true);
  const [area, setArea] = useState({ states: zone?.states ?? [], whole: zone ? zone.states.length === 0 && zone.postalRanges.length === 0 : true });
  const [text, setText] = useState(formatRanges(zone?.postalRanges ?? []));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: (z: Zone) => sapi.saveShipping("zones", z),
    onSuccess: () => {
      refresh();
      toast.success(zone ? "Zona guardada" : "Zona creada");
      onClose();
    },
    onError: (e) => {
      const fe = fieldErrors(e);
      setErrors({ ...fe, ranges: Object.entries(fe).find(([k]) => k.startsWith("postalRanges"))?.[1] ?? "" });
      toast.error(errorMessage(e));
    },
  });
  function submit() {
    const e: Record<string, string> = {};
    if (!name.trim()) e.name = "Escribe un nombre, por ejemplo Norte.";
    const parsed = parseRanges(area.whole ? "" : text);
    if (parsed.error) e.ranges = parsed.error;
    if (!area.whole && area.states.length === 0 && parsed.ranges.length === 0 && !parsed.error) e.ranges = "Elige al menos un estado o escribe un rango, o cambia la cobertura a todo el país.";
    setErrors(e);
    if (Object.keys(e).length) return;
    save.mutate({ id: zone?.id ?? "", name: name.trim(), countries: zone?.countries ?? ["MX"], states: area.whole ? [] : area.states, postalRanges: parsed.ranges, active });
  }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{zone ? `Editar ${zone.name}` : "Nueva zona"}</DialogTitle>
          <DialogDescription>Define a dónde llega esta zona. Después le agregas opciones de entrega.</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <FormField id="zone-name" label="Nombre" required error={errors.name}>
            <Input value={name} maxLength={80} placeholder="Zona norte" onChange={(e) => setName(e.target.value)} />
          </FormField>
          <AreaFields states={states} value={area} onChange={setArea} text={text} onText={setText} error={errors.ranges || undefined} />
          <div className="flex items-center justify-between gap-4">
            <span className="text-sm font-medium">Zona activa</span>
            <Switch checked={active} onChange={setActive} label="Zona activa" />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancelar
            </Button>
            <Button type="submit" disabled={save.isPending}>
              {save.isPending ? "Guardando…" : zone ? "Guardar" : "Crear zona"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// ---------- profiles ----------

const KIND_LABEL: Record<Profile["kind"], string> = { default: "Estándar", heavy: "Pesado o voluminoso", custom: "Personalizado" };

export function ProfilesTab({ cfg, canEdit }: { cfg: ShippingConfig; canEdit: boolean }) {
  const refresh = useRefresh();
  const [edit, setEdit] = useState<Profile | "new" | null>(null);
  const [del, setDel] = useState<Profile | null>(null);
  const remove = useMutation({
    mutationFn: (p: Profile) => sapi.deleteShipping("profiles", p.id),
    onSuccess: () => {
      refresh();
      toast.success("Perfil eliminado");
      setDel(null);
    },
    onError: (e) => {
      toast.error(errorMessage(e));
      setDel(null);
    },
  });
  return (
    <>
      <Section
        title="Perfiles de envío"
        description="Un perfil agrupa productos que viajan igual. Los productos sin perfil usan el estándar; un sofá o un refrigerador van en “pesado” y solo ven las opciones de ese perfil."
        actions={
          canEdit && (
            <Button onClick={() => setEdit("new")}>
              <Plus /> Nuevo perfil
            </Button>
          )
        }
      >
        {cfg.profiles.length === 0 ? (
          <p className="text-sm text-muted-foreground">Todavía no hay perfiles: todos los productos usan el estándar.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Perfil</TableHead>
                <TableHead>Tipo</TableHead>
                <TableHead className="text-right">Productos</TableHead>
                <TableHead className="text-right">Opciones</TableHead>
                {canEdit && <TableHead className="text-right">Acciones</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {cfg.profiles.map((p) => (
                <TableRow key={p.id}>
                  <TableCell>
                    <span className="font-medium">{p.name}</span>
                    {p.description && <div className="text-xs text-muted-foreground">{p.description}</div>}
                  </TableCell>
                  <TableCell>
                    <Badge variant={p.kind === "default" ? "default" : "secondary"}>{KIND_LABEL[p.kind]}</Badge>
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{p.kind === "default" ? "Todos los demás" : (cfg.productsByProfile[p.id] ?? 0)}</TableCell>
                  <TableCell className="text-right tabular-nums">{cfg.options.filter((o) => o.profileId === p.id || (p.kind === "default" && !o.profileId && o.kind === "delivery")).length}</TableCell>
                  {canEdit && (
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-1">
                        <Button variant="outline" size="sm" onClick={() => setEdit(p)}>
                          Editar
                        </Button>
                        {p.kind !== "default" && (
                          <Button variant="ghost" size="icon" aria-label={`Eliminar ${p.name}`} onClick={() => setDel(p)}>
                            <Trash2 />
                          </Button>
                        )}
                      </div>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Section>
      <ProductClasses canEdit={canEdit} focus="profile" />
      {edit && <ProfileDialog profile={edit === "new" ? null : edit} onClose={() => setEdit(null)} />}
      <Confirm
        open={Boolean(del)}
        title={`¿Eliminar el perfil ${del?.name ?? ""}?`}
        text="Solo se puede eliminar un perfil sin productos ni opciones de entrega."
        confirmLabel="Eliminar perfil"
        busy={remove.isPending}
        onConfirm={() => del && remove.mutate(del)}
        onClose={() => setDel(null)}
      />
    </>
  );
}

function ProfileDialog({ profile, onClose }: { profile: Profile | null; onClose: () => void }) {
  const refresh = useRefresh();
  const [p, setP] = useState<Profile>(profile ?? { id: "", name: "", kind: "heavy", description: "" });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => sapi.saveShipping("profiles", { ...p, name: p.name.trim() }),
    onSuccess: () => {
      refresh();
      toast.success(profile ? "Perfil guardado" : "Perfil creado");
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      toast.error(errorMessage(e));
    },
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{profile ? `Editar ${profile.name}` : "Nuevo perfil"}</DialogTitle>
          <DialogDescription>Después asigna productos al perfil y crea opciones de entrega para él.</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            if (!p.name.trim()) setErrors({ name: "Escribe un nombre." });
            else save.mutate();
          }}
        >
          <FormField id="prof-name" label="Nombre" required error={errors.name}>
            <Input value={p.name} maxLength={80} placeholder="Frágil" onChange={(e) => setP({ ...p, name: e.target.value })} />
          </FormField>
          {p.kind !== "default" && (
            <FormField id="prof-kind" label="Tipo" error={errors.kind}>
              <Select value={p.kind} onValueChange={(v) => setP({ ...p, kind: v as Profile["kind"] })}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="heavy">Pesado o voluminoso</SelectItem>
                  <SelectItem value="custom">Personalizado</SelectItem>
                </SelectContent>
              </Select>
            </FormField>
          )}
          <FormField id="prof-desc" label="Descripción (opcional)" error={errors.description}>
            <Input value={p.description ?? ""} maxLength={300} onChange={(e) => setP({ ...p, description: e.target.value })} />
          </FormField>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancelar
            </Button>
            <Button type="submit" disabled={save.isPending}>
              {save.isPending ? "Guardando…" : profile ? "Guardar" : "Crear perfil"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// ---------- carriers ----------

export function CarriersTab({ cfg, canEdit }: { cfg: ShippingConfig; canEdit: boolean }) {
  const refresh = useRefresh();
  const [edit, setEdit] = useState<Carrier | "new" | null>(null);
  const [del, setDel] = useState<Carrier | null>(null);
  const remove = useMutation({
    mutationFn: (c: Carrier) => sapi.deleteShipping("carriers", c.id),
    onSuccess: () => {
      refresh();
      toast.success("Paquetería eliminada");
      setDel(null);
    },
    onError: (e) => {
      toast.error(errorMessage(e));
      setDel(null);
    },
  });
  if (cfg.carriers.length === 0)
    return (
      <>
        <EmptyState
          icon={Truck}
          title="Agrega tus paqueterías"
          text="Con el nombre y la URL de rastreo, cada pedido enviado lleva un enlace directo para seguir la guía."
          action={canEdit && <Button onClick={() => setEdit("new")}>Nueva paquetería</Button>}
        />
        {edit && <CarrierDialog carrier={null} onClose={() => setEdit(null)} />}
      </>
    );
  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">La URL de rastreo usa {"{tracking}"} donde va el número de guía.</p>
        {canEdit && (
          <Button onClick={() => setEdit("new")}>
            <Plus /> Nueva paquetería
          </Button>
        )}
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Paquetería</TableHead>
            <TableHead>URL de rastreo</TableHead>
            <TableHead className="text-right">Opciones</TableHead>
            <TableHead>Estado</TableHead>
            {canEdit && <TableHead className="text-right">Acciones</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {cfg.carriers.map((c) => (
            <TableRow key={c.id}>
              <TableCell className="font-medium">{c.name}</TableCell>
              <TableCell className="max-w-80 break-all font-mono text-xs text-muted-foreground">{c.trackingUrl || "—"}</TableCell>
              <TableCell className="text-right tabular-nums">{cfg.options.filter((o) => o.carrierId === c.id).length}</TableCell>
              <TableCell>{c.active ? <Badge variant="ok">Activa</Badge> : <Badge variant="secondary">Inactiva</Badge>}</TableCell>
              {canEdit && (
                <TableCell className="text-right">
                  <div className="flex justify-end gap-1">
                    <Button variant="outline" size="sm" onClick={() => setEdit(c)}>
                      Editar
                    </Button>
                    <Button variant="ghost" size="icon" aria-label={`Eliminar ${c.name}`} onClick={() => setDel(c)}>
                      <Trash2 />
                    </Button>
                  </div>
                </TableCell>
              )}
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {edit && <CarrierDialog carrier={edit === "new" ? null : edit} onClose={() => setEdit(null)} />}
      <Confirm
        open={Boolean(del)}
        title={`¿Eliminar ${del?.name ?? ""}?`}
        text="Solo se puede eliminar una paquetería que ninguna opción de entrega usa."
        confirmLabel="Eliminar paquetería"
        busy={remove.isPending}
        onConfirm={() => del && remove.mutate(del)}
        onClose={() => setDel(null)}
      />
    </>
  );
}

function CarrierDialog({ carrier, onClose }: { carrier: Carrier | null; onClose: () => void }) {
  const refresh = useRefresh();
  const [c, setC] = useState<Carrier>(carrier ?? { id: "", name: "", trackingUrl: "", active: true });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => sapi.saveShipping("carriers", { ...c, name: c.name.trim(), trackingUrl: c.trackingUrl.trim() }),
    onSuccess: () => {
      refresh();
      toast.success(carrier ? "Paquetería guardada" : "Paquetería creada");
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      toast.error(errorMessage(e));
    },
  });
  function submit() {
    const e: Record<string, string> = {};
    if (!c.name.trim()) e.name = "Escribe el nombre.";
    const u = c.trackingUrl.trim();
    if (u && !/^https:\/\/\S+$/.test(u)) e.trackingUrl = "Escribe una URL https completa.";
    else if (u && !u.includes("{tracking}")) e.trackingUrl = "Incluye {tracking} donde va el número de guía.";
    setErrors(e);
    if (Object.keys(e).length === 0) save.mutate();
  }
  const sample = c.trackingUrl.includes("{tracking}") ? c.trackingUrl.replace("{tracking}", "1234567890") : "";
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{carrier ? `Editar ${carrier.name}` : "Nueva paquetería"}</DialogTitle>
          <DialogDescription>La empresa que entrega tus pedidos.</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <FormField id="car-name" label="Nombre" required error={errors.name}>
            <Input value={c.name} maxLength={80} placeholder="Estafeta" onChange={(e) => setC({ ...c, name: e.target.value })} />
          </FormField>
          <FormField id="car-url" label="URL de rastreo" error={errors.trackingUrl} description="Opcional. Usa {tracking} donde va el número de guía.">
            <Input value={c.trackingUrl} className="font-mono text-xs" placeholder="https://rastreo.paqueteria.mx/?guia={tracking}" onChange={(e) => setC({ ...c, trackingUrl: e.target.value })} />
          </FormField>
          {sample && <p className="break-all text-xs text-muted-foreground">Ejemplo con la guía 1234567890: {sample}</p>}
          <div className="flex items-center justify-between gap-4">
            <span className="text-sm font-medium">Paquetería activa</span>
            <Switch checked={c.active} onChange={(v) => setC({ ...c, active: v })} label="Paquetería activa" />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancelar
            </Button>
            <Button type="submit" disabled={save.isPending}>
              {save.isPending ? "Guardando…" : carrier ? "Guardar" : "Crear paquetería"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// ---------- holidays ----------

function longDate(iso: string): string {
  const d = new Date(iso + "T12:00:00");
  return d.toLocaleDateString("es-MX", { weekday: "long", day: "numeric", month: "long" });
}

export function CalendarTab({ cfg, canEdit }: { cfg: ShippingConfig; canEdit: boolean }) {
  const refresh = useRefresh();
  const [date, setDate] = useState("");
  const [name, setName] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [del, setDel] = useState<Holiday | null>(null);
  const thisYear = new Date().getFullYear();
  const byYear = useMemo(() => {
    const m = new Map<string, Holiday[]>();
    for (const h of cfg.holidays) m.set(h.date.slice(0, 4), [...(m.get(h.date.slice(0, 4)) ?? []), h]);
    return [...m.entries()].sort(([a], [b]) => a.localeCompare(b));
  }, [cfg.holidays]);
  const add = useMutation({
    mutationFn: () => sapi.saveHoliday({ date, name: name.trim() }),
    onSuccess: () => {
      refresh();
      toast.success("Día agregado al calendario");
      setDate("");
      setName("");
      setErr(null);
    },
    onError: (e) => setErr(errorMessage(e)),
  });
  const official = useMutation({
    mutationFn: (y: number) => sapi.holidayDefaults(y),
    onSuccess: (r, y) => {
      refresh();
      toast.success(`${r.items.length} feriados oficiales de ${y} en el calendario`);
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const remove = useMutation({
    mutationFn: (h: Holiday) => sapi.deleteHoliday(h.date),
    onSuccess: () => {
      refresh();
      toast.success("Día eliminado");
      setDel(null);
    },
    onError: (e) => {
      toast.error(errorMessage(e));
      setDel(null);
    },
  });
  const today = new Date().toISOString().slice(0, 10);

  return (
    <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_340px]">
      <div className="grid content-start gap-4">
        {cfg.holidays.length === 0 && (
          <EmptyState
            icon={CalendarDays}
            title="Tu calendario está vacío"
            text="Los días del calendario no cuentan para despachar ni para entregar, así la fecha prometida al comprador es realista."
            action={
              canEdit && (
                <Button onClick={() => official.mutate(thisYear)} disabled={official.isPending}>
                  Cargar feriados oficiales de {thisYear}
                </Button>
              )
            }
          />
        )}
        {byYear.map(([year, list]) => (
          <Section key={year} title={year} description={`${list.length} día${list.length === 1 ? "" : "s"} sin operación`}>
            <ul className="grid gap-px">
              {list.map((h) => (
                <li key={h.date} className={`flex items-center justify-between gap-3 border-b py-2 last:border-0 ${h.date < today ? "opacity-55" : ""}`}>
                  <div>
                    <div className="text-sm font-medium">{h.name}</div>
                    <div className="text-xs capitalize text-muted-foreground">{longDate(h.date)}</div>
                  </div>
                  {canEdit && (
                    <Button variant="ghost" size="icon" aria-label={`Eliminar ${h.name}`} onClick={() => setDel(h)}>
                      <Trash2 />
                    </Button>
                  )}
                </li>
              ))}
            </ul>
          </Section>
        ))}
      </div>
      {canEdit && (
        <div className="grid content-start gap-4">
          <Section title="Agregar un día" description="Inventarios, cierres de fin de año, días locales.">
            <form
              className="grid gap-4"
              noValidate
              onSubmit={(e) => {
                e.preventDefault();
                if (!date) setErr("Elige la fecha.");
                else if (!name.trim()) setErr("Escribe el motivo, por ejemplo Inventario anual.");
                else add.mutate();
              }}
            >
              <FormField id="hol-date" label="Fecha">
                <Input type="date" value={date} onChange={(e) => setDate(e.target.value)} />
              </FormField>
              <FormField id="hol-name" label="Motivo">
                <Input value={name} maxLength={80} placeholder="Inventario anual" onChange={(e) => setName(e.target.value)} />
              </FormField>
              {err && <Notice tone="bad">{err}</Notice>}
              <Button type="submit" disabled={add.isPending}>
                {add.isPending ? "Agregando…" : "Agregar al calendario"}
              </Button>
            </form>
          </Section>
          <Section title="Feriados oficiales" description="Días de descanso obligatorio de la Ley Federal del Trabajo. Se pueden cargar otra vez sin duplicar.">
            <div className="flex flex-wrap gap-2">
              {[thisYear, thisYear + 1].map((y) => (
                <Button key={y} variant="outline" disabled={official.isPending} onClick={() => official.mutate(y)}>
                  Cargar {y}
                </Button>
              ))}
            </div>
          </Section>
        </div>
      )}
      <Confirm
        open={Boolean(del)}
        title={`¿Quitar ${del?.name ?? ""} del calendario?`}
        text="Ese día volverá a contar como hábil en las fechas de entrega."
        confirmLabel="Quitar día"
        busy={remove.isPending}
        onConfirm={() => del && remove.mutate(del)}
        onClose={() => setDel(null)}
      />
    </div>
  );
}
