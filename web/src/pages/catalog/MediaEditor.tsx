import { useEffect, useRef, useState, type PointerEvent } from "react";
import { FlipHorizontal2, FlipVertical2, RotateCcw, RotateCw, Undo2 } from "lucide-react";
import { credentials, errorMessage, request } from "@/api";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import type { MediaItem } from "./api";

export interface Edits { rotate?: number; flipH?: boolean; flipV?: boolean; crop?: { x: number; y: number; w: number; h: number } | null; brightness?: number; contrast?: number; saturation?: number }
const ASPECTS: { label: string; r: number | null }[] = [{ label: "Libre", r: null }, { label: "1:1", r: 1 }, { label: "4:5", r: 4 / 5 }, { label: "3:4", r: 3 / 4 }, { label: "16:9", r: 16 / 9 }];
const MAX = 520; // preview size in CSS pixels

/** MediaEditor rotates, mirrors, crops and adjusts light and colour; the server applies the same recipe to the original. */
export function MediaEditor({ media, onClose, onSaved }: { media: MediaItem & { original?: string; edits?: Edits | null }; onClose: () => void; onSaved: (m: MediaItem) => void }) {
  const [e, setE] = useState<Edits>(() => ({ rotate: 0, brightness: 0, contrast: 0, saturation: 0, ...(media.edits ?? {}) }));
  const [img, setImg] = useState<ImageBitmap | null>(null);
  const [aspect, setAspect] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const canvas = useRef<HTMLCanvasElement>(null);
  const drag = useRef<{ x: number; y: number } | null>(null);

  // The untouched original, read with the panel's credentials.
  useEffect(() => {
    let bmp: ImageBitmap | null = null;
    const headers: Record<string, string> = {};
    if (credentials.key) headers.Authorization = `Bearer ${credentials.key}`;
    void fetch(media.original ?? media.url, { headers, credentials: "same-origin" })
      .then((r) => { if (!r.ok) throw new Error(String(r.status)); return r.blob(); })
      .then((b) => createImageBitmap(b, { imageOrientation: "from-image" }))
      .then((b) => { bmp = b; setImg(b); })
      .catch(() => toast.error("No se pudo abrir la imagen original"));
    return () => bmp?.close();
  }, [media.original, media.url]);

  const rot = ((e.rotate ?? 0) % 360 + 360) % 360;
  const side = rot === 90 || rot === 270;
  const iw = img ? (side ? img.height : img.width) : 1;
  const ih = img ? (side ? img.width : img.height) : 1;
  const scale = Math.min(MAX / iw, MAX / ih, 1);
  const cw = Math.round(iw * scale), ch = Math.round(ih * scale);

  useEffect(() => {
    const c = canvas.current;
    if (!c || !img) return;
    c.width = cw; c.height = ch;
    const ctx = c.getContext("2d")!;
    ctx.save();
    ctx.clearRect(0, 0, cw, ch);
    // Approximates the server's curves closely enough to judge the result.
    ctx.filter = `brightness(${1 + (e.brightness ?? 0) / 200}) contrast(${1 + (e.contrast ?? 0) / ((e.contrast ?? 0) > 0 ? 66 : 100)}) saturate(${1 + (e.saturation ?? 0) / 100})`;
    const dw = side ? ch : cw, dh = side ? cw : ch;
    // Like the server: turn first, then mirror on the screen axes.
    ctx.translate(cw / 2, ch / 2);
    ctx.scale(e.flipH ? -1 : 1, e.flipV ? -1 : 1);
    ctx.rotate((rot * Math.PI) / 180);
    ctx.drawImage(img, -dw / 2, -dh / 2, dw, dh);
    ctx.restore();
    if (e.crop) {
      const { x, y, w, h } = e.crop;
      ctx.fillStyle = "rgba(0,0,0,0.55)";
      ctx.beginPath();
      ctx.rect(0, 0, cw, ch);
      ctx.rect(x * cw, y * ch, w * cw, h * ch);
      ctx.fill("evenodd");
      ctx.strokeStyle = "#fff"; ctx.lineWidth = 2; ctx.setLineDash([6, 4]);
      ctx.strokeRect(x * cw, y * ch, w * cw, h * ch);
    }
  }, [img, e, rot, side, cw, ch]);

  const point = (ev: PointerEvent<HTMLCanvasElement>) => {
    const r = ev.currentTarget.getBoundingClientRect();
    return { x: Math.min(1, Math.max(0, (ev.clientX - r.left) / r.width)), y: Math.min(1, Math.max(0, (ev.clientY - r.top) / r.height)) };
  };
  const onDown = (ev: PointerEvent<HTMLCanvasElement>) => { ev.currentTarget.setPointerCapture(ev.pointerId); drag.current = point(ev); };
  const onMove = (ev: PointerEvent<HTMLCanvasElement>) => {
    const s = drag.current;
    if (!s) return;
    const p = point(ev);
    let w = Math.abs(p.x - s.x), h = Math.abs(p.y - s.y);
    if (aspect) {
      // Keep the ratio in real pixels of the rotated image.
      const hFromW = (w * iw) / aspect / ih;
      if (hFromW <= 1) h = hFromW; else { h = 1; w = (h * ih * aspect) / iw; }
    }
    const x = p.x < s.x ? Math.max(0, s.x - w) : s.x, y = p.y < s.y ? Math.max(0, s.y - h) : s.y;
    w = Math.min(w, 1 - x); h = Math.min(h, 1 - y);
    if (w > 0.02 && h > 0.02) setE((v) => ({ ...v, crop: { x, y, w, h } }));
  };
  const onUp = () => { drag.current = null; };
  const turn = (d: number) => setE((v) => {
    // A crop follows the image when it turns.
    let crop = v.crop ?? null;
    if (crop) crop = d > 0 ? { x: 1 - crop.y - crop.h, y: crop.x, w: crop.h, h: crop.w } : { x: crop.y, y: 1 - crop.x - crop.w, w: crop.h, h: crop.w };
    return { ...v, rotate: (((v.rotate ?? 0) + d) % 360 + 360) % 360, crop };
  });
  const slider = (key: "brightness" | "contrast" | "saturation", label: string) => (
    <div className="grid gap-1">
      <div className="flex justify-between text-sm"><Label htmlFor={`me-${key}`}>{label}</Label><span className="tabular-nums text-muted-foreground">{e[key] ?? 0}</span></div>
      <input id={`me-${key}`} type="range" min={-100} max={100} value={e[key] ?? 0} onChange={(ev) => setE({ ...e, [key]: Number(ev.target.value) })} onDoubleClick={() => setE({ ...e, [key]: 0 })} />
    </div>
  );
  async function save(asCopy: boolean, edits: Edits = e) {
    setBusy(true);
    const clean: Edits = { rotate: edits.rotate || undefined, flipH: edits.flipH || undefined, flipV: edits.flipV || undefined, crop: edits.crop ?? undefined,
      brightness: edits.brightness || undefined, contrast: edits.contrast || undefined, saturation: edits.saturation || undefined };
    try {
      const r = await request<{ media: MediaItem; copy: boolean }>("POST", `/v1/catalog/media/${media.id}/edit`, { edits: clean, asCopy });
      toast.success(asCopy ? "Copia editada agregada a la biblioteca." : Object.values(clean).some((v) => v !== undefined) ? "Imagen editada. Los productos que la usan ya muestran la nueva versión." : "Imagen original restaurada.");
      onSaved(r.media);
    } catch (err) { toast.error(errorMessage(err)); } finally { setBusy(false); }
  }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-4xl">
        <DialogHeader>
          <DialogTitle>Editar imagen</DialogTitle>
          <DialogDescription>Los cambios se aplican sobre el original: puedes cambiarlos o deshacerlos cuando quieras. Arrastra sobre la imagen para recortar.</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 md:grid-cols-[minmax(0,1fr)_240px]">
          <div className="grid min-h-40 place-items-center rounded-md bg-muted p-2">
            {img ? <canvas ref={canvas} data-media-editor className="max-w-full cursor-crosshair touch-none" style={{ width: cw, maxWidth: "100%", height: "auto" }} onPointerDown={onDown} onPointerMove={onMove} onPointerUp={onUp} aria-label="Vista previa; arrastra para recortar" />
              : <p className="text-sm text-muted-foreground">Abriendo el original…</p>}
          </div>
          <div className="grid content-start gap-3">
            <div className="flex flex-wrap gap-1.5">
              <Button size="icon" variant="outline" aria-label="Girar a la izquierda" onClick={() => turn(-90)}><RotateCcw className="size-4" /></Button>
              <Button size="icon" variant="outline" aria-label="Girar a la derecha" onClick={() => turn(90)}><RotateCw className="size-4" /></Button>
              <Button size="icon" variant="outline" aria-pressed={!!e.flipH} aria-label="Voltear horizontal" onClick={() => setE({ ...e, flipH: !e.flipH, crop: e.crop ? { ...e.crop, x: 1 - e.crop.x - e.crop.w } : e.crop })}><FlipHorizontal2 className="size-4" /></Button>
              <Button size="icon" variant="outline" aria-pressed={!!e.flipV} aria-label="Voltear vertical" onClick={() => setE({ ...e, flipV: !e.flipV, crop: e.crop ? { ...e.crop, y: 1 - e.crop.y - e.crop.h } : e.crop })}><FlipVertical2 className="size-4" /></Button>
            </div>
            <div className="grid gap-1">
              <span className="text-sm font-medium">Recorte</span>
              <div className="flex flex-wrap gap-1">{ASPECTS.map((a) => <Button key={a.label} size="sm" variant={aspect === a.r ? "default" : "outline"} onClick={() => setAspect(a.r)}>{a.label}</Button>)}</div>
              {e.crop && <Button size="sm" variant="ghost" onClick={() => setE({ ...e, crop: null })}>Quitar recorte</Button>}
            </div>
            {slider("brightness", "Brillo")}
            {slider("contrast", "Contraste")}
            {slider("saturation", "Saturación")}
            {media.edits && <Button variant="ghost" size="sm" disabled={busy} onClick={() => void save(false, {})}><Undo2 className="size-4" /> Restaurar el original</Button>}
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Cancelar</Button>
          <Button variant="outline" disabled={busy || !img} onClick={() => void save(true)}>Guardar como copia</Button>
          <Button disabled={busy || !img} onClick={() => void save(false)}>{busy ? "Guardando…" : "Guardar"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
