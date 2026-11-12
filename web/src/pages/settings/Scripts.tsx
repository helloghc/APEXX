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

type Kind = "gtm" | "ga4" | "gads" | "meta" | "tiktok" | "pinterest" | "script" | "code";
type Category = "necessary" | "analytics" | "marketing";
interface Tag {
  id?: string;
  name: string;
  kind: Kind;
  account?: string;
  label?: string;
  src?: string;
  code?: string;
  domains?: string[];
  category: Category;
  fire?: string[];
  url?: string;
  active: boolean;
}
interface TagsConfig { tags: Tag[]; bannerText: string; version: number; publishedAt?: string; publishedBy?: string }
interface KindInfo { id: Kind; label: string; example: string }
interface Version { version: number; publishedAt: string; publishedBy: string; tags: number; active: number }

const CAT: Record<Category, string> = { necessary: "Necesaria (siempre)", analytics: "Analítica (con permiso)", marketing: "Publicidad (con permiso)" };
const EVENTS: Record<string, string> = {
  page_view: "Todas las páginas",
  view_item: "Ficha de producto",
  add_to_cart: "Agregar al carrito",
  view_cart: "Carrito",
  begin_checkout: "Inicio del pago",
  purchase: "Pedido confirmado",
};
const ACCOUNT_LABEL: Partial<Record<Kind, string>> = {
  gtm: "ID del contenedor",
  ga4: "ID de medición",
  gads: "ID de conversión",
  meta: "ID del píxel",
  tiktok: "ID del píxel",
  pinterest: "ID de la etiqueta",
};
// What each provider receives on its own, without configuring anything.
const AUTO: Partial<Record<Kind, string>> = {
  gtm: "El contenedor recibe en el dataLayer las vistas y los eventos de comercio (view_item, add_to_cart, view_cart, begin_checkout, purchase). Las etiquetas se administran en Google Tag Manager.",
  ga4: "Recibe las vistas de página y los eventos de comercio con producto, monto y moneda. La compra lleva el número de pedido.",
  meta: "Recibe PageView, ViewContent, AddToCart, InitiateCheckout y Purchase. La compra usa el número de pedido como identificador, así Meta descarta el duplicado de las conversiones desde el servidor.",
  tiktok: "Recibe la vista de página, ViewContent, AddToCart, InitiateCheckout y CompletePayment con monto y productos.",
  pinterest: "Recibe la vista de página, pagevisit, addtocart y checkout (la compra) con monto y productos.",
};
const blank = (kind: Kind): Tag => ({
  name: "",
  kind,
  category: kind === "ga4" ? "analytics" : "marketing",
  fire: kind === "gads" ? ["purchase"] : kind === "script" || kind === "code" ? ["page_view"] : undefined,
  active: true,
});

