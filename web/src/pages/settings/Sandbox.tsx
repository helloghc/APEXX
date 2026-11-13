import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { CalendarClock, Copy, ExternalLink, History, KeyRound, Pencil, RefreshCw, Rocket, Trash2 } from "lucide-react";
import { ApiError, errorMessage, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { href } from "@/router";
import { ErrorBox, Notice } from "../common";
import { Confirm, Loading, Section, SubPage } from "./kit";

interface SandboxState {
  id: string;
  name: string;
  hasPassword: boolean;
  status: "copying" | "ready" | "failed" | "deleting";
  dataMode: "none" | "demo";
  progress?: { done?: number; total?: number; table?: string; ms?: number };
  error?: string;
  createdAt: string;
  copiedAt?: string;
  copyMs: number;
  lastUsedAt: string;
}
interface View {
  sandbox: SandboxState;
  url?: string;
  domains: string[];
  expiresAt?: string;
}
interface Plan {
  plan: string;
  sandboxes: number;
  sandboxMaxProducts: number;
  sandboxDemoData: boolean;
  sandboxSchedule: boolean;
}
interface List {
  env: "production" | "sandbox";
  parent?: string;
  current: string;
  items: View[];
  plan: Plan;
  used: number;
  areas: { id: string; label: string }[];
}
interface RowChange { table: string; pk: string; action: "insert" | "update" | "delete"; conflict: boolean; label?: string }
interface AreaDiff { area: string; label: string; inserts: number; updates: number; deletes: number; conflicts: number; sample: RowChange[] }
interface Diff { areas: AreaDiff[]; tags?: string[] }
interface Publish { id: string; sandboxId: string; areas: string[]; publishedBy: string; publishedAt: string; revertedAt?: string; revertedBy?: string; rows: number }
interface Schedule {
  id: string;
  sandboxId: string;
  sandbox: string;
  areas: string[];
  runAt: string;
  status: "pending" | "running" | "done" | "failed" | "canceled";
  result?: string;
  createdBy: string;
}

const ACTION: Record<RowChange["action"], string> = { insert: "Nuevo", update: "Cambia", delete: "Se borra" };
const SCHEDULE_STATUS: Record<Schedule["status"], string> = { pending: "Programada", running: "Publicando", done: "Publicada", failed: "No se publicó", canceled: "Cancelada" };
const when = (iso?: string) => (iso ? new Date(iso).toLocaleString("es-MX", { dateStyle: "medium", timeStyle: "short" }) : "");
const base = (id: string) => `/v1/sandboxes/${encodeURIComponent(id)}`;
const num = (n: number) => n.toLocaleString("es-MX");

/** Sandbox: complete copies of the store to try anything, and publish to production what is ready. */
export function Sandbox({ me }: { me: Me }) {
  const qc = useQueryClient();
  const admin = can(me, "settings", "admin");
  const q = useQuery({
    queryKey: ["sandbox"],
    queryFn: () => request<List>("GET", "/v1/sandboxes"),
    refetchInterval: (query) => (query.state.data?.items.some((v) => v.sandbox.status === "copying") ? 1000 : false),
  });
  const [picked, setPicked] = useState("");
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const l = q.data;
  const inSandbox = l.env === "sandbox";
  const selectedId = inSandbox ? l.current : l.items.some((v) => v.sandbox.id === picked) ? picked : (l.items[0]?.sandbox.id ?? "");
  const selected = l.items.find((v) => v.sandbox.id === selectedId);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["sandbox"] });
  const labels = (ids: string[]) => ids.map((a) => l.areas.find((x) => x.id === a)?.label ?? a).join(", ");

  return (
    <SubPage
      title="Sandbox"
      sub="Copias completas de tu tienda para probar lo que quieras: productos, precios, promociones, diseño, configuración y etiquetas. Nada llega a tu tienda real hasta que lo publiques, y cada publicación se puede revertir."
    >
      <PlanLine plan={l.plan} used={l.used} />

      {l.items.length > 1 && (
        <Section title="Tus sandboxes" description={inSandbox ? "Puedes pasar a otro sandbox de tu tienda sin volver a producción." : "Elige con cuál trabajar."}>
          <SandboxPicker items={l.items} selected={selectedId} current={l.current} onPick={setPicked} />
        </Section>
      )}

      {selected && <SandboxDetail key={selected.sandbox.id} v={selected} inSandbox={inSandbox} here={l.current === selected.sandbox.id} admin={admin} plan={l.plan} areas={l.areas} onChange={refresh} />}

      {!inSandbox && admin && l.used < l.plan.sandboxes && <CreateSandbox plan={l.plan} first={l.items.length === 0} onCreated={(id) => { setPicked(id); refresh(); }} />}
      {!inSandbox && !admin && l.items.length === 0 && <p className="text-sm text-muted-foreground">Pide a un administrador que cree el sandbox.</p>}

      {l.items.length > 0 && <Schedules admin={admin} labels={labels} />}
      {l.items.length > 0 && <PublishHistory admin={admin} labels={labels} names={new Map(l.items.map((v) => [v.sandbox.id, v.sandbox.name]))} />}
    </SubPage>
  );
}

