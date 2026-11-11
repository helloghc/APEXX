import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { CornerUpRight, Plus, Trash2, Upload } from "lucide-react";
import { errorMessage, fieldErrors, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { ErrorBox, Notice } from "../common";
import { sapi, type Redirect } from "./api";
import { Confirm, EmptyState, Loading, ReadOnlyNote, SearchInput, Section, SubPage, Switch, useDebounced } from "./kit";
import { normalizePath, parseRedirectLines, validateRedirect } from "./logic";

/** Redirects: old URLs that must lead to new ones (301/302), with chain detection. */
export function Redirects({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "settings", "edit");
  const [search, setSearch] = useState("");
  const dq = useDebounced(search);
  const q = useQuery({ queryKey: ["settings", "redirects", dq], queryFn: () => sapi.redirects(dq) });
  const [sel, setSel] = useState<string[]>([]);
  const [add, setAdd] = useState(false);
  const [bulk, setBulk] = useState(false);
  const [del, setDel] = useState(false);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["settings", "redirects"] });
  const toggle = useMutation({ mutationFn: (r: Redirect) => sapi.updateRedirect(r), onSuccess: refresh, onError: (e) => toast.error(errorMessage(e)) });
  const remove = useMutation({ mutationFn: () => sapi.deleteRedirects(sel), onSuccess: (r) => { toast.success(`${r.deleted} redirecciones eliminadas.`); setSel([]); setDel(false); refresh(); }, onError: (e) => toast.error(errorMessage(e)) });
  const items = q.data?.items ?? [];
  return (
    <SubPage title="Redirecciones" sub="Cuando cambias la dirección de un producto o migras de otra plataforma, las redirecciones llevan a tus clientes (y a Google) a la página nueva."
      actions={canEdit && <><Button variant="outline" onClick={() => setBulk(true)}><Upload aria-hidden="true" /> Importar lista</Button><Button onClick={() => setAdd(true)}><Plus aria-hidden="true" /> Nueva redirección</Button></>}>
      <ReadOnlyNote show={!canEdit} need="Editor" />
      <Tester />
      <Section title={`Redirecciones${q.data ? ` (${q.data.total})` : ""}`} actions={<>{sel.length > 0 && canEdit && <Button variant="outline" onClick={() => setDel(true)}><Trash2 aria-hidden="true" /> Eliminar {sel.length}</Button>}<SearchInput value={search} onChange={setSearch} placeholder="Buscar ruta…" label="Buscar redirecciones" /></>}>
        <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
        {q.isPending ? <Loading /> : items.length === 0 ? (
          <EmptyState icon={CornerUpRight} title={dq ? "Sin resultados" : "Sin redirecciones"} text={dq ? "Ninguna ruta coincide con tu búsqueda." : "Crea una cuando cambies la dirección de una página, o importa la lista de tu plataforma anterior."} />
        ) : (
          <div className="overflow-x-auto rounded-md border">
            <Table>
              <TableHeader><TableRow><TableHead className="w-8"><span className="sr-only">Elegir</span></TableHead><TableHead>Desde</TableHead><TableHead>Hacia</TableHead><TableHead>Tipo</TableHead><TableHead>Activa</TableHead></TableRow></TableHeader>
              <TableBody>
                {items.map((r) => (
                  <TableRow key={r.id}>
                    <TableCell><Checkbox aria-label={`Elegir ${r.from}`} checked={sel.includes(r.id)} disabled={!canEdit} onCheckedChange={(c) => setSel(c ? [...sel, r.id] : sel.filter((x) => x !== r.id))} /></TableCell>
                    <TableCell className="font-mono text-sm">{r.from}</TableCell>
                    <TableCell className="max-w-sm truncate font-mono text-sm">{r.target}</TableCell>
                    <TableCell>{r.code === 301 ? "Permanente (301)" : "Temporal (302)"}</TableCell>
                    <TableCell><Switch label={`Activar ${r.from}`} checked={r.active} disabled={!canEdit} onChange={(v) => toggle.mutate({ ...r, active: v })} /></TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </Section>
      {add && <AddDialog onClose={() => setAdd(false)} onSaved={() => { setAdd(false); refresh(); }} />}
      {bulk && <BulkDialog onClose={() => setBulk(false)} onSaved={() => { setBulk(false); refresh(); }} />}
      <Confirm open={del} title={`¿Eliminar ${sel.length} redirecciones?`} text="Las rutas anteriores volverán a mostrar “página no encontrada”." confirmLabel="Eliminar" busy={remove.isPending} onConfirm={() => remove.mutate()} onClose={() => setDel(false)} />
    </SubPage>
  );
}

function Tester() {
  const [path, setPath] = useState("");
  const test = useMutation({ mutationFn: () => sapi.testRedirect(normalizePath(path)), onError: (e) => toast.error(errorMessage(e)) });
  const r = test.data;
  return (
    <Section title="Probar una ruta" description="Escribe una dirección anterior para ver a dónde llega hoy.">
      <form className="flex flex-wrap items-end gap-3" onSubmit={(e) => { e.preventDefault(); if (path.trim()) test.mutate(); }}>
        <FormField id="rd-test" label="Ruta"><Input className="w-72 font-mono" value={path} onChange={(e) => setPath(e.target.value)} placeholder="/producto-anterior" /></FormField>
        <Button type="submit" variant="outline" disabled={test.isPending}>Probar</Button>
      </form>
      {r && (r.redirects
        ? <Notice tone={r.hops.length > 1 ? "warn" : "ok"}>{r.hops.map((h) => `${h.from} → ${h.target} (${h.code})`).join(" · ")}. Destino final: <strong>{r.final}</strong>.{r.hops.length > 1 && " Hay una cadena: conviene apuntar la primera ruta directo al destino final."}</Notice>
        : <Notice tone="ok">{r.path} no tiene redirección.</Notice>)}
    </Section>
  );
}

function AddDialog({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const [from, setFrom] = useState("");
  const [target, setTarget] = useState("");
  const [code, setCode] = useState("301");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => sapi.createRedirect({ from: normalizePath(from), target: target.trim(), code: Number(code), active: true }),
    onSuccess: () => { toast.success("Redirección creada. Ya funciona."); onSaved(); },
    onError: (e) => { setErrors(fieldErrors(e)); toast.error(errorMessage(e)); },
  });
  function submit() { const e = validateRedirect(from, target); setErrors(e); if (Object.keys(e).length === 0) save.mutate(); }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader><DialogTitle>Nueva redirección</DialogTitle><DialogDescription>Quien visite la ruta anterior llegará a la nueva.</DialogDescription></DialogHeader>
        <div className="grid gap-4">
          <FormField id="rd-from" label="Ruta anterior" error={errors.from}><Input className="font-mono" value={from} onChange={(e) => setFrom(e.target.value)} placeholder="/tenis-rojos" /></FormField>
          <FormField id="rd-to" label="Lleva a" error={errors.target} description="Una ruta de tu tienda o una URL completa."><Input className="font-mono" value={target} onChange={(e) => setTarget(e.target.value)} placeholder="/p/tenis-norte" /></FormField>
          <div className="grid gap-1.5"><span className="text-sm font-medium">Tipo</span>
            <Select value={code} onValueChange={setCode}><SelectTrigger aria-label="Tipo"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="301">Permanente (301): la página cambió para siempre</SelectItem><SelectItem value="302">Temporal (302): solo por un tiempo</SelectItem></SelectContent></Select>
          </div>
        </div>
        <DialogFooter><Button variant="outline" onClick={onClose}>Cancelar</Button><Button onClick={submit} disabled={save.isPending}>Crear</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function BulkDialog({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const [text, setText] = useState("");
  const parsed = parseRedirectLines(text);
  const save = useMutation({
    mutationFn: () => sapi.importRedirects(parsed.items),
    onSuccess: (r) => { toast.success(`${r.items.length} redirecciones importadas.`); onSaved(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader><DialogTitle>Importar redirecciones</DialogTitle><DialogDescription>Pega una línea por redirección: ruta anterior, coma, ruta nueva. Puedes copiarlas de una hoja de cálculo.</DialogDescription></DialogHeader>
        <Textarea rows={10} className="font-mono" aria-label="Lista de redirecciones" value={text} onChange={(e) => setText(e.target.value)} placeholder={"/producto-anterior,/p/producto-nuevo\n/categoria/zapatos,/c/calzado"} />
        {parsed.errors.length > 0 && <Notice tone="warn">{parsed.errors.slice(0, 5).join(" · ")}{parsed.errors.length > 5 && ` · y ${parsed.errors.length - 5} más`}</Notice>}
        <DialogFooter><Button variant="outline" onClick={onClose}>Cancelar</Button><Button onClick={() => save.mutate()} disabled={save.isPending || parsed.items.length === 0 || parsed.errors.length > 0}>Importar {parsed.items.length || ""}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
