import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { sapi, type Channel, type TaxRegion } from "./api";
import { Confirm, Loading, ReadOnlyNote, Section, SubPage } from "./kit";
import { bpsToPercent, percentToBps } from "./logic";

const NONE = "__none";
const CURRENCIES = ["MXN", "USD", "EUR", "COP", "ARS", "CLP", "PEN", "BRL"];
const LOCALES = [["es-MX", "Español (México)"], ["es", "Español"], ["en-US", "English (US)"], ["pt-BR", "Português (Brasil)"]];
const COUNTRIES = [["MX", "México"], ["US", "Estados Unidos"], ["CO", "Colombia"], ["AR", "Argentina"], ["CL", "Chile"], ["PE", "Perú"], ["BR", "Brasil"], ["ES", "España"]];

/** Channels: each sales channel has its currency, language, country and tax. */
export function Channels({ me }: { me: Me }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["settings", "channels"], queryFn: sapi.channels });
  const canEdit = can(me, "settings", "admin");
  const [edit, setEdit] = useState<{ c: Channel; isNew: boolean } | null>(null);
  const [del, setDel] = useState<Channel | null>(null);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["settings", "channels"] });
  const remove = useMutation({
    mutationFn: (id: string) => sapi.deleteChannel(id),
    onSuccess: () => { toast.success("Canal eliminado."); setDel(null); refresh(); },
    onError: (e) => { toast.error(errorMessage(e)); setDel(null); },
  });
  const items = q.data?.items ?? [];
  return (
    <SubPage
      title="Canales de venta"
      sub="Un canal es un lugar donde vendes: tu tienda en línea, otro país o una tienda de mayoreo. Cada uno tiene su moneda, idioma e impuestos."
      actions={canEdit && <Button onClick={() => setEdit({ isNew: true, c: { id: "", name: "", currency: "MXN", locale: "es-MX", country: "MX", taxBps: 1600, maxQtyPerItem: 10, shipping: undefined } })}><Plus aria-hidden="true" /> Nuevo canal</Button>}
    >
      <ReadOnlyNote show={!canEdit} />
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {q.isPending ? <Loading /> : (
        <Section title="Tus canales">
          <div className="overflow-x-auto rounded-md border">
            <Table>
              <TableHeader><TableRow><TableHead>Canal</TableHead><TableHead>Moneda</TableHead><TableHead>País e idioma</TableHead><TableHead>Impuesto</TableHead><TableHead>Máx. por producto</TableHead><TableHead><span className="sr-only">Acciones</span></TableHead></TableRow></TableHeader>
              <TableBody>
                {items.map((c) => (
                  <TableRow key={c.id}>
                    <TableCell><span className="font-medium">{c.name || c.id}</span> <span className="font-mono text-xs text-muted-foreground">{c.id}</span> {c.id === q.data?.main && <Badge variant="secondary">Principal</Badge>}</TableCell>
                    <TableCell>{c.currency}</TableCell>
                    <TableCell>{COUNTRIES.find((x) => x[0] === c.country)?.[1] ?? c.country ?? "—"} · {c.locale ?? "—"}</TableCell>
                    <TableCell>{c.taxRegion ? q.data?.taxRegions.find((r) => r.id === c.taxRegion)?.name ?? c.taxRegion : `${bpsToPercent(c.taxBps)}%`}</TableCell>
                    <TableCell className="tabular-nums">{c.maxQtyPerItem}</TableCell>
                    <TableCell className="text-right">{canEdit && <><Button size="sm" variant="ghost" onClick={() => setEdit({ c, isNew: false })}>Editar</Button>{c.id !== q.data?.main && <Button size="icon" variant="ghost" aria-label={`Eliminar ${c.name || c.id}`} onClick={() => setDel(c)}><Trash2 aria-hidden="true" /></Button>}</>}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </Section>
      )}
      {edit && <ChannelDialog channel={edit.c} isNew={edit.isNew} regions={q.data?.taxRegions ?? []} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); refresh(); }} />}
      <Confirm open={!!del} title={`¿Eliminar el canal ${del?.name || del?.id || ""}?`} text="Sus precios dejan de venderse. Los pedidos anteriores se conservan." confirmLabel="Eliminar" busy={remove.isPending} onConfirm={() => del && remove.mutate(del.id)} onClose={() => setDel(null)} />
    </SubPage>
  );
}

