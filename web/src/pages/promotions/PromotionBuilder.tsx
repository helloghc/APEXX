import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { ArrowLeft, CheckCircle2, CircleSlash, Clock, Copy, Loader2, Plus, Ticket, Trash2 } from "lucide-react";
import { ApiError, errorMessage, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { toast } from "@/components/ui/sonner";
import { Textarea } from "@/components/ui/textarea";
import { formatMoney } from "@/money";
import { ErrorBox, Notice, Page } from "@/pages/common";
import { can } from "@/perms";
import { promoApi, type Promotion, type PromotionView, type PromoType, type Redemption, type Target, type Tier } from "@/promoApi";
import { href, navigate } from "@/router";
import { CartEditor, SimulationTotals, type CartItem } from "./CartEditor";
import { cleanPromotion, emptyPromotion, storeDateTime, STORE_TZ, TYPE_HELP, TYPE_LABEL, validatePromotion } from "./lib";
import { ChipInput, Choice, DateTimeField, Field, NumField, PickedList, ProductPicker, Section, Skeleton, StatusBadge, useDebounced, useTitles } from "./shared";

const TYPES = Object.keys(TYPE_LABEL) as PromoType[];
const NONE = "__none";

export function PromotionBuilder({ me, id }: { me: Me; id?: string }) {
  const q = useQuery({ queryKey: ["promotion", id], queryFn: () => promoApi.get(id as string), enabled: Boolean(id) });
  const back = (
    <a className="inline-flex items-center gap-1 text-primary hover:underline" href={href("promociones")}>
      <ArrowLeft className="size-3.5" /> Promociones
    </a>
  );
  if (id && q.isPending)
    return (
      <Page title="Promoción" sub={back}>
        <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_22rem]">
          <div className="grid gap-4">
            <Skeleton className="h-40" />
            <Skeleton className="h-56" />
            <Skeleton className="h-40" />
          </div>
          <Skeleton className="h-72" />
        </div>
      </Page>
    );
  if (id && (q.error || !q.data))
    return (
      <Page title="Promoción" sub={back}>
        <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      </Page>
    );
  return <Builder key={id ?? "new"} me={me} saved={q.data?.promotion} redemptions={q.data?.redemptions ?? []} />;
}

