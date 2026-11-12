import { describe, expect, it } from "vitest";
import { ProblemSchema } from "./types";

describe("ProblemSchema", () => {
  it("keeps the known fields of a valid problem and drops the rest", () => {
    const r = ProblemSchema.safeParse({ status: 422, code: "invalid", title: "Datos inválidos", errors: [{ field: "title", code: "required", message: "Falta" }], extra: 1 });
    expect(r).toEqual({ success: true, data: { status: 422, code: "invalid", title: "Datos inválidos", errors: [{ field: "title", code: "required", message: "Falta" }] } });
  });
  it("rejects what is not a problem", () => {
    for (const v of [null, "x", [], { code: "a" }, { status: "400", code: "a" }, { status: 400, code: "a", errors: [{ field: 1 }] }, { status: 400, code: "a", reports: [] }]) {
      expect(ProblemSchema.safeParse(v).success).toBe(false);
    }
  });
});
