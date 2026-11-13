import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { ADMIN_THEMES } from "./adminThemes";
import { contrast } from "./theme";

const css = readFileSync(new URL("./tokens.css", import.meta.url), "utf8");

describe("admin themes", () => {
  it("has exactly the three themes, each readable (WCAG AA)", () => {
    expect(ADMIN_THEMES.map((t) => t.id)).toEqual(["rosa", "lavanda", "niebla"]);
    for (const t of ADMIN_THEMES) {
      for (const [fg, bg] of [[t.fg, t.bg], [t.fg, t.surface], [t.muted, t.bg], [t.muted, t.surface], [t.accent, t.surface], [t.accent, t.bg], [t.accentFg, t.accent], [t.fg, t.accentSoft], [t.accent, t.accentSoft]] as [string, string][]) {
        expect(contrast(fg, bg), `${t.id}: ${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5);
      }
    }
  });
  it("status colours are readable on every surface of every theme (WCAG AA)", () => {
    const token = (name: string): string => css.match(new RegExp(`${name}: (#[0-9a-f]{6});`))![1]!;
    const surfaces = (id: string) => {
      const block = css.slice(css.indexOf(`[data-theme="${id}"]`));
      const rule = block.slice(0, block.indexOf("}"));
      return ["--bg", "--surface", "--surface-2"].map((n): string => rule.match(new RegExp(`${n}: (#[0-9a-f]{6});`))![1]!);
    };
    for (const s of ["ok", "warn", "bad"]) {
      const fg = token(`--${s}`);
      expect(contrast(fg, token(`--${s}-soft`)), `--${s} on --${s}-soft`).toBeGreaterThanOrEqual(4.5);
      for (const t of ADMIN_THEMES) {
        for (const bg of surfaces(t.id)) expect(contrast(fg, bg), `${t.id}: --${s} ${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5);
      }
    }
  });
  it("matches tokens.css and uses the palette for its surfaces", () => {
    expect(css).not.toContain("prefers-color-scheme: dark");
    for (const t of ADMIN_THEMES) {
      const block = css.slice(css.indexOf(`[data-theme="${t.id}"]`));
      const rule = block.slice(0, block.indexOf("}"));
      for (const [name, value] of [["--bg", t.bg], ["--fg", t.fg], ["--muted", t.muted], ["--accent", t.accent], ["--accent-soft", t.accentSoft]] as const) {
        expect(rule, `${t.id} ${name}`).toContain(`${name}: ${value};`);
      }
      expect(t.palette).toContain(t.bg);
      expect(t.palette).toContain(t.accentSoft);
    }
  });
});