function Builder({ me, saved, redemptions }: { me: Me; saved?: PromotionView; redemptions: Redemption[] }) {
  const qc = useQueryClient();
  const canEdit = can(me, "promotions", "edit");
  const isNew = !saved;
  const [p, setP] = useState<Promotion>(() => (saved ? { ...saved } : emptyPromotion("order_percent")));
  const [dirty, setDirty] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [serverError, setServerError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [cart, setCart] = useState<CartItem[]>([]);
  const [zip, setZip] = useState("");

  const facets = useQuery({ queryKey: ["promo-facets"], queryFn: promoApi.facets, staleTime: 120_000 });
  const campaigns = useQuery({ queryKey: ["campaigns"], queryFn: promoApi.campaigns, staleTime: 60_000 });
  // A first test cart so the preview says something from the start.
  const seed = useQuery({ queryKey: ["targets", ""], queryFn: () => promoApi.targets(""), staleTime: 60_000 });
  useEffect(() => {
    if (cart.length === 0 && seed.data?.length) {
      const items: CartItem[] = [];
      for (const pr of seed.data.slice(0, 2)) {
        const v = pr.variants.find((x) => x.price !== undefined && x.available > 0);
        if (v) items.push({ sku: v.sku, qty: 1, title: `${pr.title} · ${v.title}`, price: v.price });
      }
      if (items.length) setCart(items);
    }
    // Only when the catalog sample arrives.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seed.data]);

  const set = (patch: Partial<Promotion>) => {
    setP((old) => ({ ...old, ...patch }));
    setDirty(true);
  };
  const setCond = (patch: Partial<Promotion["conditions"]>) => set({ conditions: { ...p.conditions, ...patch } });
  const setStack = (patch: Partial<Promotion["stacking"]>) => set({ stacking: { ...p.stacking, ...patch } });

  const body = useMemo(() => cleanPromotion(p), [p]);
  const debounced = useDebounced(JSON.stringify({ body, cart: cart.map((c) => [c.sku, c.qty]), zip }), 350);
  const preview = useQuery({
    queryKey: ["promo-preview", debounced],
    queryFn: () =>
      promoApi.preview(body, cart.length ? { channel: "mx", postalCode: /^\d{5}$/.test(zip) ? zip : undefined, items: cart.map((c) => ({ sku: c.sku, qty: c.qty })) } : undefined),
    placeholderData: (prev) => prev,
    retry: false,
  });

  async function save(status?: Promotion["status"]) {
    const next = status ? { ...p, status } : p;
    const errs = validatePromotion(next);
    setErrors(errs);
    setServerError(null);
    if (Object.keys(errs).length > 0) {
      toast.error("Revisa los campos marcados");
      return;
    }
    setBusy(true);
    try {
      const out = isNew ? await promoApi.create(cleanPromotion(next)) : await promoApi.save(saved.id, cleanPromotion(next));
      toast.success(isNew ? "Promoción creada" : "Cambios guardados");
      setDirty(false);
      void qc.invalidateQueries({ queryKey: ["promotions"] });
      qc.setQueryData(["promotion", out.id], { promotion: out, redemptions });
      if (isNew) navigate("promociones", "p", out.id);
      else setP({ ...out });
    } catch (e) {
      if (e instanceof ApiError && e.problem.errors?.length) {
        const fe: Record<string, string> = {};
        for (const f of e.problem.errors) fe[f.field.replace(/^conditions\.|^stacking\./, "").replace(/\[\d+\].*$/, "")] = f.message;
        setErrors(fe);
        setServerError(e.problem.errors.map((f) => f.message).join(" "));
      } else setServerError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  const itemLevel = p.type === "item_percent" || p.type === "item_fixed" || p.type === "buy_x_get_y" || p.type === "tiered";
  const targetAll = Object.values(p.target).every((v) => !v || v.length === 0);
  const [scoped, setScoped] = useState(!targetAll);
  const pv = preview.data;

  return (
    <Page
      title={isNew ? "Nueva promoción" : p.name || "Promoción"}
      sub={
        <span className="flex flex-wrap items-center gap-2">
          <a className="inline-flex items-center gap-1 text-primary hover:underline" href={href("promociones")}>
            <ArrowLeft className="size-3.5" /> Promociones
          </a>
          {saved && (
            <>
              · <StatusBadge status={dirty ? (pv?.displayStatus ?? saved.displayStatus) : saved.displayStatus} />
              <span className="font-mono text-xs">{saved.id}</span>
            </>
          )}
          {dirty && <Badge variant="warn">Cambios sin guardar</Badge>}
        </span>
      }
      actions={
        canEdit && (
          <>
            <Select value={p.status} onValueChange={(v) => set({ status: v as Promotion["status"] })}>
              <SelectTrigger className="w-36" aria-label="Estado de la promoción">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="draft">Borrador</SelectItem>
                <SelectItem value="active">Activa</SelectItem>
                <SelectItem value="paused">Pausada</SelectItem>
                {p.status === "archived" && <SelectItem value="archived">Archivada</SelectItem>}
              </SelectContent>
            </Select>
            <Button disabled={busy || (!dirty && !isNew)} onClick={() => void save()}>
              {busy && <Loader2 className="animate-spin" />}
              {isNew ? (p.status === "active" ? "Crear y activar" : "Crear promoción") : "Guardar"}
            </Button>
          </>
        )
      }
    >
      {!canEdit && <Notice tone="warn">Tu acceso es de solo lectura: puedes revisar la promoción y probarla, pero no guardar cambios.</Notice>}
      {serverError && <Notice tone="bad">{serverError}</Notice>}
      <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_22rem]">
        <fieldset disabled={!canEdit} className="grid min-w-0 gap-4">
          <Section title="Tipo de promoción" description={isNew ? "Elige qué beneficio da. Después defines a qué productos y con qué condiciones." : TYPE_HELP[p.type]}>
            {isNew ? (
              <Choice
                label="Tipo de promoción"
                value={p.type}
                onChange={(t) => {
                  const fresh = emptyPromotion(t);
                  set({ ...fresh, id: p.id, name: p.name, description: p.description, status: p.status, campaignId: p.campaignId, target: p.target, conditions: p.conditions, stacking: p.stacking, maxDiscount: p.maxDiscount });
                }}
                options={TYPES.map((t) => ({ value: t, label: TYPE_LABEL[t], help: TYPE_HELP[t] }))}
              />
            ) : (
              <p className="text-sm">
                <Badge variant="secondary">{TYPE_LABEL[p.type]}</Badge>{" "}
                <span className="text-muted-foreground">El tipo no se cambia después de crear; duplica la promoción si necesitas otro.</span>
              </p>
            )}
          </Section>

          <Section title="Datos">
            <FormField id="promo-name" label="Nombre" required error={errors.name} description="Lo ve el comprador en su carrito. Ejemplo: Buen Fin: 10% en tenis.">
              <Input value={p.name} maxLength={120} onChange={(e) => set({ name: e.target.value })} />
            </FormField>
            <FormField id="promo-desc" label="Nota interna (opcional)">
              <Textarea rows={2} maxLength={500} value={p.description ?? ""} onChange={(e) => set({ description: e.target.value })} />
            </FormField>
            <div className="grid gap-4 sm:grid-cols-2">
              {isNew && (
                <FormField id="promo-id" label="Identificador (opcional)" error={errors.id} description="Si lo dejas vacío se crea a partir del nombre.">
                  <Input value={p.id} placeholder="buen-fin-10" className="font-mono" onChange={(e) => set({ id: e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, "-") })} />
                </FormField>
              )}
              <Field label="Campaña" hint="Agrupa promociones con fechas y presupuesto comunes." error={errors.campaignId}>
                <Select value={p.campaignId || NONE} onValueChange={(v) => set({ campaignId: v === NONE ? "" : v })}>
                  <SelectTrigger aria-label="Campaña">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={NONE}>Sin campaña</SelectItem>
                    {campaigns.data?.map((c) => (
                      <SelectItem key={c.id} value={c.id}>
                        {c.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            </div>
          </Section>

          <Section title="Beneficio">
            <Benefit p={p} set={set} errors={errors} brands={facets.data?.brands.map((b) => b.value)} categories={facets.data?.categories.map((c) => c.value)} />
          </Section>

          {p.type !== "bundle" && (
            <Section
              title={itemLevel ? "Productos con descuento" : "Productos que deben estar en el carrito"}
              description={
                itemLevel
                  ? "El descuento y los mínimos se calculan solo sobre estos productos."
                  : "Los mínimos de compra se calculan sobre estos productos; el beneficio se aplica como indica el tipo."
              }
            >
              <Choice
                label="Alcance"
                columns="sm:grid-cols-2"
                value={scoped ? "some" : "all"}
                onChange={(v) => {
                  setScoped(v === "some");
                  if (v === "all") set({ target: {} });
                }}
                options={[
                  { value: "all", label: "Todo el catálogo" },
                  { value: "some", label: "Productos específicos", help: "Por marca, categoría, producto o variante." },
                ]}
              />
              {scoped && (
                <TargetFields
                  target={p.target}
                  onChange={(t) => set({ target: t })}
                  brands={facets.data?.brands.map((b) => b.value)}
                  categories={facets.data?.categories.map((c) => c.value)}
                  collections={facets.data?.collections.map((c) => c.value)}
                />
              )}
            </Section>
          )}

          <Section title="Condiciones" description="Todas son opcionales. El carrito explica al comprador cuál le falta.">
            <div className="grid gap-4 sm:grid-cols-2">
              {p.type !== "bundle" && (
                <>
                  <FormField id="promo-min" label="Compra mínima" description={scoped ? "En los productos participantes." : "En todo el carrito."} error={errors.minSubtotal}>
                    <NumField kind="money" value={p.conditions.minSubtotal} onChange={(v) => setCond({ minSubtotal: v })} />
                  </FormField>
                  <FormField id="promo-minqty" label="Piezas mínimas" error={errors.minQuantity}>
                    <NumField kind="int" value={p.conditions.minQuantity} onChange={(v) => setCond({ minQuantity: v })} placeholder="Sin mínimo" />
                  </FormField>
                </>
              )}
              <FormField id="promo-channels" label="Canales de venta" description="Vacío = todos los canales.">
                <ChipInput values={p.conditions.channels ?? []} onChange={(v) => setCond({ channels: v })} suggestions={facets.data?.channels} placeholder="mx" />
              </FormField>
              <FormField id="promo-domains" label="Solo para correos de estos dominios" description="Por ejemplo empresa.com para ventas a empleados." error={errors.emailDomains}>
                <ChipInput
                  values={p.conditions.emailDomains ?? []}
                  onChange={(v) => setCond({ emailDomains: v })}
                  placeholder="empresa.com"
                  transform={(s) => s.toLowerCase().replace(/^@/, "")}
                />
              </FormField>
            </div>
            <label className="flex items-start gap-2.5 text-sm">
              <Checkbox className="mt-0.5" checked={Boolean(p.conditions.requiresCoupon)} onCheckedChange={(c) => setCond({ requiresCoupon: c === true })} />
              <span>
                <span className="font-medium">Requiere un cupón</span>
                <span className="block text-xs text-muted-foreground">
                  Solo aplica cuando el comprador escribe un código válido.{" "}
                  {saved ? (
                    <a className="text-primary underline" href={href("promociones", "cupones", saved.id)}>
                      {saved.coupons === 0 ? "Crear cupones" : `Ver sus ${saved.coupons} cupones`}
                    </a>
                  ) : (
                    "Podrás crear los códigos después de guardar."
                  )}
                </span>
              </span>
            </label>
            <label className="flex items-start gap-2.5 text-sm">
              <Checkbox
                className="mt-0.5"
                checked={(p.conditions.paymentMethods ?? []).includes("card")}
                onCheckedChange={(c) => setCond({ paymentMethods: c === true ? ["card"] : [] })}
              />
              <span>
                <span className="font-medium">Solo pagando con tarjeta</span>
                <span className="block text-xs text-muted-foreground">Se confirma al elegir el método de pago. Más métodos llegarán con el módulo de pagos.</span>
              </span>
            </label>
            <FormField id="promo-segments" label="Segmentos de clientes" description="Reservado: aplicará cuando existan grupos de clientes. Hoy ninguna compra pertenece a un segmento.">
              <ChipInput values={p.conditions.segments ?? []} onChange={(v) => setCond({ segments: v })} placeholder="vip" transform={(s) => s.toLowerCase()} />
            </FormField>
          </Section>

          <Section title="Vigencia" description={`Hora del centro de México (${STORE_TZ}). Sin fechas, aplica mientras esté activa.`}>
            <div className="grid gap-4 sm:grid-cols-2">
              <FormField id="promo-start" label="Empieza">
                <DateTimeField value={p.conditions.startsAt} onChange={(v) => setCond({ startsAt: v })} />
              </FormField>
              <FormField id="promo-end" label="Termina" error={errors.endsAt}>
                <DateTimeField value={p.conditions.endsAt} onChange={(v) => setCond({ endsAt: v })} />
              </FormField>
            </div>
          </Section>

          <Section title="Límites" description="Los usos se cuentan por pedido pagado y se devuelven si el pedido se cancela o reembolsa.">
            <div className="grid gap-4 sm:grid-cols-3">
              <FormField id="promo-max" label="Usos en total" error={errors.maxUses}>
                <NumField kind="int" value={p.conditions.maxUses} onChange={(v) => setCond({ maxUses: v })} placeholder="Sin límite" />
              </FormField>
              <FormField id="promo-maxc" label="Usos por cliente" description="Por correo del comprador." error={errors.maxUsesPerCustomer}>
                <NumField kind="int" value={p.conditions.maxUsesPerCustomer} onChange={(v) => setCond({ maxUsesPerCustomer: v })} placeholder="Sin límite" />
              </FormField>
              <FormField id="promo-cap" label="Tope de descuento por pedido" error={errors.maxDiscount}>
                <NumField kind="money" value={p.maxDiscount} onChange={(v) => set({ maxDiscount: v })} placeholder="Sin tope" />
              </FormField>
            </div>
          </Section>

          <Section title="Combinación con otras promociones" description="Con mayor prioridad se evalúa primero. Cuando dos no se combinan, gana la de mayor prioridad y la otra explica por qué no aplicó.">
            <Choice
              label="Cómo se combina"
              value={p.stacking.mode}
              onChange={(mode) => setStack({ mode })}
              options={[
                { value: "stack", label: "Se combina", help: "Con cualquier promoción que no sea exclusiva." },
                { value: "exclusive", label: "Exclusiva", help: "No se suma con ninguna otra." },
                { value: "group", label: "Solo con su grupo", help: "Se suma únicamente con promociones del mismo grupo." },
              ]}
            />
            <div className="grid gap-4 sm:grid-cols-2">
              {p.stacking.mode === "group" && (
                <FormField id="promo-group" label="Grupo" error={errors.group} description="Mismo nombre en las promociones que se combinan.">
                  <Input value={p.stacking.group ?? ""} maxLength={40} placeholder="buen-fin" onChange={(e) => setStack({ group: e.target.value })} />
                </FormField>
              )}
              <FormField id="promo-priority" label="Prioridad" description="De -1000 a 1000. Mayor número = primero." error={errors.priority}>
                <Input
                  type="number"
                  min={-1000}
                  max={1000}
                  value={p.stacking.priority}
                  onChange={(e) => setStack({ priority: Math.max(-1000, Math.min(1000, Math.trunc(Number(e.target.value) || 0))) })}
                />
              </FormField>
            </div>
          </Section>
        </fieldset>

        <aside className="grid gap-4 lg:sticky lg:top-4" aria-label="Resumen y vista previa">
          <Card>
            <CardHeader>
              <CardTitle>En pocas palabras</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-3">
              {pv?.summary ? (
                <p className="text-[15px] font-medium leading-snug" data-testid="promo-summary">
                  {pv.summary}
                </p>
              ) : (
                <p className="text-sm text-muted-foreground">{preview.isPending ? "Calculando…" : "Completa el beneficio para ver el resumen."}</p>
              )}
              {pv && pv.errors.length > 0 && (
                <ul className="grid gap-1 text-xs text-warn">
                  {pv.errors.slice(0, 4).map((e) => (
                    <li key={e.field + e.message}>• {e.message}</li>
                  ))}
                </ul>
              )}
              {pv?.displayStatus === "programada" && (
                <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
                  <Clock className="size-3.5" /> Empezará sola el {storeDateTime(p.conditions.startsAt)}.
                </p>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Vista previa en un carrito</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-3">
              <CartEditor compact items={cart} onChange={setCart} />
              <FormField id="preview-zip" label="Código postal (para el envío)">
                <Input inputMode="numeric" maxLength={5} placeholder="53129" value={zip} onChange={(e) => setZip(e.target.value.replace(/\D/g, ""))} />
              </FormField>
              {pv?.result ? (
                <div
                  className={`grid gap-1 rounded-md px-3 py-2.5 text-sm ${pv.result.applied ? "bg-ok-soft text-ok" : "bg-muted text-foreground"}`}
                  role="status"
                  data-testid="promo-preview-result"
                >
                  <span className="flex items-center gap-1.5 font-semibold">
                    {pv.result.applied ? <CheckCircle2 className="size-4" /> : <CircleSlash className="size-4" />}
                    {pv.result.applied
                      ? `Aplica${pv.result.discount.amount + (pv.result.shippingDiscount?.amount ?? 0) > 0 ? `: −${formatMoney(pv.result.discount.amount + (pv.result.shippingDiscount?.amount ?? 0))}` : ""}`
                      : "No aplica en este carrito"}
                  </span>
                  <span>{pv.result.reason}</span>
                  {pv.result.hint && <span className="text-xs opacity-80">{pv.result.hint}</span>}
                </div>
              ) : (
                cart.length > 0 && pv && pv.errors.length > 0 && <p className="text-sm text-muted-foreground">Corrige los datos pendientes para probarla.</p>
              )}
              {pv?.simulation && <SimulationTotals sim={pv.simulation} showLines={false} />}
              {pv?.simulation && pv.simulation.promotions.filter((r) => r.applied && r.id !== pv.result?.id).length > 0 && (
                <p className="text-xs text-muted-foreground">
                  También aplican:{" "}
                  {pv.simulation.promotions
                    .filter((r) => r.applied && r.id !== pv.result?.id)
                    .map((r) => r.name)
                    .join(", ")}
                  .
                </p>
              )}
              <p className="text-xs text-muted-foreground">
                La vista previa la evalúa como si estuviera activa{p.conditions.requiresCoupon ? " y con el cupón escrito" : ""}, junto con las demás promociones de la tienda.
              </p>
            </CardContent>
          </Card>

          {saved && (
            <Card>
              <CardHeader>
                <CardTitle>Uso</CardTitle>
              </CardHeader>
              <CardContent className="grid gap-3 text-sm">
                <dl className="grid grid-cols-2 gap-3">
                  <div>
                    <dt className="text-xs text-muted-foreground">Pedidos</dt>
                    <dd className="text-xl font-semibold tabular-nums">
                      {saved.uses}
                      {saved.conditions.maxUses ? <span className="text-sm font-normal text-muted-foreground"> / {saved.conditions.maxUses}</span> : null}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted-foreground">Descuento otorgado</dt>
                    <dd className="text-xl font-semibold tabular-nums">{formatMoney(saved.discountTotal, saved.currency)}</dd>
                  </div>
                </dl>
                {saved.conditions.requiresCoupon && (
                  <Button asChild variant="outline" size="sm">
                    <a href={href("promociones", "cupones", saved.id)}>
                      <Ticket /> {saved.coupons === 0 ? "Crear cupones" : `Administrar ${saved.coupons} cupones`}
                    </a>
                  </Button>
                )}
                {redemptions.length === 0 ? (
                  <p className="text-muted-foreground">Todavía no se usa en ningún pedido.</p>
                ) : (
                  <ul className="divide-y rounded-md border">
                    {redemptions.slice(0, 8).map((r) => (
                      <li key={r.orderId} className="flex items-center justify-between gap-2 px-3 py-2">
                        <span>
                          {can(me, "orders", "read") ? (
                            <a className="font-medium text-primary hover:underline" href={href("pedidos", r.orderId)}>
                              #{r.orderNumber || r.orderId}
                            </a>
                          ) : (
                            <span className="font-medium">#{r.orderNumber || r.orderId}</span>
                          )}
                          <span className="block text-xs text-muted-foreground">
                            {storeDateTime(r.createdAt)}
                            {r.coupon ? ` · ${r.coupon}` : ""}
                          </span>
                        </span>
                        <span className="text-right">
                          <span className="block tabular-nums">−{formatMoney(r.amount, r.currency)}</span>
                          <Badge variant={r.status === "redeemed" ? "ok" : r.status === "reserved" ? "warn" : "secondary"}>
                            {r.status === "redeemed" ? "Usada" : r.status === "reserved" ? "Pago pendiente" : "Devuelta"}
                          </Badge>
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
                {canEdit && (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      void promoApi
                        .duplicate(saved.id)
                        .then((c) => {
                          toast.success("Copia creada como borrador");
                          navigate("promociones", "p", c.id);
                        })
                        .catch((e: unknown) => toast.error(errorMessage(e)))
                    }
                  >
                    <Copy /> Duplicar promoción
                  </Button>
                )}
              </CardContent>
            </Card>
          )}
        </aside>
      </div>
    </Page>
  );
}

function Benefit({
  p,
  set,
  errors,
  brands,
  categories,
}: {
  p: Promotion;
  set: (patch: Partial<Promotion>) => void;
  errors: Record<string, string>;
  brands?: string[];
  categories?: string[];
}) {
  const [pickGift, setPickGift] = useState(false);
  const [pickBundle, setPickBundle] = useState(false);
  const titles = useTitles([], [...(p.giftSku ? [p.giftSku] : []), ...(p.bundleItems ?? []).map((b) => b.sku ?? "").filter(Boolean)]);
  switch (p.type) {
    case "order_percent":
    case "item_percent":
      return (
        <FormField id="promo-bps" label="Porcentaje de descuento" required error={errors.bps} className="max-w-xs">
          <NumField kind="percent" value={p.bps} onChange={(v) => set({ bps: v })} />
        </FormField>
      );
    case "order_fixed":
    case "item_fixed":
      return (
        <FormField
          id="promo-amount"
          label={p.type === "order_fixed" ? "Monto de descuento en el pedido" : "Monto de descuento por pieza"}
          required
          error={errors.amount}
          className="max-w-xs"
          description="El descuento nunca supera el valor de los productos."
        >
          <NumField kind="money" value={p.amount} onChange={(v) => set({ amount: v })} />
        </FormField>
      );
    case "free_shipping":
      return <p className="text-sm text-muted-foreground">El envío se descuenta completo. Puedes limitarlo con el tope de descuento por pedido en “Límites”.</p>;
    case "buy_x_get_y": {
      const other = p.getTarget !== undefined && p.getTarget !== null;
      const free = (p.getBps ?? 10000) === 10000;
      return (
        <>
          <div className="grid gap-4 sm:grid-cols-3">
            <FormField id="promo-buy" label="Compra (piezas)" required error={errors.buyQty}>
              <NumField kind="int" value={p.buyQty} onChange={(v) => set({ buyQty: v })} />
            </FormField>
            <FormField id="promo-get" label="Lleva con beneficio (piezas)" required error={errors.getQty}>
              <NumField kind="int" value={p.getQty} onChange={(v) => set({ getQty: v })} />
            </FormField>
            <FormField id="promo-getbps" label="Descuento en esas piezas" required error={errors.getBps} description="100% = gratis.">
              <NumField kind="percent" value={p.getBps} onChange={(v) => set({ getBps: v })} />
            </FormField>
          </div>
          {!other && free && p.buyQty && p.getQty ? (
            <p className="text-sm text-muted-foreground">
              Es un <strong>{p.buyQty + p.getQty}x{p.buyQty}</strong>: de cada {p.buyQty + p.getQty} piezas, las {p.getQty === 1 ? "1 de menor precio va" : `${p.getQty} de menor precio van`} gratis.
            </p>
          ) : null}
          <Choice
            label="Qué productos llevan el beneficio"
            columns="sm:grid-cols-2"
            value={other ? "other" : "same"}
            onChange={(v) => set({ getTarget: v === "other" ? {} : null })}
            options={[
              { value: "same", label: "Los mismos productos", help: "3x2, 2x1: la pieza de menor precio lleva el beneficio." },
              { value: "other", label: "Otros productos", help: "Compra tenis y lleva calcetas a mitad de precio." },
            ]}
          />
          {other && (
            <Field label="Productos que llevan el beneficio">
              <TargetFields target={p.getTarget ?? {}} onChange={(t) => set({ getTarget: t })} brands={brands} categories={categories} />
            </Field>
          )}
        </>
      );
    }
    case "tiered": {
      const tiers = p.tiers ?? [];
      const by = p.tierBy ?? "subtotal";
      const setTier = (i: number, patch: Partial<Tier>) => set({ tiers: tiers.map((t, k) => (k === i ? { ...t, ...patch } : t)) });
      return (
        <>
          <Choice
            label="Los escalones son por"
            columns="sm:grid-cols-2"
            value={by}
            onChange={(v) => set({ tierBy: v, tiers: tiers.map((t) => ({ ...t, min: 0 })) })}
            options={[
              { value: "subtotal", label: "Monto de compra" },
              { value: "quantity", label: "Número de piezas" },
            ]}
          />
          <Field label="Escalones" error={errors.tiers} hint="Aplica el escalón más alto que se alcance. El carrito avisa cuánto falta para el siguiente.">
            <div className="grid gap-2">
              {tiers.map((t, i) => {
                const pct = !t.amount;
                return (
                  <div key={i} className="grid grid-cols-[1fr_1fr_auto] items-end gap-2 sm:grid-cols-[1fr_8rem_1fr_auto]">
                    <label className="grid gap-1 text-xs text-muted-foreground">
                      {by === "subtotal" ? "Desde (monto)" : "Desde (piezas)"}
                      <NumField kind={by === "subtotal" ? "money" : "int"} value={t.min} onChange={(v) => setTier(i, { min: v })} aria-label={`Mínimo del escalón ${i + 1}`} />
                    </label>
                    <label className="hidden gap-1 text-xs text-muted-foreground sm:grid">
                      Descuento en
                      <Select value={pct ? "bps" : "amount"} onValueChange={(v) => setTier(i, v === "bps" ? { bps: 0, amount: undefined } : { amount: 100, bps: undefined })}>
                        <SelectTrigger aria-label={`Tipo de descuento del escalón ${i + 1}`}>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="bps">Porcentaje</SelectItem>
                          <SelectItem value="amount">Monto</SelectItem>
                        </SelectContent>
                      </Select>
                    </label>
                    <label className="grid gap-1 text-xs text-muted-foreground">
                      Descuento
                      {pct ? (
                        <NumField kind="percent" value={t.bps} onChange={(v) => setTier(i, { bps: v, amount: undefined })} aria-label={`Descuento del escalón ${i + 1}`} />
                      ) : (
                        <NumField kind="money" value={t.amount} onChange={(v) => setTier(i, { amount: v, bps: undefined })} aria-label={`Descuento del escalón ${i + 1}`} />
                      )}
                    </label>
                    <Button type="button" variant="ghost" size="icon" aria-label={`Quitar escalón ${i + 1}`} disabled={tiers.length === 1} onClick={() => set({ tiers: tiers.filter((_, k) => k !== i) })}>
                      <Trash2 />
                    </Button>
                  </div>
                );
              })}
              <div>
                <Button type="button" variant="outline" size="sm" disabled={tiers.length >= 10} onClick={() => set({ tiers: [...tiers, { min: 0, bps: 0 }] })}>
                  <Plus /> Agregar escalón
                </Button>
              </div>
            </div>
          </Field>
        </>
      );
    }
    case "gift":
      return (
        <>
          <Field label="Producto de regalo" error={errors.giftSku} hint="Se agrega solo al carrito, sin costo, si hay existencias. Si el comprador ya lo tenía en el carrito, esa pieza queda gratis.">
            {p.giftSku ? (
              <PickedList items={[{ id: p.giftSku, title: titles.variants[p.giftSku]?.title }]} onRemove={() => set({ giftSku: "" })} />
            ) : null}
            <div>
              <Button type="button" variant="outline" size="sm" onClick={() => setPickGift(true)}>
                {p.giftSku ? "Cambiar regalo" : "Elegir regalo"}
              </Button>
            </div>
          </Field>
          <FormField id="promo-giftqty" label="Piezas de regalo" className="max-w-40">
            <NumField kind="int" value={p.giftQty} onChange={(v) => set({ giftQty: v || 1 })} />
          </FormField>
          <ProductPicker open={pickGift} onOpenChange={setPickGift} mode="sku" single title="Elegir el regalo" onPick={(xs) => xs[0] && set({ giftSku: xs[0].id })} />
        </>
      );
    case "bundle": {
      const items = p.bundleItems ?? [];
      const regular = items.reduce((a, b) => a + (titles.variants[b.sku ?? ""]?.price ?? 0) * b.qty, 0);
      return (
        <>
          <Field label="Productos del paquete" error={errors.bundleItems} hint="El precio especial aplica cada vez que el carrito tiene el conjunto completo.">
            {items.length > 0 && (
              <ul className="divide-y rounded-md border">
                {items.map((b, i) => {
                  const key = b.sku ?? b.productId ?? String(i);
                  return (
                    <li key={key} className="flex items-center gap-3 px-3 py-2 text-sm">
                      <span className="min-w-0 flex-1">
                        <span className="block truncate font-medium">{titles.variants[b.sku ?? ""]?.title ?? key}</span>
                        <span className="block font-mono text-xs text-muted-foreground">{key}</span>
                      </span>
                      <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
                        Piezas
                        <NumField kind="int" className="w-16" value={b.qty} onChange={(v) => set({ bundleItems: items.map((x, k) => (k === i ? { ...x, qty: v || 1 } : x)) })} aria-label={`Piezas de ${key}`} />
                      </label>
                      <Button type="button" variant="ghost" size="icon" className="size-7" aria-label={`Quitar ${key}`} onClick={() => set({ bundleItems: items.filter((_, k) => k !== i) })}>
                        <Trash2 />
                      </Button>
                    </li>
                  );
                })}
              </ul>
            )}
            <div>
              <Button type="button" variant="outline" size="sm" onClick={() => setPickBundle(true)}>
                <Plus /> Agregar producto al paquete
              </Button>
            </div>
          </Field>
          <FormField
            id="promo-bundle-price"
            label="Precio del paquete"
            required
            error={errors.bundlePrice}
            className="max-w-xs"
            description={regular > 0 ? `Por separado cuestan ${formatMoney(regular)}${p.bundlePrice && p.bundlePrice < regular ? `; el comprador ahorra ${formatMoney(regular - p.bundlePrice)}` : ""}.` : undefined}
          >
            <NumField kind="money" value={p.bundlePrice} onChange={(v) => set({ bundlePrice: v })} />
          </FormField>
          <ProductPicker
            open={pickBundle}
            onOpenChange={setPickBundle}
            mode="sku"
            title="Agregar al paquete"
            exclude={items.map((b) => b.sku ?? "")}
            onPick={(xs) => set({ bundleItems: [...items, ...xs.map((x) => ({ sku: x.id, qty: 1 }))] })}
          />
        </>
      );
    }
  }
}

function TargetFields({
  target,
  onChange,
  brands,
  categories,
  collections,
}: {
  target: Target;
  onChange: (t: Target) => void;
  brands?: string[];
  categories?: string[];
  collections?: string[];
}) {
  const [pick, setPick] = useState<"product" | "sku" | "exclude" | null>(null);
  const titles = useTitles(target.productIds ?? [], [...(target.skus ?? []), ...(target.excludeSkus ?? [])]);
  const set = (patch: Partial<Target>) => onChange({ ...target, ...patch });
  const products = (target.productIds ?? []).map((id) => ({ id, title: titles.products[id]?.title }));
  const skus = (target.skus ?? []).map((id) => ({ id, title: titles.variants[id]?.title }));
  const excluded = (target.excludeSkus ?? []).map((id) => ({ id, title: titles.variants[id]?.title }));
  return (
    <div className="grid gap-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <FormField id="target-brands" label="Marcas" description="Escribe y presiona Enter.">
          <ChipInput values={target.brands ?? []} onChange={(v) => set({ brands: v })} suggestions={brands} placeholder="Norte" />
        </FormField>
        <FormField id="target-cats" label="Categorías">
          <ChipInput values={target.categories ?? []} onChange={(v) => set({ categories: v })} suggestions={categories} placeholder="deportes" />
        </FormField>
      </div>
      {(collections?.length ?? 0) > 0 || (target.collections?.length ?? 0) > 0 ? (
        <FormField id="target-cols" label="Colecciones">
          <ChipInput values={target.collections ?? []} onChange={(v) => set({ collections: v })} suggestions={collections} />
        </FormField>
      ) : null}
      <Field label="Productos y variantes" hint="Si eliges marcas o categorías además de productos, deben cumplirse ambas condiciones.">
        <PickedList items={[...products, ...skus]} onRemove={(id) => set({ productIds: (target.productIds ?? []).filter((x) => x !== id), skus: (target.skus ?? []).filter((x) => x !== id) })} />
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" size="sm" onClick={() => setPick("product")}>
            <Plus /> Productos completos
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={() => setPick("sku")}>
            <Plus /> Variantes (SKU)
          </Button>
          <Button type="button" variant="ghost" size="sm" onClick={() => setPick("exclude")}>
            Excluir variantes
          </Button>
        </div>
      </Field>
      {excluded.length > 0 && (
        <Field label="Variantes excluidas">
          <PickedList items={excluded} onRemove={(id) => set({ excludeSkus: (target.excludeSkus ?? []).filter((x) => x !== id) })} />
        </Field>
      )}
      <ProductPicker
        open={pick !== null}
        onOpenChange={(o) => !o && setPick(null)}
        mode={pick === "product" ? "product" : "sku"}
        title={pick === "product" ? "Agregar productos" : pick === "exclude" ? "Excluir variantes" : "Agregar variantes"}
        exclude={pick === "product" ? target.productIds : pick === "exclude" ? target.excludeSkus : target.skus}
        onPick={(xs) => {
          const ids = xs.map((x) => x.id);
          if (pick === "product") set({ productIds: [...(target.productIds ?? []), ...ids] });
          else if (pick === "exclude") set({ excludeSkus: [...(target.excludeSkus ?? []), ...ids] });
          else set({ skus: [...(target.skus ?? []), ...ids] });
        }}
      />
    </div>
  );
}
