import type { Level, Me, Permissions } from "./api";

export const LEVELS: Level[] = ["none", "read", "edit", "admin"];

export const LEVEL_LABEL: Record<Level, string> = {
  none: "Sin acceso",
  read: "Lector",
  edit: "Editor",
  admin: "Admin",
};

/** Modules in display order, with names a merchant recognizes. */
export const MODULES: { id: string; label: string }[] = [
  { id: "catalog", label: "Catálogo" },
  { id: "pricing", label: "Precios" },
  { id: "inventory", label: "Inventario" },
  { id: "promotions", label: "Promociones" },
  { id: "orders", label: "Pedidos" },
  { id: "customers", label: "Clientes" },
  { id: "payments", label: "Pagos" },
  { id: "builder", label: "Diseño de la tienda" },
  { id: "team", label: "Equipo" },
  { id: "settings", label: "Configuración y conectores" },
  { id: "reports", label: "Reportes y analítica" },
];

const rank: Record<Level, number> = { none: 0, read: 1, edit: 2, admin: 3 };

/** can reports whether the user has at least `level` on `module`. Owners can do everything. */
export function can(me: Pick<Me, "owner" | "permissions"> | null | undefined, module: string, level: Level): boolean {
  if (!me) return false;
  if (me.owner) return true;
  const have = me.permissions?.[module] ?? "none";
  return (rank[have] ?? 0) >= rank[level];
}

export const PRESETS: { id: string; label: string; perms: Permissions }[] = [
  { id: "admin", label: "Admin", perms: Object.fromEntries(MODULES.map((m) => [m.id, "admin"])) as Permissions },
  {
    id: "catalog",
    label: "Editor de catálogo",
    perms: { catalog: "edit", pricing: "edit", inventory: "edit", promotions: "read" },
  },
  { id: "orders", label: "Atención a pedidos", perms: { orders: "edit", catalog: "read", inventory: "read" } },
  { id: "reader", label: "Lector", perms: Object.fromEntries(MODULES.map((m) => [m.id, "read"])) as Permissions },
];

/** normalize returns a full permission map with "none" for missing modules, dropping "none" entries for the API. */
export function compact(p: Permissions): Permissions {
  const out: Permissions = {};
  for (const m of MODULES) {
    const l = p[m.id];
    if (l && l !== "none") out[m.id] = l;
  }
  return out;
}

/** presetOf finds which preset matches a permission map, if any. */
export function presetOf(p: Permissions): string | null {
  const c = JSON.stringify(compact(p));
  return PRESETS.find((x) => JSON.stringify(compact(x.perms)) === c)?.id ?? null;
}

/** NAV lists sidebar entries and the permission needed to see each. */
export const NAV: { route: string; label: string; module?: string; level?: Level }[] = [
  { route: "inicio", label: "Inicio" },
  { route: "builder", label: "Diseño de la tienda", module: "builder" },
  { route: "catalogo", label: "Catálogo", module: "catalog" },
  { route: "promociones", label: "Promociones", module: "promotions" },
  { route: "busqueda", label: "Búsqueda y opiniones", module: "catalog" },
  { route: "pedidos", label: "Pedidos", module: "orders" },
  { route: "suscripciones", label: "Suscripciones", module: "orders" },
  { route: "clientes", label: "Clientes", module: "customers" },
  { route: "correos", label: "Correos", module: "settings" },
  { route: "reportes", label: "Reportes", module: "reports" },
  { route: "equipo", label: "Equipo", module: "team" },
  { route: "pagos", label: "Pagos", module: "payments" },
  { route: "tarjetas", label: "Tarjetas y antifraude", module: "payments" },
  { route: "configuracion", label: "Configuración", module: "settings" },
  { route: "conectores", label: "Conectores", module: "settings" },
  { route: "operacion", label: "Operación", module: "settings", level: "admin" },
];

export function visibleNav(me: Me | null | undefined) {
  return NAV.filter((n) => !n.module || can(me, n.module, n.level ?? "read"));
}
