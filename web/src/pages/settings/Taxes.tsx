import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Percent, Plus, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { formatMoney } from "@/money";
import { can } from "@/perms";
import { ErrorBox, Notice } from "../common";
import { sapi, type TaxRegion, type TaxResult } from "./api";
import { Chips, Confirm, EmptyState, Loading, MoneyInput, ReadOnlyNote, Section, SubPage, SwitchRow } from "./kit";
import { bpsToPercent, formatRanges, parseRanges, percentToBps } from "./logic";
import { ProductClasses } from "./ProductClasses";

const blank: TaxRegion = { id: "", name: "", country: "MX", rateBps: 1600, states: [], postalRanges: [], default: false };

/** Taxes: tax regions by state or postal code, tax class per product and a calculator. */
export function Taxes({ me }: { me: Me }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["settings", "tax"], queryFn: sapi.taxRegions });
  const canEdit = can(me, "settings", "admin");
  const [edit, setEdit] = useState<TaxRegion | null>(null);
  const [del, setDel] = useState<TaxRegion | null>(null);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["settings", "tax"] });
  const defaults = useMutation({
    mutationFn: sapi.taxDefaults,
    onSuccess: (r) => { toast.success(r.added ? `Se agregaron ${r.added} regiones.` : "Ya tenías las regiones de México."); refresh(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const remove = useMutation({
    mutationFn: (id: string) => sapi.deleteTaxRegion(id),
    onSuccess: () => { toast.success("Región eliminada."); setDel(null); refresh(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const items = q.data?.items ?? [];
  const stateName = (c: string) => q.data?.states.find((s) => s.code === c)?.name ?? c;
  return (
    <SubPage
      title="Impuestos"
      sub="Tus precios ya incluyen impuestos. Aquí defines qué tasa lleva cada destino y cada producto; el desglose aparece en el checkout, el pedido y la factura."
      actions={canEdit && (
        <>
          <Button variant="outline" onClick={() => defaults.mutate()} disabled={defaults.isPending}>Cargar tasas de México</Button>
          <Button onClick={() => setEdit({ ...blank, default: items.length === 0 })}><Plus aria-hidden="true" /> Nueva región</Button>
        </>
      )}
    >
      <ReadOnlyNote show={!canEdit} />
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {q.isPending ? <Loading /> : (
        <Section title="Regiones de impuestos" description="Se usa la región más específica: primero código postal, luego estado y al final la región predeterminada.">
          {items.length === 0 ? (
            <EmptyState icon={Percent} title="Sin regiones todavía" text={`Mientras no agregues regiones se usa la tasa del canal de venta${q.data?.legacyBps != null ? ` (${bpsToPercent(q.data.legacyBps)}%)` : ""}. Carga las tasas de México para tener IVA general y de frontera con un clic.`} />
          ) : (
            <div className="overflow-x-auto rounded-md border">
              <Table>
                <TableHeader><TableRow><TableHead>Región</TableHead><TableHead>Tasa</TableHead><TableHead>Aplica en</TableHead><TableHead><span className="sr-only">Acciones</span></TableHead></TableRow></TableHeader>
                <TableBody>
                  {items.map((r) => (
                    <TableRow key={r.id}>
                      <TableCell className="font-medium">{r.name} {r.default && <Badge variant="secondary">Predeterminada</Badge>}</TableCell>
                      <TableCell className="tabular-nums">{bpsToPercent(r.rateBps)}%</TableCell>
                      <TableCell className="max-w-md text-sm text-muted-foreground">
                        {r.default && r.states.length === 0 && r.postalRanges.length === 0 ? "Todos los destinos sin otra región" : [r.states.map(stateName).join(", "), r.postalRanges.length ? `CP ${formatRanges(r.postalRanges).replace(/\n/g, ", ")}` : ""].filter(Boolean).join(" · ")}
                      </TableCell>
                      <TableCell className="text-right">
                        {canEdit && <><Button size="sm" variant="ghost" onClick={() => setEdit(r)}>Editar</Button><Button size="icon" variant="ghost" aria-label={`Eliminar ${r.name}`} onClick={() => setDel(r)}><Trash2 aria-hidden="true" /></Button></>}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </Section>
      )}
      <Calculator />
      <Section title="Impuesto por producto" description="La mayoría de los productos llevan la tasa general. Marca aquí los exentos o de tasa 0% (por ejemplo alimentos o libros).">
        <ProductClasses canEdit={canEdit} focus="tax" />
      </Section>
      {edit && <RegionDialog region={edit} states={q.data?.states ?? []} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); refresh(); }} />}
      <Confirm open={!!del} title={`¿Eliminar ${del?.name ?? ""}?`} text="Los destinos de esta región usarán la región predeterminada. Los pedidos anteriores no cambian." confirmLabel="Eliminar" busy={remove.isPending} onConfirm={() => del && remove.mutate(del.id)} onClose={() => setDel(null)} />
      <TaxService canEdit={can(me, "settings", "admin")} />
    </SubPage>
  );
}

function RegionDialog({ region, states, onClose, onSaved }: { region: TaxRegion; states: { code: string; name: string }[]; onClose: () => void; onSaved: () => void }) {
  const [r, setR] = useState(region);
  const [rate, setRate] = useState(bpsToPercent(region.rateBps));
  const [ranges, setRanges] = useState(formatRanges(region.postalRanges));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: (x: TaxRegion) => sapi.saveTaxRegion(x),
    onSuccess: () => { toast.success("Región guardada. Aplica desde la siguiente compra."); onSaved(); },
    onError: (e) => { setErrors(fieldErrors(e)); toast.error(errorMessage(e)); },
  });
  function submit() {
    const e: Record<string, string> = {};
    const bps = percentToBps(rate);
    const pr = parseRanges(ranges);
    if (!r.name.trim()) e.name = "Ponle un nombre, por ejemplo IVA frontera.";
    if (bps === null) e.rateBps = "Escribe un porcentaje entre 0 y 100.";
    if (pr.error) e.postalRanges = pr.error;
    setErrors(e);
    if (Object.keys(e).length === 0 && bps !== null) save.mutate({ ...r, name: r.name.trim(), rateBps: bps, postalRanges: pr.ranges });
  }
  const free = states.filter((s) => !r.states.includes(s.code));
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader><DialogTitle>{region.id ? "Editar región" : "Nueva región de impuestos"}</DialogTitle><DialogDescription>Define la tasa y a qué destinos aplica.</DialogDescription></DialogHeader>
        <div className="grid gap-4">
          <div className="grid gap-4 sm:grid-cols-[1fr_120px]">
            <FormField id="tx-name" label="Nombre" error={errors.name}><Input value={r.name} onChange={(e) => setR({ ...r, name: e.target.value })} placeholder="IVA general" /></FormField>
            <FormField id="tx-rate" label="Tasa (%)" error={errors.rateBps}><Input inputMode="decimal" value={rate} onChange={(e) => setRate(e.target.value)} /></FormField>
          </div>
          <div className="grid gap-1.5">
            <span className="text-sm font-medium">Estados</span>
            <Chips items={r.states.map((c) => ({ key: c, label: states.find((s) => s.code === c)?.name ?? c }))} onRemove={(k) => setR({ ...r, states: r.states.filter((c) => c !== k) })} />
            <Select value="" onValueChange={(v) => setR({ ...r, states: [...r.states, v] })}>
              <SelectTrigger aria-label="Agregar estado"><SelectValue placeholder="Agregar estado…" /></SelectTrigger>
              <SelectContent>{free.map((s) => <SelectItem key={s.code} value={s.code}>{s.name}</SelectItem>)}</SelectContent>
            </Select>
          </div>
          <FormField id="tx-cp" label="Códigos postales" error={errors.postalRanges} description="Uno por línea. Un rango se escribe 22000-22999.">
            <Textarea rows={3} className="font-mono" value={ranges} onChange={(e) => setRanges(e.target.value)} placeholder={"22000-22999\n32000"} />
          </FormField>
          <SwitchRow title="Región predeterminada" text="Se usa cuando el destino no cae en ninguna otra región." checked={r.default} onChange={(v) => setR({ ...r, default: v })} />
        </div>
        <DialogFooter><Button variant="outline" onClick={onClose}>Cancelar</Button><Button onClick={submit} disabled={save.isPending}>{save.isPending ? "Guardando…" : "Guardar"}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Calculator() {
  const [cp, setCp] = useState("");
  const [amount, setAmount] = useState(100000);
  const [cls, setCls] = useState("general");
  const [res, setRes] = useState<TaxResult | null>(null);
  const run = useMutation({ mutationFn: () => sapi.taxPreview({ postalCode: cp.trim(), amount, class: cls }), onSuccess: setRes, onError: (e) => toast.error(errorMessage(e)) });
  return (
    <Section title="Probar un cálculo" description="Escribe un código postal y un precio para ver cuánto impuesto contiene y por qué.">
      <form className="flex flex-wrap items-end gap-3" onSubmit={(e) => { e.preventDefault(); run.mutate(); }}>
        <FormField id="tc-cp" label="Código postal"><Input className="w-32" inputMode="numeric" maxLength={5} value={cp} onChange={(e) => setCp(e.target.value)} placeholder="06600" /></FormField>
        <div className="grid gap-1.5"><label htmlFor="tc-amount" className="text-sm font-medium">Precio con impuestos</label><MoneyInput id="tc-amount" className="w-36" cents={amount} onChange={(c) => setAmount(c ?? 0)} /></div>
        <div className="grid gap-1.5"><span className="text-sm font-medium">Tipo de producto</span>
          <Select value={cls} onValueChange={setCls}><SelectTrigger className="w-40" aria-label="Tipo de producto"><SelectValue /></SelectTrigger>
            <SelectContent><SelectItem value="general">General</SelectItem><SelectItem value="tasa0">Tasa 0%</SelectItem><SelectItem value="exento">Exento</SelectItem></SelectContent></Select>
        </div>
        <Button type="submit" variant="outline" disabled={run.isPending}>Calcular</Button>
      </form>
      {res && (
        <Notice tone="ok">
          <strong>{res.regionName || "Sin región"}</strong>: {res.reason}.{" "}
          {res.lines.map((l) => `${l.label}: ${formatMoney(l.tax)} de impuesto sobre ${formatMoney(l.net)} sin impuesto`).join(" · ")}
        </Notice>
      )}
    </Section>
  );
}

interface TaxServiceCfg { active: boolean; name: string; endpoint: string; secret?: string; contract?: string }

/** TaxService: an external tax engine computes the tax; if it does not answer in time, these rules do. */
function TaxService({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["settings", "tax-service"], queryFn: () => request<TaxServiceCfg>("GET", "/v1/settings/tax-service") });
  const [c, setC] = useState<TaxServiceCfg | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const v = c ?? q.data;
  const save = useMutation({
    mutationFn: () => request<TaxServiceCfg>("PUT", "/v1/settings/tax-service", { active: v!.active, name: v!.name.trim(), endpoint: v!.endpoint.trim() }),
    onSuccess: () => { setErrors({}); setC(null); void qc.invalidateQueries({ queryKey: ["settings", "tax-service"] }); toast.success("Servicio de impuestos guardado."); },
    onError: (e) => { setErrors(fieldErrors(e)); toast.error(errorMessage(e)); },
  });
  if (!v) return null;
  return (
    <Section title="Servicio de impuestos externo" description="Opcional. Un servicio especializado (por ejemplo, para vender en varios estados de Estados Unidos) calcula el impuesto de cada carrito. Si tarda más de un segundo o falla, se usan tus regiones de arriba y la venta sigue."
      actions={canEdit && <Button onClick={() => save.mutate()} disabled={save.isPending || !c}>{save.isPending ? "Guardando…" : "Guardar"}</Button>}>
      <fieldset className="grid gap-4 sm:grid-cols-2" disabled={!canEdit}>
        <FormField id="ts-name" label="Nombre"><Input value={v.name} placeholder="Mi servicio de impuestos" onChange={(e) => setC({ ...v, name: e.target.value })} /></FormField>
        <FormField id="ts-endpoint" label="Dirección del servicio" error={errors.endpoint}><Input className="font-mono text-xs" value={v.endpoint} placeholder="https://impuestos.ejemplo.com/apexx" onChange={(e) => setC({ ...v, endpoint: e.target.value })} /></FormField>
        <label className="flex items-center gap-2 text-sm sm:col-span-2"><input type="checkbox" checked={v.active} onChange={(e) => setC({ ...v, active: e.target.checked })} /> Usar este servicio para calcular el impuesto</label>
      </fieldset>
      {q.data?.secret && <div className="grid gap-1 text-sm"><span className="font-medium">Secreto de firma para el servicio</span><code className="break-all rounded bg-muted px-2 py-1 text-xs">{q.data.secret}</code><span className="text-muted-foreground">{q.data.contract}</span></div>}
    </Section>
  );
}
