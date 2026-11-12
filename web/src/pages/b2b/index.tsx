import { ProductPicker } from "../catalog/ProductPicker";
import { CustomFields } from "@/components/CustomFields";
import { AutopayCard, Branches, Members, Requests, Roles, RulesEditor, type Branch, type Rule } from "./CompanyDepth";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Building2, Copy, Database, Download, Plus, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { centsToPesos, formatMoney, parsePesos } from "@/money";
import { can } from "@/perms";
import { href } from "@/router";
import { dateTime, ErrorBox, Notice, Page } from "../common";
import { EmptyState, SkeletonRows } from "../promotions/shared";

const enc = encodeURIComponent;
interface Company { id: string; name: string; rfc: string; status: string; terms: string; creditLimit: number; discountBps: number; note: string; balance: number; overdue: number; buyers: number; priceGroup: string; taxExempt?: boolean; taxExemptUntil?: string; requirePO?: boolean; creditOnly?: boolean; approvalThreshold: number; approvalRules?: Rule[]; approvalDays?: number; catalogRestricted?: boolean;
  parentId?: string; parentName?: string; sharedCredit?: boolean; inheritTerms?: boolean;
  children?: { id: string; name: string; status: string; balance: number; sharedCredit: boolean; inheritTerms: boolean; children: number }[];
  pool?: { rootId: string; rootName: string; limit: number; balance: number; available: number; members: string[] } }
interface CEntry { id: string; kind: string; amount: number; orderId?: string; orderNumber?: number; dueAt?: string; note?: string; at: string }
interface Field { key: string; label: string; type: string; required: boolean; options?: string[] }
interface Entity { id: string; name: string; description: string; public: boolean; showOnProduct?: boolean; success: string; fields: Field[]; records: number }
interface Rec { id: string; data: Record<string, string>; source: string; createdAt: string }

const api = {
  companies: () => request<{ items: Company[] }>("GET", "/v1/companies").then((r) => r.items),
  company: (id: string) => request<{ company: Company; buyers: string[]; approvers: string[]; statement: CEntry[] }>("GET", `/v1/companies/${enc(id)}`),
  saveCompany: (c: Partial<Company> & { buyerEmails?: string[]; approverEmails?: string[] }) => (c.id ? request<Company>("PUT", `/v1/companies/${enc(c.id)}`, c) : request<Company>("POST", "/v1/companies", c)),
  entry: (id: string, b: { kind: string; amount: number; note: string }) => request<void>("POST", `/v1/companies/${enc(id)}/entries`, b, { as: "none" }),
  entities: () => request<{ items: Entity[]; formBase: string }>("GET", "/v1/entities"),
  saveEntity: (e: Entity) => request<Entity>("PUT", `/v1/entities/${enc(e.id)}`, e),
  deleteEntity: (id: string) => request<void>("DELETE", `/v1/entities/${enc(id)}`, undefined, { as: "none" }),
  records: (id: string) => request<{ entity: Entity; items: Rec[] }>("GET", `/v1/entities/${enc(id)}/records?limit=200`),
  deleteRecord: (id: string, rec: string) => request<void>("DELETE", `/v1/entities/${enc(id)}/records/${enc(rec)}`, undefined, { as: "none" }),
};
const TERMS: Record<string, string> = { prepaid: "De contado", net15: "Crédito a 15 días", net30: "Crédito a 30 días", net60: "Crédito a 60 días" };
const CKIND: Record<string, string> = { charge: "Pedido a crédito", payment: "Pago recibido", refund: "Reembolso", release: "Pedido no completado", adjust: "Ajuste" };

/** Routes: #empresas, #empresas/<id>. */
export function Companies({ me, route }: { me: Me; route: string[] }) {
  const isAdmin = can(me, "customers", "admin");
  return route[1] ? <CompanyPage id={route[1]} isAdmin={isAdmin} /> : <CompanyList isAdmin={isAdmin} />;
}

