import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { Loading, ReadOnlyNote, Section, SubPage } from "./kit";

interface Status { source: string; newPrices?: boolean; products?: number; lastRun?: string; lastError?: string; copied?: number; failed?: string[] }
const api = {
  get: () => request<Status>("GET", "/v1/settings/catalog-source"),
  save: (b: { source: string; apiKey: string; newPrices: boolean }) => request<Status>("PUT", "/v1/settings/catalog-source", b),
  sync: () => request<Status>("POST", "/v1/settings/catalog-source/sync"),
  stop: () => request<void>("DELETE", "/v1/settings/catalog-source", undefined, { as: "none" }),
};

/** Master catalog: this store copies its products from another store of the business. */
export function CatalogSource({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "settings", "admin");
  const q = useQuery({ queryKey: ["settings", "catalog-source"], queryFn: api.get });
  const [source, setSource] = useState("");
  const [key, setKey] = useState("");
  const [prices, setPrices] = useState(true);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const done = (s: Status | void) => { if (s) qc.setQueryData(["settings", "catalog-source"], s); else void qc.invalidateQueries({ queryKey: ["settings", "catalog-source"] }); };
  const save = useMutation({ mutationFn: () => api.save({ source: source.trim(), apiKey: key.trim(), newPrices: prices }), onSuccess: (s) => { setKey(""); setErrors({}); done(s); toast.success(`Catálogo conectado: ${s.products ?? 0} productos copiados.`); }, onError: (e) => { setErrors(fieldErrors(e)); toast.error(errorMessage(e)); } });
  const sync = useMutation({ mutationFn: api.sync, onSuccess: (s) => { done(s); toast.success(s.copied ? `${s.copied} productos actualizados.` : "Todo estaba al día."); }, onError: (e) => toast.error(errorMessage(e)) });
  const stop = useMutation({ mutationFn: api.stop, onSuccess: () => { done(); toast.success("Ya no se copia el catálogo. Los productos se quedan en tu tienda."); }, onError: (e) => toast.error(errorMessage(e)) });
  if (q.error) return <ErrorBox error={q.error} onRetry={() => void q.refetch()} />;
  if (!q.data) return <Loading />;
  const s = q.data;
  return (
    <SubPage title="Catálogo maestro" sub="Toma los productos de otra de tus tiendas y mantenlos al día solos. Nombres, descripciones y variantes vienen del origen; precios, existencias, categorías e imágenes los decides aquí.">
      <ReadOnlyNote show={!canEdit} need="Administrador" />
      {s.source ? (
        <Section title={`Copiando de la tienda ${s.source}`} actions={canEdit && <div className="flex gap-2"><Button variant="outline" onClick={() => stop.mutate()} disabled={stop.isPending}>Dejar de copiar</Button><Button onClick={() => sync.mutate()} disabled={sync.isPending}>{sync.isPending ? "Revisando…" : "Actualizar ahora"}</Button></div>}>
          <p className="text-sm" data-testid="catalog-source-status">{s.products ?? 0} productos vienen del origen · se revisa cada minuto{s.lastRun ? ` · última revisión ${new Date(s.lastRun).toLocaleString("es-MX", { dateStyle: "medium", timeStyle: "short" })}` : ""}{s.copied ? ` · ${s.copied} actualizados` : ""}</p>
          {s.lastError && <p className="text-sm text-bad" role="alert">{s.lastError}</p>}
          {(s.failed?.length ?? 0) > 0 && <div className="text-sm"><p className="font-medium">No se pudieron copiar:</p><ul className="list-disc pl-5 text-muted-foreground">{s.failed!.map((f) => <li key={f}>{f}</li>)}</ul></div>}
          <p className="text-sm text-muted-foreground">Si un producto se borra en el origen, aquí se archiva: sus pedidos y su historial se conservan.</p>
        </Section>
      ) : null}
      {canEdit && (
        <Section title={s.source ? "Cambiar la tienda de origen" : "Conectar una tienda de origen"}>
          <form className="grid gap-4 sm:grid-cols-2" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
            <FormField id="cs-source" label="Identificador de la tienda de origen" error={errors.source}><Input className="font-mono" value={source} onChange={(e) => setSource(e.target.value)} placeholder="mi-tienda-mx" /></FormField>
            <FormField id="cs-key" label="Llave de API de esa tienda" error={errors.apiKey} description="Créala en la tienda de origen (Equipo → Crear API key) con permiso de lectura de catálogo. Si la revocas, la copia se detiene."><Input type="password" autoComplete="off" value={key} onChange={(e) => setKey(e.target.value)} placeholder="apx_…" /></FormField>
            <label className="flex items-start gap-2 text-sm sm:col-span-2"><input type="checkbox" className="mt-1" checked={prices} onChange={(e) => setPrices(e.target.checked)} /><span>Los productos nuevos empiezan con el precio del origen<span className="block text-muted-foreground">Después puedes cambiarlo aquí; la copia nunca toca tus precios.</span></span></label>
            <div className="sm:col-span-2"><Button type="submit" disabled={save.isPending || !source.trim() || !key.trim()}>{save.isPending ? "Conectando…" : "Conectar y copiar"}</Button></div>
          </form>
        </Section>
      )}
    </SubPage>
  );
}
