import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { Loading, Section, SubPage } from "./kit";

interface P { active: boolean; id: string; hasSecret: boolean; testCode?: string }
interface Cfg { meta: P; ga4: P; last30Days: { Sent: number; Pending: number; Failed: number } }

/** Server-side conversions for ad platforms, sent only with the shopper's consent. */
export function AdConversions({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "settings", "admin");
  const q = useQuery({ queryKey: ["ad-conversions"], queryFn: () => request<Cfg>("GET", "/v1/settings/ad-conversions") });
  const [v, setV] = useState({ meta: { active: false, id: "", secret: "", testCode: "" }, ga4: { active: false, id: "", secret: "" } });
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (q.data) setV({ meta: { active: q.data.meta.active, id: q.data.meta.id, secret: "", testCode: q.data.meta.testCode ?? "" }, ga4: { active: q.data.ga4.active, id: q.data.ga4.id, secret: "" } }); }, [q.data]);
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  async function save() {
    setBusy(true); setErrs({});
    try { await request("PUT", "/v1/settings/ad-conversions", v); toast.success("Conversiones guardadas."); void qc.invalidateQueries({ queryKey: ["ad-conversions"] }); }
    catch (e) { setErrs(fieldErrors(e)); toast.error(errorMessage(e)); } finally { setBusy(false); }
  }
  const s = q.data.last30Days;
  return (
    <SubPage title="Conversiones para anuncios" sub="Avisamos de cada compra a tus plataformas de anuncios desde el servidor: más precisas que un píxel y sin hacer lenta la tienda. Solo se envían compras de quien aceptó publicidad en el aviso de cookies, con su correo y teléfono cifrados (SHA-256).">
      <p className="text-sm text-muted-foreground">Últimos 30 días: {s.Sent} enviadas · {s.Pending} en espera · {s.Failed} con error.</p>
      <Section title="Meta (Facebook e Instagram)">
        <fieldset disabled={!canEdit} className="grid gap-3 sm:grid-cols-3">
          <div className="grid gap-1.5"><Label htmlFor="ad-meta-id">ID del píxel</Label><Input id="ad-meta-id" className="font-mono" value={v.meta.id} onChange={(e) => setV({ ...v, meta: { ...v.meta, id: e.target.value } })} />{errs["meta.id"] && <p className="text-xs text-bad">{errs["meta.id"]}</p>}</div>
          <div className="grid gap-1.5"><Label htmlFor="ad-meta-token">Token de acceso</Label><Input id="ad-meta-token" type="password" autoComplete="off" value={v.meta.secret} onChange={(e) => setV({ ...v, meta: { ...v.meta, secret: e.target.value } })} placeholder={q.data.meta.hasSecret ? "Guardado" : ""} /></div>
          <div className="grid gap-1.5"><Label htmlFor="ad-meta-test">Código de prueba (opcional)</Label><Input id="ad-meta-test" value={v.meta.testCode} onChange={(e) => setV({ ...v, meta: { ...v.meta, testCode: e.target.value } })} /></div>
          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={v.meta.active} onChange={(e) => setV({ ...v, meta: { ...v.meta, active: e.target.checked } })} /> Activo</label>
        </fieldset>
      </Section>
      <Section title="Google Analytics 4">
        <fieldset disabled={!canEdit} className="grid gap-3 sm:grid-cols-3">
          <div className="grid gap-1.5"><Label htmlFor="ad-ga4-id">ID de medición</Label><Input id="ad-ga4-id" className="font-mono" value={v.ga4.id} onChange={(e) => setV({ ...v, ga4: { ...v.ga4, id: e.target.value } })} placeholder="G-XXXXXXX" />{errs["ga4.id"] && <p className="text-xs text-bad">{errs["ga4.id"]}</p>}</div>
          <div className="grid gap-1.5"><Label htmlFor="ad-ga4-secret">Secreto de la API</Label><Input id="ad-ga4-secret" type="password" autoComplete="off" value={v.ga4.secret} onChange={(e) => setV({ ...v, ga4: { ...v.ga4, secret: e.target.value } })} placeholder={q.data.ga4.hasSecret ? "Guardado" : ""} /></div>
          <label className="flex items-center gap-2 self-end text-sm"><input type="checkbox" checked={v.ga4.active} onChange={(e) => setV({ ...v, ga4: { ...v.ga4, active: e.target.checked } })} /> Activo</label>
        </fieldset>
      </Section>
      {canEdit && <div><Button disabled={busy} onClick={() => void save()}>{busy ? "Guardando…" : "Guardar"}</Button></div>}
    </SubPage>
  );
}
