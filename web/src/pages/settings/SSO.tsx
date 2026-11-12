import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Copy } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { Loading, Section, SubPage } from "./kit";

interface Cfg { enabled: boolean; issuer: string; clientId: string; clientSecret?: string; domains: string[]; enforce: boolean; name: string }

/** Single sign-on for the team with the company's identity provider (OpenID Connect). */
export function SSO({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "team", "admin");
  const q = useQuery({ queryKey: ["sso"], queryFn: () => request<{ config: Cfg; hasSecret: boolean; redirectUri: string }>("GET", "/v1/settings/sso") });
  const [v, setV] = useState<Cfg & { domainsText: string }>({ enabled: false, issuer: "", clientId: "", clientSecret: "", domains: [], enforce: false, name: "", domainsText: "" });
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (q.data) setV({ ...q.data.config, clientSecret: "", domainsText: q.data.config.domains.join(", ") }); }, [q.data]);
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  async function save() {
    setBusy(true); setErrs({});
    const { domainsText, ...rest } = v;
    try {
      await request("PUT", "/v1/settings/sso", { ...rest, domains: domainsText.split(/[,\s]+/).filter(Boolean) });
      toast.success(v.enabled ? "Inicio de sesión con tu empresa activo." : "Guardado.");
      void qc.invalidateQueries({ queryKey: ["sso"] });
    } catch (e) {
      setErrs(fieldErrors(e)); toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <SubPage title="Inicio de sesión de la empresa" sub="Tu equipo entra con la cuenta de tu empresa (Google Workspace, Microsoft Entra, Okta o cualquier proveedor OpenID Connect). Solo entran personas invitadas al equipo y activas; quien deja la empresa pierde el acceso al desactivarlo en tu proveedor.">
      <Section title="Proveedor" description="Crea una aplicación web en tu proveedor y usa esta dirección de regreso.">
        <div className="flex flex-wrap items-center gap-2 text-sm"><span>Dirección de regreso:</span><code className="break-all rounded bg-muted px-2 py-1" data-sso-redirect>{q.data.redirectUri}</code><Button size="sm" variant="ghost" aria-label="Copiar dirección de regreso" onClick={() => { void navigator.clipboard?.writeText(q.data.redirectUri); toast.success("Copiada."); }}><Copy className="size-4" /></Button></div>
        <fieldset disabled={!canEdit} className="grid gap-3 sm:grid-cols-2">
          <div className="grid gap-1.5"><Label htmlFor="sso-name">Nombre que verá tu equipo</Label><Input id="sso-name" value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} placeholder="Mi empresa" /></div>
          <div className="grid gap-1.5"><Label htmlFor="sso-issuer">Dirección del proveedor (issuer)</Label><Input id="sso-issuer" className="font-mono text-xs" value={v.issuer} onChange={(e) => setV({ ...v, issuer: e.target.value })} placeholder="https://accounts.google.com" />{errs.issuer && <p className="text-xs text-bad">{errs.issuer}</p>}</div>
          <div className="grid gap-1.5"><Label htmlFor="sso-client">ID de cliente</Label><Input id="sso-client" className="font-mono text-xs" value={v.clientId} onChange={(e) => setV({ ...v, clientId: e.target.value })} />{errs.clientId && <p className="text-xs text-bad">{errs.clientId}</p>}</div>
          <div className="grid gap-1.5"><Label htmlFor="sso-secret">Secreto de cliente</Label><Input id="sso-secret" type="password" autoComplete="off" value={v.clientSecret ?? ""} onChange={(e) => setV({ ...v, clientSecret: e.target.value })} placeholder={q.data.hasSecret ? "Guardado (déjalo vacío para conservarlo)" : ""} /></div>
          <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="sso-domains">Dominios de correo permitidos</Label><Input id="sso-domains" value={v.domainsText} onChange={(e) => setV({ ...v, domainsText: e.target.value })} placeholder="miempresa.com" /></div>
          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={v.enabled} onChange={(e) => setV({ ...v, enabled: e.target.checked })} /> Activo</label>
          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={v.enforce} onChange={(e) => setV({ ...v, enforce: e.target.checked })} /> Obligatorio para el equipo (los dueños conservan su contraseña de respaldo)</label>
        </fieldset>
        {canEdit && <div><Button disabled={busy} onClick={() => void save()}>{busy ? "Comprobando el proveedor…" : "Guardar"}</Button></div>}
      </Section>
    </SubPage>
  );
}
