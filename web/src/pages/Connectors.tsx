import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Plus, RefreshCw, RotateCcw } from "lucide-react";
import { api, errorMessage, type Me, type Subscription } from "@/api";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { toast } from "@/components/ui/sonner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { validateWebhookUrl } from "@/forms";
import { can } from "@/perms";
import { dateTime, ErrorBox, Notice, Page, Secret, StatusPill } from "./common";

const EVENT_TYPES = [
  "apexx.product.updated",
  "apexx.price.updated",
  "apexx.inventory.level.updated",
  "apexx.promotion.updated",
  "apexx.channel.updated",
  "apexx.order.*",
  "apexx.payment.failed",
  "apexx.invoice.issued",
  "apexx.site.published",
];

export function Connectors({ me }: { me: Me }) {
  return (
    <Page title="Conectores" sub="Envía cada cambio de tu tienda a tus sistemas (ERP, marketplaces, facturación) con eventos firmados.">
      <Tabs defaultValue="webhooks">
        <TabsList>
          <TabsTrigger value="webhooks">Webhooks</TabsTrigger>
          <TabsTrigger value="events">Eventos recientes</TabsTrigger>
        </TabsList>
        <TabsContent value="webhooks">
          <Webhooks me={me} />
        </TabsContent>
        <TabsContent value="events">
          <EventFeed />
        </TabsContent>
      </Tabs>
    </Page>
  );
}

