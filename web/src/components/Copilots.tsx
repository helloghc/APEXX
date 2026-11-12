import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Sparkles } from "lucide-react";
import { errorMessage, request } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { toast } from "@/components/ui/sonner";
import { href, navigate } from "@/router";

interface Sug { id: string; feature: string; targetId: string; proposal: Record<string, unknown>; status: string; sources: { text: string }[]; title?: string }

const FEATURE: Record<string, string> = { attributes: "Atributos", category: "Categoría", seo: "Buscadores (SEO)", duplicate: "Posible duplicado" };
const STATUS: Record<string, string> = { proposed: "Por revisar", applied: "Aplicada", rejected: "Rechazada", undone: "Deshecha" };

function proposalText(s: Sug): string {
  const p = s.proposal;
  if (s.feature === "attributes") return Object.entries((p.attributes as Record<string, string>) ?? {}).map(([k, v]) => `${k}: ${v}`).join(", ");
  if (s.feature === "seo") return [p.seoTitle && `Título: ${p.seoTitle}`, p.seoDescription && `Descripción: ${p.seoDescription}`].filter(Boolean).join(" · ");
  if (s.feature === "duplicate") return String(p.title ?? "");
  if (s.feature === "category") return "Asignar la categoría propuesta";
  return "";
}

function useSugActions(keys: unknown[][]) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { id: string; act: "apply" | "undo" | "reject" }) => request("POST", `/v1/ai/suggestions/${encodeURIComponent(v.id)}/${v.act}`, undefined, { as: v.act === "reject" ? "none" : "json" }),
    onSuccess: (_, v) => { toast.success(v.act === "apply" ? "Aplicada. Ya se ve en la tienda." : v.act === "undo" ? "Deshecha: el producto quedó como estaba." : "Rechazada. No se volverá a proponer."); keys.forEach((k) => void qc.invalidateQueries({ queryKey: k })); },
    onError: (e) => toast.error(errorMessage(e)),
  });
}

function SugRow({ s, canEdit, act, busy, showTitle }: { s: Sug; canEdit: boolean; act: (id: string, a: "apply" | "undo" | "reject") => void; busy: boolean; showTitle?: boolean }) {
  return (
    <li className="grid gap-1 rounded-md border p-3">
      <div className="flex flex-wrap items-center gap-2"><Badge variant="outline">{FEATURE[s.feature] ?? s.feature}</Badge>{showTitle && s.title && <a className="font-medium text-primary hover:underline" href={href("catalogo", "productos", s.targetId)}>{s.title}</a>}<span className="text-muted-foreground">{STATUS[s.status] ?? s.status}</span></div>
      {proposalText(s) && <p className="break-words">{proposalText(s)}</p>}
      {s.sources?.map((x, i) => <p key={i} className="text-muted-foreground">{x.text}</p>)}
      {canEdit && <div className="flex flex-wrap gap-2 pt-1">
        {s.status === "proposed" && s.feature !== "duplicate" && <Button type="button" size="sm" disabled={busy} onClick={() => act(s.id, "apply")}>Aplicar</Button>}
        {s.status === "applied" && <Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => act(s.id, "undo")}>Deshacer</Button>}
        {s.status === "proposed" && <Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => act(s.id, "reject")}>{s.feature === "duplicate" ? "Revisado" : "Rechazar"}</Button>}
      </div>}
    </li>
  );
}

/** Suggestions for one product, in its editor. */
export function ProductSuggestions({ productId, canEdit }: { productId: string; canEdit: boolean }) {
  const key = ["suggestions", productId];
  const q = useQuery({ queryKey: key, queryFn: () => request<{ items: Sug[] }>("GET", `/v1/products/${encodeURIComponent(productId)}/suggestions`).then((r) => r.items) });
  const m = useSugActions([key, ["product", productId]]);
  const items = q.data ?? [];
  if (q.isLoading) return <p className="text-sm text-muted-foreground">Revisando el producto…</p>;
  if (items.length === 0) return <p className="text-sm text-muted-foreground">Nada que sugerir: el producto se ve completo.</p>;
  return <ul className="grid gap-2 text-sm" data-product-suggestions>{items.map((s) => <SugRow key={s.id} s={s} canEdit={canEdit} busy={m.isPending} act={(id, act) => m.mutate({ id, act })} />)}</ul>;
}

