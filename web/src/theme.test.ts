import { describe, expect, it } from "vitest";
import { applyPatch } from "./jsonpatch";
import { contrast, contrastProblems, resolvedColors, setThemeValue } from "./theme";
import type { StoreTheme, Template } from "./types";

const base = {
  id: "lienzo",
  colors: { bg: "#F6F4F0", surface: "#FFFFFF", text: "#191817", muted: "#625C55", primary: "#191817", primaryText: "#FFFFFF" },
} as unknown as StoreTheme;
const tpl = { id: "t", kind: "home", handle: "index", name: "Inicio", seo: { title: "x", description: "y" }, regions: { main: [] } } as Template;

describe("theme helpers", () => {
  it("computes WCAG contrast", () => {
    expect(contrast("#000000", "#FFFFFF")).toBeCloseTo(21, 0);
    expect(contrast("#777777", "#FFFFFF")).toBeCloseTo(4.48, 1);
  });
  it("flags low-contrast pairs only", () => {
    const colors = resolvedColors(base, { id: "lienzo", colors: { text: "#EEEEEE" } });
    const p = contrastProblems(colors, [["text", "bg"], ["primaryText", "primary"]]);
    expect(p.map((x) => x.fg)).toEqual(["text"]);
  });
  it("creates undoable theme edits", () => {
    const e = setThemeValue(tpl, "lienzo", "colors/accent", "#112233")!;
    const after = applyPatch(tpl, e.ops);
    expect(after.theme).toEqual({ id: "lienzo", colors: { accent: "#112233" } });
    expect(applyPatch(after, e.inverse).theme).toBeUndefined();
    const e2 = setThemeValue(after, "lienzo", "radius", 12)!;
    expect(applyPatch(after, e2.ops).theme?.radius).toBe(12);
    expect(setThemeValue(after, "lienzo", "colors/accent", "#112233")).toBeNull();
  });
});
