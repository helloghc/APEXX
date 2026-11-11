import type { Me } from "@/api";
import { can } from "@/perms";
import { Page } from "../common";
import { Campaigns } from "./Campaigns";
import { Coupons } from "./Coupons";
import { BulkPrices, PriceHistory, PriceLists } from "./Pricing";
import { PromotionBuilder } from "./PromotionBuilder";
import { PromotionList } from "./PromotionList";
import { SubNav } from "./shared";
import { Simulator } from "./Simulator";

const TABS = [
  { key: "", label: "Promociones", route: ["promociones"], module: "promotions" },
  { key: "campanas", label: "Campañas", route: ["promociones", "campanas"], module: "promotions" },
  { key: "cupones", label: "Cupones", route: ["promociones", "cupones"], module: "promotions" },
  { key: "listas", label: "Listas de precios", route: ["promociones", "listas"], module: "pricing" },
  { key: "masivo", label: "Cambio masivo", route: ["promociones", "masivo"], module: "pricing" },
  { key: "historial", label: "Historial de precios", route: ["promociones", "historial"], module: "pricing" },
  { key: "simulador", label: "Simulador", route: ["promociones", "simulador"], module: "promotions" },
];

/** Routes: #promociones[/nueva|/p/{id}|/campanas[/{id}]|/cupones|/listas|/masivo|/historial|/simulador]. */
export function Promotions({ me, route }: { me: Me; route: string[] }) {
  const [, section, id] = route;
  if (section === "nueva") return <PromotionBuilder me={me} />;
  if (section === "p" && id) return <PromotionBuilder key={id} me={me} id={id} />;
  if (section === "campanas" && id) return <Campaigns me={me} id={id} />;
  const tabs = TABS.filter((t) => can(me, t.module, "read"));
  const active = tabs.find((t) => t.key === (section ?? "")) ? (section ?? "") : (tabs[0]?.key ?? "");
  let body;
  switch (active) {
    case "campanas": body = <Campaigns me={me} />; break;
    case "cupones": body = <Coupons me={me} />; break;
    case "listas": body = <PriceLists me={me} />; break;
    case "masivo": body = <BulkPrices me={me} />; break;
    case "historial": body = <PriceHistory />; break;
    case "simulador": body = <Simulator />; break;
    default: body = <PromotionList me={me} />;
  }
  return (
    <Page title="Promociones y precios" sub="Cada promoción le explica al comprador por qué aplica o qué le falta. Los cambios se ven en la tienda al instante.">
      <SubNav items={tabs} active={active} label="Secciones de promociones" />
      {body}
    </Page>
  );
}
