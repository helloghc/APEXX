import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { ExternalLink, History, Plus, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { Loading, Section, SubPage } from "./kit";

type Category = "necessary" | "analytics" | "marketing";
interface Tag {
  id?: string;
  name: string;
  kind: string;
  account?: string;
  params?: Record<string, string>;
  src?: string;
  code?: string;
  domains?: string[];
  category: Category;
  fire?: string[];
  url?: string;
  active: boolean;
}
interface MetaTag { kind: string; name?: string; content: string }
type ConsentMode = "notice" | "analytics" | "opt_in";
interface TagsConfig { tags: Tag[]; meta: MetaTag[]; bannerText: string; consentMode?: ConsentMode; version: number; publishedAt?: string; publisherName?: string }
interface Field { key: string; label: string; example?: string; required: boolean; options?: string[] }
interface Kind { id: string; label: string; group: string; category: Category; fields: Field[]; fire: boolean; receives: string; docs?: string }
interface MetaKind { id: string; label: string; name: string }
interface Version { version: number; publishedAt: string; publisherName?: string; restoredFrom?: number; changes?: string[]; tags: number; active: number }

const CAT: Record<Category, string> = { necessary: "Necesaria (siempre)", analytics: "Analítica (con permiso)", marketing: "Publicidad (con permiso)" };
const EVENTS: Record<string, string> = {
  page_view: "Todas las páginas",
  view_item: "Ficha de producto",
  add_to_cart: "Agregar al carrito",
  view_cart: "Carrito",
  begin_checkout: "Inicio del pago",
  purchase: "Pedido confirmado",
};
const MODES: Record<ConsentMode, { label: string; help: string }> = {
  notice: {
    label: "Solo aviso (recomendado en México y Latinoamérica)",
    help: "Todas las etiquetas cargan desde la primera página y un aviso informa al visitante, que puede apagarlas en «Preferencias de cookies». Con Global Privacy Control, la publicidad queda apagada (lo exigen leyes de Estados Unidos).",
  },
  analytics: {
    label: "Analítica sin permiso, publicidad con permiso",
    help: "Analytics y los mapas de calor cargan desde la primera página; los píxeles de publicidad esperan a que el visitante acepte.",
  },
  opt_in: {
    label: "Todo con permiso (obligatorio si vendes en Europa o Reino Unido)",
    help: "Nada opcional carga hasta que el visitante acepta, como pide el GDPR. Google recibe las señales del modo de consentimiento v2.",
  },
};
const POSITION: Record<string, string> = { right: "Abajo a la derecha", left: "Abajo a la izquierda" };

/** Tags, pixels, chats and verification tags of the storefront: published as a new version, live on the next page view. */
export function Scripts({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "builder", "admin");
  const q = useQuery({ queryKey: ["store-tags"], queryFn: () => request<{ config: TagsConfig; kinds: Kind[]; metaKinds: MetaKind[] }>("GET", "/v1/settings/tags") });
  const hist = useQuery({ queryKey: ["store-tags", "history"], queryFn: () => request<{ items: Version[] }>("GET", "/v1/settings/tags/history").then((r) => r.items) });
  const [tags, setTags] = useState<Tag[]>([]);
  const [metas, setMetas] = useState<MetaTag[]>([]);
  const [banner, setBanner] = useState("");
  const [mode, setMode] = useState<ConsentMode>("notice");
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [newKind, setNewKind] = useState("ga4");
  useEffect(() => {
    if (q.data) {
      setTags(q.data.config.tags);
      setMetas(q.data.config.meta ?? []);
      setBanner(q.data.config.bannerText);
      setMode(q.data.config.consentMode ?? "notice");
    }
  }, [q.data]);
  const dirty = useMemo(
    () => !!q.data && (JSON.stringify(tags) !== JSON.stringify(q.data.config.tags) || JSON.stringify(metas) !== JSON.stringify(q.data.config.meta ?? []) || banner !== q.data.config.bannerText || mode !== (q.data.config.consentMode ?? "notice")),
    [q.data, tags, metas, banner, mode],
  );
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const cfg = q.data.config;
  const kinds = q.data.kinds;
  const kindOf = (k: string) => kinds.find((x) => x.id === k);
  const groups = [...new Set(kinds.map((k) => k.group))];
  const set = (i: number, p: Partial<Tag>) => setTags((x) => x.map((t, j) => (j === i ? { ...t, ...p } : t)));
  const setParam = (i: number, key: string, v: string) =>
    setTags((x) => x.map((t, j) => (j !== i ? t : key === "account" ? { ...t, account: v } : { ...t, params: { ...(t.params ?? {}), [key]: v } })));
  const setMeta = (i: number, p: Partial<MetaTag>) => setMetas((x) => x.map((m, j) => (j === i ? { ...m, ...p } : m)));
  const testURL = `/s/${encodeURIComponent(me.tenant)}/main/index?apexx_tags=prueba`;
  const add = () => {
    const k = kindOf(newKind);
    if (!k) return;
    setTags((x) => [...x, { name: "", kind: k.id, category: k.category, fire: k.fire ? (k.id === "gads" ? ["purchase"] : ["page_view"]) : undefined, params: k.id === "whatsapp" ? { position: "right" } : undefined, active: true }]);
  };

  const done = (c: TagsConfig, msg: string) => {
    qc.setQueryData(["store-tags"], (old: typeof q.data) => (old ? { ...old, config: c } : old));
    setTags(c.tags);
    setMetas(c.meta ?? []);
    setBanner(c.bannerText);
    setMode(c.consentMode ?? "notice");
    void qc.invalidateQueries({ queryKey: ["store-tags", "history"] });
    toast.success(msg);
  };
  async function publish() {
    setBusy(true);
    setErrs({});
    try {
      const r = await request<{ config: TagsConfig }>("PUT", "/v1/settings/tags", { tags, meta: metas, bannerText: banner, consentMode: mode });
      done(r.config, `Versión ${r.config.version} publicada. La tienda la usa desde la siguiente página que se abra.`);
    } catch (e) {
      setErrs(fieldErrors(e));
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  async function restore(v: number) {
    setBusy(true);
    try {
      const r = await request<{ config: TagsConfig }>("POST", "/v1/settings/tags/restore", { version: v });
      done(r.config, `Se restauró la versión ${v} como versión ${r.config.version}.`);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  const err = (i: number, f: string) => errs[`tags[${i}].${f}`] && <p className="text-xs text-bad">{errs[`tags[${i}].${f}`]}</p>;
  const when = (iso?: string) => (iso ? new Date(iso).toLocaleString("es-MX", { dateStyle: "medium", timeStyle: "short" }) : "");

  return (
    <SubPage
      title="Etiquetas y píxeles"
      sub="Analítica, píxeles de anuncios, mapas de calor, chats y verificación de dominio. Al publicar, cada página de la tienda usa la versión nueva desde la siguiente visita: sin despliegues ni cambios de código. Las de analítica y publicidad cargan solo con el permiso del visitante, y ninguna carga en el pago ni en la cuenta del cliente."
      actions={
        <a className="inline-flex items-center gap-1.5 text-sm underline" href={testURL} target="_blank" rel="noreferrer">
          Probar en la tienda <ExternalLink className="size-3.5" aria-hidden="true" />
        </a>
      }
    >
      <Section
        title={cfg.version ? `Versión publicada: ${cfg.version}` : "Aún no hay etiquetas publicadas"}
        description={
          cfg.publishedAt
            ? `Publicada el ${when(cfg.publishedAt)}${cfg.publisherName ? ` por ${cfg.publisherName}` : ""}. Para verificar, abre la tienda con «Probar en la tienda»: un recuadro muestra cada etiqueta que cargó y cada evento que recibió.`
            : "Agrega una etiqueta, publica y verifica con «Probar en la tienda»."
        }
      >
        <div className="grid gap-1.5">
          <Label htmlFor="tg-mode">Permiso de cookies</Label>
          <select id="tg-mode" className="h-9 rounded-md border bg-background px-2 text-sm" value={mode} disabled={!canEdit} onChange={(e) => setMode(e.target.value as ConsentMode)}>
            {Object.entries(MODES).map(([v, m]) => (
              <option key={v} value={v}>{m.label}</option>
            ))}
          </select>
          <p className="text-xs text-muted-foreground">{MODES[mode].help}</p>
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="tg-banner">Aviso de cookies</Label>
          <Textarea id="tg-banner" rows={2} value={banner} disabled={!canEdit} onChange={(e) => setBanner(e.target.value)} placeholder="Usamos cookies y herramientas de terceros para medir visitas y mostrarte anuncios. Tú decides." />
          <p className="text-xs text-muted-foreground">Se muestra una vez a cada visitante cuando hay etiquetas de analítica o publicidad. Déjalo vacío para usar el texto de Apexx.</p>
        </div>
      </Section>

      {tags.map((t, i) => {
        const k = kindOf(t.kind);
        return (
          <Section
            key={t.id ?? `new-${i}`}
            title={t.name || k?.label || "Etiqueta"}
            description={
              <>
                {k?.group} · {k?.label}
                {k?.docs && (
                  <>
                    {" · "}
                    <a className="underline" href={k.docs} target="_blank" rel="noreferrer">
                      Guía del proveedor
                    </a>
                  </>
                )}
              </>
            }
            actions={
              canEdit && (
                <Button variant="ghost" size="icon" aria-label={`Quitar ${t.name || k?.label}`} onClick={() => setTags((x) => x.filter((_, j) => j !== i))}>
                  <Trash2 className="size-4" />
                </Button>
              )
            }
          >
            <fieldset disabled={!canEdit} className="grid gap-3 sm:grid-cols-2" data-tag={t.kind}>
              <div className="grid gap-1.5">
                <Label htmlFor={`tg-n-${i}`}>Nombre</Label>
                <Input id={`tg-n-${i}`} value={t.name} onChange={(e) => set(i, { name: e.target.value })} placeholder={k?.label} />
                {err(i, "name")}
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor={`tg-c-${i}`}>Permiso que necesita</Label>
                <select id={`tg-c-${i}`} className="h-9 rounded-md border bg-background px-2 text-sm" value={t.category} onChange={(e) => set(i, { category: e.target.value as Category })}>
                  {Object.entries(CAT).map(([v, l]) => (
                    <option key={v} value={v}>{l}</option>
                  ))}
                </select>
                {err(i, "category")}
              </div>
              {k?.fields.map((f) => {
                const v = f.key === "account" ? (t.account ?? "") : (t.params?.[f.key] ?? "");
                const id = `tg-${f.key}-${i}`;
                return (
                  <div key={f.key} className="grid gap-1.5">
                    <Label htmlFor={id}>{f.label}{f.required ? "" : " (opcional)"}</Label>
                    {f.options ? (
                      <select id={id} className="h-9 rounded-md border bg-background px-2 text-sm" value={v || f.options[0]} onChange={(e) => setParam(i, f.key, e.target.value)}>
                        {f.options.map((o) => (
                          <option key={o} value={o}>{POSITION[o] ?? o}</option>
                        ))}
                      </select>
                    ) : (
                      <Input id={id} className={f.key === "message" || f.key === "text" ? "" : "font-mono"} value={v} onChange={(e) => setParam(i, f.key, e.target.value)} placeholder={f.example} />
                    )}
                    {err(i, f.key)}
                  </div>
                );
              })}
              {t.kind === "script" && (
                <div className="grid gap-1.5 sm:col-span-2">
                  <Label htmlFor={`tg-s-${i}`}>Dirección del script</Label>
                  <Input id={`tg-s-${i}`} className="font-mono text-xs" value={t.src ?? ""} onChange={(e) => set(i, { src: e.target.value })} placeholder="https://" />
                  {err(i, "src")}
                </div>
              )}
              {t.kind === "code" && (
                <div className="grid gap-1.5 sm:col-span-2">
                  <Label htmlFor={`tg-code-${i}`}>Código JavaScript</Label>
                  <Textarea id={`tg-code-${i}`} rows={6} className="font-mono text-xs" value={t.code ?? ""} onChange={(e) => set(i, { code: e.target.value })} placeholder={"// apexx.event: page_view, view_item, add_to_cart, view_cart, begin_checkout o purchase\n// apexx.ecommerce: { value, currency, transaction_id, items: [{ item_id, item_name, price, quantity }] }"} />
                  <p className="text-xs text-muted-foreground">Sin etiquetas &lt;script&gt;. Se ejecuta como función con el evento en <code>apexx</code>. Corre en su propio archivo: un error suyo no detiene las demás etiquetas.</p>
                  {err(i, "code")}
                </div>
              )}
              {(t.kind === "script" || t.kind === "code") && (
                <div className="grid gap-1.5 sm:col-span-2">
                  <Label htmlFor={`tg-d-${i}`}>Otros dominios que usa (opcional)</Label>
                  <Input id={`tg-d-${i}`} className="font-mono text-xs" value={(t.domains ?? []).join(" ")} onChange={(e) => set(i, { domains: e.target.value.split(/[\s,]+/).filter(Boolean) })} placeholder="https://api.proveedor.com" />
                  <p className="text-xs text-muted-foreground">La política de seguridad de la tienda solo permite los dominios de las etiquetas activas.</p>
                  {err(i, "domains")}
                </div>
              )}
              {k?.fire && (
                <fieldset className="grid gap-1.5 sm:col-span-2">
                  <legend className="text-sm font-medium">Se dispara en</legend>
                  <div className="flex flex-wrap gap-x-4 gap-y-1.5">
                    {Object.entries(EVENTS).map(([ev, l]) => (
                      <label key={ev} className="flex items-center gap-1.5 text-sm">
                        <input type="checkbox" checked={(t.fire ?? []).includes(ev)} onChange={(e) => set(i, { fire: e.target.checked ? [...(t.fire ?? []), ev] : (t.fire ?? []).filter((x) => x !== ev) })} />
                        {l}
                      </label>
                    ))}
                  </div>
                  {err(i, "fire")}
                </fieldset>
              )}
              <p className="text-xs text-muted-foreground sm:col-span-2">{k?.receives}</p>
              <div className="grid gap-1.5">
                <Label htmlFor={`tg-u-${i}`}>Solo en direcciones que contienen (opcional)</Label>
                <Input id={`tg-u-${i}`} className="font-mono text-xs" value={t.url ?? ""} onChange={(e) => set(i, { url: e.target.value })} placeholder="/p/ o /gracias" />
                {err(i, "url")}
              </div>
              <label className="flex items-center gap-2 self-end text-sm">
                <input type="checkbox" checked={t.active} onChange={(e) => set(i, { active: e.target.checked })} /> Activa
              </label>
            </fieldset>
          </Section>
        );
      })}

      {canEdit && (
        <div className="flex flex-wrap items-end gap-2">
          <div className="grid gap-1.5">
            <Label htmlFor="tg-new">Agregar</Label>
            <select id="tg-new" className="h-9 max-w-full rounded-md border bg-background px-2 text-sm" value={newKind} onChange={(e) => setNewKind(e.target.value)}>
              {groups.map((g) => (
                <optgroup key={g} label={g}>
                  {kinds.filter((k) => k.group === g).map((k) => (
                    <option key={k.id} value={k.id}>{k.label}</option>
                  ))}
                </optgroup>
              ))}
            </select>
          </div>
          <Button variant="outline" onClick={add}>
            <Plus className="size-4" /> Agregar etiqueta
          </Button>
        </div>
      )}

      <Section
        title="Verificación de dominio"
        description="Las etiquetas meta que piden Google Search Console, Bing, Meta, Pinterest u otros servicios para comprobar que el dominio es tuyo. Van en el encabezado de todas las páginas; puedes pegar la etiqueta completa o solo el código."
      >
        {metas.map((m, i) => (
          <fieldset key={i} disabled={!canEdit} className="grid gap-3 sm:grid-cols-[14rem_1fr_auto] sm:items-end" data-meta>
            <div className="grid gap-1.5">
              <Label htmlFor={`mt-k-${i}`}>Servicio</Label>
              <select id={`mt-k-${i}`} className="h-9 rounded-md border bg-background px-2 text-sm" value={m.kind} onChange={(e) => setMeta(i, { kind: e.target.value })}>
                {q.data.metaKinds.map((k) => (
                  <option key={k.id} value={k.id}>{k.label}</option>
                ))}
              </select>
            </div>
            <div className="grid gap-1.5">
              {m.kind === "other" && (
                <>
                  <Label htmlFor={`mt-n-${i}`}>Nombre de la etiqueta</Label>
                  <Input id={`mt-n-${i}`} className="font-mono text-xs" value={m.name ?? ""} onChange={(e) => setMeta(i, { name: e.target.value })} placeholder="nombre-de-verificacion" />
                  {errs[`meta[${i}].name`] && <p className="text-xs text-bad">{errs[`meta[${i}].name`]}</p>}
                </>
              )}
              <Label htmlFor={`mt-c-${i}`}>Código de verificación</Label>
              <Input id={`mt-c-${i}`} className="font-mono text-xs" value={m.content} onChange={(e) => setMeta(i, { content: e.target.value })} placeholder='abc123… o <meta name="…" content="…">' />
              {errs[`meta[${i}].content`] && <p className="text-xs text-bad">{errs[`meta[${i}].content`]}</p>}
              {errs[`meta[${i}].kind`] && <p className="text-xs text-bad">{errs[`meta[${i}].kind`]}</p>}
            </div>
            {canEdit && (
              <Button variant="ghost" size="icon" aria-label="Quitar verificación" onClick={() => setMetas((x) => x.filter((_, j) => j !== i))}>
                <Trash2 className="size-4" />
              </Button>
            )}
          </fieldset>
        ))}
        {canEdit && (
          <div>
            <Button variant="outline" size="sm" onClick={() => setMetas((x) => [...x, { kind: "google", content: "" }])}>
              <Plus className="size-4" /> Agregar verificación
            </Button>
          </div>
        )}
      </Section>

      {canEdit && (
        <div className="flex flex-wrap gap-2">
          <Button disabled={busy || !dirty} onClick={() => void publish()}>
            {busy ? "Publicando…" : `Publicar versión ${cfg.version + 1}`}
          </Button>
        </div>
      )}

      <Section title="Versiones" description="Quién publicó, cuándo y qué cambió. Restaurar una versión la publica de nuevo como la más reciente. La auditoría guarda además la configuración completa de cada versión.">
        {hist.isPending ? (
          <Loading rows={2} />
        ) : !hist.data?.length ? (
          <p className="text-sm text-muted-foreground">Todavía no hay versiones.</p>
        ) : (
          <ul className="grid gap-2">
            {hist.data.map((v) => (
              <li key={v.version} className="grid gap-1 rounded-md border px-3 py-2 text-sm">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="flex items-center gap-2">
                    <History className="size-4 text-muted-foreground" aria-hidden="true" />
                    <strong>Versión {v.version}</strong> · {when(v.publishedAt)}
                    {v.publisherName ? ` · ${v.publisherName}` : ""} · {v.active} de {v.tags} activas
                    {v.version === cfg.version && <span className="text-xs font-medium">(publicada)</span>}
                  </span>
                  {canEdit && v.version !== cfg.version && (
                    <Button variant="outline" size="sm" disabled={busy} onClick={() => void restore(v.version)}>
                      Restaurar
                    </Button>
                  )}
                </div>
                {!!v.changes?.length && (
                  <ul className="ml-6 list-disc text-muted-foreground">
                    {v.changes.map((c, j) => (
                      <li key={j}>{c}</li>
                    ))}
                  </ul>
                )}
              </li>
            ))}
          </ul>
        )}
      </Section>
    </SubPage>
  );
}
