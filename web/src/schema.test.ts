import { describe, expect, it } from "vitest";
import { coerce, fieldsFor, groupIssues, localError } from "./schema";
import type { Schema } from "./types";

const hero: Schema = {
  type: "object",
  required: ["heading", "alt"],
  properties: {
    heading: { type: "string", title: "Título", maxLength: 80, "x-apexx-widget": "text" },
    subheading: { type: "string", title: "Texto", maxLength: 160, "x-apexx-widget": "textarea" },
    alt: { type: "string", title: "Texto alternativo", minLength: 5 },
    height: { type: "string", enum: ["small", "medium", "large"], default: "medium" },
    overlay: { type: "number", minimum: 0, maximum: 0.8, "x-apexx-widget": "slider" },
    limit: { type: "integer", minimum: 1, maximum: 24 },
    dark: { type: "boolean" },
    link: { type: "object" },
  },
};

describe("fieldsFor", () => {
  const f = Object.fromEntries(fieldsFor(hero).map((x) => [x.key, x]));
  it("maps widgets and types", () => {
    expect(f.heading?.kind).toBe("text");
    expect(f.subheading?.kind).toBe("textarea");
    expect(f.height?.kind).toBe("select");
    expect(f.overlay?.kind).toBe("slider");
    expect(f.limit?.kind).toBe("number");
    expect(f.limit?.integer).toBe(true);
    expect(f.dark?.kind).toBe("checkbox");
    expect(f.link?.kind).toBe("unsupported");
  });
  it("keeps labels, required and order", () => {
    expect(fieldsFor(hero).map((x) => x.key)).toEqual(Object.keys(hero.properties!));
    expect(f.heading?.label).toBe("Título");
    expect(f.heading?.required).toBe(true);
    expect(f.subheading?.required).toBe(false);
  });
  it("handles missing schema", () => expect(fieldsFor(undefined)).toEqual([]));
  it("follows the server's order, then the rest", () => {
    const s = { type: "object", properties: { alt: { type: "string" }, heading: { type: "string" }, zeta: { type: "string" } }, "x-apexx-order": ["heading", "alt", "gone"] } as Schema;
    expect(fieldsFor(s).map((x) => x.key)).toEqual(["heading", "alt", "zeta"]);
  });
});

describe("coerce and localError", () => {
  const f = Object.fromEntries(fieldsFor(hero).map((x) => [x.key, x]));
  it("coerces numbers and enums", () => {
    expect(coerce(f.limit!, "7.6")).toBe(8);
    expect(coerce(f.overlay!, "0.3")).toBe(0.3);
    expect(coerce(f.overlay!, "")).toBeUndefined();
    expect(coerce(f.height!, "large")).toBe("large");
    expect(coerce(f.height!, "huge")).toBeUndefined();
  });
  it("removes empty optional text but keeps empty required text", () => {
    expect(coerce(f.subheading!, "")).toBeUndefined();
    expect(coerce(f.heading!, "")).toBe("");
  });
  it("validates like the server", () => {
    expect(localError(f.heading!, "")).toMatch(/obligatorio/);
    expect(localError(f.alt!, "foto")).toMatch(/Mínimo 5/);
    expect(localError(f.heading!, "x".repeat(81))).toMatch(/Máximo 80/);
    expect(localError(f.overlay!, 0.9)).toMatch(/Máximo 0.8/);
    expect(localError(f.heading!, "Hola")).toBeNull();
  });
});

describe("groupIssues", () => {
  it("routes issues to sections, fields and page", () => {
    const { bySection, page } = groupIssues([
      { path: "/regions/main/2/settings/alt", code: "min_length", message: "m", blocking: true },
      { path: "/regions/main/2/blocks", code: "block_count", message: "b", blocking: true },
      { path: "/", code: "budget_js", message: "js", blocking: true },
      { path: "/regions/header/0", code: "x", message: "h", blocking: false },
    ]);
    expect(bySection.get(2)?.fields.alt?.[0]?.code).toBe("min_length");
    expect(bySection.get(2)?.section[0]?.code).toBe("block_count");
    expect(page.map((i) => i.code)).toEqual(["budget_js", "x"]);
  });
});
