import { PANEL_LANGUAGES, currentLanguage, setLanguage } from "./i18n";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { lazy, Suspense, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Boxes, Cable, CreditCard, Home as HomeIcon, LayoutTemplate, LogOut, Receipt, Users, BadgePercent, Contact, Mails, Settings2, Activity, BarChart3, SearchCheck, Gift, Repeat, Store, Building2, Database, Blocks } from "lucide-react";
import { api, ApiError, credentials, errorMessage, request, UNAUTHORIZED_EVENT, type Me } from "@/api";
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
// Every screen of the panel, loaded in the background once the person is in,
// so moving between screens never waits for a download.
const SCREENS = [() => import("@/pages/Connectors"), () => import("@/pages/Payments"), () => import("@/pages/Team"), () => import("@/pages/account/index"), () => import("@/pages/b2b/index"), () => import("@/pages/catalog/index"), () => import("@/pages/customers/index"), () => import("@/pages/engage/index"), () => import("@/pages/marketplace/index"), () => import("@/pages/notifications/index"), () => import("@/pages/orders/index"), () => import("@/pages/promotions/index"), () => import("@/pages/reports/Ops"), () => import("@/pages/reports/Reports"), () => import("@/pages/settings/Settings"), () => import("@/pages/subscriptions/index"), () => import("@/pages/wallet/index"), () => import("@/pages/settings/Fees"), () => import("@/pages/settings/Channels"), () => import("@/pages/settings/Shipping"), () => import("@/pages/settings/Taxes")];
let preloaded = false;
export function preloadScreens() {
  if (preloaded) return;
  preloaded = true;
  const run = () => SCREENS.forEach((load, i) => setTimeout(() => void load().catch(() => {}), i * 40));
  const w = window as Window & { requestIdleCallback?: (cb: () => void) => void };
  if (w.requestIdleCallback) w.requestIdleCallback(run); else setTimeout(run, 300);
}

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
const AppsHub = lazy(() => import("@/pages/apps/index").then((m) => ({ default: m.AppsHub })));
const Marketplace = lazy(() => import("@/pages/marketplace/index").then((m) => ({ default: m.Marketplace })));
const SellerPortal = lazy(() => import("@/pages/marketplace/index").then((m) => ({ default: m.SellerPortal })));
const Companies = lazy(() => import("@/pages/b2b/index").then((m) => ({ default: m.Companies })));
const CustomData = lazy(() => import("@/pages/b2b/index").then((m) => ({ default: m.CustomData })));
const Settings = lazy(() => import("@/pages/settings/Settings").then((m) => ({ default: m.Settings })));
const Reports = lazy(() => import("@/pages/reports/Reports").then((m) => ({ default: m.Reports })));
const Ops = lazy(() => import("@/pages/reports/Ops").then((m) => ({ default: m.Ops })));
const Connectors = lazy(() => import("@/pages/Connectors").then((m) => ({ default: m.Connectors })));
import { visibleNav } from "@/perms";
import { ADMIN_THEMES, applyTheme, currentTheme } from "@/adminThemes";
import { href, navigate, useRoute } from "@/router";

const LAST_STORE = "apexx.lastStore";

/** Another store of the same person: asks the password once and lists only the stores it opens. */
/** Lets each person read the panel in their language. */
function LanguagePicker() {
  return (
    <label className="grid gap-1 text-xs text-muted-foreground" data-no-translate>
      <span>Idioma · Language</span>
      <select className="h-8 rounded-md border bg-card px-2 text-sm text-foreground" value={currentLanguage()} onChange={(e) => setLanguage(e.target.value)} aria-label="Idioma del panel / Panel language">
        {PANEL_LANGUAGES.map((l) => (
          <option key={l.code} value={l.code}>{l.name}</option>
        ))}
      </select>
    </label>
  );
}

