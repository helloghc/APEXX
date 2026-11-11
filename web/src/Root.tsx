import { useQuery, useQueryClient } from "@tanstack/react-query";
import { lazy, Suspense, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Boxes, Cable, CreditCard, Home as HomeIcon, LayoutTemplate, LogOut, Receipt, Users, BadgePercent, Contact, Mails, Settings2, Activity, BarChart3, SearchCheck, Gift, Repeat } from "lucide-react";
import { api, ApiError, credentials, errorMessage, UNAUTHORIZED_EVENT, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader } from "@/components/ui/card";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Toaster } from "@/components/ui/sonner";
import { Logo } from "@/components/Logo";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar";
import { Notice, Page } from "@/pages/common";
import { Home } from "@/pages/Home";

// Each section loads on demand so the login screen and home stay light.
const Editor = lazy(() => import("@/App").then((m) => ({ default: m.Editor })));
const Catalog = lazy(() => import("@/pages/catalog/index").then((m) => ({ default: m.Catalog })));
const Promotions = lazy(() => import("@/pages/promotions/index").then((m) => ({ default: m.Promotions })));
const Orders = lazy(() => import("@/pages/orders/index").then((m) => ({ default: m.Orders })));
const Customers = lazy(() => import("@/pages/customers/index").then((m) => ({ default: m.Customers })));
const Notifications = lazy(() => import("@/pages/notifications/index").then((m) => ({ default: m.Notifications })));
const Team = lazy(() => import("@/pages/Team").then((m) => ({ default: m.Team })));
const Payments = lazy(() => import("@/pages/Payments").then((m) => ({ default: m.Payments })));
const Engage = lazy(() => import("@/pages/engage/index").then((m) => ({ default: m.Engage })));
const Wallet = lazy(() => import("@/pages/wallet/index").then((m) => ({ default: m.Wallet })));
const Subscriptions = lazy(() => import("@/pages/subscriptions/index").then((m) => ({ default: m.Subscriptions })));
const Account = lazy(() => import("@/pages/account/index").then((m) => ({ default: m.Account })));
const AcceptInvite = lazy(() => import("@/pages/account/index").then((m) => ({ default: m.AcceptInvite })));
const Settings = lazy(() => import("@/pages/settings/Settings").then((m) => ({ default: m.Settings })));
const Reports = lazy(() => import("@/pages/reports/Reports").then((m) => ({ default: m.Reports })));
const Ops = lazy(() => import("@/pages/reports/Ops").then((m) => ({ default: m.Ops })));
const Connectors = lazy(() => import("@/pages/Connectors").then((m) => ({ default: m.Connectors })));
import { visibleNav } from "@/perms";
import { href, navigate, useRoute } from "@/router";

const LAST_STORE = "apexx.lastStore";

function lastStore(): string {
  const q = new URLSearchParams(window.location.search).get("store");
  if (q) return q;
  try {
    return window.localStorage.getItem(LAST_STORE) ?? "";
  } catch {
    return "";
  }
}

/** Root checks the session (cookie) and shows the login screen or the admin shell. */
export function Root() {
  const qc = useQueryClient();
  const meQ = useQuery({ queryKey: ["me"], queryFn: api.me, retry: false, staleTime: 60_000 });
  const [invited, setInvited] = useState(false);
  const topRoute = useRoute(); // re-renders when the address changes, so an invitation link works in an open tab

  useEffect(() => {
    const on = () => {
      qc.setQueryData(["me"], null);
      void qc.invalidateQueries({ queryKey: ["me"] });
    };
    window.addEventListener(UNAUTHORIZED_EVENT, on);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, on);
  }, [qc]);

  let body: ReactNode;
  if (meQ.isPending) {
    body = <main className="grid min-h-full place-items-center text-muted-foreground">Cargando…</main>;
  } else {
    const unauth = meQ.error instanceof ApiError && meQ.error.status === 401;
    const hash = topRoute;
    if (hash[0] === "invitacion" && hash[1] && !invited) {
      return (
        <>
          <Suspense fallback={null}>
            <AcceptInvite
              token={hash[1]}
              onDone={(st) => {
                if (st) {
                  try {
                    window.localStorage.setItem(LAST_STORE, st);
                  } catch {
                    /* storage blocked */
                  }
                }
                window.location.hash = "";
                setInvited(true);
                // A person accepting an invitation must not stay signed in as someone else.
                const out = meQ.data ? api.logout().catch(() => undefined) : Promise.resolve();
                void out.finally(() => { qc.setQueryData(["me"], null); void qc.invalidateQueries({ queryKey: ["me"] }); });
              }}
            />
          </Suspense>
          <Toaster />
        </>
      );
    }
    body =
      !meQ.data || unauth ? (
        <Login
          error={meQ.error && !unauth ? meQ.error : null}
          onDone={() => {
            // Drop everything cached for the previous user, then ask who is logged in now.
            qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "me" });
            void qc.refetchQueries({ queryKey: ["me"] });
          }}
        />
      ) : (
        <Shell me={meQ.data} />
      );
  }
  return (
    <>
      {body}
      <Toaster />
    </>
  );
}

