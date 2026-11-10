import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, errorMessage, fieldErrors, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/sonner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { can } from "@/perms";
import { ErrorBox, Notice, Page, Secret } from "./common";

export function Payments({ me }: { me: Me }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["payments"], queryFn: api.paymentAccount });
  const isAdmin = can(me, "payments", "admin");
  const [mode, setMode] = useState<"connect" | "own_keys">("connect");
  const [accountId, setAccountId] = useState("");
  const [pk, setPk] = useState("");
  const [sk, setSk] = useState("");
  const [wh, setWh] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [err, setErr] = useState<string | null>(null);
  const webhookUrl = `${window.location.origin}/v1/stripe/webhook/${encodeURIComponent(me.tenant)}`;

  const save = useMutation({
    mutationFn: () =>
      api.savePaymentAccount(
        mode === "connect"
          ? { mode, accountId: accountId.trim(), webhookSecret: wh.trim() || undefined }
          : { mode, publishableKey: pk.trim(), secretKey: sk.trim(), webhookSecret: wh.trim() || undefined },
      ),
    onSuccess: (a) => {
      qc.setQueryData(["payments"], a);
      setSk("");
      setWh("");
      toast.success("Cuenta de Stripe guardada");
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      setErr(errorMessage(e));
    },
  });

  function submit() {
    setErr(null);
    const e: Record<string, string> = {};
    if (mode === "connect" && !/^acct_\w+$/.test(accountId.trim())) e.accountId = "Usa el id que empieza con acct_ que te da Stripe Connect.";
    if (mode === "own_keys") {
      if (!/^(sk|rk)_/.test(sk.trim())) e.secretKey = "Pega tu llave secreta (sk_…) o restringida (rk_…).";
      if (pk.trim() && !pk.trim().startsWith("pk_")) e.publishableKey = "La llave publicable empieza con pk_.";
    }
    if (wh.trim() && !wh.trim().startsWith("whsec_")) e.webhookSecret = "El secreto del webhook empieza con whsec_.";
    setErrors(e);
    if (Object.keys(e).length === 0) save.mutate();
  }

  const a = q.data;
  return (
    <Page title="Pagos" sub="Cada tienda cobra con su propia cuenta de Stripe. Apexx nunca ve los datos de las tarjetas.">
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      <Card>
        <CardHeader>
          <CardTitle>Cuenta conectada</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-2 text-sm">
          {q.isPending ? (
            <p className="text-muted-foreground">Cargando…</p>
          ) : a?.connected ? (
            <>
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant="ok">Conectada</Badge>
                <span>{a.mode === "connect" ? "Stripe Connect" : "Llaves propias de Stripe"}</span>
              </div>
              {a.accountId && <span className="font-mono text-xs">Cuenta: {a.accountId}</span>}
              {a.publishableKey && <span className="font-mono text-xs">Llave publicable: {a.publishableKey}</span>}
              <span>Llave secreta: {a.hasSecretKey ? "guardada y cifrada" : a.mode === "connect" ? "no se necesita" : "falta"}</span>
              <span>Secreto del webhook: {a.hasWebhookSecret ? "guardado y cifrado" : "no configurado"}</span>
            </>
          ) : (
            <Notice tone="warn">Esta tienda todavía no conectó Stripe: los compradores no pueden pagar.</Notice>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Webhook de Stripe</CardTitle>
          <CardDescription>
            En el panel de Stripe crea un endpoint con esta URL y los eventos payment_intent.succeeded y payment_intent.payment_failed.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Secret label="URL del endpoint" value={webhookUrl} />
        </CardContent>
      </Card>

      {isAdmin ? (
        <Card>
          <CardHeader>
            <CardTitle>{a?.connected ? "Cambiar la cuenta" : "Conectar Stripe"}</CardTitle>
            <CardDescription>Las llaves se guardan cifradas con la llave de tu tienda y nunca se vuelven a mostrar.</CardDescription>
          </CardHeader>
          <CardContent>
            <form
              className="grid gap-4"
              onSubmit={(e) => {
                e.preventDefault();
                submit();
              }}
              noValidate
            >
              <Tabs value={mode} onValueChange={(v) => setMode(v as "connect" | "own_keys")}>
                <TabsList>
                  <TabsTrigger value="connect">Conectar con Stripe (Connect)</TabsTrigger>
                  <TabsTrigger value="own_keys">Usar mis llaves de Stripe</TabsTrigger>
                </TabsList>
                <TabsContent value="connect" className="grid gap-3">
                  <p className="text-sm text-muted-foreground">Recomendado. Autorizas tu cuenta existente en Stripe y Apexx solo guarda su identificador.</p>
                  <FormField id="pay-acct" label="Id de la cuenta conectada" required error={errors.accountId} description="Empieza con acct_.">
                    <Input value={accountId} onChange={(e) => setAccountId(e.target.value)} autoComplete="off" />
                  </FormField>
                </TabsContent>
                <TabsContent value="own_keys" className="grid gap-3">
                  <FormField id="pay-pk" label="Llave publicable" error={errors.publishableKey} description="Empieza con pk_. La usa el checkout en el navegador.">
                    <Input value={pk} onChange={(e) => setPk(e.target.value)} autoComplete="off" />
                  </FormField>
                  <FormField id="pay-sk" label="Llave secreta o restringida" required error={errors.secretKey} description="Empieza con sk_ o rk_. Se cifra al guardarla.">
                    <Input type="password" value={sk} onChange={(e) => setSk(e.target.value)} autoComplete="off" />
                  </FormField>
                </TabsContent>
              </Tabs>
              <FormField id="pay-wh" label="Secreto del webhook (opcional)" error={errors.webhookSecret} description="Empieza con whsec_. Stripe lo muestra al crear el endpoint.">
                <Input type="password" value={wh} onChange={(e) => setWh(e.target.value)} autoComplete="off" />
              </FormField>
              {err && <Notice tone="bad">{err}</Notice>}
              <div>
                <Button type="submit" disabled={save.isPending}>
                  {save.isPending ? "Guardando…" : "Guardar cuenta de Stripe"}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      ) : (
        <p className="text-sm text-muted-foreground">Para cambiar la cuenta de Stripe necesitas nivel Admin en Pagos.</p>
      )}
    </Page>
  );
}