/** Suggestions across the catalog, with a scan button. */
export function CatalogSuggestions({ canEdit }: { canEdit: boolean }) {
  const [status, setStatus] = useState("proposed");
  const key = ["suggestions", "catalog", status];
  const q = useQuery({ queryKey: key, queryFn: () => request<{ items: Sug[] }>("GET", `/v1/ai/suggestions?status=${status}`).then((r) => r.items) });
  const qc = useQueryClient();
  const m = useSugActions([["suggestions", "catalog"]]);
  const scan = useMutation({
    mutationFn: () => request<{ new: number }>("POST", "/v1/ai/suggestions/scan"),
    onSuccess: (r) => { toast.success(r.new ? `${r.new} sugerencias nuevas.` : "Nada nuevo que sugerir."); void qc.invalidateQueries({ queryKey: ["suggestions", "catalog"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const items = q.data ?? [];
  return (
    <div className="grid gap-3 text-sm" data-catalog-suggestions>
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex flex-wrap gap-2" role="tablist" aria-label="Estado">
          {(["proposed", "applied", "rejected"] as const).map((k) => <Button key={k} type="button" role="tab" aria-selected={status === k} size="sm" variant={status === k ? "default" : "outline"} onClick={() => setStatus(k)}>{STATUS[k]}</Button>)}
        </div>
        {canEdit && <Button type="button" size="sm" variant="outline" disabled={scan.isPending} onClick={() => scan.mutate()}>{scan.isPending ? "Revisando…" : "Revisar el catálogo"}</Button>}
      </div>
      {q.isLoading ? <p className="text-muted-foreground">Cargando…</p> : items.length === 0 ? <p className="text-muted-foreground">No hay sugerencias aquí.</p> :
        <ul className="grid gap-2">{items.map((s) => <SugRow key={s.id} s={s} canEdit={canEdit} busy={m.isPending} showTitle act={(id, act) => m.mutate({ id, act })} />)}</ul>}
    </div>
  );
}

interface Rules { match: "all" | "any"; conditions: { field: string; op: string; value: unknown }[] }

/** "Describe your segment": rules the segment dialog opens prefilled. */
export function DescribeSegment({ onRules }: { onRules: (name: string, rules: Rules, explain: string[]) => void }) {
  const [q, setQ] = useState("");
  const m = useMutation({
    mutationFn: (text: string) => request<{ understood: boolean; rules?: Rules; explain: string[]; count?: number; message?: string }>("POST", "/v1/ai/segment", { q: text }),
    onSuccess: (r) => { if (r.understood && r.rules) onRules(q.trim(), r.rules, r.explain); else toast.error(r.message ?? "No entendí condiciones."); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <form className="flex flex-wrap items-center gap-2 text-sm" data-describe-segment onSubmit={(e) => { e.preventDefault(); if (q.trim().length >= 3) m.mutate(q.trim()); }}>
      <Sparkles className="size-4 text-muted-foreground" aria-hidden="true" />
      <label htmlFor="seg-q" className="sr-only">Describe el segmento</label>
      <input id="seg-q" className="h-9 min-w-0 flex-1 basis-72 rounded-md border bg-background px-3" value={q} maxLength={300} onChange={(e) => setQ(e.target.value)} placeholder="Describe el segmento: compraron más de 3 veces y aceptan correos" />
      <Button type="submit" variant="outline" disabled={m.isPending || q.trim().length < 3}>{m.isPending ? "Leyendo…" : "Armar reglas"}</Button>
    </form>
  );
}

/** "Describe the promotion": a draft checked by the promotion engine. */
export function DescribePromotion() {
  const [q, setQ] = useState("");
  const qc = useQueryClient();
  const m = useMutation({ mutationFn: (text: string) => request<{ understood: boolean; promotion?: Record<string, unknown>; explain: string[]; summary?: string; problems?: string[]; message?: string }>("POST", "/v1/ai/promotion", { q: text }) });
  const create = useMutation({
    mutationFn: (p: Record<string, unknown>) => request<{ id?: string; promotion?: { id: string } }>("POST", "/v1/promotions", p),
    onSuccess: (r) => { toast.success("Promoción creada como borrador. Revísala y actívala cuando quieras."); void qc.invalidateQueries({ queryKey: ["promotions"] }); const id = r.id ?? r.promotion?.id; if (id) navigate("promociones", "p", id); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const r = m.data;
  return (
    <div className="grid gap-2 rounded-lg border bg-card p-3 text-sm" data-describe-promotion>
      <form className="flex flex-wrap items-center gap-2" onSubmit={(e) => { e.preventDefault(); if (q.trim().length >= 3) m.mutate(q.trim()); }}>
        <Sparkles className="size-4 text-muted-foreground" aria-hidden="true" />
        <label htmlFor="promo-q" className="sr-only">Describe la promoción</label>
        <input id="promo-q" className="h-9 min-w-0 flex-1 basis-72 rounded-md border bg-background px-3" value={q} maxLength={300} onChange={(e) => setQ(e.target.value)} placeholder="Describe la promoción: 3x2 en calcetas hasta el 30 de noviembre" />
        <Button type="submit" variant="outline" disabled={m.isPending || q.trim().length < 3}>{m.isPending ? "Leyendo…" : "Armar promoción"}</Button>
      </form>
      <div role="status" aria-live="polite" className="grid gap-1">
        {m.error && <p className="text-destructive">{errorMessage(m.error)}</p>}
        {r && !r.understood && <p className="text-muted-foreground">{r.message}</p>}
        {r?.understood && <>
          {r.summary && <p className="font-medium">{r.summary}</p>}
          <ul className="list-disc pl-5 text-muted-foreground">{r.explain.map((x, i) => <li key={i}>{x}</li>)}</ul>
          {(r.problems?.length ?? 0) > 0 && <ul className="text-destructive">{r.problems!.map((x, i) => <li key={i}>{x}</li>)}</ul>}
          {r.promotion && (r.problems?.length ?? 0) === 0 && <div><Button type="button" size="sm" disabled={create.isPending} onClick={() => create.mutate(r.promotion!)}>Crear como borrador</Button></div>}
        </>}
      </div>
    </div>
  );
}

/** Order: what happened and a reply for the customer, to copy. */
export function OrderAssist({ orderId }: { orderId: string }) {
  const q = useQuery({ queryKey: ["order-assist", orderId], queryFn: () => request<{ summary: string; reply: string }>("GET", `/v1/ai/orders/${encodeURIComponent(orderId)}`) });
  if (!q.data) return null;
  return (
    <div className="grid gap-2 text-sm" data-order-assist>
      <p>{q.data.summary}</p>
      <label htmlFor="order-reply" className="font-medium">Respuesta sugerida para el cliente</label>
      <textarea id="order-reply" readOnly rows={4} className="rounded-md border bg-muted/40 p-2" value={q.data.reply} />
      <div><Button type="button" size="sm" variant="outline" onClick={() => { void navigator.clipboard?.writeText(q.data!.reply).then(() => toast.success("Respuesta copiada.")).catch(() => toast.error("No se pudo copiar; selecciónala y cópiala.")); }}>Copiar respuesta</Button></div>
    </div>
  );
}

/** Texts in the store's voice (needs a language model). */
export function WriteHelper({ kind, about, label, onPick }: { kind: string; about: string; label: string; onPick: (text: string) => void }) {
  const m = useMutation({ mutationFn: () => request<{ available: boolean; options?: string[]; message?: string; check?: string[] }>("POST", "/v1/ai/write", { kind, about }) });
  const r = m.data;
  return (
    <div className="grid gap-2 text-sm" data-write-helper>
      <div><Button type="button" size="sm" variant="outline" disabled={m.isPending || about.trim().length < 3} onClick={() => m.mutate()}><Sparkles aria-hidden="true" /> {m.isPending ? "Escribiendo…" : label}</Button></div>
      <div role="status" aria-live="polite">
        {m.error && <p className="text-destructive">{errorMessage(m.error)}</p>}
        {r && !r.available && <p className="text-muted-foreground">{r.message}</p>}
        {r?.available && <ul className="grid gap-1">{r.options?.map((o, i) => <li key={i} className="flex flex-wrap items-center gap-2"><span className="min-w-0 flex-1 break-words">{o}</span><Button type="button" size="sm" variant="outline" onClick={() => onPick(o)}>Usar</Button></li>)}</ul>}
        {(r?.check?.length ?? 0) > 0 && <p className="text-amber-700 dark:text-amber-400">Revisa estas cifras: no venían en lo que escribiste ({r!.check!.join(", ")}).</p>}
      </div>
    </div>
  );
}
