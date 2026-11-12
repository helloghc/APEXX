import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Copy, Gift, Plus } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { centsToPesos, formatMoney, parsePesos, readPesos } from "@/money";
import { can } from "@/perms";
import { href } from "@/router";
import { dateTime, ErrorBox, Notice, Page } from "../common";
import { EmptyState, SkeletonRows, SubNav, useDebounced } from "../promotions/shared";

const enc = encodeURIComponent;
interface GiftCard { id: string; code?: string; last4: string; kind: "gift" | "credit"; initial: number; balance: number; currency: string; status: string; recipientName?: string; note?: string; expiresAt?: string; expired: boolean; createdAt: string }
interface Movement { id: string; kind: string; amount: number; orderId?: string; actor: string; note?: string; at: string }
interface Installments { enabled: boolean; plans: number[]; minAmount: number }
interface Risk { enabled: boolean; reviewAmount: number; maxOrdersPerHour: number; maxOrdersPerIpHour: number; blockedEmails: string[]; blockedPostalCodes: string[] }

const api = {
  list: (kind: string, q: string) => request<{ items: GiftCard[]; stats: Record<string, number> }>("GET", `/v1/gift-cards?kind=${enc(kind)}&q=${enc(q)}`),
  create: (b: Record<string, unknown>) => request<GiftCard>("POST", "/v1/gift-cards", b),
  get: (id: string) => request<{ card: GiftCard; movements: Movement[] }>("GET", `/v1/gift-cards/${enc(id)}`),
  patch: (id: string, b: { status?: string; delta?: number; note?: string }) => request<GiftCard>("PATCH", `/v1/gift-cards/${enc(id)}`, b),
  rules: () => request<{ installments: Installments; risk: Risk }>("GET", "/v1/payment-rules"),
  saveRules: (b: { installments?: Installments; risk?: Risk }) => request<{ installments: Installments; risk: Risk }>("PUT", "/v1/payment-rules", b),
};

const TABS = [
  { key: "", label: "Tarjetas y saldo a favor", route: ["tarjetas"] },
  { key: "meses", label: "Meses sin intereses", route: ["tarjetas", "meses"] },
  { key: "antifraude", label: "Antifraude", route: ["tarjetas", "antifraude"] },
];
const MOVE: Record<string, string> = { issue: "Emisión", redeem: "Compra", release: "Devuelto (pedido no completado)", refund: "Reembolso", adjust: "Ajuste manual" };

/** Routes: #tarjetas, #tarjetas/meses, #tarjetas/antifraude. */
export function Wallet({ me, route }: { me: Me; route: string[] }) {
  const section = route[1] ?? "";
  return (
    <Page title="Tarjetas, plazos y antifraude" sub="Tarjetas de regalo y saldo a favor que tus clientes usan al pagar, meses sin intereses y reglas para frenar pedidos sospechosos.">
      <SubNav items={TABS} active={section} label="Secciones" />
      {section === "meses" ? <InstallmentsForm canEdit={can(me, "payments", "admin")} /> : section === "antifraude" ? <RiskForm canEdit={can(me, "payments", "admin")} /> : <Cards canEdit={can(me, "payments", "edit")} />}
    </Page>
  );
}

function CardStatus({ c }: { c: GiftCard }) {
  if (c.status !== "active") return <Badge variant="outline">Desactivada</Badge>;
  if (c.expired) return <Badge variant="warn">Vencida</Badge>;
  if (c.balance === 0) return <Badge variant="secondary">Sin saldo</Badge>;
  return <Badge variant="ok">Activa</Badge>;
}