/** What the store's plan includes, in one line. */
function PlanLine({ plan, used }: { plan: Plan; used: number }) {
  if (plan.sandboxes === 0) return <Notice tone="warn">Tu plan no incluye sandbox. Pide que lo agreguen a tu plan.</Notice>;
  const parts = [
    plan.plan ? `Plan ${plan.plan}:` : "",
    plan.sandboxes === 1 ? "incluye 1 sandbox;" : `incluye ${plan.sandboxes} sandboxes;`,
    `usas ${used}.`,
    plan.sandboxMaxProducts > 0 ? `Cada uno admite hasta ${num(plan.sandboxMaxProducts)} productos.` : "",
    plan.sandboxDemoData ? "" : "Sin datos de prueba.",
    plan.sandboxSchedule ? "" : "Sin publicaciones programadas.",
  ].filter(Boolean);
  // One text per phrase: each one is translated on its own.
  return (
    <p className="flex flex-wrap gap-x-1 text-sm text-muted-foreground" data-plan>
      {parts.map((x) => (
        <span key={x}>{x}</span>
      ))}
    </p>
  );
}

function SandboxPicker({ items, selected, current, onPick }: { items: View[]; selected: string; current: string; onPick: (id: string) => void }) {
  const qc = useQueryClient();
  const [busy, setBusy] = useState("");
  async function enter(id: string) {
    setBusy(id);
    try {
      await request("POST", `${base(id)}/enter`);
      window.location.hash = "#inicio";
      window.location.reload();
    } catch (e) {
      toast.error(errorMessage(e));
      setBusy("");
      void qc.invalidateQueries({ queryKey: ["sandbox"] });
    }
  }
  return (
    <ul className="grid gap-2 sm:grid-cols-2">
      {items.map((v) => {
        const sb = v.sandbox;
        const isHere = current === sb.id;
        return (
          <li key={sb.id} className={`flex flex-wrap items-center justify-between gap-2 rounded-md border p-3 ${sb.id === selected ? "border-primary" : ""}`}>
            <span className="grid">
              <span className="font-medium">{sb.name}</span>
              <span className="text-xs text-muted-foreground">{isHere ? "Estás aquí" : sb.status === "ready" ? `Copiado el ${when(sb.copiedAt)}` : sb.status === "copying" ? "Copiando…" : sb.status === "failed" ? "La copia no terminó" : "Borrando…"}</span>
            </span>
            {current ? (
              !isHere && sb.status === "ready" && (
                <Button size="sm" variant="outline" disabled={busy !== ""} onClick={() => void enter(sb.id)}>
                  {busy === sb.id ? "Entrando…" : `Pasar a ${sb.name}`}
                </Button>
              )
            ) : (
              <Button size="sm" variant={sb.id === selected ? "default" : "outline"} aria-pressed={sb.id === selected} onClick={() => onPick(sb.id)}>
                {sb.id === selected ? "Seleccionado" : `Ver ${sb.name}`}
              </Button>
            )}
          </li>
        );
      })}
    </ul>
  );
}