function ChannelDialog({ channel, isNew, regions, onClose, onSaved }: { channel: Channel; isNew: boolean; regions: TaxRegion[]; onClose: () => void; onSaved: () => void }) {
  const [c, setC] = useState(channel);
  const [rate, setRate] = useState(bpsToPercent(channel.taxBps));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: (x: Channel) => sapi.saveChannel(x),
    onSuccess: () => { toast.success("Canal guardado."); onSaved(); },
    onError: (e) => { setErrors(fieldErrors(e)); toast.error(errorMessage(e)); },
  });
  function submit() {
    const e: Record<string, string> = {};
    const bps = percentToBps(rate);
    if (!/^[a-z0-9][a-z0-9-]{0,30}$/.test(c.id)) e.id = "Usa minúsculas, números y guiones, por ejemplo mx o mayoreo.";
    if (bps === null) e.taxBps = "Escribe un porcentaje entre 0 y 100.";
    if (!(c.maxQtyPerItem >= 1)) e.maxQtyPerItem = "Debe ser al menos 1.";
    setErrors(e);
    if (Object.keys(e).length === 0 && bps !== null) save.mutate({ ...c, taxBps: bps });
  }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader><DialogTitle>{isNew ? "Nuevo canal" : `Editar ${channel.name || channel.id}`}</DialogTitle><DialogDescription>Los cambios aplican de inmediato en la tienda.</DialogDescription></DialogHeader>
        <div className="grid gap-4 sm:grid-cols-2">
          <FormField id="ch-id" label="Identificador" error={errors.id} description={isNew ? "No se puede cambiar después." : undefined}><Input className="font-mono" value={c.id} disabled={!isNew} onChange={(e) => setC({ ...c, id: e.target.value.toLowerCase() })} placeholder="mayoreo" /></FormField>
          <FormField id="ch-name" label="Nombre" error={errors.name}><Input value={c.name ?? ""} onChange={(e) => setC({ ...c, name: e.target.value })} placeholder="Tienda México" /></FormField>
          <div className="grid gap-1.5"><span className="text-sm font-medium">Moneda</span>
            <Select value={c.currency} onValueChange={(v) => setC({ ...c, currency: v })} disabled={!isNew}><SelectTrigger aria-label="Moneda"><SelectValue /></SelectTrigger><SelectContent>{CURRENCIES.map((x) => <SelectItem key={x} value={x}>{x}</SelectItem>)}</SelectContent></Select>
            {!isNew && <span className="text-xs text-muted-foreground">La moneda no cambia: los precios ya están en ella.</span>}
          </div>
          <div className="grid gap-1.5"><span className="text-sm font-medium">País</span>
            <Select value={c.country || "MX"} onValueChange={(v) => setC({ ...c, country: v })}><SelectTrigger aria-label="País"><SelectValue /></SelectTrigger><SelectContent>{COUNTRIES.map((x) => <SelectItem key={x[0]} value={x[0] as string}>{x[1]}</SelectItem>)}</SelectContent></Select>
          </div>
          <div className="grid gap-1.5"><span className="text-sm font-medium">Idioma</span>
            <Select value={c.locale || "es-MX"} onValueChange={(v) => setC({ ...c, locale: v })}><SelectTrigger aria-label="Idioma"><SelectValue /></SelectTrigger><SelectContent>{LOCALES.map((x) => <SelectItem key={x[0]} value={x[0] as string}>{x[1]}</SelectItem>)}</SelectContent></Select>
          </div>
          <FormField id="ch-max" label="Máximo por producto" error={errors.maxQtyPerItem} description="Unidades de un mismo artículo por pedido."><Input inputMode="numeric" value={String(c.maxQtyPerItem)} onChange={(e) => setC({ ...c, maxQtyPerItem: Number(e.target.value.replace(/\D/g, "")) || 0 })} /></FormField>
          <div className="grid gap-1.5"><span className="text-sm font-medium">Región de impuestos</span>
            <Select value={c.taxRegion || NONE} onValueChange={(v) => setC({ ...c, taxRegion: v === NONE ? "" : v })}><SelectTrigger aria-label="Región de impuestos"><SelectValue /></SelectTrigger>
              <SelectContent><SelectItem value={NONE}>Según el destino</SelectItem>{regions.map((r) => <SelectItem key={r.id} value={r.id}>{r.name}</SelectItem>)}</SelectContent></Select>
          </div>
          <FormField id="ch-tax" label="Tasa de respaldo (%)" error={errors.taxBps} description="Se usa si no hay regiones de impuestos."><Input inputMode="decimal" value={rate} onChange={(e) => setRate(e.target.value)} /></FormField>
        </div>
        <DialogFooter><Button variant="outline" onClick={onClose}>Cancelar</Button><Button onClick={submit} disabled={save.isPending}>{save.isPending ? "Guardando…" : "Guardar"}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