function CompanyList({ isAdmin }: { isAdmin: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["companies"], queryFn: api.companies });
  const [create, setCreate] = useState(false);
  const items = q.data ?? [];
  return (
    <Page title="Empresas (B2B)" sub="Clientes de negocio con su propio precio y, si quieres, compra a crédito. Sus compradores entran a su cuenta en la tienda y ven el precio de empresa al pagar."
      actions={isAdmin && <Button onClick={() => setCreate(true)}><Plus aria-hidden="true" /> Nueva empresa</Button>}>
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {items.length > 0 && (
        <div className="grid gap-3 sm:grid-cols-3">{[["Empresas", String(items.length)], ["Por cobrar", formatMoney(items.reduce((a, c) => a + Math.max(c.balance, 0), 0))], ["Vencido", formatMoney(items.reduce((a, c) => a + c.overdue, 0))]].map(([l, v]) => (
          <Card key={l}><CardContent className="grid gap-1 pt-5"><span className="text-sm text-muted-foreground">{l}</span><span className="text-xl font-semibold tabular-nums">{v}</span></CardContent></Card>
        ))}</div>
      )}
      {!q.isLoading && items.length === 0 ? <EmptyState icon={<Building2 />} title="Todavía no tienes empresas" text="Da de alta a tus clientes de mayoreo: define su descuento, su plazo de pago y quién puede comprar a su nombre." /> : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader><TableRow><TableHead>Empresa</TableHead><TableHead>Condiciones</TableHead><TableHead>Precio</TableHead><TableHead className="text-right">Debe</TableHead><TableHead className="text-right">Crédito disponible</TableHead><TableHead>Estado</TableHead></TableRow></TableHeader>
            <TableBody>{q.isLoading ? <SkeletonRows cols={6} /> : items.map((c) => (
              <TableRow key={c.id}>
                <TableCell><a className="font-medium text-primary hover:underline" href={href("empresas", c.id)}>{c.name}</a><div className="text-xs text-muted-foreground">{c.rfc || "Sin RFC"} · {c.buyers} comprador(es)</div></TableCell>
                <TableCell>{TERMS[c.terms]}</TableCell><TableCell>{c.discountBps ? `${c.discountBps / 100}% de descuento` : "Precio de lista"}</TableCell>
                <TableCell className="text-right tabular-nums">{formatMoney(c.balance)}{c.overdue > 0 && <div className="text-xs text-bad">{formatMoney(c.overdue)} vencido</div>}</TableCell>
                <TableCell className="text-right tabular-nums">{c.terms === "prepaid" ? "—" : formatMoney(Math.max(c.creditLimit - c.balance, 0))}</TableCell>
                <TableCell>{c.status === "active" ? <Badge variant="ok">Activa</Badge> : <Badge variant="outline">Suspendida</Badge>}</TableCell>
              </TableRow>
            ))}</TableBody>
          </Table>
        </div>
      )}
      {create && <CompanyDialog onClose={() => setCreate(false)} onDone={() => void qc.invalidateQueries({ queryKey: ["companies"] })} />}
    </Page>
  );
}

