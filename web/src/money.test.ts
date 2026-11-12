import { describe, expect, it } from "vitest";
import { InvalidNumber, readNumber, readPesos } from "./money";

describe("números escritos por personas", () => {
  it("lee cifras con punto o coma y usa el valor por omisión si está vacío", () => {
    expect(readNumber("12,5", "Descuento")).toBe(12.5);
    expect(readNumber(" 3 ", "Días")).toBe(3);
    expect(readNumber("", "Mínimo", 1)).toBe(1);
    expect(readPesos("1,299.50", "Precio")).toBe(129950);
    expect(readPesos("", "Envío")).toBe(0);
  });
  it("nunca convierte en 0 algo que no es número: avisa con el nombre del campo", () => {
    expect(() => readNumber("abc", "Descuento (%)")).toThrow(InvalidNumber);
    expect(() => readNumber("12abc", "Descuento (%)")).toThrow("«Descuento (%)»");
    expect(() => readPesos("doce", "Límite de crédito")).toThrow("«Límite de crédito»");
  });
});
