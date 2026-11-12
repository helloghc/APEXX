import { lazy, Suspense } from "react";
import { ArrowLeftRight, Building2, CornerUpRight, FileText, Globe, ListTree, MapPin, Megaphone, Percent, ShoppingBag, Store, Truck, type LucideIcon, Receipt, Undo2, Languages as LanguagesIcon, Copy, ListPlus, Blocks, Workflow, Filter, Cookie, Layers, KeyRound, SquarePlus, Award, Newspaper } from "lucide-react";
import type { Me } from "@/api";
import { cn } from "@/lib/utils";
import { href } from "@/router";
import { Loading } from "./kit";

const StorePage = lazy(() => import("./Store").then((m) => ({ default: m.StorePage })));
const Locations = lazy(() => import("./Locations").then((m) => ({ default: m.Locations })));
const Shipping = lazy(() => import("./Shipping").then((m) => ({ default: m.Shipping })));
const Taxes = lazy(() => import("./Taxes").then((m) => ({ default: m.Taxes })));
const Channels = lazy(() => import("./Channels").then((m) => ({ default: m.Channels })));
const Policies = lazy(() => import("./Policies").then((m) => ({ default: m.Policies })));
const Navigation = lazy(() => import("./Navigation").then((m) => ({ default: m.Navigation })));
const Redirects = lazy(() => import("./Redirects").then((m) => ({ default: m.Redirects })));
const Domains = lazy(() => import("./Domains").then((m) => ({ default: m.Domains })));
const Feeds = lazy(() => import("./Feeds").then((m) => ({ default: m.Feeds })));
const Languages = lazy(() => import("./Languages").then((m) => ({ default: m.Languages })));
const Fields = lazy(() => import("./Fields").then((m) => ({ default: m.Fields })));
const CatalogSource = lazy(() => import("./CatalogSource").then((m) => ({ default: m.CatalogSource })));
const ReturnsPolicy = lazy(() => import("./ReturnsPolicy").then((m) => ({ default: m.ReturnsPolicy })));
const Fees = lazy(() => import("./Fees").then((m) => ({ default: m.Fees })));
const Blog = lazy(() => import("./Blog").then((m) => ({ default: m.Blog })));
const Loyalty = lazy(() => import("./Loyalty").then((m) => ({ default: m.Loyalty })));
const CheckoutBlocks = lazy(() => import("./CheckoutBlocks").then((m) => ({ default: m.CheckoutBlocks })));
const SSO = lazy(() => import("./SSO").then((m) => ({ default: m.SSO })));
const Bulk = lazy(() => import("./Bulk").then((m) => ({ default: m.Bulk })));
const Scripts = lazy(() => import("./Scripts").then((m) => ({ default: m.Scripts })));
const CheckoutRules = lazy(() => import("./CheckoutRules").then((m) => ({ default: m.CheckoutRules })));
const Automations = lazy(() => import("./Automations").then((m) => ({ default: m.Automations })));
const Apps = lazy(() => import("./Apps").then((m) => ({ default: m.Apps })));
const Checkout = lazy(() => import("./Checkout").then((m) => ({ default: m.Checkout })));

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
  { id: "anuncios", label: "Anuncios y catálogos", icon: Megaphone, group: "Tienda en línea" },
];

/** Settings is the Configuración area: a sub-navigation plus one screen per topic. */
export function Settings({ me, sub, rest }: { me: Me; sub?: string; rest: string[] }) {
  const current = SETTINGS_NAV.find((n) => n.id === sub)?.id ?? "tienda";
  const groups = [...new Set(SETTINGS_NAV.map((n) => n.group))];
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
    case "blog":
      body = <Blog me={me} />;
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
