import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { LogOut, Search, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Toaster, toast } from "@/components/ui/sonner";

// Superadmin: the platform's operators set each store's plan (how many
// sandboxes, how big). Its own sign-in and session: a store's never works here.

interface Plan {
  plan: string;
  sandboxes: number;
  sandboxMaxProducts: number;
  sandboxDemoData: boolean;
  sandboxSchedule: boolean;
}
interface SandboxUsage { id: string; name: string; scope: "full" | "light"; status: string; products: number; sizeMB: number }
interface Store {
  id: string;
  name: string;
  createdAt: string;
  plan: Plan;
  members: number;
  products: number;
  sizeMB: number;
  sandboxes: SandboxUsage[];
}
interface Operator { id: string; email: string; name: string }
interface LogEntry { at: string; operator: string; action: string; target: string; data: Record<string, unknown> }

class OpError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

/** A request of the superadmin: its own cookie, never a store's key. */
async function op<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new OpError(res.status, (data as { detail?: string }).detail ?? `Error ${res.status}`);
  return data as T;
}

const num = (n: number) => n.toLocaleString("es-MX");
const mb = (n: number) => (n >= 1024 ? `${(n / 1024).toFixed(1)} GB` : `${n.toFixed(1)} MB`);
const when = (iso: string) => new Date(iso).toLocaleString("es-MX", { dateStyle: "medium", timeStyle: "short" });
const ACTIONS: Record<string, string> = { login: "Entró", "login.failed": "Intento fallido", "plan.change": "Cambió el plan" };

export function Superadmin() {
  const me = useQuery({ queryKey: ["op", "me"], queryFn: () => op<{ operator: Operator }>("GET", "/v1/operator/me").then((r) => r.operator), retry: false });
  return (
    <div className="min-h-screen bg-background text-foreground">
      <Toaster />
      {me.isPending ? (
        <p className="p-6 text-sm text-muted-foreground" role="status">
          Cargando…
        </p>
      ) : me.data ? (
        <Console operator={me.data} />
      ) : (
        <SignIn />
      )}
    </div>
  );
}

function SignIn() {
  const qc = useQueryClient();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await op("POST", "/v1/operator/login", { email, password, code });
      await qc.invalidateQueries({ queryKey: ["op"] });
    } catch (err) {
      setError(err instanceof Error ? err.message : "No se pudo entrar");
      setCode("");
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="mx-auto grid min-h-screen max-w-sm content-center gap-6 p-4">
      <div className="grid gap-1">
        <p className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
          <ShieldCheck className="size-4" aria-hidden="true" /> Apexx · Superadmin
        </p>
        <h1 className="text-2xl font-semibold">Operadores de la plataforma</h1>
        <p className="text-sm text-muted-foreground">Para el equipo de Apexx. Las cuentas de las tiendas no entran aquí.</p>
      </div>
      <form className="grid gap-4" onSubmit={(e) => void submit(e)}>
        <div className="grid gap-1.5">
          <Label htmlFor="op-email">Correo</Label>
          <Input id="op-email" type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} />
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="op-password">Contraseña</Label>
          <Input id="op-password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="op-code">Código de verificación</Label>
          <Input
            id="op-code"
            inputMode="numeric"
            autoComplete="one-time-code"
            pattern="[0-9 ]{6,7}"
            required
            value={code}
            onChange={(e) => setCode(e.target.value)}
            aria-describedby="op-code-hint"
          />
          <span id="op-code-hint" className="text-xs text-muted-foreground">
            Los 6 dígitos de tu app de autenticación.
          </span>
        </div>
        {error && (
          <p className="text-sm text-bad" role="alert">
            {error}
          </p>
        )}
        <Button type="submit" disabled={busy}>
          {busy ? "Entrando…" : "Entrar"}
        </Button>
      </form>
    </main>
  );
}

