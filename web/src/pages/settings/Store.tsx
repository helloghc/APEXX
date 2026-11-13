import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { ExternalLink, ImageUp, Trash2 } from "lucide-react";
import { errorMessage, fieldErrors, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { toast } from "@/components/ui/sonner";
import { Textarea } from "@/components/ui/textarea";
import { can } from "@/perms";
import { ErrorBox } from "../common";
import { sapi, type StateInfo, type StoreSettings } from "./api";
import { Confirm, Loading, ReadOnlyNote, SaveBar, Section, SubPage } from "./kit";
import { validateRfc } from "./logic";

const SOCIAL_LABEL: Record<string, { label: string; placeholder: string }> = {
  facebook: { label: "Facebook", placeholder: "https://facebook.com/mitienda" },
  instagram: { label: "Instagram", placeholder: "https://instagram.com/mitienda" },
  tiktok: { label: "TikTok", placeholder: "https://tiktok.com/@mitienda" },
  x: { label: "X", placeholder: "https://x.com/mitienda" },
  youtube: { label: "YouTube", placeholder: "https://youtube.com/@mitienda" },
  whatsapp: { label: "WhatsApp", placeholder: "https://wa.me/5215555550100" },
};

const TIMEZONES = [
  { id: "America/Mexico_City", label: "Centro (Ciudad de México)" },
  { id: "America/Cancun", label: "Sureste (Cancún)" },
  { id: "America/Chihuahua", label: "Pacífico (Chihuahua)" },
  { id: "America/Mazatlan", label: "Pacífico (Mazatlán)" },
  { id: "America/Hermosillo", label: "Sonora (Hermosillo)" },
  { id: "America/Tijuana", label: "Noroeste (Tijuana)" },
  { id: "America/Monterrey", label: "Centro (Monterrey)" },
  { id: "America/Bogota", label: "Colombia (Bogotá)" },
  { id: "America/Lima", label: "Perú (Lima)" },
  { id: "America/Santiago", label: "Chile (Santiago)" },
  { id: "America/Argentina/Buenos_Aires", label: "Argentina (Buenos Aires)" },
  { id: "UTC", label: "UTC" },
];

export function StorePage({ me }: { me: Me }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["settings", "store"], queryFn: sapi.settings });
  const canEdit = can(me, "settings", "admin");
  const [form, setForm] = useState<StoreSettings | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [dropLogo, setDropLogo] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (q.data && !form) setForm(structuredClone(q.data.settings));
  }, [q.data, form]);

  const dirty = Boolean(form && q.data && JSON.stringify(form) !== JSON.stringify(q.data.settings));

  const apply = (s: StoreSettings) => {
    qc.setQueryData(["settings", "store"], (old: typeof q.data) => (old ? { ...old, settings: s } : old));
    setForm(structuredClone(s));
  };

  const save = useMutation({
    mutationFn: () => sapi.saveSettings(form as StoreSettings),
    onSuccess: (r) => {
      apply(r.settings);
      setErrors({});
      toast.success("Datos de la tienda guardados");
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      toast.error(errorMessage(e));
    },
  });

  const upload = useMutation({
    mutationFn: (f: File) => sapi.uploadLogo(f),
    onSuccess: (r) => {
      // Only the logo changes; keep whatever the person is typing in the form.
      qc.setQueryData(["settings", "store"], (old: typeof q.data) => (old ? { ...old, settings: { ...old.settings, logoUrl: r.settings.logoUrl } } : old));
      setForm((f) => (f ? { ...f, logoUrl: r.settings.logoUrl } : f));
      toast.success("Logo actualizado");
    },
    onError: (e) => toast.error(errorMessage(e)),
  });

  const removeLogo = useMutation({
    mutationFn: sapi.deleteLogo,
    onSuccess: () => {
      qc.setQueryData(["settings", "store"], (old: typeof q.data) => (old ? { ...old, settings: { ...old.settings, logoUrl: "" } } : old));
      setForm((f) => (f ? { ...f, logoUrl: "" } : f));
      setDropLogo(false);
      toast.success("Logo eliminado");
    },
    onError: (e) => toast.error(errorMessage(e)),
  });

  function submit() {
    if (!form) return;
    const e: Record<string, string> = {};
    if (!form.name.trim()) e.name = "Escribe el nombre de la tienda.";
    const rfc = validateRfc(form.rfc);
    if (rfc) e.rfc = rfc;
    if (form.email && !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(form.email.trim())) e.email = "Escribe un correo válido.";
    const a = form.address;
    if ((a.line1 || a.city || a.postalCode) && !/^\d{5}$/.test(a.postalCode.trim())) e["address.postalCode"] = "El código postal tiene 5 dígitos.";
    setErrors(e);
    if (Object.keys(e).length) {
      toast.error("Revisa los campos marcados");
      return;
    }
    save.mutate();
  }

  if (q.isPending || !form)
    return (
      <SubPage title="Tienda">
        <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
        {!q.error && <Loading rows={5} />}
      </SubPage>
    );

  const set = (patch: Partial<StoreSettings>) => setForm({ ...form, ...patch });
  const setAddr = (patch: Partial<StoreSettings["address"]>) => setForm({ ...form, address: { ...form.address, ...patch } });
  const states: StateInfo[] = q.data?.states ?? [];
  const front = q.data?.storefront ?? "";

  return (
    <SubPage
      title="Tienda"
      sub="Los datos que identifican a tu negocio. Aparecen en el pie de la tienda, en correos y en las facturas."
      actions={
        <Button variant="outline" asChild>
          <a href={`${front}/index`} target="_blank" rel="noreferrer">
            <ExternalLink /> Ver tienda
          </a>
        </Button>
      }
    >
      <ReadOnlyNote show={!canEdit} />
      <fieldset disabled={!canEdit} className="grid min-w-0 gap-5">
        <Section title="Identidad" description="El nombre comercial es el que ven tus clientes; la razón social y el RFC van en documentos fiscales.">
          <div className="grid gap-4 md:grid-cols-2">
            <FormField id="st-name" label="Nombre de la tienda" required error={errors.name}>
              <Input value={form.name} maxLength={80} onChange={(e) => set({ name: e.target.value })} />
            </FormField>
            <FormField id="st-legal" label="Razón social" error={errors.legalName}>
              <Input value={form.legalName} maxLength={160} placeholder="Mi Tienda S.A. de C.V." onChange={(e) => set({ legalName: e.target.value })} />
            </FormField>
            <FormField id="st-rfc" label="RFC" error={errors.rfc} description="12 caracteres para empresas, 13 para personas físicas.">
              <Input value={form.rfc} maxLength={13} className="font-mono uppercase" placeholder="XAXX010101000" onChange={(e) => set({ rfc: e.target.value.toUpperCase() })} />
            </FormField>
          </div>
        </Section>

        <Section title="Logo" description="PNG, JPG, WebP o GIF de hasta 512 KB. Se guarda al subirlo.">
          <div className="flex flex-wrap items-center gap-4">
            <div className="grid h-20 w-44 place-items-center overflow-hidden rounded-lg border bg-muted">
              {form.logoUrl ? (
                <img src={form.logoUrl} alt={`Logo de ${form.name}`} className="max-h-full max-w-full object-contain" />
              ) : (
                <span className="px-2 text-center text-xs text-muted-foreground">Sin logo: se muestra el nombre</span>
              )}
            </div>
            <input
              ref={fileRef}
              type="file"
              accept="image/png,image/jpeg,image/webp,image/gif"
              className="sr-only"
              tabIndex={-1}
              aria-hidden="true"
              onChange={(e) => {
                const f = e.target.files?.[0];
                e.target.value = "";
                if (!f) return;
                if (f.size > 512 * 1024) {
                  toast.error("El logo debe pesar menos de 512 KB");
                  return;
                }
                upload.mutate(f);
              }}
            />
            <div className="flex flex-wrap gap-2">
              <Button type="button" variant="outline" disabled={upload.isPending} onClick={() => fileRef.current?.click()}>
                <ImageUp /> {upload.isPending ? "Subiendo…" : form.logoUrl ? "Cambiar logo" : "Subir logo"}
              </Button>
              {form.logoUrl && (
                <Button type="button" variant="ghost" onClick={() => setDropLogo(true)}>
                  <Trash2 /> Quitar
                </Button>
              )}
            </div>
          </div>
        </Section>

        <Section title="Contacto y dirección" description="A dónde escriben o llaman tus clientes, y el domicilio del negocio.">
          <div className="grid gap-4 md:grid-cols-2">
            <FormField id="st-email" label="Correo de contacto" error={errors.email}>
              <Input type="email" value={form.email} placeholder="hola@mitienda.mx" onChange={(e) => set({ email: e.target.value })} />
            </FormField>
            <FormField id="st-phone" label="Teléfono" error={errors.phone}>
              <Input type="tel" value={form.phone} placeholder="55 5555 0100" onChange={(e) => set({ phone: e.target.value })} />
            </FormField>
            <FormField id="st-line1" label="Calle y número" error={errors["address.line1"]} className="md:col-span-2">
              <Input value={form.address.line1} autoComplete="address-line1" onChange={(e) => setAddr({ line1: e.target.value })} />
            </FormField>
            <FormField id="st-line2" label="Colonia o interior (opcional)">
              <Input value={form.address.line2 ?? ""} onChange={(e) => setAddr({ line2: e.target.value })} />
            </FormField>
            <FormField id="st-city" label="Ciudad o municipio" error={errors["address.city"]}>
              <Input value={form.address.city} onChange={(e) => setAddr({ city: e.target.value })} />
            </FormField>
            <FormField id="st-cp" label="Código postal" error={errors["address.postalCode"]}>
              <Input value={form.address.postalCode} inputMode="numeric" maxLength={5} onChange={(e) => setAddr({ postalCode: e.target.value.replace(/\D/g, "") })} />
            </FormField>
            <FormField id="st-state" label="Estado" error={errors["address.state"]} description="Si lo dejas vacío se toma del código postal.">
              <StateSelect value={form.address.state} states={states} onChange={(v) => setAddr({ state: v })} />
            </FormField>
          </div>
        </Section>

        <Section title="Redes sociales" description="Enlaces completos. Los que dejes vacíos no se muestran.">
          <div className="grid gap-4 md:grid-cols-2">
            {(q.data?.socialNetworks ?? []).map((n) => (
              <FormField key={n} id={`st-social-${n}`} label={SOCIAL_LABEL[n]?.label ?? n} error={errors[`social.${n}`]}>
                <Input
                  type="url"
                  value={form.social[n] ?? ""}
                  placeholder={SOCIAL_LABEL[n]?.placeholder}
                  onChange={(e) => set({ social: { ...form.social, [n]: e.target.value } })}
                />
              </FormField>
            ))}
          </div>
        </Section>

        <Section title="Operación" description="La zona horaria define la hora de corte de los envíos; el umbral marca cuándo un producto aparece en alertas de stock bajo.">
          <div className="grid gap-4 md:grid-cols-2">
            <FormField id="st-tz" label="Zona horaria" error={errors.timezone}>
              <Select value={form.timezone} onValueChange={(v) => set({ timezone: v })}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {TIMEZONES.map((t) => (
                    <SelectItem key={t.id} value={t.id}>
                      {t.label}
                    </SelectItem>
                  ))}
                  {!TIMEZONES.some((t) => t.id === form.timezone) && <SelectItem value={form.timezone}>{form.timezone}</SelectItem>}
                </SelectContent>
              </Select>
            </FormField>
            <FormField id="st-low" label="Stock bajo desde" error={errors.lowStockThreshold} description="Unidades disponibles. 0 apaga las alertas por omisión.">
              <Input
                type="number"
                min={0}
                value={form.lowStockThreshold}
                onChange={(e) => set({ lowStockThreshold: Math.max(0, Math.trunc(Number(e.target.value) || 0)) })}
              />
            </FormField>
          </div>
        </Section>

        <Section
          title="Buscadores"
          description={
            <>
              El mapa del sitio se genera solo, con la fecha real del último cambio de cada página.{" "}
              <a className="text-primary underline" href={`${front}/sitemap.xml`} target="_blank" rel="noreferrer">
                Ver sitemap.xml
              </a>{" "}
              ·{" "}
              <a className="text-primary underline" href={`${front}/robots.txt`} target="_blank" rel="noreferrer">
                Ver robots.txt
              </a>
            </>
          }
        >
          <FormField
            id="st-robots"
            label="Reglas adicionales de robots.txt"
            error={errors.robots}
            description="Opcional. Una regla por línea: User-agent, Allow, Disallow o Crawl-delay. El carrito y el pago nunca se indexan."
          >
            <Textarea value={form.robots} rows={4} className="font-mono text-xs" placeholder={"User-agent: GPTBot\nDisallow: /"} onChange={(e) => set({ robots: e.target.value })} />
          </FormField>
        </Section>
      </fieldset>

      <SaveBar dirty={dirty && canEdit} busy={save.isPending} onSave={submit} onDiscard={() => (q.data ? (setForm(structuredClone(q.data.settings)), setErrors({})) : undefined)} />

      <Confirm
        open={dropLogo}
        title="¿Quitar el logo?"
        text="La tienda mostrará el nombre en texto hasta que subas otro."
        confirmLabel="Quitar logo"
        busy={removeLogo.isPending}
        onConfirm={() => removeLogo.mutate()}
        onClose={() => setDropLogo(false)}
      />
    </SubPage>
  );
}

export function StateSelect({
  value,
  states,
  onChange,
  ...rest
}: {
  value: string;
  states: StateInfo[];
  onChange: (v: string) => void;
  id?: string;
  "aria-invalid"?: boolean;
  "aria-describedby"?: string;
}) {
  return (
    <Select value={value || "_"} onValueChange={(v) => onChange(v === "_" ? "" : v)}>
      <SelectTrigger {...rest}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="_">Según el código postal</SelectItem>
        {states.map((s) => (
          <SelectItem key={s.code} value={s.code}>
            {s.name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
