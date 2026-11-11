import { useState } from "react";
import { FlaskConical } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { promoApi, type Simulation } from "@/promoApi";
import { ErrorBox, Notice } from "../common";
import { CartEditor, PromoOutcome, SimulationTotals, type CartItem } from "./CartEditor";
import { ChipInput, DateTimeField, EmptyState, Field } from "./shared";

/** Simulator answers "what would this cart pay, and why?" for any moment in time. */
export function Simulator() {
  const [items, setItems] = useState<CartItem[]>([]);
  const [coupons, setCoupons] = useState<string[]>([]);
  const [postalCode, setPostalCode] = useState("53129");
  const [email, setEmail] = useState("");
  const [at, setAt] = useState<string | undefined>();
  const [sim, setSim] = useState<{ result: Simulation; atText: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  async function run() {
    setBusy(true);
    setError(null);
    try {
      setSim(await promoApi.simulate({ channel: "mx", postalCode, items: items.map((i) => ({ sku: i.sku, qty: i.qty })), coupons, email: email || undefined, at }));
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }
  const applied = sim?.result.promotions.filter((p) => p.applied) ?? [];
  const skipped = sim?.result.promotions.filter((p) => !p.applied) ?? [];
  return (
    <div className="grid gap-5 lg:grid-cols-[minmax(0,420px)_minmax(0,1fr)]">
      <Card className="content-start">
        <CardHeader>
          <CardTitle className="text-base">Carrito de prueba</CardTitle>
          <CardDescription>Arma un carrito como lo haría un comprador y mira exactamente qué promociones aplican y por qué.</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4">
          <CartEditor items={items} onChange={setItems} />
          <Field label="Cupones"><ChipInput values={coupons} onChange={(v) => setCoupons(v.map((c) => c.toUpperCase()))} placeholder="Escribe un código y Enter" /></Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Código postal"><Input aria-label="Código postal" inputMode="numeric" maxLength={5} value={postalCode} onChange={(e) => setPostalCode(e.target.value)} /></Field>
            <Field label="Correo del cliente" hint="Para límites por cliente."><Input aria-label="Correo del cliente" type="email" value={email} onChange={(e) => setEmail(e.target.value)} /></Field>
          </div>
          <Field label="Fecha y hora" hint="Vacío = ahora. Útil para probar una promoción programada."><DateTimeField value={at} onChange={setAt} /></Field>
          <Button onClick={() => void run()} disabled={busy || items.length === 0}><FlaskConical aria-hidden="true" /> {busy ? "Calculando…" : "Simular"}</Button>
        </CardContent>
      </Card>
      <div className="grid content-start gap-4">
        <ErrorBox error={error} />
        {!sim ? (
          <EmptyState icon={<FlaskConical />} title="Sin simulación todavía" text="Agrega productos al carrito de prueba y presiona Simular." />
        ) : (
          <>
            <Card>
              <CardHeader><CardTitle className="text-base">Resultado</CardTitle><CardDescription>Calculado para {sim.atText}.</CardDescription></CardHeader>
              <CardContent className="grid gap-3">
                {sim.result.messages.map((m) => <Notice key={m.code + (m.sku ?? "")} tone="warn">{m.text}</Notice>)}
                {sim.result.coupons.map((c) => <Notice key={c.code} tone={c.applied ? "ok" : "bad"}><strong className="font-mono">{c.code}</strong>: {c.reason}</Notice>)}
                <SimulationTotals sim={sim.result} />
              </CardContent>
            </Card>
            <Card>
              <CardHeader><CardTitle className="text-base">Promociones que aplican ({applied.length})</CardTitle></CardHeader>
              <CardContent className="grid gap-2">{applied.length ? applied.map((r) => <PromoOutcome key={r.id} r={r} link />) : <p className="text-sm text-muted-foreground">Ninguna.</p>}</CardContent>
            </Card>
            <Card>
              <CardHeader><CardTitle className="text-base">Promociones que no aplican ({skipped.length})</CardTitle><CardDescription>Con la razón exacta, la misma que ve el comprador.</CardDescription></CardHeader>
              <CardContent className="grid gap-2">{skipped.length ? skipped.map((r) => <PromoOutcome key={r.id} r={r} link />) : <p className="text-sm text-muted-foreground">Ninguna.</p>}</CardContent>
            </Card>
          </>
        )}
      </div>
    </div>
  );
}
