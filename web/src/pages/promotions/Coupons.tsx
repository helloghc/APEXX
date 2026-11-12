import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Download, Plus, Search, Ticket, Wand2 } from "lucide-react";
import { errorMessage, fieldErrors, type Me } from "@/api";
import { readNumber } from "@/money";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { href } from "@/router";
import { promoApi, type CouponSpec } from "@/promoApi";
import { ErrorBox } from "../common";
import { storeDate, windowText } from "./lib";
import { DateTimeField, EmptyState, Field, SkeletonRows, useDebounced } from "./shared";

const ALL = "__all";
const STATE_TONE: Record<string, "ok" | "warn" | "bad" | "secondary" | "outline"> = { activo: "ok", programado: "secondary", vencido: "outline", agotado: "warn", desactivado: "outline" };

export function Coupons({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "promotions", "edit");
  const [q, setQ] = useState("");
  const dq = useDebounced(q.trim());
  const [state, setState] = useState("");
  const [promotion, setPromotion] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [dialog, setDialog] = useState<null | "one" | "many">(null);
  const promos = useQuery({ queryKey: ["promotions", "coupon-only"], queryFn: () => promoApi.list({ limit: 100 }) });
  const couponPromos = (promos.data?.items ?? []).filter((p) => p.conditions?.requiresCoupon);
  const batches = useQuery({ queryKey: ["coupon-batches"], queryFn: promoApi.batches });
  const list = useInfiniteQuery({
    queryKey: ["coupons", dq, state, promotion],
    queryFn: ({ pageParam }) => promoApi.coupons({ q: dq, state, promotion, cursor: pageParam, limit: 50 }),
    initialPageParam: "",
    getNextPageParam: (l) => l.nextCursor || undefined,
  });
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const total = list.data?.pages[0]?.total ?? 0;
  const refresh = () => {
    setSelected(new Set());
    void qc.invalidateQueries({ queryKey: ["coupons"] });
    void qc.invalidateQueries({ queryKey: ["coupon-batches"] });
  };
  const bulk = (action: "enable" | "disable" | "delete") =>
    promoApi.couponsBulk([...selected], action).then((r) => { toast.success(`${r.changed} cupones actualizados.`); refresh(); }).catch((e) => toast.error(errorMessage(e)));
  const toggle = (code: string) => setSelected((s) => { const n = new Set(s); if (n.has(code)) n.delete(code); else n.add(code); return n; });
  const noPromos = !promos.isLoading && couponPromos.length === 0;

  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-52 flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden="true" />
          <Input aria-label="Buscar cupón" placeholder="Buscar por código" className="pl-8" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <Select value={state || ALL} onValueChange={(v) => setState(v === ALL ? "" : v)}>
          <SelectTrigger className="w-40" aria-label="Estado"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>Todos los estados</SelectItem>
            {["activo", "programado", "agotado", "vencido", "desactivado"].map((s) => <SelectItem key={s} value={s} className="capitalize">{s}</SelectItem>)}
          </SelectContent>
        </Select>
        <Select value={promotion || ALL} onValueChange={(v) => setPromotion(v === ALL ? "" : v)}>
          <SelectTrigger className="w-56" aria-label="Promoción"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>Todas las promociones</SelectItem>
            {couponPromos.map((p) => <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>)}
          </SelectContent>
        </Select>
        <Button variant="outline" onClick={() => void promoApi.exportCoupons({ promotion, state }).catch((e) => toast.error(errorMessage(e)))}><Download aria-hidden="true" /> Exportar CSV</Button>
        {canEdit && (
          <>
            <Button variant="outline" disabled={noPromos} onClick={() => setDialog("many")}><Wand2 aria-hidden="true" /> Generar en lote</Button>
            <Button disabled={noPromos} onClick={() => setDialog("one")}><Plus aria-hidden="true" /> Nuevo cupón</Button>
          </>
        )}
      </div>
      {noPromos && (
        <p className="rounded-md bg-warn-soft px-3 py-2 text-sm text-warn">
          Para crear cupones primero necesitas una promoción que pida cupón. <a className="underline" href={href("promociones", "nueva")}>Crear promoción</a>
        </p>
      )}
      {selected.size > 0 && canEdit && (
        <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/50 px-3 py-2 text-sm">
          <span className="font-medium">{selected.size} seleccionados</span>
          <Button size="sm" variant="outline" onClick={() => void bulk("enable")}>Activar</Button>
          <Button size="sm" variant="outline" onClick={() => void bulk("disable")}>Desactivar</Button>
          <Button size="sm" variant="outline" onClick={() => void bulk("delete")}>Eliminar los sin uso</Button>
        </div>
      )}
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      {!list.isLoading && items.length === 0 ? (
        <EmptyState icon={<Ticket />} title={dq || state || promotion ? "Ningún cupón coincide" : "Aún no hay cupones"} text="Un cupón activa una promoción cuando el comprador escribe su código en el carrito. Si no es válido, se le explica por qué." />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10"><span className="sr-only">Seleccionar</span></TableHead>
                <TableHead>Código</TableHead>
                <TableHead>Promoción</TableHead>
                <TableHead>Estado</TableHead>
                <TableHead className="text-right">Usos</TableHead>
                <TableHead>Vigencia</TableHead>
                <TableHead>Creado</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.isLoading ? <SkeletonRows cols={7} /> : items.map((c) => (
                <TableRow key={c.code}>
                  <TableCell><Checkbox aria-label={`Seleccionar ${c.code}`} checked={selected.has(c.code)} onCheckedChange={() => toggle(c.code)} /></TableCell>
                  <TableCell className="font-mono font-medium">{c.code}</TableCell>
                  <TableCell><a className="text-primary hover:underline" href={href("promociones", "p", c.promotionId)}>{c.promotionName}</a></TableCell>
                  <TableCell><Badge variant={STATE_TONE[c.state] ?? "outline"} className="capitalize">{c.state}</Badge></TableCell>
                  <TableCell className="text-right tabular-nums">{c.uses}{c.maxUses ? ` / ${c.maxUses}` : ""}</TableCell>
                  <TableCell className="text-muted-foreground">{windowText(c.startsAt, c.endsAt)}</TableCell>
                  <TableCell className="text-muted-foreground">{storeDate(c.createdAt)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <div className="flex items-center justify-between text-sm text-muted-foreground">
        <span>{total} cupones</span>
        {list.hasNextPage && <Button variant="outline" size="sm" onClick={() => void list.fetchNextPage()}>Ver más</Button>}
      </div>
      {(batches.data?.length ?? 0) > 0 && (
        <div className="grid gap-2">
          <h2 className="text-sm font-semibold">Lotes generados</h2>
          <ul className="divide-y rounded-lg border bg-card text-sm">
            {batches.data!.map((b) => (
              <li key={b.id} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2">
                <span><span className="font-mono font-medium">{b.prefix || "(sin prefijo)"}</span> · {b.quantity} códigos · {b.used} usados · {b.promotionName}</span>
                <span className="flex items-center gap-2 text-muted-foreground">
                  {storeDate(b.createdAt)}
                  <Button size="sm" variant="ghost" onClick={() => void promoApi.exportCoupons({ batch: b.id }).catch((e) => toast.error(errorMessage(e)))}><Download aria-hidden="true" /> CSV</Button>
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
      {dialog && <CouponDialog many={dialog === "many"} promos={couponPromos.map((p) => ({ id: p.id, name: p.name }))} defaultPromotion={promotion} onClose={() => setDialog(null)} onDone={refresh} />}
    </div>
  );
}

function CouponDialog({ many, promos, defaultPromotion, onClose, onDone }: { many: boolean; promos: { id: string; name: string }[]; defaultPromotion: string; onClose: () => void; onDone: () => void }) {
  const [spec, setSpec] = useState<CouponSpec>({ promotionId: defaultPromotion || promos[0]?.id || "", maxUses: many ? 1 : 0, maxUsesPerCustomer: 1 });
  const [code, setCode] = useState("");
  const [prefix, setPrefix] = useState("");
  const [quantity, setQuantity] = useState("100");
  const [length, setLength] = useState("8");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErrors({});
    try {
      if (many) {
        const b = await promoApi.generateCoupons({ ...spec, prefix: prefix.trim().toUpperCase(), length: readNumber(length, "Largo del código", 8), quantity: readNumber(quantity, "Cantidad") });
        toast.success(`${b.quantity} cupones generados. Descárgalos desde “Lotes generados”.`);
      } else {
        await promoApi.createCoupon(code.trim().toUpperCase(), spec);
        toast.success("Cupón creado.");
      }
      onDone();
      onClose();
    } catch (err) {
      const fe = fieldErrors(err);
      setErrors(Object.keys(fe).length ? fe : { form: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  }
  const num = (v: string) => Math.max(0, Number(v.replace(/\D/g, "")) || 0);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form onSubmit={(e) => void submit(e)} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{many ? "Generar cupones en lote" : "Nuevo cupón"}</DialogTitle>
            <DialogDescription>{many ? "Códigos únicos, por ejemplo para una campaña de correo. Cada código se guarda protegido." : "Un código que cualquiera puede usar, por ejemplo BIENVENIDA10."}</DialogDescription>
          </DialogHeader>
          <Field label="Promoción que activa" error={errors.promotionId}>
            <Select value={spec.promotionId} onValueChange={(v) => setSpec({ ...spec, promotionId: v })}>
              <SelectTrigger aria-label="Promoción"><SelectValue /></SelectTrigger>
              <SelectContent>{promos.map((p) => <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>)}</SelectContent>
            </Select>
          </Field>
          {many ? (
            <div className="grid gap-4 sm:grid-cols-3">
              <Field label="Prefijo" hint="Opcional" error={errors.prefix}><Input aria-label="Prefijo" maxLength={12} className="font-mono uppercase" value={prefix} onChange={(e) => setPrefix(e.target.value)} placeholder="NAVIDAD" /></Field>
              <Field label="Cantidad" error={errors.quantity}><Input aria-label="Cantidad" inputMode="numeric" value={quantity} onChange={(e) => setQuantity(e.target.value)} /></Field>
              <Field label="Largo del código" error={errors.length}><Input aria-label="Largo del código" inputMode="numeric" value={length} onChange={(e) => setLength(e.target.value)} /></Field>
            </div>
          ) : (
            <Field label="Código" error={errors.code}><Input aria-label="Código" required autoFocus maxLength={40} className="font-mono uppercase" value={code} onChange={(e) => setCode(e.target.value)} placeholder="BIENVENIDA10" /></Field>
          )}
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Usos totales por código" hint="0 = sin límite" error={errors.maxUses}><Input aria-label="Usos totales" inputMode="numeric" value={String(spec.maxUses)} onChange={(e) => setSpec({ ...spec, maxUses: num(e.target.value) })} /></Field>
            <Field label="Usos por cliente" hint="0 = sin límite" error={errors.maxUsesPerCustomer}><Input aria-label="Usos por cliente" inputMode="numeric" value={String(spec.maxUsesPerCustomer)} onChange={(e) => setSpec({ ...spec, maxUsesPerCustomer: num(e.target.value) })} /></Field>
            <Field label="Válido desde" hint="Opcional"><DateTimeField value={spec.startsAt} onChange={(v) => setSpec({ ...spec, startsAt: v })} /></Field>
            <Field label="Válido hasta" hint="Opcional" error={errors.endsAt}><DateTimeField value={spec.endsAt} onChange={(v) => setSpec({ ...spec, endsAt: v })} /></Field>
          </div>
          {errors.form && <p className="text-sm font-medium text-destructive" role="alert">{errors.form}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>Cancelar</Button>
            <Button type="submit" disabled={busy || !spec.promotionId}>{busy ? "Guardando…" : many ? "Generar" : "Crear cupón"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