/** Tags and pixels of the storefront: published as a new version, live on the next page view. */
export function Scripts({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "builder", "admin");
  const q = useQuery({ queryKey: ["store-tags"], queryFn: () => request<{ config: TagsConfig; kinds: KindInfo[]; events: string[] }>("GET", "/v1/settings/tags") });
  const hist = useQuery({ queryKey: ["store-tags", "history"], queryFn: () => request<{ items: Version[] }>("GET", "/v1/settings/tags/history").then((r) => r.items) });
  const [tags, setTags] = useState<Tag[]>([]);
  const [banner, setBanner] = useState("");
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [newKind, setNewKind] = useState<Kind>("ga4");
  useEffect(() => {
    if (q.data) {
      setTags(q.data.config.tags);
      setBanner(q.data.config.bannerText);
    }
  }, [q.data]);
  const dirty = useMemo(() => !!q.data && (JSON.stringify(tags) !== JSON.stringify(q.data.config.tags) || banner !== q.data.config.bannerText), [q.data, tags, banner]);
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const cfg = q.data.config;
  const kindOf = (k: Kind) => q.data.kinds.find((x) => x.id === k);
  const set = (i: number, p: Partial<Tag>) => setTags((x) => x.map((t, j) => (j === i ? { ...t, ...p } : t)));
  const testURL = `/s/${encodeURIComponent(me.tenant)}/main/index?apexx_tags=prueba`;

  const done = (c: TagsConfig, msg: string) => {
    qc.setQueryData(["store-tags"], (old: typeof q.data) => (old ? { ...old, config: c } : old));
    setTags(c.tags);
    setBanner(c.bannerText);
    void qc.invalidateQueries({ queryKey: ["store-tags", "history"] });
    toast.success(msg);
  };
  async function publish() {
    setBusy(true);
    setErrs({});
    try {
      const r = await request<{ config: TagsConfig }>("PUT", "/v1/settings/tags", { tags, bannerText: banner });
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

  return (
    <SubPage
      title="Etiquetas y píxeles"
      sub="Analítica, píxeles de anuncios y conversiones. Al publicar, cada página de la tienda usa la versión nueva desde la siguiente visita: sin despliegues ni cambios de código. Las de analítica y publicidad cargan solo con el permiso del visitante, y ninguna carga en el pago ni en la cuenta del cliente."
      actions={
        <a className="inline-flex items-center gap-1.5 text-sm underline" href={testURL} target="_blank" rel="noreferrer">
          Probar en la tienda <ExternalLink className="size-3.5" aria-hidden="true" />
        </a>
      }
    >
      <Section
        title={cfg.version ? `Versión publicada: ${cfg.version}` : "Aún no hay etiquetas publicadas"}
        description={
          cfg.publishedAt ? (
            <>
              Publicada el {new Date(cfg.publishedAt).toLocaleString("es-MX", { dateStyle: "medium", timeStyle: "short" })}. Para verificar, abre la tienda con «Probar en la tienda»: un recuadro muestra cada etiqueta que cargó y cada evento que recibió.
            </>
          ) : (
            "Agrega una etiqueta, publica y verifica con «Probar en la tienda»."
          )
        }
      >
        <div className="grid gap-1.5">
          <Label htmlFor="tg-banner">Aviso de cookies</Label>
          <Textarea id="tg-banner" rows={2} value={banner} disabled={!canEdit} onChange={(e) => setBanner(e.target.value)} placeholder="Usamos cookies y herramientas de terceros para medir visitas y mostrarte anuncios. Tú decides." />
          <p className="text-xs text-muted-foreground">Se muestra cuando hay etiquetas de analítica o publicidad. Con Global Privacy Control, la analítica y la publicidad quedan apagadas hasta que el visitante las encienda.</p>
        </div>
      </Section>

      {tags.map((t, i) => {
        const k = kindOf(t.kind);
        const fireable = t.kind === "gads" || t.kind === "script" || t.kind === "code";
        return (
          <Section
            key={t.id ?? `new-${i}`}
            title={t.name || k?.label || "Etiqueta"}
            description={k?.label}
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
              {ACCOUNT_LABEL[t.kind] && (
                <div className="grid gap-1.5">
                  <Label htmlFor={`tg-a-${i}`}>{ACCOUNT_LABEL[t.kind]}</Label>
                  <Input id={`tg-a-${i}`} className="font-mono" value={t.account ?? ""} onChange={(e) => set(i, { account: e.target.value })} placeholder={k?.example} />
                  {err(i, "account")}
                </div>
              )}
              {t.kind === "gads" && (
                <div className="grid gap-1.5">
                  <Label htmlFor={`tg-l-${i}`}>Etiqueta de conversión</Label>
                  <Input id={`tg-l-${i}`} className="font-mono" value={t.label ?? ""} onChange={(e) => set(i, { label: e.target.value })} placeholder="AbC-D_efG-h12_34-567" />
                  {err(i, "label")}
                </div>
              )}
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
              {fireable ? (
                <fieldset className="grid gap-1.5 sm:col-span-2">
                  <legend className="text-sm font-medium">Se dispara en</legend>
                  <div className="flex flex-wrap gap-x-4 gap-y-1.5">
                    {Object.entries(EVENTS).map(([ev, l]) => (
                      <label key={ev} className="flex items-center gap-1.5 text-sm">
                        <input
                          type="checkbox"
                          checked={(t.fire ?? []).includes(ev)}
                          onChange={(e) => set(i, { fire: e.target.checked ? [...(t.fire ?? []), ev] : (t.fire ?? []).filter((x) => x !== ev) })}
                        />
                        {l}
                      </label>
                    ))}
                  </div>
                  {err(i, "fire")}
                </fieldset>
              ) : (
                <p className="text-xs text-muted-foreground sm:col-span-2">{AUTO[t.kind]}</p>
              )}
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
            <select id="tg-new" className="h-9 rounded-md border bg-background px-2 text-sm" value={newKind} onChange={(e) => setNewKind(e.target.value as Kind)}>
              {q.data.kinds.map((k) => (
                <option key={k.id} value={k.id}>{k.label}</option>
              ))}
            </select>
          </div>
          <Button variant="outline" onClick={() => setTags((x) => [...x, blank(newKind)])}>
            <Plus className="size-4" /> Agregar etiqueta
          </Button>
          <Button disabled={busy || !dirty} onClick={() => void publish()}>
            {busy ? "Publicando…" : `Publicar versión ${cfg.version + 1}`}
          </Button>
        </div>
      )}

      <Section title="Versiones" description="Cada publicación queda guardada. Restaurar una versión la publica de nuevo como la más reciente.">
        {hist.isPending ? (
          <Loading rows={2} />
        ) : !hist.data?.length ? (
          <p className="text-sm text-muted-foreground">Todavía no hay versiones.</p>
        ) : (
          <ul className="grid gap-2">
            {hist.data.map((v) => (
              <li key={v.version} className="flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm">
                <span className="flex items-center gap-2">
                  <History className="size-4 text-muted-foreground" aria-hidden="true" />
                  Versión {v.version} · {new Date(v.publishedAt).toLocaleString("es-MX", { dateStyle: "medium", timeStyle: "short" })} · {v.active} de {v.tags} activas
                  {v.version === cfg.version && <strong className="text-xs">(publicada)</strong>}
                </span>
                {canEdit && v.version !== cfg.version && (
                  <Button variant="outline" size="sm" disabled={busy} onClick={() => void restore(v.version)}>
                    Restaurar
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
      </Section>
    </SubPage>
  );
}
