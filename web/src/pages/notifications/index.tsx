import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { MailCheck, RefreshCw, Send } from "lucide-react";
import { errorMessage, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { href } from "@/router";
import { mail, type Blocks, type MailSettings, type MailTemplate } from "@/saleApi";
import { dateTime, ErrorBox, Notice, Page, StatusPill } from "../common";
import { EmptyState, SkeletonRows, SubNav, useDebounced } from "../promotions/shared";

const ALL = "__all";
const TABS = [
  { key: "", label: "Plantillas", route: ["correos"] },
  { key: "enviados", label: "Correos enviados", route: ["correos", "enviados"] },
  { key: "ajustes", label: "Remitente y proveedor", route: ["correos", "ajustes"] },
];

/** Routes: #correos, #correos/enviados, #correos/ajustes. */
export function Notifications({ me, route }: { me: Me; route: string[] }) {
  const section = route[1] ?? "";
  const canEdit = can(me, "settings", "edit");
  return (
    <Page title="Correos" sub="Los correos que recibe el comprador: confirmación, envío con guía, reembolso y más. Se envían solos y se reintentan si fallan.">
      <SubNav items={TABS} active={section} label="Secciones de correos" />
      {section === "enviados" ? <Sent canEdit={canEdit} /> : section === "ajustes" ? <Settings canEdit={canEdit} /> : <Templates canEdit={canEdit} />}
    </Page>
  );
}

function Templates({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["mail-templates"], queryFn: mail.templates });
  const [key, setKey] = useState<string>("");
  const items = list.data ?? [];
  const current = items.find((t) => t.key === key) ?? items[0];
  return (
    <div className="grid gap-5 lg:grid-cols-[280px_minmax(0,1fr)]">
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      <ul className="grid content-start gap-1">
        {items.map((t) => (
          <li key={t.key}>
            <button type="button" onClick={() => setKey(t.key)} aria-current={current?.key === t.key ? "true" : undefined}
              className={`grid w-full gap-0.5 rounded-md border px-3 py-2 text-left text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${current?.key === t.key ? "border-primary bg-accent" : "bg-card hover:border-primary/50"}`}>
              <span className="flex items-center justify-between gap-2 font-medium">{t.name}{!t.enabled && <Badge variant="outline">Apagado</Badge>}{t.customized && t.enabled && <Badge variant="secondary">Editado</Badge>}</span>
              <span className="text-xs text-muted-foreground">{t.description}</span>
            </button>
          </li>
        ))}
      </ul>
      {current && <TemplateEditor key={current.key} t={current} canEdit={canEdit} onSaved={() => void qc.invalidateQueries({ queryKey: ["mail-templates"] })} />}
    </div>
  );
}