function CompanyDialog({ company, buyers, approvers, onClose, onDone }: { company?: Company; buyers?: string[]; approvers?: string[]; onClose: () => void; onDone: () => void }) {
  const [approverEmails, setApproverEmails] = useState((approvers ?? []).join("\n"));
  const [threshold, setThreshold] = useState(company?.approvalThreshold ? centsToPesos(company.approvalThreshold) : "");
  const [c, setC] = useState<Partial<Company>>(company ?? { name: "", rfc: "", terms: "prepaid", status: "active", note: "" });
  const [limit, setLimit] = useState(centsToPesos(company?.creditLimit ?? 0));
  const [pct, setPct] = useState(String((company?.discountBps ?? 0) / 100));
  const [emails, setEmails] = useState((buyers ?? []).filter((b) => !(approvers ?? []).includes(b)).join("\n"));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const all = useQuery({ queryKey: ["companies"], queryFn: () => api.companies() });
  const branches = useQuery({ queryKey: ["company-branches", company?.id], queryFn: () => request<{ items: Branch[] }>("GET", `/v1/companies/${enc(company!.id)}/locations`).then((r) => r.items), enabled: !!company });
  const save = useMutation({
    mutationFn: () => api.saveCompany({ ...c, creditLimit: c.terms === "prepaid" ? 0 : parsePesos(limit) ?? 0, discountBps: Math.round((Number(pct.replace(",", ".")) || 0) * 100), buyerEmails: emails.split(/[\n,;]+/).map((x) => x.trim()).filter(Boolean),
      approverEmails: approverEmails.split(/[\n,;]+/).map((x) => x.trim()).filter(Boolean), approvalThreshold: parsePesos(threshold) ?? 0 }),
    onSuccess: () => { toast.success("Empresa guardada."); onDone(); onClose(); },
    onError: (e) => { setErrors(fieldErrors(e)); toast.error(errorMessage(e)); },
  });
  const err = (k: string) => errors[k] && <p className="text-sm text-bad">{errors[k]}</p>;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader><DialogTitle>{company ? "Editar empresa" : "Nueva empresa"}</DialogTitle><DialogDescription>El precio y el crédito aplican cuando un comprador de la lista entra a su cuenta en tu tienda.</DialogDescription></DialogHeader>
        <form id="co-form" className="grid gap-4 sm:grid-cols-2" onSubmit={(e: FormEvent) => { e.preventDefault(); save.mutate(); }}>
          <div className="grid gap-1.5"><Label htmlFor="co-name">Nombre o razón social</Label><Input id="co-name" value={c.name ?? ""} onChange={(e) => setC({ ...c, name: e.target.value })} />{err("name")}</div>
          <div className="grid gap-1.5"><Label htmlFor="co-rfc">RFC (opcional)</Label><Input id="co-rfc" className="font-mono uppercase" maxLength={13} value={c.rfc ?? ""} onChange={(e) => setC({ ...c, rfc: e.target.value.toUpperCase() })} />{err("rfc")}</div>
          <div className="grid gap-1.5"><Label htmlFor="co-pct">Descuento sobre precio de lista (%)</Label><Input id="co-pct" inputMode="decimal" value={pct} onChange={(e) => setPct(e.target.value)} />{err("discountBps")}</div>
          <div className="grid gap-1.5"><span className="text-sm font-medium">Forma de pago</span><Select value={c.terms ?? "prepaid"} onValueChange={(v) => setC({ ...c, terms: v })}><SelectTrigger aria-label="Forma de pago"><SelectValue /></SelectTrigger><SelectContent>{Object.entries(TERMS).map(([k, l]) => <SelectItem key={k} value={k}>{l}</SelectItem>)}</SelectContent></Select></div>
          {c.terms !== "prepaid" && <div className="grid gap-1.5"><Label htmlFor="co-limit">Límite de crédito (MXN)</Label><Input id="co-limit" inputMode="decimal" value={limit} onChange={(e) => setLimit(e.target.value)} />{err("creditLimit")}</div>}
          {company && <div className="grid gap-1.5"><span className="text-sm font-medium">Estado</span><Select value={c.status ?? "active"} onValueChange={(v) => setC({ ...c, status: v })}><SelectTrigger aria-label="Estado"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="active">Activa</SelectItem><SelectItem value="suspended">Suspendida (compra como cliente normal)</SelectItem></SelectContent></Select></div>}
          <fieldset className="grid gap-2 rounded-md border p-3 sm:col-span-2"><legend className="px-1 text-sm font-medium">Grupo de empresas</legend>
            <div className="grid gap-1.5"><Label htmlFor="co-parent">Empresa matriz</Label>
              <select id="co-parent" className="h-9 rounded-md border bg-background px-2 text-sm" value={c.parentId ?? ""} onChange={(e) => setC({ ...c, parentId: e.target.value, ...(e.target.value ? {} : { sharedCredit: false, inheritTerms: false }) })}>
                <option value="">Ninguna (es independiente o es la matriz)</option>
                {(all.data ?? []).filter((x: Company) => x.id !== company?.id).map((x: Company) => <option key={x.id} value={x.id}>{x.name}</option>)}
              </select>{err("parentId")}</div>
            {c.parentId && <>
              <label className="flex items-start gap-2 text-sm"><input type="checkbox" className="mt-1" checked={!!c.sharedCredit} onChange={(e) => setC({ ...c, sharedCredit: e.target.checked })} /><span>Compra con el crédito de la matriz<span className="block text-muted-foreground">La matriz y sus filiales comparten un solo límite; ninguna puede gastar el crédito de otra a la vez.</span></span></label>
              <label className="flex items-start gap-2 text-sm"><input type="checkbox" className="mt-1" checked={!!c.inheritTerms} onChange={(e) => setC({ ...c, inheritTerms: e.target.checked })} /><span>Usa las condiciones de la matriz<span className="block text-muted-foreground">Plazo, descuento, lista de precios, catálogo, exención y orden de compra se toman de la matriz.</span></span></label>
            </>}
          </fieldset>
          <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="co-buyers">Correos de sus compradores (uno por línea)</Label><Textarea id="co-buyers" rows={4} className="font-mono" value={emails} onChange={(e) => setEmails(e.target.value)} placeholder={"compras@empresa.com\ndirector@empresa.com"} />{err("buyers")}<p className="text-xs text-muted-foreground">Cada persona entra a “Mi cuenta” en tu tienda con su correo (le llega un código). Así nadie más puede usar el crédito.</p></div>
          <div className="grid gap-1.5"><Label htmlFor="co-group">Grupo de precios (precio por producto)</Label><Input id="co-group" className="font-mono" value={c.priceGroup ?? ""} onChange={(e) => setC({ ...c, priceGroup: e.target.value.trim() })} placeholder="acme" />{err("priceGroup")}<p className="text-xs text-muted-foreground">Escribe el mismo “grupo de clientes” de una lista de precios (Promociones › Listas de precios). Esa lista fija el precio de cada producto para esta empresa.</p></div>
          <div className="grid gap-1.5"><Label htmlFor="co-thr">Pedir aprobación desde (MXN)</Label><Input id="co-thr" inputMode="decimal" value={threshold} onChange={(e) => setThreshold(e.target.value)} placeholder="Sin aprobación" />{err("approvalThreshold")}<p className="text-xs text-muted-foreground">Las compras a crédito por encima de este total esperan a que alguien de la empresa las apruebe.</p></div>
          <div className="grid gap-2 sm:col-span-2">
            <label className="flex items-start gap-2 text-sm"><input type="checkbox" className="mt-1" checked={!!c.requirePO} onChange={(e) => setC({ ...c, requirePO: e.target.checked })} /><span>Pedir número de orden de compra<span className="block text-muted-foreground">Sus compradores lo escriben al pagar y aparece en el pedido y en la exportación.</span></span></label>
            <label className="flex items-start gap-2 text-sm"><input type="checkbox" className="mt-1" checked={!!c.creditOnly} disabled={c.terms === "prepaid"} onChange={(e) => setC({ ...c, creditOnly: e.target.checked })} /><span>Solo puede pagar a crédito<span className="block text-muted-foreground">Oculta el pago con tarjeta para esta empresa.</span></span></label>
            <label className="flex items-start gap-2 text-sm"><input type="checkbox" className="mt-1" checked={!!c.catalogRestricted} onChange={(e) => setC({ ...c, catalogRestricted: e.target.checked })} /><span>Solo ve su catálogo negociado<span className="block text-muted-foreground">Sus compradores solo ven y compran los productos con precio en una lista de su grupo de precios.</span></span></label>
            {errors.creditOnly && <p className="text-sm text-bad">{errors.creditOnly}</p>}
            {errors.catalogRestricted && <p className="text-sm text-bad">{errors.catalogRestricted}</p>}
          </div>
          <div className="grid gap-1.5 sm:col-span-2"><label className="flex items-start gap-2 text-sm"><input type="checkbox" className="mt-1" checked={!!c.taxExempt} onChange={(e) => setC({ ...c, taxExempt: e.target.checked })} /><span>Exenta de impuestos<span className="block text-muted-foreground">Sus pedidos se cobran sin impuesto. Guarda su constancia en la nota interna.</span></span></label>
            {c.taxExempt && <div className="grid gap-1.5"><Label htmlFor="co-exu">Vigente hasta (opcional)</Label><Input id="co-exu" type="date" value={c.taxExemptUntil ?? ""} onChange={(e) => setC({ ...c, taxExemptUntil: e.target.value })} />{errors.taxExemptUntil && <p className="text-sm text-bad">{errors.taxExemptUntil}</p>}</div>}</div>
          <RulesEditor rules={c.approvalRules ?? []} days={c.approvalDays ?? 7} branches={branches.data ?? []} onChange={(r) => setC({ ...c, approvalRules: r })} onDays={(d) => setC({ ...c, approvalDays: d })} />
          {(errors.approvalRules || errors.approvalDays) && <p className="text-sm text-bad sm:col-span-2">{errors.approvalRules || errors.approvalDays}</p>}
          <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="co-appr">Correos de quienes aprueban (uno por línea)</Label><Textarea id="co-appr" rows={2} className="font-mono" value={approverEmails} onChange={(e) => setApproverEmails(e.target.value)} placeholder="direccion@empresa.com" />{err("approvers")}<p className="text-xs text-muted-foreground">Aprueban el nivel 1 y también compran. Para niveles, topes por persona, sucursales y suplentes usa “Editar compradores” en la página de la empresa.</p></div>
          <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="co-note">Nota interna</Label><Input id="co-note" value={c.note ?? ""} onChange={(e) => setC({ ...c, note: e.target.value })} /></div>
        </form>
        <DialogFooter><Button variant="outline" onClick={onClose}>Cancelar</Button><Button type="submit" form="co-form" disabled={save.isPending}>{save.isPending ? "Guardando…" : "Guardar"}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function CompanyPage({ id, isAdmin }: { id: string; isAdmin: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["company", id], queryFn: () => api.company(id) });
  const [edit, setEdit] = useState(false);
  const [pay, setPay] = useState<"payment" | "adjust" | null>(null);
  const refresh = () => { void qc.invalidateQueries({ queryKey: ["company", id] }); void qc.invalidateQueries({ queryKey: ["companies"] }); };
  if (q.error || !q.data) return <Page title="Empresa"><ErrorBox error={q.error} onRetry={() => void q.refetch()} />{!q.error && <p className="text-sm text-muted-foreground">Cargando…</p>}</Page>;
  const { company: c, buyers, approvers, statement } = q.data;
  return (
    <Page title={c.name} sub={<><a className="hover:underline" href={href("empresas")}>← Empresas</a> · {TERMS[c.terms]}{c.discountBps ? ` · ${c.discountBps / 100}% de descuento` : ""}{c.status !== "active" ? " · suspendida" : ""}</>}
      actions={isAdmin && <><Button variant="outline" onClick={() => setEdit(true)}>Editar</Button></>}>
      <div className="grid gap-3 sm:grid-cols-3">{[["Debe", formatMoney(c.balance)], ["Vencido", formatMoney(c.overdue)], ["Crédito disponible", c.pool ? formatMoney(c.pool.available) : c.terms === "prepaid" ? "No aplica" : formatMoney(Math.max(c.creditLimit - c.balance, 0))]].map(([l, v]) => (
        <Card key={l}><CardContent className="grid gap-1 pt-5"><span className="text-sm text-muted-foreground">{l}</span><span className="text-xl font-semibold tabular-nums">{v}</span></CardContent></Card>
      ))}</div>
      {(c.parentId || (c.children?.length ?? 0) > 0 || c.pool) && (
        <Card data-company-group><CardHeader><CardTitle className="text-base">Grupo</CardTitle>
          <CardDescription>{c.parentId ? <>Filial de <a className="text-primary underline" href={href("empresas", c.parentId)}>{c.parentName}</a>{c.sharedCredit ? " · compra con su crédito" : ""}{c.inheritTerms ? " · usa sus condiciones" : ""}</> : "Matriz del grupo"}</CardDescription></CardHeader>
          <CardContent className="grid gap-3">
            {c.pool && <p className="text-sm">Crédito compartido de <strong>{c.pool.rootName}</strong>: límite {formatMoney(c.pool.limit)}, deben {formatMoney(c.pool.balance)} entre {c.pool.members.length} empresas, disponible <strong>{formatMoney(c.pool.available)}</strong>.</p>}
            {(c.children?.length ?? 0) > 0 && <Table><TableHeader><TableRow><TableHead>Filial</TableHead><TableHead>Crédito</TableHead><TableHead>Condiciones</TableHead><TableHead className="text-right">Debe</TableHead></TableRow></TableHeader>
              <TableBody>{c.children!.map((k) => <TableRow key={k.id}><TableCell><a className="text-primary underline" href={href("empresas", k.id)}>{k.name}</a>{k.children ? <span className="text-xs text-muted-foreground"> · {k.children} sucursales</span> : null}{k.status !== "active" && <span className="text-xs text-bad"> · suspendida</span>}</TableCell>
                <TableCell>{k.sharedCredit ? "Compartido" : "Propio"}</TableCell><TableCell>{k.inheritTerms ? "De la matriz" : "Propias"}</TableCell><TableCell className="text-right tabular-nums">{formatMoney(k.balance)}</TableCell></TableRow>)}</TableBody></Table>}
          </CardContent></Card>
      )}
      {c.overdue > 0 && <Notice tone="warn">Esta empresa tiene {formatMoney(c.overdue)} vencido. Puedes suspenderla para que no compre a crédito hasta que pague.</Notice>}
      <Card>
        <CardHeader className="flex-row flex-wrap items-start justify-between gap-3"><div className="grid gap-1.5"><CardTitle className="text-base">Estado de cuenta</CardTitle><CardDescription>Los pedidos a crédito suman; los pagos y reembolsos restan.</CardDescription></div>
          {isAdmin && <div className="flex gap-2"><Button variant="outline" onClick={() => setPay("adjust")}>Ajuste</Button><Button onClick={() => setPay("payment")}>Registrar pago</Button></div>}</CardHeader>
        <CardContent>
          {statement.length === 0 ? <p className="text-sm text-muted-foreground">Sin movimientos todavía.</p> : (
            <div className="max-h-80 overflow-auto rounded-md border"><Table>
              <TableHeader><TableRow><TableHead>Fecha</TableHead><TableHead>Movimiento</TableHead><TableHead>Vence</TableHead><TableHead className="text-right">Monto</TableHead></TableRow></TableHeader>
              <TableBody>{statement.map((e) => (
                <TableRow key={e.id}><TableCell className="whitespace-nowrap text-muted-foreground">{dateTime(e.at)}</TableCell>
                  <TableCell>{CKIND[e.kind] ?? e.kind}{e.orderId && <> · <a className="text-primary hover:underline" href={href("pedidos", e.orderId)}>pedido #{e.orderNumber}</a></>}{e.note && <span className="text-muted-foreground"> · {e.note}</span>}</TableCell>
                  <TableCell className="whitespace-nowrap">{e.dueAt ? dateTime(e.dueAt) : "—"}</TableCell>
                  <TableCell className={`text-right tabular-nums ${e.amount < 0 ? "text-ok" : ""}`}>{e.amount < 0 ? "−" : "+"}{formatMoney(Math.abs(e.amount))}</TableCell></TableRow>
              ))}</TableBody></Table></div>
          )}
        </CardContent>
      </Card>
      <CustomFields resource="company" id={id} canEdit={isAdmin} />
      <Roles id={id} canEdit={isAdmin} />
      <Members id={id} canEdit={isAdmin} onChanged={refresh} />
      <Branches id={id} canEdit={isAdmin} />
      <Requests id={id} />
      <AutopayCard id={id} isAdmin={isAdmin} credit={c.terms !== "prepaid"} />
      {edit && <CompanyDialog company={c} buyers={buyers} approvers={approvers} onClose={() => setEdit(false)} onDone={refresh} />}
      {pay && <PayDialog id={id} kind={pay} balance={c.balance} onClose={() => setPay(null)} onDone={refresh} />}
    </Page>
  );
}

function PayDialog({ id, kind, balance, onClose, onDone }: { id: string; kind: "payment" | "adjust"; balance: number; onClose: () => void; onDone: () => void }) {
  const [amount, setAmount] = useState(kind === "payment" && balance > 0 ? (balance / 100).toFixed(2) : "");
  const [note, setNote] = useState("");
  const neg = amount.trim().startsWith("-");
  const cents = parsePesos(amount.replace(/^-/, ""));
  const save = useMutation({ mutationFn: () => api.entry(id, { kind, amount: (neg ? -1 : 1) * (cents ?? 0), note }), onSuccess: () => { toast.success(kind === "payment" ? "Pago registrado." : "Ajuste registrado."); onDone(); onClose(); }, onError: (e) => toast.error(errorMessage(e)) });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader><DialogTitle>{kind === "payment" ? "Registrar pago de la empresa" : "Ajuste a la cuenta"}</DialogTitle><DialogDescription>{kind === "payment" ? `Debe ${formatMoney(balance)}. Anota la transferencia que recibiste.` : "Positivo aumenta lo que debe; negativo lo reduce (por ejemplo -200)."}</DialogDescription></DialogHeader>
        <form id="cpay-form" className="grid gap-4" onSubmit={(e: FormEvent) => { e.preventDefault(); save.mutate(); }}>
          <div className="grid gap-1.5"><Label htmlFor="cp-amount">Monto (MXN)</Label><Input id="cp-amount" inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} /></div>
          <div className="grid gap-1.5"><Label htmlFor="cp-note">{kind === "payment" ? "Referencia" : "Motivo"}</Label><Input id="cp-note" value={note} onChange={(e) => setNote(e.target.value)} /></div>
        </form>
        <DialogFooter><Button variant="outline" onClick={onClose}>Cancelar</Button><Button type="submit" form="cpay-form" disabled={save.isPending || !cents || !note.trim()}>Guardar</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------- custom data ----------

const FTYPES: [string, string][] = [["text", "Texto corto"], ["textarea", "Texto largo"], ["email", "Correo"], ["phone", "Teléfono"], ["number", "Número"], ["select", "Lista de opciones"], ["checkbox", "Casilla"], ["product", "Producto"]];
const slug = (s: string) => s.toLowerCase().normalize("NFD").replace(/[̀-ͯ]/g, "").replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "").slice(0, 40);
const keyOf = (s: string) => slug(s).replace(/-/g, "_").replace(/^[^a-z]+/, "").slice(0, 40);

