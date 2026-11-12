import { describe, expect, it } from "vitest";
import { translate } from "./i18n";
import en from "./i18n/en.json";
import fr from "./i18n/fr.json";
import id from "./i18n/id.json";
import ja from "./i18n/ja.json";
import pt from "./i18n/pt.json";

describe("idiomas del panel", () => {
  it("traduce frases exactas y conserva los espacios", () => {
    expect(translate(id, " Pedidos ")).toBe(" Pesanan ");
    expect(translate(ja, "Guardar")).toBe("保存");
  });
  it("traduce frases con partes variables", () => {
    expect(translate(en, "Buscar Marca")).toBe("Search Brand");
    expect(translate(id, "Analítica reconstruida: 12 pedidos en 3 s")).toBe("Analitik dibangun ulang: 12 pesanan dalam 3 dtk");
  });
  it("mezcla cifras y textos en una misma frase, y traduce listas", () => {
    expect(translate(en, "10% en todo el pedido desde $3,000. Solo en el canal mx.")).toBe("10% on the whole order from $3,000. Only on channel mx.");
    expect(translate(ja, "Se paga un pedido → agregar nota interna al pedido, etiquetar al cliente. Hecha: 12 · No aplicó: 6")).toBe("注文が支払われる → 注文に社内メモを追加, 顧客にタグを付ける。実行：12 · 対象外：6");
    expect(translate(fr, "Chrome en Linux")).toBe("Chrome sur Linux");
  });
  it("deja en español lo que no conoce", () => {
    expect(translate(en, "Playera Sierra talla M")).toBeNull();
  });
  it("todos los idiomas traducen las mismas frases", () => {
    const keys = Object.keys(en).sort();
    expect(keys.length).toBeGreaterThan(700);
    for (const d of [fr, pt, id, ja]) expect(Object.keys(d).sort()).toEqual(keys);
  });
});
