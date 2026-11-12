import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Plus, Trash2 } from "lucide-react";
import { errorMessage, request } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "@/components/ui/sonner";
import { centsToPesos, formatMoney, parsePesos } from "@/money";
import { dateTime } from "../common";

const enc = encodeURIComponent;
export interface Member { email: string; approver: boolean; level: number; monthlyLimit: number; location: string; admin: boolean; substitute: string; awayUntil: string; monthSpent?: number }
export interface Branch { id: string; name: string; priceGroup: string; monthlyLimit: number; address: Record<string, string>; monthSpent?: number }
export interface Rule { minTotal: number; level: number; category?: string; location?: string }
interface Step { level: number; action: string; at: string }
interface Req { id: string; status: string; total: number; currency: string; level: number; requiredLevel: number; reason: string; requester: string; location?: string; orderId?: string; createdAt: string; expiresAt: string; decisions: Step[] }

const api = {
  members: (id: string) => request<{ items: Member[] }>("GET", `/v1/companies/${enc(id)}/members`).then((r) => r.items),
  saveMembers: (id: string, items: Member[]) => request<{ items: Member[] }>("PUT", `/v1/companies/${enc(id)}/members`, { items }),
  branches: (id: string) => request<{ items: Branch[] }>("GET", `/v1/companies/${enc(id)}/locations`).then((r) => r.items),
  saveBranches: (id: string, items: Branch[]) => request<{ items: Branch[] }>("PUT", `/v1/companies/${enc(id)}/locations`, { items }),
  requests: (id: string) => request<{ items: Req[] }>("GET", `/v1/companies/${enc(id)}/requests`).then((r) => r.items),
};
const STATUS: Record<string, [string, "default" | "secondary" | "outline" | "bad" | "ok" | "warn"]> = {
  pending: ["En espera", "warn"], approved: ["Aprobada", "ok"], rejected: ["Rechazada", "bad"], expired: ["Vencida", "outline"], cancelled: ["Cancelada", "outline"],
};
const ACTION: Record<string, string> = { approved: "aprobó", rejected: "rechazó", expired: "venció", cancelled: "canceló quien compra" };

/** Buyers with their role, approval level, monthly cap, branch and substitute. */
export function Members({ id, canEdit, onChanged }: { id: string; canEdit: boolean; onChanged: () => void }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["company-members", id], queryFn: () => api.members(id), enabled: canEdit });
  const br = useQuery({ queryKey: ["company-branches", id], queryFn: () => api.branches(id) });
  const [edit, setEdit] = useState(false);
  const names = Object.fromEntries((br.data ?? []).map((b) => [b.id, b.name]));
  const list = q.data ?? [];
  return (
    <Card>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-3">
        <div className="grid gap-1.5"><CardTitle className="text-base">Compradores y aprobaciones ({list.length})</CardTitle>
          <CardDescription>Quién compra, hasta qué nivel aprueba, su tope mensual a crédito y su sucursal. Quien administra la empresa también los cambia desde “Mi cuenta” en tu tienda.</CardDescription></div>
        {canEdit && <Button variant="outline" onClick={() => setEdit(true)} disabled={!q.data}>Editar compradores</Button>}
      </CardHeader>
      <CardContent>
        {!canEdit ? <p className="text-sm text-muted-foreground">Tu acceso no incluye ver los correos.</p> : list.length === 0 ? <p className="text-sm text-muted-foreground">Sin compradores todavía.</p> : (
          <div className="overflow-auto rounded-md border"><Table>
            <TableHeader><TableRow><TableHead>Correo</TableHead><TableHead>Rol</TableHead><TableHead>Sucursal</TableHead><TableHead className="text-right">Este mes / tope</TableHead></TableRow></TableHeader>
            <TableBody>{list.map((m) => (
              <TableRow key={m.email}>
                <TableCell className="font-mono text-sm">{m.email}</TableCell>
                <TableCell className="space-x-1 whitespace-nowrap">{m.approver ? <Badge variant="secondary">Aprueba nivel {m.level}</Badge> : <span className="text-sm text-muted-foreground">Compra</span>}{m.admin && <Badge variant="outline">Administra</Badge>}
                  {m.awayUntil && <span className="block text-xs text-muted-foreground">Ausente hasta {m.awayUntil}{m.substitute ? ` · suple ${m.substitute}` : ""}</span>}</TableCell>
                <TableCell>{m.location ? names[m.location] ?? "—" : "Empresa"}</TableCell>
                <TableCell className="whitespace-nowrap text-right tabular-nums">{formatMoney(m.monthSpent ?? 0)}{m.monthlyLimit ? ` / ${formatMoney(m.monthlyLimit)}` : " / sin tope"}</TableCell>
              </TableRow>
            ))}</TableBody></Table></div>
        )}
      </CardContent>
      {edit && q.data && <MembersDialog id={id} members={q.data} branches={br.data ?? []} onClose={() => setEdit(false)} onDone={() => { void qc.invalidateQueries({ queryKey: ["company-members", id] }); onChanged(); }} />}
    </Card>
  );
}

