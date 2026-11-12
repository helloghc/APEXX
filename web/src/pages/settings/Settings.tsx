import { Suspense, useEffect } from "react";
import { ArrowLeftRight, Building2, CornerUpRight, FileText, Globe, ListTree, MapPin, Megaphone, Percent, ShoppingBag, Store, Truck, type LucideIcon, Receipt, Undo2, Languages as LanguagesIcon, Copy, ListPlus, Blocks, Workflow, Filter, Cookie, Layers, KeyRound, SquarePlus, Award, Gift, Newspaper, ShoppingCart, LayoutGrid, CalendarClock, MailPlus, Target } from "lucide-react";
import type { Me } from "@/api";
import { cn } from "@/lib/utils";
import { href } from "@/router";
import { Loading } from "./kit";
import { lazyPage, whenIdle } from "@/lib/lazy";

const StorePage = lazyPage(() => import("./Store").then((m) => m.StorePage));
const Locations = lazyPage(() => import("./Locations").then((m) => m.Locations));
const Shipping = lazyPage(() => import("./Shipping").then((m) => m.Shipping));
const Taxes = lazyPage(() => import("./Taxes").then((m) => m.Taxes));
const Channels = lazyPage(() => import("./Channels").then((m) => m.Channels));
const Policies = lazyPage(() => import("./Policies").then((m) => m.Policies));
const Navigation = lazyPage(() => import("./Navigation").then((m) => m.Navigation));
const Redirects = lazyPage(() => import("./Redirects").then((m) => m.Redirects));
const Domains = lazyPage(() => import("./Domains").then((m) => m.Domains));
const Feeds = lazyPage(() => import("./Feeds").then((m) => m.Feeds));
const Languages = lazyPage(() => import("./Languages").then((m) => m.Languages));
const Fields = lazyPage(() => import("./Fields").then((m) => m.Fields));
const CatalogSource = lazyPage(() => import("./CatalogSource").then((m) => m.CatalogSource));
const ReturnsPolicy = lazyPage(() => import("./ReturnsPolicy").then((m) => m.ReturnsPolicy));
const Fees = lazyPage(() => import("./Fees").then((m) => m.Fees));
const AdConversions = lazyPage(() => import("./AdConversions").then((m) => m.AdConversions));
const MailCampaigns = lazyPage(() => import("./MailCampaigns").then((m) => m.MailCampaigns));
const DesignSchedules = lazyPage(() => import("./DesignSchedules").then((m) => m.DesignSchedules));
const Blog = lazyPage(() => import("./Blog").then((m) => m.Blog));
const Storefronts = lazyPage(() => import("./Storefronts").then((m) => m.Storefronts));
const SalesChannels = lazyPage(() => import("./SalesChannels").then((m) => m.SalesChannels));
const Gifts = lazyPage(() => import("./Gifts").then((m) => m.Gifts));
const Loyalty = lazyPage(() => import("./Loyalty").then((m) => m.Loyalty));
const CheckoutBlocks = lazyPage(() => import("./CheckoutBlocks").then((m) => m.CheckoutBlocks));
const SSO = lazyPage(() => import("./SSO").then((m) => m.SSO));
const Bulk = lazyPage(() => import("./Bulk").then((m) => m.Bulk));
const Scripts = lazyPage(() => import("./Scripts").then((m) => m.Scripts));
const CheckoutRules = lazyPage(() => import("./CheckoutRules").then((m) => m.CheckoutRules));
const Automations = lazyPage(() => import("./Automations").then((m) => m.Automations));
const Apps = lazyPage(() => import("./Apps").then((m) => m.Apps));
const Checkout = lazyPage(() => import("./Checkout").then((m) => m.Checkout));

const PAGES: Record<string, { preload: () => Promise<void> }> = {
  tienda: StorePage,
  "ubicaciones": Locations,
  "envios": Shipping,
  "impuestos": Taxes,
  "canales": Channels,
  "politicas": Policies,
  "navegacion": Navigation,
  "redirecciones": Redirects,
  "dominios": Domains,
  "anuncios": Feeds,
  "idiomas": Languages,
  "campos": Fields,
  "catalogo-maestro": CatalogSource,
  "devoluciones": ReturnsPolicy,
  "conversiones": AdConversions,
  "campanas-correo": MailCampaigns,
  "diseno-programado": DesignSchedules,
  "blog": Blog,
  "tiendas": Storefronts,
  "canales-externos": SalesChannels,
  "regalos": Gifts,
  "lealtad": Loyalty,
  "bloques-pago": CheckoutBlocks,
  "sso": SSO,
  "cambios-masivos": Bulk,
  "scripts": Scripts,
  "reglas-pago": CheckoutRules,
  "automatizaciones": Automations,
  "apps": Apps,
  "cargos": Fees,
  "checkout": Checkout,
};

