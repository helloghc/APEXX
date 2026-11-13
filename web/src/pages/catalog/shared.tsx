import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { Check, ChevronDown, ImageIcon, ImagePlus, Search, Upload, X, type LucideIcon } from "lucide-react";
import { errorMessage, fieldErrors } from "@/api";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/sonner";
import { cn } from "@/lib/utils";
import { href } from "@/router";
import { ErrorBox } from "../common";
import { cat, type MediaItem } from "./api";

export const TABS: { route: string; label: string }[] = [
  { route: "", label: "Productos" },
  { route: "categorias", label: "Categorías" },
  { route: "colecciones", label: "Colecciones" },
  { route: "marcas", label: "Marcas" },
  { route: "atributos", label: "Atributos" },
  { route: "medios", label: "Medios" },
  { route: "importar", label: "Importar y exportar" },
];

/** CatalogPage is the shell of every catalog screen: title, actions and the section tabs. */
export function CatalogPage({
  tab,
  title,
  sub,
  actions,
  children,
  wide,
}: {
  tab: string;
  title: string;
  sub?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  wide?: boolean;
}) {
  return (
    <main className={cn("mx-auto grid w-full content-start gap-5 px-4 py-6 md:px-8", wide ? "max-w-[1400px]" : "max-w-6xl")}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="grid min-w-0 gap-1">
          <h1 className="text-2xl font-bold tracking-tight">{title}</h1>
          {sub && <div className="text-sm text-muted-foreground">{sub}</div>}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
      <nav aria-label="Secciones del catálogo" className="-mx-4 overflow-x-auto border-b px-4 md:mx-0 md:px-0">
        <ul className="flex min-w-max gap-1">
          {TABS.map((t) => (
            <li key={t.route}>
              <a
                href={t.route ? href("catalogo", t.route) : href("catalogo")}
                aria-current={t.route === tab ? "page" : undefined}
                className={cn(
                  "-mb-px inline-block border-b-2 px-3 py-2 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                  t.route === tab ? "border-primary text-foreground" : "border-transparent text-muted-foreground hover:text-foreground",
                )}
              >
                {t.label}
              </a>
            </li>
          ))}
        </ul>
      </nav>
      {children}
    </main>
  );
}

export function EmptyState({ icon: Icon, title, text, action }: { icon: LucideIcon; title: string; text: ReactNode; action?: ReactNode }) {
  return (
    <div className="grid justify-items-center gap-3 rounded-xl border border-dashed bg-card px-6 py-12 text-center">
      <span className="grid size-12 place-items-center rounded-full bg-accent text-accent-foreground">
        <Icon className="size-6" aria-hidden="true" />
      </span>
      <div className="grid gap-1">
        <h2 className="text-base font-semibold">{title}</h2>
        <p className="mx-auto max-w-md text-sm text-muted-foreground">{text}</p>
      </div>
      {action}
    </div>
  );
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("animate-pulse rounded-md bg-muted", className)} aria-hidden="true" />;
}

export function SkeletonRows({ rows = 6, cols }: { rows?: number; cols: number }) {
  return (
    <>
      {Array.from({ length: rows }, (_, i) => (
        <tr key={i} className="border-b last:border-0">
          {Array.from({ length: cols }, (_, j) => (
            <td key={j} className="px-3 py-3">
              <Skeleton className={cn("h-4", j === 0 ? "w-40" : "w-16")} />
            </td>
          ))}
        </tr>
      ))}
    </>
  );
}

/** Thumb shows a small product or media image, or a neutral placeholder. */
export function Thumb({ media, size = 40, className }: { media?: MediaItem | null; size?: number; className?: string }) {
  if (!media)
    return (
      <span className={cn("grid shrink-0 place-items-center rounded-md border bg-muted text-muted-foreground", className)} style={{ width: size, height: size }}>
        <ImageIcon className="size-4" aria-hidden="true" />
      </span>
    );
  return (
    <img
      src={media.thumb}
      srcSet={media.srcset}
      sizes={`${size}px`}
      alt={media.alt}
      width={size}
      height={size}
      loading="lazy"
      className={cn("shrink-0 rounded-md border bg-muted object-cover", className)}
      style={{ width: size, height: size }}
    />
  );
}

