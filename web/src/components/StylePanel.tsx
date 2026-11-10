import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { AlertTriangle, Check, Palette, RotateCcw } from "lucide-react";
import { api } from "../api";
import { contrastProblems, isHex, resolvedColors, setThemeValue, TOKEN_LABELS } from "../theme";
import type { Edit } from "../patch";
import type { StoreTheme, Template } from "../types";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
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

interface Props {
  open: boolean;
  onClose: () => void;
  site: string;
  template: Template;
  onEdit: (e: Edit | null) => void;
  /** Called after the server replaced the draft (theme switch); returns the revision to undo to. */
  onApplied: (previousRevisionId: string, themeName: string) => void;
}

function Swatches({ t }: { t: StoreTheme }) {
  return (
    <div className="flex gap-1" aria-hidden="true">
      {["bg", "surface", "text", "primary", "accent", "sale"].map((k) => (
        <span key={k} className="size-5 rounded-full border border-black/10" style={{ background: t.colors[k] }} />
      ))}
    </div>
  );
}

/** StylePanel lets a merchant pick one of the store themes and fine-tune colors, fonts and radius. */
export function StylePanel(p: Props) {
  const q = useQuery({ queryKey: ["themes"], queryFn: api.themes, enabled: p.open });
  const [confirm, setConfirm] = useState<{ theme: StoreTheme; keep: boolean } | null>(null);
  const [busy, setBusy] = useState(false);
  const themes = q.data?.items ?? [];
  const fonts = q.data?.fonts ?? [];
  const currentId = p.template.theme?.id ?? "lienzo";
  const base = themes.find((t) => t.id === currentId);
  const colors = resolvedColors(base, p.template.theme);
  const problems = contrastProblems(colors, q.data?.contrastPairs ?? []);
  const fontFamily = (id: string) => {
    const f = fonts.find((x) => x.id === id);
    return f ? `"${f.family}", ${f.fallback}` : undefined;
  };
  const display = p.template.theme?.display || base?.display || "";
  const body = p.template.theme?.body || base?.body || "";
  const radius = p.template.theme?.radius ?? base?.radius ?? 0;
  const edit = (path: string, v: string | number | undefined) => p.onEdit(setThemeValue(p.template, currentId, path, v));

  async function apply() {
    if (!confirm) return;
    setBusy(true);
    try {
      const r = await api.applyTheme(p.site, confirm.theme.id, confirm.keep);
      p.onApplied(r.previousRevisionId, confirm.theme.name);
      setConfirm(null);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Sheet open={p.open} onOpenChange={(o) => !o && p.onClose()}>
      <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-xl">
        <SheetHeader>
          <SheetTitle className="flex items-center gap-2">
            <Palette className="size-5" /> Estilo de la tienda
          </SheetTitle>
          <SheetDescription>Elige un tema y ajústalo a tu marca. Los cambios se ven en la vista previa y salen al publicar.</SheetDescription>
        </SheetHeader>
        {q.isLoading && <p className="text-sm text-muted-foreground">Cargando temas…</p>}
        {q.data && (
          <Tabs defaultValue="themes">
            <TabsList>
              <TabsTrigger value="themes">Temas</TabsTrigger>
              <TabsTrigger value="style">Colores y fuentes</TabsTrigger>
            </TabsList>

            <TabsContent value="themes" className="mt-4 grid gap-3">
              {themes.map((t) => (
                <article
                  key={t.id}
                  className={`rounded-lg border p-4 ${t.id === currentId ? "border-primary ring-1 ring-primary" : ""}`}
                  style={{ background: t.colors.bg, color: t.colors.text }}
                  aria-label={`Tema ${t.name}`}
                >
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <p style={{ fontFamily: fontFamily(t.display), fontSize: "1.8rem", lineHeight: 1.1 }}>{t.name}</p>
                      <p className="text-xs uppercase tracking-wider" style={{ color: t.colors.muted }}>{t.vertical}</p>
                    </div>
                    {t.id === currentId && (
                      <span className="inline-flex items-center gap-1 rounded-full px-2 py-1 text-xs font-semibold" style={{ background: t.colors.primary, color: t.colors.primaryText }}>
                        <Check className="size-3" /> En uso
                      </span>
                    )}
                  </div>
                  <p className="mt-2 text-sm" style={{ fontFamily: fontFamily(t.body), color: t.colors.muted }}>{t.description}</p>
                  <div className="mt-3 flex flex-wrap items-center justify-between gap-3">
                    <Swatches t={t} />
                    <div className="flex gap-2">
                      {t.id !== currentId && (
                        <Button size="sm" variant="outline" onClick={() => setConfirm({ theme: t, keep: true })}>
                          Solo cambiar el estilo
                        </Button>
                      )}
                      <Button size="sm" onClick={() => setConfirm({ theme: t, keep: false })}>
                        {t.id === currentId ? "Restablecer plantilla" : "Usar plantilla completa"}
                      </Button>
                    </div>
                  </div>
                </article>
              ))}
            </TabsContent>

            <TabsContent value="style" className="mt-4 grid gap-5">
              {problems.length > 0 && (
                <div role="alert" className="flex gap-2 rounded-md border border-bad bg-bad-soft p-3 text-sm text-bad">
                  <AlertTriangle className="mt-0.5 size-4 shrink-0" />
                  <div>
                    <p className="font-semibold">Algunos textos no se leerían bien. No podrás publicar hasta corregirlo:</p>
                    <ul className="mt-1 list-disc pl-4">
                      {problems.map((x) => (
                        <li key={x.fg + x.bg}>
                          {TOKEN_LABELS[x.fg]} sobre {TOKEN_LABELS[x.bg]?.toLowerCase()}: {x.ratio.toFixed(2)}:1 (mínimo 4.5:1)
                        </li>
                      ))}
                    </ul>
                  </div>
                </div>
              )}
              <fieldset className="grid gap-3">
                <legend className="mb-2 text-sm font-semibold">Colores</legend>
                {(q.data.tokens ?? []).map((k) => {
                  const v = colors[k] ?? "#000000";
                  const overridden = !!p.template.theme?.colors?.[k];
                  return (
                    <div key={k} className="flex items-center gap-3">
                      <input
                        id={`c-${k}`}
                        type="color"
                        value={v}
                        onChange={(e) => edit(`colors/${k}`, e.target.value.toUpperCase())}
                        className="h-9 w-12 cursor-pointer rounded border bg-transparent"
                      />
                      <label htmlFor={`c-${k}`} className="flex-1 text-sm">
                        {TOKEN_LABELS[k] ?? k}
                      </label>
                      <input
                        aria-label={`${TOKEN_LABELS[k] ?? k} en hexadecimal`}
                        defaultValue={v}
                        key={v}
                        onBlur={(e) => isHex(e.target.value) && edit(`colors/${k}`, e.target.value.toUpperCase())}
                        className="w-24 rounded border bg-background px-2 py-1 font-mono text-xs"
                      />
                      <Button size="icon" variant="ghost" disabled={!overridden} onClick={() => edit(`colors/${k}`, undefined)} title="Volver al color del tema" aria-label={`Restablecer ${TOKEN_LABELS[k] ?? k}`}>
                        <RotateCcw className="size-4" />
                      </Button>
                    </div>
                  );
                })}
              </fieldset>
              <fieldset className="grid gap-3">
                <legend className="mb-2 text-sm font-semibold">Tipografía</legend>
                {([
                  ["display", "Títulos", display],
                  ["body", "Texto", body],
                ] as const).map(([key, label, value]) => (
                  <label key={key} className="grid gap-1 text-sm">
                    {label}
                    <select
                      value={value}
                      onChange={(e) => edit(key, e.target.value)}
                      className="h-9 rounded-md border bg-background px-2"
                      style={{ fontFamily: fontFamily(value) }}
                    >
                      {fonts.map((f) => (
                        <option key={f.id} value={f.id}>{f.family}</option>
                      ))}
                    </select>
                  </label>
                ))}
                <p className="rounded-md border p-3" style={{ fontFamily: fontFamily(display), fontSize: "1.6rem", background: colors.bg, color: colors.text }}>
                  Prendas que duran
                  <span className="mt-1 block text-sm" style={{ fontFamily: fontFamily(body), color: colors.muted }}>
                    Así se verán los títulos y el texto de tu tienda.
                  </span>
                </p>
              </fieldset>
              <label className="grid gap-1 text-sm">
                Redondeo de esquinas: {radius} px
                <input type="range" min={0} max={32} value={radius} onChange={(e) => edit("radius", Number(e.target.value))} />
              </label>
            </TabsContent>
          </Tabs>
        )}
      </SheetContent>

      <AlertDialog open={!!confirm} onOpenChange={(o) => !o && setConfirm(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirm?.keep ? `Cambiar al estilo ${confirm?.theme.name}` : `Usar la plantilla ${confirm?.theme.name}`}</AlertDialogTitle>
            <AlertDialogDescription>
              {confirm?.keep
                ? "Tus secciones y textos se quedan igual; cambian colores, fuentes y formas."
                : "La página de inicio se reemplaza por la plantilla del tema. Guardamos tu versión actual para que puedas volver a ella."}{" "}
              La tienda en vivo no cambia hasta que publiques.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>Cancelar</AlertDialogCancel>
            <AlertDialogAction disabled={busy} onClick={(e) => { e.preventDefault(); void apply(); }}>
              {busy ? "Aplicando…" : "Aplicar"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Sheet>
  );
}
