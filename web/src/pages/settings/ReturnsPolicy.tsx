import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { readNumber } from "@/money";
import { Button } from "@/components/ui/button";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { sapi } from "./api";
import { Loading, ReadOnlyNote, Section, SubPage } from "./kit";

interface Policy { selfService: boolean; windowDays: number; carrierId: string }
const api = {
  get: () => request<Policy>("GET", "/v1/settings/returns"),
  save: (p: Policy) => request<Policy>("PUT", "/v1/settings/returns", p),
};

/** Returns: customers ask from their account, within a window, with a prepaid label. */
export function ReturnsPolicy({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "settings", "admin");
  const q = useQuery({ queryKey: ["settings", "returns"], queryFn: api.get });
  const ship = useQuery({ queryKey: ["settings", "shipping"], queryFn: sapi.shipping });
  const [p, setP] = useState<Policy | null>(null);
  const [days, setDays] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  useEffect(() => { if (q.data) { setP(q.data); setDays(String(q.data.windowDays)); } }, [q.data]);
  const save = useMutation({
    mutationFn: () => api.save({ ...p!, windowDays: readNumber(days, "Plazo desde el envío (días)") }),
    onSuccess: (x) => { setErrors({}); qc.setQueryData(["settings", "returns"], x); toast.success("Política de devoluciones guardada."); },
    onError: (e) => { setErrors(fieldErrors(e)); toast.error(errorMessage(e)); },
  });
  if (q.error) return <ErrorBox error={q.error} onRetry={() => void q.refetch()} />;
  if (!p) return <Loading />;
  const connected = (ship.data?.carriers ?? []).filter((c) => c.active && c.provider);
  return (
    <SubPage title="Devoluciones" sub="Deja que tus clientes pidan la devolución desde su cuenta, con su guía lista para imprimir. Tú decides el plazo; el reembolso o el cambio se hacen cuando recibes el paquete.">
      <ReadOnlyNote show={!canEdit} need="Administrador" />
      <form onSubmit={(e: FormEvent) => { e.preventDefault(); save.mutate(); }}>
        <Section title="Autoservicio" actions={canEdit && <Button type="submit" disabled={save.isPending}>{save.isPending ? "Guardando…" : "Guardar"}</Button>}>
          <fieldset className="grid gap-4 sm:grid-cols-2" disabled={!canEdit}>
            <label className="flex items-start gap-2 text-sm sm:col-span-2"><input type="checkbox" className="mt-1" checked={p.selfService} onChange={(e) => setP({ ...p, selfService: e.target.checked })} /><span>Los clientes piden devoluciones desde «Mi cuenta»<span className="block text-muted-foreground">Sin esto, solo tu equipo las registra desde el pedido.</span></span></label>
            <FormField id="rp-days" label="Plazo desde el envío (días)" error={errors.windowDays}><Input inputMode="numeric" value={days} onChange={(e) => setDays(e.target.value.replace(/\D/g, ""))} /></FormField>
            <FormField id="rp-carrier" label="Guía prepagada" error={errors.carrierId} description={connected.length ? "La guía se compra al pedir la devolución y el cliente la imprime." : "Conecta una paquetería en Envíos › Paqueterías para dar guías prepagadas."}>
              <select id="rp-carrier" className="h-9 rounded-md border bg-background px-2 text-sm" value={p.carrierId} onChange={(e) => setP({ ...p, carrierId: e.target.value })}>
                <option value="">Sin guía: el cliente envía por su cuenta</option>
                {connected.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
              </select>
            </FormField>
          </fieldset>
        </Section>
      </form>
    </SubPage>
  );
}