const blank = (): Member => ({ email: "", approver: false, level: 1, monthlyLimit: 0, location: "", admin: false, substitute: "", awayUntil: "" });

function MembersDialog({ id, members, branches, onClose, onDone }: { id: string; members: Member[]; branches: Branch[]; onClose: () => void; onDone: () => void }) {
  const [rows, setRows] = useState(members.map((m) => ({ ...m, cap: m.monthlyLimit ? centsToPesos(m.monthlyLimit) : "" })));
  const [error, setError] = useState("");
  const set = (i: number, p: Partial<(typeof rows)[number]>) => setRows(rows.map((r, j) => (j === i ? { ...r, ...p } : r)));
  const save = useMutation({
    mutationFn: () => api.saveMembers(id, rows.filter((r) => r.email.trim()).map(({ cap, monthSpent: _s, ...r }) => ({ ...r, level: r.approver ? r.level : 1, monthlyLimit: parsePesos(cap) ?? 0 }))),
    onSuccess: () => { toast.success("Compradores guardados."); onDone(); onClose(); },
    onError: (e) => { setError(errorMessage(e)); toast.error(errorMessage(e)); },
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-4xl">
        <DialogHeader><DialogTitle>Compradores de la empresa</DialogTitle><DialogDescription>Quien aprueba de nivel 2 firma lo que supera al nivel 1. Si alguien se ausenta, su suplente recibe los enlaces mientras tanto.</DialogDescription></DialogHeader>
        <form id="mem-form" className="grid max-h-[60vh] gap-3 overflow-auto pr-1" onSubmit={(e: FormEvent) => { e.preventDefault(); save.mutate(); }}>
          {rows.map((r, i) => (
            <fieldset key={i} className="grid gap-2 rounded-md border p-3 sm:grid-cols-6">
              <legend className="sr-only">Comprador {i + 1}</legend>
              <div className="grid gap-1 sm:col-span-2"><Label htmlFor={`m-email-${i}`}>Correo</Label><Input id={`m-email-${i}`} type="email" className="font-mono" value={r.email} onChange={(e) => set(i, { email: e.target.value })} /></div>
              <div className="grid gap-1"><Label htmlFor={`m-cap-${i}`}>Tope al mes (MXN)</Label><Input id={`m-cap-${i}`} inputMode="decimal" placeholder="Sin tope" value={r.cap} onChange={(e) => set(i, { cap: e.target.value })} /></div>
              <div className="grid gap-1"><Label htmlFor={`m-loc-${i}`}>Sucursal</Label>
                <select id={`m-loc-${i}`} className="h-9 rounded-md border bg-background px-2 text-sm" value={r.location} onChange={(e) => set(i, { location: e.target.value })}>
                  <option value="">Empresa</option>{branches.map((b) => <option key={b.id} value={b.id}>{b.name}</option>)}</select></div>
              <div className="grid content-end gap-1 text-sm">
                <label className="flex items-center gap-2"><input type="checkbox" checked={r.approver} onChange={(e) => set(i, { approver: e.target.checked })} />Aprueba</label>
                <label className="flex items-center gap-2"><input type="checkbox" checked={r.admin} onChange={(e) => set(i, { admin: e.target.checked })} />Administra</label>
              </div>
              <div className="flex items-end justify-end"><Button type="button" variant="ghost" size="icon" aria-label={`Quitar ${r.email || "comprador"}`} onClick={() => setRows(rows.filter((_, j) => j !== i))}><Trash2 className="size-4" /></Button></div>
              {r.approver && <>
                <div className="grid gap-1"><Label htmlFor={`m-lvl-${i}`}>Nivel</Label>
                  <select id={`m-lvl-${i}`} className="h-9 rounded-md border bg-background px-2 text-sm" value={r.level} onChange={(e) => set(i, { level: Number(e.target.value) })}>{[1, 2, 3, 4, 5].map((n) => <option key={n} value={n}>{n}</option>)}</select></div>
                <div className="grid gap-1"><Label htmlFor={`m-away-${i}`}>Ausente hasta</Label><Input id={`m-away-${i}`} type="date" value={r.awayUntil} onChange={(e) => set(i, { awayUntil: e.target.value })} /></div>
                <div className="grid gap-1 sm:col-span-2"><Label htmlFor={`m-sub-${i}`}>Suplente</Label>
                  <select id={`m-sub-${i}`} className="h-9 rounded-md border bg-background px-2 text-sm" value={r.substitute} onChange={(e) => set(i, { substitute: e.target.value })}>
                    <option value="">Nadie</option>{rows.filter((o) => o.email && o.email !== r.email).map((o) => <option key={o.email} value={o.email}>{o.email}</option>)}</select></div>
              </>}
            </fieldset>
          ))}
          <Button type="button" variant="outline" className="justify-self-start" onClick={() => setRows([...rows, { ...blank(), cap: "" }])}><Plus className="size-4" /> Agregar comprador</Button>
          {error && <p className="text-sm text-bad" role="alert">{error}</p>}
        </form>
        <DialogFooter><Button variant="outline" onClick={onClose}>Cancelar</Button><Button type="submit" form="mem-form" disabled={save.isPending}>{save.isPending ? "Guardando…" : "Guardar"}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Branches: their own price list group, a monthly cap and delivery address. */
export function Branches({ id, canEdit }: { id: string; canEdit: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["company-branches", id], queryFn: () => api.branches(id) });
  const [edit, setEdit] = useState(false);
  const list = q.data ?? [];
  return (
    <Card>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-3">
        <div className="grid gap-1.5"><CardTitle className="text-base">Sucursales ({list.length})</CardTitle><CardDescription>Cada sucursal puede comprar con su propia lista de precios y un tope mensual a crédito.</CardDescription></div>
        {canEdit && <Button variant="outline" onClick={() => setEdit(true)} disabled={!q.data}>Editar sucursales</Button>}
      </CardHeader>
      <CardContent>{list.length === 0 ? <p className="text-sm text-muted-foreground">La empresa compra como una sola ubicación.</p> : (
        <ul className="grid gap-2 text-sm">{list.map((b) => (
          <li key={b.id} className="flex flex-wrap justify-between gap-2 rounded-md border p-2"><span><span className="font-medium">{b.name}</span>{b.address.city && <span className="text-muted-foreground"> · {b.address.city}</span>}{b.priceGroup && <Badge variant="outline" className="ml-2 font-mono">{b.priceGroup}</Badge>}</span>
            <span className="tabular-nums text-muted-foreground">{formatMoney(b.monthSpent ?? 0)} este mes{b.monthlyLimit ? ` de ${formatMoney(b.monthlyLimit)}` : ""}</span></li>
        ))}</ul>
      )}</CardContent>
      {edit && q.data && <BranchesDialog id={id} branches={q.data} onClose={() => setEdit(false)} onDone={() => { void qc.invalidateQueries({ queryKey: ["company-branches", id] }); void qc.invalidateQueries({ queryKey: ["company-members", id] }); }} />}
    </Card>
  );
}

function BranchesDialog({ id, branches, onClose, onDone }: { id: string; branches: Branch[]; onClose: () => void; onDone: () => void }) {
  const [rows, setRows] = useState(branches.map((b) => ({ ...b, cap: b.monthlyLimit ? centsToPesos(b.monthlyLimit) : "" })));
  const [error, setError] = useState("");
  const set = (i: number, p: Partial<(typeof rows)[number]>) => setRows(rows.map((r, j) => (j === i ? { ...r, ...p } : r)));
  const save = useMutation({
    mutationFn: () => api.saveBranches(id, rows.map(({ cap, monthSpent: _s, ...r }) => ({ ...r, monthlyLimit: parsePesos(cap) ?? 0 }))),
    onSuccess: () => { toast.success("Sucursales guardadas."); onDone(); onClose(); },
    onError: (e) => { setError(errorMessage(e)); toast.error(errorMessage(e)); },
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-3xl">
        <DialogHeader><DialogTitle>Sucursales</DialogTitle><DialogDescription>El grupo de precios es el “grupo de clientes” de una lista de precios. Vacío: la sucursal usa el precio de la empresa.</DialogDescription></DialogHeader>
        <form id="br-form" className="grid max-h-[60vh] gap-3 overflow-auto pr-1" onSubmit={(e: FormEvent) => { e.preventDefault(); save.mutate(); }}>
          {rows.map((r, i) => (
            <fieldset key={r.id || i} className="grid gap-2 rounded-md border p-3 sm:grid-cols-4">
              <legend className="sr-only">Sucursal {i + 1}</legend>
              <div className="grid gap-1 sm:col-span-2"><Label htmlFor={`b-name-${i}`}>Nombre</Label><Input id={`b-name-${i}`} value={r.name} onChange={(e) => set(i, { name: e.target.value })} /></div>
              <div className="grid gap-1"><Label htmlFor={`b-group-${i}`}>Grupo de precios</Label><Input id={`b-group-${i}`} className="font-mono" value={r.priceGroup} onChange={(e) => set(i, { priceGroup: e.target.value.trim() })} /></div>
              <div className="grid gap-1"><Label htmlFor={`b-cap-${i}`}>Tope al mes (MXN)</Label><Input id={`b-cap-${i}`} inputMode="decimal" placeholder="Sin tope" value={r.cap} onChange={(e) => set(i, { cap: e.target.value })} /></div>
              <div className="grid gap-1 sm:col-span-2"><Label htmlFor={`b-street-${i}`}>Calle y número</Label><Input id={`b-street-${i}`} value={r.address.street ?? ""} onChange={(e) => set(i, { address: { ...r.address, street: e.target.value } })} /></div>
              <div className="grid gap-1"><Label htmlFor={`b-city-${i}`}>Ciudad</Label><Input id={`b-city-${i}`} value={r.address.city ?? ""} onChange={(e) => set(i, { address: { ...r.address, city: e.target.value } })} /></div>
              <div className="flex items-end gap-2"><div className="grid flex-1 gap-1"><Label htmlFor={`b-cp-${i}`}>C.P.</Label><Input id={`b-cp-${i}`} value={r.address.postalCode ?? ""} onChange={(e) => set(i, { address: { ...r.address, postalCode: e.target.value } })} /></div>
                <Button type="button" variant="ghost" size="icon" aria-label={`Quitar ${r.name || "sucursal"}`} onClick={() => setRows(rows.filter((_, j) => j !== i))}><Trash2 className="size-4" /></Button></div>
            </fieldset>
          ))}
          <Button type="button" variant="outline" className="justify-self-start" onClick={() => setRows([...rows, { id: "", name: "", priceGroup: "", monthlyLimit: 0, address: {}, cap: "" }])}><Plus className="size-4" /> Agregar sucursal</Button>
          {error && <p className="text-sm text-bad" role="alert">{error}</p>}
        </form>
        <DialogFooter><Button variant="outline" onClick={onClose}>Cancelar</Button><Button type="submit" form="br-form" disabled={save.isPending}>{save.isPending ? "Guardando…" : "Guardar"}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Purchase requests of the company and each step they went through. */
export function Requests({ id }: { id: string }) {
  const q = useQuery({ queryKey: ["company-requests", id], queryFn: () => api.requests(id) });
  const br = useQuery({ queryKey: ["company-branches", id], queryFn: () => api.branches(id) });
  const names = Object.fromEntries((br.data ?? []).map((b) => [b.id, b.name]));
  const list = q.data ?? [];
  return (
    <Card>
      <CardHeader><CardTitle className="text-base">Solicitudes de aprobación ({list.length})</CardTitle><CardDescription>Compras a crédito que esperan, o esperaron, a quien aprueba en la empresa.</CardDescription></CardHeader>
      <CardContent>{list.length === 0 ? <p className="text-sm text-muted-foreground">Ninguna todavía.</p> : (
        <div className="max-h-96 overflow-auto rounded-md border"><Table>
          <TableHeader><TableRow><TableHead>Fecha</TableHead><TableHead>Quién</TableHead><TableHead>Motivo</TableHead><TableHead>Nivel</TableHead><TableHead>Estado</TableHead><TableHead className="text-right">Total</TableHead></TableRow></TableHeader>
          <TableBody>{list.map((r) => {
            const [label, tone] = STATUS[r.status] ?? [r.status, "outline"];
            return (
              <TableRow key={r.id}>
                <TableCell className="whitespace-nowrap text-muted-foreground">{dateTime(r.createdAt)}{r.status === "pending" && <span className="block text-xs">vence {dateTime(r.expiresAt)}</span>}</TableCell>
                <TableCell>{r.requester}{r.location && <span className="block text-xs text-muted-foreground">{names[r.location] ?? ""}</span>}</TableCell>
                <TableCell className="max-w-64 text-sm">{r.reason}{r.decisions.length > 0 && <span className="block text-xs text-muted-foreground">{r.decisions.map((d) => (d.level ? `Nivel ${d.level} ${ACTION[d.action] ?? d.action}` : ACTION[d.action] ?? d.action) + " " + dateTime(d.at)).join(" · ")}</span>}</TableCell>
                <TableCell className="whitespace-nowrap tabular-nums">{r.level} de {r.requiredLevel}</TableCell>
                <TableCell><Badge variant={tone}>{label}</Badge>{r.orderId && <a className="ml-2 text-sm text-primary hover:underline" href={`#pedidos/${enc(r.orderId)}`}>Ver pedido</a>}</TableCell>
                <TableCell className="text-right tabular-nums">{formatMoney(r.total, r.currency)}</TableCell>
              </TableRow>
            );
          })}</TableBody></Table></div>
      )}</CardContent>
    </Card>
  );
}

/** The rules that ask for more levels, inside the company form. */
export function RulesEditor({ rules, days, branches, onChange, onDays }: { rules: Rule[]; days: number; branches: Branch[]; onChange: (r: Rule[]) => void; onDays: (d: number) => void }) {
  const [amounts, setAmounts] = useState(rules.map((r) => centsToPesos(r.minTotal)));
  const set = (i: number, p: Partial<Rule>) => onChange(rules.map((r, j) => (j === i ? { ...r, ...p } : r)));
  return (
    <div className="grid gap-2 rounded-md border p-3 sm:col-span-2">
      <span className="text-sm font-medium">Reglas de aprobación por niveles</span>
      <p className="text-xs text-muted-foreground">Una compra que cumpla la regla necesita la firma de quien apruebe ese nivel (los niveles menores firman primero). Puedes limitarla a una categoría o a una sucursal.</p>
      {rules.map((r, i) => (
        <div key={i} className="grid items-end gap-2 sm:grid-cols-5">
          <div className="grid gap-1"><Label htmlFor={`r-min-${i}`}>Desde (MXN)</Label><Input id={`r-min-${i}`} inputMode="decimal" value={amounts[i] ?? ""} onChange={(e) => { const a = [...amounts]; a[i] = e.target.value; setAmounts(a); set(i, { minTotal: parsePesos(e.target.value) ?? 0 }); }} /></div>
          <div className="grid gap-1"><Label htmlFor={`r-lvl-${i}`}>Nivel</Label><select id={`r-lvl-${i}`} className="h-9 rounded-md border bg-background px-2 text-sm" value={r.level} onChange={(e) => set(i, { level: Number(e.target.value) })}>{[1, 2, 3, 4, 5].map((n) => <option key={n} value={n}>{n}</option>)}</select></div>
          <div className="grid gap-1"><Label htmlFor={`r-cat-${i}`}>Categoría (opcional)</Label><Input id={`r-cat-${i}`} className="font-mono" value={r.category ?? ""} onChange={(e) => set(i, { category: e.target.value.trim() })} /></div>
          <div className="grid gap-1"><Label htmlFor={`r-loc-${i}`}>Sucursal</Label><select id={`r-loc-${i}`} className="h-9 rounded-md border bg-background px-2 text-sm" value={r.location ?? ""} onChange={(e) => set(i, { location: e.target.value })}><option value="">Todas</option>{branches.map((b) => <option key={b.id} value={b.id}>{b.name}</option>)}</select></div>
          <Button type="button" variant="ghost" size="icon" aria-label={`Quitar regla ${i + 1}`} onClick={() => { onChange(rules.filter((_, j) => j !== i)); setAmounts(amounts.filter((_, j) => j !== i)); }}><Trash2 className="size-4" /></Button>
        </div>
      ))}
      <div className="flex flex-wrap items-end justify-between gap-2">
        <Button type="button" variant="outline" size="sm" onClick={() => { onChange([...rules, { minTotal: 0, level: 2 }]); setAmounts([...amounts, ""]); }}><Plus className="size-4" /> Agregar regla</Button>
        <div className="grid gap-1"><Label htmlFor="r-days">Una solicitud vence a los (días)</Label><Input id="r-days" className="w-28" type="number" min={1} max={90} value={days} onChange={(e) => onDays(Number(e.target.value))} /></div>
      </div>
    </div>
  );
}

interface AutopayState { on: boolean; savedAt?: string; lastTry?: string; failures: number; error?: string; reminderDays: number }

/** Automatic payment: the company saves its card from the store; the store sees it, sets the reminder and can turn it off. */
export function AutopayCard({ id, isAdmin, credit }: { id: string; isAdmin: boolean; credit: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["company-autopay", id], queryFn: () => request<AutopayState>("GET", `/v1/companies/${enc(id)}/autopay`), enabled: credit });
  const [days, setDays] = useState<string | null>(null);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["company-autopay", id] });
  const saveDays = useMutation({ mutationFn: (d: number) => request<AutopayState>("PUT", `/v1/companies/${enc(id)}/autopay`, { reminderDays: d }), onSuccess: () => { toast.success("Aviso guardado."); setDays(null); refresh(); }, onError: (e) => toast.error(errorMessage(e)) });
  const stop = useMutation({ mutationFn: () => request<void>("DELETE", `/v1/companies/${enc(id)}/autopay`, undefined, { as: "none" }), onSuccess: () => { toast.success("Pago automático apagado."); refresh(); }, onError: (e) => toast.error(errorMessage(e)) });
  if (!credit) return null;
  const a = q.data;
  return (
    <Card>
      <CardHeader><CardTitle className="text-base">Pago automático y avisos</CardTitle>
        <CardDescription>Quien administra la empresa guarda su tarjeta desde “Mi empresa” en tu tienda. Cobramos lo vencido el día que vence; tres rechazos seguidos lo apagan y le avisamos.</CardDescription></CardHeader>
      <CardContent className="grid gap-3 text-sm">
        {!a ? <p className="text-muted-foreground">Cargando…</p> : <>
          <p>{a.on ? <Badge variant="ok">Activo</Badge> : <Badge variant="outline">Apagado</Badge>}
            {a.savedAt && <span className="ml-2 text-muted-foreground">Tarjeta guardada {dateTime(a.savedAt)}</span>}
            {a.lastTry && <span className="ml-2 text-muted-foreground">· último intento {dateTime(a.lastTry)}</span>}</p>
          {a.error && <p className="text-bad">{a.error}{a.failures ? ` (${a.failures} de 3)` : ""}</p>}
          <form className="flex flex-wrap items-end gap-2" onSubmit={(e: FormEvent) => { e.preventDefault(); saveDays.mutate(Number(days ?? a.reminderDays)); }}>
            <div className="grid gap-1"><Label htmlFor="ap-days">Aviso antes de cada vencimiento (días, 0 = sin aviso)</Label><Input id="ap-days" className="w-28" type="number" min={0} max={30} value={days ?? String(a.reminderDays)} onChange={(e) => setDays(e.target.value)} disabled={!isAdmin} /></div>
            {isAdmin && <Button type="submit" variant="outline" disabled={saveDays.isPending || days === null}>Guardar aviso</Button>}
            {isAdmin && a.on && <Button type="button" variant="outline" onClick={() => { if (confirm("¿Apagar el pago automático de esta empresa? Se borra su tarjeta guardada.")) stop.mutate(); }} disabled={stop.isPending}>Apagar pago automático</Button>}
          </form>
        </>}
      </CardContent>
    </Card>
  );
}
