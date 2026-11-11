import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import QRCode from "qrcode";
import { useEffect, useState, type FormEvent } from "react";
import { Copy, MailPlus, Monitor, ShieldCheck, Trash2 } from "lucide-react";
import { ApiError, errorMessage, fieldErrors, request, type Me, type Permissions } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { toast } from "@/components/ui/sonner";
import { compact, PRESETS } from "@/perms";
import { dateTime, ErrorBox, Notice, Page } from "../common";

const enc = encodeURIComponent;
interface Session { id: string; createdAt: string; lastSeenAt: string; ip: string; userAgent: string; current: boolean }
interface Invitation { id: string; email: string; name: string; permissions: Permissions; createdAt: string; expiresAt: string; expired: boolean }
export interface Role { id: string; name: string; description: string; permissions: Permissions }

export const accountApi = {
  begin: () => request<{ secret: string; uri: string }>("POST", "/v1/me/2fa/begin", {}),
  confirm: (code: string) => request<{ recoveryCodes: string[] }>("POST", "/v1/me/2fa/confirm", { code }),
  disable: (password: string) => request<void>("POST", "/v1/me/2fa/disable", { password }, { as: "none" }),
  reset: (id: string) => request<void>("POST", `/v1/team/members/${enc(id)}/2fa/reset`, {}, { as: "none" }),
  sessions: () => request<{ items: Session[] }>("GET", "/v1/me/sessions").then((r) => r.items),
  revoke: (id: string) => request<void>("DELETE", `/v1/me/sessions/${enc(id)}`, undefined, { as: "none" }),
  revokeOthers: () => request<{ revoked: number }>("POST", "/v1/me/sessions/revoke-others", {}),
  password: (current: string, next: string) => request<void>("POST", "/v1/me/password", { current, new: next }, { as: "none" }),
  invitations: () => request<{ items: Invitation[] }>("GET", "/v1/team/invitations").then((r) => r.items),
  invite: (b: { email: string; name: string; permissions: Permissions }) => request<{ invitation: Invitation; link: string; emailed: boolean }>("POST", "/v1/team/invitations", b),
  revokeInvite: (id: string) => request<void>("DELETE", `/v1/team/invitations/${enc(id)}`, undefined, { as: "none" }),
  inviteInfo: (token: string) => request<{ store: string; storeName: string; email: string; name: string }>("GET", `/v1/invitations/${enc(token)}`),
  accept: (token: string, name: string, password: string) => request<{ store: string }>("POST", `/v1/invitations/${enc(token)}/accept`, { name, password }),
  roles: () => request<{ items: Role[] }>("GET", "/v1/team/roles").then((r) => r.items),
  saveRole: (r: Role) => request<Role>("PUT", `/v1/team/roles/${enc(r.id)}`, r),
  deleteRole: (id: string) => request<void>("DELETE", `/v1/team/roles/${enc(id)}`, undefined, { as: "none" }),
};

function device(ua: string): string {
  const os = /Windows/.test(ua) ? "Windows" : /Android/.test(ua) ? "Android" : /iPhone|iPad/.test(ua) ? "iPhone o iPad" : /Mac OS/.test(ua) ? "Mac" : /Linux/.test(ua) ? "Linux" : "";
  const br = /Edg\//.test(ua) ? "Edge" : /Chrome\//.test(ua) ? "Chrome" : /Firefox\//.test(ua) ? "Firefox" : /Safari\//.test(ua) ? "Safari" : "";
  return [br, os].filter(Boolean).join(" en ") || "Dispositivo desconocido";
}

/** Route #cuenta: password, two-step verification and signed-in devices. */
export function Account({ me }: { me: Me & { twoStep?: boolean } }) {
  if (me.kind !== "member") return <Page title="Mi cuenta"><Notice tone="warn">Entraste con una llave de API. La seguridad de la cuenta es para personas con sesión iniciada.</Notice></Page>;
  return (
    <Page title="Mi cuenta" sub={`${me.name || me.email} · ${me.email ?? ""}`}>
      <div className="grid max-w-3xl gap-5">
        <TwoStep enabled={!!me.twoStep} />
        <Password />
        <Sessions />
      </div>
    </Page>
  );
}