/** preloadSettings fetches the code of one section (the store section when sub is unknown). */
export function preloadSettings(sub?: string): Promise<void> {
  return (PAGES[sub ?? ""] ?? StorePage).preload();
}

/** preloadAllSettings fetches the code of every section. */
export function preloadAllSettings(): Promise<void> {
  return Promise.all(Object.values(PAGES).map((p) => p.preload().catch(() => {}))).then(() => {});
}

export const SETTINGS_NAV: { id: string; label: string; icon: LucideIcon; group: string }[] = [
  { id: "tienda", label: "Tienda", icon: Store, group: "General" },
  { id: "checkout", label: "Checkout", icon: ShoppingBag, group: "General" },
  { id: "reglas-pago", label: "Reglas de pago y envío", icon: Filter, group: "General" },
  { id: "bloques-pago", label: "Bloques del pago", icon: SquarePlus, group: "General" },
  { id: "canales", label: "Canales", icon: ArrowLeftRight, group: "General" },
  { id: "catalogo-maestro", label: "Catálogo maestro", icon: Copy, group: "General" },
  { id: "campos", label: "Campos propios", icon: ListPlus, group: "General" },
  { id: "apps", label: "Apps", icon: Blocks, group: "General" },
  { id: "sso", label: "Inicio de sesión de la empresa", icon: KeyRound, group: "General" },
  { id: "automatizaciones", label: "Automatizaciones", icon: Workflow, group: "General" },
  { id: "lealtad", label: "Puntos de lealtad", icon: Award, group: "General" },
  { id: "regalos", label: "Regalos y mesa de regalos", icon: Gift, group: "General" },
  { id: "canales-externos", label: "Mercado Libre y Amazon", icon: ShoppingCart, group: "General" },
  { id: "tiendas", label: "Tiendas y mercados", icon: LayoutGrid, group: "General" },
  { id: "campanas-correo", label: "Campañas de correo", icon: MailPlus, group: "General" },
  { id: "cambios-masivos", label: "Cambios masivos", icon: Layers, group: "General" },
  { id: "ubicaciones", label: "Ubicaciones", icon: MapPin, group: "Logística" },
  { id: "envios", label: "Envíos", icon: Truck, group: "Logística" },
  { id: "impuestos", label: "Impuestos", icon: Percent, group: "Logística" },
  { id: "cargos", label: "Cargos extra", icon: Receipt, group: "Logística" },
  { id: "devoluciones", label: "Devoluciones", icon: Undo2, group: "Logística" },
  { id: "politicas", label: "Políticas", icon: FileText, group: "Tienda en línea" },
  { id: "navegacion", label: "Navegación", icon: ListTree, group: "Tienda en línea" },
  { id: "redirecciones", label: "Redirecciones", icon: CornerUpRight, group: "Tienda en línea" },
  { id: "dominios", label: "Dominios", icon: Globe, group: "Tienda en línea" },
  { id: "idiomas", label: "Idiomas", icon: LanguagesIcon, group: "Tienda en línea" },
  { id: "scripts", label: "Scripts y cookies", icon: Cookie, group: "Tienda en línea" },
  { id: "blog", label: "Blog", icon: Newspaper, group: "Tienda en línea" },
  { id: "diseno-programado", label: "Diseño programado", icon: CalendarClock, group: "Tienda en línea" },
  { id: "anuncios", label: "Anuncios y catálogos", icon: Megaphone, group: "Tienda en línea" },
  { id: "conversiones", label: "Conversiones para anuncios", icon: Target, group: "Tienda en línea" },
];

