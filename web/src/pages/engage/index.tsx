import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Heart, MessageSquare, Plus, Search, Star, Trash2 } from "lucide-react";
import { errorMessage, request, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { href } from "@/router";
import { ProductPicker } from "../catalog/ProductPicker";
import { AIPanel } from "./AI";
import { dateTime, ErrorBox, Notice, Page, StatusPill } from "../common";
import { ChipInput, EmptyState, SkeletonRows, SubNav, useDebounced } from "../promotions/shared";

const enc = encodeURIComponent;
interface Ref { id: string; title: string; handle: string }
interface Config { synonyms: string[][]; redirects: { term: string; url: string }[]; pins: { term: string; productIds: string[] }[]; hidden: string[]; noLearning?: boolean }
interface Term { term: string; searches: number; results: number; suggest?: { synonym?: string; products: Ref[] } }
interface Review { id: string; productId: string; productTitle: string; productHandle: string; rating: number; title: string; body: string; author: string; verified: boolean; status: string; reply: string; createdAt: string }
interface Preview { total: number; items: { id: string; title: string; brand: string; pinned: boolean }[]; alternatives: string[]; redirect: string }

const api = {
  config: () => request<{ config: Config; products: Ref[] }>("GET", "/v1/search/config"),
  save: (c: Config) => request<{ config: Config }>("PUT", "/v1/search/config", c),
  terms: (days: number) => request<{ top: Term[]; noResults: Term[] }>("GET", `/v1/search/terms?days=${days}`),
  preview: (q: string) => request<Preview>("GET", `/v1/search/preview?q=${enc(q)}`),
  learned: (q: string) => request<{ items: { id: string; clicks: number; carts: number; boost: number }[] }>("GET", `/v1/search/learned?q=${enc(q)}`),
  reviews: (status: string) => request<{ items: Review[]; counts: Record<string, number> }>("GET", `/v1/reviews?status=${enc(status)}&limit=50`),
  moderate: (id: string, b: { status?: string; reply?: string }) => request<Review>("POST", `/v1/reviews/${enc(id)}/moderate`, b),
  remove: (id: string) => request<void>("DELETE", `/v1/reviews/${enc(id)}`, undefined, { as: "none" }),
  wishes: () => request<{ items: { productId: string; title: string; handle: string; customers: number; available: number }[] }>("GET", "/v1/wishlists/top"),
};

const TABS = [
  { key: "", label: "Reglas de búsqueda", route: ["busqueda"] },
  { key: "terminos", label: "Qué buscan", route: ["busqueda", "terminos"] },
  { key: "opiniones", label: "Opiniones", route: ["busqueda", "opiniones"] },
  { key: "favoritos", label: "Favoritos", route: ["busqueda", "favoritos"] },
  { key: "ia", label: "Recomendaciones e IA", route: ["busqueda", "ia"] },
];

/** Routes: #busqueda, #busqueda/terminos, #busqueda/opiniones, #busqueda/favoritos. */
export function Engage({ me, route }: { me: Me; route: string[] }) {
  const section = route[1] ?? "";
  const canEdit = can(me, "catalog", "edit");
  return (
    <Page title="Búsqueda y opiniones" sub="Decide qué encuentran tus clientes, mira qué buscan y modera lo que opinan. Los cambios aplican al instante, sin reindexar.">
      <SubNav items={TABS} active={section} label="Secciones" />
      {section === "terminos" ? <Terms canEdit={canEdit} /> : section === "opiniones" ? <Reviews canEdit={canEdit} canDelete={can(me, "catalog", "admin")} /> : section === "favoritos" ? <Wishes /> : section === "ia" ? <AIPanel canEdit={canEdit} canAdmin={can(me, "catalog", "admin")} /> : <Rules canEdit={canEdit} />}
    </Page>
  );
}

function Rules({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["search-config"], queryFn: api.config });
  const [cfg, setCfg] = useState<Config | null>(null);
  const [names, setNames] = useState<Record<string, string>>({});
  const [picker, setPicker] = useState<{ kind: "hidden" } | { kind: "pin"; i: number } | null>(null);
  const [test, setTest] = useState("");
  const dtest = useDebounced(test.trim());
  useEffect(() => {
    if (q.data && !cfg) {
      setCfg(q.data.config);
      setNames(Object.fromEntries(q.data.products.map((p) => [p.id, p.title])));
    }
  }, [q.data, cfg]);
  const dirty = !!cfg && !!q.data && JSON.stringify(cfg) !== JSON.stringify(q.data.config);
  const preview = useQuery({ queryKey: ["search-preview", dtest, q.dataUpdatedAt], queryFn: () => api.preview(dtest), enabled: dtest.length >= 2 });
  const learned = useQuery({ queryKey: ["search-learned", dtest], queryFn: () => api.learned(dtest), enabled: dtest.length >= 2 });
  const lmap = Object.fromEntries((learned.data?.items ?? []).filter((x) => x.boost > 0).map((x) => [x.id, x]));
  const save = useMutation({
    mutationFn: () => api.save({ ...(cfg as Config), synonyms: (cfg as Config).synonyms.filter((g) => g.length > 0) }),
    onSuccess: (r) => { toast.success("Reglas guardadas. Ya aplican en tu tienda."); qc.setQueryData(["search-config"], (old: typeof q.data) => (old ? { ...old, config: r.config } : old)); setCfg(r.config); void qc.invalidateQueries({ queryKey: ["search-preview"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  if (q.error) return <ErrorBox error={q.error} onRetry={() => void q.refetch()} />;
  if (!cfg) return <p className="text-sm text-muted-foreground">Cargando…</p>;
  const set = (patch: Partial<Config>) => setCfg({ ...cfg, ...patch });
  const chip = (id: string, onRemove: () => void) => (
    <span key={id} className="inline-flex items-center gap-1 rounded-md border bg-muted px-2 py-1 text-sm">{names[id] ?? id}{canEdit && <button type="button" aria-label={`Quitar ${names[id] ?? id}`} className="text-muted-foreground hover:text-foreground" onClick={onRemove}>×</button>}</span>
  );
  return (
    <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_360px]">
      <fieldset disabled={!canEdit} className="grid min-w-0 content-start gap-5">
        <Card>
          <CardHeader><CardTitle className="text-base">Sinónimos</CardTitle><CardDescription>Palabras que significan lo mismo para tus clientes. Si alguien busca “remera” y tú vendes “playeras”, las encuentra.</CardDescription></CardHeader>
          <CardContent className="grid gap-3">
            {cfg.synonyms.map((g, i) => (
              <div key={i} className="flex items-start gap-2">
                <div className="min-w-0 flex-1"><ChipInput values={g} onChange={(v) => set({ synonyms: cfg.synonyms.map((x, j) => (j === i ? v : x)) })} placeholder="Escribe una palabra y pulsa Enter" transform={(s) => s.toLowerCase()} /></div>
                <Button type="button" size="icon" variant="ghost" aria-label="Quitar grupo" onClick={() => set({ synonyms: cfg.synonyms.filter((_, j) => j !== i) })}><Trash2 aria-hidden="true" /></Button>
              </div>
            ))}
            <div><Button type="button" variant="outline" onClick={() => set({ synonyms: [...cfg.synonyms, []] })}><Plus aria-hidden="true" /> Agregar grupo de sinónimos</Button></div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle className="text-base">Productos fijados</CardTitle><CardDescription>Para un término, estos productos salen primero (aunque su nombre no coincida). Útil para lanzamientos y campañas.</CardDescription></CardHeader>
          <CardContent className="grid gap-3">
            {cfg.pins.map((p, i) => (
              <div key={i} className="grid gap-2 rounded-md border p-3">
                <div className="flex items-center gap-2">
                  <Input aria-label="Término" className="max-w-56" value={p.term} placeholder="tenis" onChange={(e) => set({ pins: cfg.pins.map((x, j) => (j === i ? { ...x, term: e.target.value } : x)) })} />
                  <Button type="button" variant="outline" onClick={() => setPicker({ kind: "pin", i })}><Plus aria-hidden="true" /> Producto</Button>
                  <Button type="button" size="icon" variant="ghost" className="ml-auto" aria-label="Quitar regla" onClick={() => set({ pins: cfg.pins.filter((_, j) => j !== i) })}><Trash2 aria-hidden="true" /></Button>
                </div>
                <div className="flex flex-wrap gap-1.5">{p.productIds.length === 0 ? <span className="text-sm text-muted-foreground">Agrega al menos un producto.</span> : p.productIds.map((id) => chip(id, () => set({ pins: cfg.pins.map((x, j) => (j === i ? { ...x, productIds: x.productIds.filter((y) => y !== id) } : x)) })))}</div>
              </div>
            ))}
            <div><Button type="button" variant="outline" onClick={() => set({ pins: [...cfg.pins, { term: "", productIds: [] }] })}><Plus aria-hidden="true" /> Agregar término</Button></div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle className="text-base">Redirecciones de búsqueda</CardTitle><CardDescription>Lleva un término directo a una página: “envíos” a tu política de envíos, “buen fin” a la colección de la campaña.</CardDescription></CardHeader>
          <CardContent className="grid gap-2">
            {cfg.redirects.map((r, i) => (
              <div key={i} className="flex flex-wrap items-center gap-2">
                <Input aria-label="Término" className="w-48" value={r.term} placeholder="envíos" onChange={(e) => set({ redirects: cfg.redirects.map((x, j) => (j === i ? { ...x, term: e.target.value } : x)) })} />
                <span aria-hidden="true">→</span>
                <Input aria-label="Página de destino" className="min-w-56 flex-1 font-mono" value={r.url} placeholder="/politicas/envios" onChange={(e) => set({ redirects: cfg.redirects.map((x, j) => (j === i ? { ...x, url: e.target.value } : x)) })} />
                <Button type="button" size="icon" variant="ghost" aria-label="Quitar redirección" onClick={() => set({ redirects: cfg.redirects.filter((_, j) => j !== i) })}><Trash2 aria-hidden="true" /></Button>
              </div>
            ))}
            <div><Button type="button" variant="outline" onClick={() => set({ redirects: [...cfg.redirects, { term: "", url: "" }] })}><Plus aria-hidden="true" /> Agregar redirección</Button></div>
          </CardContent>
        </Card>
        <Card data-search-learning>
          <CardHeader><CardTitle className="text-base">Aprender de tus clientes</CardTitle><CardDescription>Los productos que tus clientes abren y agregan al carrito después de buscar un término suben en los resultados de ese término. Lo que fijas sigue saliendo primero y lo oculto sigue oculto.</CardDescription></CardHeader>
          <CardContent><label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={!cfg.noLearning} onChange={(e) => set({ noLearning: !e.target.checked })} /> Ordenar los resultados con lo que eligen tus clientes</label></CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle className="text-base">Ocultos en la búsqueda</CardTitle><CardDescription>Productos que no aparecen al buscar ni en el catálogo general (siguen disponibles con su enlace directo).</CardDescription></CardHeader>
          <CardContent className="grid gap-3">
            <div className="flex flex-wrap gap-1.5">{cfg.hidden.length === 0 ? <span className="text-sm text-muted-foreground">Ningún producto oculto.</span> : cfg.hidden.map((id) => chip(id, () => set({ hidden: cfg.hidden.filter((y) => y !== id) })))}</div>
            <div><Button type="button" variant="outline" onClick={() => setPicker({ kind: "hidden" })}><Plus aria-hidden="true" /> Ocultar producto</Button></div>
          </CardContent>
        </Card>
        {canEdit && <div className="sticky bottom-4 z-10 flex w-fit gap-2 rounded-lg border bg-card p-2 shadow-md"><Button type="button" onClick={() => save.mutate()} disabled={!dirty || save.isPending}>{save.isPending ? "Guardando…" : "Guardar reglas"}</Button>{dirty && <Button type="button" variant="outline" onClick={() => q.data && setCfg(q.data.config)}>Descartar</Button>}</div>}
      </fieldset>
      <Card className="content-start xl:sticky xl:top-6 xl:self-start">
        <CardHeader><CardTitle className="text-base">Probar una búsqueda</CardTitle><CardDescription>Así responde tu tienda con las reglas guardadas.</CardDescription></CardHeader>
        <CardContent className="grid gap-3">
          <div className="relative"><Search className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden="true" /><Input aria-label="Buscar como cliente" className="pl-8" value={test} onChange={(e) => setTest(e.target.value)} placeholder="remera roja" /></div>
          {dirty && <Notice tone="warn">Guarda las reglas para probarlas.</Notice>}
          {preview.data && dtest.length >= 2 && (
            <div className="grid gap-2 text-sm">
              {preview.data.redirect && <Notice tone="ok">Lleva directo a <code>{preview.data.redirect}</code></Notice>}
              {preview.data.alternatives.length > 0 && <p className="text-muted-foreground">También busca: {preview.data.alternatives.join(", ")}</p>}
              <p className="font-medium">{preview.data.total} resultados</p>
              <ol className="grid gap-1">{preview.data.items.map((p) => <li key={p.id} className="flex items-center justify-between gap-2 rounded border px-2 py-1.5"><span className="truncate">{p.title}</span>{p.pinned ? <Badge variant="secondary">Fijado</Badge> : lmap[p.id] && !cfg.noLearning ? <Badge variant="outline" title={`${lmap[p.id]!.clicks} clics y ${lmap[p.id]!.carts} veces al carrito desde esta búsqueda`}>Elegido {lmap[p.id]!.clicks + lmap[p.id]!.carts}×</Badge> : null}</li>)}</ol>
              {preview.data.total === 0 && <p className="text-muted-foreground">Sin resultados. Agrega un sinónimo o una redirección para este término.</p>}
            </div>
          )}
        </CardContent>
      </Card>
      <ProductPicker open={!!picker} title={picker?.kind === "hidden" ? "Ocultar productos" : "Fijar productos"} exclude={picker?.kind === "hidden" ? cfg.hidden : picker ? cfg.pins[picker.i]?.productIds ?? [] : []} onClose={() => setPicker(null)}
        onPick={(ps) => {
          setNames({ ...names, ...Object.fromEntries(ps.map((p) => [p.id, p.title])) });
          const ids = ps.map((p) => p.id);
          if (picker?.kind === "hidden") set({ hidden: [...cfg.hidden, ...ids] });
          else if (picker) set({ pins: cfg.pins.map((x, j) => (j === picker.i ? { ...x, productIds: [...x.productIds, ...ids].slice(0, 12) } : x)) });
          setPicker(null);
        }} />
    </div>
  );
}

function Terms({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const [days, setDays] = useState("30");
  const q = useQuery({ queryKey: ["search-terms", days], queryFn: () => api.terms(Number(days)) });
  // A suggested synonym is added to the saved rules; nothing else changes.
  const addSyn = useMutation({
    mutationFn: async (v: { term: string; synonym: string }) => {
      const cur = await api.config();
      const syn = [...(cur.config.synonyms ?? []), [v.term, v.synonym]];
      return api.save({ ...cur.config, synonyms: syn });
    },
    onSuccess: (_, v) => { toast.success(`Listo: «${v.term}» ahora busca «${v.synonym}».`); void qc.invalidateQueries({ queryKey: ["search-config"] }); void qc.invalidateQueries({ queryKey: ["search-terms"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const table = (title: string, text: string, rows: Term[], zero: boolean) => (
    <Card>
      <CardHeader><CardTitle className="text-base">{title}</CardTitle><CardDescription>{text}</CardDescription></CardHeader>
      <CardContent>
        {q.isLoading ? <p className="text-sm text-muted-foreground">Cargando…</p> : rows.length === 0 ? <p className="text-sm text-muted-foreground">{zero ? "Nada por aquí: todas las búsquedas encontraron productos." : "Todavía no hay búsquedas en este periodo."}</p> : (
          <Table>
            <TableHeader><TableRow><TableHead>Término</TableHead><TableHead className="text-right">Búsquedas</TableHead>{!zero && <TableHead className="text-right">Resultados</TableHead>}{zero && <TableHead>Sugerencia</TableHead>}</TableRow></TableHeader>
            <TableBody>{rows.map((t) => <TableRow key={t.term}><TableCell className="font-medium">{t.term}</TableCell><TableCell className="text-right tabular-nums">{t.searches}</TableCell>{!zero && <TableCell className="text-right tabular-nums">{t.results === 0 ? <Badge variant="warn">0</Badge> : t.results}</TableCell>}
              {zero && <TableCell data-term-suggest>{t.suggest ? <div className="grid gap-1 text-sm">
                <span className="text-muted-foreground">Parecido a: {t.suggest.products.map((p) => p.title).join(" · ")}</span>
                {t.suggest.synonym && canEdit && <div><Button type="button" size="sm" variant="outline" disabled={addSyn.isPending} onClick={() => addSyn.mutate({ term: t.term, synonym: t.suggest!.synonym! })}>Agregar sinónimo: {t.suggest.synonym}</Button></div>}
              </div> : <span className="text-muted-foreground">Sin sugerencia</span>}</TableCell>}</TableRow>)}</TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
  return (
    <div className="grid gap-4">
      <div className="flex items-center gap-2"><Select value={days} onValueChange={setDays}><SelectTrigger className="w-44" aria-label="Periodo"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="7">Últimos 7 días</SelectItem><SelectItem value="30">Últimos 30 días</SelectItem><SelectItem value="90">Últimos 90 días</SelectItem></SelectContent></Select></div>
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      <div className="grid gap-5 lg:grid-cols-2">
        {table("Búsquedas sin resultados", "Lo que tus clientes quieren y no encuentran. Agrega un sinónimo, una redirección o el producto.", q.data?.noResults ?? [], true)}
        {table("Lo más buscado", "Los términos más usados en el buscador de tu tienda.", q.data?.top ?? [], false)}
      </div>
    </div>
  );
}

function Stars({ n }: { n: number }) {
  return <span className="inline-flex text-amber-500" role="img" aria-label={`${n} de 5 estrellas`}>{[1, 2, 3, 4, 5].map((i) => <Star key={i} className="size-4" fill={i <= n ? "currentColor" : "none"} aria-hidden="true" />)}</span>;
}

function Reviews({ canEdit, canDelete }: { canEdit: boolean; canDelete: boolean }) {
  const qc = useQueryClient();
  const [status, setStatus] = useState("pending");
  const q = useQuery({ queryKey: ["reviews", status], queryFn: () => api.reviews(status) });
  const [reply, setReply] = useState<Record<string, string>>({});
  const refresh = () => void qc.invalidateQueries({ queryKey: ["reviews"] });
  const act = useMutation({ mutationFn: (v: { id: string; status?: string; reply?: string }) => api.moderate(v.id, { status: v.status, reply: v.reply }), onSuccess: (_, v) => { toast.success(v.status === "approved" ? "Opinión publicada." : v.status === "rejected" ? "Opinión rechazada." : "Respuesta guardada."); refresh(); }, onError: (e) => toast.error(errorMessage(e)) });
  const del = useMutation({ mutationFn: (id: string) => api.remove(id), onSuccess: () => { toast.success("Opinión eliminada."); refresh(); }, onError: (e) => toast.error(errorMessage(e)) });
  const counts = q.data?.counts ?? {};
  const items = q.data?.items ?? [];
  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap gap-2" role="tablist" aria-label="Estado">
        {[["pending", "Por revisar"], ["approved", "Publicadas"], ["rejected", "Rechazadas"]].map(([k, l]) => (
          <Button key={k} role="tab" aria-selected={status === k} variant={status === k ? "default" : "outline"} size="sm" onClick={() => setStatus(k as string)}>{l} ({counts[k as string] ?? 0})</Button>
        ))}
      </div>
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {q.isLoading ? <div className="rounded-lg border"><Table><TableBody><SkeletonRows cols={3} /></TableBody></Table></div> : items.length === 0 ? (
        <EmptyState icon={<MessageSquare />} title={status === "pending" ? "Nada por revisar" : "Sin opiniones aquí"} text="Tus clientes pueden opinar desde la página de cada producto. Las opiniones se publican cuando las apruebas." />
      ) : items.map((r) => (
        <Card key={r.id}>
          <CardContent className="grid gap-3 pt-5">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex flex-wrap items-center gap-2"><Stars n={r.rating} /><strong>{r.title || "Sin título"}</strong>{r.verified && <Badge variant="ok">Compra verificada</Badge>}<StatusPill status={r.status} /></div>
              <span className="text-sm text-muted-foreground">{r.author} · {dateTime(r.createdAt)}</span>
            </div>
            <p className="text-sm">{r.body}</p>
            <p className="text-sm text-muted-foreground">Producto: {r.productHandle ? <a className="text-primary hover:underline" href={href("catalogo", "productos", r.productId)}>{r.productTitle}</a> : r.productTitle}</p>
            {canEdit && (
              <div className="grid gap-2 border-t pt-3">
                <Textarea aria-label={`Respuesta pública a ${r.author}`} rows={2} placeholder="Respuesta pública de la tienda (opcional)" value={reply[r.id] ?? r.reply} onChange={(e) => setReply({ ...reply, [r.id]: e.target.value })} />
                <div className="flex flex-wrap gap-2">
                  {r.status !== "approved" && <Button size="sm" onClick={() => act.mutate({ id: r.id, status: "approved", reply: reply[r.id] ?? r.reply })} disabled={act.isPending}>Publicar</Button>}
                  {r.status !== "rejected" && <Button size="sm" variant="outline" onClick={() => act.mutate({ id: r.id, status: "rejected" })} disabled={act.isPending}>Rechazar</Button>}
                  {(reply[r.id] ?? r.reply) !== r.reply && <Button size="sm" variant="outline" onClick={() => act.mutate({ id: r.id, reply: reply[r.id] })} disabled={act.isPending}>Guardar respuesta</Button>}
                  {canDelete && <Button size="sm" variant="ghost" className="ml-auto" onClick={() => del.mutate(r.id)} disabled={del.isPending}><Trash2 aria-hidden="true" /> Eliminar</Button>}
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

function Wishes() {
  const q = useQuery({ queryKey: ["wishes-top"], queryFn: api.wishes });
  const items = q.data?.items ?? [];
  return (
    <div className="grid gap-4">
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {!q.isLoading && items.length === 0 ? <EmptyState icon={<Heart />} title="Todavía nadie guarda favoritos" text="Cuando tus clientes con cuenta guarden productos con el corazón, aquí verás cuáles desean más: buena pista para promociones y reabasto." /> : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader><TableRow><TableHead>Producto</TableHead><TableHead className="text-right">Clientes que lo guardaron</TableHead><TableHead className="text-right">Disponibles</TableHead></TableRow></TableHeader>
            <TableBody>{q.isLoading ? <SkeletonRows cols={3} /> : items.map((w) => (
              <TableRow key={w.productId}><TableCell>{w.handle ? <a className="font-medium text-primary hover:underline" href={href("catalogo", "productos", w.productId)}>{w.title}</a> : w.title}</TableCell><TableCell className="text-right tabular-nums">{w.customers}</TableCell><TableCell className="text-right tabular-nums">{w.available === 0 ? <Badge variant="warn">Agotado</Badge> : w.available}</TableCell></TableRow>
            ))}</TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}