function Webhooks({ me }: { me: Me }) {
  const q = useQuery({ queryKey: ["webhooks"], queryFn: api.webhooks });
  const isAdmin = can(me, "settings", "admin");
  const [create, setCreate] = useState(false);
  const [open, setOpen] = useState<Subscription | null>(null);
  return (
    <div className="grid gap-4">
      {isAdmin && (
        <div>
          <Button onClick={() => setCreate(true)}>
            <Plus /> Nuevo webhook
          </Button>
        </div>
      )}
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>URL</TableHead>
            <TableHead>Eventos</TableHead>
            <TableHead>Estado</TableHead>
            <TableHead>Creado</TableHead>
            <TableHead className="text-right">Detalle</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {(q.data ?? []).map((s) => (
            <TableRow key={s.id}>
              <TableCell className="max-w-[280px] break-all font-mono text-xs">{s.url}</TableCell>
              <TableCell className="text-xs">{s.types?.length ? s.types.join(", ") : "Todos"}</TableCell>
              <TableCell>{s.active ? <Badge variant="ok">Activo</Badge> : <Badge variant="bad">Desactivado</Badge>}</TableCell>
              <TableCell className="text-sm">{dateTime(s.createdAt)}</TableCell>
              <TableCell className="text-right">
                <Button variant="outline" size="sm" onClick={() => setOpen(s)}>
                  Entregas
                </Button>
              </TableCell>
            </TableRow>
          ))}
          {q.data && q.data.length === 0 && (
            <TableRow>
              <TableCell colSpan={5} className="py-8 text-center text-muted-foreground">
                Todavía no hay webhooks. Crea uno para enviar los cambios de tu tienda a otro sistema.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      {create && <CreateWebhook onClose={() => setCreate(false)} />}
      {open && <DeliveriesSheet sub={open} isAdmin={isAdmin} onClose={() => setOpen(null)} />}
    </div>
  );
}

function CreateWebhook({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const [url, setUrl] = useState("");
  const [types, setTypes] = useState<string[]>([]);
  const [urlErr, setUrlErr] = useState<string | undefined>();
  const [secret, setSecret] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const m = useMutation({
    mutationFn: () => api.createWebhook(url.trim(), types),
    onSuccess: (r) => {
      setSecret(r.secret);
      void qc.invalidateQueries({ queryKey: ["webhooks"] });
    },
    onError: (e) => setErr(errorMessage(e)),
  });
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Nuevo webhook</DialogTitle>
          <DialogDescription>Apexx enviará cada evento firmado con HMAC-SHA256 en el encabezado Apexx-Signature y reintentará durante 24 h si tu sistema falla.</DialogDescription>
        </DialogHeader>
        {secret ? (
          <div className="grid gap-3">
            <Notice tone="ok">Webhook creado.</Notice>
            <Secret label="Secreto para verificar la firma" value={secret} />
            <DialogFooter>
              <Button onClick={onClose}>Listo</Button>
            </DialogFooter>
          </div>
        ) : (
          <form
            className="grid gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              setErr(null);
              const v = validateWebhookUrl(url.trim());
              setUrlErr(v ?? undefined);
              if (!v) m.mutate();
            }}
            noValidate
          >
            <FormField id="wh-url" label="URL de destino" required error={urlErr} description="Debe ser https y pública.">
              <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://hooks.mi-erp.com/apexx" />
            </FormField>
            <fieldset className="grid gap-2">
              <legend className="mb-1 text-sm font-medium">Eventos (sin selección = todos)</legend>
              {EVENT_TYPES.map((t) => (
                <label key={t} className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    className="size-4 accent-[var(--accent)]"
                    checked={types.includes(t)}
                    onChange={(e) => setTypes(e.target.checked ? [...types, t] : types.filter((x) => x !== t))}
                  />
                  <code className="text-xs">{t}</code>
                </label>
              ))}
            </fieldset>
            {err && <Notice tone="bad">{err}</Notice>}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={onClose}>
                Cancelar
              </Button>
              <Button type="submit" disabled={m.isPending}>
                Crear webhook
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

function DeliveriesSheet({ sub, isAdmin, onClose }: { sub: Subscription; isAdmin: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["deliveries", sub.id], queryFn: () => api.deliveries(sub.id), refetchInterval: 5000 });
  const [after, setAfter] = useState("0");
  const [preview, setPreview] = useState<{ count: number; capped: boolean } | null>(null);
  const [disableOpen, setDisableOpen] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const count = useMutation({
    mutationFn: async () => {
      const n = Number(after);
      if (!Number.isInteger(n) || n < 0) throw new Error("Escribe un número de secuencia desde 0.");
      const r = await api.events(n, 500, sub.types ?? []);
      return { count: r.items.length, capped: r.items.length >= 500 };
    },
    onSuccess: setPreview,
    onError: (e) => setErr(errorMessage(e)),
  });
  const replay = useMutation({
    mutationFn: () => api.replay(sub.id, Number(after)),
    onSuccess: (r) => {
      toast.success(`Se reencolaron ${r.requeued} eventos`);
      setPreview(null);
      void qc.invalidateQueries({ queryKey: ["deliveries", sub.id] });
    },
    onError: (e) => setErr(errorMessage(e)),
  });
  const disable = useMutation({
    mutationFn: () => api.disableWebhook(sub.id),
    onSuccess: () => {
      toast.success("Webhook desactivado");
      void qc.invalidateQueries({ queryKey: ["webhooks"] });
      onClose();
    },
    onError: (e) => setErr(errorMessage(e)),
  });

  return (
    <Sheet open onOpenChange={(v) => !v && onClose()}>
      <SheetContent className="w-[min(760px,96vw)]">
        <SheetHeader>
          <SheetTitle>Entregas del webhook</SheetTitle>
          <SheetDescription className="break-all font-mono text-xs">{sub.url}</SheetDescription>
        </SheetHeader>
        {err && <Notice tone="bad">{err}</Notice>}
        <div className="flex items-center justify-between">
          <span className="text-sm text-muted-foreground">Últimas 200 entregas · se actualiza cada 5 s</span>
          <Button variant="ghost" size="sm" onClick={() => void q.refetch()}>
            <RefreshCw /> Actualizar
          </Button>
        </div>
        <ErrorBox error={q.error} />
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Secuencia</TableHead>
              <TableHead>Evento</TableHead>
              <TableHead>Estado</TableHead>
              <TableHead className="text-right">Intentos</TableHead>
              <TableHead>Último resultado</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(q.data ?? []).map((d) => (
              <TableRow key={d.eventId}>
                <TableCell className="font-mono text-xs">{d.eventId}</TableCell>
                <TableCell className="text-xs">
                  {d.type}
                  <div className="text-muted-foreground">{d.subject}</div>
                </TableCell>
                <TableCell>
                  <StatusPill status={d.status} />
                </TableCell>
                <TableCell className="text-right tabular-nums">{d.attempts}</TableCell>
                <TableCell className="text-xs">
                  {d.lastStatus ? `HTTP ${d.lastStatus}` : "—"}
                  {d.lastError && <div className="text-bad">{d.lastError}</div>}
                  {d.status === "pending" && <div className="text-muted-foreground">Próximo intento: {dateTime(d.nextAttempt)}</div>}
                </TableCell>
              </TableRow>
            ))}
            {q.data && q.data.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="py-6 text-center text-muted-foreground">
                  Sin entregas todavía.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>

        {isAdmin && sub.active && (
          <Card>
            <CardHeader>
              <CardTitle>Reenviar eventos</CardTitle>
              <CardDescription>Útil si tu sistema estuvo caído o perdió datos. Primero calcula cuántos eventos se enviarán.</CardDescription>
            </CardHeader>
            <CardContent className="grid gap-3">
              <div className="flex flex-wrap items-end gap-2">
                <FormField id="replay-after" label="Desde la secuencia (exclusiva)" className="w-48">
                  <Input
                    inputMode="numeric"
                    value={after}
                    onChange={(e) => {
                      setAfter(e.target.value);
                      setPreview(null);
                    }}
                  />
                </FormField>
                <Button variant="outline" onClick={() => count.mutate()} disabled={count.isPending}>
                  Calcular
                </Button>
              </div>
              {preview && (
                <Notice tone="warn">
                  Se reenviarán {preview.capped ? "al menos " : "hasta "}
                  {preview.count} eventos a este webhook.{" "}
                  <Button size="sm" className="ml-2" onClick={() => replay.mutate()} disabled={replay.isPending || preview.count === 0}>
                    <RotateCcw /> Confirmar reenvío
                  </Button>
                </Notice>
              )}
            </CardContent>
          </Card>
        )}
        {isAdmin && sub.active && (
          <div>
            <Button variant="destructive" onClick={() => setDisableOpen(true)}>
              Desactivar webhook
            </Button>
          </div>
        )}
        <AlertDialog open={disableOpen} onOpenChange={setDisableOpen}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>¿Desactivar este webhook?</AlertDialogTitle>
              <AlertDialogDescription>Dejaremos de enviarle eventos. Las entregas pendientes no se enviarán.</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Volver</AlertDialogCancel>
              <Button variant="destructive" onClick={() => disable.mutate()} disabled={disable.isPending}>
                Sí, desactivar
              </Button>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </SheetContent>
    </Sheet>
  );
}

function EventFeed() {
  const [after, setAfter] = useState(0);
  const q = useQuery({ queryKey: ["events", after], queryFn: () => api.events(after, 50) });
  const [openId, setOpenId] = useState<string | null>(null);
  return (
    <div className="grid gap-3">
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Secuencia</TableHead>
            <TableHead>Evento</TableHead>
            <TableHead>Recurso</TableHead>
            <TableHead>Fecha</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {(q.data?.items ?? []).map((e) => (
            <TableRow key={e.id} className="cursor-pointer" onClick={() => setOpenId(openId === e.id ? null : e.id)}>
              <TableCell className="font-mono text-xs">{e.apexxsequence}</TableCell>
              <TableCell className="text-xs">
                <button className="text-left text-primary hover:underline" aria-expanded={openId === e.id}>
                  {e.type}
                </button>
                {openId === e.id && <pre className="mt-2 max-w-[560px] overflow-auto rounded bg-muted p-2 text-[11px]">{JSON.stringify(e.data, null, 2)}</pre>}
              </TableCell>
              <TableCell className="font-mono text-xs">{e.subject}</TableCell>
              <TableCell className="text-sm">{dateTime(e.time)}</TableCell>
            </TableRow>
          ))}
          {q.data && q.data.items.length === 0 && (
            <TableRow>
              <TableCell colSpan={4} className="py-6 text-center text-muted-foreground">
                No hay más eventos.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      <div className="flex gap-2">
        <Button variant="outline" disabled={after === 0} onClick={() => setAfter(0)}>
          Desde el inicio
        </Button>
        <Button variant="outline" disabled={!q.data || q.data.items.length < 50} onClick={() => q.data && setAfter(q.data.next)}>
          Siguientes 50
        </Button>
      </div>
    </div>
  );
}