function Cards({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const [kind, setKind] = useState("");
  const [search, setSearch] = useState("");
  const dq = useDebounced(search.trim());
  const q = useQuery({ queryKey: ["gift-cards", kind, dq], queryFn: () => api.list(kind, dq) });
  const [create, setCreate] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["gift-cards"] });
  const items = q.data?.items ?? [];
  const st = q.data?.stats ?? {};
  return (
    <div className="grid gap-4">
      <div className="grid gap-3 sm:grid-cols-3">
        {[["Saldo por usar", formatMoney(st.outstanding ?? 0)], ["Total emitido", formatMoney(st.issued ?? 0)], ["Tarjetas", String(st.cards ?? 0)]].map(([l, v]) => (
          <Card key={l}><CardContent className="grid gap-1 pt-5"><span className="text-sm text-muted-foreground">{l}</span><span className="text-xl font-semibold tabular-nums">{v}</span></CardContent></Card>
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Input aria-label="Buscar tarjeta" className="w-64" placeholder="Últimos 4 o nombre" value={search} onChange={(e) => setSearch(e.target.value)} />
        <Select value={kind || "all"} onValueChange={(v) => setKind(v === "all" ? "" : v)}>
          <SelectTrigger className="w-48" aria-label="Tipo"><SelectValue /></SelectTrigger>
          <SelectContent><SelectItem value="all">Todas</SelectItem><SelectItem value="gift">Tarjetas de regalo</SelectItem><SelectItem value="credit">Saldo a favor</SelectItem></SelectContent>
        </Select>
        {canEdit && <Button className="ml-auto" onClick={() => setCreate(true)}><Plus aria-hidden="true" /> Nueva tarjeta</Button>}
      </div>
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {!q.isLoading && items.length === 0 ? (
        <EmptyState icon={<Gift />} title="Sin tarjetas todavía" text="Crea una tarjeta de regalo para un cliente, o da saldo a favor al reembolsar un pedido. Se usan escribiendo el código al pagar." />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader><TableRow><TableHead>Tarjeta</TableHead><TableHead>Tipo</TableHead><TableHead>Para</TableHead><TableHead className="text-right">Saldo</TableHead><TableHead>Estado</TableHead><TableHead>Creada</TableHead></TableRow></TableHeader>
            <TableBody>
              {q.isLoading ? <SkeletonRows cols={6} /> : items.map((c) => (
                <TableRow key={c.id} className="cursor-pointer" onClick={() => setOpen(c.id)}>
                  <TableCell><button type="button" className="font-mono font-medium text-primary hover:underline" onClick={(e) => { e.stopPropagation(); setOpen(c.id); }}>•••• {c.last4}</button></TableCell>
                  <TableCell>{c.kind === "credit" ? "Saldo a favor" : "Regalo"}</TableCell>
                  <TableCell className="max-w-56 truncate">{c.recipientName || c.note || "—"}</TableCell>
                  <TableCell className="text-right tabular-nums">{formatMoney(c.balance, c.currency)} <span className="text-muted-foreground">de {formatMoney(c.initial, c.currency)}</span></TableCell>
                  <TableCell><CardStatus c={c} /></TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{dateTime(c.createdAt)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {create && <CreateDialog onClose={() => setCreate(false)} onDone={refresh} />}
      {open && <CardDialog id={open} canEdit={canEdit} onClose={() => setOpen(null)} onDone={refresh} />}
    </div>
  );
}

function CreateDialog({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [amount, setAmount] = useState("500.00");
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [note, setNote] = useState("");
  const [expires, setExpires] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [made, setMade] = useState<GiftCard | null>(null);
  const save = useMutation({
    mutationFn: () => api.create({ amount: parsePesos(amount) ?? 0, kind: "gift", recipientEmail: email.trim(), recipientName: name.trim(), note: note.trim(), expiresAt: expires ? new Date(expires + "T23:59:59").toISOString() : undefined }),
    onSuccess: (c) => { setMade(c); onDone(); },
    onError: (e) => { setErrors(fieldErrors(e)); toast.error(errorMessage(e)); },
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        {made ? (
          <>
            <DialogHeader><DialogTitle>Tarjeta creada</DialogTitle><DialogDescription>Este es el único momento en que se muestra el código completo. {email ? `También lo enviamos a ${email}.` : "Cópialo y entrégalo a tu cliente."}</DialogDescription></DialogHeader>
            <div className="flex items-center justify-between gap-3 rounded-lg border bg-muted p-4"><code className="text-lg font-semibold tracking-wider" data-testid="gift-code">{made.code}</code>
              <Button variant="outline" size="sm" onClick={() => void navigator.clipboard.writeText(made.code ?? "").then(() => toast.success("Código copiado."))}><Copy aria-hidden="true" /> Copiar</Button></div>
            <p className="text-sm text-muted-foreground">Saldo: {formatMoney(made.balance, made.currency)}. Guardamos solo una huella del código: ni tu equipo ni Apexx pueden verlo después.</p>
            <DialogFooter><Button onClick={onClose}>Listo</Button></DialogFooter>
          </>
        ) : (
          <>
            <DialogHeader><DialogTitle>Nueva tarjeta de regalo</DialogTitle><DialogDescription>Tu cliente la usa escribiendo el código al pagar. Si no gasta todo, el resto queda en la misma tarjeta.</DialogDescription></DialogHeader>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="grid gap-1.5"><Label htmlFor="gc-amount">Saldo (MXN)</Label><Input id="gc-amount" inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} aria-invalid={!!errors.amount} />{errors.amount && <p className="text-sm text-bad" role="alert">{errors.amount}</p>}</div>
              <div className="grid gap-1.5"><Label htmlFor="gc-exp">Vence (opcional)</Label><Input id="gc-exp" type="date" value={expires} onChange={(e) => setExpires(e.target.value)} />{errors.expiresAt && <p className="text-sm text-bad">{errors.expiresAt}</p>}</div>
              <div className="grid gap-1.5"><Label htmlFor="gc-name">Nombre de quien la recibe</Label><Input id="gc-name" value={name} onChange={(e) => setName(e.target.value)} /></div>
              <div className="grid gap-1.5"><Label htmlFor="gc-email">Correo (para enviarle el código)</Label><Input id="gc-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} aria-invalid={!!errors.recipientEmail} />{errors.recipientEmail && <p className="text-sm text-bad">{errors.recipientEmail}</p>}</div>
              <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="gc-note">Nota interna</Label><Input id="gc-note" value={note} onChange={(e) => setNote(e.target.value)} placeholder="Regalo por aniversario, compensación…" /></div>
            </div>
            <DialogFooter><Button variant="outline" onClick={onClose}>Cancelar</Button><Button onClick={() => { const c = parsePesos(amount); if (!c || c <= 0) { setErrors({ amount: "Escribe un saldo mayor a cero, por ejemplo 500." }); document.getElementById("gc-amount")?.focus(); return; } save.mutate(); }} disabled={save.isPending}>{save.isPending ? "Creando…" : "Crear tarjeta"}</Button></DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

function CardDialog({ id, canEdit, onClose, onDone }: { id: string; canEdit: boolean; onClose: () => void; onDone: () => void }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["gift-card", id], queryFn: () => api.get(id) });
  const [delta, setDelta] = useState("");
  const [note, setNote] = useState("");
  const act = useMutation({
    mutationFn: (b: { status?: string; delta?: number; note?: string }) => api.patch(id, b),
    onSuccess: () => { toast.success("Tarjeta actualizada."); setDelta(""); setNote(""); void qc.invalidateQueries({ queryKey: ["gift-card", id] }); onDone(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const c = q.data?.card;
  const cents = parsePesos(delta.replace(/^-/, ""));
  const signed = cents == null ? null : delta.trim().startsWith("-") ? -cents : cents;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader><DialogTitle>Tarjeta •••• {c?.last4 ?? ""}</DialogTitle><DialogDescription>{c ? `${c.kind === "credit" ? "Saldo a favor" : "Tarjeta de regalo"} · saldo ${formatMoney(c.balance, c.currency)} de ${formatMoney(c.initial, c.currency)}${c.expiresAt ? ` · vence ${dateTime(c.expiresAt)}` : ""}` : "Cargando…"}</DialogDescription></DialogHeader>
        <ErrorBox error={q.error} />
        {c && (
          <div className="grid gap-4">
            <div className="flex flex-wrap items-center gap-2"><CardStatus c={c} />{c.recipientName && <span className="text-sm">Para {c.recipientName}</span>}{c.note && <span className="text-sm text-muted-foreground">{c.note}</span>}</div>
            <div className="max-h-64 overflow-auto rounded-md border">
              <Table>
                <TableHeader><TableRow><TableHead>Fecha</TableHead><TableHead>Movimiento</TableHead><TableHead className="text-right">Monto</TableHead></TableRow></TableHeader>
                <TableBody>{(q.data?.movements ?? []).map((m) => (
                  <TableRow key={m.id}><TableCell className="whitespace-nowrap text-muted-foreground">{dateTime(m.at)}</TableCell>
                    <TableCell>{MOVE[m.kind] ?? m.kind}{m.orderId && <> · <a className="text-primary hover:underline" href={href("pedidos", m.orderId)}>ver pedido</a></>}{m.note && <span className="text-muted-foreground"> · {m.note}</span>}</TableCell>
                    <TableCell className={`text-right tabular-nums ${m.amount < 0 ? "" : "text-ok"}`}>{m.amount < 0 ? "−" : "+"}{formatMoney(Math.abs(m.amount), c.currency)}</TableCell></TableRow>
                ))}</TableBody>
              </Table>
            </div>
            {canEdit && (
              <div className="grid gap-3 border-t pt-4">
                <div className="grid gap-3 sm:grid-cols-[140px_1fr_auto] sm:items-end">
                  <div className="grid gap-1.5"><Label htmlFor="gc-delta">Ajustar saldo</Label><Input id="gc-delta" inputMode="decimal" placeholder="100 o -50" value={delta} onChange={(e) => setDelta(e.target.value)} /></div>
                  <div className="grid gap-1.5"><Label htmlFor="gc-why">Motivo</Label><Input id="gc-why" value={note} onChange={(e) => setNote(e.target.value)} /></div>
                  <Button variant="outline" disabled={act.isPending || !signed || !note.trim()} onClick={() => signed && act.mutate({ delta: signed, note })}>Aplicar ajuste</Button>
                </div>
                <div><Button variant="outline" disabled={act.isPending} onClick={() => act.mutate({ status: c.status === "active" ? "disabled" : "active" })}>{c.status === "active" ? "Desactivar tarjeta" : "Reactivar tarjeta"}</Button></div>
              </div>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function useRules() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["payment-rules"], queryFn: api.rules });
  const save = useMutation({
    mutationFn: api.saveRules,
    onSuccess: (r) => { qc.setQueryData(["payment-rules"], r); toast.success("Guardado. Aplica desde la siguiente compra."); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return { q, save };
}

function InstallmentsForm({ canEdit }: { canEdit: boolean }) {
  const { q, save } = useRules();
  const [v, setV] = useState<Installments | null>(null);
  const [min, setMin] = useState("");
  useEffect(() => { if (q.data && !v) { setV(q.data.installments); setMin(centsToPesos(q.data.installments.minAmount)); } }, [q.data, v]);
  if (q.error) return <ErrorBox error={q.error} onRetry={() => void q.refetch()} />;
  if (!v) return <p className="text-sm text-muted-foreground">Cargando…</p>;
  return (
    <fieldset disabled={!canEdit} className="grid max-w-2xl gap-5">
      <Card>
        <CardHeader><CardTitle className="text-base">Meses sin intereses</CardTitle><CardDescription>Tu cliente elige el plazo al pagar con tarjeta de crédito. Stripe cobra la comisión del plazo a tu cuenta; revisa las tarifas vigentes antes de activarlo.</CardDescription></CardHeader>
        <CardContent className="grid gap-4">
          <label className="flex items-center gap-2 text-sm font-medium"><Checkbox checked={v.enabled} onCheckedChange={(c) => setV({ ...v, enabled: !!c })} /> Ofrecer meses sin intereses</label>
          <div className="grid gap-2"><span className="text-sm font-medium">Plazos</span>
            <div className="flex flex-wrap gap-3">{[3, 6, 9, 12, 18, 24].map((n) => (
              <label key={n} className="flex items-center gap-2 text-sm"><Checkbox checked={v.plans.includes(n)} onCheckedChange={(c) => setV({ ...v, plans: c ? [...v.plans, n].sort((a, b) => a - b) : v.plans.filter((x) => x !== n) })} /> {n} meses</label>
            ))}</div>
          </div>
          <div className="grid max-w-xs gap-1.5"><Label htmlFor="msi-min">Compra mínima (MXN)</Label><Input id="msi-min" inputMode="decimal" value={min} onChange={(e) => setMin(e.target.value)} /><p className="text-xs text-muted-foreground">Los plazos se ofrecen solo en pedidos desde este total.</p></div>
        </CardContent>
      </Card>
      {canEdit ? <div><Button onClick={() => { try { save.mutate({ installments: { ...v, minAmount: readPesos(min, "Compra mínima (MXN)") } }); } catch (e) { toast.error(errorMessage(e)); } }} disabled={save.isPending}>{save.isPending ? "Guardando…" : "Guardar"}</Button></div> : <Notice tone="warn">Solo un administrador de pagos puede cambiar esto.</Notice>}
    </fieldset>
  );
}

function RiskForm({ canEdit }: { canEdit: boolean }) {
  const { q, save } = useRules();
  const [v, setV] = useState<Risk | null>(null);
  const [review, setReview] = useState("");
  const [emails, setEmails] = useState("");
  const [zips, setZips] = useState("");
  useEffect(() => {
    if (q.data && !v) { setV(q.data.risk); setReview(q.data.risk.reviewAmount ? centsToPesos(q.data.risk.reviewAmount) : ""); setEmails((q.data.risk.blockedEmails ?? []).join("\n")); setZips((q.data.risk.blockedPostalCodes ?? []).join("\n")); }
  }, [q.data, v]);
  if (q.error) return <ErrorBox error={q.error} onRetry={() => void q.refetch()} />;
  if (!v) return <p className="text-sm text-muted-foreground">Cargando…</p>;
  const list = (s: string) => s.split(/[\n,;]+/).map((x) => x.trim()).filter(Boolean);
  const num = (s: string) => Number(s.replace(/\D/g, "")) || 0;
  return (
    <fieldset disabled={!canEdit} className="grid max-w-3xl gap-5">
      <Notice tone="ok">Stripe ya revisa cada pago con su propio antifraude. Estas reglas son tuyas y se aplican antes de cobrar. Los pedidos marcados aparecen en <a className="underline" href={href("pedidos")}>Pedidos</a>, con el filtro “Por revisar (antifraude)”.</Notice>
      <Card>
        <CardHeader><CardTitle className="text-base">Reglas</CardTitle><CardDescription>Deja un límite en blanco o en 0 para no usarlo.</CardDescription></CardHeader>
        <CardContent className="grid gap-4">
          <label className="flex items-center gap-2 text-sm font-medium"><Checkbox checked={v.enabled} onCheckedChange={(c) => setV({ ...v, enabled: !!c })} /> Aplicar mis reglas antifraude</label>
          <div className="grid gap-4 sm:grid-cols-3">
            <div className="grid gap-1.5"><Label htmlFor="rk-review">Revisar pedidos desde (MXN)</Label><Input id="rk-review" inputMode="decimal" value={review} onChange={(e) => setReview(e.target.value)} placeholder="20000" /><p className="text-xs text-muted-foreground">Se cobran, pero quedan marcados para revisar antes de enviar.</p></div>
            <div className="grid gap-1.5"><Label htmlFor="rk-email">Máx. pedidos por correo en 1 hora</Label><Input id="rk-email" inputMode="numeric" value={v.maxOrdersPerHour || ""} onChange={(e) => setV({ ...v, maxOrdersPerHour: num(e.target.value) })} placeholder="5" /><p className="text-xs text-muted-foreground">Al pasar el límite, el pedido se rechaza.</p></div>
            <div className="grid gap-1.5"><Label htmlFor="rk-ip">Máx. pedidos por conexión en 1 hora</Label><Input id="rk-ip" inputMode="numeric" value={v.maxOrdersPerIpHour || ""} onChange={(e) => setV({ ...v, maxOrdersPerIpHour: num(e.target.value) })} placeholder="8" /><p className="text-xs text-muted-foreground">Al pasar el límite, el pedido se marca para revisar.</p></div>
          </div>
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle className="text-base">Listas de bloqueo</CardTitle><CardDescription>Los pedidos que coinciden se rechazan con un mensaje neutro; el intento queda en el registro de actividad.</CardDescription></CardHeader>
        <CardContent className="grid gap-4 sm:grid-cols-2">
          <div className="grid gap-1.5"><Label htmlFor="rk-emails">Correos o dominios</Label><Textarea id="rk-emails" rows={6} className="font-mono" value={emails} onChange={(e) => setEmails(e.target.value)} placeholder={"estafa@ejemplo.com\n@dominio-temporal.com"} /></div>
          <div className="grid gap-1.5"><Label htmlFor="rk-zips">Códigos postales</Label><Textarea id="rk-zips" rows={6} className="font-mono" value={zips} onChange={(e) => setZips(e.target.value)} placeholder="00000" /></div>
        </CardContent>
      </Card>
      {canEdit ? <div><Button onClick={() => save.mutate({ risk: { ...v, reviewAmount: parsePesos(review) ?? 0, blockedEmails: list(emails), blockedPostalCodes: list(zips) } })} disabled={save.isPending}>{save.isPending ? "Guardando…" : "Guardar reglas"}</Button></div> : <Notice tone="warn">Solo un administrador de pagos puede cambiar esto.</Notice>}
    </fieldset>
  );
}