export function Confirm({
  open,
  title,
  description,
  confirmLabel,
  destructive,
  busy,
  onConfirm,
  onClose,
}: {
  open: boolean;
  title: string;
  description: ReactNode;
  confirmLabel: string;
  destructive?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <AlertDialog open={open} onOpenChange={(v) => !v && !busy && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div className="text-sm text-muted-foreground">{description}</div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>Cancelar</AlertDialogCancel>
          <AlertDialogAction
            destructive={destructive}
            disabled={busy}
            onClick={(e) => {
              e.preventDefault();
              onConfirm();
            }}
          >
            {busy ? "Un momento…" : confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

export function useDebounced<T>(value: T, ms = 250): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

/** TagInput edits a list of short texts as chips: Enter or comma adds, Backspace removes the last. */
export function TagInput({
  id,
  value,
  onChange,
  suggestions = [],
  placeholder,
  disabled,
  ariaLabel,
}: {
  id?: string;
  value: string[];
  onChange: (v: string[]) => void;
  suggestions?: string[];
  placeholder?: string;
  disabled?: boolean;
  ariaLabel?: string;
}) {
  const [text, setText] = useState("");
  const listId = id ? `${id}-list` : undefined;
  const add = (raw: string) => {
    const parts = raw
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    if (!parts.length) return;
    const next = [...value];
    for (const p of parts) if (!next.some((x) => x.toLowerCase() === p.toLowerCase())) next.push(p);
    onChange(next);
    setText("");
  };
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      add(text);
    } else if (e.key === "Backspace" && text === "" && value.length) {
      onChange(value.slice(0, -1));
    }
  };
  return (
    <div
      className={cn(
        "flex min-h-9 w-full flex-wrap items-center gap-1.5 rounded-md border border-input bg-card px-2 py-1.5 text-sm shadow-xs focus-within:ring-2 focus-within:ring-ring",
        disabled && "opacity-60",
      )}
    >
      {value.map((t) => (
        <Badge key={t} variant="secondary" className="gap-1 pr-1">
          {t}
          {!disabled && (
            <button
              type="button"
              className="rounded-full p-0.5 hover:bg-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              aria-label={`Quitar ${t}`}
              onClick={() => onChange(value.filter((x) => x !== t))}
            >
              <X className="size-3" />
            </button>
          )}
        </Badge>
      ))}
      <input
        id={id}
        list={listId}
        aria-label={ariaLabel}
        className="min-w-24 flex-1 bg-transparent py-0.5 outline-none placeholder:text-muted-foreground"
        value={text}
        disabled={disabled}
        placeholder={value.length ? "" : placeholder}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKey}
        onBlur={() => add(text)}
      />
      {listId && (
        <datalist id={listId}>
          {suggestions
            .filter((s) => !value.includes(s))
            .map((s) => (
              <option key={s} value={s} />
            ))}
        </datalist>
      )}
    </div>
  );
}

/** MultiPick is a dropdown with checkboxes for choosing several items of a list. */
export function MultiPick({
  id,
  label,
  options,
  value,
  onChange,
  empty,
  disabled,
}: {
  id?: string;
  label: string;
  options: { id: string; label: string; depth?: number }[];
  value: string[];
  onChange: (v: string[]) => void;
  empty: string;
  disabled?: boolean;
}) {
  const names = value.map((v) => options.find((o) => o.id === v)?.label).filter(Boolean);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild disabled={disabled}>
        <Button id={id} type="button" variant="outline" className="h-auto min-h-9 w-full justify-between gap-2 whitespace-normal px-3 py-1.5 text-left font-normal">
          <span className={cn("min-w-0 flex-1", !names.length && "text-muted-foreground")}>{names.length ? names.join(", ") : label}</span>
          <ChevronDown className="size-4 shrink-0 opacity-60" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-72 w-[var(--radix-dropdown-menu-trigger-width)] min-w-56 overflow-y-auto">
        {options.length === 0 && <p className="px-2 py-3 text-sm text-muted-foreground">{empty}</p>}
        {options.map((o) => (
          <DropdownMenuCheckboxItem
            key={o.id}
            checked={value.includes(o.id)}
            onSelect={(e) => e.preventDefault()}
            onCheckedChange={(c) => onChange(c ? [...value, o.id] : value.filter((x) => x !== o.id))}
          >
            <span style={{ paddingLeft: (o.depth ?? 0) * 14 }}>{o.label}</span>
          </DropdownMenuCheckboxItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

const ACCEPT = "image/jpeg,image/png,image/webp,image/gif";
export const MAX_UPLOAD = 10 * 1024 * 1024;

interface Pending {
  key: string;
  file: File;
  alt: string;
  preview: string;
  error?: string;
  busy?: boolean;
}

/** altFromName proposes an alt text from a file name; the person must still confirm it. */
function altFromName(name: string): string {
  const base = name
    .replace(/\.[^.]+$/, "")
    .replace(/[_-]+/g, " ")
    .trim();
  return /^(img|dsc|image|foto|photo|screenshot|captura)?[\s\d]*$/i.test(base) ? "" : base.charAt(0).toUpperCase() + base.slice(1);
}

/**
 * Uploader lets people pick or drop images, asks for the alt text of each
 * (it is required) and uploads them one by one, reporting each error in place.
 */
export function Uploader({ onUploaded, compact }: { onUploaded: (m: MediaItem[]) => void; compact?: boolean }) {
  const [items, setItems] = useState<Pending[]>([]);
  const [over, setOver] = useState(false);
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const qc = useQueryClient();

  const addFiles = (files: FileList | File[]) => {
    const next: Pending[] = [];
    for (const f of Array.from(files)) {
      const p: Pending = { key: `${f.name}-${f.size}-${f.lastModified}-${Math.random()}`, file: f, alt: altFromName(f.name), preview: "" };
      if (!ACCEPT.split(",").includes(f.type)) p.error = "Solo se aceptan imágenes JPG, PNG, WebP o GIF.";
      else if (f.size > MAX_UPLOAD) p.error = `Pesa ${bytes(f.size)}; el máximo es 10 MB.`;
      else p.preview = URL.createObjectURL(f);
      next.push(p);
    }
    setItems((cur) => [...cur, ...next].slice(0, 30));
  };
  useEffect(() => () => items.forEach((i) => i.preview && URL.revokeObjectURL(i.preview)), []); // eslint-disable-line react-hooks/exhaustive-deps

  const ready = items.filter((i) => !i.error || i.preview);
  const missingAlt = ready.some((i) => i.preview && !i.alt.trim());

  async function uploadAll() {
    setBusy(true);
    const done: MediaItem[] = [];
    let dup = 0;
    for (const it of items) {
      if (!it.preview || !it.alt.trim()) continue;
      setItems((cur) => cur.map((x) => (x.key === it.key ? { ...x, busy: true, error: undefined } : x)));
      try {
        const r = await cat.uploadMedia(it.file, it.alt.trim());
        done.push(r.media);
        if (r.duplicate) dup++;
        URL.revokeObjectURL(it.preview);
        setItems((cur) => cur.filter((x) => x.key !== it.key));
      } catch (e) {
        const fe = fieldErrors(e);
        setItems((cur) => cur.map((x) => (x.key === it.key ? { ...x, busy: false, error: fe.alt ?? fe.file ?? errorMessage(e) } : x)));
      }
    }
    setBusy(false);
    if (done.length) {
      void qc.invalidateQueries({ queryKey: ["media"] });
      void qc.invalidateQueries({ queryKey: ["catalog-summary"] });
      toast.success(
        done.length === 1 ? "Imagen subida" : `${done.length} imágenes subidas`,
        dup ? { description: `${dup} ya estaba${dup === 1 ? "" : "n"} en la biblioteca y se reutiliz${dup === 1 ? "ó" : "aron"}.` } : undefined,
      );
      onUploaded(done);
    }
  }

  return (
    <div className="grid gap-3">
      <div
        onDragOver={(e) => {
          e.preventDefault();
          setOver(true);
        }}
        onDragLeave={() => setOver(false)}
        onDrop={(e) => {
          e.preventDefault();
          setOver(false);
          addFiles(e.dataTransfer.files);
        }}
        className={cn(
          "grid justify-items-center gap-2 rounded-xl border-2 border-dashed px-4 text-center transition-colors",
          compact ? "py-5" : "py-8",
          over ? "border-primary bg-accent" : "border-border bg-card",
        )}
      >
        <Upload className="size-6 text-muted-foreground" aria-hidden="true" />
        <p className="text-sm">
          Arrastra imágenes aquí o{" "}
          <button type="button" className="font-medium text-primary underline-offset-4 hover:underline" onClick={() => input.current?.click()}>
            elige archivos
          </button>
        </p>
        <p className="text-xs text-muted-foreground">JPG, PNG, WebP o GIF · hasta 10 MB cada una</p>
        <input
          ref={input}
          type="file"
          accept={ACCEPT}
          multiple
          className="sr-only"
          tabIndex={-1}
          aria-hidden="true"
          onChange={(e) => {
            if (e.target.files) addFiles(e.target.files);
            e.target.value = "";
          }}
        />
      </div>
      {items.length > 0 && (
        <ul className="grid gap-2">
          {items.map((it) => (
            <li key={it.key} className="grid grid-cols-[56px_1fr_auto] items-start gap-3 rounded-lg border bg-card p-2">
              {it.preview ? (
                <img src={it.preview} alt="" className="size-14 rounded-md border object-cover" />
              ) : (
                <span className="grid size-14 place-items-center rounded-md border bg-muted text-muted-foreground">
                  <ImageIcon className="size-5" />
                </span>
              )}
              <div className="grid min-w-0 gap-1">
                <p className="truncate text-xs text-muted-foreground">
                  {it.file.name} · {bytes(it.file.size)}
                </p>
                {it.preview && (
                  <>
                    <Label htmlFor={`alt-${it.key}`} className="sr-only">
                      Texto alternativo de {it.file.name}
                    </Label>
                    <Input
                      id={`alt-${it.key}`}
                      value={it.alt}
                      maxLength={300}
                      disabled={it.busy}
                      placeholder="Describe la imagen (obligatorio)"
                      aria-invalid={!it.alt.trim() || undefined}
                      onChange={(e) => setItems((cur) => cur.map((x) => (x.key === it.key ? { ...x, alt: e.target.value } : x)))}
                    />
                  </>
                )}
                {it.error && (
                  <p className="text-xs font-medium text-destructive" role="alert">
                    {it.error}
                  </p>
                )}
                {it.busy && <p className="text-xs text-muted-foreground">Subiendo y generando tamaños…</p>}
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={`Quitar ${it.file.name}`}
                disabled={it.busy}
                onClick={() => {
                  if (it.preview) URL.revokeObjectURL(it.preview);
                  setItems((cur) => cur.filter((x) => x.key !== it.key));
                }}
              >
                <X />
              </Button>
            </li>
          ))}
        </ul>
      )}
      {ready.some((i) => i.preview) && (
        <div className="flex flex-wrap items-center gap-3">
          <Button type="button" onClick={() => void uploadAll()} disabled={busy || missingAlt}>
            <Upload /> {busy ? "Subiendo…" : `Subir ${ready.filter((i) => i.preview).length === 1 ? "imagen" : "imágenes"}`}
          </Button>
          {missingAlt && <p className="text-xs text-muted-foreground">Describe cada imagen: lo leen los lectores de pantalla y los buscadores.</p>}
        </div>
      )}
    </div>
  );
}

/** MediaPicker is a dialog to choose images from the library or upload new ones. */
export function MediaPicker({
  open,
  onClose,
  onPick,
  multiple = true,
  exclude = [],
  title = "Elegir imágenes",
}: {
  open: boolean;
  onClose: () => void;
  onPick: (m: MediaItem[]) => void;
  multiple?: boolean;
  exclude?: string[];
  title?: string;
}) {
  const [q, setQ] = useState("");
  const dq = useDebounced(q);
  const [picked, setPicked] = useState<MediaItem[]>([]);
  const list = useInfiniteQuery({
    queryKey: ["media", dq],
    queryFn: ({ pageParam }) => cat.media(dq, pageParam, 36),
    initialPageParam: "",
    getNextPageParam: (l) => l.nextCursor || undefined,
    enabled: open,
  });
  useEffect(() => {
    if (open) {
      setPicked([]);
      setQ("");
    }
  }, [open]);
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const toggle = (m: MediaItem) =>
    setPicked((cur) => (cur.some((x) => x.id === m.id) ? cur.filter((x) => x.id !== m.id) : multiple ? [...cur, m] : [m]));

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="w-[min(860px,calc(100vw-32px))]">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>Elige de la biblioteca o sube imágenes nuevas. Las que subas quedan seleccionadas.</DialogDescription>
        </DialogHeader>
        <Uploader compact onUploaded={(ms) => setPicked((cur) => (multiple ? [...cur, ...ms.filter((m) => !cur.some((x) => x.id === m.id))] : ms.slice(0, 1)))} />
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden="true" />
          <Input type="search" className="pl-8" placeholder="Buscar por nombre o descripción" aria-label="Buscar en la biblioteca" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <ErrorBox error={list.error} onRetry={() => void list.refetch()} />
        {list.isPending ? (
          <div className="grid grid-cols-3 gap-2 sm:grid-cols-5">
            {Array.from({ length: 10 }, (_, i) => (
              <Skeleton key={i} className="aspect-square" />
            ))}
          </div>
        ) : items.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted-foreground">
            {dq ? "Ninguna imagen coincide con la búsqueda." : "La biblioteca está vacía. Sube la primera imagen arriba."}
          </p>
        ) : (
          <ul className="grid max-h-[40vh] grid-cols-3 gap-2 overflow-y-auto p-0.5 sm:grid-cols-5" aria-label="Imágenes de la biblioteca">
            {items.map((m) => {
              const on = picked.some((x) => x.id === m.id);
              const used = exclude.includes(m.id);
              return (
                <li key={m.id}>
                  <button
                    type="button"
                    disabled={used}
                    aria-pressed={on}
                    aria-label={`${m.alt}${used ? " (ya está en el producto)" : ""}`}
                    onClick={() => toggle(m)}
                    className={cn(
                      "relative block aspect-square w-full overflow-hidden rounded-lg border-2 bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                      on ? "border-primary" : "border-transparent",
                      used && "opacity-40",
                    )}
                  >
                    <img src={m.thumb} alt="" loading="lazy" className="size-full object-cover" />
                    {on && (
                      <span className="absolute right-1 top-1 grid size-5 place-items-center rounded-full bg-primary text-primary-foreground">
                        <Check className="size-3.5" />
                      </span>
                    )}
                  </button>
                </li>
              );
            })}
          </ul>
        )}
        {list.hasNextPage && (
          <Button type="button" variant="outline" size="sm" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>
            {list.isFetchingNextPage ? "Cargando…" : "Cargar más"}
          </Button>
        )}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            Cancelar
          </Button>
          <Button
            type="button"
            disabled={picked.length === 0}
            onClick={() => {
              onPick(picked);
              onClose();
            }}
          >
            <ImagePlus /> {picked.length > 1 ? `Usar ${picked.length} imágenes` : "Usar imagen"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** FieldRow is a labelled control for places where FormField's cloning does not fit (custom widgets). */
export function Field({ label, htmlFor, hint, error, children, className }: { label: string; htmlFor?: string; hint?: ReactNode; error?: string; children: ReactNode; className?: string }) {
  // The control itself says it is wrong, so screen readers and the save
  // button's "go to the first error" find it.
  useEffect(() => {
    const el = htmlFor ? document.getElementById(htmlFor) : null;
    if (!el) return;
    if (error) el.setAttribute("aria-invalid", "true");
    else el.removeAttribute("aria-invalid");
  }, [htmlFor, error]);
  return (
    <div className={cn("grid gap-1.5", className)}>
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {error ? (
        <p className="text-xs font-medium text-destructive" role="alert">
          {error}
        </p>
      ) : (
        hint && <p className="text-xs text-muted-foreground">{hint}</p>
      )}
    </div>
  );
}

export function slug(s: string): string {
  return s
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}