function SwitchStore({ email, current }: { email: string; current: string }) {
  const [open, setOpen] = useState(false);
  const [password, setPassword] = useState("");
  const [list, setList] = useState<{ id: string; name: string }[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  async function find(e: FormEvent) {
    e.preventDefault();
    setErr(null);
    setBusy(true);
    try {
      const all = await api.stores(email, password);
      const others = all.filter((x) => x.id !== current);
      if (others.length === 0) setErr("Con este correo y contraseña solo tienes acceso a esta tienda.");
      else setList(others);
    } catch (e2) {
      setErr(e2 instanceof ApiError && e2.status === 401 ? "La contraseña no es correcta." : errorMessage(e2));
    } finally {
      setBusy(false);
    }
  }
  async function enter(id: string) {
    setBusy(true);
    try {
      await api.logout().catch(() => undefined);
      await api.login(id, email, password);
      try {
        window.localStorage.setItem(LAST_STORE, id);
      } catch {
        /* storage blocked */
      }
      window.location.hash = "";
      window.location.reload();
    } catch (e2) {
      setErr(e2 instanceof ApiError && e2.problem.code === "totp_required" ? "Esa tienda pide tu código de verificación: cierra sesión y entra desde el inicio." : errorMessage(e2));
      setBusy(false);
    }
  }
  if (!open) return <button type="button" className="text-left text-sm text-primary hover:underline" onClick={() => setOpen(true)}>Cambiar de tienda</button>;
  return (
    <form className="grid gap-2 rounded-md border bg-card p-2" onSubmit={(e) => void find(e)}>
      {list ? (
        list.map((x) => (
          <Button key={x.id} type="button" size="sm" variant="outline" disabled={busy} onClick={() => void enter(x.id)}>
            {x.name} <span className="font-mono text-xs text-muted-foreground">{x.id}</span>
          </Button>
        ))
      ) : (
        <>
          <label htmlFor="sw-pass" className="text-xs text-muted-foreground">Confirma tu contraseña para ver tus otras tiendas</label>
          <Input id="sw-pass" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
          <Button type="submit" size="sm" disabled={busy || !password}>Ver mis tiendas</Button>
        </>
      )}
      {err && <p className="text-xs text-bad" role="alert">{err}</p>}
      <button type="button" className="text-xs text-muted-foreground hover:underline" onClick={() => { setOpen(false); setList(null); setPassword(""); setErr(null); }}>Cancelar</button>
    </form>
  );
}

function ThemePicker() {
  const [id, setId] = useState(currentTheme);
  return (
    <div role="radiogroup" aria-label="Tema del panel" className="flex items-center gap-2">
      <span className="text-xs text-muted-foreground">Tema</span>
      {ADMIN_THEMES.map((t) => (
        <button
          key={t.id}
          type="button"
          role="radio"
          aria-checked={id === t.id}
          title={t.label}
          onClick={() => {
            applyTheme(t.id);
            setId(t.id);
          }}
          className={`flex h-6 overflow-hidden rounded-full border-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${id === t.id ? "border-foreground" : "border-transparent"}`}
        >
          <span className="sr-only">{t.label}</span>
          {t.palette.slice(0, 3).map((c) => (
            <span key={c} aria-hidden="true" className="h-full w-3" style={{ background: c }} />
          ))}
        </button>
      ))}
    </div>
  );
}

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
  const [choices, setChoices] = useState<{ id: string; name: string }[] | null>(null);
  // Single sign-on: offered when the typed store has it; its result comes back in the hash.
  const [sso, setSso] = useState<{ enabled: boolean; enforce: boolean; name?: string } | null>(null);
  useEffect(() => {
    const m = /[#&]sso=([^&]*)/.exec(window.location.hash);
    if (m && m[1] !== undefined) { setErr(decodeURIComponent(m[1].replace(/\+/g, " "))); history.replaceState(null, "", window.location.pathname); }
  }, []);
  useEffect(() => {
    const t = store.trim();
    if (!/^[a-z0-9][a-z0-9-]{1,62}$/.test(t)) { setSso(null); return; }
    const h = setTimeout(() => { fetch(`/v1/auth/sso/available?store=${encodeURIComponent(t)}`).then((r) => (r.ok ? r.json() : null)).then((x) => setSso(x && x.enabled ? x : null)).catch(() => setSso(null)); }, 300);
    return () => clearTimeout(h);
  }, [store]);

  async function submit(e: FormEvent | null, pick?: string) {
    e?.preventDefault();
    setErr(null);
    if (!email.trim() || !password) {
      setErr("Escribe tu correo y tu contraseña.");
      return;
    }
    setBusy(true);
    try {
      credentials.save("");
      let target = (pick ?? store).trim();
      if (!target) {
        // No store typed: find the stores this person can enter.
        const list = await api.stores(email.trim(), password);
        if (list.length > 1) {
          setChoices(list);
          return;
        }
        target = list[0]?.id ?? "";
        setStore(target);
      }
      await api.login(target, email.trim(), password, needCode ? code.trim() : undefined);
      try {
        window.localStorage.setItem(LAST_STORE, target);
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
          : e2 instanceof ApiError && e2.problem.code === "sso_required"
          ? "Tu tienda usa el inicio de sesión de tu empresa. Usa el botón de abajo."
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
            {choices && (
              <div className="grid gap-2" role="group" aria-label="Elige la tienda">
                <span className="text-sm font-medium">¿A qué tienda quieres entrar?</span>
                {choices.map((c) => (
                  <Button key={c.id} type="button" variant="outline" disabled={busy} onClick={() => { setStore(c.id); void submit(null, c.id); }}>
                    {c.name} <span className="font-mono text-xs text-muted-foreground">{c.id}</span>
                  </Button>
                ))}
              </div>
            )}
            <FormField id="login-store" label="Tienda (opcional)" description="Déjalo vacío y te mostramos tus tiendas. O escribe su identificador, por ejemplo mi-tienda.">
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
            {sso && (
              <Button type="button" variant="outline" data-sso onClick={() => { try { window.localStorage.setItem(LAST_STORE, store.trim()); } catch { /* storage blocked */ } window.location.href = `/v1/auth/sso/start?store=${encodeURIComponent(store.trim())}`; }}>
                Entrar con mi empresa{sso.name ? ` (${sso.name})` : ""}
              </Button>
            )}
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
  marketplace: Store,
  empresas: Building2,
  datos: Database,
  configuracion: Settings2,
  conectores: Cable,
  reportes: BarChart3,
  operacion: Activity,
  apps: Blocks,
};

function Shell({ me }: { me: Me }) {
  useEffect(() => preloadScreens(), []);
  const qc = useQueryClient();
  const route = useRoute();
  // A seller's person only has the seller portal (as "inicio") and their own account.
  const nav = me.sellerId ? [{ route: "inicio", label: "Portal de vendedor" }] : visibleNav(me);
  const section = route[0] ?? "inicio";
  // "cuenta" is every member's own security page; it needs no module permission.
  // Apps that add pages to the panel get their own section.
  const exts = useQuery({ queryKey: ["app-extensions"], queryFn: () => request<{ items: { id: string; adminPage: boolean }[] }>("GET", "/v1/app-extensions").then((r) => r.items), enabled: !me.sellerId && !credentials.key, staleTime: 60_000 });
  if (!me.sellerId && (exts.data ?? []).some((a) => a.adminPage) && !nav.some((n) => n.route === "apps")) nav.push({ route: "apps", label: "Apps" } as (typeof nav)[number]);
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
    case "apps":
      body = <AppsHub route={route} />;
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
    case "marketplace":
      body = <Marketplace me={me} route={route} />;
      break;
    case "empresas":
      body = <Companies me={me} route={route} />;
      break;
    case "datos":
      body = <CustomData me={me} route={route} />;
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
      body = me.sellerId ? <SellerPortal /> : <Home me={me} />;
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
          <ThemePicker />
          <LanguagePicker />
          {me.kind === "member" && <SwitchStore email={me.email ?? ""} current={me.tenant} />}
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