function TemplateEditor({ t, canEdit, onSaved }: { t: MailTemplate; canEdit: boolean; onSaved: () => void }) {
  const [subject, setSubject] = useState(t.subject);
  const [blocks, setBlocks] = useState<Blocks>(t.blocks);
  const [enabled, setEnabled] = useState(t.enabled);
  const [to, setTo] = useState("");
  const [busy, setBusy] = useState(false);
  const draft = useDebounced(JSON.stringify({ subject, blocks }), 500);
  const preview = useQuery({ queryKey: ["mail-preview", t.key, draft], queryFn: () => mail.preview(t.key, JSON.parse(draft) as { subject: string; blocks: Blocks }) });
  const dirty = subject !== t.subject || JSON.stringify(blocks) !== JSON.stringify(t.blocks) || enabled !== t.enabled;
  const save = (reset = false) => {
    setBusy(true);
    mail.save(t.key, reset ? { reset: true } : { subject, blocks, enabled })
      .then((n) => { toast.success(reset ? "Plantilla restablecida." : "Plantilla guardada."); if (reset) { setSubject(n.subject); setBlocks(n.blocks); setEnabled(n.enabled); } onSaved(); })
      .catch((e) => toast.error(errorMessage(e))).finally(() => setBusy(false));
  };
  const insert = (v: string) => setBlocks({ ...blocks, body: blocks.body + ` {{${v}}}` });
  return (
    <div className="grid gap-5 xl:grid-cols-2">
      <Card>
        <CardHeader><CardTitle className="text-base">{t.name}</CardTitle><CardDescription>{t.description}</CardDescription></CardHeader>
        <CardContent>
          <fieldset disabled={!canEdit} className="grid gap-4">
            <div className="grid gap-1.5"><Label htmlFor="t-subject">Asunto</Label><Input id="t-subject" value={subject} onChange={(e) => setSubject(e.target.value)} /></div>
            <div className="grid gap-1.5"><Label htmlFor="t-heading">Título</Label><Input id="t-heading" value={blocks.heading} onChange={(e) => setBlocks({ ...blocks, heading: e.target.value })} /></div>
            <div className="grid gap-1.5"><Label htmlFor="t-body">Mensaje</Label><Textarea id="t-body" rows={6} value={blocks.body} onChange={(e) => setBlocks({ ...blocks, body: e.target.value })} /></div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="grid gap-1.5"><Label htmlFor="t-cta">Texto del botón</Label><Input id="t-cta" value={blocks.cta} onChange={(e) => setBlocks({ ...blocks, cta: e.target.value })} /></div>
              <div className="grid gap-1.5"><Label htmlFor="t-footer">Pie</Label><Input id="t-footer" value={blocks.footer} onChange={(e) => setBlocks({ ...blocks, footer: e.target.value })} /></div>
            </div>
            <div className="grid gap-1.5">
              <span className="text-sm font-medium">Datos que puedes insertar</span>
              <div className="flex flex-wrap gap-1">{t.variables.map((v) => <button key={v.name} type="button" title={`Ejemplo: ${v.sample}`} onClick={() => insert(v.name)} className="rounded border bg-muted px-2 py-0.5 font-mono text-xs hover:border-primary">{`{{${v.name}}}`}</button>)}</div>
            </div>
            {t.optional && <label className="flex items-center gap-2 text-sm"><Checkbox checked={enabled} onCheckedChange={(c) => setEnabled(!!c)} /> Enviar este correo</label>}
            <div className="flex flex-wrap gap-2">
              <Button type="button" onClick={() => save()} disabled={busy || !dirty}>Guardar</Button>
              {t.customized && <Button type="button" variant="outline" onClick={() => save(true)} disabled={busy}>Restablecer el original</Button>}
            </div>
            <form className="flex flex-wrap items-end gap-2 border-t pt-4" onSubmit={(e: FormEvent) => { e.preventDefault(); mail.test(t.key, to.trim()).then(() => toast.success("Correo de prueba en camino.")).catch((er) => toast.error(errorMessage(er))); }}>
              <div className="grid flex-1 gap-1.5"><Label htmlFor="t-test">Enviar prueba a</Label><Input id="t-test" type="email" required value={to} onChange={(e) => setTo(e.target.value)} placeholder="tu@correo.com" /></div>
              <Button type="submit" variant="outline"><Send aria-hidden="true" /> Enviar prueba</Button>
            </form>
          </fieldset>
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle className="text-base">Vista previa</CardTitle><CardDescription>Asunto: {preview.data?.subject ?? "…"}</CardDescription></CardHeader>
        <CardContent>
          {preview.error ? <ErrorBox error={preview.error} /> : <iframe title="Vista previa del correo" sandbox="" srcDoc={preview.data?.html ?? ""} className="h-[560px] w-full rounded-md border bg-white" />}
        </CardContent>
      </Card>
    </div>
  );
}