function Console({ operator }: { operator: Operator }) {
  const qc = useQueryClient();
  const [q, setQ] = useState("");
  const [term, setTerm] = useState("");
  const [editing, setEditing] = useState("");
  const stores = useQuery({ queryKey: ["op", "stores", term], queryFn: () => op<{ items: Store[]; defaultPlan: Plan }>("GET", `/v1/operator/stores?q=${encodeURIComponent(term)}`) });
  const log = useQuery({ queryKey: ["op", "log"], queryFn: () => op<{ items: LogEntry[] }>("GET", "/v1/operator/log").then((r) => r.items) });
  async function signOut() {
    await op("POST", "/v1/operator/logout").catch(() => undefined);
    qc.removeQueries({ queryKey: ["op"] });
    await qc.invalidateQueries({ queryKey: ["op"] });
  }
  const totalSandboxes = stores.data?.items.reduce((n, s) => n + s.sandboxes.length, 0) ?? 0;
  const totalMB = stores.data?.items.reduce((n, s) => n + s.sandboxes.reduce((m, x) => m + x.sizeMB, 0), 0) ?? 0;
  return (
    <div className="mx-auto grid max-w-6xl gap-6 p-4 md:p-8">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <p className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
            <ShieldCheck className="size-4" aria-hidden="true" /> Apexx · Superadmin
          </p>
          <h1 className="text-2xl font-semibold">Tiendas y planes</h1>
        </div>
        <div className="flex items-center gap-3 text-sm">
          <span className="text-muted-foreground">{operator.email}</span>
          <Button variant="outline" size="sm" onClick={() => void signOut()}>
            <LogOut className="size-4" /> Cerrar sesión
          </Button>
        </div>
      </header>

      <p className="text-sm text-muted-foreground">
        Cada tienda tiene 1 sandbox incluido. Aquí habilitas más sandboxes (por ejemplo, los que contrató) y el máximo de productos por sandbox, que es lo que ocupa disco. Las personas del equipo son ilimitadas en todas las tiendas. Cada cambio queda en la actividad de la tienda y en el registro de operadores.
      </p>

      <Card>
        <CardHeader>
          <CardTitle>Tiendas</CardTitle>
          <CardDescription>
            {stores.data ? `${num(stores.data.items.length)} tiendas · ${num(totalSandboxes)} sandboxes · ${mb(totalMB)} en sandboxes (estimado)` : "Cargando…"}
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4">
          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              setTerm(q.trim());
            }}
          >
            <div className="grid min-w-0 flex-1 gap-1.5">
              <Label htmlFor="op-q">Buscar tienda</Label>
              <Input id="op-q" value={q} placeholder="Nombre o id" onChange={(e) => setQ(e.target.value)} />
            </div>
            <Button type="submit" variant="outline">
              <Search className="size-4" /> Buscar
            </Button>
          </form>
          {stores.error ? (
            <p className="text-sm text-bad" role="alert">
              {stores.error.message}
            </p>
          ) : !stores.data ? (
            <p className="text-sm text-muted-foreground" role="status">
              Cargando tiendas…
            </p>
          ) : (
            <ul className="grid gap-3">
              {stores.data.items.map((s) => (
                <li key={s.id} className="rounded-lg border p-4">
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="grid gap-0.5">
                      <span className="font-medium">{s.name}</span>
                      <span className="text-xs text-muted-foreground">
                        {s.id} · desde {when(s.createdAt)} · {num(s.members)} personas · {num(s.products)} productos · {mb(s.sizeMB)}
                      </span>
                    </div>
                    <div className="flex flex-wrap items-center gap-2 text-sm">
                      <span className="rounded-full bg-muted px-2 py-0.5">{s.plan.plan || "Plan base"}</span>
                      <span>
                        {s.sandboxes.length} de {s.plan.sandboxes} sandboxes
                      </span>
                      <Button size="sm" variant={editing === s.id ? "default" : "outline"} aria-expanded={editing === s.id} onClick={() => setEditing(editing === s.id ? "" : s.id)}>
                        Cambiar plan
                      </Button>
                    </div>
                  </div>
                  {s.sandboxes.length > 0 && (
                    <table className="mt-3 w-full text-sm">
                      <caption className="sr-only">Sandboxes de {s.name}</caption>
                      <thead className="text-xs text-muted-foreground">
                        <tr>
                          <th scope="col" className="py-1 text-left font-medium">
                            Sandbox
                          </th>
                          <th scope="col" className="py-1 text-left font-medium">
                            Tipo
                          </th>
                          <th scope="col" className="py-1 text-right font-medium">
                            Productos
                          </th>
                          <th scope="col" className="py-1 text-right font-medium">
                            Tamaño
                          </th>
                        </tr>
                      </thead>
                      <tbody>
                        {s.sandboxes.map((x) => (
                          <tr key={x.id} className="border-t">
                            <td className="py-1">
                              {x.name}
                              {x.status !== "ready" && <span className="text-xs text-muted-foreground"> · {x.status === "copying" ? "copiando" : x.status === "failed" ? "copia fallida" : "borrando"}</span>}
                            </td>
                            <td className="py-1">{x.scope === "light" ? "Ligero" : "Completo"}</td>
                            <td className="py-1 text-right tabular-nums">{num(x.products)}</td>
                            <td className="py-1 text-right tabular-nums">{mb(x.sizeMB)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  )}
                  {editing === s.id && (
                    <PlanForm
                      store={s}
                      onDone={() => {
                        setEditing("");
                        void qc.invalidateQueries({ queryKey: ["op"] });
                      }}
                    />
                  )}
                </li>
              ))}
              {stores.data.items.length === 0 && <li className="text-sm text-muted-foreground">Ninguna tienda con esa búsqueda.</li>}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Registro de operadores</CardTitle>
          <CardDescription>Entradas, intentos fallidos y cambios de plan.</CardDescription>
        </CardHeader>
        <CardContent>
          {!log.data?.length ? (
            <p className="text-sm text-muted-foreground">Sin movimientos.</p>
          ) : (
            <ul className="grid gap-1 text-sm">
              {log.data.slice(0, 30).map((l, i) => (
                <li key={i} className="flex flex-wrap justify-between gap-2 border-t py-1">
                  <span>
                    {ACTIONS[l.action] ?? l.action}
                    {l.target ? ` · ${l.target}` : ""}
                    {l.operator ? ` · ${l.operator}` : ""}
                  </span>
                  <span className="text-muted-foreground">{when(l.at)}</span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function PlanForm({ store, onDone }: { store: Store; onDone: () => void }) {
  const [p, setP] = useState<Plan>(store.plan);
  const [busy, setBusy] = useState(false);
  const id = (f: string) => `plan-${store.id}-${f}`;
  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await op("PUT", `/v1/operator/stores/${encodeURIComponent(store.id)}/plan`, p);
      toast.success(`Plan de ${store.name} guardado`);
      onDone();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "No se pudo guardar");
    } finally {
      setBusy(false);
    }
  }
  return (
    <form className="mt-4 grid gap-4 rounded-md bg-muted/50 p-4" onSubmit={(e) => void save(e)}>
      <div className="grid gap-4 sm:grid-cols-3">
        <div className="grid gap-1.5">
          <Label htmlFor={id("name")}>Nombre del plan</Label>
          <Input id={id("name")} maxLength={40} value={p.plan} placeholder="Por ejemplo: pro" onChange={(e) => setP({ ...p, plan: e.target.value })} />
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor={id("sbx")}>Sandboxes</Label>
          <Input id={id("sbx")} type="number" min={0} max={50} required value={p.sandboxes} onChange={(e) => setP({ ...p, sandboxes: Number(e.target.value) })} aria-describedby={id("sbx-hint")} />
          <span id={id("sbx-hint")} className="text-xs text-muted-foreground">
            1 incluido; más si los contrató. 0 quita el sandbox.
          </span>
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor={id("max")}>Productos por sandbox</Label>
          <Input id={id("max")} type="number" min={0} required value={p.sandboxMaxProducts} onChange={(e) => setP({ ...p, sandboxMaxProducts: Number(e.target.value) })} aria-describedby={id("max-hint")} />
          <span id={id("max-hint")} className="text-xs text-muted-foreground">
            0: sin límite. Limita el disco que ocupa cada copia.
          </span>
        </div>
      </div>
      <div className="flex flex-wrap gap-4 text-sm">
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={p.sandboxDemoData} onChange={(e) => setP({ ...p, sandboxDemoData: e.target.checked })} />
          Datos de prueba (clientes y pedidos generados)
        </label>
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={p.sandboxSchedule} onChange={(e) => setP({ ...p, sandboxSchedule: e.target.checked })} />
          Publicaciones programadas
        </label>
      </div>
      {p.sandboxes < store.sandboxes.length && (
        <p className="text-sm text-warn" role="status">
          La tienda ya tiene {store.sandboxes.length} sandboxes: se quedan, pero no podrá crear más hasta tener menos de {p.sandboxes}.
        </p>
      )}
      <div className="flex gap-2">
        <Button type="submit" disabled={busy}>
          {busy ? "Guardando…" : "Guardar plan"}
        </Button>
        <Button type="button" variant="ghost" onClick={onDone}>
          Cancelar
        </Button>
      </div>
    </form>
  );
}
