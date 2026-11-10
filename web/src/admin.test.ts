import { describe, expect, it } from "vitest";
import { centsToPesos, formatMoney, parsePesos } from "./money";
import { can, compact, presetOf, PRESETS, visibleNav } from "./perms";
import { href, parseHash } from "./router";
import { validateMember, validateProduct, validateWebhookUrl } from "./forms";

describe("money", () => {
  it("parses pesos into exact cents", () => {
    expect(parsePesos("1299.50")).toBe(129950);
    expect(parsePesos("$1,299.5")).toBe(129950);
    expect(parsePesos("1299,5")).toBe(129950);
    expect(parsePesos("0.1")).toBe(10);
    expect(parsePesos("19.99")).toBe(1999);
    expect(parsePesos("1 000")).toBe(100000);
    expect(parsePesos("12")).toBe(1200);
  });
  it("rejects invalid amounts", () => {
    for (const bad of ["", "abc", "-5", "1.234", "1..2", "1,2,3.4.5"]) expect(parsePesos(bad)).toBeNull();
  });
  it("round-trips cents", () => {
    for (const c of [0, 1, 99, 100, 129950, 1999, 100000005]) expect(parsePesos(centsToPesos(c))).toBe(c);
  });
  it("formats for reading", () => {
    expect(formatMoney(129950)).toBe("$1,299.50 MXN");
    expect(formatMoney(5, "USD")).toBe("$0.05 USD");
    expect(formatMoney(-12345)).toBe("-$123.45 MXN");
  });
});

describe("permissions", () => {
  const editor = { owner: false, permissions: { catalog: "edit", orders: "read" } as const };
  it("compares levels", () => {
    expect(can(editor, "catalog", "read")).toBe(true);
    expect(can(editor, "catalog", "edit")).toBe(true);
    expect(can(editor, "catalog", "admin")).toBe(false);
    expect(can(editor, "orders", "edit")).toBe(false);
    expect(can(editor, "team", "read")).toBe(false);
    expect(can(null, "catalog", "read")).toBe(false);
  });
  it("owners can do everything", () => {
    expect(can({ owner: true, permissions: {} }, "team", "admin")).toBe(true);
  });
  it("hides navigation the user cannot open", () => {
    const routes = visibleNav({ tenant: "t", kind: "member", id: "m", name: "", owner: false, permissions: { catalog: "edit" } }).map(
      (n) => n.route,
    );
    expect(routes).toEqual(["inicio", "catalogo"]);
  });
  it("recognizes presets regardless of 'none' entries", () => {
    const p = PRESETS.find((x) => x.id === "catalog")!.perms;
    expect(presetOf({ ...p, team: "none" })).toBe("catalog");
    expect(presetOf({ catalog: "read" })).toBeNull();
    expect(compact({ catalog: "edit", team: "none" })).toEqual({ catalog: "edit" });
  });
});

describe("router", () => {
  it("parses and builds hashes", () => {
    expect(parseHash("#pedidos/ord_1")).toEqual(["pedidos", "ord_1"]);
    expect(parseHash("")).toEqual([]);
    expect(parseHash(href("catalogo", "P 1"))).toEqual(["catalogo", "P 1"]);
  });
});

describe("forms", () => {
  it("validates products", () => {
    expect(validateProduct({ id: "", handle: "", title: "", variants: [] })).toMatchObject({
      id: expect.any(String),
      handle: expect.any(String),
      title: expect.any(String),
      variants: expect.any(String),
    });
    expect(
      validateProduct({ id: "P1", handle: "tenis-rojos", title: "Tenis", variants: [{ sku: "A" }, { sku: "A" }] }),
    ).toHaveProperty("variants");
    expect(validateProduct({ id: "P1", handle: "Tenis Rojos", title: "Tenis", variants: [{ sku: "A" }] })).toHaveProperty("handle");
    expect(validateProduct({ id: "P1", handle: "tenis-rojos", title: "Tenis", variants: [{ sku: "A" }] })).toEqual({});
  });
  it("validates members", () => {
    expect(validateMember({ email: "x", name: "", password: "short" })).toMatchObject({
      email: expect.any(String),
      password: expect.any(String),
    });
    expect(validateMember({ email: "ana@tienda.mx", name: "Ana", password: "una-clave-larga" })).toEqual({});
  });
  it("validates webhook URLs", () => {
    expect(validateWebhookUrl("http://example.com/x")).toBeTruthy();
    expect(validateWebhookUrl("nota url")).toBeTruthy();
    expect(validateWebhookUrl("https://hooks.example.com/apexx")).toBeNull();
  });
});