function Login({ onDone, error }: { onDone: () => void; error: unknown }) {
  const [store, setStore] = useState(lastStore);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [needCode, setNeedCode] = useState(false);
  const [code, setCode] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    setErr(null);
    if (!store.trim() || !email.trim() || !password) {
      setErr("Completa la tienda, tu correo y tu contraseña.");
      return;
    }
    setBusy(true);
    try {
      credentials.save("");
      await api.login(store.trim(), email.trim(), password, needCode ? code.trim() : undefined);
      try {
        window.localStorage.setItem(LAST_STORE, store.trim());
      } catch {
        /* storage blocked */
      }
      onDone();
    } catch (e2) {
      if (e2 instanceof ApiError && e2.problem.code === "totp_required") {
        setNeedCode(true);
        return;
      }
      setErr(
        e2 instanceof ApiError && e2.problem.code === "invalid_code"
          ? "El código no es correcto. Usa el código actual de tu app o un código de respaldo."
          : e2 instanceof ApiError && e2.status === 401
          ? "Correo, contraseña o tienda incorrectos."
          : e2 instanceof ApiError && e2.status === 429
            ? "Demasiados intentos. Espera un minuto y vuelve a intentar."
            : errorMessage(e2),
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="grid min-h-full place-items-center px-4 py-8" style={{ background: "radial-gradient(900px 500px at 50% -10%, var(--accent-soft), transparent 70%), var(--bg)" }}>
      <Card className="w-full max-w-sm" style={{ boxShadow: "var(--shadow-2)" }}>
        <CardHeader>
          <Logo size={32} className="mb-3" />
          <h1 className="text-2xl font-semibold leading-tight tracking-tight">Inicia sesión</h1>
          <CardDescription>Administra tu tienda: catálogo, pedidos, equipo y diseño.</CardDescription>
        </CardHeader>
        <CardContent>
          <form className="grid gap-4" onSubmit={(e) => void submit(e)} noValidate>
            {Boolean(error) && <Notice tone="bad">{errorMessage(error)}</Notice>}
            <FormField id="login-store" label="Tienda" description="El identificador de tu tienda, por ejemplo mi-tienda.">
              <Input value={store} onChange={(e) => setStore(e.target.value)} autoComplete="organization" />
            </FormField>
            <FormField id="login-email" label="Correo">
              <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="username" />
            </FormField>
            <FormField id="login-password" label="Contraseña">
              <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
            </FormField>
            {needCode && (
              <FormField id="login-code" label="Código de verificación" description="El código de 6 dígitos de tu app, o uno de tus códigos de respaldo.">
                <Input value={code} onChange={(e) => setCode(e.target.value)} autoComplete="one-time-code" inputMode="text" autoFocus className="font-mono tracking-widest" />
              </FormField>
            )}
            {err && <Notice tone="bad">{err}</Notice>}
            <Button type="submit" disabled={busy}>
              {busy ? "Entrando…" : "Entrar"}
            </Button>
            <details className="text-sm">
              <summary className="cursor-pointer text-muted-foreground">Entrar con una API key</summary>
              <div className="mt-3 grid gap-3">
                <FormField id="login-key" label="API key" description="Para integraciones y pruebas. Se guarda solo en esta pestaña.">
                  <Input type="password" autoComplete="off" value={key} onChange={(e) => setKey(e.target.value)} />
                </FormField>
                <Button
                  type="button"
                  variant="outline"
                  disabled={!key.trim()}
                  onClick={() => {
                    credentials.save(key.trim());
                    onDone();
                  }}
                >
                  Usar API key
                </Button>
              </div>
            </details>
          </form>
        </CardContent>
      </Card>
    </main>
  );
}

const ICONS: Record<string, typeof HomeIcon> = {
  inicio: HomeIcon,
  builder: LayoutTemplate,
  catalogo: Boxes,
  promociones: BadgePercent,
  pedidos: Receipt,
  clientes: Contact,
  correos: Mails,
  equipo: Users,
  pagos: CreditCard,
  busqueda: SearchCheck,
  tarjetas: Gift,
  suscripciones: Repeat,
  configuracion: Settings2,
  conectores: Cable,
  reportes: BarChart3,
  operacion: Activity,
};

function Shell({ me }: { me: Me }) {
  const qc = useQueryClient();
  const route = useRoute();
  const nav = visibleNav(me);
  const section = route[0] ?? "inicio";
  // "cuenta" is every member's own security page; it needs no module permission.
  const active = section === "cuenta" || nav.find((n) => n.route === section) ? section : null;

  async function logout() {
    try {
      await api.logout();
    } catch {
      /* the session may already be gone */
    }
    credentials.save("");
    qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "me" });
    qc.setQueryData(["me"], null);
    navigate("inicio");
  }

  const params = new URLSearchParams(window.location.search);
  const site = route[1] ?? params.get("site") ?? "main";
  const pageHandle = route[2] ?? params.get("page") ?? "index";

  let body: ReactNode;
  let full = false;
  switch (active) {
    case "builder":
      full = true;
      body = <Editor key={site + "/" + pageHandle} site={site} handle={pageHandle} me={me} />;
      break;
    case "catalogo":
      body = <Catalog me={me} route={route} />;
      break;
    case "promociones":
      body = <Promotions me={me} route={route} />;
      break;
    case "pedidos":
      body = <Orders me={me} route={route} />;
      break;
    case "clientes":
      body = <Customers me={me} route={route} />;
      break;
    case "correos":
      body = <Notifications me={me} route={route} />;
      break;
    case "equipo":
      body = <Team me={me} />;
      break;
    case "pagos":
      body = <Payments me={me} />;
      break;
    case "busqueda":
      body = <Engage me={me} route={route} />;
      break;
    case "tarjetas":
      body = <Wallet me={me} route={route} />;
      break;
    case "suscripciones":
      body = <Subscriptions me={me} route={route} />;
      break;
    case "cuenta":
      body = <Account me={me} />;
      break;
    case "configuracion":
      body = <Settings me={me} sub={route[1]} rest={route.slice(2)} />;
      break;
    case "conectores":
      body = <Connectors me={me} />;
      break;
    case "reportes":
      body = <Reports me={me} report={route[1]} />;
      break;
    case "operacion":
      body = <Ops me={me} view={route[1]} />;
      break;
    case "inicio":
      body = <Home me={me} />;
      break;
    default:
      body = (
        <Page title="Sin acceso" sub="Tu acceso no incluye esta sección.">
          <p>
            Pide a un administrador de la tienda que te dé permiso, o vuelve al{" "}
            <a className="text-primary underline" href={href("inicio")}>
              inicio
            </a>
            .
          </p>
        </Page>
      );
  }

  return (
    <SidebarProvider>
      <Sidebar label="Menú principal">
        <SidebarHeader>
          <Logo size={26} />
          <span className="text-xs text-muted-foreground">Tienda {me.tenant}</span>
        </SidebarHeader>
        <SidebarContent>
          <nav aria-label="Secciones">
            <SidebarMenu>
              {nav.map((n) => {
                const Icon = ICONS[n.route] ?? HomeIcon;
                return (
                  <SidebarMenuItem key={n.route}>
                    <SidebarMenuButton asChild isActive={n.route === section}>
                      <a href={href(n.route)}>
                        <Icon aria-hidden="true" />
                        {n.label}
                      </a>
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                );
              })}
            </SidebarMenu>
          </nav>
        </SidebarContent>
        <SidebarFooter>
          <div className="grid gap-0.5 text-sm">
            <strong data-testid="who" className="break-words">
              {me.name || me.email || me.id}
            </strong>
            <span className="break-words text-xs text-muted-foreground">
              {me.owner ? "Dueño" : me.kind === "api_key" ? "API key" : "Miembro"} · {me.email ?? me.tenant}
            </span>
          </div>
          {me.kind === "member" && (
            <a href={href("cuenta")} className="text-sm text-primary hover:underline">
              Mi cuenta y seguridad
            </a>
          )}
          <Button variant="outline" size="sm" onClick={() => void logout()}>
            <LogOut /> Cerrar sesión
          </Button>
        </SidebarFooter>
      </Sidebar>
      <SidebarInset>
        <header className="flex items-center gap-2 border-b bg-card px-2 py-1 md:hidden">
          <SidebarTrigger />
          <Logo size={22} />
          <span className="truncate text-xs text-muted-foreground">{me.tenant}</span>
        </header>
        <div className={full ? "min-h-0 flex-1 overflow-hidden" : "min-h-0 flex-1 overflow-auto"}>
          <Suspense fallback={<p className="p-6 text-sm text-muted-foreground">Cargando…</p>}>{body}</Suspense>
        </div>
      </SidebarInset>
    </SidebarProvider>
  );
}
