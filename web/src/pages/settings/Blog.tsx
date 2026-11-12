import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ExternalLink, ImagePlus, Plus, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { MediaPicker } from "../catalog/shared";
import { ErrorBox } from "../common";
import { Loading, Section, SubPage } from "./kit";

interface Post { id?: string; handle: string; title: string; excerpt: string; body: string; cover: string; author: string; tags: string[]; status: "draft" | "published"; publishAt?: string | null }
const empty: Post = { handle: "", title: "", excerpt: "", body: "", cover: "", author: "", tags: [], status: "draft", publishAt: null };
// Plain paragraphs become <p>; HTML is kept (the server cleans it).
const toHTML = (t: string) => (/<[a-z][\s\S]*>/i.test(t) ? t : t.split(/\n{2,}/).map((p) => p.trim()).filter(Boolean).map((p) => `<p>${p.replace(/[<>&]/g, (c) => ({ "<": "&lt;", ">": "&gt;", "&": "&amp;" })[c]!).replace(/\n/g, "<br>")}</p>`).join("\n"));
const local = (iso?: string | null) => (iso ? new Date(new Date(iso).getTime() - new Date().getTimezoneOffset() * 60000).toISOString().slice(0, 16) : "");

/** Blog: posts with cover, tags and a publication time (scheduled posts go live by themselves). */
export function Blog({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "builder", "edit");
  const q = useQuery({ queryKey: ["blog"], queryFn: () => request<{ items: Post[] }>("GET", "/v1/blog").then((r) => r.items) });
  const [edit, setEdit] = useState<(Post & { tagsText: string; when: string }) | null>(null);
  const [preview, setPreview] = useState("");
  const [pick, setPick] = useState(false);
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  async function open(id?: string) {
    setErrs({});
    if (!id) { setEdit({ ...empty, tagsText: "", when: "" }); setPreview(""); return; }
    try {
      const r = await request<{ post: Post; previewUrl: string }>("GET", `/v1/blog/${id}`);
      setEdit({ ...r.post, tagsText: r.post.tags.join(", "), when: local(r.post.publishAt) }); setPreview(r.previewUrl);
    } catch (e) { toast.error(errorMessage(e)); }
  }
  async function save() {
    if (!edit) return;
    setBusy(true); setErrs({});
    const { tagsText, when, ...p } = edit;
    const body = { ...p, body: toHTML(p.body), tags: tagsText.split(/[,]+/).map((t) => t.trim()).filter(Boolean), publishAt: when ? new Date(when).toISOString() : null };
    try {
      const r = await request<{ post: Post; previewUrl: string }>(p.id ? "PUT" : "POST", p.id ? `/v1/blog/${p.id}` : "/v1/blog", body);
      setEdit({ ...r.post, tagsText: r.post.tags.join(", "), when: local(r.post.publishAt) }); setPreview(r.previewUrl);
      const future = r.post.publishAt && new Date(r.post.publishAt) > new Date();
      toast.success(r.post.status === "draft" ? "Borrador guardado." : future ? "Programado: se publica solo en su fecha." : "Publicado en tu blog.");
      void qc.invalidateQueries({ queryKey: ["blog"] });
    } catch (e) { setErrs(fieldErrors(e)); toast.error(errorMessage(e)); } finally { setBusy(false); }
  }
  async function remove(p: Post) {
    if (!window.confirm(`¿Borrar «${p.title}»?`)) return;
    try { await request("DELETE", `/v1/blog/${p.id}`, undefined, { as: "none" }); toast.success("Artículo borrado."); setEdit(null); void qc.invalidateQueries({ queryKey: ["blog"] }); } catch (e) { toast.error(errorMessage(e)); }
  }
  const state = (p: Post) => (p.status === "draft" ? "Borrador" : p.publishAt && new Date(p.publishAt) > new Date() ? `Programado para ${new Date(p.publishAt).toLocaleString("es-MX", { dateStyle: "medium", timeStyle: "short" })}` : "Publicado");
  return (
    <SubPage title="Blog" sub="Escribe artículos para tu tienda. Si eliges una fecha futura, el artículo se publica solo en ese momento; antes puedes verlo con el enlace de vista previa." actions={canEdit && !edit && <Button onClick={() => void open()}><Plus className="size-4" /> Nuevo artículo</Button>}>
      {edit && (
        <Section title={edit.id ? "Editar artículo" : "Nuevo artículo"} actions={<div className="flex gap-2">{preview && <a className="inline-flex items-center gap-1 text-sm text-primary hover:underline" href={preview} target="_blank" rel="noopener"><ExternalLink className="size-4" /> Vista previa</a>}{edit.id && canEdit && <Button variant="ghost" size="icon" aria-label="Borrar artículo" onClick={() => void remove(edit)}><Trash2 className="size-4" /></Button>}</div>}>
          <fieldset disabled={!canEdit} className="grid gap-3 sm:grid-cols-2" data-post-form>
            <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="bp-title">Título</Label><Input id="bp-title" value={edit.title} onChange={(e) => setEdit({ ...edit, title: e.target.value })} />{errs.title && <p className="text-xs text-bad">{errs.title}</p>}</div>
            <div className="grid gap-1.5"><Label htmlFor="bp-handle">Dirección (opcional)</Label><Input id="bp-handle" className="font-mono text-xs" value={edit.handle} onChange={(e) => setEdit({ ...edit, handle: e.target.value })} placeholder="se crea del título" />{errs.handle && <p className="text-xs text-bad">{errs.handle}</p>}</div>
            <div className="grid gap-1.5"><Label htmlFor="bp-author">Autor</Label><Input id="bp-author" value={edit.author} onChange={(e) => setEdit({ ...edit, author: e.target.value })} /></div>
            <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="bp-excerpt">Resumen (para la lista y buscadores)</Label><Input id="bp-excerpt" value={edit.excerpt} onChange={(e) => setEdit({ ...edit, excerpt: e.target.value })} /></div>
            <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="bp-body">Texto (deja una línea en blanco entre párrafos)</Label><Textarea id="bp-body" rows={12} value={edit.body} onChange={(e) => setEdit({ ...edit, body: e.target.value })} />{errs.body && <p className="text-xs text-bad">{errs.body}</p>}</div>
            <div className="grid gap-1.5"><Label htmlFor="bp-tags">Temas (separados por coma)</Label><Input id="bp-tags" value={edit.tagsText} onChange={(e) => setEdit({ ...edit, tagsText: e.target.value })} /></div>
            <div className="grid gap-1.5"><Label>Portada</Label><div className="flex items-center gap-2"><Button type="button" variant="outline" size="sm" onClick={() => setPick(true)}><ImagePlus className="size-4" /> {edit.cover ? "Cambiar imagen" : "Elegir imagen"}</Button>{edit.cover && <Button type="button" variant="ghost" size="sm" onClick={() => setEdit({ ...edit, cover: "" })}>Quitar</Button>}</div></div>
            <div className="grid gap-1.5"><Label htmlFor="bp-status">Estado</Label><select id="bp-status" className="h-9 rounded-md border bg-background px-2 text-sm" value={edit.status} onChange={(e) => setEdit({ ...edit, status: e.target.value as Post["status"] })}><option value="draft">Borrador</option><option value="published">Publicar</option></select></div>
            <div className="grid gap-1.5"><Label htmlFor="bp-when">Publicar el (vacío = ahora)</Label><Input id="bp-when" type="datetime-local" value={edit.when} onChange={(e) => setEdit({ ...edit, when: e.target.value })} /></div>
          </fieldset>
          <div className="flex gap-2">{canEdit && <Button disabled={busy} onClick={() => void save()}>{busy ? "Guardando…" : "Guardar"}</Button>}<Button variant="ghost" onClick={() => setEdit(null)}>Cerrar</Button></div>
          <MediaPicker open={pick} multiple={false} title="Portada del artículo" onClose={() => setPick(false)} onPick={(m) => { if (m[0]) setEdit({ ...edit, cover: m[0].id }); setPick(false); }} />
        </Section>
      )}
      {!edit && (q.data.length === 0 ? <p className="text-sm text-muted-foreground">Aún no hay artículos.</p> : (
        <ul className="divide-y rounded-md border" data-post-list>{q.data.map((p) => <li key={p.id} className="flex flex-wrap items-center justify-between gap-2 p-3 text-sm"><button type="button" className="font-medium text-primary hover:underline" onClick={() => void open(p.id)}>{p.title}</button><span className="text-muted-foreground">{state(p)}</span></li>)}</ul>
      ))}
    </SubPage>
  );
}