function CreateSandbox({ plan, first, onCreated }: { plan: Plan; first: boolean; onCreated: (id: string) => void }) {
  const [name, setName] = useState(first ? "Sandbox" : "");
  const [dataMode, setDataMode] = useState<"none" | "demo">("none");
  const [busy, setBusy] = useState(false);
  async function create() {
    setBusy(true);
    try {
      const r = await request<{ sandbox: SandboxState }>("POST", "/v1/sandboxes", { name: name.trim(), dataMode });
      toast.success("Copiando tu tienda al sandbox…");
      setName("");
      onCreated(r.sandbox.id);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Section
      title={first ? "Crea tu sandbox" : "Crear otro sandbox"}
      description="Se copia todo lo de tu tienda en segundos. No se copian los datos personales de tus clientes, los pedidos reales ni las credenciales (pagos, llaves, conexiones): en el sandbox se pone lo de prueba."
    >
      <form
        className="grid gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          void create();
        }}
      >
        <div className="grid max-w-sm gap-1.5">
          <Label htmlFor="sbx-name">Nombre</Label>
          <Input id="sbx-name" required maxLength={60} value={name} placeholder="Por ejemplo: Black Friday" onChange={(e) => setName(e.target.value)} />
        </div>
        <fieldset className="grid gap-2">
          <legend className="text-sm font-medium">Clientes y pedidos en el sandbox</legend>
          <label className="flex items-start gap-2 text-sm">
            <input type="radio" name="dm" checked={dataMode === "none"} onChange={() => setDataMode("none")} className="mt-1" />
            <span>Sin clientes ni pedidos (recomendado). Ningún dato personal sale de producción.</span>
          </label>
          <label className={`flex items-start gap-2 text-sm ${plan.sandboxDemoData ? "" : "text-muted-foreground"}`}>
            <input type="radio" name="dm" checked={dataMode === "demo"} disabled={!plan.sandboxDemoData} onChange={() => setDataMode("demo")} className="mt-1" />
            <span>
              Con clientes y pedidos de prueba, generados con tus productos (300 pedidos de 90 días), para probar reportes, segmentos y campañas.
              {!plan.sandboxDemoData && " Tu plan no lo incluye."}
            </span>
          </label>
        </fieldset>
        <div>
          <Button type="submit" disabled={busy || name.trim() === ""}>
            {busy ? "Creando…" : "Crear sandbox"}
          </Button>
        </div>
      </form>
    </Section>
  );
}

