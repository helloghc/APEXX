import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { ExternalLink, FileText, Plus } from "lucide-react";
import { errorMessage, fieldErrors, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { href, navigate } from "@/router";
import { dateTime, ErrorBox } from "../common";
import { sapi, type Policy } from "./api";
import { Confirm, EmptyState, Loading, ReadOnlyNote, SaveBar, Section, SubPage, SwitchRow } from "./kit";

const slugify = (s: string) => s.toLowerCase().normalize("NFD").replace(/[̀-ͯ]/g, "").replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "").slice(0, 60);

/** Policies: legal pages of the store (privacy, terms, shipping, returns). */
export function Policies({ me, slug }: { me: Me; slug?: string }) {
  const q = useQuery({ queryKey: ["settings", "policies"], queryFn: sapi.policies });
  const canEdit = can(me, "settings", "edit");
  if (q.isPending || q.error) return <SubPage title="Políticas"><ErrorBox error={q.error} onRetry={() => void q.refetch()} />{!q.error && <Loading />}</SubPage>;
  const items = q.data.items;
  if (slug) {
    const std = q.data.standard.find((s) => s.slug === slug);
    const found = items.find((p) => p.slug === slug) ?? (slug === "nueva" ? { slug: "", title: "", body: "", published: false, updatedAt: "" } : std ? { slug: std.slug, title: std.title, body: "", published: false, updatedAt: "" } : undefined);
    if (found) return <Editor key={slug} policy={found} isNew={!items.some((p) => p.slug === slug)} base={q.data.base} canEdit={canEdit} />;
  }
  const missing = q.data.standard.filter((s) => !items.some((p) => p.slug === s.slug));
  return (
    <SubPage title="Políticas" sub="Las páginas legales de tu tienda. Se enlazan en el pie de página y en el checkout." actions={canEdit && <Button onClick={() => navigate("configuracion", "politicas", "nueva")}><Plus aria-hidden="true" /> Nueva política</Button>}>
      <ReadOnlyNote show={!canEdit} need="Editor" />
      {items.length === 0 && <EmptyState icon={FileText} title="Todavía no tienes políticas" text="Empieza por el aviso de privacidad y los términos: tus clientes y los medios de pago los piden." />}
      {items.length > 0 && (
        <Section title="Tus políticas">
          <ul className="grid divide-y rounded-md border">
            {items.map((p) => (
              <li key={p.slug} className="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
                <a href={href("configuracion", "politicas", p.slug)} className="grid gap-0.5 hover:underline"><span className="font-medium">{p.title}</span><span className="text-xs text-muted-foreground">Actualizada {dateTime(p.updatedAt)}</span></a>
                <span className="flex items-center gap-2">{p.published ? <Badge variant="ok">Publicada</Badge> : <Badge variant="outline">Borrador</Badge>}{p.published && <a className="text-sm text-primary hover:underline" href={`${q.data.base}/politicas/${p.slug}`} target="_blank" rel="noreferrer">Ver <ExternalLink className="inline size-3.5" aria-hidden="true" /></a>}</span>
              </li>
            ))}
          </ul>
        </Section>
      )}
      {missing.length > 0 && canEdit && (
        <Section title="Recomendadas" description="Páginas que casi toda tienda necesita. Elige una para empezar a escribirla.">
          <div className="flex flex-wrap gap-2">{missing.map((s) => <Button key={s.slug} variant="outline" onClick={() => navigate("configuracion", "politicas", s.slug)}><Plus aria-hidden="true" /> {s.title}</Button>)}</div>
        </Section>
      )}
    </SubPage>
  );
}

function Editor({ policy, isNew, base, canEdit }: { policy: Policy; isNew: boolean; base: string; canEdit: boolean }) {
  const qc = useQueryClient();
  const [p, setP] = useState(policy);
  const [saved, setSaved] = useState(policy);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [del, setDel] = useState(false);
  const fresh = isNew && !policy.slug;
  useEffect(() => { if (fresh) setP((x) => ({ ...x, slug: slugify(x.title) })); }, [p.title, fresh]);
  const done = () => void qc.invalidateQueries({ queryKey: ["settings", "policies"] });
  const save = useMutation({
    mutationFn: () => sapi.savePolicy(p),
    onSuccess: (r) => { toast.success(r.published ? "Política publicada." : "Borrador guardado."); setSaved(r); setP(r); setErrors({}); done(); if (isNew) navigate("configuracion", "politicas", r.slug); },
    onError: (e) => { setErrors(fieldErrors(e)); toast.error(errorMessage(e)); },
  });
  const remove = useMutation({ mutationFn: () => sapi.deletePolicy(policy.slug), onSuccess: () => { toast.success("Política eliminada."); done(); navigate("configuracion", "politicas"); }, onError: (e) => { toast.error(errorMessage(e)); setDel(false); } });
  function submit() {
    const e: Record<string, string> = {};
    if (!p.title.trim()) e.title = "Escribe el título.";
    if (!p.slug) e.slug = "Escribe la dirección de la página.";
    if (p.published && p.body.trim().length < 20) e.body = "Escribe el contenido antes de publicar.";
    setErrors(e);
    if (Object.keys(e).length === 0) save.mutate();
  }
  return (
    <SubPage title={isNew ? "Nueva política" : policy.title} sub={<a className="hover:underline" href={href("configuracion", "politicas")}>← Todas las políticas</a>}
      actions={!isNew && canEdit && <Button variant="outline" onClick={() => setDel(true)}>Eliminar</Button>}>
      <ReadOnlyNote show={!canEdit} need="Editor" />
      <fieldset disabled={!canEdit} className="grid min-w-0 gap-5">
        <Section title="Contenido">
          <div className="grid gap-4 md:grid-cols-2">
            <FormField id="po-title" label="Título" error={errors.title}><Input value={p.title} onChange={(e) => setP({ ...p, title: e.target.value })} placeholder="Aviso de privacidad" /></FormField>
            <FormField id="po-slug" label="Dirección" error={errors.slug} description={`${base}/politicas/${p.slug || "…"}`}><Input className="font-mono" value={p.slug} disabled={!fresh} onChange={(e) => setP({ ...p, slug: slugify(e.target.value) })} /></FormField>
          </div>
          <FormField id="po-body" label="Texto" error={errors.body} description="Puedes usar títulos (## Título), listas (- punto), **negritas** y enlaces [texto](https://…). Deja una línea en blanco entre párrafos.">
            <Textarea rows={18} value={p.body} onChange={(e) => setP({ ...p, body: e.target.value })} />
          </FormField>
          <SwitchRow title="Publicada" text="Visible en tu tienda y disponible para enlazar en el checkout y los menús." checked={p.published} onChange={(v) => setP({ ...p, published: v })} />
        </Section>
      </fieldset>
      <SaveBar dirty={canEdit && (isNew || JSON.stringify(p) !== JSON.stringify(saved))} busy={save.isPending} onSave={submit} onDiscard={() => (isNew ? navigate("configuracion", "politicas") : setP(saved))} label={p.published ? "Guardar y publicar" : "Guardar borrador"} />
      <Confirm open={del} title={`¿Eliminar ${policy.title}?`} text="La página deja de existir en tu tienda y los enlaces hacia ella dejan de funcionar." confirmLabel="Eliminar" busy={remove.isPending} onConfirm={() => remove.mutate()} onClose={() => setDel(false)} />
    </SubPage>
  );
}