/** Settings is the Configuración area: a sub-navigation plus one screen per topic. */
export function Settings({ me, sub, rest }: { me: Me; sub?: string; rest: string[] }) {
  const current = SETTINGS_NAV.find((n) => n.id === sub)?.id ?? "tienda";
  const groups = [...new Set(SETTINGS_NAV.map((n) => n.group))];
  // The other sections load while the person reads this one, so moving
  // between them never waits for code.
  useEffect(() => whenIdle(() => void preloadAllSettings()), []);
  let body;
  switch (current) {
    case "ubicaciones":
      body = <Locations me={me} tab={rest[0]} />;
      break;
    case "envios":
      body = <Shipping me={me} tab={rest[0]} />;
      break;
    case "impuestos":
      body = <Taxes me={me} />;
      break;
    case "canales":
      body = <Channels me={me} />;
      break;
    case "politicas":
      body = <Policies me={me} slug={rest[0]} />;
      break;
    case "navegacion":
      body = <Navigation me={me} menuId={rest[0]} />;
      break;
    case "redirecciones":
      body = <Redirects me={me} />;
      break;
    case "dominios":
      body = <Domains me={me} />;
      break;
    case "anuncios":
      body = <Feeds me={me} />;
      break;
    case "idiomas":
      body = <Languages me={me} />;
      break;
    case "campos":
      body = <Fields me={me} />;
      break;
    case "catalogo-maestro":
      body = <CatalogSource me={me} />;
      break;
    case "devoluciones":
      body = <ReturnsPolicy me={me} />;
      break;
    case "conversiones":
      body = <AdConversions me={me} />;
      break;
    case "campanas-correo":
      body = <MailCampaigns me={me} />;
      break;
    case "diseno-programado":
      body = <DesignSchedules me={me} />;
      break;
    case "blog":
      body = <Blog me={me} />;
      break;
    case "tiendas":
      body = <Storefronts me={me} />;
      break;
    case "canales-externos":
      body = <SalesChannels me={me} />;
      break;
    case "regalos":
      body = <Gifts me={me} />;
      break;
    case "lealtad":
      body = <Loyalty me={me} />;
      break;
    case "bloques-pago":
      body = <CheckoutBlocks me={me} />;
      break;
    case "sso":
      body = <SSO me={me} />;
      break;
    case "cambios-masivos":
      body = <Bulk me={me} />;
      break;
    case "scripts":
      body = <Scripts me={me} />;
      break;
    case "reglas-pago":
      body = <CheckoutRules me={me} />;
      break;
    case "automatizaciones":
      body = <Automations me={me} />;
      break;
    case "apps":
      body = <Apps me={me} />;
      break;
    case "cargos":
      body = <Fees me={me} />;
      break;
    case "checkout":
      body = <Checkout me={me} />;
      break;
    default:
      body = <StorePage me={me} />;
  }
  return (
    <div className="mx-auto grid w-full max-w-7xl gap-6 px-4 py-6 md:px-8 lg:grid-cols-[212px_minmax(0,1fr)]">
      <nav aria-label="Configuración" className="min-w-0 lg:sticky lg:top-6 lg:self-start">
        <div className="mb-3 hidden items-center gap-2 lg:flex">
          <Building2 className="size-4 text-muted-foreground" aria-hidden="true" />
          <span className="text-sm font-semibold">Configuración</span>
        </div>
        {/* Mobile and tablet: one scrollable row. Desktop: grouped list. */}
        <ul className="-mx-4 flex gap-1 overflow-x-auto px-4 pb-1 lg:mx-0 lg:grid lg:gap-0.5 lg:overflow-visible lg:px-0 lg:pb-0">
          {groups.map((g) => (
            <li key={g} className="contents lg:block lg:pb-3">
              <span className="hidden px-2.5 pb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground lg:block">{g}</span>
              <ul className="contents lg:grid lg:gap-0.5">
                {SETTINGS_NAV.filter((n) => n.group === g).map((n) => {
                  const active = n.id === current;
                  return (
                    <li key={n.id} className="shrink-0">
                      <a
                        href={href("configuracion", n.id)}
                        aria-current={active ? "page" : undefined}
                        className={cn(
                          "flex items-center gap-2 whitespace-nowrap rounded-md px-2.5 py-1.5 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                          active ? "bg-accent font-medium text-accent-foreground" : "text-muted-foreground hover:bg-muted hover:text-foreground",
                        )}
                      >
                        <n.icon className="size-4 shrink-0" aria-hidden="true" />
                        {n.label}
                      </a>
                    </li>
                  );
                })}
              </ul>
            </li>
          ))}
        </ul>
      </nav>
      <Suspense fallback={<Loading />}>{body}</Suspense>
    </div>
  );
}