function TwoStep({ enabled }: { enabled: boolean }) {
  const qc = useQueryClient();
  const [setup, setSetup] = useState<{ secret: string; uri: string; qr: string } | null>(null);
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[] | null>(null);
  const [password, setPassword] = useState("");
  const [off, setOff] = useState(false);
  const done = () => void qc.invalidateQueries({ queryKey: ["me"] });
  const begin = useMutation({
    mutationFn: async () => { const b = await accountApi.begin(); return { ...b, qr: await QRCode.toDataURL(b.uri, { margin: 1, width: 220 }) }; },
    onSuccess: setSetup, onError: (e) => toast.error(errorMessage(e)),
  });
  const confirm = useMutation({ mutationFn: () => accountApi.confirm(code), onSuccess: (r) => { setCodes(r.recoveryCodes); setSetup(null); setCode(""); done(); }, onError: (e) => toast.error(errorMessage(e)) });
  const disable = useMutation({ mutationFn: () => accountApi.disable(password), onSuccess: () => { toast.success("Verificación en dos pasos desactivada."); setOff(false); setPassword(""); done(); }, onError: (e) => toast.error(errorMessage(e)) });
  return (
    <Card>
      <CardHeader><CardTitle className="flex items-center gap-2 text-base"><ShieldCheck className="size-4" aria-hidden="true" /> Verificación en dos pasos {enabled ? <Badge variant="ok">Activa</Badge> : <Badge variant="outline">Inactiva</Badge>}</CardTitle>
        <CardDescription>Además de tu contraseña, pide un código de tu teléfono al entrar. Aunque alguien robe tu contraseña, no puede entrar a la tienda.</CardDescription></CardHeader>
      <CardContent className="grid gap-4">
        {codes && (
          <Notice tone="warn">
            <strong>Guarda estos códigos de respaldo.</strong> Cada uno sirve una vez para entrar si pierdes tu teléfono. No se vuelven a mostrar.
            <div className="mt-2 grid grid-cols-2 gap-1 font-mono text-sm sm:grid-cols-4" data-testid="recovery-codes">{codes.map((c) => <span key={c}>{c}</span>)}</div>
            <Button size="sm" variant="outline" className="mt-3" onClick={() => void navigator.clipboard.writeText(codes.join("\n")).then(() => toast.success("Códigos copiados."))}><Copy aria-hidden="true" /> Copiar</Button>
          </Notice>
        )}
        {!enabled && !setup && <div><Button onClick={() => begin.mutate()} disabled={begin.isPending}>Activar verificación en dos pasos</Button></div>}
        {setup && (
          <form className="grid gap-4 sm:grid-cols-[220px_1fr]" onSubmit={(e: FormEvent) => { e.preventDefault(); confirm.mutate(); }}>
            <img src={setup.qr} width={220} height={220} alt="Código QR para tu app de verificación" className="rounded-md border bg-white" />
            <div className="grid content-start gap-3 text-sm">
              <p><strong>1.</strong> Abre Google Authenticator, Authy, 1Password o Microsoft Authenticator y escanea el código.</p>
              <p className="text-muted-foreground">¿No puedes escanear? Escribe esta clave: <code className="break-all font-mono" data-testid="totp-secret">{setup.secret}</code></p>
              <p><strong>2.</strong> Escribe el código de 6 dígitos que muestra la app.</p>
              <div className="flex flex-wrap items-end gap-2">
                <div className="grid gap-1.5"><Label htmlFor="tf-code">Código</Label><Input id="tf-code" className="w-36 font-mono text-lg tracking-widest" inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} /></div>
                <Button type="submit" disabled={confirm.isPending || code.length !== 6}>Confirmar</Button>
                <Button type="button" variant="outline" onClick={() => setSetup(null)}>Cancelar</Button>
              </div>
            </div>
          </form>
        )}
        {enabled && !off && <div><Button variant="outline" onClick={() => setOff(true)}>Desactivar</Button></div>}
        {enabled && off && (
          <form className="flex flex-wrap items-end gap-2" onSubmit={(e: FormEvent) => { e.preventDefault(); disable.mutate(); }}>
            <div className="grid gap-1.5"><Label htmlFor="tf-pass">Confirma con tu contraseña</Label><Input id="tf-pass" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} /></div>
            <Button type="submit" variant="outline" className="text-destructive" disabled={disable.isPending || !password}>Desactivar</Button>
            <Button type="button" variant="ghost" onClick={() => setOff(false)}>Cancelar</Button>
          </form>
        )}
      </CardContent>
    </Card>
  );
}