function SandboxDetail({ v, inSandbox, here, admin, plan, areas, onChange }: { v: View; inSandbox: boolean; here: boolean; admin: boolean; plan: Plan; areas: { id: string; label: string }[]; onChange: () => void }) {
  const sb = v.sandbox;
  const [busy, setBusy] = useState("");
  const [confirm, setConfirm] = useState<"" | "refresh" | "delete">("");
  const [share, setShare] = useState<{ url: string; expiresAt: string } | null>(null);
  const [hours, setHours] = useState(24);
  const [renaming, setRenaming] = useState(false);
  const [name, setName] = useState(sb.name);

  async function act(what: string, fn: () => Promise<unknown>, ok?: string) {
    setBusy(what);
    try {
      await fn();
      if (ok) toast.success(ok);
      onChange();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy("");
    }
  }
  const enter = () =>
    act("enter", async () => {
      await request("POST", `${base(sb.id)}/enter`);
      window.location.hash = "#inicio";
      window.location.reload();
    });
  const openStore = () =>
    act("open", async () => {
      const r = await request<{ url: string }>("POST", `${base(sb.id)}/preview`, { hours: 12 });
      window.open(r.url, "_blank", "noopener");
    });
  const makeShare = () =>
    act("share", async () => {
      const r = await request<{ url: string; expiresAt: string }>("POST", `${base(sb.id)}/preview`, { hours });
      setShare({ url: r.url.startsWith("http") ? r.url : window.location.origin + r.url, expiresAt: r.expiresAt });
    });
  const rename = () =>
    act(
      "rename",
      async () => {
        await request("PATCH", base(sb.id), { name: name.trim() });
        setRenaming(false);
      },
      "Nombre guardado",
    );

  const title = here ? `Estás en «${sb.name}»` : sb.status === "ready" ? `«${sb.name}» está listo` : sb.status === "copying" ? `Copiando tu tienda a «${sb.name}»…` : sb.status === "failed" ? `La copia de «${sb.name}» no terminó` : "Borrando…";
  return (
    <>
      <Section
        title={title}
        description={
          sb.status === "copying"
            ? `${sb.progress?.done ?? 0} de ${sb.progress?.total ?? "…"} partes copiadas`
            : sb.status === "failed"
              ? sb.error
              : `Copiado el ${when(sb.copiedAt)} en ${(sb.copyMs / 1000).toFixed(1)} s · ${sb.dataMode === "demo" ? "con clientes y pedidos de prueba" : "sin clientes ni pedidos"}${v.expiresAt ? ` · se borra el ${when(v.expiresAt)} si nadie lo usa` : " · no vence (usa tu propio subdominio)"}`
        }
      >
        {sb.status === "copying" && (
          <div className="h-2 overflow-hidden rounded bg-muted" role="progressbar" aria-label="Avance de la copia" aria-valuemin={0} aria-valuemax={sb.progress?.total ?? 1} aria-valuenow={sb.progress?.done ?? 0}>
            <div className="h-full bg-primary transition-all" style={{ width: `${Math.round(((sb.progress?.done ?? 0) / Math.max(1, sb.progress?.total ?? 1)) * 100)}%` }} />
          </div>
        )}
        {sb.status === "ready" && (
          <div className="flex flex-wrap gap-2">
            {!here && (
              <Button disabled={busy !== ""} onClick={() => void enter()}>
                {busy === "enter" ? "Entrando…" : `Entrar a «${sb.name}»`}
              </Button>
            )}
            <Button variant="outline" disabled={busy !== ""} onClick={() => void openStore()}>
              <ExternalLink className="size-4" /> Ver la tienda del sandbox
            </Button>
          </div>
        )}
        {admin && sb.status !== "copying" && (
          <div className="flex flex-wrap items-end gap-2">
            {renaming ? (
              <form
                className="flex flex-wrap items-end gap-2"
                onSubmit={(e) => {
                  e.preventDefault();
                  void rename();
                }}
              >
                <div className="grid gap-1.5">
                  <Label htmlFor="sbx-rename">Nombre del sandbox</Label>
                  <Input id="sbx-rename" required maxLength={60} value={name} onChange={(e) => setName(e.target.value)} autoFocus />
                </div>
                <Button type="submit" disabled={busy !== "" || name.trim() === ""}>
                  {busy === "rename" ? "Guardando…" : "Guardar"}
                </Button>
                <Button type="button" variant="ghost" onClick={() => { setRenaming(false); setName(sb.name); }}>
                  Cancelar
                </Button>
              </form>
            ) : (
              <Button variant="ghost" disabled={busy !== ""} onClick={() => setRenaming(true)}>
                <Pencil className="size-4" /> Cambiar nombre
              </Button>
            )}
            {!inSandbox && !renaming && (
              <>
                <Button variant="outline" disabled={busy !== ""} onClick={() => setConfirm("refresh")}>
                  <RefreshCw className="size-4" /> Refrescar desde producción
                </Button>
                <Button variant="ghost" disabled={busy !== ""} onClick={() => setConfirm("delete")}>
                  <Trash2 className="size-4" /> Borrar «{sb.name}»
                </Button>
              </>
            )}
          </div>
        )}
      </Section>

      {sb.status === "ready" && (
        <Section title="Dirección del sandbox" description="Tu sandbox es privado: solo lo ve tu equipo con sesión, quien reciba un enlace de vista previa o quien sepa su contraseña, si le pones una. Nunca aparece en buscadores.">
          <div className="grid gap-1 text-sm">
            <span className="text-muted-foreground">Dirección temporal</span>
            <code className="break-all">{v.url}</code>
            {v.domains.length > 0 && (
              <>
                <span className="mt-2 text-muted-foreground">Tus dominios del sandbox</span>
                {v.domains.map((d) => (
                  <code key={d}>{d}</code>
                ))}
              </>
            )}
          </div>
          <p className="text-sm text-muted-foreground">
            ¿Prefieres tu propio subdominio, como pruebas.mitienda.com? {here ? "Agrégalo en " : "Entra al sandbox y agrégalo en "}
            <a className="underline" href={href("configuracion", "dominios")}>
              Configuración › Dominios
            </a>
            : te damos el registro DNS, el certificado HTTPS sale solo y ese subdominio queda ligado al sandbox, nunca a tu tienda real.
          </p>
          <div className="flex flex-wrap items-end gap-2">
            <div className="grid gap-1.5">
              <Label htmlFor="sbx-hours">Compartir vista previa por</Label>
              <select id="sbx-hours" className="h-9 rounded-md border bg-background px-2 text-sm" value={hours} onChange={(e) => setHours(Number(e.target.value))}>
                <option value={1}>1 hora</option>
                <option value={24}>1 día</option>
                <option value={72}>3 días</option>
                <option value={168}>7 días</option>
                <option value={336}>14 días</option>
              </select>
            </div>
            <Button variant="outline" disabled={busy !== ""} onClick={() => void makeShare()}>
              Crear enlace
            </Button>
          </div>
          {share && (
            <div className="grid gap-1.5">
              <Label htmlFor="sbx-share">Enlace (vence el {when(share.expiresAt)})</Label>
              <div className="flex gap-2">
                <Input id="sbx-share" readOnly value={share.url} className="font-mono text-xs" onFocus={(e) => e.currentTarget.select()} />
                <Button
                  variant="outline"
                  size="icon"
                  aria-label="Copiar enlace"
                  onClick={() => {
                    navigator.clipboard.writeText(share.url).then(
                      () => toast.success("Enlace copiado"),
                      () => toast.error("Selecciona el enlace y cópialo"),
                    );
                  }}
                >
                  <Copy className="size-4" />
                </Button>
              </div>
            </div>
          )}
        </Section>
      )}

      {sb.status === "ready" && <PasswordPanel id={sb.id} has={sb.hasPassword} admin={admin} onChange={onChange} />}
      {sb.status === "ready" && <PublishPanel id={sb.id} name={sb.name} admin={admin} areas={areas} canSchedule={plan.sandboxSchedule} />}
      {sb.status === "ready" && <CapturedMail id={sb.id} />}

      <Confirm
        open={confirm === "refresh"}
        title={`¿Refrescar «${sb.name}» desde producción?`}
        text="Se vuelve a copiar tu tienda real y se pierde lo que cambiaste en este sandbox y no publicaste. Su dirección, su contraseña y tu equipo se mantienen."
        confirmLabel="Refrescar"
        busy={busy === "refresh"}
        onClose={() => setConfirm("")}
        onConfirm={() => act("refresh", () => request("POST", `${base(sb.id)}/refresh`), "Copiando tu tienda al sandbox…").then(() => setConfirm(""))}
      />
      <Confirm
        open={confirm === "delete"}
        title={`¿Borrar «${sb.name}»?`}
        text="Se borra la copia, todo lo que no publicaste y sus publicaciones programadas. Tu tienda real no cambia, y lo que ya publicaste se queda."
        confirmLabel="Borrar sandbox"
        busy={busy === "delete"}
        onClose={() => setConfirm("")}
        onConfirm={() => act("delete", () => request("DELETE", base(sb.id), undefined, { as: "none" }), "Sandbox borrado").then(() => setConfirm(""))}
      />
    </>
  );
}

