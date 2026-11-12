import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ArrowLeft, Megaphone, Pencil, Plus, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, type Me } from "@/api";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { toast } from "@/components/ui/sonner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { formatMoney } from "@/money";
import { ErrorBox, Notice } from "@/pages/common";
import { can } from "@/perms";
import { promoApi, type Campaign, type CampaignView } from "@/promoApi";
import { href, navigate } from "@/router";
import { STORE_TZ, TYPE_LABEL, windowText } from "./lib";
import { Choice, DateTimeField, EmptyState, Field, NumField, Skeleton, SkeletonRows, StatusBadge } from "./shared";

/** Budget shows how much of a campaign budget is used. */
function Budget({ c, large }: { c: Campaign; large?: boolean }) {
  if (c.budgetType === "none") return <span className="text-muted-foreground">Sin presupuesto</span>;
  const used = c.budgetType === "amount" ? c.spent : c.used;
  const pct = c.budgetLimit > 0 ? Math.min(100, Math.round((used / c.budgetLimit) * 100)) : 0;
  const text =
    c.budgetType === "amount" ? `${formatMoney(c.spent, c.currency)} de ${formatMoney(c.budgetLimit, c.currency)}` : `${c.used} de ${c.budgetLimit} usos`;
  return (
    <div className={large ? "grid gap-2" : "grid min-w-40 gap-1"}>
      <div className={large ? "text-lg font-semibold tabular-nums" : "whitespace-nowrap tabular-nums"}>{text}</div>
      <div className="h-1.5 overflow-hidden rounded-full bg-muted" role="progressbar" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100} aria-label="Presupuesto usado">
        <div className={pct >= 100 ? "h-full bg-bad" : pct >= 80 ? "h-full bg-warn" : "h-full bg-primary"} style={{ width: `${pct}%` }} />
      </div>
      {large && <span className="text-xs text-muted-foreground">{pct}% usado. Al agotarse, sus promociones dejan de aplicar y lo explican.</span>}
    </div>
  );
}

export function Campaigns({ me, id }: { me: Me; id?: string }) {
  return id ? <CampaignDetail key={id} me={me} id={id} /> : <CampaignList me={me} />;
}

