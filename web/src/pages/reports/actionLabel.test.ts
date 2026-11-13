import { describe, expect, it } from "vitest";
import { actionLabel } from "./Ops";

describe("actionLabel", () => {
  it("names known actions in Spanish", () => {
    expect(actionLabel("category.reorder")).toBe("Categorías reordenadas");
    expect(actionLabel("session.revoke_others")).toBe("Sesiones cerradas en los demás dispositivos");
  });
  it("reads actions built from parts in Spanish too", () => {
    expect(actionLabel("subscription.paused")).toBe("Suscripción · pausado");
    expect(actionLabel("seller.credit")).toBe("Vendedor · abono");
    expect(actionLabel("settings.zones.delete")).toBe("Configuración zonas · eliminado");
  });
});
