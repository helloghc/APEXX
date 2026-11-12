import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Copy, Plus } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { Loading, Section, SubPage } from "./kit";

interface App { id: string; name: string; developer: string; url: string; adminPage?: string; orderBlock?: string; permissions: Record<string, string>; createdAt: string; lastUsedAt?: string; revokedAt?: string }
const MODULE: Record<string, string> = { catalog: "Catálogo", pricing: "Precios", inventory: "Inventario", promotions: "Promociones", orders: "Pedidos", customers: "Clientes", payments: "Pagos", builder: "Diseño de la tienda", settings: "Configuración", reports: "Reportes", marketplace: "Marketplace" };
const LEVEL: Record<string, string> = { none: "Sin acceso", read: "Ver", edit: "Editar", admin: "Administrar" };
const fmt = (d?: string) => (d ? new Date(d).toLocaleString("es-MX", { dateStyle: "medium", timeStyle: "short" }) : "");

/** Apps: third parties with their own token and permissions per module. */
export function Apps({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "team", "admin");
  const q = useQuery({ queryKey: ["apps"], queryFn: () => request<{ items: App[]; modules: string[] }>("GET", "/v1/apps") });
  const [draft, setDraft] = useState<{ name: string; developer: string; url: string; permissions: Record<string, string> } | null>(null);
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [token, setToken] = useState<{ name: string; token: string } | null>(null);
  const [busy, setBusy] = useState("");
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const modules = q.data.modules.filter((m) => m !== "team");
  const refresh = () => void qc.invalidateQueries({ queryKey: ["apps"] });
  async function create() {
    if (!draft) return;
    setBusy("new"); setErrs({});
    try {
      const r = await request<{ app: App; token: string }>("POST", "/v1/apps", draft);
      setToken({ name: r.app.name, token: r.token }); setDraft(null); refresh();
    } catch (e) {
      setErrs(fieldErrors(e)); toast.error(errorMessage(e));
    } finally {
      setBusy("");
    }
  }
  async function setPerm(a: App, m: string, l: string) {
    setBusy(a.id);
    try {
      await request("PATCH", `/v1/apps/${a.id}`, { permissions: { ...a.permissions, [m]: l } });
      toast.success(`Permisos de ${a.name} actualizados. Aplican desde su siguiente solicitud.`); refresh();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy("");
    }
  }
  async function revoke(a: App) {
    if (!window.confirm(`¿Revocar ${a.name}? Deja de funcionar de inmediato y no se puede deshacer.`)) return;
    setBusy(a.id);
    try {
      await request("DELETE", `/v1/apps/${a.id}`, undefined, { as: "none" });
      toast.success(`${a.name} revocada: ya no tiene acceso.`); refresh();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy("");
    }
  }
  const PermGrid = ({ value, onChange, idp, disabled }: { value: Record<string, string>; onChange: (m: string, l: string) => void; idp: string; disabled?: boolean }) => (
    <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
      {modules.map((m) => (
        <div key={m} className="grid gap-1">
          <Label htmlFor={`${idp}-${m}`}>{MODULE[m] ?? m}</Label>
          <select id={`${idp}-${m}`} className="h-9 rounded-md border bg-background px-2 text-sm" disabled={disabled} value={value[m] ?? "none"} onChange={(e) => onChange(m, e.target.value)}>
            {Object.entries(LEVEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
          </select>
        </div>
      ))}
    </div>
  );
  const active = q.data.items.filter((a) => !a.revokedAt), revoked = q.data.items.filter((a) => a.revokedAt);
  return (
    <SubPage title="Apps" sub="Conecta sistemas de terceros (ERP, facturación, marketplaces) con su propia llave y solo los permisos que necesitan. Una app nunca administra tu equipo, y revocarla corta su acceso de inmediato."
      actions={canEdit && !draft && <Button onClick={() => setDraft({ name: "", developer: "", url: "", permissions: {} })}><Plus className="size-4" /> Instalar app</Button>}>
      {token && (
        <Section title={`Llave de ${token.name}`} description="Cópiala ahora: no la volveremos a mostrar. Si la pierdes, revoca la app e instálala de nuevo.">
          <div className="flex flex-wrap items-center gap-2" data-app-token>
            <code className="min-w-0 flex-1 break-all rounded-md bg-muted px-3 py-2 text-sm">{token.token}</code>
            <Button variant="outline" onClick={() => { void navigator.clipboard?.writeText(token.token); toast.success("Llave copiada."); }}><Copy className="size-4" /> Copiar</Button>
            <Button variant="ghost" onClick={() => setToken(null)}>Listo</Button>
          </div>
        </Section>
      )}
      {draft && (
        <Section title="Nueva app" description="Elige solo lo que la app necesita. Puedes cambiarlo después.">
          <div className="grid gap-3 sm:grid-cols-3">
            <div className="grid gap-1.5"><Label htmlFor="app-name">Nombre</Label><Input id="app-name" value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} placeholder="Sincronizador ERP" />{errs.name && <p className="text-xs text-bad">{errs.name}</p>}</div>
            <div className="grid gap-1.5"><Label htmlFor="app-dev">Desarrollador (opcional)</Label><Input id="app-dev" value={draft.developer} onChange={(e) => setDraft({ ...draft, developer: e.target.value })} /></div>
            <div className="grid gap-1.5"><Label htmlFor="app-url">Sitio (opcional)</Label><Input id="app-url" value={draft.url} onChange={(e) => setDraft({ ...draft, url: e.target.value })} placeholder="https://" />{errs.url && <p className="text-xs text-bad">{errs.url}</p>}</div>
          </div>
          <PermGrid idp="new" value={draft.permissions} onChange={(m, l) => setDraft({ ...draft, permissions: { ...draft.permissions, [m]: l } })} />
          {errs.permissions && <p className="text-sm text-bad">{errs.permissions}</p>}
          <div className="flex gap-2"><Button disabled={busy === "new"} onClick={() => void create()}>{busy === "new" ? "Instalando…" : "Instalar y ver la llave"}</Button><Button variant="ghost" onClick={() => setDraft(null)}>Cancelar</Button></div>
        </Section>
      )}
      {active.length === 0 && !draft && <p className="text-sm text-muted-foreground">No hay apps instaladas.</p>}
      {active.map((a) => (
        <Section key={a.id} title={a.name} description={`${a.developer ? a.developer + " · " : ""}Instalada ${fmt(a.createdAt)}${a.lastUsedAt ? " · último uso " + fmt(a.lastUsedAt) : " · sin uso todavía"}`}
          actions={canEdit && <Button variant="outline" size="sm" disabled={busy === a.id} onClick={() => void revoke(a)}>Revocar</Button>}>
          <PermGrid idp={a.id} value={a.permissions} disabled={!canEdit || busy === a.id} onChange={(m, l) => void setPerm(a, m, l)} />
          <Extensions app={a} canEdit={canEdit} onSaved={refresh} />
        </Section>
      ))}
      {revoked.length > 0 && <p className="text-sm text-muted-foreground">Revocadas: {revoked.map((a) => `${a.name} (${fmt(a.revokedAt)})`).join(", ")}.</p>}
    </SubPage>
  );
}

/** Where the app shows itself in the panel, and the secret it checks sessions with. */
function Extensions({ app, canEdit, onSaved }: { app: App; canEdit: boolean; onSaved: () => void }) {
  const [page, setPage] = useState(app.adminPage ?? "");
  const [block, setBlock] = useState(app.orderBlock ?? "");
  const [secret, setSecret] = useState("");
  const dirty = page !== (app.adminPage ?? "") || block !== (app.orderBlock ?? "");
  async function save() {
    try { await request("PUT", `/v1/apps/${app.id}/extensions`, { adminPage: page, orderBlock: block }); toast.success("Vistas de la app guardadas."); onSaved(); } catch (e) { toast.error(errorMessage(e)); }
  }
  async function reveal() {
    try { setSecret((await request<{ secret: string }>("GET", `/v1/apps/${app.id}/extension-secret`)).secret); } catch (e) { toast.error(errorMessage(e)); }
  }
  return (
    <details className="rounded-md border p-3 text-sm" data-app-ext>
      <summary className="cursor-pointer font-medium">Vistas en el panel</summary>
      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        <div className="grid gap-1.5"><Label htmlFor={`ap-p-${app.id}`}>Página propia (https)</Label><Input id={`ap-p-${app.id}`} disabled={!canEdit} value={page} onChange={(e) => setPage(e.target.value)} placeholder="https://" /></div>
        <div className="grid gap-1.5"><Label htmlFor={`ap-b-${app.id}`}>Bloque en cada pedido (https)</Label><Input id={`ap-b-${app.id}`} disabled={!canEdit} value={block} onChange={(e) => setBlock(e.target.value)} placeholder="https://" /></div>
      </div>
      <p className="mt-2 text-xs text-muted-foreground">Se cargan aisladas (sin acceso al panel) con una sesión firmada que vence en 5 minutos e indica tienda, persona y pedido.</p>
      {canEdit && <div className="mt-2 flex flex-wrap items-center gap-2"><Button size="sm" disabled={!dirty} onClick={() => void save()}>Guardar vistas</Button><Button size="sm" variant="ghost" onClick={() => void reveal()}>Ver secreto de sesiones</Button>{secret && <code className="break-all rounded bg-muted px-2 py-1 text-xs">{secret}</code>}</div>}
    </details>
  );
}
