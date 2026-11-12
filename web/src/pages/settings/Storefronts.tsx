import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Archive, ExternalLink, Plus } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { href } from "@/router";
import { ErrorBox } from "../common";
import { sapi } from "./api";
import { Loading, Section, SubPage } from "./kit";

interface Host { hostname: string; status: string; market: string }
interface Front { id: string; name: string; market: string; live: boolean; createdAt: string; domains: Host[]; url: string }

/** Storefronts: several sites (brands, outlet, wholesale, a country) over one catalog, stock and order list. */
export function Storefronts({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "builder", "admin");
  const q = useQuery({ queryKey: ["storefronts"], queryFn: () => request<{ items: Front[]; max: number }>("GET", "/v1/storefronts") });
  const ch = useQuery({ queryKey: ["settings", "channels"], queryFn: sapi.channels });
  const [n, setN] = useState({ id: "", name: "", market: "", copyFrom: "main" });
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["storefronts"] });
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const markets = (ch.data?.items ?? []).filter((c) => c.market || c.id === ch.data?.main);
  const mkName = (id: string) => (id ? (markets.find((m) => m.id === id)?.name ?? id) : "Principal");
  const marketSelect = (id: string, value: string, onChange: (v: string) => void, disabled?: boolean) => (
    <select id={id} disabled={disabled} className="h-9 rounded-md border bg-background px-2 text-sm" value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">{`Mercado principal${ch.data ? ` (${markets.find((m) => m.id === ch.data.main)?.currency ?? ""})` : ""}`}</option>
      {markets.filter((m) => m.id !== ch.data?.main).map((m) => <option key={m.id} value={m.id}>{m.name || m.id} ({m.currency}{m.language ? `, ${m.language}` : ""})</option>)}
    </select>
  );
  async function create() {
    setBusy(true); setErrs({});
    try {
      await request("POST", "/v1/storefronts", n);
      toast.success("Tienda creada. Edita su diseño y publícala cuando esté lista."); setN({ id: "", name: "", market: "", copyFrom: "main" }); refresh();
    } catch (e) { setErrs(fieldErrors(e)); toast.error(errorMessage(e)); } finally { setBusy(false); }
  }
  async function update(f: Front, p: Partial<Front>) {
    try { await request("PUT", `/v1/storefronts/${f.id}`, { name: p.name ?? f.name, market: p.market ?? f.market }); toast.success("Tienda actualizada."); refresh(); } catch (e) { toast.error(errorMessage(e)); }
  }
  async function archive(f: Front) {
    if (!window.confirm(`¿Archivar «${f.name}»? Deja de responder; su historial de publicaciones se conserva.`)) return;
    try { await request("DELETE", `/v1/storefronts/${f.id}`, undefined, { as: "none" }); toast.success("Tienda archivada."); refresh(); } catch (e) { toast.error(errorMessage(e)); }
  }
  async function domainMarket(host: string, market: string) {
    const d = (await sapi.domains()).items.find((x) => x.hostname === host);
    if (!d) return;
    try { await request("PUT", `/v1/domains/${d.id}/market`, { market }, { as: "none" }); toast.success(`${host} abre ahora en ${mkName(market)}.`); refresh(); } catch (e) { toast.error(errorMessage(e)); }
  }
  return (
    <SubPage title="Tiendas y mercados" sub="Ten varias tiendas con su propio diseño (una marca, un outlet, mayoreo o un país) sobre el mismo catálogo, inventario, clientes y pedidos. Cada tienda o dominio puede abrir en un mercado: su moneda, precios e idioma.">
      {q.data.items.map((f) => (
        <Section key={f.id} title={f.name} description={`${f.id} · ${f.live ? "publicada" : "sin publicar"} · abre en ${mkName(f.market)}`}
          actions={<div className="flex gap-2">
            <Button asChild variant="outline" size="sm"><a href={f.url} target="_blank" rel="noopener"><ExternalLink className="size-4" /> Ver</a></Button>
            <Button asChild variant="outline" size="sm"><a href={href("builder", f.id)}>Editar diseño</a></Button>
            {canEdit && f.id !== "main" && <Button variant="ghost" size="sm" onClick={() => void archive(f)}><Archive className="size-4" /> Archivar</Button>}
          </div>}>
          <div className="grid gap-3 sm:grid-cols-[1fr_auto] sm:items-end">
            <div className="grid gap-1.5"><Label htmlFor={`sf-name-${f.id}`}>Nombre</Label><Input id={`sf-name-${f.id}`} disabled={!canEdit} defaultValue={f.name} onBlur={(e) => e.target.value.trim() && e.target.value !== f.name && void update(f, { name: e.target.value.trim() })} /></div>
            <div className="grid gap-1.5"><Label htmlFor={`sf-mk-${f.id}`}>Abre en</Label>{marketSelect(`sf-mk-${f.id}`, f.market, (v) => void update(f, { market: v }), !canEdit)}</div>
          </div>
          {f.domains.length > 0 && (
            <table className="w-full text-sm">
              <thead className="text-left text-muted-foreground"><tr><th className="py-1">Dominio</th><th>Estado</th><th>Abre en</th></tr></thead>
              <tbody>{f.domains.map((d) => (
                <tr key={d.hostname} className="border-t"><td className="py-2">{d.hostname}</td><td>{d.status === "verified" ? "Conectado" : "Pendiente"}</td>
                  <td>{marketSelect(`dm-mk-${d.hostname}`, d.market, (v) => void domainMarket(d.hostname, v), !canEdit)}</td></tr>
              ))}</tbody>
            </table>
          )}
        </Section>
      ))}
      {canEdit && q.data.items.length < q.data.max && (
        <Section title="Nueva tienda">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="grid gap-1.5"><Label htmlFor="sf-new-name">Nombre</Label><Input id="sf-new-name" value={n.name} onChange={(e) => setN({ ...n, name: e.target.value, id: n.id || "" })} placeholder="Outlet" />{errs.name && <p className="text-xs text-bad">{errs.name}</p>}</div>
            <div className="grid gap-1.5"><Label htmlFor="sf-new-id">Identificador en la dirección</Label><Input id="sf-new-id" value={n.id} onChange={(e) => setN({ ...n, id: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "-") })} placeholder="outlet" />{errs.id && <p className="text-xs text-bad">{errs.id}</p>}</div>
            <div className="grid gap-1.5"><Label htmlFor="sf-new-mk">Abre en</Label>{marketSelect("sf-new-mk", n.market, (v) => setN({ ...n, market: v }))}</div>
            <div className="grid gap-1.5"><Label htmlFor="sf-new-copy">Diseño inicial</Label>
              <select id="sf-new-copy" className="h-9 rounded-md border bg-background px-2 text-sm" value={n.copyFrom} onChange={(e) => setN({ ...n, copyFrom: e.target.value })}>
                <option value="">En blanco</option>{q.data.items.map((f) => <option key={f.id} value={f.id}>Copia de {f.name}</option>)}
              </select></div>
          </div>
          <div><Button disabled={busy || !n.name || !n.id} onClick={() => void create()}><Plus className="size-4" /> Crear tienda</Button></div>
        </Section>
      )}
    </SubPage>
  );
}