function Sent({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const [status, setStatus] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const list = useQuery({ queryKey: ["mail-messages", status], queryFn: () => mail.messages({ status }), refetchInterval: 5000 });
  const detail = useQuery({ queryKey: ["mail-message", open], queryFn: () => mail.message(open!), enabled: !!open });
  const items = list.data?.items ?? [];
  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <Select value={status || ALL} onValueChange={(v) => setStatus(v === ALL ? "" : v)}>
          <SelectTrigger className="w-44" aria-label="Estado"><SelectValue /></SelectTrigger>
          <SelectContent><SelectItem value={ALL}>Todos</SelectItem><SelectItem value="sent">Enviados</SelectItem><SelectItem value="queued">En cola</SelectItem><SelectItem value="failed">Fallidos</SelectItem></SelectContent>
        </Select>
        {list.data?.stats && <span className="text-sm text-muted-foreground">{Object.entries(list.data.stats).map(([k, v]) => `${v} ${k}`).join(" · ")}</span>}
      </div>
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      {!list.isLoading && items.length === 0 ? (
        <EmptyState icon={<MailCheck />} title="Sin correos todavía" text="Aquí verás cada correo enviado a tus clientes, con su estado y contenido." />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader><TableRow><TableHead>Fecha</TableHead><TableHead>Para</TableHead><TableHead>Asunto</TableHead><TableHead>Estado</TableHead><TableHead>Pedido</TableHead><TableHead><span className="sr-only">Acciones</span></TableHead></TableRow></TableHeader>
            <TableBody>
              {list.isLoading ? <SkeletonRows cols={6} /> : items.map((m) => (
                <TableRow key={m.id} className="cursor-pointer" onClick={() => setOpen(m.id)}>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{dateTime(m.createdAt)}</TableCell>
                  <TableCell>{m.to}</TableCell>
                  <TableCell className="max-w-80 truncate">{m.subject}</TableCell>
                  <TableCell><StatusPill status={m.status} />{m.lastError && <div className="max-w-56 truncate text-xs text-bad" title={m.lastError}>{m.lastError}</div>}</TableCell>
                  <TableCell>{m.orderId ? <a className="text-primary hover:underline" href={href("pedidos", m.orderId)} onClick={(e) => e.stopPropagation()}>Ver</a> : "—"}</TableCell>
                  <TableCell>{canEdit && m.status === "failed" && <Button size="sm" variant="outline" onClick={(e) => { e.stopPropagation(); mail.retry(m.id).then(() => { toast.success("Reintentando."); void qc.invalidateQueries({ queryKey: ["mail-messages"] }); }).catch((er) => toast.error(errorMessage(er))); }}><RefreshCw aria-hidden="true" /> Reintentar</Button>}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <Dialog open={!!open} onOpenChange={(o) => !o && setOpen(null)}>
        <DialogContent className="max-w-2xl">
          <DialogHeader><DialogTitle className="truncate">{detail.data?.subject ?? "Correo"}</DialogTitle><DialogDescription>Para {detail.data?.to} · {dateTime(detail.data?.createdAt)} · {detail.data?.attempts ?? 0} intento(s) · proveedor {detail.data?.provider}</DialogDescription></DialogHeader>
          <iframe title="Contenido del correo" sandbox="" srcDoc={detail.data?.html ?? ""} className="h-[520px] w-full rounded-md border bg-white" />
        </DialogContent>
      </Dialog>
    </div>
  );
}

function Settings({ canEdit }: { canEdit: boolean }) {
  const q = useQuery({ queryKey: ["mail-settings"], queryFn: mail.settings });
  const [s, setS] = useState<MailSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => { if (q.data) setS(q.data); }, [q.data]);
  if (q.error) return <ErrorBox error={q.error} onRetry={() => void q.refetch()} />;
  if (!s) return <p className="text-sm text-muted-foreground">Cargando…</p>;
  const set = (patch: Partial<MailSettings>) => setS({ ...s, ...patch });
  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!s) return;
    setBusy(true);
    setError(null);
    try {
      setS(await mail.saveSettings(s));
      toast.success("Ajustes guardados.");
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }
  return (
    <form onSubmit={(e) => void submit(e)}>
      <fieldset disabled={!canEdit} className="grid max-w-3xl gap-5">
        <Card>
          <CardHeader><CardTitle className="text-base">Remitente y marca</CardTitle><CardDescription>Cómo se ven tus correos en la bandeja del cliente.</CardDescription></CardHeader>
          <CardContent className="grid gap-4 sm:grid-cols-2">
            <div className="grid gap-1.5"><Label htmlFor="ms-name">Nombre del remitente</Label><Input id="ms-name" value={s.senderName} onChange={(e) => set({ senderName: e.target.value })} placeholder="Tienda Norte" /></div>
            <div className="grid gap-1.5"><Label htmlFor="ms-email">Correo del remitente</Label><Input id="ms-email" type="email" value={s.senderEmail} onChange={(e) => set({ senderEmail: e.target.value })} placeholder="hola@tutienda.mx" /></div>
            <div className="grid gap-1.5"><Label htmlFor="ms-reply">Responder a</Label><Input id="ms-reply" type="email" value={s.replyTo} onChange={(e) => set({ replyTo: e.target.value })} /></div>
            <div className="grid gap-1.5"><Label htmlFor="ms-color">Color de la marca</Label><Input id="ms-color" type="color" className="h-9 w-24 p-1" value={s.brand.color || "#2E44E8"} onChange={(e) => set({ brand: { ...s.brand, color: e.target.value } })} /></div>
            <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="ms-footer">Pie de todos los correos</Label><Input id="ms-footer" value={s.brand.footer} onChange={(e) => set({ brand: { ...s.brand, footer: e.target.value } })} placeholder="Dirección fiscal, teléfono de atención…" /></div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle className="text-base">Proveedor de envío</CardTitle><CardDescription>Quién entrega los correos. En modo de prueba se guardan aquí sin enviarse.</CardDescription></CardHeader>
          <CardContent className="grid gap-4 sm:grid-cols-2">
            <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="ms-prov">Proveedor</Label>
              <Select value={s.provider || "outbox"} onValueChange={(v) => set({ provider: v })}>
                <SelectTrigger id="ms-prov" className="max-w-xs"><SelectValue /></SelectTrigger>
                <SelectContent><SelectItem value="outbox">Modo de prueba (no envía)</SelectItem><SelectItem value="smtp">SMTP</SelectItem><SelectItem value="sendgrid">SendGrid</SelectItem></SelectContent>
              </Select>
            </div>
            {s.provider === "smtp" && (
              <>
                <div className="grid gap-1.5"><Label htmlFor="ms-host">Servidor</Label><Input id="ms-host" value={s.smtp.host ?? ""} onChange={(e) => set({ smtp: { ...s.smtp, host: e.target.value } })} placeholder="smtp.tuproveedor.com" /></div>
                <div className="grid gap-1.5"><Label htmlFor="ms-port">Puerto</Label><Input id="ms-port" inputMode="numeric" value={String(s.smtp.port ?? 587)} onChange={(e) => set({ smtp: { ...s.smtp, port: Number(e.target.value) || 587 } })} /></div>
                <div className="grid gap-1.5"><Label htmlFor="ms-user">Usuario</Label><Input id="ms-user" value={s.smtp.username ?? ""} onChange={(e) => set({ smtp: { ...s.smtp, username: e.target.value } })} /></div>
                <div className="grid gap-1.5"><Label htmlFor="ms-pass">Contraseña</Label><Input id="ms-pass" type="password" autoComplete="new-password" placeholder={s.hasSmtpPassword ? "Guardada. Escribe para cambiarla" : ""} onChange={(e) => set({ smtp: { ...s.smtp, password: e.target.value } })} /></div>
              </>
            )}
            {s.provider === "sendgrid" && (
              <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="ms-sg">Llave de API de SendGrid</Label><Input id="ms-sg" type="password" autoComplete="new-password" placeholder={s.hasSendgridKey ? "Guardada. Escribe para cambiarla" : "SG.…"} onChange={(e) => set({ sendgridKey: e.target.value })} /></div>
            )}
            <div className="grid gap-1.5"><Label htmlFor="ms-cart">Recordar carritos abandonados después de (minutos)</Label><Input id="ms-cart" inputMode="numeric" value={String(s.cartReminderMinutes)} onChange={(e) => set({ cartReminderMinutes: Number(e.target.value.replace(/\D/g, "")) || 0 })} /><p className="text-xs text-muted-foreground">0 = no enviar recordatorios automáticos.</p></div>
          </CardContent>
        </Card>
        {s.provider === "outbox" && <Notice tone="warn">Estás en modo de prueba: los correos se generan y se pueden ver en “Correos enviados”, pero no llegan a los clientes.</Notice>}
        <ErrorBox error={error} />
        {canEdit && <div><Button type="submit" disabled={busy}>{busy ? "Guardando…" : "Guardar ajustes"}</Button></div>}
      </fieldset>
    </form>
  );
}
