import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Copy, Globe, Plus, RefreshCw, Trash2 } from "lucide-react";
import { errorMessage, request, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { dateTime, ErrorBox, Notice } from "../common";
import { sapi, type Domain } from "./api";
import { Confirm, EmptyState, Loading, ReadOnlyNote, Section, SubPage } from "./kit";
import { validateHostname } from "./logic";

const copy = (v: string) => navigator.clipboard.writeText(v).then(() => toast.success("Copiado."), () => toast.error("No se pudo copiar."));

/** Domains: connect your own domain with a DNS check and choose the main one. */
export function Domains({ me }: { me: Me }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["settings", "domains"], queryFn: sapi.domains });
  const canEdit = can(me, "settings", "admin");
  const [host, setHost] = useState("");
  const [site, setSite] = useState("main");
  const fronts = useQuery({ queryKey: ["storefronts"], queryFn: () => request<{ items: { id: string; name: string }[] }>("GET", "/v1/storefronts").then((r) => r.items) });
  const [error, setError] = useState<string | null>(null);
  const [del, setDel] = useState<Domain | null>(null);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["settings", "domains"] });
  const add = useMutation({ mutationFn: () => sapi.createDomain(host.trim().toLowerCase(), site), onSuccess: () => { toast.success("Dominio agregado. Ahora configura tu DNS."); setHost(""); refresh(); }, onError: (e) => setError(errorMessage(e)) });
  const verify = useMutation({ mutationFn: (id: string) => sapi.verifyDomain(id), onSuccess: (d) => { if (d.status === "verified") toast.success(`${d.hostname} está conectado.`); else toast.error(d.lastError || "Todavía no vemos los registros DNS."); refresh(); }, onError: (e) => toast.error(errorMessage(e)) });
  const primary = useMutation({ mutationFn: (id: string) => sapi.primaryDomain(id), onSuccess: () => { toast.success("Dominio principal actualizado."); refresh(); }, onError: (e) => toast.error(errorMessage(e)) });
  const remove = useMutation({ mutationFn: (id: string) => sapi.deleteDomain(id), onSuccess: () => { toast.success("Dominio eliminado."); setDel(null); refresh(); }, onError: (e) => toast.error(errorMessage(e)) });
  const items = q.data?.items ?? [];
  const row = (label: string, value: string) => (
    <div className="flex flex-wrap items-center gap-2 text-sm"><span className="w-28 text-muted-foreground">{label}</span><code className="rounded bg-muted px-2 py-1 font-mono text-xs break-all">{value}</code><Button size="icon" variant="ghost" aria-label={`Copiar ${label}`} onClick={() => void copy(value)}><Copy aria-hidden="true" /></Button></div>
  );
  return (
    <SubPage title="Dominios" sub="Conecta tu propio dominio (por ejemplo www.tutienda.mx). Mientras tanto tu tienda siempre funciona en la dirección de Apexx.">
      <ReadOnlyNote show={!canEdit} />
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {q.data && <Notice tone="ok">Dirección de Apexx: <a className="underline" href={q.data.defaultUrl} target="_blank" rel="noreferrer">{q.data.defaultUrl}</a></Notice>}
      {canEdit && (
        <Section title="Conectar un dominio">
          <form className="flex flex-wrap items-end gap-3" onSubmit={(e) => { e.preventDefault(); const v = validateHostname(host.trim().toLowerCase()); setError(v); if (!v) add.mutate(); }}>
            <FormField id="dm-host" label="Dominio" error={error ?? undefined}><Input className="w-72" value={host} onChange={(e) => setHost(e.target.value)} placeholder="www.tutienda.mx" /></FormField>
            {(fronts.data?.length ?? 0) > 1 && (
              <div className="grid gap-1"><label htmlFor="dm-site" className="text-sm font-medium">Tienda</label>
                <select id="dm-site" className="h-9 rounded-md border bg-background px-2 text-sm" value={site} onChange={(e) => setSite(e.target.value)}>
                  {fronts.data?.map((f) => <option key={f.id} value={f.id}>{f.name}</option>)}
                </select></div>
            )}
            <Button type="submit" disabled={add.isPending}><Plus aria-hidden="true" /> Agregar</Button>
          </form>
        </Section>
      )}
      {q.isPending ? <Loading /> : items.length === 0 ? <EmptyState icon={Globe} title="Sin dominios propios" text="Agrega tu dominio y te diremos exactamente qué registros crear con tu proveedor (GoDaddy, Cloudflare, Google Domains…)." /> : items.map((d) => (
        <Section key={d.id} title={d.hostname} description={d.verifiedAt ? `Conectado el ${dateTime(d.verifiedAt)}` : d.lastCheckedAt ? `Última revisión: ${dateTime(d.lastCheckedAt)}` : "Aún sin revisar"}
          actions={<>{d.primary && <Badge variant="secondary">Principal</Badge>}<Badge variant={d.status === "verified" ? "ok" : d.status === "failed" ? "bad" : "warn"}>{d.status === "verified" ? "Conectado" : d.status === "failed" ? "No se pudo verificar" : "Pendiente de DNS"}</Badge></>}>
          {d.status !== "verified" && (
            <div className="grid gap-2">
              <p className="text-sm">Crea estos dos registros con tu proveedor de dominio y luego pulsa “Verificar”. Los cambios de DNS pueden tardar unos minutos.</p>
              <div className="grid gap-1 rounded-md border p-3"><strong className="text-sm">1. Registro TXT (prueba que el dominio es tuyo)</strong>{row("Nombre", d.txtHost)}{row("Valor", d.txtValue)}</div>
              <div className="grid gap-1 rounded-md border p-3"><strong className="text-sm">2. Registro CNAME (apunta el dominio a tu tienda)</strong>{row("Nombre", d.hostname)}{row("Valor", d.cnameTarget)}</div>
              {d.lastError && <Notice tone="warn">{d.lastError}</Notice>}
            </div>
          )}
          {canEdit && (
            <div className="flex flex-wrap gap-2">
              {d.status !== "verified" && <Button variant="outline" onClick={() => verify.mutate(d.id)} disabled={verify.isPending}><RefreshCw aria-hidden="true" /> Verificar</Button>}
              {d.status === "verified" && !d.primary && <Button variant="outline" onClick={() => primary.mutate(d.id)} disabled={primary.isPending}>Hacer principal</Button>}
              <Button variant="ghost" onClick={() => setDel(d)}><Trash2 aria-hidden="true" /> Quitar</Button>
            </div>
          )}
        </Section>
      ))}
      <Confirm open={!!del} title={`¿Quitar ${del?.hostname ?? ""}?`} text="Tu tienda dejará de responder en ese dominio. Seguirá funcionando en la dirección de Apexx." confirmLabel="Quitar" busy={remove.isPending} onConfirm={() => del && remove.mutate(del.id)} onClose={() => setDel(null)} />
    </SubPage>
  );
}