/** Routes: #datos, #datos/<id>. */
export function CustomData({ me, route }: { me: Me; route: string[] }) {
  const isAdmin = can(me, "settings", "admin");
  return route[1] ? <Records id={route[1]} canEdit={can(me, "settings", "edit")} /> : <EntityList isAdmin={isAdmin} />;
}

function EntityList({ isAdmin }: { isAdmin: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["entities"], queryFn: api.entities });
  const [edit, setEdit] = useState<Entity | "new" | null>(null);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["entities"] });
  const del = useMutation({ mutationFn: (id: string) => api.deleteEntity(id), onSuccess: () => { toast.success("Eliminado con todos sus registros."); refresh(); }, onError: (e) => toast.error(errorMessage(e)) });
  const items = q.data?.items ?? [];
  return (
    <Page title="Datos y formularios" sub="Guarda la información propia de tu negocio (solicitudes de mayoreo, garantías, mensajes de contacto) y recíbela con un formulario en tu tienda. Las respuestas se guardan cifradas."
      actions={isAdmin && <Button onClick={() => setEdit("new")}><Plus aria-hidden="true" /> Nuevo formulario</Button>}>
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {!q.isLoading && items.length === 0 ? <EmptyState icon={<Database />} title="Todavía no tienes formularios" text="Crea uno, define sus campos y comparte el enlace o ponlo en el menú de tu tienda." /> : (
        <div className="grid gap-3">{items.map((e) => (
          <Card key={e.id}><CardContent className="flex flex-wrap items-center justify-between gap-3 pt-5">
            <div className="grid gap-1"><a className="font-medium text-primary hover:underline" href={href("datos", e.id)}>{e.name}</a><span className="text-sm text-muted-foreground">{e.records} registro(s) · {e.fields.length} campos · {e.public ? "formulario abierto en la tienda" : "solo uso interno"}</span>
              {e.public && q.data && <span className="flex items-center gap-2 text-xs"><code className="break-all">{q.data.formBase + e.id}</code><Button size="icon" variant="ghost" aria-label="Copiar enlace del formulario" onClick={() => void navigator.clipboard.writeText(q.data.formBase + e.id).then(() => toast.success("Enlace copiado."))}><Copy aria-hidden="true" /></Button></span>}</div>
            {isAdmin && <div className="flex gap-2"><Button variant="outline" onClick={() => setEdit(e)}>Editar campos</Button><Button variant="ghost" size="icon" aria-label={`Eliminar ${e.name}`} onClick={() => window.confirm(`¿Eliminar “${e.name}” y sus ${e.records} registros? No se puede deshacer.`) && del.mutate(e.id)}><Trash2 aria-hidden="true" /></Button></div>}
          </CardContent></Card>
        ))}</div>
      )}
      {edit && <EntityDialog entity={edit === "new" ? undefined : edit} onClose={() => setEdit(null)} onDone={refresh} />}
    </Page>
  );
}