/** Optional password for the sandbox's storefront: for people outside the team without a link. */
function PasswordPanel({ id, has, admin, onChange }: { id: string; has: boolean; admin: boolean; onChange: () => void }) {
  const [pw, setPw] = useState("");
  const [busy, setBusy] = useState(false);
  async function save(value: string) {
    setBusy(true);
    try {
      await request("PUT", `${base(id)}/password`, { password: value });
      toast.success(value ? "Contraseña guardada" : "Contraseña quitada");
      setPw("");
      onChange();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Section
      title="Contraseña de la tienda del sandbox"
      description={
        has
          ? "Quien abra la tienda del sandbox sin sesión ni enlace ve un formulario y entra con la contraseña por 12 horas. Si la cambias, quien entró con la anterior tiene que escribir la nueva."
          : "Opcional. Útil para mostrar el sandbox a alguien de fuera (una agencia, un cliente) sin crear enlaces: le das la dirección y la contraseña."
      }
    >
      <p className="flex items-center gap-2 text-sm">
        <KeyRound className="size-4 text-muted-foreground" aria-hidden="true" />
        {has ? "Con contraseña" : "Sin contraseña"}
      </p>
      {admin && (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            void save(pw);
          }}
        >
          <div className="grid gap-1.5">
            <Label htmlFor="sbx-pw">{has ? "Nueva contraseña" : "Contraseña"}</Label>
            <Input id="sbx-pw" type="password" autoComplete="new-password" minLength={8} maxLength={128} required value={pw} onChange={(e) => setPw(e.target.value)} aria-describedby="sbx-pw-hint" />
            <span id="sbx-pw-hint" className="text-xs text-muted-foreground">
              Al menos 8 caracteres.
            </span>
          </div>
          <Button type="submit" disabled={busy || pw.length < 8}>
            {busy ? "Guardando…" : "Guardar contraseña"}
          </Button>
          {has && (
            <Button type="button" variant="ghost" disabled={busy} onClick={() => void save("")}>
              Quitar contraseña
            </Button>
          )}
        </form>
      )}
    </Section>
  );
}

