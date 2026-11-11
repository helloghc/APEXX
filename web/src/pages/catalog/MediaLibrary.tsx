import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { ImageIcon, Search, Trash2 } from "lucide-react";
import { ApiError, errorMessage, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { href } from "@/router";
import { dateTime, ErrorBox, Notice } from "../common";
import { cat, type MediaItem } from "./api";
import { bytes, CatalogPage, Confirm, EmptyState, Field, Skeleton, Uploader, useDebounced } from "./shared";

export function MediaLibrary({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "catalog", "edit");
  const [q, setQ] = useState("");
  const dq = useDebounced(q.trim());
  const list = useInfiniteQuery({
    queryKey: ["media", dq],
    queryFn: ({ pageParam }) => cat.media(dq, pageParam),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor || undefined,
  });
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const total = list.data?.pages[0]?.total ?? 0;
  const [open, setOpen] = useState<MediaItem | null>(null);
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["media"] });
    void qc.invalidateQueries({ queryKey: ["catalog-summary"] });
  };

  return (
    <CatalogPage tab="medios" wide title="Medios" sub={`${total} imágenes. Cada una se guarda en varios tamaños y en WebP para que la tienda cargue rápido.`}>
      {canEdit && <Uploader onUploaded={refresh} />}
      <div className="relative max-w-sm">
        <Search className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden="true" />
        <Input aria-label="Buscar imágenes" placeholder="Buscar por nombre o texto alternativo" className="pl-8" value={q} onChange={(e) => setQ(e.target.value)} />
      </div>
      <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
      {list.isLoading ? (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(140px,1fr))] gap-3">{Array.from({ length: 12 }, (_, i) => <Skeleton key={i} className="aspect-square w-full" />)}</div>
      ) : items.length === 0 ? (
        <EmptyState icon={ImageIcon} title={dq ? "Sin resultados" : "Aún no hay imágenes"} text={dq ? "Prueba con otra palabra." : "Arrastra tus fotos arriba para subirlas."} />
      ) : (
        <ul className="grid grid-cols-[repeat(auto-fill,minmax(140px,1fr))] gap-3">
          {items.map((m) => (
            <li key={m.id}>
              <button type="button" onClick={() => setOpen(m)} className="group grid w-full gap-1 rounded-lg border bg-card p-2 text-left transition-colors hover:border-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                <img src={m.thumb} alt={m.alt} loading="lazy" width={m.width} height={m.height} className="aspect-square w-full rounded object-cover" />
                <span className="truncate text-xs font-medium">{m.filename}</span>
                <span className="text-[11px] text-muted-foreground">{m.width}×{m.height} · {bytes(m.bytes)}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
      {list.hasNextPage && (
        <div className="flex justify-center">
          <Button variant="outline" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>{list.isFetchingNextPage ? "Cargando…" : "Ver más"}</Button>
        </div>
      )}
      {open && <MediaDetail media={open} canEdit={canEdit} onClose={() => setOpen(null)} onChanged={refresh} />}
    </CatalogPage>
  );
}

function MediaDetail({ media, canEdit, onClose, onChanged }: { media: MediaItem; canEdit: boolean; onClose: () => void; onChanged: () => void }) {
  const detail = useQuery({ queryKey: ["media-detail", media.id], queryFn: () => cat.mediaDetail(media.id) });
  const [alt, setAlt] = useState(media.alt);
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState<null | "plain" | "force">(null);
  useEffect(() => setAlt(media.alt), [media]);
  const used = (detail.data?.usedBy.products?.length ?? 0) + (detail.data?.usedBy.brands?.length ?? 0);

  async function saveAlt() {
    if (alt.trim().length < 3) {
      toast.error("Describe la imagen con al menos 3 caracteres.");
      return;
    }
    setBusy(true);
    try {
      await cat.setAlt(media.id, alt.trim());
      toast.success("Texto alternativo guardado.");
      onChanged();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  async function remove(force: boolean) {
    try {
      await cat.deleteMedia(media.id, force);
      toast.success("Imagen eliminada.");
      onChanged();
      onClose();
    } catch (e) {
      if (e instanceof ApiError && e.problem.status === 409 && !force) setConfirm("force");
      else {
        toast.error(errorMessage(e));
        setConfirm(null);
      }
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle className="truncate">{media.filename}</DialogTitle>
          <DialogDescription>{media.width}×{media.height} px · {bytes(media.bytes)} · subida el {dateTime(media.createdAt)}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 sm:grid-cols-[240px_minmax(0,1fr)]">
          <img src={media.url} alt={media.alt} className="max-h-72 w-full rounded-md border object-contain" />
          <div className="grid content-start gap-4">
            <Field label="Texto alternativo" htmlFor="m-alt" hint="Describe la imagen para personas con lector de pantalla y para buscadores.">
              <Input id="m-alt" value={alt} maxLength={200} disabled={!canEdit} onChange={(e) => setAlt(e.target.value)} />
            </Field>
            <div className="text-sm">
              <div className="font-medium">Tamaños generados</div>
              <p className="text-muted-foreground">{media.variants.map((v) => `${v.w}px ${v.format}`).join(" · ")}</p>
            </div>
            {detail.data && (
              used > 0 ? (
                <Notice tone="warn">
                  En uso en {used} {used === 1 ? "lugar" : "lugares"}:{" "}
                  {(detail.data.usedBy.products ?? []).slice(0, 5).map((pid, i) => (
                    <a key={pid} className="underline" href={href("catalogo", "productos", pid)} onClick={onClose}>{i ? ", " : ""}{pid}</a>
                  ))}
                </Notice>
              ) : (
                <p className="text-sm text-muted-foreground">No se usa en ningún producto.</p>
              )
            )}
          </div>
        </div>
        <DialogFooter className="sm:justify-between">
          {canEdit ? <Button variant="outline" onClick={() => setConfirm("plain")}><Trash2 aria-hidden="true" /> Eliminar</Button> : <span />}
          <div className="flex gap-2">
            <Button variant="outline" onClick={onClose}>Cerrar</Button>
            {canEdit && <Button onClick={() => void saveAlt()} disabled={busy || alt === media.alt}>Guardar</Button>}
          </div>
        </DialogFooter>
        <Confirm
          open={!!confirm}
          title={confirm === "force" ? "Esta imagen está en uso" : "¿Eliminar esta imagen?"}
          description={confirm === "force" ? "Si la eliminas, desaparece de los productos y marcas que la usan. No se puede deshacer." : "No se puede deshacer."}
          confirmLabel={confirm === "force" ? "Eliminar de todos modos" : "Eliminar"}
          destructive
          onConfirm={() => void remove(confirm === "force")}
          onClose={() => setConfirm(null)}
        />
      </DialogContent>
    </Dialog>
  );
}