function Password() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const [error, setError] = useState<string | null>(null);
  const save = useMutation({
    mutationFn: () => accountApi.password(current, next),
    onSuccess: () => { toast.success("Contraseña cambiada. Vuelve a entrar en tus otros dispositivos."); setCurrent(""); setNext(""); setAgain(""); },
    onError: (e) => setError(e instanceof ApiError && e.status === 403 ? "Tu contraseña actual no es correcta." : errorMessage(e)),
  });
  function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (next.length < 12) return setError("La contraseña nueva debe tener al menos 12 caracteres.");
    if (next !== again) return setError("Las dos contraseñas nuevas no coinciden.");
    save.mutate();
  }
  return (
    <Card>
      <CardHeader><CardTitle className="text-base">Contraseña</CardTitle><CardDescription>Al cambiarla se cierran tus sesiones en otros dispositivos.</CardDescription></CardHeader>
      <CardContent>
        <form className="grid gap-4 sm:grid-cols-3" onSubmit={submit}>
          <div className="grid gap-1.5"><Label htmlFor="pw-cur">Contraseña actual</Label><Input id="pw-cur" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} /></div>
          <div className="grid gap-1.5"><Label htmlFor="pw-new">Contraseña nueva</Label><Input id="pw-new" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} /></div>
          <div className="grid gap-1.5"><Label htmlFor="pw-again">Repite la nueva</Label><Input id="pw-again" type="password" autoComplete="new-password" value={again} onChange={(e) => setAgain(e.target.value)} /></div>
          {error && <p className="text-sm text-bad sm:col-span-3" role="alert">{error}</p>}
          <div className="sm:col-span-3"><Button type="submit" disabled={save.isPending || !current || !next}>Cambiar contraseña</Button></div>
        </form>
      </CardContent>
    </Card>
  );
}

function Sessions() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["my-sessions"], queryFn: accountApi.sessions });
  const refresh = () => void qc.invalidateQueries({ queryKey: ["my-sessions"] });
  const revoke = useMutation({ mutationFn: (id: string) => accountApi.revoke(id), onSuccess: () => { toast.success("Sesión cerrada."); refresh(); }, onError: (e) => toast.error(errorMessage(e)) });
  const others = useMutation({ mutationFn: accountApi.revokeOthers, onSuccess: (r) => { toast.success(r.revoked ? `Se cerraron ${r.revoked} sesiones.` : "No había otras sesiones."); refresh(); }, onError: (e) => toast.error(errorMessage(e)) });
  const items = q.data ?? [];
  return (
    <Card>
      <CardHeader><CardTitle className="text-base">Dónde tienes sesión abierta</CardTitle><CardDescription>Si no reconoces un dispositivo, ciérralo y cambia tu contraseña.</CardDescription></CardHeader>
      <CardContent className="grid gap-3">
        <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
        <ul className="grid divide-y rounded-md border">
          {items.map((s) => (
            <li key={s.id} className="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
              <span className="flex items-center gap-3"><Monitor className="size-4 text-muted-foreground" aria-hidden="true" />
                <span className="grid"><span className="text-sm font-medium">{device(s.userAgent)} {s.current && <Badge variant="ok">Este dispositivo</Badge>}</span><span className="text-xs text-muted-foreground">{s.ip || "IP desconocida"} · activa {dateTime(s.lastSeenAt)} · inició {dateTime(s.createdAt)}</span></span></span>
              {!s.current && <Button size="sm" variant="outline" disabled={revoke.isPending} onClick={() => revoke.mutate(s.id)}>Cerrar sesión</Button>}
            </li>
          ))}
        </ul>
        {items.length > 1 && <div><Button variant="outline" disabled={others.isPending} onClick={() => others.mutate()}>Cerrar todas las demás</Button></div>}
      </CardContent>
    </Card>
  );
}

