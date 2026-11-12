import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { DescribePromotion } from "@/components/Copilots";
import { useState } from "react";
import { Archive, Copy, MoreHorizontal, Pause, Pencil, Play, Plus, Search, Tag, Ticket, Trash2 } from "lucide-react";
import { errorMessage, type Me } from "@/api";
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
import { Checkbox } from "@/components/ui/checkbox";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { toast } from "@/components/ui/sonner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { formatMoney } from "@/money";
import { ErrorBox } from "@/pages/common";
import { can } from "@/perms";
import { promoApi, type PromotionView, type PromoType } from "@/promoApi";
import { href, navigate } from "@/router";
import { TYPE_LABEL, windowText } from "./lib";
import { EmptyState, SkeletonRows, StatusBadge, useDebounced } from "./shared";

const TABS: { key: string; label: string }[] = [
  { key: "", label: "Todas" },
  { key: "activa", label: "Activas" },
  { key: "programada", label: "Programadas" },
  { key: "pausada", label: "Pausadas" },
  { key: "borrador", label: "Borradores" },
  { key: "vencida", label: "Vencidas" },
  { key: "archivada", label: "Archivadas" },
];

const ALL = "__all";

export function PromotionList({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "promotions", "edit");
  const canAdmin = can(me, "promotions", "admin");
  const [status, setStatus] = useState("");
  const [type, setType] = useState("");
  const [campaign, setCampaign] = useState("");
  const [sort, setSort] = useState("recent");
  const [text, setText] = useState("");
  const [sel, setSel] = useState<Set<string>>(new Set());
  const [del, setDel] = useState<PromotionView | null>(null);
  const [busy, setBusy] = useState(false);
  const q = useDebounced(text.trim(), 300);

  const filter = { status, type, campaign, q, sort };
  const list = useInfiniteQuery({
    queryKey: ["promotions", filter],
    queryFn: ({ pageParam }) => promoApi.list({ ...filter, cursor: pageParam, limit: 25 }),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor || undefined,
  });
  const campaigns = useQuery({ queryKey: ["campaigns"], queryFn: promoApi.campaigns, staleTime: 60_000 });

  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const first = list.data?.pages[0];
  const counts = first?.counts ?? {};
  const totalAll = Object.entries(counts).reduce((a, [k, n]) => (k === "archivada" ? a : a + n), 0);
  const filtered = Boolean(status || type || campaign || q);
  const nothingYet = !list.isPending && !filtered && totalAll === 0 && (counts.archivada ?? 0) === 0;

  function refresh() {
    setSel(new Set());
    void qc.invalidateQueries({ queryKey: ["promotions"] });
  }

  async function bulk(ids: string[], action: "activate" | "pause" | "archive", done: string) {
    setBusy(true);
    try {
      const r = await promoApi.bulk(ids, action);
      toast.success(r.changed === 1 ? done : `${r.changed} promociones: ${done.toLowerCase()}`);
      refresh();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  async function duplicate(p: PromotionView) {
    try {
      const copy = await promoApi.duplicate(p.id);
      toast.success("Copia creada como borrador");
      void qc.invalidateQueries({ queryKey: ["promotions"] });
      navigate("promociones", "p", copy.id);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  async function remove(p: PromotionView) {
    setBusy(true);
    try {
      const r = await promoApi.remove(p.id);
      toast.success(r.archived ? "La promoción ya tenía usos: se archivó para conservar el historial" : "Promoción eliminada");
      setDel(null);
      refresh();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  if (nothingYet) {
    return (
      <EmptyState
        icon={<Tag />}
        title="Crea tu primera promoción"
        text="Descuentos por porcentaje o monto, envío gratis, 3x2, escalonadas, regalos y paquetes. Cada una explica al comprador por qué aplica o qué le falta."
        action={
          canEdit && (
            <Button asChild>
              <a href={href("promociones", "nueva")}>
                <Plus /> Nueva promoción
              </a>
            </Button>
          )
        }
      />
    );
  }

  const allSelected = items.length > 0 && items.every((p) => sel.has(p.id));
  const cols = canEdit ? 8 : 6;

  return (
    <div className="grid gap-4">
      {canEdit && <DescribePromotion />}
      <div className="flex gap-1 overflow-x-auto pb-1" role="tablist" aria-label="Filtrar por estado">
        {TABS.map((t) => {
          const n = t.key === "" ? totalAll : (counts[t.key] ?? 0);
          return (
            <button
              key={t.key}
              type="button"
              role="tab"
              aria-selected={status === t.key}
              onClick={() => {
                setStatus(t.key);
                setSel(new Set());
              }}
              className={cn(
                "flex cursor-pointer items-center gap-1.5 whitespace-nowrap rounded-full border px-3 py-1 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                status === t.key ? "border-primary bg-primary text-primary-foreground" : "bg-card text-muted-foreground hover:text-foreground",
              )}
            >
              {t.label}
              <span className={cn("rounded-full px-1.5 text-xs tabular-nums", status === t.key ? "bg-primary-foreground/20" : "bg-muted")}>{n}</span>
            </button>
          );
        })}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-52 flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
          <Input className="pl-9" placeholder="Buscar por nombre o identificador" aria-label="Buscar promociones" value={text} onChange={(e) => setText(e.target.value)} />
        </div>
        <Select value={type || ALL} onValueChange={(v) => setType(v === ALL ? "" : v)}>
          <SelectTrigger className="w-44" aria-label="Tipo">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>Todos los tipos</SelectItem>
            {(Object.keys(TYPE_LABEL) as PromoType[]).map((t) => (
              <SelectItem key={t} value={t}>
                {TYPE_LABEL[t]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {(campaigns.data?.length ?? 0) > 0 && (
          <Select value={campaign || ALL} onValueChange={(v) => setCampaign(v === ALL ? "" : v)}>
            <SelectTrigger className="w-44" aria-label="Campaña">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>Todas las campañas</SelectItem>
              {campaigns.data?.map((c) => (
                <SelectItem key={c.id} value={c.id}>
                  {c.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        <Select value={sort} onValueChange={setSort}>
          <SelectTrigger className="w-48" aria-label="Ordenar">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="recent">Modificadas recientemente</SelectItem>
            <SelectItem value="name">Nombre (A–Z)</SelectItem>
            <SelectItem value="uses">Más usadas</SelectItem>
            <SelectItem value="discount">Mayor descuento otorgado</SelectItem>
            <SelectItem value="priority">Mayor prioridad</SelectItem>
            <SelectItem value="ends">Terminan pronto</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {sel.size > 0 && (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border bg-accent/50 px-3 py-2 text-sm" role="region" aria-label="Acciones en lote">
          <strong>{sel.size === 1 ? "1 seleccionada" : `${sel.size} seleccionadas`}</strong>
          <span className="flex-1" />
          <Button size="sm" variant="outline" disabled={busy} onClick={() => void bulk([...sel], "activate", "Promoción activada")}>
            <Play /> Activar
          </Button>
          <Button size="sm" variant="outline" disabled={busy} onClick={() => void bulk([...sel], "pause", "Promoción pausada")}>
            <Pause /> Pausar
          </Button>
          <Button size="sm" variant="outline" disabled={busy} onClick={() => void bulk([...sel], "archive", "Promoción archivada")}>
            <Archive /> Archivar
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setSel(new Set())}>
            Cancelar
          </Button>
        </div>
      )}

      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      <Table>
        <TableHeader>
          <TableRow>
            {canEdit && (
              <TableHead className="w-9">
                <Checkbox
                  aria-label="Seleccionar todas"
                  checked={allSelected}
                  onCheckedChange={(c) => setSel(c ? new Set(items.map((p) => p.id)) : new Set())}
                />
              </TableHead>
            )}
            <TableHead>Promoción</TableHead>
            <TableHead>Estado</TableHead>
            <TableHead className="hidden lg:table-cell">Tipo</TableHead>
            <TableHead className="hidden md:table-cell">Vigencia</TableHead>
            <TableHead className="text-right">Usos</TableHead>
            <TableHead className="hidden text-right sm:table-cell">Descuento otorgado</TableHead>
            {canEdit && (
              <TableHead className="w-10">
                <span className="sr-only">Acciones</span>
              </TableHead>
            )}
          </TableRow>
        </TableHeader>
        <TableBody>
          {list.isPending && <SkeletonRows cols={cols} />}
          {items.map((p) => (
            <TableRow key={p.id} data-state={sel.has(p.id) ? "selected" : undefined} className="data-[state=selected]:bg-accent/30">
              {canEdit && (
                <TableCell>
                  <Checkbox
                    aria-label={`Seleccionar ${p.name}`}
                    checked={sel.has(p.id)}
                    onCheckedChange={(c) =>
                      setSel((s) => {
                        const n = new Set(s);
                        if (c) n.add(p.id);
                        else n.delete(p.id);
                        return n;
                      })
                    }
                  />
                </TableCell>
              )}
              <TableCell className="max-w-md">
                <a className="font-medium text-primary hover:underline" href={href("promociones", "p", p.id)}>
                  {p.name}
                </a>
                <div className="text-xs text-muted-foreground">{p.summary}</div>
                <div className="mt-1 flex flex-wrap gap-1">
                  {p.conditions.requiresCoupon && (
                    <Badge variant="outline" className="gap-1 font-normal">
                      <Ticket className="size-3" /> {p.coupons === 1 ? "1 cupón" : `${p.coupons} cupones`}
                    </Badge>
                  )}
                  {p.campaignName && (
                    <Badge variant="outline" className="font-normal">
                      {p.campaignName}
                    </Badge>
                  )}
                  {p.stacking.mode === "exclusive" && (
                    <Badge variant="outline" className="font-normal">
                      Exclusiva
                    </Badge>
                  )}
                </div>
              </TableCell>
              <TableCell>
                <StatusBadge status={p.displayStatus} />
              </TableCell>
              <TableCell className="hidden whitespace-nowrap lg:table-cell">{TYPE_LABEL[p.type] ?? p.type}</TableCell>
              <TableCell className="hidden whitespace-nowrap text-muted-foreground md:table-cell">{windowText(p.conditions.startsAt, p.conditions.endsAt)}</TableCell>
              <TableCell className="whitespace-nowrap text-right tabular-nums">
                {p.uses}
                {p.conditions.maxUses ? <span className="text-muted-foreground"> / {p.conditions.maxUses}</span> : null}
              </TableCell>
              <TableCell className="hidden text-right tabular-nums sm:table-cell">{formatMoney(p.discountTotal, p.currency)}</TableCell>
              {canEdit && (
                <TableCell>
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button variant="ghost" size="icon" className="size-8" aria-label={`Acciones de ${p.name}`}>
                        <MoreHorizontal />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem onSelect={() => navigate("promociones", "p", p.id)}>
                        <Pencil /> Editar
                      </DropdownMenuItem>
                      <DropdownMenuItem onSelect={() => void duplicate(p)}>
                        <Copy /> Duplicar
                      </DropdownMenuItem>
                      {p.status === "active" ? (
                        <DropdownMenuItem onSelect={() => void bulk([p.id], "pause", "Promoción pausada")}>
                          <Pause /> Pausar
                        </DropdownMenuItem>
                      ) : (
                        <DropdownMenuItem onSelect={() => void bulk([p.id], "activate", "Promoción activada")}>
                          <Play /> Activar
                        </DropdownMenuItem>
                      )}
                      {canAdmin && (
                        <>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem className="text-destructive focus:text-destructive" onSelect={() => setDel(p)}>
                            <Trash2 /> Eliminar
                          </DropdownMenuItem>
                        </>
                      )}
                    </DropdownMenuContent>
                  </DropdownMenu>
                </TableCell>
              )}
            </TableRow>
          ))}
          {!list.isPending && items.length === 0 && (
            <TableRow>
              <TableCell colSpan={cols} className="py-10 text-center text-muted-foreground">
                Ninguna promoción coincide con estos filtros.{" "}
                <Button
                  variant="link"
                  className="h-auto p-0"
                  onClick={() => {
                    setStatus("");
                    setType("");
                    setCampaign("");
                    setText("");
                  }}
                >
                  Quitar filtros
                </Button>
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      <div className="flex flex-wrap items-center justify-between gap-2 text-sm text-muted-foreground">
        <span aria-live="polite">{first ? `Mostrando ${items.length} de ${first.total}` : ""}</span>
        {list.hasNextPage && (
          <Button variant="outline" size="sm" disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
            {list.isFetchingNextPage ? "Cargando…" : "Cargar más"}
          </Button>
        )}
      </div>

      <AlertDialog open={del !== null} onOpenChange={(o) => !o && setDel(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>¿Eliminar “{del?.name}”?</AlertDialogTitle>
            <AlertDialogDescription>
              {del && del.uses > 0
                ? "Esta promoción ya se usó en pedidos. Se archivará: deja de aplicar y sus cupones dejan de funcionar, pero el historial se conserva."
                : "Deja de aplicar de inmediato y se borran sus cupones. Esta acción no se puede deshacer."}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>Cancelar</AlertDialogCancel>
            <Button variant="destructive" disabled={busy} onClick={() => del && void remove(del)}>
              {busy ? "Eliminando…" : del && del.uses > 0 ? "Archivar" : "Eliminar"}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
