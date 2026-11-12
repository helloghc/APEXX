import type { FieldDef } from "@/components/CustomFields";
import { CustomFields } from "@/components/CustomFields";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { ArrowLeft, Mail, Pencil, Plus, Search, ShoppingCart, Trash2, UserRound, Users, X } from "lucide-react";
import { errorMessage, fieldErrors, request, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
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
import { href, navigate } from "@/router";
import { crm, type Condition, type Customer, type Group } from "@/saleApi";
import { dateTime, ErrorBox, Notice, Page, StatusPill } from "../common";
import { ChipInput, EmptyState, SkeletonRows, SubNav, useDebounced } from "../promotions/shared";

const ALL = "__all";
const TABS = [
  { key: "", label: "Clientes", route: ["clientes"] },
  { key: "segmentos", label: "Segmentos", route: ["clientes", "segmentos"] },
  { key: "carritos", label: "Carritos abandonados", route: ["clientes", "carritos"] },
];

/** Routes: #clientes, #clientes/{id}, #clientes/segmentos, #clientes/carritos. */
export function Customers({ me, route }: { me: Me; route: string[] }) {
  const section = route[1] ?? "";
  if (section && section !== "segmentos" && section !== "carritos") return <CustomerDetail key={section} me={me} id={section} />;
  return (
    <Page title="Clientes" sub="Los datos personales se guardan cifrados con la llave de tu tienda. Solo quien tiene permiso de edición los ve completos.">
      <SubNav items={TABS} active={section} label="Secciones de clientes" />
      {section === "segmentos" ? <Segments me={me} /> : section === "carritos" ? <Carts me={me} /> : <CustomerList me={me} />}
    </Page>
  );
}

function CustomerList({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "customers", "edit");
  const [q, setQ] = useState("");
  const dq = useDebounced(q.trim());
  const [tag, setTag] = useState("");
  const [consent, setConsent] = useState("");
  const [group, setGroup] = useState("");
  const [sort, setSort] = useState("created_desc");
  const [adding, setAdding] = useState(false);
  const tags = useQuery({ queryKey: ["customer-tags"], queryFn: crm.tags });
  const groups = useQuery({ queryKey: ["customer-groups"], queryFn: crm.groups });
  const list = useInfiniteQuery({
    queryKey: ["customers", dq, tag, consent, group, sort],
    queryFn: ({ pageParam }) => crm.list({ q: dq, tag, consent, group, sort, cursor: pageParam, limit: 50 }),
    initialPageParam: "",
    getNextPageParam: (l) => l.nextCursor || undefined,
  });
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const total = list.data?.pages[0]?.total ?? 0;
  const filtered = !!(dq || tag || consent || group);
  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-56 flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden="true" />
          <Input aria-label="Buscar clientes" placeholder="Buscar por correo exacto, teléfono o nombre" className="pl-8" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <Select value={group || ALL} onValueChange={(v) => setGroup(v === ALL ? "" : v)}>
          <SelectTrigger className="w-48" aria-label="Segmento"><SelectValue /></SelectTrigger>
          <SelectContent><SelectItem value={ALL}>Todos los segmentos</SelectItem>{(groups.data ?? []).map((g) => <SelectItem key={g.id} value={g.id}>{g.name}</SelectItem>)}</SelectContent>
        </Select>
        <Select value={tag || ALL} onValueChange={(v) => setTag(v === ALL ? "" : v)}>
          <SelectTrigger className="w-40" aria-label="Etiqueta"><SelectValue /></SelectTrigger>
          <SelectContent><SelectItem value={ALL}>Todas las etiquetas</SelectItem>{(tags.data ?? []).map((t) => <SelectItem key={t} value={t}>{t}</SelectItem>)}</SelectContent>
        </Select>
        <Select value={consent || ALL} onValueChange={(v) => setConsent(v === ALL ? "" : v)}>
          <SelectTrigger className="w-48" aria-label="Correos de marketing"><SelectValue /></SelectTrigger>
          <SelectContent><SelectItem value={ALL}>Marketing: todos</SelectItem><SelectItem value="yes">Aceptan marketing</SelectItem><SelectItem value="no">No aceptan</SelectItem></SelectContent>
        </Select>
        <Select value={sort} onValueChange={setSort}>
          <SelectTrigger className="w-44" aria-label="Orden"><SelectValue /></SelectTrigger>
          <SelectContent><SelectItem value="created_desc">Más recientes</SelectItem><SelectItem value="spent_desc">Mayor gasto</SelectItem><SelectItem value="orders_desc">Más pedidos</SelectItem><SelectItem value="last_order_desc">Última compra</SelectItem></SelectContent>
        </Select>
        {canEdit && <Button onClick={() => setAdding(true)}><Plus aria-hidden="true" /> Nuevo cliente</Button>}
      </div>
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      {!list.isLoading && items.length === 0 ? (
        <EmptyState icon={<Users />} title={filtered ? "Ningún cliente coincide" : "Aún no hay clientes"} text={filtered ? "La búsqueda por correo o teléfono necesita el dato completo, porque se guardan cifrados." : "Cada persona que compra queda registrada aquí automáticamente."} />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader><TableRow><TableHead>Cliente</TableHead><TableHead>Etiquetas</TableHead><TableHead>Marketing</TableHead><TableHead className="text-right">Pedidos</TableHead><TableHead className="text-right">Total gastado</TableHead><TableHead>Última compra</TableHead></TableRow></TableHeader>
            <TableBody>
              {list.isLoading ? <SkeletonRows cols={6} /> : items.map((c) => (
                <TableRow key={c.id} className="cursor-pointer" onClick={() => navigate("clientes", c.id)}>
                  <TableCell><a className="font-medium text-primary hover:underline" href={href("clientes", c.id)} onClick={(e) => e.stopPropagation()}>{c.name || "Sin nombre"}</a><div className="text-xs text-muted-foreground">{c.email}</div></TableCell>
                  <TableCell><div className="flex flex-wrap gap-1">{c.tags.map((t) => <Badge key={t} variant="secondary">{t}</Badge>)}</div></TableCell>
                  <TableCell>{c.marketingConsent ? <Badge variant="ok">Acepta</Badge> : <span className="text-muted-foreground">No</span>}</TableCell>
                  <TableCell className="text-right tabular-nums">{c.ordersCount}</TableCell>
                  <TableCell className="text-right tabular-nums">{formatMoney(c.totalSpent, c.currency || "MXN")}</TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{dateTime(c.lastOrderAt)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <div className="flex items-center justify-between text-sm text-muted-foreground">
        <span>{total} clientes</span>
        {list.hasNextPage && <Button variant="outline" size="sm" onClick={() => void list.fetchNextPage()}>Ver más</Button>}
      </div>
      {adding && <NewCustomer onClose={() => setAdding(false)} onDone={(c) => { void qc.invalidateQueries({ queryKey: ["customers"] }); navigate("clientes", c.id); }} />}
    </div>
  );
}

function NewCustomer({ onClose, onDone }: { onClose: () => void; onDone: (c: Customer) => void }) {
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [consent, setConsent] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErrors({});
    try {
      onDone(await crm.create({ email: email.trim(), name: name.trim(), phone: phone.trim() || undefined, marketingConsent: consent }));
      toast.success("Cliente creado.");
    } catch (err) {
      const fe = fieldErrors(err);
      setErrors(Object.keys(fe).length ? fe : { form: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form onSubmit={(e) => void submit(e)} className="grid gap-4">
          <DialogHeader><DialogTitle>Nuevo cliente</DialogTitle><DialogDescription>Para registrar a alguien que compra por teléfono o en mostrador.</DialogDescription></DialogHeader>
          <div className="grid gap-1.5"><Label htmlFor="nc-email">Correo</Label><Input id="nc-email" type="email" required autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />{errors.email && <p className="text-xs text-destructive">{errors.email}</p>}</div>
          <div className="grid gap-1.5"><Label htmlFor="nc-name">Nombre</Label><Input id="nc-name" required value={name} onChange={(e) => setName(e.target.value)} /></div>
          <div className="grid gap-1.5"><Label htmlFor="nc-phone">Teléfono</Label><Input id="nc-phone" type="tel" value={phone} onChange={(e) => setPhone(e.target.value)} /></div>
          <label className="flex items-center gap-2 text-sm"><Checkbox checked={consent} onCheckedChange={(c) => setConsent(!!c)} /> Aceptó recibir correos de marketing</label>
          {errors.form && <p className="text-sm text-destructive" role="alert">{errors.form}</p>}
          <DialogFooter><Button type="button" variant="outline" onClick={onClose}>Cancelar</Button><Button type="submit" disabled={busy}>{busy ? "Guardando…" : "Crear cliente"}</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function CustomerDetail({ me, id }: { me: Me; id: string }) {
  const qc = useQueryClient();
  const canEdit = can(me, "customers", "edit");
  const q = useQuery({ queryKey: ["customer", id], queryFn: () => crm.get(id) });
  const orders = useQuery({ queryKey: ["customer-orders", id], queryFn: () => crm.orders(id) });
  const [tags, setTags] = useState<string[]>([]);
  const [note, setNote] = useState("");
  const c = q.data;
  useEffect(() => { if (c) setTags(c.tags); }, [c]);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["customer", id] });
  const save = (patch: Parameters<typeof crm.update>[1]) => crm.update(id, patch).then(() => { toast.success("Guardado."); refresh(); void qc.invalidateQueries({ queryKey: ["customers"] }); }).catch((e) => toast.error(errorMessage(e)));
  if (q.isLoading) return <main className="mx-auto w-full max-w-6xl px-4 py-6 text-sm text-muted-foreground md:px-8">Cargando cliente…</main>;
  if (q.error || !c) return <main className="mx-auto w-full max-w-3xl px-4 py-6"><ErrorBox error={q.error} onRetry={() => void q.refetch()} /></main>;
  const cur = c.currency || "MXN";
  const avg = c.ordersCount ? Math.round(c.totalSpent / c.ordersCount) : 0;
  return (
    <main className="mx-auto grid w-full max-w-6xl content-start gap-5 px-4 py-6 md:px-8">
      <div className="grid gap-1">
        <a href={href("clientes")} className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"><ArrowLeft className="size-4" aria-hidden="true" /> Clientes</a>
        <div className="flex flex-wrap items-center gap-2">
          <h1 className="text-2xl font-bold tracking-tight">{c.name || "Sin nombre"}</h1>
          {c.hasAccount && <Badge variant="secondary">Tiene cuenta</Badge>}
          {c.status !== "active" && <StatusPill status={c.status} />}
        </div>
        <p className="text-sm text-muted-foreground">Cliente desde {dateTime(c.createdAt)}</p>
      </div>
      {c.masked && <Notice tone="warn">Tu acceso es de lectura: el correo y el teléfono se muestran parcialmente.</Notice>}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        {[["Pedidos", String(c.ordersCount)], ["Total gastado", formatMoney(c.totalSpent, cur)], ["Ticket promedio", formatMoney(avg, cur)], ["Última compra", c.lastOrderAt ? dateTime(c.lastOrderAt) : "—"]].map(([l, v]) => (
          <div key={l} className="rounded-lg border bg-card p-4"><div className="text-xs text-muted-foreground">{l}</div><div className="pt-1 text-xl font-bold tabular-nums">{v}</div></div>
        ))}
      </div>
      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_340px]">
        <div className="grid content-start gap-5">
          <Card>
            <CardHeader><CardTitle className="text-base">Pedidos</CardTitle></CardHeader>
            <CardContent>
              {(orders.data?.items ?? []).length === 0 ? <p className="text-sm text-muted-foreground">Todavía no ha comprado.</p> : (
                <ul className="divide-y text-sm">
                  {orders.data!.items.map((o) => (
                    <li key={o.id} className="flex flex-wrap items-center justify-between gap-2 py-2">
                      <a className="font-medium text-primary hover:underline" href={href("pedidos", o.id)}>#{o.number}</a>
                      <span className="text-muted-foreground">{dateTime(o.createdAt)}</span>
                      <StatusPill status={o.paymentStatus || o.status} />
                      <span className="tabular-nums">{formatMoney(o.grandTotal, o.currency)}</span>
                    </li>
                  ))}
                </ul>
              )}
            </CardContent>
          </Card>
          <LoyaltyCard id={id} canEdit={canEdit} />
          <CustomFields resource="customer" id={id} canEdit={canEdit} />
          {can(me, "customers", "admin") && <PrivacyCard id={id} name={c.name || c.email} />}
          <Card>
            <CardHeader><CardTitle className="text-base">Notas internas</CardTitle></CardHeader>
            <CardContent className="grid gap-3">
              {canEdit && (
                <form className="flex gap-2" onSubmit={(e) => { e.preventDefault(); if (!note.trim()) return; crm.addNote(id, note.trim()).then(() => { setNote(""); refresh(); }).catch((er) => toast.error(errorMessage(er))); }}>
                  <Input aria-label="Nueva nota" placeholder="Prefiere entregas por la tarde…" value={note} onChange={(e) => setNote(e.target.value)} />
                  <Button type="submit" variant="outline" disabled={!note.trim()}>Agregar</Button>
                </form>
              )}
              {(c.notes ?? []).length === 0 ? <p className="text-sm text-muted-foreground">Sin notas.</p> : c.notes!.map((n) => (
                <div key={n.id} className="rounded-md bg-muted/50 px-3 py-2 text-sm"><p className="whitespace-pre-wrap">{n.body}</p><p className="text-xs text-muted-foreground">{n.authorName} · {dateTime(n.createdAt)}</p></div>
              ))}
            </CardContent>
          </Card>
        </div>
        <div className="grid content-start gap-5">
          <Card>
            <CardHeader><CardTitle className="text-base">Contacto</CardTitle></CardHeader>
            <CardContent className="grid gap-2 text-sm">
              <span className="flex items-center gap-2"><Mail className="size-4 text-muted-foreground" aria-hidden="true" /> {c.email}</span>
              {c.phone && <span>{c.phone}</span>}
              <label className="flex items-center gap-2 pt-1"><Checkbox disabled={!canEdit} checked={c.marketingConsent} onCheckedChange={(v) => void save({ marketingConsent: !!v, version: c.version })} /> Acepta correos de marketing</label>
            </CardContent>
          </Card>
          <Card>
            <CardHeader><CardTitle className="text-base">Direcciones</CardTitle></CardHeader>
            <CardContent className="grid gap-2 text-sm">
              {(c.addresses ?? []).length === 0 ? <span className="text-muted-foreground">Sin direcciones guardadas.</span> : c.addresses!.map((a) => (
                <div key={a.id} className="rounded-md border px-3 py-2">{[a.line1, a.line2, a.city, a.state, a.postalCode].filter(Boolean).join(", ") || String(a.address ?? "")}{a.isDefault && <Badge variant="secondary" className="ml-2">Principal</Badge>}</div>
              ))}
            </CardContent>
          </Card>
          <Card>
            <CardHeader><CardTitle className="text-base">Etiquetas y segmentos</CardTitle></CardHeader>
            <CardContent className="grid gap-3 text-sm">
              {canEdit ? (
                <>
                  <ChipInput values={tags} onChange={setTags} placeholder="mayoreo, vip…" />
                  {JSON.stringify(tags) !== JSON.stringify(c.tags) && <Button size="sm" onClick={() => void save({ tags, version: c.version })}>Guardar etiquetas</Button>}
                </>
              ) : <div className="flex flex-wrap gap-1">{c.tags.map((t) => <Badge key={t} variant="secondary">{t}</Badge>)}</div>}
              {(c.groups ?? []).length > 0 && <p className="text-muted-foreground">Segmentos: {c.groups!.map((g) => g.name).join(", ")}</p>}
            </CardContent>
          </Card>
        </div>
      </div>
    </main>
  );
}

// ---------- segments ----------

const FIELDS: Record<string, { label: string; kind: "money" | "int" | "bool" | "text" }> = {
  total_spent: { label: "Total gastado", kind: "money" }, orders_count: { label: "Número de pedidos", kind: "int" }, days_since_last_order: { label: "Días desde la última compra", kind: "int" },
  marketing_consent: { label: "Acepta marketing", kind: "bool" }, has_account: { label: "Tiene cuenta", kind: "bool" }, tag: { label: "Etiqueta", kind: "text" },
};
const OPS: [string, string][] = [["gte", "al menos"], ["lte", "como máximo"], ["eq", "igual a"]];

function Segments({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "customers", "edit");
  const list = useQuery({ queryKey: ["customer-groups"], queryFn: crm.groups });
  const [edit, setEdit] = useState<Partial<Group> | null>(null);
  const items = list.data ?? [];
  const refresh = () => void qc.invalidateQueries({ queryKey: ["customer-groups"] });
  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="max-w-2xl text-sm text-muted-foreground">Un segmento agrupa clientes por reglas que se actualizan solas, por ejemplo “gastaron más de $5,000” o “no compran hace 90 días”.</p>
        {canEdit && <Button onClick={() => setEdit({ kind: "rules", name: "", description: "", rules: { match: "all", conditions: [{ field: "total_spent", op: "gte", value: 500000 }] } })}><Plus aria-hidden="true" /> Nuevo segmento</Button>}
      </div>
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      {!list.isLoading && items.length === 0 ? (
        <EmptyState icon={<UserRound />} title="Aún no hay segmentos" text="Crea segmentos para filtrar clientes y, más adelante, dirigir promociones y correos." />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader><TableRow><TableHead>Segmento</TableHead><TableHead>Tipo</TableHead><TableHead className="text-right">Clientes</TableHead><TableHead className="w-24"><span className="sr-only">Acciones</span></TableHead></TableRow></TableHeader>
            <TableBody>
              {list.isLoading ? <SkeletonRows cols={4} /> : items.map((g) => (
                <TableRow key={g.id}>
                  <TableCell><a className="font-medium text-primary hover:underline" href={href("clientes")} onClick={() => undefined}>{g.name}</a><div className="text-xs text-muted-foreground">{g.description}</div></TableCell>
                  <TableCell>{g.kind === "rules" ? "Por reglas" : "Manual"}</TableCell>
                  <TableCell className="text-right tabular-nums">{g.count}</TableCell>
                  <TableCell>
                    {canEdit && (
                      <div className="flex justify-end">
                        <Button variant="ghost" size="icon" aria-label={`Editar ${g.name}`} onClick={() => setEdit(g)}><Pencil aria-hidden="true" /></Button>
                        <Button variant="ghost" size="icon" aria-label={`Eliminar ${g.name}`} onClick={() => void crm.deleteGroup(g.id).then(() => { toast.success("Segmento eliminado."); refresh(); }).catch((e) => toast.error(errorMessage(e)))}><Trash2 aria-hidden="true" /></Button>
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {edit && <SegmentDialog g={edit} onClose={() => setEdit(null)} onDone={refresh} />}
    </div>
  );
}

function SegmentDialog({ g, onClose, onDone }: { g: Partial<Group>; onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState(g.name ?? "");
  const [description, setDescription] = useState(g.description ?? "");
  const [match, setMatch] = useState<"all" | "any">(g.rules?.match ?? "all");
  const [conds, setConds] = useState<Condition[]>(g.rules?.conditions ?? []);
  const [count, setCount] = useState<number | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const rules = { match, conditions: conds };
  const key = useDebounced(JSON.stringify(rules), 400);
  useEffect(() => {
    let live = true;
    crm.previewGroup(JSON.parse(key) as Group["rules"]).then((r) => live && setCount(r.count)).catch(() => live && setCount(null));
    return () => { live = false; };
  }, [key]);
  const setCond = (i: number, patch: Partial<Condition>) => setConds(conds.map((c, j) => (j === i ? { ...c, ...patch } : c)));
  // The store's own customer fields that are not personal can be used too.
  const cf = useQuery({ queryKey: ["fields", "customer"], queryFn: () => request<{ fields: FieldDef[] }>("GET", "/v1/fields/customer") });
  const ALL: Record<string, { label: string; kind: "money" | "int" | "bool" | "text"; options?: string[] }> = { ...FIELDS };
  for (const f of cf.data?.fields ?? []) {
    if (!f.personal) ALL["cf:" + f.key] = { label: f.label + " (campo propio)", kind: f.type === "number" ? "int" : f.type === "boolean" ? "bool" : "text", options: f.type === "select" ? f.options : undefined };
  }
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await crm.saveGroup({ id: g.id, name: name.trim(), description, kind: "rules", rules });
      toast.success("Segmento guardado.");
      onDone();
      onClose();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-2xl overflow-y-auto">
        <form onSubmit={(e) => void submit(e)} className="grid gap-4">
          <DialogHeader><DialogTitle>{g.id ? "Editar segmento" : "Nuevo segmento"}</DialogTitle><DialogDescription>Los clientes entran y salen solos según las reglas.</DialogDescription></DialogHeader>
          <div className="grid gap-1.5"><Label htmlFor="sg-name">Nombre</Label><Input id="sg-name" required autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Clientes frecuentes" /></div>
          <div className="grid gap-1.5"><Label htmlFor="sg-desc">Descripción</Label><Textarea id="sg-desc" rows={2} value={description} onChange={(e) => setDescription(e.target.value)} /></div>
          <div className="grid gap-2">
            <p className="text-sm">Incluir clientes que cumplan{" "}
              <select aria-label="Cómo combinar" className="rounded border bg-background px-1 py-0.5" value={match} onChange={(e) => setMatch(e.target.value as "all" | "any")}><option value="all">todas</option><option value="any">cualquiera de</option></select> estas reglas:
            </p>
            {conds.map((c, i) => {
              const kind = ALL[c.field]?.kind ?? "int";
              const options = ALL[c.field]?.options;
              return (
                <div key={i} className="grid gap-2 rounded-md border p-2 sm:grid-cols-[1fr_140px_140px_auto]">
                  <select aria-label="Campo" className="h-9 rounded-md border bg-background px-2 text-sm" value={c.field}
                    onChange={(e) => { const k = ALL[e.target.value]!.kind; setCond(i, { field: e.target.value, op: k === "bool" || k === "text" ? "eq" : "gte", value: k === "bool" ? true : k === "text" ? (ALL[e.target.value]!.options?.[0] ?? "") : 0 }); }}>
                    {Object.entries(ALL).map(([k, v]) => <option key={k} value={k}>{v.label}</option>)}
                  </select>
                  {kind === "bool" || kind === "text" ? <span className="self-center text-sm text-muted-foreground">es</span> : (
                    <select aria-label="Comparación" className="h-9 rounded-md border bg-background px-2 text-sm" value={c.op} onChange={(e) => setCond(i, { op: e.target.value })}>{OPS.map(([k, v]) => <option key={k} value={k}>{v}</option>)}</select>
                  )}
                  {kind === "bool" ? (
                    <select aria-label="Valor" className="h-9 rounded-md border bg-background px-2 text-sm" value={String(c.value)} onChange={(e) => setCond(i, { value: e.target.value === "true" })}><option value="true">Sí</option><option value="false">No</option></select>
                  ) : options ? (
                    <select aria-label="Valor" className="h-9 rounded-md border bg-background px-2 text-sm" value={String(c.value)} onChange={(e) => setCond(i, { value: e.target.value })}>{options.map((o) => <option key={o} value={o}>{o}</option>)}</select>
                  ) : kind === "money" ? (
                    <Input aria-label="Monto" inputMode="decimal" defaultValue={centsToPesos(Number(c.value) || 0)} onBlur={(e) => setCond(i, { value: parsePesos(e.target.value) ?? 0 })} />
                  ) : (
                    <Input aria-label="Valor" inputMode={kind === "int" ? "numeric" : undefined} value={String(c.value)} onChange={(e) => setCond(i, { value: kind === "int" ? Number(e.target.value.replace(/\D/g, "")) || 0 : e.target.value })} />
                  )}
                  <Button type="button" variant="ghost" size="icon" aria-label="Quitar regla" onClick={() => setConds(conds.filter((_, j) => j !== i))}><X aria-hidden="true" /></Button>
                </div>
              );
            })}
            <div><Button type="button" variant="outline" size="sm" onClick={() => setConds([...conds, { field: "orders_count", op: "gte", value: 1 }])}><Plus aria-hidden="true" /> Agregar regla</Button></div>
          </div>
          <Notice tone="ok">{count == null ? "Calculando…" : `${count} clientes cumplen hoy estas reglas.`}</Notice>
          <ErrorBox error={error} />
          <DialogFooter><Button type="button" variant="outline" onClick={onClose}>Cancelar</Button><Button type="submit" disabled={busy || !conds.length}>{busy ? "Guardando…" : "Guardar segmento"}</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// ---------- abandoned carts ----------

function Carts({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "customers", "edit");
  const [status, setStatus] = useState("");
  const list = useQuery({ queryKey: ["abandoned-carts", status], queryFn: () => crm.carts(status) });
  const items = list.data?.items ?? [];
  const refresh = () => void qc.invalidateQueries({ queryKey: ["abandoned-carts"] });
  const act = (p: Promise<unknown>, ok: string) => p.then(() => { toast.success(ok); refresh(); }).catch((e) => toast.error(errorMessage(e)));
  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="max-w-2xl text-sm text-muted-foreground">Carritos de personas que escribieron su correo en el pago y no terminaron. Puedes enviarles un recordatorio con un enlace para retomar la compra.</p>
        <Select value={status || ALL} onValueChange={(v) => setStatus(v === ALL ? "" : v)}>
          <SelectTrigger className="w-44" aria-label="Estado"><SelectValue /></SelectTrigger>
          <SelectContent><SelectItem value={ALL}>Todos</SelectItem><SelectItem value="open">Abiertos</SelectItem><SelectItem value="reminded">Recordados</SelectItem><SelectItem value="recovered">Recuperados</SelectItem><SelectItem value="dismissed">Descartados</SelectItem></SelectContent>
        </Select>
      </div>
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      {!list.isLoading && items.length === 0 ? (
        <EmptyState icon={<ShoppingCart />} title="Sin carritos abandonados" text="Buena señal: nadie dejó una compra a medias." />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <Table>
            <TableHeader><TableRow><TableHead>Cliente</TableHead><TableHead>Productos</TableHead><TableHead className="text-right">Valor</TableHead><TableHead>Estado</TableHead><TableHead>Última actividad</TableHead><TableHead><span className="sr-only">Acciones</span></TableHead></TableRow></TableHeader>
            <TableBody>
              {list.isLoading ? <SkeletonRows cols={6} /> : items.map((c) => (
                <TableRow key={c.id}>
                  <TableCell><div className="font-medium">{c.name || c.email}</div><div className="text-xs text-muted-foreground">{c.email}</div></TableCell>
                  <TableCell className="max-w-72 text-sm">{c.lines.map((l) => `${l.qty} × ${l.title}`).join(", ")}</TableCell>
                  <TableCell className="text-right tabular-nums">{formatMoney(c.total, c.currency)}</TableCell>
                  <TableCell><StatusPill status={c.status} /></TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{dateTime(c.updatedAt)}</TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-2">
                      {c.recoveredOrderId && <Button size="sm" variant="outline" asChild><a href={href("pedidos", c.recoveredOrderId)}>Ver pedido</a></Button>}
                      {canEdit && (c.status === "open" || c.status === "reminded") && <Button size="sm" onClick={() => void act(crm.remind(c.id), "Recordatorio enviado.")}><Mail aria-hidden="true" /> {c.remindedAt ? "Recordar otra vez" : "Enviar recordatorio"}</Button>}
                      {canEdit && c.status !== "recovered" && c.status !== "dismissed" && <Button size="sm" variant="ghost" onClick={() => void act(crm.dismiss(c.id), "Carrito descartado.")}>Descartar</Button>}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}

/** Privacy requests: hand the person a copy of their data, or erase it. */
function PrivacyCard({ id, name }: { id: string; name: string }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  async function download() {
    try {
      const text = await request<string>("GET", `/v1/customers/${encodeURIComponent(id)}/export`, undefined, { as: "text" });
      const a = document.createElement("a");
      a.href = URL.createObjectURL(new Blob([text], { type: "application/json" }));
      a.download = `datos-${id}.json`;
      a.click();
      URL.revokeObjectURL(a.href);
      toast.success("Descargamos sus datos. Entrégaselos por un canal seguro.");
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }
  async function erase() {
    setBusy(true);
    try {
      await request<void>("DELETE", `/v1/customers/${encodeURIComponent(id)}`, undefined, { as: "none" });
      toast.success("Borramos a la persona. Sus pedidos se conservan sin datos personales.");
      void qc.invalidateQueries({ queryKey: ["customers"] });
      navigate("clientes");
    } catch (e) {
      toast.error(errorMessage(e));
      setBusy(false);
    }
  }
  return (
    <Card>
      <CardHeader><CardTitle className="text-base">Privacidad</CardTitle></CardHeader>
      <CardContent className="grid gap-3">
        <p className="text-sm text-muted-foreground">Si la persona lo pide, entrégale una copia de sus datos o bórralos. Al borrar, sus pedidos se conservan con sus importes pero sin nombre, correo ni dirección. El comprador también puede hacerlo desde su cuenta en la tienda.</p>
        <div className="flex flex-wrap gap-2"><Button variant="outline" onClick={() => void download()}>Descargar sus datos</Button><Button variant="outline" className="text-bad" onClick={() => setOpen(true)}><Trash2 aria-hidden="true" /> Borrar sus datos</Button></div>
      </CardContent>
      <Dialog open={open} onOpenChange={(o) => { setOpen(o); if (!o) setTyped(""); }}>
        <DialogContent>
          <DialogHeader><DialogTitle>Borrar los datos de {name}</DialogTitle><DialogDescription>No se puede deshacer. Se borran su perfil, direcciones, notas, favoritos y campos propios; sus pedidos quedan sin datos personales.</DialogDescription></DialogHeader>
          <div className="grid gap-1.5"><Label htmlFor="erase-confirm">Escribe BORRAR para confirmar</Label><Input id="erase-confirm" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} /></div>
          <DialogFooter><Button variant="outline" onClick={() => setOpen(false)}>Cancelar</Button><Button variant="destructive" disabled={typed.trim().toUpperCase() !== "BORRAR" || busy} onClick={() => void erase()}>{busy ? "Borrando…" : "Borrar para siempre"}</Button></DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}

interface LoyaltyAccount { points: number; value: number; tier?: string; nextTier?: string; toNext?: number; entries: { id: string; orderId?: string; kind: string; points: number; note?: string; createdAt: string }[] }

/** LoyaltyCard: the customer's points, tier, history and a manual adjustment. */
function LoyaltyCard({ id, canEdit }: { id: string; canEdit: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["loyalty", id], queryFn: () => request<LoyaltyAccount>("GET", `/v1/customers/${encodeURIComponent(id)}/loyalty`) });
  const [pts, setPts] = useState("");
  const [note, setNote] = useState("");
  if (!q.data) return null;
  const a = q.data;
  async function adjust() {
    try {
      await request("POST", `/v1/customers/${encodeURIComponent(id)}/loyalty/adjust`, { points: Number(pts), note });
      toast.success("Puntos ajustados."); setPts(""); setNote("");
      void qc.invalidateQueries({ queryKey: ["loyalty", id] });
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }
  return (
    <Card data-loyalty-card>
      <CardHeader><CardTitle className="text-base">Puntos de lealtad</CardTitle></CardHeader>
      <CardContent className="grid gap-3 text-sm">
        <p><strong className="text-2xl tabular-nums">{a.points}</strong> puntos · valen {formatMoney(a.value, "MXN")}{a.tier ? ` · nivel ${a.tier}` : ""}{a.nextTier ? ` · le faltan ${formatMoney(a.toNext ?? 0, "MXN")} para ${a.nextTier}` : ""}</p>
        {a.entries.length > 0 && <ul className="divide-y">{a.entries.slice(0, 8).map((e) => <li key={e.id} className="flex justify-between gap-2 py-1.5"><span>{e.note || e.kind}{e.orderId ? <> · <a className="text-primary hover:underline" href={href("pedidos", e.orderId)}>pedido</a></> : null}</span><span className={`tabular-nums ${e.points < 0 ? "text-bad" : ""}`}>{e.points > 0 ? "+" : ""}{e.points}</span></li>)}</ul>}
        {canEdit && (
          <div className="grid gap-2 sm:grid-cols-[7rem_1fr_auto] sm:items-end">
            <div className="grid gap-1"><Label htmlFor="ly-pts">Puntos (+/−)</Label><Input id="ly-pts" inputMode="numeric" value={pts} onChange={(e) => setPts(e.target.value.replace(/[^\d-]/g, ""))} /></div>
            <div className="grid gap-1"><Label htmlFor="ly-note">Motivo</Label><Input id="ly-note" value={note} onChange={(e) => setNote(e.target.value)} placeholder="Compensación por retraso" /></div>
            <Button size="sm" disabled={!pts || !note.trim()} onClick={() => void adjust()}>Ajustar</Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
