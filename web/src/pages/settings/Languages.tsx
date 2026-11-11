import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { errorMessage, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { sapi, type LanguageConfig } from "./api";
import { Loading, ReadOnlyNote, Section, SubPage, Switch } from "./kit";

/** Languages: which languages the storefront offers and the store's own translations. */
export function Languages({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "settings", "admin");
  const q = useQuery({ queryKey: ["settings", "languages"], queryFn: sapi.languages });
  const [cfg, setCfg] = useState<LanguageConfig | null>(null);
  const [lang, setLang] = useState("");
  const [rows, setRows] = useState<{ es: string; tr: string }[]>([]);
  useEffect(() => {
    if (q.data && !cfg) {
      setCfg(q.data.config);
      setLang(q.data.config.enabled[0] ?? "");
    }
  }, [q.data, cfg]);
  useEffect(() => {
    if (cfg && lang) setRows(Object.entries(cfg.own[lang] ?? {}).map(([es, tr]) => ({ es, tr })));
  }, [lang]); // eslint-disable-line react-hooks/exhaustive-deps
  const save = useMutation({
    mutationFn: (c: LanguageConfig) => sapi.saveLanguages(c),
    onSuccess: (r) => {
      setCfg(r.config);
      toast.success("Idiomas guardados.");
      void qc.invalidateQueries({ queryKey: ["settings", "languages"] });
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  if (q.error) return <ErrorBox error={q.error} onRetry={() => void q.refetch()} />;
  if (!q.data || !cfg) return <Loading />;
  const others = q.data.available.filter((l) => l.code !== q.data.default);
  const withRows = (): LanguageConfig => {
    if (!lang) return cfg;
    const own = { ...cfg.own, [lang]: Object.fromEntries(rows.filter((r) => r.es.trim() && r.tr.trim()).map((r) => [r.es.trim(), r.tr.trim()])) };
    return { ...cfg, own };
  };
  const toggle = (code: string, on: boolean) => {
    const enabled = on ? [...cfg.enabled, code] : cfg.enabled.filter((c) => c !== code);
    setCfg({ ...withRows(), enabled });
    if (on && !lang) setLang(code);
    if (!on && lang === code) setLang(enabled[0] ?? "");
  };
  return (
    <SubPage
      title="Idiomas"
      sub="Tu tienda está escrita en español. Activa otros idiomas y Apexx traduce todos los textos fijos (botones, carrito, pago, cuenta). Cada idioma tiene su propia dirección para Google."
      actions={canEdit && <Button disabled={save.isPending} onClick={() => save.mutate(withRows())}>Guardar</Button>}
    >
      <ReadOnlyNote show={!canEdit} need="Administrador" />
      <Section title="Idiomas de la tienda">
        <ul className="grid gap-2 sm:grid-cols-2">
          <li className="flex items-center justify-between gap-3 rounded-md border p-3">
            <span>Español <span className="text-sm text-muted-foreground">· idioma principal</span></span>
            <Switch label="Español" checked disabled onChange={() => undefined} />
          </li>
          {others.map((l) => (
            <li key={l.code} className="flex items-center justify-between gap-3 rounded-md border p-3">
              <span lang={l.tag}>{l.name}</span>
              <Switch label={`Activar ${l.name}`} checked={cfg.enabled.includes(l.code)} disabled={!canEdit} onChange={(v) => toggle(l.code, v)} />
            </li>
          ))}
        </ul>
      </Section>
      {cfg.enabled.length > 0 && (
        <Section title="Tus propias traducciones">
          <p className="mb-3 text-sm text-muted-foreground">
            Los textos que tú escribes (nombres de productos, menús, títulos de secciones) no se traducen solos. Escribe aquí el texto tal como aparece en español y cómo debe verse en el otro idioma. También sirve para cambiar una traducción de Apexx.
          </p>
          <div className="mb-3 flex flex-wrap gap-2" role="tablist" aria-label="Idioma a traducir">
            {cfg.enabled.map((c) => (
              <Button key={c} type="button" size="sm" role="tab" aria-selected={lang === c} variant={lang === c ? "default" : "outline"} onClick={() => { setCfg(withRows()); setLang(c); }}>
                {q.data.available.find((l) => l.code === c)?.name ?? c}
              </Button>
            ))}
          </div>
          <div className="grid gap-2">
            {rows.length === 0 && <p className="text-sm text-muted-foreground">Todavía no agregas traducciones propias en este idioma.</p>}
            {rows.map((r, i) => (
              <div key={i} className="grid gap-2 sm:grid-cols-[1fr_1fr_auto]">
                <Input aria-label={`Texto en español ${i + 1}`} placeholder="Texto en español" value={r.es} disabled={!canEdit} onChange={(e) => setRows(rows.map((x, j) => (j === i ? { ...x, es: e.target.value } : x)))} />
                <Input aria-label={`Traducción ${i + 1}`} placeholder="Traducción" value={r.tr} disabled={!canEdit} onChange={(e) => setRows(rows.map((x, j) => (j === i ? { ...x, tr: e.target.value } : x)))} />
                {canEdit && <Button type="button" variant="ghost" size="icon" aria-label={`Quitar traducción ${i + 1}`} onClick={() => setRows(rows.filter((_, j) => j !== i))}><Trash2 aria-hidden="true" /></Button>}
              </div>
            ))}
            {canEdit && <div><Button type="button" variant="outline" size="sm" onClick={() => setRows([...rows, { es: "", tr: "" }])}><Plus aria-hidden="true" /> Agregar traducción</Button></div>}
          </div>
        </Section>
      )}
    </SubPage>
  );
}
