import { useEffect, useState } from "react";

/** Route is the part after "#", split by "/": "#pedidos/ord_1" → ["pedidos", "ord_1"]. */
export function parseHash(hash: string): string[] {
  const h = hash.replace(/^#\/?/, "");
  return h ? h.split("/").map((p) => decodeURIComponent(p)) : [];
}

export function href(...parts: string[]): string {
  return "#" + parts.map((p) => encodeURIComponent(p)).join("/");
}

export function navigate(...parts: string[]) {
  window.location.hash = href(...parts);
}

export function useRoute(): string[] {
  const [route, setRoute] = useState(() => parseHash(window.location.hash));
  useEffect(() => {
    const on = () => setRoute(parseHash(window.location.hash));
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return route;
}
