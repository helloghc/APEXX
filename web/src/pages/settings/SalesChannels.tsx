import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ExternalLink, Plus, RefreshCw, Trash2, Unplug } from "lucide-react";
import { errorMessage, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { formatMoney } from "@/money";
import { can } from "@/perms";
import { ErrorBox, Notice } from "../common";
import { ProductPicker } from "../promotions/shared";
import { Loading, Section, SubPage } from "./kit";

interface Settings { markupBps: number; stockBuffer: number; importOrders: boolean }
interface Account { provider: "mercadolibre" | "amazon"; name: string; connected: boolean; sellerId?: string; status?: string; lastError?: string; settings: Settings; counts: Record<string, number>; orders: number }
interface Listing { sku: string; title: string; externalId: string; status: string; pushedPrice: number | null; pushedQty: number | null; price: number; qty: number; error?: string; syncedAt: string | null; url?: string }

const STATUS: Record<string, string> = { pending: "Por publicar", active: "Publicado", error: "Con error", closing: "Cerrando", paused: "Pausado" };
const HELP: Record<string, string> = {
  mercadolibre: "Crea una app en el portal de desarrolladores de Mercado Libre, autorízala con tu cuenta de vendedor y copia aquí su ID, su clave secreta y el token de actualización (refresh token).",
  amazon: "Registra una app en Seller Central (Selling Partner API), autorízala y copia el ID de cliente LWA, su secreto, el token de actualización y tu identificador de vendedor (Merchant Token).",
};

/** Sales channels: list products on Mercado Libre and Amazon, with price and stock kept in step and orders imported. */
export function SalesChannels({ me }: { me: Me }) {
  const q = useQuery({ queryKey: ["sales-channels"], queryFn: () => request<{ items: Account[] }>("GET", "/v1/sales-channels").then((r) => r.items) });
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  return (
    <SubPage title="Mercado Libre y Amazon" sub="Publica tus productos en otros canales sin capturarlos dos veces. Apexx mantiene su precio y existencias al día (comparten inventario con tu tienda) y trae sus pedidos a tu lista de pedidos.">
      {q.data.map((a) => <ChannelCard key={a.provider} a={a} me={me} />)}
    </SubPage>
  );
}

function ChannelCard({ a, me }: { a: Account; me: Me }) {
  const qc = useQueryClient();
  const isAdmin = can(me, "settings", "admin");
  const canList = can(me, "catalog", "edit");
  const [f, setF] = useState({ clientId: "", clientSecret: "", refreshToken: "", sellerId: a.sellerId ?? "" });
  const [st, setSt] = useState({ markup: String(a.settings.markupBps / 100), buffer: String(a.settings.stockBuffer), importOrders: a.settings.importOrders });
  const [busy, setBusy] = useState(false);
  const [pick, setPick] = useState(false);
  const refresh = () => { void qc.invalidateQueries({ queryKey: ["sales-channels"] }); void qc.invalidateQueries({ queryKey: ["channel-listings", a.provider] }); };
  const listings = useQuery({ queryKey: ["channel-listings", a.provider], enabled: a.connected, refetchInterval: 15_000,
    queryFn: () => request<{ items: Listing[] }>("GET", `/v1/sales-channels/${a.provider}/listings`).then((r) => r.items) });
  const settings = () => ({ markupBps: Math.round((Number(st.markup) || 0) * 100), stockBuffer: Number(st.buffer) || 0, importOrders: st.importOrders });
  async function connect() {
    setBusy(true);
    try {
      await request("PUT", `/v1/sales-channels/${a.provider}`, { ...f, settings: settings() });
      toast.success(`${a.name} conectado.`); setF({ ...f, clientSecret: "", refreshToken: "" }); refresh();
    } catch (e) { toast.error(errorMessage(e)); } finally { setBusy(false); }
  }
  async function saveSettings() {
    try { await request("PUT", `/v1/sales-channels/${a.provider}`, { settings: settings() }); toast.success("Reglas guardadas; los precios se actualizan en segundos."); refresh(); } catch (e) { toast.error(errorMessage(e)); }
  }
  async function disconnect() {
    if (!window.confirm(`¿Desconectar ${a.name}? Tus publicaciones allá no se cierran; Apexx deja de actualizarlas.`)) return;
    try { await request("DELETE", `/v1/sales-channels/${a.provider}`, undefined, { as: "none" }); toast.success(`${a.name} desconectado.`); refresh(); } catch (e) { toast.error(errorMessage(e)); }
  }
  async function sync() {
    setBusy(true);
    try {
      const r = await request<{ listings: number; orders: number; error: string }>("POST", `/v1/sales-channels/${a.provider}/sync`);
      if (r.error) toast.error(r.error); else toast.success(`Listo: ${r.listings} publicaciones actualizadas, ${r.orders} pedidos nuevos.`);
      refresh();
    } catch (e) { toast.error(errorMessage(e)); } finally { setBusy(false); }
  }
  async function add(skus: string[]) {
    try { await request("POST", `/v1/sales-channels/${a.provider}/listings`, { skus }); toast.success(`${skus.length} productos en cola para publicar.`); refresh(); } catch (e) { toast.error(errorMessage(e)); }
  }
  async function close(sku: string) {
    try { await request("DELETE", `/v1/sales-channels/${a.provider}/listings/${encodeURIComponent(sku)}`, undefined, { as: "none" }); toast.success("La publicación se cerrará en segundos."); refresh(); } catch (e) { toast.error(errorMessage(e)); }
  }
  const p = a.provider;
  return (
    <Section title={a.name} description={a.connected ? `Conectado · vendedor ${a.sellerId} · ${a.orders} pedidos importados` : "Sin conectar"}
      actions={a.connected && <div className="flex gap-2">
        {canList && <Button size="sm" variant="outline" disabled={busy} onClick={() => void sync()}><RefreshCw className="size-4" /> Sincronizar ahora</Button>}
        {isAdmin && <Button size="sm" variant="ghost" onClick={() => void disconnect()}><Unplug className="size-4" /> Desconectar</Button>}
      </div>}>
      {a.lastError && <Notice tone="bad">{a.lastError}</Notice>}
      {isAdmin && (
        <details open={!a.connected} className="rounded-md border p-3">
          <summary className="cursor-pointer text-sm font-medium">{a.connected ? "Cambiar credenciales" : "Conectar mi cuenta"}</summary>
          <p className="mt-2 text-sm text-muted-foreground">{HELP[p]}</p>
          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            <div className="grid gap-1.5"><Label htmlFor={`${p}-cid`}>ID de la app</Label><Input id={`${p}-cid`} autoComplete="off" value={f.clientId} onChange={(e) => setF({ ...f, clientId: e.target.value })} /></div>
            <div className="grid gap-1.5"><Label htmlFor={`${p}-sec`}>Clave secreta</Label><Input id={`${p}-sec`} type="password" autoComplete="new-password" value={f.clientSecret} onChange={(e) => setF({ ...f, clientSecret: e.target.value })} /></div>
            <div className="grid gap-1.5"><Label htmlFor={`${p}-tok`}>Token de actualización</Label><Input id={`${p}-tok`} type="password" autoComplete="new-password" value={f.refreshToken} onChange={(e) => setF({ ...f, refreshToken: e.target.value })} /></div>
            <div className="grid gap-1.5"><Label htmlFor={`${p}-sel`}>Identificador de vendedor{p === "mercadolibre" ? " (opcional)" : ""}</Label><Input id={`${p}-sel`} autoComplete="off" value={f.sellerId} onChange={(e) => setF({ ...f, sellerId: e.target.value })} /></div>
          </div>
          <p className="mt-2 text-xs text-muted-foreground">Las credenciales se guardan cifradas y nunca se vuelven a mostrar.</p>
          <Button className="mt-3" disabled={busy || !f.clientId || !f.clientSecret || !f.refreshToken} onClick={() => void connect()}>{busy ? "Comprobando…" : a.connected ? "Guardar credenciales" : `Conectar ${a.name}`}</Button>
        </details>
      )}
      {a.connected && (
        <>
          <fieldset disabled={!isAdmin} className="grid gap-3 sm:grid-cols-[10rem_12rem_1fr_auto] sm:items-end">
            <div className="grid gap-1.5"><Label htmlFor={`${p}-mk`}>Aumento al precio (%)</Label><Input id={`${p}-mk`} inputMode="decimal" value={st.markup} onChange={(e) => setSt({ ...st, markup: e.target.value })} /></div>
            <div className="grid gap-1.5"><Label htmlFor={`${p}-bf`}>Piezas que no se ofrecen</Label><Input id={`${p}-bf`} inputMode="numeric" value={st.buffer} onChange={(e) => setSt({ ...st, buffer: e.target.value.replace(/\D/g, "") })} /></div>
            <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={st.importOrders} onChange={(e) => setSt({ ...st, importOrders: e.target.checked })} /> Traer los pedidos pagados a Apexx</label>
            {isAdmin && <Button variant="outline" onClick={() => void saveSettings()}>Guardar reglas</Button>}
          </fieldset>
          <div className="flex items-center justify-between gap-2">
            <h3 className="text-sm font-medium">Publicaciones</h3>
            {canList && <Button size="sm" onClick={() => setPick(true)}><Plus className="size-4" /> Publicar productos</Button>}
          </div>
          {listings.isPending ? <Loading /> : listings.error ? <ErrorBox error={listings.error} /> : listings.data.length === 0 ? (
            <p className="text-sm text-muted-foreground">Todavía no publicas productos en {a.name}.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm" data-listings={p}>
                <thead className="text-left text-muted-foreground"><tr><th className="py-1">Producto</th><th>Estado</th><th className="text-right">Precio</th><th className="text-right">Existencias</th><th /></tr></thead>
                <tbody>
                  {listings.data.map((l) => (
                    <tr key={l.sku} className="border-t align-top">
                      <td className="py-2"><div className="font-medium">{l.title}</div><div className="text-xs text-muted-foreground">{l.sku}{l.externalId && ` · ${l.externalId}`}</div>{l.error && <div className="text-xs text-bad">{l.error}</div>}</td>
                      <td>{STATUS[l.status] ?? l.status}</td>
                      <td className="text-right tabular-nums">{formatMoney(l.price)}{l.pushedPrice !== null && l.pushedPrice !== l.price && <div className="text-xs text-muted-foreground">allá {formatMoney(l.pushedPrice)}</div>}</td>
                      <td className="text-right tabular-nums">{l.qty}{l.pushedQty !== null && l.pushedQty !== l.qty && <div className="text-xs text-muted-foreground">allá {l.pushedQty}</div>}</td>
                      <td className="whitespace-nowrap text-right">
                        {l.url && <Button asChild size="icon" variant="ghost" aria-label={`Ver ${l.title} en ${a.name}`}><a href={l.url} target="_blank" rel="noopener"><ExternalLink className="size-4" /></a></Button>}
                        {canList && l.status !== "closing" && <Button size="icon" variant="ghost" aria-label={`Cerrar publicación de ${l.title}`} onClick={() => void close(l.sku)}><Trash2 className="size-4" /></Button>}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <ProductPicker open={pick} onOpenChange={setPick} mode="sku" title={`Publicar en ${a.name}`} description="Elige las variantes a publicar. Se publican con el precio y las existencias de tu tienda."
            exclude={listings.data?.map((l) => l.sku)} onPick={(items) => { setPick(false); void add(items.map((i) => i.id)); }} />
        </>
      )}
    </Section>
  );
}