/** A default time to schedule: tomorrow at 9:00, in the browser's time. */
function tomorrowAtNine() {
  const d = new Date();
  d.setDate(d.getDate() + 1);
  d.setHours(9, 0, 0, 0);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

function PublishPanel({ id, name, admin, areas, canSchedule }: { id: string; name: string; admin: boolean; areas: { id: string; label: string }[]; canSchedule: boolean }) {
  const qc = useQueryClient();
  const [chosen, setChosen] = useState<string[]>([]);
  const [force, setForce] = useState(false);
  const [mode, setMode] = useState<"now" | "later">("now");
  const [at, setAt] = useState(tomorrowAtNine);
  const [busy, setBusy] = useState(false);
  const diff = useQuery({ queryKey: ["sandbox", "diff", id], queryFn: () => request<Diff>("GET", `${base(id)}/diff`) });
  if (diff.isPending) return <Loading rows={3} />;
  if (diff.error) return <ErrorBox error={diff.error} />;
  const byArea = new Map(diff.data.areas.map((a) => [a.area, a]));
  const tagChanges = diff.data.tags ?? [];
  const total = (a?: AreaDiff) => (a ? a.inserts + a.updates + a.deletes + a.conflicts : 0);
  const pending = areas.filter((a) => (a.id === "tags" ? tagChanges.length > 0 : total(byArea.get(a.id)) > 0));
  const conflicts = chosen.reduce((n, a) => n + (byArea.get(a)?.conflicts ?? 0), 0);

  async function publish() {
    setBusy(true);
    try {
      if (mode === "later") {
        const runAt = new Date(at);
        if (Number.isNaN(runAt.getTime())) throw new Error("Elige una fecha y hora");
        await request("POST", `${base(id)}/publish`, { areas: chosen, force, runAt: runAt.toISOString() });
        toast.success(`Publicación programada para el ${when(runAt.toISOString())}. Se publicará lo que tenga «${name}» a esa hora.`);
      } else {
        const r = await request<{ publish?: { applied: number; skipped: number } | null; tags?: { version: number } }>("POST", `${base(id)}/publish`, { areas: chosen, force });
        const parts = [];
        if (r.publish) parts.push(`${r.publish.applied} cambios publicados${r.publish.skipped ? `, ${r.publish.skipped} conflictos sin tocar` : ""}`);
        if (r.tags) parts.push(`etiquetas en la versión ${r.tags.version}`);
        toast.success(`Publicado a producción: ${parts.join(" y ")}.`);
      }
      setChosen([]);
      setForce(false);
      void qc.invalidateQueries({ queryKey: ["sandbox"] });
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Section title="Publicar a producción" description="Lo que cambiaste en el sandbox, por área. Si alguien cambió lo mismo en tu tienda real después de copiar, aparece como conflicto y no se toca salvo que lo confirmes.">
      {pending.length === 0 ? (
        <p className="text-sm text-muted-foreground">El sandbox está igual a tu tienda real: no hay nada que publicar.</p>
      ) : (
        <ul className="grid gap-3">
          {pending.map((a) => {
            const d = byArea.get(a.id);
            const on = chosen.includes(a.id);
            return (
              <li key={a.id} className="rounded-md border p-3">
                <label className="flex flex-wrap items-center gap-2 font-medium">
                  <input type="checkbox" checked={on} disabled={!admin} onChange={(e) => setChosen((c) => (e.target.checked ? [...c, a.id] : c.filter((x) => x !== a.id)))} />
                  {a.label}
                  <span className="text-xs font-normal text-muted-foreground">
                    {a.id === "tags"
                      ? `${tagChanges.length} cambios`
                      : [d!.inserts && `${d!.inserts} nuevos`, d!.updates && `${d!.updates} cambiados`, d!.deletes && `${d!.deletes} por borrar`, d!.conflicts && `${d!.conflicts} en conflicto`].filter(Boolean).join(" · ")}
                  </span>
                </label>
                <ul className="mt-2 grid gap-0.5 text-sm text-muted-foreground">
                  {a.id === "tags"
                    ? tagChanges.slice(0, 8).map((c, i) => <li key={i}>{c}</li>)
                    : d!.sample.slice(0, 8).map((c, i) => (
                        <li key={i} className={c.conflict ? "text-bad" : undefined}>
                          {c.conflict ? "Conflicto" : ACTION[c.action]}: {c.label || c.pk}
                        </li>
                      ))}
                </ul>
              </li>
            );
          })}
        </ul>
      )}
      {admin && pending.length > 0 && (
        <div className="grid gap-3">
          {conflicts > 0 && (
            <label className="flex items-start gap-2 text-sm">
              <input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} className="mt-1" />
              <span>Publicar también los {conflicts} conflictos: la versión del sandbox reemplaza la de tu tienda real.</span>
            </label>
          )}
          {canSchedule && (
            <fieldset className="grid gap-2">
              <legend className="text-sm font-medium">Cuándo</legend>
              <label className="flex items-center gap-2 text-sm">
                <input type="radio" name={`when-${id}`} checked={mode === "now"} onChange={() => setMode("now")} />
                Ahora
              </label>
              <label className="flex items-center gap-2 text-sm">
                <input type="radio" name={`when-${id}`} checked={mode === "later"} onChange={() => setMode("later")} />
                En una fecha y hora
              </label>
              {mode === "later" && (
                <div className="grid max-w-xs gap-1.5 pl-6">
                  <Label htmlFor={`at-${id}`}>Fecha y hora (tu hora local)</Label>
                  <Input id={`at-${id}`} type="datetime-local" value={at} onChange={(e) => setAt(e.target.value)} required aria-describedby={`at-hint-${id}`} />
                  <span id={`at-hint-${id}`} className="text-xs text-muted-foreground">
                    Se publica lo que tenga el sandbox a esa hora. Hasta 90 días.
                  </span>
                </div>
              )}
            </fieldset>
          )}
          <div>
            <Button disabled={busy || chosen.length === 0 || (mode === "later" && !at)} onClick={() => void publish()}>
              {mode === "later" ? <CalendarClock className="size-4" /> : <Rocket className="size-4" />}
              {busy ? (mode === "later" ? "Programando…" : "Publicando…") : mode === "later" ? "Programar publicación" : "Publicar a producción"}
            </Button>
          </div>
        </div>
      )}
    </Section>
  );
}

function Schedules({ admin, labels }: { admin: boolean; labels: (ids: string[]) => string }) {
  const qc = useQueryClient();
  const [busy, setBusy] = useState("");
  const list = useQuery({
    queryKey: ["sandbox", "schedules"],
    queryFn: () => request<{ items: Schedule[] }>("GET", "/v1/sandbox/schedules").then((r) => r.items),
    refetchInterval: (query) => (query.state.data?.some((x) => x.status === "pending" || x.status === "running") ? 30_000 : false),
  });
  async function cancel(id: string) {
    setBusy(id);
    try {
      await request("DELETE", `/v1/sandbox/schedules/${encodeURIComponent(id)}`, undefined, { as: "none" });
      toast.success("Publicación cancelada");
      void qc.invalidateQueries({ queryKey: ["sandbox"] });
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy("");
    }
  }
  if (list.isPending || !list.data?.length) return null;
  return (
    <Section title="Publicaciones programadas" description="A la hora elegida se publica lo que tenga el sandbox en ese momento. Quien la programó debe seguir siendo administrador.">
      <ul className="grid gap-2">
        {list.data.map((x) => (
          <li key={x.id} className="flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm">
            <span className="grid">
              <span className="flex items-center gap-2">
                <CalendarClock className="size-4 text-muted-foreground" aria-hidden="true" />
                {when(x.runAt)} · {x.sandbox || "Sandbox borrado"} · {labels(x.areas)}
              </span>
              <span className={`text-xs ${x.status === "failed" ? "text-bad" : "text-muted-foreground"}`}>
                {SCHEDULE_STATUS[x.status]}
                {x.result ? `: ${x.result}` : ""}
              </span>
            </span>
            {admin && x.status === "pending" && (
              <Button variant="outline" size="sm" disabled={busy !== ""} onClick={() => void cancel(x.id)}>
                {busy === x.id ? "Cancelando…" : "Cancelar"}
              </Button>
            )}
          </li>
        ))}
      </ul>
    </Section>
  );
}

function PublishHistory({ admin, names, labels }: { admin: boolean; names: Map<string, string>; labels: (ids: string[]) => string }) {
  const qc = useQueryClient();
  const [busy, setBusy] = useState("");
  const [askForce, setAskForce] = useState<{ id: string; detail: string } | null>(null);
  const list = useQuery({ queryKey: ["sandbox", "publishes"], queryFn: () => request<{ items: Publish[] }>("GET", "/v1/sandbox/publishes").then((r) => r.items) });
  async function revert(id: string, force = false) {
    setBusy(id);
    try {
      const r = await request<{ rows: number }>("POST", `/v1/sandbox/publishes/${id}/revert`, { force });
      toast.success(`Revertido: ${r.rows} elementos regresaron a como estaban.`);
      setAskForce(null);
      void qc.invalidateQueries({ queryKey: ["sandbox"] });
    } catch (e) {
      if (e instanceof ApiError && e.problem.code === "changed_since") setAskForce({ id, detail: errorMessage(e) });
      else toast.error(errorMessage(e));
    } finally {
      setBusy("");
    }
  }
  if (list.isPending || !list.data?.length) return null;
  return (
    <Section title="Publicaciones" description="Cada publicación guarda cómo estaba tu tienda real antes. Revertir la deja exactamente así.">
      <ul className="grid gap-2">
        {list.data.map((p) => (
          <li key={p.id} className="flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm">
            <span className="flex items-center gap-2">
              <History className="size-4 text-muted-foreground" aria-hidden="true" />
              {when(p.publishedAt)}
              {names.size > 1 && names.get(p.sandboxId) ? ` · ${names.get(p.sandboxId)}` : ""} · {labels(p.areas)} · {p.rows} elementos
              {p.revertedAt && <span className="text-xs text-muted-foreground">(revertida el {when(p.revertedAt)})</span>}
            </span>
            {admin && !p.revertedAt && (
              <Button variant="outline" size="sm" disabled={busy !== ""} onClick={() => void revert(p.id)}>
                {busy === p.id ? "Revirtiendo…" : "Revertir"}
              </Button>
            )}
          </li>
        ))}
      </ul>
      <Confirm
        open={!!askForce}
        title="Hubo cambios en tu tienda real después de publicar"
        text={askForce?.detail ?? ""}
        confirmLabel="Revertir de todos modos"
        busy={busy !== ""}
        onClose={() => setAskForce(null)}
        onConfirm={() => askForce && void revert(askForce.id, true)}
      />
    </Section>
  );
}

function CapturedMail({ id }: { id: string }) {
  const mail = useQuery({ queryKey: ["sandbox", "mail", id], queryFn: () => request<{ items: { id: string; template: string; createdAt: string }[] }>("GET", `${base(id)}/mail`).then((r) => r.items) });
  return (
    <Section title="Correos capturados" description="Los correos que el sandbox habría enviado. Se guardan aquí y nunca llegan a nadie.">
      {mail.isPending ? (
        <Loading rows={2} />
      ) : !mail.data?.length ? (
        <p className="text-sm text-muted-foreground">Aún no hay correos.</p>
      ) : (
        <ul className="grid gap-1 text-sm">
          {mail.data.slice(0, 20).map((m) => (
            <li key={m.id} className="flex justify-between gap-2">
              <span>{m.template}</span>
              <span className="text-muted-foreground">{when(m.createdAt)}</span>
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}
