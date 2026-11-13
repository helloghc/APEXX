import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Copy, ExternalLink, History, RefreshCw, Rocket, Trash2 } from "lucide-react";
import { ApiError, errorMessage, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { href } from "@/router";
import { ErrorBox } from "../common";
import { Confirm, Loading, Section, SubPage } from "./kit";

interface SandboxState {
  id: string;
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
  env: "production" | "sandbox";
  parent?: string;
  sandbox: SandboxState | null;
  url?: string;
  domains: string[];
  areas: { id: string; label: string }[];
  expiresAt?: string;
}
interface RowChange { table: string; pk: string; action: "insert" | "update" | "delete"; conflict: boolean; label?: string }
interface AreaDiff { area: string; label: string; inserts: number; updates: number; deletes: number; conflicts: number; sample: RowChange[] }
interface Diff { areas: AreaDiff[]; tags?: string[] }
interface Publish { id: string; areas: string[]; publishedBy: string; publishedAt: string; revertedAt?: string; revertedBy?: string; rows: number }

const ACTION: Record<RowChange["action"], string> = { insert: "Nuevo", update: "Cambia", delete: "Se borra" };
const when = (iso?: string) => (iso ? new Date(iso).toLocaleString("es-MX", { dateStyle: "medium", timeStyle: "short" }) : "");

/** Sandbox: a complete copy of the store to try anything, and publish to production what is ready. */
export function Sandbox({ me }: { me: Me }) {
  const qc = useQueryClient();
  const admin = can(me, "settings", "admin");
  const q = useQuery({
    queryKey: ["sandbox"],
    queryFn: () => request<View>("GET", "/v1/sandbox"),
    refetchInterval: (query) => (query.state.data?.sandbox?.status === "copying" ? 1000 : false),
  });
  const [dataMode, setDataMode] = useState<"none" | "demo">("none");
  const [busy, setBusy] = useState("");
  const [confirm, setConfirm] = useState<"" | "refresh" | "delete">("");
  const [share, setShare] = useState<{ url: string; expiresAt: string } | null>(null);
  const [hours, setHours] = useState(24);
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const v = q.data;
  const sb = v.sandbox;
  const inSandbox = v.env === "sandbox";
  const refresh = () => void qc.invalidateQueries({ queryKey: ["sandbox"] });

  async function act(name: string, fn: () => Promise<unknown>, ok?: string) {
    setBusy(name);
    try {
      await fn();
      if (ok) toast.success(ok);
      refresh();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy("");
    }
  }
  const enter = () =>
    act("enter", async () => {
      await request("POST", "/v1/sandbox/enter");
      window.location.hash = "#inicio";
      window.location.reload();
    });
  const openStore = () =>
    act("open", async () => {
      const r = await request<{ url: string }>("POST", "/v1/sandbox/preview", { hours: 12 });
      window.open(r.url, "_blank", "noopener");
    });
  const makeShare = () =>
    act("share", async () => {
      const r = await request<{ url: string; expiresAt: string }>("POST", "/v1/sandbox/preview", { hours });
      setShare({ url: r.url.startsWith("http") ? r.url : window.location.origin + r.url, expiresAt: r.expiresAt });
    });

  return (
    <SubPage
      title="Sandbox"
      sub="Una copia completa de tu tienda para probar lo que quieras: productos, precios, promociones, diseño, configuración y etiquetas. Nada llega a tu tienda real hasta que lo publiques, y cada publicación se puede revertir."
    >
      {!sb && !inSandbox && (
        <Section title="Crea tu sandbox" description="Se copia todo lo de tu tienda en segundos. No se copian los datos personales de tus clientes, los pedidos reales ni las credenciales (pagos, llaves, conexiones): en el sandbox se pone lo de prueba.">
          <fieldset className="grid gap-2" disabled={!admin}>
            <legend className="text-sm font-medium">Clientes y pedidos en el sandbox</legend>
            <label className="flex items-start gap-2 text-sm">
              <input type="radio" name="dm" checked={dataMode === "none"} onChange={() => setDataMode("none")} className="mt-1" />
              <span>Sin clientes ni pedidos (recomendado). Ningún dato personal sale de producción.</span>
            </label>
            <label className="flex items-start gap-2 text-sm">
              <input type="radio" name="dm" checked={dataMode === "demo"} onChange={() => setDataMode("demo")} className="mt-1" />
              <span>Con clientes y pedidos de prueba, generados con tus productos (300 pedidos de 90 días), para probar reportes, segmentos y campañas.</span>
            </label>
          </fieldset>
          {admin ? (
            <div>
              <Button disabled={busy !== ""} onClick={() => act("create", () => request("POST", "/v1/sandbox", { dataMode }), "Copiando tu tienda al sandbox…")}>
                {busy === "create" ? "Creando…" : "Crear sandbox"}
              </Button>
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">Pide a un administrador que lo cree.</p>
          )}
        </Section>
      )}

      {sb && (
        <Section
          title={inSandbox ? "Estás en el sandbox" : sb.status === "ready" ? "Tu sandbox está listo" : sb.status === "copying" ? "Copiando tu tienda…" : sb.status === "failed" ? "La copia no terminó" : "Borrando…"}
          description={
            sb.status === "copying"
              ? `${sb.progress?.done ?? 0} de ${sb.progress?.total ?? "…"} partes copiadas`
              : sb.status === "failed"
                ? sb.error
                : `Copiado el ${when(sb.copiedAt)} en ${(sb.copyMs / 1000).toFixed(1)} s · ${sb.dataMode === "demo" ? "con clientes y pedidos de prueba" : "sin clientes ni pedidos"}${v.expiresAt ? ` · se borra el ${when(v.expiresAt)} si nadie lo usa` : " · no vence (usa tu propio subdominio)"}`
          }
        >
          {sb.status === "copying" && (
            <div className="h-2 overflow-hidden rounded bg-muted" role="progressbar" aria-valuemin={0} aria-valuemax={sb.progress?.total ?? 1} aria-valuenow={sb.progress?.done ?? 0}>
              <div className="h-full bg-primary transition-all" style={{ width: `${Math.round(((sb.progress?.done ?? 0) / Math.max(1, sb.progress?.total ?? 1)) * 100)}%` }} />
            </div>
          )}
          {sb.status === "ready" && (
            <div className="flex flex-wrap gap-2">
              {!inSandbox && (
                <Button disabled={busy !== ""} onClick={() => void enter()}>
                  {busy === "enter" ? "Entrando…" : "Entrar al sandbox"}
                </Button>
              )}
              <Button variant="outline" disabled={busy !== ""} onClick={() => void openStore()}>
                <ExternalLink className="size-4" /> Ver la tienda del sandbox
              </Button>
            </div>
          )}
          {sb.status !== "copying" && admin && !inSandbox && (
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" disabled={busy !== ""} onClick={() => setConfirm("refresh")}>
                <RefreshCw className="size-4" /> Refrescar desde producción
              </Button>
              <Button variant="ghost" disabled={busy !== ""} onClick={() => setConfirm("delete")}>
                <Trash2 className="size-4" /> Borrar sandbox
              </Button>
            </div>
          )}
        </Section>
      )}

      {sb?.status === "ready" && (
        <Section title="Dirección del sandbox" description="Tu sandbox es privado: solo lo ve tu equipo con sesión, o quien reciba un enlace de vista previa. Nunca aparece en buscadores.">
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
            ¿Prefieres tu propio subdominio, como pruebas.mitienda.com? {inSandbox ? "Agrégalo en " : "Entra al sandbox y agrégalo en "}
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

      {sb?.status === "ready" && <PublishPanel admin={admin} areas={v.areas} />}
      {sb && <PublishHistory admin={admin} />}
      {sb?.status === "ready" && <CapturedMail />}

      <Confirm
        open={confirm === "refresh"}
        title="¿Refrescar el sandbox desde producción?"
        text="Se vuelve a copiar tu tienda real y se pierde lo que cambiaste en el sandbox y no publicaste. Tu dirección y tu equipo se mantienen."
        confirmLabel="Refrescar"
        busy={busy === "refresh"}
        onClose={() => setConfirm("")}
        onConfirm={() => act("refresh", () => request("POST", "/v1/sandbox/refresh"), "Copiando tu tienda al sandbox…").then(() => setConfirm(""))}
      />
      <Confirm
        open={confirm === "delete"}
        title="¿Borrar el sandbox?"
        text="Se borra la copia y todo lo que no publicaste. Tu tienda real no cambia, y lo que ya publicaste se queda."
        confirmLabel="Borrar sandbox"
        busy={busy === "delete"}
        onClose={() => setConfirm("")}
        onConfirm={() => act("delete", () => request("DELETE", "/v1/sandbox", undefined, { as: "none" }), "Sandbox borrado").then(() => setConfirm(""))}
      />
    </SubPage>
  );
}

function PublishPanel({ admin, areas }: { admin: boolean; areas: { id: string; label: string }[] }) {
  const qc = useQueryClient();
  const [chosen, setChosen] = useState<string[]>([]);
  const [force, setForce] = useState(false);
  const [busy, setBusy] = useState(false);
  const diff = useQuery({ queryKey: ["sandbox", "diff"], queryFn: () => request<Diff>("GET", "/v1/sandbox/diff") });
  if (diff.isPending) return <Loading rows={3} />;
  if (diff.error) return <ErrorBox error={diff.error} />;
  const byArea = new Map(diff.data.areas.map((a) => [a.area, a]));
  const tagChanges = diff.data.tags ?? [];
  const total = (a?: AreaDiff) => (a ? a.inserts + a.updates + a.deletes + a.conflicts : 0);
  const pending = areas.filter((a) => (a.id === "tags" ? tagChanges.length > 0 : total(byArea.get(a.id)) > 0));
  const conflicts = chosen.reduce((n, id) => n + (byArea.get(id)?.conflicts ?? 0), 0);

  async function publish() {
    setBusy(true);
    try {
      const r = await request<{ publish?: { applied: number; skipped: number } | null; tags?: { version: number } }>("POST", "/v1/sandbox/publish", { areas: chosen, force });
      const parts = [];
      if (r.publish) parts.push(`${r.publish.applied} cambios publicados${r.publish.skipped ? `, ${r.publish.skipped} conflictos sin tocar` : ""}`);
      if (r.tags) parts.push(`etiquetas en la versión ${r.tags.version}`);
      toast.success(`Publicado a producción: ${parts.join(" y ")}.`);
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
        <div className="grid gap-2">
          {conflicts > 0 && (
            <label className="flex items-start gap-2 text-sm">
              <input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} className="mt-1" />
              <span>Publicar también los {conflicts} conflictos: la versión del sandbox reemplaza la de tu tienda real.</span>
            </label>
          )}
          <div>
            <Button disabled={busy || chosen.length === 0} onClick={() => void publish()}>
              <Rocket className="size-4" /> {busy ? "Publicando…" : "Publicar a producción"}
            </Button>
          </div>
        </div>
      )}
    </Section>
  );
}

function PublishHistory({ admin }: { admin: boolean }) {
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
              {when(p.publishedAt)} · {p.areas.join(", ")} · {p.rows} elementos
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

function CapturedMail() {
  const mail = useQuery({ queryKey: ["sandbox", "mail"], queryFn: () => request<{ items: { id: string; template: string; createdAt: string }[] }>("GET", "/v1/sandbox/mail").then((r) => r.items) });
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
