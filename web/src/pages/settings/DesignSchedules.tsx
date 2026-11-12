import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { CalendarClock, ExternalLink, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { Loading, Section, SubPage } from "./kit";

interface Sched { id: string; name: string; startsAt: string; endsAt: string; state: string }
const fmt = (d: string) => new Date(d).toLocaleString("es-MX", { dateStyle: "medium", timeStyle: "short" });

/** Scheduled design: the current draft goes live by itself between two dates. */
export function DesignSchedules({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "builder", "admin");
  const q = useQuery({ queryKey: ["design-schedules"], queryFn: () => request<{ items: Sched[] }>("GET", "/v1/sites/main/schedules").then((r) => r.items) });
  const [v, setV] = useState({ name: "", start: "", end: "" });
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  async function create() {
    setBusy(true); setErrs({});
    try {
      await request("POST", "/v1/sites/main/schedules", { name: v.name, startsAt: new Date(v.start).toISOString(), endsAt: new Date(v.end).toISOString() });
      toast.success("Versión programada: entra y sale sola en sus fechas."); setV({ name: "", start: "", end: "" });
      void qc.invalidateQueries({ queryKey: ["design-schedules"] });
    } catch (e) { setErrs(fieldErrors(e)); toast.error(errorMessage(e)); } finally { setBusy(false); }
  }
  async function preview(s: Sched) {
    try {
      const at = new Date(Math.max(new Date(s.startsAt).getTime(), Date.now()) + 60_000).toISOString();
      const r = await request<{ url: string }>("GET", `/v1/sites/main/schedules/preview?at=${encodeURIComponent(at)}`);
      window.open(r.url, "_blank", "noopener");
    } catch (e) { toast.error(errorMessage(e)); }
  }
  async function remove(s: Sched) {
    if (!window.confirm(`¿Quitar «${s.name}»?`)) return;
    try { await request("DELETE", `/v1/sites/main/schedules/${s.id}`, undefined, { as: "none" }); toast.success("Programación quitada."); void qc.invalidateQueries({ queryKey: ["design-schedules"] }); } catch (e) { toast.error(errorMessage(e)); }
  }
  return (
    <SubPage title="Diseño programado" sub="Prepara en el diseñador cómo se verá tu tienda en una temporada (Buen Fin, Navidad) y prográmala: entra sola en su fecha y al terminar vuelve tu diseño publicado. Antes puedes verla como se verá ese día.">
      {canEdit && (
        <Section title="Programar el borrador actual" description="Se guarda el borrador tal como está ahora; puedes seguir editando sin cambiar la versión programada.">
          <div className="grid gap-3 sm:grid-cols-3">
            <div className="grid gap-1.5"><Label htmlFor="ds-name">Nombre</Label><Input id="ds-name" value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} placeholder="Buen Fin" />{errs.name && <p className="text-xs text-bad">{errs.name}</p>}</div>
            <div className="grid gap-1.5"><Label htmlFor="ds-start">Entra el</Label><Input id="ds-start" type="datetime-local" value={v.start} onChange={(e) => setV({ ...v, start: e.target.value })} /></div>
            <div className="grid gap-1.5"><Label htmlFor="ds-end">Sale el</Label><Input id="ds-end" type="datetime-local" value={v.end} onChange={(e) => setV({ ...v, end: e.target.value })} />{errs.endsAt && <p className="text-xs text-bad">{errs.endsAt}</p>}</div>
          </div>
          <div><Button disabled={busy || !v.name || !v.start || !v.end} onClick={() => void create()}><CalendarClock className="size-4" /> {busy ? "Programando…" : "Programar"}</Button></div>
        </Section>
      )}
      {q.data.length === 0 ? <p className="text-sm text-muted-foreground">No hay versiones programadas.</p> : (
        <ul className="divide-y rounded-md border" data-schedules>{q.data.map((s) => (
          <li key={s.id} className="flex flex-wrap items-center justify-between gap-2 p-3 text-sm">
            <span><strong>{s.name}</strong> · {fmt(s.startsAt)} → {fmt(s.endsAt)} · <span className={s.state === "en vivo" ? "font-semibold text-ok" : "text-muted-foreground"}>{s.state}</span></span>
            <span className="flex gap-1">{s.state !== "terminada" && <Button size="sm" variant="outline" onClick={() => void preview(s)}><ExternalLink className="size-4" /> Ver como se verá</Button>}{canEdit && <Button size="icon" variant="ghost" aria-label={`Quitar ${s.name}`} onClick={() => void remove(s)}><Trash2 className="size-4" /></Button>}</span>
          </li>))}</ul>
      )}
    </SubPage>
  );
}
