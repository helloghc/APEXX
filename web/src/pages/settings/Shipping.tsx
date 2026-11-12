import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { CheckCircle2, MoreHorizontal, PackageCheck, Plus, Sparkles, Trash2, Truck, XCircle } from "lucide-react";
import { errorMessage, fieldErrors, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { toast } from "@/components/ui/sonner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatMoney } from "@/money";
import { can } from "@/perms";
import { href, navigate } from "@/router";
import { ErrorBox, Notice } from "../common";
import { sapi, type Bracket, type Quote, type ShippingConfig, type ShippingOption } from "./api";
import { Confirm, EmptyState, Loading, MoneyInput, ReadOnlyNote, Section, SubPage, Switch } from "./kit";
import { describeRate, gramsToKg, kgToGrams, validateBrackets } from "./logic";
import { CalendarTab, CarriersTab, ProfilesTab, ZonesTab } from "./ShippingParts";

const TABS = [
  { id: "opciones", label: "Opciones de entrega" },
  { id: "zonas", label: "Zonas" },
  { id: "perfiles", label: "Perfiles" },
  { id: "paqueterias", label: "Paqueterías" },
  { id: "calendario", label: "Calendario" },
  { id: "simulador", label: "Simulador" },
];

export function Shipping({ me, tab }: { me: Me; tab?: string }) {
  const qc = useQueryClient();
  const current = TABS.find((t) => t.id === tab)?.id ?? "opciones";
  const q = useQuery({ queryKey: ["settings", "shipping"], queryFn: sapi.shipping });
  const canEdit = can(me, "settings", "admin");
  const defaults = useMutation({
    mutationFn: sapi.shippingDefaults,
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: ["settings", "shipping"] });
      toast.success(r.added > 0 ? "Configuración recomendada lista. Ya aplica en el carrito." : "Ya tenías la configuración recomendada.");
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const cfg = q.data;
  const empty = cfg && cfg.zones.length === 0 && cfg.options.length === 0;

  return (
    <SubPage
      title="Envíos"
      sub="A dónde entregas, con quién, cuánto cuesta y cuándo llega. Cada cambio aplica al siguiente carrito, sin esperar una reindexación."
    >
      <div className="-mx-4 overflow-x-auto px-4 md:mx-0 md:px-0">
        <Tabs value={current} onValueChange={(v) => navigate("configuracion", "envios", ...(v === "opciones" ? [] : [v]))}>
          <TabsList>
            {TABS.map((t) => (
              <TabsTrigger key={t.id} value={t.id}>
                {t.label}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </div>
      <ReadOnlyNote show={!canEdit} />
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {q.isPending && <Loading />}
      {cfg && empty && current !== "calendario" && current !== "paqueterias" && current !== "perfiles" && current !== "zonas" ? (
        <EmptyState
          icon={Truck}
          title="Configura tus envíos"
          text={
            <>
              {cfg.legacy
                ? `Hoy tu tienda cobra una sola tarifa (${cfg.legacy.name}: ${formatMoney(cfg.legacy.base.amount)} + ${formatMoney(cfg.legacy.perKg.amount)} por kg). `
                : ""}
              Empieza con la configuración recomendada para México: zona metropolitana y resto del país, envío estándar y exprés, carga pesada, envío gratis desde $1,499 y feriados
              oficiales. Después la ajustas a tu gusto.
            </>
          }
          action={
            canEdit && (
              <>
                <Button onClick={() => defaults.mutate()} disabled={defaults.isPending}>
                  <Sparkles /> {defaults.isPending ? "Creando…" : "Usar configuración recomendada"}
                </Button>
                <Button variant="outline" onClick={() => navigate("configuracion", "envios", "zonas")}>
                  Empezar desde cero
                </Button>
              </>
            )
          }
        />
      ) : (
        cfg && (
          <>
            {current === "opciones" && <OptionsTab cfg={cfg} canEdit={canEdit} />}
            {current === "zonas" && <ZonesTab cfg={cfg} canEdit={canEdit} />}
            {current === "perfiles" && <ProfilesTab cfg={cfg} canEdit={canEdit} />}
            {current === "paqueterias" && <CarriersTab cfg={cfg} canEdit={canEdit} />}
            {current === "calendario" && <CalendarTab cfg={cfg} canEdit={canEdit} />}
            {current === "simulador" && <Simulator cfg={cfg} />}
          </>
        )
      )}
    </SubPage>
  );
}

const WEEKEND_LABEL = { none: "Lunes a viernes", saturday: "Lunes a sábado", all: "Todos los días" } as const;

function estimateText(o: ShippingOption): string {
  const e = o.estimate;
  const days = e.minDays === e.maxDays ? `${e.maxDays} día${e.maxDays === 1 ? "" : "s"} hábil${e.maxDays === 1 ? "" : "es"}` : `${e.minDays} a ${e.maxDays} días hábiles`;
  if (o.kind === "pickup") return e.maxDays === 0 ? "Listo el mismo día" : `Listo en ${days}`;
  return e.maxDays === 0 ? "El mismo día" : days;
}

function OptionsTab({ cfg, canEdit }: { cfg: ShippingConfig; canEdit: boolean }) {
  const qc = useQueryClient();
  const [edit, setEdit] = useState<ShippingOption | "new" | null>(null);
  const [del, setDel] = useState<ShippingOption | null>(null);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["settings", "shipping"] });
  const toggle = useMutation({
    mutationFn: (o: ShippingOption) => sapi.saveShipping("options", { ...o, active: !o.active }),
    onSuccess: (_r, o) => {
      refresh();
      toast.success(o.active ? `“${o.name}” desactivada` : `“${o.name}” activada`);
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const remove = useMutation({
    mutationFn: (o: ShippingOption) => sapi.deleteShipping("options", o.id),
    onSuccess: () => {
      refresh();
      void qc.invalidateQueries({ queryKey: ["settings", "locations"] });
      toast.success("Opción eliminada");
      setDel(null);
    },
    onError: (e) => {
      toast.error(errorMessage(e));
      setDel(null);
    },
  });
  const name = (list: { id: string; name: string }[], id?: string) => list.find((x) => x.id === id)?.name ?? "";
  const canCreate = cfg.zones.length > 0 || cfg.locations.some((l) => l.pickupEnabled);

  if (cfg.options.length === 0)
    return (
      <EmptyState
        icon={PackageCheck}
        title="Crea tu primera opción de entrega"
        text={
          canCreate
            ? "Una opción es lo que el comprador elige en el carrito: por ejemplo “Estándar, $99, 2 a 4 días”."
            : "Primero crea una zona de envío (a dónde entregas). Luego agrega las opciones de esa zona."
        }
        action={
          canEdit &&
          (canCreate ? (
            <Button onClick={() => setEdit("new")}>Nueva opción</Button>
          ) : (
            <Button onClick={() => navigate("configuracion", "envios", "zonas")}>Ir a Zonas</Button>
          ))
        }
      />
    );

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          {cfg.options.filter((o) => o.active).length} activas de {cfg.options.length}. Gana la zona más específica: código postal, luego estado, luego país.
        </p>
        {canEdit && (
          <Button onClick={() => setEdit("new")}>
            <Plus /> Nueva opción
          </Button>
        )}
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Opción</TableHead>
            <TableHead>Cobertura</TableHead>
            <TableHead>Tarifa</TableHead>
            <TableHead>Entrega</TableHead>
            <TableHead>Activa</TableHead>
            {canEdit && <TableHead className="w-10 text-right">Acciones</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {cfg.options.map((o) => {
            const profile = cfg.profiles.find((p) => p.id === o.profileId);
            return (
              <TableRow key={o.id} data-testid={`option-${o.id}`} className={o.active ? "" : "opacity-60"}>
                <TableCell>
                  {canEdit ? (
                    <button type="button" className="cursor-pointer text-left font-medium hover:underline" onClick={() => setEdit(o)}>
                      {o.name}
                    </button>
                  ) : (
                    <span className="font-medium">{o.name}</span>
                  )}
                  <div className="text-xs text-muted-foreground">{o.kind === "pickup" ? "Recolección en tienda" : name(cfg.carriers, o.carrierId) || "Sin paquetería"}</div>
                </TableCell>
                <TableCell className="text-sm">
                  {o.kind === "pickup" ? name(cfg.locations, o.locationId) : name(cfg.zones, o.zoneId)}
                  {profile && profile.kind !== "default" && (
                    <div>
                      <Badge variant="secondary">{profile.name}</Badge>
                    </div>
                  )}
                </TableCell>
                <TableCell className="max-w-72 text-sm">{describeRate(o.rate)}</TableCell>
                <TableCell className="text-sm">
                  {estimateText(o)}
                  <div className="text-xs text-muted-foreground">
                    {o.estimate.cutoff ? `Corte ${o.estimate.cutoff} · ` : ""}
                    {WEEKEND_LABEL[o.estimate.weekend] ?? ""}
                  </div>
                </TableCell>
                <TableCell>
                  <Switch checked={o.active} disabled={!canEdit || toggle.isPending} label={`${o.name} activa`} onChange={() => toggle.mutate(o)} />
                </TableCell>
                {canEdit && (
                  <TableCell className="text-right">
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon" aria-label={`Acciones de ${o.name}`}>
                          <MoreHorizontal />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onSelect={() => setEdit(o)}>Editar</DropdownMenuItem>
                        <DropdownMenuItem onSelect={() => setEdit({ ...o, id: "", name: `${o.name} (copia)`, active: false })}>Duplicar</DropdownMenuItem>
                        <DropdownMenuItem className="text-destructive" onSelect={() => setDel(o)}>
                          <Trash2 /> Eliminar
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                )}
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
      {edit && <OptionSheet cfg={cfg} option={edit === "new" ? null : edit} onClose={() => setEdit(null)} />}
      <Confirm
        open={Boolean(del)}
        title={`¿Eliminar “${del?.name ?? ""}”?`}
        text="Los compradores dejarán de verla de inmediato. Los pedidos que ya la usaron conservan sus datos."
        confirmLabel="Eliminar opción"
        busy={remove.isPending}
        onConfirm={() => del && remove.mutate(del)}
        onClose={() => setDel(null)}
      />
    </>
  );
}

type RateMode = "flat" | "weight-formula" | "weight" | "subtotal" | "free" | "live";

function modeOf(o: ShippingOption): RateMode {
  const r = o.rate;
  if (r.type === "weight") return r.brackets.length ? "weight" : "weight-formula";
  if (r.type === "subtotal") return "subtotal";
  if (r.type === "live") return "live";
  return r.amount === 0 && r.freeOver === 0 ? "free" : "flat";
}

function blank(cfg: ShippingConfig): ShippingOption {
  const hasZone = cfg.zones.length > 0;
  return {
    id: "",
    name: "",
    kind: hasZone ? "delivery" : "pickup",
    zoneId: cfg.zones[0]?.id ?? "",
    profileId: cfg.profiles.find((p) => p.kind === "default")?.id ?? "",
    carrierId: "",
    locationId: cfg.locations.find((l) => l.pickupEnabled)?.id ?? "",
    rate: { type: "flat", amount: 9900, perKg: 0, brackets: [], freeOver: 0 },
    estimate: { minDays: 2, maxDays: 4, cutoff: "14:00", weekend: "none" },
    maxWeightGrams: 0,
    active: true,
    position: 0,
  };
}

function OptionSheet({ cfg, option, onClose }: { cfg: ShippingConfig; option: ShippingOption | null; onClose: () => void }) {
  const qc = useQueryClient();
  const [o, setO] = useState<ShippingOption>(() => structuredClone(option ?? blank(cfg)));
  const [mode, setMode] = useState<RateMode>(() => (option ? modeOf(option) : "flat"));
  const [maxKg, setMaxKg] = useState(o.maxWeightGrams ? gramsToKg(o.maxWeightGrams) : "");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const creating = !o.id;
  const pickupLocs = cfg.locations.filter((l) => l.pickupEnabled);

  const save = useMutation({
    mutationFn: (doc: ShippingOption) => sapi.saveShipping("options", doc),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["settings", "shipping"] });
      void qc.invalidateQueries({ queryKey: ["settings", "locations"] });
      toast.success(creating ? "Opción creada. Ya aparece en el carrito." : "Opción guardada. Ya aplica en el carrito.");
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      toast.error(errorMessage(e));
    },
  });

  const setRate = (p: Partial<ShippingOption["rate"]>) => setO({ ...o, rate: { ...o.rate, ...p } });
  const setEst = (p: Partial<ShippingOption["estimate"]>) => setO({ ...o, estimate: { ...o.estimate, ...p } });
  const setBracket = (i: number, p: Partial<Bracket>) => setRate({ brackets: o.rate.brackets.map((b, j) => (j === i ? { ...b, ...p } : b)) });

  function changeMode(m: RateMode) {
    setMode(m);
    if (m === "flat") setRate({ type: "flat", brackets: [], perKg: 0, amount: o.rate.amount || 9900 });
    if (m === "free") setRate({ type: "flat", brackets: [], perKg: 0, amount: 0, freeOver: 0 });
    if (m === "live") setRate({ type: "live", brackets: [], perKg: 0, amount: o.rate.amount || 14900 });
    if (m === "weight-formula") setRate({ type: "weight", brackets: [], amount: o.rate.amount || 9900, perKg: o.rate.perKg || 1500 });
    if (m === "weight")
      setRate({ type: "weight", amount: 0, brackets: o.rate.type === "weight" && o.rate.brackets.length ? o.rate.brackets : [{ upTo: 1000, amount: 9900 }, { upTo: 5000, amount: 14900 }] });
    if (m === "subtotal")
      setRate({ type: "subtotal", amount: 0, perKg: 0, brackets: o.rate.type === "subtotal" && o.rate.brackets.length ? o.rate.brackets : [{ upTo: 50000, amount: 14900 }, { upTo: 0, amount: 9900 }] });
  }

  function submit() {
    const e: Record<string, string> = {};
    if (!o.name.trim()) e.name = "Escribe un nombre, por ejemplo Estándar.";
    if (o.kind === "delivery" && !o.zoneId) e.zoneId = "Elige una zona.";
    if (o.kind === "pickup" && !o.locationId) e.locationId = "Elige dónde se recoge.";
    if (mode === "weight" || mode === "subtotal") {
      const b = o.rate.brackets.length === 0 ? "Agrega al menos un tramo." : validateBrackets(o.rate.brackets);
      if (b) e["rate.brackets"] = b;
    }
    if (o.estimate.maxDays < o.estimate.minDays) e["estimate.maxDays"] = "El máximo no puede ser menor que el mínimo.";
    let maxWeightGrams = 0;
    if (maxKg.trim()) {
      const g = kgToGrams(maxKg);
      if (g === null) e.maxWeightGrams = "Escribe el peso en kg, por ejemplo 30.";
      else maxWeightGrams = g;
    }
    setErrors(e);
    if (Object.keys(e).length) return;
    save.mutate({ ...o, name: o.name.trim(), maxWeightGrams, profileId: o.kind === "pickup" ? "" : o.profileId });
  }

  const unit = mode === "weight" ? "kg" : "$";

  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="grid content-start gap-5 p-6">
        <SheetHeader>
          <SheetTitle>{creating ? "Nueva opción de entrega" : `Editar ${option?.name ?? ""}`}</SheetTitle>
          <SheetDescription>Lo que el comprador ve y elige en el carrito.</SheetDescription>
        </SheetHeader>
        <form
          className="grid gap-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <FormField id="op-name" label="Nombre para el comprador" required error={errors.name}>
            <Input value={o.name} maxLength={80} placeholder="Estándar" onChange={(e) => setO({ ...o, name: e.target.value })} />
          </FormField>
          <div className="grid gap-4 sm:grid-cols-2">
            <FormField id="op-kind" label="Tipo">
              <Select value={o.kind} onValueChange={(v) => setO({ ...o, kind: v as ShippingOption["kind"] })}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="delivery">Envío a domicilio</SelectItem>
                  <SelectItem value="pickup">Recolección en tienda</SelectItem>
                </SelectContent>
              </Select>
            </FormField>
            {o.kind === "delivery" ? (
              <FormField id="op-zone" label="Zona" required error={errors.zoneId}>
                <Select value={o.zoneId ?? ""} onValueChange={(v) => setO({ ...o, zoneId: v })}>
                  <SelectTrigger>
                    <SelectValue placeholder="Elige una zona" />
                  </SelectTrigger>
                  <SelectContent>
                    {cfg.zones.map((z) => (
                      <SelectItem key={z.id} value={z.id}>
                        {z.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </FormField>
            ) : (
              <FormField id="op-loc" label="Se recoge en" required error={errors.locationId}>
                <Select value={o.locationId ?? ""} onValueChange={(v) => setO({ ...o, locationId: v })}>
                  <SelectTrigger>
                    <SelectValue placeholder="Elige una ubicación" />
                  </SelectTrigger>
                  <SelectContent>
                    {pickupLocs.map((l) => (
                      <SelectItem key={l.id} value={l.id}>
                        {l.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </FormField>
            )}
          </div>
          {o.kind === "pickup" && pickupLocs.length === 0 && (
            <Notice tone="warn">
              Ninguna ubicación ofrece recolección.{" "}
              <a className="underline" href={href("configuracion", "ubicaciones")}>
                Actívala en Ubicaciones
              </a>{" "}
              y vuelve aquí.
            </Notice>
          )}
          {o.kind === "delivery" && (
            <div className="grid gap-4 sm:grid-cols-2">
              <FormField id="op-carrier" label="Paquetería" error={errors.carrierId}>
                <Select value={o.carrierId || "_"} onValueChange={(v) => setO({ ...o, carrierId: v === "_" ? "" : v })}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="_">Sin paquetería (reparto propio)</SelectItem>
                    {cfg.carriers.map((c) => (
                      <SelectItem key={c.id} value={c.id}>
                        {c.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </FormField>
              <FormField id="op-profile" label="Perfil de productos" error={errors.profileId} description="Qué productos transporta esta opción.">
                <Select value={o.profileId || "_"} onValueChange={(v) => setO({ ...o, profileId: v === "_" ? "" : v })}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {!cfg.profiles.some((p) => p.kind === "default") && <SelectItem value="_">Estándar</SelectItem>}
                    {cfg.profiles.map((p) => (
                      <SelectItem key={p.id} value={p.id}>
                        {p.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </FormField>
            </div>
          )}

          <h3 className="border-t pt-4 text-sm font-semibold">Tarifa</h3>
          <FormField id="op-mode" label="Cómo se cobra" error={errors["rate.type"]}>
            <Select value={mode} onValueChange={(v) => changeMode(v as RateMode)}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="flat">Tarifa fija</SelectItem>
                <SelectItem value="free">Siempre gratis</SelectItem>
                <SelectItem value="weight">Por tramos de peso</SelectItem>
                <SelectItem value="weight-formula">Base + precio por kg</SelectItem>
                <SelectItem value="subtotal">Por monto del pedido</SelectItem>
                <SelectItem value="live">En vivo de la paquetería</SelectItem>
              </SelectContent>
            </Select>
          </FormField>
          {mode === "live" && (
            <div className="grid gap-3 rounded-md border p-3">
              <p className="text-sm text-muted-foreground">El carrito pregunta el precio a la paquetería de esta opción (debe estar conectada). Si no responde en menos de un segundo, se cobra la tarifa de respaldo y la venta sigue.</p>
              {!cfg.carriers.some((c) => c.id === o.carrierId && c.provider) && <p className="text-sm text-bad">Elige arriba una paquetería conectada (en Paqueterías, «Conexión»).</p>}
              <FormField id="op-fallback" label="Tarifa de respaldo" error={errors["rate.amount"]}>
                <MoneyInput cents={o.rate.amount} onChange={(c) => c !== null && setRate({ amount: c })} />
              </FormField>
            </div>
          )}
          {(mode === "flat" || mode === "weight-formula") && (
            <div className="grid gap-4 sm:grid-cols-2">
              <FormField id="op-amount" label={mode === "flat" ? "Precio" : "Precio base"} error={errors["rate.amount"]}>
                <MoneyInput cents={o.rate.amount} onChange={(c) => c !== null && setRate({ amount: c })} />
              </FormField>
              {mode === "weight-formula" && (
                <FormField id="op-perkg" label="Por cada kg" description="Se cobra por kg iniciado.">
                  <MoneyInput cents={o.rate.perKg} onChange={(c) => c !== null && setRate({ perKg: c })} />
                </FormField>
              )}
            </div>
          )}
          {(mode === "weight" || mode === "subtotal") && (
            <div className="grid gap-2">
              <div className="grid grid-cols-[1fr_1fr_36px] gap-2 text-xs font-medium text-muted-foreground">
                <span>{mode === "weight" ? "Hasta (kg)" : "Pedidos hasta ($)"}</span>
                <span>Precio del envío</span>
                <span />
              </div>
              {o.rate.brackets.map((b, i) => (
                <div key={i} className="grid grid-cols-[1fr_1fr_36px] items-center gap-2">
                  {b.upTo === 0 ? (
                    <span className="flex h-9 items-center rounded-md border bg-muted px-3 text-sm text-muted-foreground">Sin límite</span>
                  ) : mode === "weight" ? (
                    <Input
                      inputMode="decimal"
                      aria-label={`Tramo ${i + 1}: hasta kg`}
                      defaultValue={gramsToKg(b.upTo)}
                      onChange={(e) => {
                        const g = kgToGrams(e.target.value);
                        if (g !== null && g > 0) setBracket(i, { upTo: g });
                      }}
                    />
                  ) : (
                    <MoneyInput aria-label={`Tramo ${i + 1}: pedidos hasta`} cents={b.upTo} onChange={(c) => c !== null && c > 0 && setBracket(i, { upTo: c })} />
                  )}
                  <MoneyInput aria-label={`Tramo ${i + 1}: precio`} cents={b.amount} onChange={(c) => c !== null && setBracket(i, { amount: c })} />
                  <Button type="button" variant="ghost" size="icon" aria-label={`Quitar tramo ${i + 1}`} onClick={() => setRate({ brackets: o.rate.brackets.filter((_, j) => j !== i) })}>
                    <Trash2 />
                  </Button>
                </div>
              ))}
              {errors["rate.brackets"] && <p className="text-xs font-medium text-destructive">{errors["rate.brackets"]}</p>}
              <div className="flex flex-wrap gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    const bs = o.rate.brackets;
                    const open = bs.length > 0 && (bs[bs.length - 1] as Bracket).upTo === 0;
                    const closed = open ? bs.slice(0, -1) : bs;
                    const lastUp = closed.length ? (closed[closed.length - 1] as Bracket).upTo : 0;
                    const step = mode === "weight" ? 5000 : 50000;
                    const next = [...closed, { upTo: lastUp + step, amount: closed.length ? (closed[closed.length - 1] as Bracket).amount : 9900 }];
                    setRate({ brackets: open ? [...next, bs[bs.length - 1] as Bracket] : next });
                  }}
                >
                  <Plus /> Agregar tramo
                </Button>
                {mode === "subtotal" && !o.rate.brackets.some((b) => b.upTo === 0) && (
                  <Button type="button" variant="outline" size="sm" onClick={() => setRate({ brackets: [...o.rate.brackets, { upTo: 0, amount: 0 }] })}>
                    Agregar tramo sin límite
                  </Button>
                )}
              </div>
              {mode === "weight" && (
                <FormField id="op-extra" label="Kg adicional después del último tramo" description={`Con $0, los pedidos más pesados que el último tramo no pueden usar esta opción. Unidad: ${unit}.`}>
                  <MoneyInput cents={o.rate.perKg} onChange={(c) => c !== null && setRate({ perKg: c })} />
                </FormField>
              )}
            </div>
          )}
          {mode !== "free" && (
            <FormField id="op-free" label="Gratis en pedidos desde" description="Monto después de descuentos. Con $0 nunca es gratis.">
              <MoneyInput cents={o.rate.freeOver} onChange={(c) => c !== null && setRate({ freeOver: c })} />
            </FormField>
          )}

          <h3 className="border-t pt-4 text-sm font-semibold">{o.kind === "pickup" ? "Cuándo está listo" : "Tiempo de entrega"}</h3>
          <div className="grid gap-4 sm:grid-cols-2">
            <FormField id="op-min" label="Mínimo (días hábiles)">
              <Input type="number" min={0} max={120} value={o.estimate.minDays} onChange={(e) => setEst({ minDays: Math.max(0, Math.trunc(Number(e.target.value) || 0)) })} />
            </FormField>
            <FormField id="op-max" label="Máximo (días hábiles)" error={errors["estimate.maxDays"]}>
              <Input type="number" min={0} max={120} value={o.estimate.maxDays} onChange={(e) => setEst({ maxDays: Math.max(0, Math.trunc(Number(e.target.value) || 0)) })} />
            </FormField>
            <FormField id="op-cutoff" label="Hora de corte" error={errors["estimate.cutoff"]} description="Después de esta hora el pedido sale al siguiente día hábil.">
              <Input type="time" value={o.estimate.cutoff} onChange={(e) => setEst({ cutoff: e.target.value })} />
            </FormField>
            <FormField id="op-weekend" label="Días que cuentan">
              <Select value={o.estimate.weekend} onValueChange={(v) => setEst({ weekend: v as ShippingOption["estimate"]["weekend"] })}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">Lunes a viernes</SelectItem>
                  <SelectItem value="saturday">Lunes a sábado</SelectItem>
                  <SelectItem value="all">Todos los días</SelectItem>
                </SelectContent>
              </Select>
            </FormField>
          </div>
          <p className="text-xs text-muted-foreground">
            Los feriados del{" "}
            <a className="underline" href={href("configuracion", "envios", "calendario")}>
              calendario
            </a>{" "}
            nunca cuentan. Zona horaria: {cfg.timezone}.
          </p>
          {o.kind === "delivery" && (
            <FormField id="op-maxkg" label="Peso máximo del pedido (kg)" error={errors.maxWeightGrams} description="Opcional. Pedidos más pesados no ven esta opción.">
              <Input inputMode="decimal" value={maxKg} placeholder="Sin límite" onChange={(e) => setMaxKg(e.target.value)} />
            </FormField>
          )}
          <div className="flex items-start justify-between gap-4 border-t pt-4">
            <div className="grid gap-1">
              <span className="text-sm font-semibold">Opción activa</span>
              <span className="text-sm text-muted-foreground">Las opciones inactivas no se ofrecen a los compradores.</span>
            </div>
            <Switch checked={o.active} onChange={(v) => setO({ ...o, active: v })} label="Opción activa" />
          </div>
          <div className="flex justify-end gap-2 border-t pt-4">
            <Button type="button" variant="outline" onClick={onClose}>
              Cancelar
            </Button>
            <Button type="submit" disabled={save.isPending}>
              {save.isPending ? "Guardando…" : creating ? "Crear opción" : "Guardar"}
            </Button>
          </div>
        </form>
      </SheetContent>
    </Sheet>
  );
}

// ---------- simulator ----------

function Simulator({ cfg }: { cfg: ShippingConfig }) {
  const [cp, setCp] = useState("");
  const [subtotal, setSubtotal] = useState(89900);
  const [kg, setKg] = useState("1");
  const [profile, setProfile] = useState("");
  const [at, setAt] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const run = useMutation({
    mutationFn: () =>
      sapi.quote({ postalCode: cp, subtotal, weightGrams: kgToGrams(kg) ?? 0, profile, ...(at ? { at: new Date(at).toISOString() } : {}) }),
    onError: (e) => setErr(errorMessage(e)),
    onSuccess: () => setErr(null),
  });
  const res = run.data;
  const on = res?.quotes.filter((q) => q.available) ?? [];
  const off = res?.quotes.filter((q) => !q.available) ?? [];

  return (
    <div className="grid gap-5 xl:grid-cols-[340px_minmax(0,1fr)]">
      <Section title="Probar un pedido" description="Mira exactamente qué vería un comprador y por qué.">
        <form
          className="grid gap-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            if (cp && !/^\d{5}$/.test(cp)) {
              setErr("El código postal tiene 5 dígitos.");
              return;
            }
            if (kgToGrams(kg) === null) {
              setErr("Escribe el peso en kg, por ejemplo 1.5.");
              return;
            }
            run.mutate();
          }}
        >
          <FormField id="sim-cp" label="Código postal de entrega">
            <Input value={cp} inputMode="numeric" maxLength={5} placeholder="06600" onChange={(e) => setCp(e.target.value.replace(/\D/g, ""))} />
          </FormField>
          <FormField id="sim-sub" label="Monto del pedido">
            <MoneyInput cents={subtotal} onChange={(c) => c !== null && setSubtotal(c)} />
          </FormField>
          <FormField id="sim-kg" label="Peso total (kg)">
            <Input inputMode="decimal" value={kg} onChange={(e) => setKg(e.target.value)} />
          </FormField>
          <FormField id="sim-profile" label="Perfil de los productos">
            <Select value={profile || "_"} onValueChange={(v) => setProfile(v === "_" ? "" : v)}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="_">Estándar</SelectItem>
                {cfg.profiles
                  .filter((p) => p.kind !== "default")
                  .map((p) => (
                    <SelectItem key={p.id} value={p.id}>
                      {p.name}
                    </SelectItem>
                  ))}
              </SelectContent>
            </Select>
          </FormField>
          <FormField id="sim-at" label="Fecha y hora de la compra" description="Opcional: prueba la hora de corte y los feriados.">
            <Input type="datetime-local" value={at} onChange={(e) => setAt(e.target.value)} />
          </FormField>
          {err && <Notice tone="bad">{err}</Notice>}
          <Button type="submit" disabled={run.isPending}>
            {run.isPending ? "Calculando…" : "Simular"}
          </Button>
        </form>
      </Section>

      <div className="grid content-start gap-3" aria-live="polite">
        {!res && (
          <EmptyState icon={Truck} title="Sin simulación todavía" text="Escribe un código postal y un pedido de ejemplo. Verás cada opción, disponible o no, con su motivo, la fórmula del precio y la fecha estimada." />
        )}
        {res && (
          <>
            <p className="text-sm text-muted-foreground">
              {cp ? `Entrega en ${cp}${res.state ? ` (${res.state})` : ""}. ` : "Sin código postal: solo aplica la recolección. "}
              {on.length} opción{on.length === 1 ? "" : "es"} disponible{on.length === 1 ? "" : "s"}, {off.length} no disponible{off.length === 1 ? "" : "s"}.
            </p>
            {on.map((qt) => (
              <QuoteCard key={qt.id} q={qt} chosen={qt.id === res.default} />
            ))}
            {on.length === 0 && <Notice tone="warn">Ningún comprador podría terminar este pedido: no hay opción de entrega disponible.</Notice>}
            {off.length > 0 && <h3 className="pt-2 text-sm font-semibold">No disponibles</h3>}
            {off.map((qt) => (
              <QuoteCard key={qt.id} q={qt} chosen={false} />
            ))}
          </>
        )}
      </div>
    </div>
  );
}

function QuoteCard({ q, chosen }: { q: Quote; chosen: boolean }) {
  return (
    <div className={`grid gap-2 rounded-xl border bg-card p-4 ${q.available ? "" : "opacity-75"}`} data-testid={`quote-${q.id}`}>
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="flex items-start gap-2">
          {q.available ? <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-ok" aria-hidden="true" /> : <XCircle className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />}
          <div>
            <span className="font-medium">{q.name}</span>
            {q.carrier && <span className="text-sm text-muted-foreground"> · {q.carrier}</span>}
            {chosen && (
              <Badge variant="secondary" className="ml-2">
                Se preselecciona
              </Badge>
            )}
            <div className="text-sm text-muted-foreground">{q.reason}</div>
          </div>
        </div>
        {q.available && <span className="text-base font-semibold tabular-nums">{q.price === 0 ? "Gratis" : formatMoney(q.price)}</span>}
      </div>
      {q.available && (
        <dl className="grid gap-1 border-t pt-2 text-sm">
          <div className="flex flex-wrap gap-x-2">
            <dt className="text-muted-foreground">Precio:</dt>
            <dd>
              {q.priceReason}
              {q.freeMissing ? ` · faltan ${formatMoney(q.freeMissing)} para envío gratis` : ""}
            </dd>
          </div>
          {q.estimate && (
            <div className="flex flex-wrap gap-x-2">
              <dt className="text-muted-foreground">Fecha:</dt>
              <dd>
                {q.estimate.text}
                {q.estimate.notes.length > 0 && <span className="text-muted-foreground"> ({q.estimate.notes.join(" · ")})</span>}
              </dd>
            </div>
          )}
          {q.pickupAddress && (
            <div className="flex flex-wrap gap-x-2">
              <dt className="text-muted-foreground">Dirección:</dt>
              <dd>{q.pickupAddress}</dd>
            </div>
          )}
        </dl>
      )}
    </div>
  );
}
