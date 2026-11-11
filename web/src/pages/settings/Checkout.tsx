import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { errorMessage, fieldErrors, type Me } from "@/api";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { href } from "@/router";
import { ErrorBox, Notice } from "../common";
import { sapi, type CheckoutRules } from "./api";
import { Loading, ReadOnlyNote, SaveBar, Section, SubPage, SwitchRow } from "./kit";

export function Checkout({ me }: { me: Me }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["settings", "store"], queryFn: sapi.settings });
  const pol = useQuery({ queryKey: ["settings", "policies"], queryFn: sapi.policies });
  const canEdit = can(me, "settings", "admin");
  const [rules, setRules] = useState<CheckoutRules | null>(null);
  const [prefix, setPrefix] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    if (q.data && !rules) {
      setRules({ ...q.data.settings.checkout });
      setPrefix(q.data.settings.orderPrefix);
    }
  }, [q.data, rules]);

  const save = useMutation({
    mutationFn: () => sapi.saveCheckout(rules as CheckoutRules, prefix),
    onSuccess: (r) => {
      qc.setQueryData(["settings", "store"], (old: typeof q.data) => (old ? { ...old, settings: r.settings } : old));
      setRules({ ...r.settings.checkout });
      setPrefix(r.settings.orderPrefix);
      setErrors({});
      toast.success("Checkout guardado. Aplica a la siguiente compra.");
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      toast.error(errorMessage(e));
    },
  });

  if (q.isPending || !rules)
    return (
      <SubPage title="Checkout">
        <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
        {!q.error && <Loading />}
      </SubPage>
    );

  const saved = q.data?.settings;
  const dirty = Boolean(saved && (JSON.stringify(rules) !== JSON.stringify(saved.checkout) || prefix !== saved.orderPrefix));
  const published = (pol.data?.items ?? []).filter((p) => p.published);
  const termsReady = published.some((p) => p.slug === rules.termsPolicy);
  const set = (patch: Partial<CheckoutRules>) => setRules({ ...rules, ...patch });

  function submit() {
    const e: Record<string, string> = {};
    if (!/^[A-Z0-9-]{0,8}$/.test(prefix)) e.orderPrefix = "Hasta 8 letras mayúsculas, números o guiones.";
    if (rules?.termsCheckbox && !termsReady) e["checkout.termsPolicy"] = "Elige una política publicada.";
    setErrors(e);
    if (Object.keys(e).length === 0) save.mutate();
  }

  return (
    <SubPage title="Checkout" sub="Qué se le pide al comprador antes de pagar. Los cambios aplican de inmediato, sin publicar la tienda.">
      <ReadOnlyNote show={!canEdit} />
      <fieldset disabled={!canEdit} className="grid min-w-0 gap-5">
        <Section title="Datos del comprador">
          <div>
            <SwitchRow
              title="Comprar sin crear cuenta"
              text="El comprador solo escribe su correo, nombre y dirección. Es la opción que más ventas convierte."
              checked={rules.guestCheckout}
              onChange={(v) => set({ guestCheckout: v })}
            >
              {!rules.guestCheckout && (
                <Notice tone="warn">
                  Con esto apagado la tienda en línea rechaza las compras de visitantes sin cuenta. Los pedidos creados por API o por tu equipo siguen funcionando.
                </Notice>
              )}
            </SwitchRow>
            <SwitchRow
              title="Teléfono obligatorio"
              text="Útil cuando la paquetería necesita llamar para entregar. Si está apagado, el teléfono es opcional."
              checked={rules.phoneRequired}
              onChange={(v) => set({ phoneRequired: v })}
            />
            <SwitchRow
              title="Nota para la tienda"
              text="Muestra un campo de texto libre (hasta 500 caracteres) para instrucciones de entrega o dedicatorias."
              checked={rules.notesField}
              onChange={(v) => set({ notesField: v })}
            />
          </div>
        </Section>

        <Section title="Términos y condiciones">
          <div>
            <SwitchRow
              title="Pedir que acepten los términos"
              text="Agrega una casilla obligatoria con enlace a tu política. La aceptación queda registrada en el historial del pedido."
              checked={rules.termsCheckbox}
              onChange={(v) => set({ termsCheckbox: v })}
            >
              {rules.termsCheckbox && (
                <div className="grid gap-3 md:max-w-sm">
                  <FormField id="co-terms" label="Política que se enlaza" error={errors["checkout.termsPolicy"]}>
                    <Select value={termsReady ? rules.termsPolicy : ""} onValueChange={(v) => set({ termsPolicy: v })}>
                      <SelectTrigger>
                        <SelectValue placeholder="Elige una política publicada" />
                      </SelectTrigger>
                      <SelectContent>
                        {published.map((p) => (
                          <SelectItem key={p.slug} value={p.slug}>
                            {p.title}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </FormField>
                  {published.length === 0 && (
                    <Notice tone="warn">
                      Todavía no tienes políticas publicadas.{" "}
                      <a className="underline" href={href("configuracion", "politicas")}>
                        Escribe tus términos
                      </a>{" "}
                      y vuelve aquí.
                    </Notice>
                  )}
                </div>
              )}
            </SwitchRow>
          </div>
        </Section>

        <Section title="Número de pedido" description="El prefijo se antepone al número consecutivo en la confirmación y en los correos. Los pedidos anteriores conservan su número.">
          <div className="grid gap-4 md:grid-cols-2">
            <FormField id="co-prefix" label="Prefijo" error={errors.orderPrefix} description="Opcional. Hasta 8 caracteres: A-Z, 0-9 y guion.">
              <Input value={prefix} maxLength={8} className="font-mono uppercase" placeholder="MX-" onChange={(e) => setPrefix(e.target.value.toUpperCase())} />
            </FormField>
            <div className="grid content-start gap-1.5">
              <span className="text-sm font-medium">Así se verá</span>
              <span className="flex h-9 items-center rounded-md border bg-muted px-3 font-mono text-sm" data-testid="order-preview">
                #{prefix}1042
              </span>
            </div>
          </div>
        </Section>
      </fieldset>
      <SaveBar
        dirty={dirty && canEdit}
        busy={save.isPending}
        onSave={submit}
        onDiscard={() => {
          if (!saved) return;
          setRules({ ...saved.checkout });
          setPrefix(saved.orderPrefix);
          setErrors({});
        }}
      />
    </SubPage>
  );
}
