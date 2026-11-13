import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, CheckCircle2, CircleAlert, Database, FileSearch, Gauge, RefreshCw, ScrollText, ShieldCheck, TriangleAlert, Wrench, X } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { api, errorMessage, request, type Me } from "@/api";
import { AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { toast } from "@/components/ui/sonner";
import { cn } from "@/lib/utils";
import { can } from "@/perms";
import { dateTime, ErrorBox, Notice, Page } from "../common";
import { reportsApi, type AuditEntry, type AuditQuery, type SystemStatus, type WorkerView } from "./api";
import { Skeleton, useNow } from "./charts";
import { Empty, ExportButton } from "./controls";
import { ago, duration, num } from "./format";
import { SubNav } from "./Reports";

const OPS = [
  { id: "actividad", label: "Actividad", icon: ScrollText },
  { id: "estado", label: "Estado del sistema", icon: Activity },
] as const;

// ---------- activity ----------

const ACTION: Record<string, string> = {
  "order.created": "Pedido creado",
  "order.fulfilled": "Pedido enviado",
  "order.cancelled": "Pedido cancelado",
  "order.refunded": "Pedido reembolsado",
  "payment.captured": "Pago cobrado",
  "payment.failed": "Pago rechazado",
  "product.upsert": "Producto guardado",
  "stock.set": "Existencias ajustadas",
  "prices.set": "Precios cambiados",
  "channel.set": "Canal configurado",
  "promotion.set": "Promoción guardada",
  "member.created": "Miembro invitado",
  "member.updated": "Permisos de miembro cambiados",
  "member.password": "Contraseña cambiada",
  "session.revoke": "Sesión cerrada en un dispositivo",
  "session.revoke_others": "Sesiones cerradas en los demás dispositivos",
  "site.publish": "Tienda publicada",
  "site.rollback": "Publicación deshecha",
  "webhook.created": "Conector creado",
  "webhook.disabled": "Conector desactivado",
  "analytics.rebuilt": "Analítica reconstruida",
  "attribute.save": "Atributo guardado",
  "brand.save": "Marca guardada",
  "category.save": "Categoría guardada",
  "collection.save": "Colección guardada",
  "product.save": "Producto guardado",
  "media.upload": "Imagen subida",
  "price.set": "Precio cambiado",
  "coupon.create": "Cupón creado",
  "customer.subscribed": "Suscripción al boletín",
  "fulfillment.shipped": "Envío registrado",
  "fulfillment.delivered": "Envío entregado",
  "invoice.issued": "Factura emitida",
  "member.create": "Miembro invitado",
  "member.update": "Permisos de miembro cambiados",
  "order.note_added": "Nota agregada al pedido",
  "order.view_saved": "Vista de pedidos guardada",
  "order.edited": "Pedido editado",
  "refund.succeeded": "Reembolso completado",
  "refund.failed": "Reembolso fallido",
  "return.received": "Devolución recibida",
  "return.requested": "Devolución solicitada",
  "settings.carriers.set": "Paquetería guardada",
  "settings.domain.create": "Dominio agregado",
  "settings.domain.verify": "Dominio verificado",
  "settings.domain.delete": "Dominio eliminado",
  "settings.holidays.set": "Día festivo guardado",
  "settings.locations.set": "Ubicación guardada",
  "settings.menus.set": "Menú guardado",
  "settings.options.set": "Opción de entrega guardada",
  "settings.policy.set": "Política guardada",
  "settings.profiles.set": "Perfil de envío guardado",
  "settings.redirects.set": "Redirección guardada",
  "settings.store.set": "Datos de la tienda guardados",
  "settings.tax.set": "Región de impuestos guardada",
  "settings.zones.set": "Zona de envío guardada",
  "site.revision": "Borrador de la tienda guardado",
  "webhook.disable": "Conector desactivado",
  "webhook.replay": "Evento reenviado",
  "webhook.subscribe": "Conector creado",
  "analytics.demo_seeded": "Historial de demostración cargado",
  "company.autopay_charged": "Pago automático de empresa cobrado",
  "company.autopay_failed": "Pago automático de empresa rechazado",
  "customer.card_removed": "Tarjeta guardada eliminada",
  "customer.card_saved": "Tarjeta guardada",
  "customer.punchout_session": "Sesión punchout iniciada",
  "member.recovery_code_used": "Código de recuperación usado",
  "order.blocked": "Pedido bloqueado por riesgo",
  "product.publish_scheduled": "Producto publicado según lo programado",
  "purchase.approval_requested": "Aprobación de compra solicitada",
  "purchase.approved": "Compra aprobada",
  "purchase.cancelled": "Solicitud de compra cancelada",
  "purchase.expired": "Solicitud de compra vencida",
  "purchase.level_approved": "Nivel de aprobación superado",
  "purchase.rejected": "Compra rechazada",
  "quote.requested": "Cotización solicitada",
  "subscription.created": "Suscripción creada",
  "subscription.renewal_failed": "Renovación de suscripción fallida",
  "ai.suggestion.applied": "Sugerencia de IA aplicada",
  "ai.suggestion.rejected": "Sugerencia de IA rechazada",
  "ai.suggestion.undone": "Sugerencia de IA deshecha",
  "app.extensions": "Extensiones de app cambiadas",
  "app.installed": "App instalada",
  "app.permissions": "Permisos de app cambiados",
  "app.revoked": "App desinstalada",
  "attribute.delete": "Atributo eliminado",
  "automation.deleted": "Automatización eliminada",
  "automation.saved": "Automatización guardada",
  "blog.deleted": "Entrada de blog eliminada",
  "blog.saved": "Entrada de blog guardada",
  "brand.delete": "Marca eliminada",
  "bulk.prices": "Precios cambiados en masa",
  "bundle.set": "Paquete guardado",
  "campaign.delete": "Campaña eliminada",
  "campaign.scheduled": "Campaña programada",
  "campaign.set": "Campaña guardada",
  "cart.dismissed": "Carrito abandonado descartado",
  "cart.reminded": "Recordatorio de carrito enviado",
  "category.delete": "Categoría eliminada",
  "category.reorder": "Categorías reordenadas",
  "category.repair": "Productos sin categoría válida reparados",
  "channel.connect": "Canal de venta conectado",
  "channel.delete": "Canal de venta eliminado",
  "channel.disconnect": "Canal de venta desconectado",
  "channel.list": "Productos publicados en un canal",
  "channel.settings": "Ajustes del canal cambiados",
  "channel.unlist": "Productos retirados de un canal",
  "company.act_as": "Compra en nombre de una empresa",
  "company.autopay_off": "Pago automático desactivado",
  "company.autopay_on": "Pago automático activado",
  "company.file_delete": "Archivo de empresa eliminado",
  "company.file_upload": "Archivo de empresa subido",
  "company.invoices_paid": "Facturas de empresa pagadas",
  "company.locations": "Sucursales de empresa cambiadas",
  "company.members": "Compradores de empresa cambiados",
  "company.punchout": "Punchout de empresa configurado",
  "company.reminder_days": "Recordatorios de pago cambiados",
  "company.rep_access": "Acceso de representante cambiado",
  "company.role": "Rol de empresa guardado",
  "company.role.delete": "Rol de empresa eliminado",
  "company.save": "Empresa guardada",
  "coupon.delete": "Cupón eliminado",
  "coupon.generate": "Cupones generados",
  "coupon.status": "Estado de cupón cambiado",
  "custom_reports.set": "Reporte personalizado guardado",
  "customer.address_deleted": "Dirección de cliente eliminada",
  "customer.address_saved": "Dirección de cliente guardada",
  "customer.bulk_tag": "Clientes etiquetados en masa",
  "customer.created": "Cliente creado",
  "customer.deleted": "Cliente eliminado",
  "customer.erased": "Datos de cliente borrados (derecho ARCO)",
  "customer.group_deleted": "Grupo de clientes eliminado",
  "customer.group_members": "Miembros de grupo cambiados",
  "customer.group_saved": "Grupo de clientes guardado",
  "customer.note_added": "Nota de cliente agregada",
  "customer.note_deleted": "Nota de cliente eliminada",
  "customer.password_changed": "Cliente cambió su contraseña",
  "customer.rep_session": "Representante entró como cliente",
  "customer.signed_in": "Cliente inició sesión",
  "customer.updated": "Cliente actualizado",
  "digital.added": "Archivo digital agregado",
  "digital.deleted": "Archivo digital eliminado",
  "digital.updated": "Archivo digital actualizado",
  "domain.market": "Dominio asignado a un mercado",
  "draft.cancelled": "Pedido manual cancelado",
  "draft.completed": "Pedido manual completado",
  "draft.link_sent": "Enlace de pago enviado",
  "draft.saved": "Pedido manual guardado",
  "entity.record_deleted": "Registro eliminado",
  "giftcard.issue": "Tarjeta de regalo emitida",
  "giftcard.update": "Tarjeta de regalo actualizada",
  "import.apply": "Importación aplicada",
  "media.alt": "Texto alternativo cambiado",
  "media.delete": "Imagen eliminada",
  "media.edit": "Imagen editada",
  "member.invitation_revoked": "Invitación cancelada",
  "member.invited": "Miembro invitado",
  "member.joined": "Miembro se unió al equipo",
  "notification.retry": "Notificación reenviada",
  "notification.settings": "Ajustes de notificaciones cambiados",
  "notification.template": "Plantilla de notificación guardada",
  "order.note_deleted": "Nota de pedido eliminada",
  "order.risk_cleared": "Riesgo de pedido revisado",
  "order.view_deleted": "Vista de pedidos eliminada",
  "payments.account.set": "Cuenta de pagos configurada",
  "payments.set": "Métodos de pago cambiados",
  "plan.change": "Plan de la tienda cambiado",
  "pos.sale": "Venta en tienda física",
  "preorder.ended": "Preventa terminada",
  "preorder.set": "Preventa configurada",
  "price.bulk": "Precios cambiados en masa",
  "pricelist.delete": "Lista de precios eliminada",
  "pricelist.set": "Lista de precios guardada",
  "product.bulk.price_percent": "Precios ajustados por porcentaje",
  "product.delete": "Producto eliminado",
  "promotion.delete": "Promoción eliminada",
  "promotion.status": "Estado de promoción cambiado",
  "quote.cancelled": "Cotización cancelada",
  "quote.offered": "Cotización enviada",
  "quote_template.save": "Plantilla de cotización guardada",
  "registry.status": "Estado de mesa de regalos cambiado",
  "review.delete": "Reseña eliminada",
  "review.moderate": "Reseña moderada",
  "sandbox.bring": "Productos traídos al sandbox",
  "sandbox.create": "Sandbox creado",
  "sandbox.delete": "Sandbox eliminado",
  "sandbox.password": "Contraseña del sandbox cambiada",
  "sandbox.preview_link": "Enlace de vista previa del sandbox",
  "sandbox.publish": "Sandbox publicado a producción",
  "sandbox.refresh": "Sandbox actualizado desde producción",
  "sandbox.rename": "Sandbox renombrado",
  "sandbox.revert": "Publicación del sandbox revertida",
  "sandbox.schedule": "Publicación del sandbox programada",
  "sandbox.schedule_cancel": "Publicación programada cancelada",
  "sandbox.schedule_run": "Publicación programada ejecutada",
  "sandbox.send": "Productos enviados a otro sandbox",
  "search.set": "Ajustes de búsqueda cambiados",
  "seller.payout.start": "Pago a vendedor iniciado",
  "seller.products": "Productos de vendedor cambiados",
  "seller.save": "Vendedor guardado",
  "session.switch": "Cambio de tienda en la sesión",
  "settings.asset.delete": "Archivo del tema eliminado",
  "settings.asset.set": "Archivo del tema guardado",
  "settings.domain.primary": "Dominio principal cambiado",
  "settings.holidays.delete": "Día feriado eliminado",
  "settings.policy.delete": "Política eliminada",
  "settings.product_class.set": "Clase de producto guardada",
  "settings.redirects.delete": "Redirección eliminada",
  "stock.threshold.set": "Umbral de existencias cambiado",
  "stock.transfer": "Transferencia de existencias",
  "store_tags.set": "Etiquetas de la tienda guardadas",
  "storefront.create": "Tienda creada",
  "storefront.delete": "Tienda eliminada",
  "storefront.update": "Tienda actualizada",
  "subscriptions.set": "Ajustes de suscripciones cambiados",
  "term.add": "Término agregado",
  "term.delete": "Término eliminado",
};

// Actions built from parts ("settings.<section>.set", "bulk.<kind>", ...) and
// any code without its own name still read in Spanish: "<qué> · <acción>".
const VERB: Record<string, string> = {
  set: "guardado", save: "guardado", saved: "guardado", update: "actualizado", updated: "actualizado", create: "creado", created: "creado",
  delete: "eliminado", deleted: "eliminado", add: "agregado", added: "agregado", status: "estado cambiado", cancel: "cancelado", cancelled: "cancelado",
  pause: "pausado", paused: "pausado", resume: "reanudado", resumed: "reanudado", skip: "entrega saltada", charge: "cobro", credit: "abono", debit: "cargo",
};
const NOUN: Record<string, string> = {
  subscription: "suscripción", seller: "vendedor", company: "empresa", bulk: "edición masiva", fields: "campos", product: "producto", customer: "cliente",
  order: "pedido", category: "categoría", brand: "marca", payout: "pago", payment: "pago", carriers: "paqueterías", zones: "zonas", locations: "ubicaciones",
};
export function actionLabel(a: string): string {
  if (ACTION[a]) return ACTION[a];
  const parts = a.split(".");
  const verb = parts.length > 1 ? VERB[parts[parts.length - 1]!] : undefined;
  const what = (verb ? parts.slice(0, -1) : parts).filter((p) => p !== "settings").map((p) => NOUN[p] ?? p.replace(/_/g, " ")).join(" ");
  const head = (parts[0] === "settings" ? "Configuración " : "") + what;
  const text = verb ? `${head} · ${verb}` : head;
  return text.charAt(0).toUpperCase() + text.slice(1);
}

function actorLabel(e: Pick<AuditEntry, "actor" | "actorName">): { name: string; kind: string } {
  if (e.actorName) return { name: e.actorName, kind: "Miembro" };
  const [kind = "", id = ""] = e.actor.split(":");
  if (kind === "api_key") return { name: id || e.actor, kind: "API key" };
  if (kind === "member") return { name: id, kind: "Miembro (ya no existe)" };
  return { name: e.actor, kind: "Sistema" };
}

const EMPTY: AuditQuery = { actor: "", action: "", target: "", from: "", to: "" };

function Actividad() {
  const [f, setF] = useState<AuditQuery>(EMPTY);
  const [target, setTarget] = useState("");
  const [open, setOpen] = useState<AuditEntry | null>(null);
  const facets = useQuery({ queryKey: ["ops", "audit", "facets"], queryFn: reportsApi.auditFacets, staleTime: 60_000 });
  const q = useInfiniteQuery({
    queryKey: ["ops", "audit", f],
    queryFn: ({ pageParam }) => reportsApi.audit(f, pageParam),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  });
  const verify = useMutation({
    mutationFn: api.auditVerify,
    onSuccess: (r) => (r.intact ? toast.success("El registro está íntegro: nadie lo alteró") : toast.error(`El registro fue alterado desde la entrada ${r.firstBrokenSeq}`)),
    onError: (e) => toast.error(`No se pudo verificar: ${errorMessage(e)}`),
  });
  const items = q.data?.pages.flatMap((p) => p.items) ?? [];
  const filtered = Object.values(f).some(Boolean);
  const dateErr = f.from && f.to && f.to < f.from ? "La fecha final es anterior a la inicial." : null;
  const set = (patch: Partial<AuditQuery>) => setF({ ...f, ...patch });
  function submit(e: FormEvent) {
    e.preventDefault();
    set({ target: target.trim() });
  }

  return (
    <>
      <Card>
        <CardContent className="flex flex-wrap items-center justify-between gap-3 p-4">
          <div className="flex min-w-0 items-start gap-3">
            <span className="grid size-10 shrink-0 place-items-center rounded-full bg-accent text-accent-foreground">
              <ShieldCheck className="size-5" aria-hidden="true" />
            </span>
            <div className="grid gap-0.5 text-sm">
              <strong>Registro a prueba de alteraciones</strong>
              <span className="text-muted-foreground">
                Cada cambio en la tienda queda encadenado al anterior con una firma. {facets.data ? `${num(facets.data.total)} entradas en total.` : ""}
              </span>
              {verify.data && (
                <span className={cn("mt-1 inline-flex items-center gap-1.5 font-medium", verify.data.intact ? "text-ok" : "text-bad")} role="status">
                  {verify.data.intact ? <CheckCircle2 className="size-4" aria-hidden="true" /> : <CircleAlert className="size-4" aria-hidden="true" />}
                  {verify.data.intact ? "Cadena verificada: íntegra" : `Cadena rota desde la entrada ${verify.data.firstBrokenSeq}`}
                </span>
              )}
            </div>
          </div>
          <Button variant="outline" onClick={() => verify.mutate()} disabled={verify.isPending}>
            <ShieldCheck /> {verify.isPending ? "Verificando…" : "Verificar cadena"}
          </Button>
        </CardContent>
      </Card>

      <form className="grid gap-2" onSubmit={submit} aria-label="Filtros de actividad">
        <div className="flex flex-wrap items-end gap-2">
          <Select value={f.actor || "__all"} onValueChange={(v) => set({ actor: v === "__all" ? "" : v })}>
            <SelectTrigger className="w-[13rem]" aria-label="Quién">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__all">Cualquier persona o sistema</SelectItem>
              {facets.data?.actors.map((a) => (
                <SelectItem key={a.value} value={a.value}>
                  {actorLabel({ actor: a.value, actorName: a.label === a.value ? "" : a.label }).name} ({num(a.count)})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select value={f.action || "__all"} onValueChange={(v) => set({ action: v === "__all" ? "" : v })}>
            <SelectTrigger className="w-[14rem]" aria-label="Acción">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__all">Cualquier acción</SelectItem>
              {facets.data?.actions.map((a) => (
                <SelectItem key={a.value} value={a.value}>
                  {actionLabel(a.value)} ({num(a.count)})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="relative">
            <FileSearch className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden="true" />
            <Input className="w-[13rem] pl-8" placeholder="Objeto: pedido, SKU…" aria-label="Objeto afectado" value={target} onChange={(e) => setTarget(e.target.value)} onBlur={() => target.trim() !== (f.target ?? "") && set({ target: target.trim() })} />
          </div>
          <div className="flex items-center gap-2">
            <Input type="date" aria-label="Desde" className="w-[9.5rem]" value={f.from} max={f.to || undefined} onChange={(e) => set({ from: e.target.value })} />
            <span className="text-muted-foreground" aria-hidden="true">
              –
            </span>
            <Input type="date" aria-label="Hasta" className="w-[9.5rem]" value={f.to} min={f.from || undefined} onChange={(e) => set({ to: e.target.value })} />
          </div>
          <button type="submit" className="sr-only focus:not-sr-only focus:rounded-md focus:border focus:px-3 focus:py-1.5 focus:text-sm focus:ring-2 focus:ring-ring">
            Aplicar filtros
          </button>
          {filtered && (
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setF(EMPTY);
                setTarget("");
              }}
            >
              <X /> Limpiar
            </Button>
          )}
          <div className="ml-auto">
            <ExportButton path={reportsApi.auditCsvPath(f)} name="apexx-actividad.csv" />
          </div>
        </div>
        {dateErr && (
          <p className="text-xs text-bad" role="alert">
            {dateErr}
          </p>
        )}
      </form>

      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {q.isPending ? (
        <div className="grid gap-2" role="status" aria-label="Cargando actividad">
          {Array.from({ length: 8 }, (_, i) => (
            <Skeleton key={i} className="h-11 w-full" />
          ))}
        </div>
      ) : items.length === 0 ? (
        <Empty
          icon={<ScrollText />}
          title={filtered ? "Ninguna entrada coincide con los filtros" : "Todavía no hay actividad"}
          action={
            filtered ? (
              <Button
                variant="outline"
                onClick={() => {
                  setF(EMPTY);
                  setTarget("");
                }}
              >
                Quitar filtros
              </Button>
            ) : undefined
          }
        >
          {filtered ? "Prueba con otra persona, acción o fechas." : "Cada cambio de catálogo, pedidos, equipo o diseño aparecerá aquí."}
        </Empty>
      ) : (
        <>
          <div className="relative w-full overflow-x-auto rounded-xl border bg-card">
            <table className="w-full text-sm">
              <caption className="sr-only">Actividad de la tienda, de la más reciente a la más antigua</caption>
              <thead className="bg-muted text-xs text-muted-foreground">
                <tr className="border-b">
                  <th scope="col" className="h-10 whitespace-nowrap px-3 text-left font-semibold">
                    Fecha
                  </th>
                  <th scope="col" className="px-3 text-left font-semibold">
                    Quién
                  </th>
                  <th scope="col" className="px-3 text-left font-semibold">
                    Acción
                  </th>
                  <th scope="col" className="hidden px-3 text-left font-semibold md:table-cell">
                    Objeto
                  </th>
                  <th scope="col" className="px-3 text-right font-semibold">
                    <span className="sr-only">Detalle</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {items.map((e) => {
                  const who = actorLabel(e);
                  return (
                    <tr key={e.seq} className="border-b last:border-0 hover:bg-muted/50">
                      <td className="whitespace-nowrap px-3 py-2.5 align-top tabular-nums text-muted-foreground">{dateTime(e.at)}</td>
                      <td className="px-3 py-2.5 align-top">
                        <div className="grid">
                          <span className="font-medium">{who.name}</span>
                          <span className="text-xs text-muted-foreground">{who.kind}</span>
                        </div>
                      </td>
                      <td className="px-3 py-2.5 align-top">
                        <div className="grid">
                          <span>{actionLabel(e.action)}</span>
                          {ACTION[e.action] && <code className="font-mono text-xs text-muted-foreground">{e.action}</code>}
                          <code className="break-all font-mono text-xs text-muted-foreground md:hidden">{e.target}</code>
                        </div>
                      </td>
                      <td className="hidden max-w-[16rem] px-3 py-2.5 align-top md:table-cell">
                        <code className="break-all font-mono text-xs">{e.target}</code>
                      </td>
                      <td className="px-3 py-2.5 text-right align-top">
                        <Button variant="ghost" size="sm" onClick={() => setOpen(e)} aria-label={`Ver detalle de la entrada ${e.seq}`}>
                          Detalle
                        </Button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <div className="flex items-center justify-between gap-3 text-sm text-muted-foreground">
            <span aria-live="polite">{num(items.length)} entradas cargadas</span>
            {q.hasNextPage ? (
              <Button variant="outline" onClick={() => void q.fetchNextPage()} disabled={q.isFetchingNextPage}>
                {q.isFetchingNextPage ? "Cargando…" : "Cargar más antiguas"}
              </Button>
            ) : (
              <span>Fin del registro</span>
            )}
          </div>
        </>
      )}

      <Sheet open={open !== null} onOpenChange={(v) => !v && setOpen(null)}>
        <SheetContent>
          {open && (
            <div className="grid gap-5">
              <SheetHeader>
                <SheetTitle>{actionLabel(open.action)}</SheetTitle>
                <SheetDescription>
                  Entrada {open.seq} · {dateTime(open.at)}
                </SheetDescription>
              </SheetHeader>
              <dl className="grid gap-3 text-sm">
                {(
                  [
                    ["Quién", `${actorLabel(open).name} · ${actorLabel(open).kind}`],
                    ["Identificador", open.actor],
                    ["Acción", open.action],
                    ["Objeto", open.target],
                    ["Firma (inicio)", open.hash],
                  ] as const
                ).map(([k, v]) => (
                  <div key={k} className="grid gap-0.5">
                    <dt className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{k}</dt>
                    <dd className="break-all font-mono text-xs">{v}</dd>
                  </div>
                ))}
                <div className="grid gap-1">
                  <dt className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Datos registrados</dt>
                  <dd>
                    <pre className="max-h-80 overflow-auto rounded-md border bg-muted p-3 font-mono text-xs">{JSON.stringify(open.data, null, 2)}</pre>
                  </dd>
                </div>
              </dl>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </>
  );
}

// ---------- system status ----------

const WORKER: Record<string, string> = {
  "analytics-orders": "Analítica: pedidos y ventas",
  "analytics-traffic": "Analítica: visitas",
  "analytics-retention": "Analítica: borrado a 90 días",
  invoicer: "Emisión de facturas",
  "outbox-prune": "Limpieza de eventos antiguos",
  "payment-reconciler": "Conciliación de pagos",
  "reservation-expiry": "Liberación de apartados vencidos",
  "session-purge": "Cierre de sesiones vencidas",
  "tenant-keys": "Llaves de cifrado de tiendas",
  "webhook-dispatcher": "Conectores: preparación de avisos",
  "webhook-sender": "Conectores: envío de avisos",
};

function every(sec: number): string {
  if (sec < 60) return `cada ${sec} s`;
  if (sec < 3600) return `cada ${Math.round(sec / 60)} min`;
  return `cada ${Math.round(sec / 3600)} h`;
}

type Tone = "ok" | "warn" | "bad";

function Check({ title, icon, tone, headline, rows, footer }: { title: string; icon: ReactNode; tone: Tone; headline: string; rows: [string, ReactNode][]; footer?: ReactNode }) {
  const label = { ok: "Bien", warn: "Atención", bad: "Falla" }[tone];
  return (
    <Card className="min-w-0">
      <CardHeader className="flex-row items-start justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2.5">
          <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground [&_svg]:size-4">{icon}</span>
          <div className="grid min-w-0">
            <CardTitle className="leading-tight">{title}</CardTitle>
            <CardDescription className="truncate">{headline}</CardDescription>
          </div>
        </div>
        <Badge variant={tone}>{label}</Badge>
      </CardHeader>
      <CardContent>
        <dl className="grid gap-1.5 text-sm">
          {rows.map(([k, v]) => (
            <div key={k} className="flex items-baseline justify-between gap-3">
              <dt className="text-muted-foreground">{k}</dt>
              <dd className="text-right font-medium tabular-nums">{v}</dd>
            </div>
          ))}
        </dl>
        {footer && <div className="mt-3 text-xs text-muted-foreground">{footer}</div>}
      </CardContent>
    </Card>
  );
}

function ms(v: number): string {
  return v >= 1000 ? `${(v / 1000).toFixed(1)} s` : `${v < 10 ? v.toFixed(1) : Math.round(v)} ms`;
}

function StatusBoard({ s, now }: { s: SystemStatus; now: number }) {
  const h = s.health;
  const p = s.projection;
  const projTone: Tone = p.stats.errors > 0 || p.stats.lastLagMs > 5000 ? "bad" : p.eventsBehind > 100 || p.stats.lastLagMs > 1000 ? "warn" : "ok";
  const dbTone: Tone = !h.db.ok ? "bad" : h.db.pingMs > 200 || h.db.poolUsed >= h.db.poolMax ? "warn" : "ok";
  const whTone: Tone = h.webhooks.dead7d > 0 ? "bad" : h.webhooks.retrying > 0 ? "warn" : "ok";
  const payTone: Tone = h.payments.pending > 0 || h.invoices.failed > 0 ? "warn" : "ok";
  const lagSec = h.analytics.ordersSyncedAt ? (now - new Date(h.analytics.ordersSyncedAt).getTime()) / 1000 : Infinity;
  const anTone: Tone = h.analytics.ordersBehind > 0 && lagSec > 120 ? "warn" : "ok";
  const errRate = s.http.requests ? (s.http.errors5xx * 100) / s.http.requests : 0;
  const httpTone: Tone = errRate > 1 ? "bad" : s.http.p95Ms > 1000 ? "warn" : "ok";
  const badWorkers = s.workers.filter((w) => w.state !== "ok");
  const banner = {
    ok: { tone: "ok" as const, text: "Todo funciona con normalidad." },
    degraded: { tone: "warn" as const, text: "La tienda funciona, pero hay puntos que revisar." },
    down: { tone: "bad" as const, text: "No se pudo consultar la base de datos." },
  }[s.status];

  return (
    <>
      <Notice tone={banner.tone} className="items-center px-4 py-3 text-sm font-medium">
        {banner.text} <span className="font-normal opacity-80">Revisado {ago(s.checkedAt, now)}; se actualiza solo cada 10 segundos.</span>
      </Notice>
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        <Check
          title="Tienda en vivo"
          icon={<Gauge />}
          tone={projTone}
          headline="Qué tan rápido se ve un cambio en la tienda"
          rows={[
            ["Último cambio aplicado en", ms(p.stats.lastLagMs)],
            ["Promedio / máximo", `${ms(p.stats.avgLagMs)} / ${ms(p.stats.maxLagMs)}`],
            ["Cambios pendientes de aplicar", num(p.eventsBehind)],
            ["Cambios aplicados", num(p.stats.eventsApplied)],
            ["Errores al aplicar", num(p.stats.errors)],
          ]}
          footer={`En marcha desde ${dateTime(p.stats.loadedAt)}`}
        />
        <Check
          title="Base de datos"
          icon={<Database />}
          tone={dbTone}
          headline={h.db.ok ? `PostgreSQL ${h.db.version}` : "Sin respuesta"}
          rows={[
            ["Tiempo de respuesta", h.db.ok ? ms(h.db.pingMs) : "—"],
            ["Conexiones en uso", `${h.db.poolUsed} de ${h.db.poolMax}`],
            ["Cambios registrados (24 h)", num(h.outbox.last24h)],
          ]}
        />
        <Check
          title="Analítica"
          icon={<Activity />}
          tone={anTone}
          headline="Reportes y panel de inicio"
          rows={[
            ["Ventas actualizadas", ago(h.analytics.ordersSyncedAt, now)],
            ["Visitas actualizadas", ago(h.analytics.trafficRolledAt, now)],
            ["Pedidos por incorporar", num(h.analytics.ordersBehind)],
            ["Eventos de la tienda (24 h)", num(h.analytics.events24h)],
          ]}
        />
        <Check
          title="Conectores"
          icon={<RefreshCw />}
          tone={whTone}
          headline={`${num(h.webhooks.subscriptions)} ${h.webhooks.subscriptions === 1 ? "destino activo" : "destinos activos"}`}
          rows={[
            ["Avisos entregados (24 h)", num(h.webhooks.delivered24h)],
            ["Reintentándose", num(h.webhooks.retrying)],
            ["Fallidos sin entregar (7 días)", num(h.webhooks.dead7d)],
            ["Aviso más antiguo en espera", h.webhooks.oldestPendingSec > 0 ? duration(h.webhooks.oldestPendingSec).replace("hace ", "") : "ninguno"],
          ]}
        />
        <Check
          title="Pagos y facturas"
          icon={<TriangleAlert />}
          tone={payTone}
          headline="Lo que espera una confirmación"
          rows={[
            ["Pagos sin conciliar (+5 min)", num(h.payments.pending)],
            ["Pago más antiguo en espera", h.payments.oldestPendingSec > 0 ? duration(h.payments.oldestPendingSec).replace("hace ", "") : "ninguno"],
            ["Facturas en espera (+10 min)", num(h.invoices.pending)],
            ["Facturas con error", num(h.invoices.failed)],
          ]}
        />
        <Check
          title="Respuesta del servidor"
          icon={<Gauge />}
          tone={httpTone}
          headline="Desde el último arranque de este servidor"
          rows={[
            ["Solicitudes atendidas", num(s.http.requests)],
            ["95% responde en menos de", ms(s.http.p95Ms)],
            ["Errores del servidor", `${num(s.http.errors5xx)} (${errRate.toFixed(2)}%)`],
            ["Rechazos por saturación", num(s.counters.bulkheadRejects)],
            ["Fallas inesperadas", num(s.counters.panics)],
          ]}
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Tareas en segundo plano</CardTitle>
          <CardDescription>
            {badWorkers.length === 0 ? `Las ${s.workers.length} tareas corren a tiempo.` : `${badWorkers.length} de ${s.workers.length} necesitan atención.`} Reinicios automáticos: {num(s.counters.workerRestarts)}.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {s.workers.length === 0 ? (
            <p className="text-sm text-muted-foreground">Este servidor no tiene tareas en segundo plano activas.</p>
          ) : (
            <div className="overflow-x-auto rounded-lg border">
              <table className="w-full text-sm">
                <caption className="sr-only">Estado de las tareas en segundo plano</caption>
                <thead className="bg-muted text-xs text-muted-foreground">
                  <tr>
                    <th scope="col" className="h-9 px-3 text-left font-semibold">
                      Tarea
                    </th>
                    <th scope="col" className="px-3 text-left font-semibold">
                      Estado
                    </th>
                    <th scope="col" className="hidden px-3 text-left font-semibold md:table-cell">
                      Frecuencia
                    </th>
                    <th scope="col" className="px-3 text-left font-semibold">
                      Última vez bien
                    </th>
                    <th scope="col" className="hidden px-3 text-right font-semibold md:table-cell">
                      Duración
                    </th>
                    <th scope="col" className="hidden px-3 text-right font-semibold md:table-cell">
                      Fallas / ejecuciones
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {s.workers.map((w: WorkerView) => (
                    <tr key={w.name} className="border-t">
                      <td className="px-3 py-2">
                        <div className="grid">
                          <span className="font-medium">{WORKER[w.name] ?? w.name}</span>
                          <code className="font-mono text-xs text-muted-foreground">{w.name}</code>
                        </div>
                      </td>
                      <td className="px-3 py-2">
                        <Badge variant={w.state === "ok" ? "ok" : w.state === "late" ? "warn" : "bad"}>{w.state === "ok" ? "A tiempo" : w.state === "late" ? "Atrasada" : "Fallando"}</Badge>
                      </td>
                      <td className="hidden px-3 py-2 text-muted-foreground md:table-cell">{every(w.intervalSec)}</td>
                      <td className="px-3 py-2 tabular-nums">{ago(w.lastOk, now)}</td>
                      <td className="hidden px-3 py-2 text-right tabular-nums md:table-cell">{ms(w.lastMs)}</td>
                      <td className="hidden px-3 py-2 text-right tabular-nums md:table-cell">
                        {num(w.failures)} / {num(w.runs)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      {s.http.slowest.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>Rutas más lentas</CardTitle>
            <CardDescription>Tiempo en que responde el 95% de las solicitudes de cada ruta</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="overflow-x-auto rounded-lg border">
              <table className="w-full text-sm">
                <caption className="sr-only">Rutas más lentas del servidor</caption>
                <thead className="bg-muted text-xs text-muted-foreground">
                  <tr>
                    <th scope="col" className="h-9 px-3 text-left font-semibold">
                      Ruta
                    </th>
                    <th scope="col" className="px-3 text-right font-semibold">
                      95%
                    </th>
                    <th scope="col" className="px-3 text-right font-semibold">
                      Promedio
                    </th>
                    <th scope="col" className="px-3 text-right font-semibold">
                      Solicitudes
                    </th>
                    <th scope="col" className="hidden px-3 text-right font-semibold md:table-cell">
                      Errores
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {s.http.slowest.map((r) => (
                    <tr key={r.route} className="border-t">
                      <td className="px-3 py-2">
                        <code className="break-all font-mono text-xs">{r.route}</code>
                      </td>
                      <td className="px-3 py-2 text-right tabular-nums">≤ {ms(r.p95Ms)}</td>
                      <td className="px-3 py-2 text-right tabular-nums">{ms(r.avgMs)}</td>
                      <td className="px-3 py-2 text-right tabular-nums">{num(r.requests)}</td>
                      <td className="hidden px-3 py-2 text-right tabular-nums md:table-cell">{num(r.errors5xx)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </CardContent>
        </Card>
      )}
    </>
  );
}

function Estado({ me }: { me: Me }) {
  const qc = useQueryClient();
  const now = useNow(5000);
  const q = useQuery({ queryKey: ["ops", "status"], queryFn: reportsApi.status, refetchInterval: 10_000 });
  const [confirm, setConfirm] = useState(false);
  const rebuild = useMutation({
    mutationFn: reportsApi.rebuild,
    onSuccess: (r) => {
      toast.success(`Analítica reconstruida: ${num(r.orders)} pedidos en ${(r.tookMs / 1000).toFixed(1)} s`);
      setConfirm(false);
      void qc.invalidateQueries({ queryKey: ["reports"] });
      void qc.invalidateQueries({ queryKey: ["ops"] });
    },
  });
  return (
    <>
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {q.data && <SandboxMetrics />}
      {q.data ? (
        <StatusBoard s={q.data} now={now} />
      ) : (
        q.isPending && (
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3" role="status" aria-label="Cargando el estado del sistema">
            {Array.from({ length: 6 }, (_, i) => (
              <Skeleton key={i} className="h-48 w-full" />
            ))}
          </div>
        )
      )}
      {can(me, "reports", "admin") && (
        <Card>
          <CardHeader>
            <CardTitle>Mantenimiento</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-wrap items-center justify-between gap-3">
            <p className="max-w-xl text-sm text-muted-foreground">
              Los reportes se actualizan solos. Si alguna cifra no coincide con tus pedidos, puedes volver a calcular toda la analítica desde los pedidos y las visitas guardadas. No cambia ningún pedido.
            </p>
            <Button variant="outline" onClick={() => setConfirm(true)}>
              <Wrench /> Reconstruir analítica
            </Button>
          </CardContent>
        </Card>
      )}
      <AlertDialog open={confirm} onOpenChange={(v) => !v && !rebuild.isPending && setConfirm(false)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>¿Reconstruir la analítica de la tienda?</AlertDialogTitle>
            <AlertDialogDescription>
              Se recalculan ventas, productos, clientes y visitas a partir de los pedidos. Puede tardar hasta unos minutos en tiendas grandes; mientras tanto los reportes siguen disponibles. Quedará anotado en Actividad.
            </AlertDialogDescription>
          </AlertDialogHeader>
          {rebuild.error && <Notice tone="bad">{errorMessage(rebuild.error)}</Notice>}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={rebuild.isPending}>Volver</AlertDialogCancel>
            <Button onClick={() => rebuild.mutate()} disabled={rebuild.isPending}>
              {rebuild.isPending ? "Reconstruyendo…" : "Reconstruir"}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

interface SandboxOps {
  id: string;
  name: string;
  status: string;
  hasPassword: boolean;
  products: number;
  prices: number;
  orders: number;
  copiedAt?: string;
  copyMs: number;
  lastUsedAt: string;
  expiresAt?: string;
  publishes: number;
  lastPublishAt?: string;
  scheduled: number;
  mailCaptured: number;
  aiRequests: number;
  aiCostUsd: number;
  node: { requests: number; errors: number; denied: number; lastAt?: string };
}

const SBX_STATUS: Record<string, string> = { ready: "Listo", copying: "Copiando", failed: "Copia fallida", deleting: "Borrando" };

/** Each sandbox of the store: size, copies, publishes, traffic and AI use. */
function SandboxMetrics() {
  const q = useQuery({
    queryKey: ["ops", "sandboxes"],
    queryFn: () => request<{ items: SandboxOps[]; since: string }>("GET", "/v1/ops/sandboxes"),
    refetchInterval: 30_000,
  });
  if (!q.data?.items.length) return null;
  const d = q.data;
  return (
    <Card>
      <CardHeader>
        <CardTitle>Sandboxes</CardTitle>
        <CardDescription>Tamaño, copias, publicaciones y uso de cada sandbox. Las solicitudes cuentan las de este servidor desde {dateTime(d.since)}</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full text-sm">
            <caption className="sr-only">Métricas de cada sandbox</caption>
            <thead className="bg-muted text-xs text-muted-foreground">
              <tr>
                <th scope="col" className="h-9 px-3 text-left font-semibold">Sandbox</th>
                <th scope="col" className="px-3 text-right font-semibold">Productos</th>
                <th scope="col" className="hidden px-3 text-right font-semibold md:table-cell">Última copia</th>
                <th scope="col" className="px-3 text-right font-semibold">Solicitudes</th>
                <th scope="col" className="px-3 text-right font-semibold">Errores</th>
                <th scope="col" className="hidden px-3 text-right font-semibold md:table-cell">Publicaciones</th>
                <th scope="col" className="hidden px-3 text-right font-semibold lg:table-cell">Correos capturados</th>
                <th scope="col" className="hidden px-3 text-right font-semibold lg:table-cell">IA (30 días)</th>
              </tr>
            </thead>
            <tbody>
              {d.items.map((x) => (
                <tr key={x.id} className="border-t align-top">
                  <td className="px-3 py-2">
                    <span className="font-medium">{x.name}</span>
                    <span className="block text-xs text-muted-foreground">
                      {SBX_STATUS[x.status] ?? x.status}
                      {x.hasPassword ? " · con contraseña" : ""}
                      {x.expiresAt ? ` · vence ${dateTime(x.expiresAt)}` : " · no vence"}
                    </span>
                  </td>
                  <td className="px-3 py-2 text-right tabular-nums">
                    {num(x.products)}
                    <span className="block text-xs text-muted-foreground">{num(x.prices)} precios</span>
                  </td>
                  <td className="hidden px-3 py-2 text-right md:table-cell">
                    {x.copiedAt ? dateTime(x.copiedAt) : "—"}
                    <span className="block text-xs text-muted-foreground">{x.copyMs ? `${(x.copyMs / 1000).toFixed(1)} s` : ""}</span>
                  </td>
                  <td className="px-3 py-2 text-right tabular-nums">
                    {num(x.node.requests)}
                    {x.node.denied > 0 && <span className="block text-xs text-muted-foreground">{num(x.node.denied)} sin acceso</span>}
                  </td>
                  <td className={cn("px-3 py-2 text-right tabular-nums", x.node.errors > 0 && "text-bad")}>{num(x.node.errors)}</td>
                  <td className="hidden px-3 py-2 text-right md:table-cell">
                    {num(x.publishes)}
                    <span className="block text-xs text-muted-foreground">
                      {x.lastPublishAt ? `última ${dateTime(x.lastPublishAt)}` : "ninguna"}
                      {x.scheduled > 0 ? ` · ${x.scheduled} programadas` : ""}
                    </span>
                  </td>
                  <td className="hidden px-3 py-2 text-right tabular-nums lg:table-cell">{num(x.mailCaptured)}</td>
                  <td className="hidden px-3 py-2 text-right tabular-nums lg:table-cell">
                    {num(x.aiRequests)}
                    <span className="block text-xs text-muted-foreground">US$ {x.aiCostUsd.toFixed(2)}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </CardContent>
    </Card>
  );
}

/** Ops hosts the activity log and the system status under #operacion/<id>. */
export function Ops({ me, view }: { me: Me; view?: string }) {
  const active = OPS.find((o) => o.id === view)?.id ?? "actividad";
  return (
    <Page title="Operación" sub="Quién cambió qué en la tienda y cómo está funcionando el sistema.">
      <SubNav base="operacion" items={OPS} active={active} label="Operación" />
      {active === "actividad" ? <Actividad /> : <Estado me={me} />}
    </Page>
  );
}