function CampaignList({ me }: { me: Me }) {
  const canEdit = can(me, "promotions", "edit");
  const q = useQuery({ queryKey: ["campaigns"], queryFn: promoApi.campaigns });
  const [form, setForm] = useState<CampaignView | "new" | null>(null);
  const items = q.data ?? [];
  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="max-w-2xl text-sm text-muted-foreground">
          Una campaña agrupa promociones bajo las mismas fechas y un presupuesto compartido, por ejemplo Buen Fin o Hot Sale.
        </p>
        {canEdit && items.length > 0 && (
          <Button onClick={() => setForm("new")}>
            <Plus /> Nueva campaña
          </Button>
        )}
      </div>
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {!q.isPending && items.length === 0 && !q.error ? (
        <EmptyState
          icon={<Megaphone />}
          title="Todavía no hay campañas"
          text="Crea una para controlar cuánto descuento se regala en total y pausar todas sus promociones de un solo movimiento."
          action={
            canEdit && (
              <Button onClick={() => setForm("new")}>
                <Plus /> Nueva campaña
              </Button>
            )
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Campaña</TableHead>
              <TableHead>Estado</TableHead>
              <TableHead className="hidden md:table-cell">Vigencia</TableHead>
              <TableHead className="text-right">Promociones</TableHead>
              <TableHead>Presupuesto</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {q.isPending && <SkeletonRows cols={5} rows={3} />}
            {items.map((c) => (
              <TableRow key={c.id}>
                <TableCell>
                  <a className="font-medium text-primary hover:underline" href={href("promociones", "campanas", c.id)}>
                    {c.name}
                  </a>
                  {c.description && <div className="max-w-sm truncate text-xs text-muted-foreground">{c.description}</div>}
                </TableCell>
                <TableCell>
                  <StatusBadge status={c.displayStatus} />
                </TableCell>
                <TableCell className="hidden whitespace-nowrap text-muted-foreground md:table-cell">{windowText(c.startsAt, c.endsAt)}</TableCell>
                <TableCell className="text-right tabular-nums">{c.promotions}</TableCell>
                <TableCell>
                  <Budget c={c} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <CampaignForm
        open={form !== null}
        campaign={form === "new" ? null : form}
        onClose={() => setForm(null)}
        onSaved={(c, created) => {
          setForm(null);
          if (created) navigate("promociones", "campanas", c.id);
        }}
      />
    </div>
  );
}

function CampaignForm({ open, campaign, onClose, onSaved }: { open: boolean; campaign: CampaignView | null; onClose: () => void; onSaved: (c: CampaignView, created: boolean) => void }) {
  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-md">
        <SheetHeader>
          <SheetTitle>{campaign ? "Editar campaña" : "Nueva campaña"}</SheetTitle>
          <SheetDescription>Las fechas usan la hora del centro de México ({STORE_TZ}).</SheetDescription>
        </SheetHeader>
        {open && <CampaignFields key={campaign?.id ?? "new"} campaign={campaign} onClose={onClose} onSaved={onSaved} />}
      </SheetContent>
    </Sheet>
  );
}

function CampaignFields({ campaign, onClose, onSaved }: { campaign: CampaignView | null; onClose: () => void; onSaved: (c: CampaignView, created: boolean) => void }) {
  const qc = useQueryClient();
  const [c, setC] = useState<Campaign>(
    () => campaign ?? { id: "", name: "", description: "", status: "active", budgetType: "none", budgetLimit: 0, spent: 0, used: 0, currency: "MXN" },
  );
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const set = (patch: Partial<Campaign>) => setC((o) => ({ ...o, ...patch }));

  async function submit() {
    const e: Record<string, string> = {};
    if (!c.name.trim()) e.name = "Escribe el nombre de la campaña.";
    if (c.budgetType !== "none" && c.budgetLimit < 1) e.budgetLimit = "Escribe un presupuesto mayor a cero.";
    if (c.startsAt && c.endsAt && Date.parse(c.endsAt) <= Date.parse(c.startsAt)) e.endsAt = "La fecha de fin debe ser posterior a la de inicio.";
    setErrs(e);
    setErr(null);
    if (Object.keys(e).length) return;
    setBusy(true);
    const body = {
      name: c.name.trim(),
      description: c.description?.trim() || undefined,
      status: c.status,
      startsAt: c.startsAt || undefined,
      endsAt: c.endsAt || undefined,
      budgetType: c.budgetType,
      budgetLimit: c.budgetType === "none" ? 0 : c.budgetLimit,
    };
    try {
      const out = campaign ? await promoApi.saveCampaign(campaign.id, body) : await promoApi.createCampaign(body);
      toast.success(campaign ? "Campaña guardada" : "Campaña creada");
      void qc.invalidateQueries({ queryKey: ["campaigns"] });
      void qc.invalidateQueries({ queryKey: ["campaign", out.id] });
      onSaved(out, !campaign);
    } catch (x) {
      setErrs(fieldErrors(x));
      setErr(errorMessage(x));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form
      className="mt-4 grid gap-4"
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      {err && <Notice tone="bad">{err}</Notice>}
      <FormField id="camp-name" label="Nombre" required error={errs.name}>
        <Input value={c.name} maxLength={120} placeholder="Buen Fin 2026" onChange={(e) => set({ name: e.target.value })} />
      </FormField>
      <FormField id="camp-desc" label="Descripción (opcional)" error={errs.description}>
        <Textarea rows={2} maxLength={500} value={c.description ?? ""} onChange={(e) => set({ description: e.target.value })} />
      </FormField>
      <div className="grid gap-4 sm:grid-cols-2">
        <FormField id="camp-start" label="Empieza">
          <DateTimeField value={c.startsAt} onChange={(v) => set({ startsAt: v })} />
        </FormField>
        <FormField id="camp-end" label="Termina" error={errs.endsAt}>
          <DateTimeField value={c.endsAt} onChange={(v) => set({ endsAt: v })} />
        </FormField>
      </div>
      <Field label="Presupuesto">
        <Choice
          label="Tipo de presupuesto"
          columns="grid-cols-1"
          value={c.budgetType}
          onChange={(v) => set({ budgetType: v, budgetLimit: 0 })}
          options={[
            { value: "none", label: "Sin presupuesto" },
            { value: "amount", label: "Monto máximo de descuento", help: "Suma de los descuentos de todas sus promociones." },
            { value: "uses", label: "Número máximo de usos", help: "Cada promoción aplicada a un pedido cuenta como un uso." },
          ]}
        />
      </Field>
      {c.budgetType !== "none" && (
        <FormField id="camp-budget" label={c.budgetType === "amount" ? "Monto máximo" : "Usos máximos"} required error={errs.budgetLimit}>
          <NumField kind={c.budgetType === "amount" ? "money" : "int"} value={c.budgetLimit} onChange={(v) => set({ budgetLimit: v })} />
        </FormField>
      )}
      <Field label="Estado" hint="Pausada: ninguna de sus promociones aplica.">
        <Select value={c.status} onValueChange={(v) => set({ status: v as Campaign["status"] })}>
          <SelectTrigger aria-label="Estado">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="active">Activa</SelectItem>
            <SelectItem value="paused">Pausada</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onClose}>
          Cancelar
        </Button>
        <Button type="submit" disabled={busy}>
          {busy ? "Guardando…" : campaign ? "Guardar" : "Crear campaña"}
        </Button>
      </div>
    </form>
  );
}

function CampaignDetail({ me, id }: { me: Me; id: string }) {
  const qc = useQueryClient();
  const canEdit = can(me, "promotions", "edit");
  const canAdmin = can(me, "promotions", "admin");
  const q = useQuery({ queryKey: ["campaign", id], queryFn: () => promoApi.campaign(id) });
  const [edit, setEdit] = useState(false);
  const [del, setDel] = useState(false);
  const [busy, setBusy] = useState(false);
  const back = (
    <a className="inline-flex items-center gap-1 text-sm text-primary hover:underline" href={href("promociones", "campanas")}>
      <ArrowLeft className="size-3.5" /> Campañas
    </a>
  );
  if (q.isPending)
    return (
      <div className="grid gap-4">
        {back}
        <Skeleton className="h-32" />
        <Skeleton className="h-48" />
      </div>
    );
  if (q.error || !q.data)
    return (
      <div className="grid gap-4">
        {back}
        <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      </div>
    );
  const { campaign: c, promotions } = q.data;
  const given = promotions.reduce((a, p) => a + p.discountTotal, 0);

  async function remove() {
    setBusy(true);
    try {
      await promoApi.removeCampaign(id);
      toast.success("Campaña eliminada; sus promociones quedaron sin campaña");
      void qc.invalidateQueries({ queryKey: ["campaigns"] });
      void qc.invalidateQueries({ queryKey: ["promotions"] });
      navigate("promociones", "campanas");
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="grid gap-1">
          {back}
          <h1 className="flex flex-wrap items-center gap-2 text-xl font-semibold tracking-tight">
            {c.name} <StatusBadge status={c.displayStatus} />
          </h1>
          {c.description && <p className="max-w-2xl text-sm text-muted-foreground">{c.description}</p>}
        </div>
        <div className="flex flex-wrap gap-2">
          {canEdit && (
            <Button variant="outline" onClick={() => setEdit(true)}>
              <Pencil /> Editar
            </Button>
          )}
          {canAdmin && (
            <Button variant="outline" onClick={() => setDel(true)}>
              <Trash2 /> Eliminar
            </Button>
          )}
        </div>
      </div>
      <div className="grid gap-4 md:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle>Presupuesto</CardTitle>
          </CardHeader>
          <CardContent>
            <Budget c={c} large />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Vigencia</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-1 text-sm">
            <span className="text-lg font-semibold">{windowText(c.startsAt, c.endsAt)}</span>
            <span className="text-xs text-muted-foreground">Fuera de estas fechas sus promociones no aplican, aunque estén activas.</span>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Resultado</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-1 text-sm">
            <span className="text-lg font-semibold tabular-nums">{formatMoney(given, c.currency)}</span>
            <span className="text-xs text-muted-foreground">
              de descuento otorgado en {c.used} {c.used === 1 ? "uso" : "usos"}.
            </span>
          </CardContent>
        </Card>
      </div>
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-base font-semibold">Promociones de la campaña</h3>
        {canEdit && (
          <Button asChild variant="outline" size="sm">
            <a href={href("promociones", "nueva")}>
              <Plus /> Nueva promoción
            </a>
          </Button>
        )}
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Promoción</TableHead>
            <TableHead>Estado</TableHead>
            <TableHead className="hidden md:table-cell">Tipo</TableHead>
            <TableHead className="text-right">Usos</TableHead>
            <TableHead className="text-right">Descuento otorgado</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {promotions.map((p) => (
            <TableRow key={p.id}>
              <TableCell>
                <a className="font-medium text-primary hover:underline" href={href("promociones", "p", p.id)}>
                  {p.name}
                </a>
                <div className="text-xs text-muted-foreground">{p.summary}</div>
              </TableCell>
              <TableCell>
                <StatusBadge status={p.displayStatus} />
              </TableCell>
              <TableCell className="hidden md:table-cell">{TYPE_LABEL[p.type]}</TableCell>
              <TableCell className="text-right tabular-nums">{p.uses}</TableCell>
              <TableCell className="text-right tabular-nums">{formatMoney(p.discountTotal, p.currency)}</TableCell>
            </TableRow>
          ))}
          {promotions.length === 0 && (
            <TableRow>
              <TableCell colSpan={5} className="py-8 text-center text-muted-foreground">
                Ninguna promoción pertenece a esta campaña. Elige “{c.name}” en el campo Campaña al crear o editar una promoción.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      <CampaignForm open={edit} campaign={c} onClose={() => setEdit(false)} onSaved={() => setEdit(false)} />
      <AlertDialog open={del} onOpenChange={setDel}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>¿Eliminar la campaña “{c.name}”?</AlertDialogTitle>
            <AlertDialogDescription>
              Sus {promotions.length} promociones no se borran: quedan sin campaña y dejan de depender de estas fechas y presupuesto.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>Cancelar</AlertDialogCancel>
            <Button variant="destructive" disabled={busy} onClick={() => void remove()}>
              {busy ? "Eliminando…" : "Eliminar campaña"}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