/** Shown before login for #invitacion/<token>. */
export function AcceptInvite({ token, onDone }: { token: string; onDone: (store: string) => void }) {
  const q = useQuery({ queryKey: ["invite", token], queryFn: () => accountApi.inviteInfo(token), retry: false });
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  useEffect(() => { if (q.data) setName(q.data.name); }, [q.data]);
  const accept = useMutation({
    mutationFn: () => accountApi.accept(token, name.trim(), password),
    onSuccess: (r) => { toast.success("Cuenta creada. Entra con tu correo y tu contraseña."); onDone(r.store); },
    onError: (e) => setError(fieldErrors(e).password ?? errorMessage(e)),
  });
  return (
    <main className="grid min-h-full place-items-center p-4">
      <Card className="w-full max-w-md">
        <CardHeader><CardTitle>{q.data ? `Únete al equipo de ${q.data.storeName}` : "Invitación"}</CardTitle><CardDescription>{q.data ? `Crea tu contraseña para ${q.data.email}.` : q.error ? "" : "Revisando la invitación…"}</CardDescription></CardHeader>
        <CardContent>
          {q.error ? <div className="grid gap-3"><Notice tone="bad">{errorMessage(q.error)}</Notice><Button variant="outline" onClick={() => onDone("")}>Ir a iniciar sesión</Button></div> : q.data && (
            <form className="grid gap-4" onSubmit={(e: FormEvent) => { e.preventDefault(); setError(null); if (password.length < 12) return setError("La contraseña debe tener al menos 12 caracteres."); accept.mutate(); }}>
              <div className="grid gap-1.5"><Label htmlFor="iv-name">Tu nombre</Label><Input id="iv-name" autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} required /></div>
              <div className="grid gap-1.5"><Label htmlFor="iv-pass">Crea una contraseña</Label><Input id="iv-pass" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} required /><p className="text-xs text-muted-foreground">Al menos 12 caracteres. Una frase larga es más segura y fácil de recordar.</p></div>
              {error && <p className="text-sm text-bad" role="alert">{error}</p>}
              <Button type="submit" disabled={accept.isPending}>{accept.isPending ? "Creando tu cuenta…" : "Crear mi cuenta"}</Button>
            </form>
          )}
        </CardContent>
      </Card>
    </main>
  );
}