function EntityDialog({ entity, onClose, onDone }: { entity?: Entity; onClose: () => void; onDone: () => void }) {
  const [e, setE] = useState<Entity>(entity ?? { id: "", name: "", description: "", public: true, success: "", fields: [{ key: "nombre", label: "Nombre", type: "text", required: true }, { key: "correo", label: "Correo", type: "email", required: true }], records: 0 });
  const save = useMutation({
    mutationFn: () => api.saveEntity({ ...e, id: entity?.id ?? slug(e.name), fields: e.fields.map((f) => ({ ...f, key: f.key || keyOf(f.label) })) }),
    onSuccess: () => { toast.success("Formulario guardado."); onDone(); onClose(); }, onError: (er) => toast.error(errorMessage(er)),
  });
  const setF = (i: number, patch: Partial<Field>) => setE({ ...e, fields: e.fields.map((f, j) => (j === i ? { ...f, ...patch } : f)) });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-3xl overflow-y-auto">
        <DialogHeader><DialogTitle>{entity ? `Editar ${entity.name}` : "Nuevo formulario"}</DialogTitle><DialogDescription>Define qué información quieres guardar.</DialogDescription></DialogHeader>
        <form id="ent-form" className="grid gap-4" onSubmit={(ev: FormEvent) => { ev.preventDefault(); save.mutate(); }}>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="grid gap-1.5"><Label htmlFor="en-name">Nombre</Label><Input id="en-name" value={e.name} onChange={(ev) => setE({ ...e, name: ev.target.value })} placeholder="Solicitudes de mayoreo" /></div>
            <div className="grid gap-1.5"><Label htmlFor="en-ok">Mensaje al enviar</Label><Input id="en-ok" value={e.success} onChange={(ev) => setE({ ...e, success: ev.target.value })} placeholder="¡Gracias! Te contactamos en 24 horas." /></div>
            <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="en-desc">Texto de introducción</Label><Input id="en-desc" value={e.description} onChange={(ev) => setE({ ...e, description: ev.target.value })} /></div>
          </div>
          <label className="flex items-center gap-2 text-sm"><Checkbox checked={e.public} onCheckedChange={(c) => setE({ ...e, public: !!c, showOnProduct: c ? false : e.showOnProduct })} /> Abrir el formulario en mi tienda (cualquier visitante puede enviarlo)</label>
          <label className="flex items-start gap-2 text-sm"><Checkbox className="mt-0.5" checked={!!e.showOnProduct} onCheckedChange={(c) => setE({ ...e, showOnProduct: !!c, public: c ? false : e.public })} /><span>Mostrar en la ficha del producto<span className="block text-muted-foreground">Para guías de tallas, cuidados o garantías: cada registro aparece en el producto que elijas. Necesita un campo de tipo Producto; correos y teléfonos nunca se muestran.</span></span></label>
          <div className="grid gap-2"><span className="text-sm font-medium">Campos</span>
            {e.fields.map((f, i) => (
              <div key={i} className="grid gap-2 rounded-md border p-2 sm:grid-cols-[1fr_170px_auto_auto] sm:items-center">
                <Input aria-label="Etiqueta del campo" value={f.label} placeholder="Etiqueta" onChange={(ev) => setF(i, { label: ev.target.value, key: entity && entity.fields.some((x) => x.key === f.key) ? f.key : keyOf(ev.target.value) })} />
                <Select value={f.type} onValueChange={(v) => setF(i, { type: v })}><SelectTrigger aria-label="Tipo de campo"><SelectValue /></SelectTrigger><SelectContent>{FTYPES.map(([k, l]) => <SelectItem key={k} value={k}>{l}</SelectItem>)}</SelectContent></Select>
                <label className="flex items-center gap-2 text-sm"><Checkbox checked={f.required} onCheckedChange={(c) => setF(i, { required: !!c })} /> Obligatorio</label>
                <Button type="button" size="icon" variant="ghost" aria-label={`Quitar ${f.label || "campo"}`} onClick={() => setE({ ...e, fields: e.fields.filter((_, j) => j !== i) })}><Trash2 aria-hidden="true" /></Button>
                {f.type === "select" && <Input className="sm:col-span-4" aria-label="Opciones separadas por coma" placeholder="Opciones separadas por coma: Tienda, Restaurante, Otro" value={(f.options ?? []).join(", ")} onChange={(ev) => setF(i, { options: ev.target.value.split(",").map((x) => x.trimStart()) })} />}
              </div>
            ))}
            <div><Button type="button" variant="outline" onClick={() => setE({ ...e, fields: [...e.fields, { key: "", label: "", type: "text", required: false }] })}><Plus aria-hidden="true" /> Agregar campo</Button></div>
          </div>
        </form>
        <DialogFooter><Button variant="outline" onClick={onClose}>Cancelar</Button><Button type="submit" form="ent-form" disabled={save.isPending || !e.name.trim()}>{save.isPending ? "Guardando…" : "Guardar"}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Records({ id, canEdit }: { id: string; canEdit: boolean }) {
  const qc = useQueryClient();
  const [addOpen, setAddOpen] = useState(false);
  const q = useQuery({ queryKey: ["records", id], queryFn: () => api.records(id), enabled: canEdit });
  const del = useMutation({ mutationFn: (rec: string) => api.deleteRecord(id, rec), onSuccess: () => { toast.success("Registro eliminado."); void qc.invalidateQueries({ queryKey: ["records", id] }); }, onError: (e) => toast.error(errorMessage(e)) });
  if (!canEdit) return <Page title="Registros"><Notice tone="warn">Ver las respuestas requiere permiso de edición en Configuración, porque contienen datos personales.</Notice></Page>;
  if (q.error || !q.data) return <Page title="Registros"><ErrorBox error={q.error} onRetry={() => void q.refetch()} />{!q.error && <p className="text-sm text-muted-foreground">Cargando…</p>}</Page>;
  const { entity: e, items } = q.data;
  const [adding, setAdding] = [addOpen, setAddOpen];
  const exportCsv = () => void request<string>("GET", `/v1/entities/${enc(id)}/export`, undefined, { as: "text" }).then((t) => { const a = document.createElement("a"); a.href = URL.createObjectURL(new Blob([t], { type: "text/csv;charset=utf-8" })); a.download = id + ".csv"; a.click(); URL.revokeObjectURL(a.href); }).catch((er) => toast.error(errorMessage(er)));
  return (
    <Page title={e.name} sub={<><a className="hover:underline" href={href("datos")}>← Datos y formularios</a> · {items.length} registro(s)</>} actions={<div className="flex gap-2"><Button variant="outline" onClick={exportCsv}><Download aria-hidden="true" /> Exportar CSV</Button><Button onClick={() => setAdding(true)}><Plus aria-hidden="true" /> Agregar registro</Button></div>}>
      {adding && <RecordDialog entity={e} onClose={() => setAdding(false)} onDone={() => { setAdding(false); void qc.invalidateQueries({ queryKey: ["records", id] }); }} />}
      {items.length === 0 ? <EmptyState icon={<Database />} title="Sin registros todavía" text={e.public ? "Cuando alguien envíe el formulario, su respuesta aparecerá aquí." : "Agrega el primero con el botón Agregar registro."} /> : (
        <div className="overflow-x-auto rounded-lg border bg-card"><Table>
          <TableHeader><TableRow><TableHead>Fecha</TableHead>{e.fields.map((f) => <TableHead key={f.key}>{f.label}</TableHead>)}<TableHead><span className="sr-only">Acciones</span></TableHead></TableRow></TableHeader>
          <TableBody>{items.map((r) => (
            <TableRow key={r.id}><TableCell className="whitespace-nowrap text-muted-foreground">{dateTime(r.createdAt)}</TableCell>{e.fields.map((f) => <TableCell key={f.key} className="max-w-64 truncate" title={r.data[f.key]}>{f.type === "product" && r.data[f.key] ? <ProductName id={r.data[f.key]!} /> : r.data[f.key] || "—"}</TableCell>)}
              <TableCell><Button size="icon" variant="ghost" aria-label="Eliminar registro" onClick={() => window.confirm("¿Eliminar este registro?") && del.mutate(r.id)}><Trash2 aria-hidden="true" /></Button></TableCell></TableRow>
          ))}</TableBody></Table></div>
      )}
    </Page>
  );
}

/** Adds one record by hand; a product field is chosen from the catalog. */
function RecordDialog({ entity, onClose, onDone }: { entity: Entity; onClose: () => void; onDone: () => void }) {
  const [data, setData] = useState<Record<string, string>>({});
  const [names, setNames] = useState<Record<string, string>>({});
  const [pick, setPick] = useState<string | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => request<unknown>("POST", `/v1/entities/${enc(entity.id)}/records`, { data }),
    onSuccess: () => { toast.success("Registro agregado."); onDone(); },
    onError: (e) => { setErrors(fieldErrors(e)); toast.error(errorMessage(e)); },
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-lg overflow-y-auto">
        <DialogHeader><DialogTitle>Agregar registro a {entity.name}</DialogTitle><DialogDescription>Los datos se guardan cifrados.</DialogDescription></DialogHeader>
        <form id="rec-form" className="grid gap-3" onSubmit={(ev) => { ev.preventDefault(); save.mutate(); }}>
          {entity.fields.map((f) => {
            const set = (v: string) => setData((d) => ({ ...d, [f.key]: v }));
            const fid = `rec-${f.key}`;
            return (
              <div key={f.key} className="grid gap-1.5">
                <Label htmlFor={fid}>{f.label}{f.required ? " *" : ""}</Label>
                {f.type === "product" ? (
                  <div className="flex items-center gap-2"><span id={fid} className="text-sm">{names[f.key] ?? "Ningún producto elegido"}</span><Button type="button" size="sm" variant="outline" onClick={() => setPick(f.key)}>Elegir producto</Button></div>
                ) : f.type === "select" ? (
                  <select id={fid} className="h-9 rounded-md border bg-card px-2 text-sm" value={data[f.key] ?? ""} onChange={(ev) => set(ev.target.value)}><option value="">—</option>{(f.options ?? []).map((o) => <option key={o} value={o}>{o}</option>)}</select>
                ) : f.type === "checkbox" ? (
                  <input id={fid} type="checkbox" className="size-4" checked={data[f.key] === "true"} onChange={(ev) => set(ev.target.checked ? "true" : "false")} />
                ) : f.type === "textarea" ? (
                  <Textarea id={fid} rows={3} value={data[f.key] ?? ""} onChange={(ev) => set(ev.target.value)} />
                ) : (
                  <Input id={fid} type={f.type === "email" ? "email" : f.type === "phone" ? "tel" : "text"} inputMode={f.type === "number" ? "decimal" : undefined} value={data[f.key] ?? ""} onChange={(ev) => set(ev.target.value)} />
                )}
                {errors[f.key] && <p className="text-sm text-bad">{errors[f.key]}</p>}
              </div>
            );
          })}
        </form>
        <DialogFooter><Button variant="outline" onClick={onClose}>Cancelar</Button><Button type="submit" form="rec-form" disabled={save.isPending}>{save.isPending ? "Guardando…" : "Agregar"}</Button></DialogFooter>
        <ProductPicker open={pick !== null} title="Elegir producto" exclude={[]} onClose={() => setPick(null)} onPick={(ps) => { const p = ps[0]; if (p && pick) { setData((d) => ({ ...d, [pick]: p.id })); setNames((n) => ({ ...n, [pick]: p.title })); } setPick(null); }} />
      </DialogContent>
    </Dialog>
  );
}

/** Shows the product's current name instead of its id (cached per product). */
function ProductName({ id }: { id: string }) {
  const q = useQuery({ queryKey: ["product-name", id], queryFn: () => request<{ title: string }>("GET", `/v1/products/${enc(id)}`), staleTime: 60_000, retry: false });
  if (q.isError) return <span className="text-muted-foreground">Producto eliminado ({id})</span>;
  return <a className="text-primary hover:underline" href={href("catalogo", "productos", id)}>{q.data?.title ?? id}</a>;
}
