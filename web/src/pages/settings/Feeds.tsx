import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Copy, RefreshCw } from "lucide-react";
import { errorMessage, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { ErrorBox, Notice } from "../common";
import { Confirm, Loading, ReadOnlyNote, SaveBar, Section, SubPage, SwitchRow } from "./kit";

interface FeedConfig { enabled: boolean; key: string; googleCategory: string; condition: string; onlyInStock: boolean }
interface Feeds { config: FeedConfig; urls: { google?: string; meta?: string } }
const api = {
  get: () => request<Feeds>("GET", "/v1/feeds"),
  save: (c: FeedConfig) => request<Feeds>("PUT", "/v1/feeds", c),
  rotate: () => request<Feeds>("POST", "/v1/feeds/rotate", {}),
};

/** Feeds: the catalogue as Google Merchant Center and Meta expect it. */
export function Feeds({ me }: { me: Me }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["settings", "feeds"], queryFn: api.get });
  const canEdit = can(me, "catalog", "admin");
  const [c, setC] = useState<FeedConfig | null>(null);
  const [rotate, setRotate] = useState(false);
  useEffect(() => { if (q.data && !c) setC(q.data.config); }, [q.data, c]);
  const done = (r: Feeds, msg: string) => { qc.setQueryData(["settings", "feeds"], r); setC(r.config); toast.success(msg); };
  const save = useMutation({ mutationFn: () => api.save(c as FeedConfig), onSuccess: (r) => done(r, "Guardado."), onError: (e) => toast.error(errorMessage(e)) });
  const rot = useMutation({ mutationFn: api.rotate, onSuccess: (r) => { done(r, "Dirección nueva lista. Actualízala en Google y Meta."); setRotate(false); }, onError: (e) => toast.error(errorMessage(e)) });
  if (q.isPending || !c) return <SubPage title="Anuncios y catálogos"><ErrorBox error={q.error} onRetry={() => void q.refetch()} />{!q.error && <Loading />}</SubPage>;
  const urls = q.data?.urls ?? {};
  const row = (label: string, help: string, url?: string) => (
    <div className="grid gap-1.5 rounded-md border p-3">
      <strong className="text-sm">{label}</strong><span className="text-sm text-muted-foreground">{help}</span>
      {url ? <div className="flex items-center gap-2"><Input readOnly value={url} aria-label={`Dirección del catálogo para ${label}`} className="font-mono text-xs" /><Button variant="outline" onClick={() => void navigator.clipboard.writeText(url).then(() => toast.success("Dirección copiada."))}><Copy aria-hidden="true" /> Copiar</Button></div>
        : <span className="text-sm text-muted-foreground">Activa y guarda para obtener la dirección.</span>}
    </div>
  );
  return (
    <SubPage title="Anuncios y catálogos" sub="Tu catálogo siempre al día en Google Shopping y en Facebook e Instagram: precios, existencias e imágenes se leen de tu tienda, sin subir archivos.">
      <ReadOnlyNote show={!canEdit} />
      <fieldset disabled={!canEdit} className="grid min-w-0 gap-5">
        <Section title="Publicar mi catálogo">
          <div>
            <SwitchRow title="Compartir el catálogo con plataformas de anuncios" text="Crea una dirección privada que Google y Meta consultan cada día." checked={c.enabled} onChange={(v) => setC({ ...c, enabled: v })} />
            <SwitchRow title="Solo productos con existencias" text="Los agotados no se envían, para no pagar anuncios de algo que no puedes vender." checked={c.onlyInStock} onChange={(v) => setC({ ...c, onlyInStock: v })} />
          </div>
          <div className="grid gap-4 md:grid-cols-2">
            <FormField id="fd-cat" label="Categoría de Google (opcional)" description="Por ejemplo: Apparel & Accessories > Clothing. Ayuda a Google a mostrar tus anuncios a quien busca eso.">
              <Input value={c.googleCategory} onChange={(e) => setC({ ...c, googleCategory: e.target.value })} />
            </FormField>
            <div className="grid content-start gap-1.5"><span className="text-sm font-medium">Estado de los productos</span>
              <Select value={c.condition || "new"} onValueChange={(v) => setC({ ...c, condition: v })}><SelectTrigger aria-label="Estado de los productos"><SelectValue /></SelectTrigger>
                <SelectContent><SelectItem value="new">Nuevos</SelectItem><SelectItem value="refurbished">Reacondicionados</SelectItem><SelectItem value="used">Usados</SelectItem></SelectContent></Select>
            </div>
          </div>
        </Section>
        <Section title="Direcciones del catálogo" description="Pégalas una sola vez en cada plataforma, como fuente de datos programada (“scheduled fetch”)."
          actions={canEdit && c.key ? <Button type="button" variant="outline" onClick={() => setRotate(true)}><RefreshCw aria-hidden="true" /> Cambiar direcciones</Button> : undefined}>
          {!c.enabled && c.key && <Notice tone="warn">El catálogo está apagado: las direcciones responden “no encontrado” hasta que lo actives y guardes.</Notice>}
          {row("Google Merchant Center", "Productos › Fuentes de datos › Agregar fuente › Archivo desde una URL.", urls.google)}
          {row("Meta (Facebook e Instagram)", "Commerce Manager › Catálogo › Fuentes de datos › Lista de datos programada.", urls.meta)}
        </Section>
      </fieldset>
      <SaveBar dirty={canEdit && JSON.stringify(c) !== JSON.stringify(q.data?.config)} busy={save.isPending} onSave={() => save.mutate()} onDiscard={() => q.data && setC(q.data.config)} />
      <Confirm open={rotate} title="¿Cambiar las direcciones del catálogo?" text="Las direcciones actuales dejan de funcionar de inmediato. Tendrás que pegar las nuevas en Google y Meta." confirmLabel="Cambiar" busy={rot.isPending} onConfirm={() => rot.mutate()} onClose={() => setRotate(false)} />
    </SubPage>
  );
}