/** Invitations by email and custom roles, shown under the team table. */
export function TeamAccess({ isAdmin }: { isAdmin: boolean }) {
  const qc = useQueryClient();
  const inv = useQuery({ queryKey: ["invitations"], queryFn: accountApi.invitations });
  const roles = useQuery({ queryKey: ["roles"], queryFn: accountApi.roles });
  const [open, setOpen] = useState(false);
  const revoke = useMutation({ mutationFn: (id: string) => accountApi.revokeInvite(id), onSuccess: () => { toast.success("Invitación cancelada."); void qc.invalidateQueries({ queryKey: ["invitations"] }); }, onError: (e) => toast.error(errorMessage(e)) });
  const delRole = useMutation({ mutationFn: (id: string) => accountApi.deleteRole(id), onSuccess: () => { toast.success("Rol eliminado."); void qc.invalidateQueries({ queryKey: ["roles"] }); }, onError: (e) => toast.error(errorMessage(e)) });
  const pending = inv.data ?? [];
  return (
    <div className="grid gap-5">
      <Card>
        <CardHeader className="flex-row flex-wrap items-start justify-between gap-3"><div className="grid gap-1.5"><CardTitle className="text-base">Invitaciones por correo</CardTitle><CardDescription>La persona recibe un enlace y crea su propia contraseña: nunca tienes que conocerla ni compartirla.</CardDescription></div>
          {isAdmin && <Button onClick={() => setOpen(true)}><MailPlus aria-hidden="true" /> Invitar por correo</Button>}</CardHeader>
        <CardContent>
          {pending.length === 0 ? <p className="text-sm text-muted-foreground">No hay invitaciones pendientes.</p> : (
            <ul className="grid divide-y rounded-md border">{pending.map((i) => (
              <li key={i.id} className="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
                <span className="grid"><span className="text-sm font-medium">{i.name || i.email}</span><span className="text-xs text-muted-foreground">{i.email} · enviada {dateTime(i.createdAt)} · {i.expired ? "vencida" : `vence ${dateTime(i.expiresAt)}`}</span></span>
                {isAdmin && <Button size="sm" variant="outline" disabled={revoke.isPending} onClick={() => revoke.mutate(i.id)}>Cancelar</Button>}
              </li>
            ))}</ul>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle className="text-base">Roles de tu tienda</CardTitle><CardDescription>Guarda combinaciones de permisos con nombre (por ejemplo “Atención a clientes”) para asignarlas con un clic. Se crean al invitar: elige permisos y marca “Guardar como rol”.</CardDescription></CardHeader>
        <CardContent>
          {(roles.data ?? []).length === 0 ? <p className="text-sm text-muted-foreground">Todavía no guardas roles propios. Los roles básicos (Admin, Catálogo, Pedidos, Solo lectura) siempre están disponibles.</p> : (
            <ul className="grid divide-y rounded-md border">{(roles.data ?? []).map((r) => (
              <li key={r.id} className="flex flex-wrap items-center justify-between gap-3 px-4 py-3"><span className="grid"><span className="text-sm font-medium">{r.name}</span><span className="text-xs text-muted-foreground">{Object.entries(r.permissions).map(([m, l]) => `${m}: ${l}`).join(" · ") || "Sin permisos"}</span></span>
                {isAdmin && <Button size="icon" variant="ghost" aria-label={`Eliminar rol ${r.name}`} disabled={delRole.isPending} onClick={() => delRole.mutate(r.id)}><Trash2 aria-hidden="true" /></Button>}</li>
            ))}</ul>
          )}
        </CardContent>
      </Card>
      {open && <InviteDialog roles={roles.data ?? []} onClose={() => setOpen(false)} />}
    </div>
  );
}

function InviteDialog({ roles, onClose }: { roles: Role[]; onClose: () => void }) {
  const qc = useQueryClient();
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState("preset:" + (PRESETS[1]?.id ?? "admin"));
  const [result, setResult] = useState<{ link: string; emailed: boolean } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const perms = (): Permissions => role.startsWith("role:") ? roles.find((r) => "role:" + r.id === role)?.permissions ?? {} : PRESETS.find((p) => "preset:" + p.id === role)?.perms ?? {};
  const send = useMutation({
    mutationFn: () => accountApi.invite({ email: email.trim(), name: name.trim(), permissions: compact(perms()) }),
    onSuccess: (r) => { setResult(r); void qc.invalidateQueries({ queryKey: ["invitations"] }); },
    onError: (e) => setError(errorMessage(e)),
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        {result ? (
          <>
            <DialogHeader><DialogTitle>Invitación enviada</DialogTitle><DialogDescription>{result.emailed ? `Le enviamos el enlace a ${email.trim()}.` : "No se pudo enviar el correo; comparte tú el enlace."} Vence en 7 días y funciona una sola vez.</DialogDescription></DialogHeader>
            <div className="flex items-center gap-2"><Input readOnly value={result.link} aria-label="Enlace de invitación" className="font-mono text-xs" data-testid="invite-link" /><Button variant="outline" onClick={() => void navigator.clipboard.writeText(result.link).then(() => toast.success("Enlace copiado."))}><Copy aria-hidden="true" /> Copiar</Button></div>
            <DialogFooter><Button onClick={onClose}>Listo</Button></DialogFooter>
          </>
        ) : (
          <>
            <DialogHeader><DialogTitle>Invitar por correo</DialogTitle><DialogDescription>Elige qué podrá hacer. Puedes afinar sus permisos después con “Editar acceso”.</DialogDescription></DialogHeader>
            <form id="invite-form" className="grid gap-4" onSubmit={(e: FormEvent) => { e.preventDefault(); setError(null); send.mutate(); }}>
              <div className="grid gap-1.5"><Label htmlFor="in-email">Correo</Label><Input id="in-email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} /></div>
              <div className="grid gap-1.5"><Label htmlFor="in-name">Nombre</Label><Input id="in-name" value={name} onChange={(e) => setName(e.target.value)} /></div>
              <div className="grid gap-1.5"><span className="text-sm font-medium">Rol</span>
                <Select value={role} onValueChange={setRole}><SelectTrigger aria-label="Rol"><SelectValue /></SelectTrigger>
                  <SelectContent>{PRESETS.map((p) => <SelectItem key={p.id} value={"preset:" + p.id}>{p.label}</SelectItem>)}{roles.map((r) => <SelectItem key={r.id} value={"role:" + r.id}>{r.name} (rol de tu tienda)</SelectItem>)}</SelectContent></Select>
              </div>
              {error && <p className="text-sm text-bad" role="alert">{error}</p>}
            </form>
            <DialogFooter><Button variant="outline" onClick={onClose}>Cancelar</Button><Button type="submit" form="invite-form" disabled={send.isPending}>{send.isPending ? "Enviando…" : "Enviar invitación"}</Button></DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
