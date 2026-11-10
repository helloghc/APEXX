import { describe, expect, it } from "vitest";
import { applyPatch } from "./jsonpatch";
import {
  addBlock,
  addSection,
  duplicateSection,
  moveSection,
  removeBlock,
  removeSection,
  reorder,
  setBlockSetting,
  setSetting,
  type Edit,
} from "./patch";
import type { SectionDefinition, Template } from "./types";

const tpl: Template = {
  id: "t",
  kind: "home",
  handle: "index",
  name: "Inicio",
  seo: { title: "x", description: "y" },
  regions: {
    main: [
      { id: "hero", type: "hero", settings: { heading: "Hola" } },
      { id: "txt", type: "rich-text" },
      { id: "ben", type: "benefits", settings: {}, blocks: [{ id: "b1", type: "benefit", settings: { title: "A" } }] },
    ],
  },
};

const benefits: SectionDefinition = {
  type: "benefits",
  version: "1.0.0",
  name: "Íconos de beneficios",
  category: "contenido",
  description: "",
  hydration: "none",
  blocks: { allowed: ["benefit"], min: 2, max: 4 },
  budget: { htmlBytes: 1, jsBytes: 0, cssBytes: 0, requests: 0, aboveFoldImages: 0 },
  settingsSchema: {
    type: "object",
    required: ["heading"],
    properties: { heading: { type: "string" }, columns: { type: "integer", default: 3 } },
  },
};

// Every edit must round-trip: apply ops, then inverse, and get the original back.
function roundTrip(e: Edit | null) {
  expect(e).not.toBeNull();
  const after = applyPatch(tpl, e!.ops);
  expect(applyPatch(after, e!.inverse)).toEqual(tpl);
  return after;
}

const ids = (t: Template) => (t.regions.main ?? []).map((s) => s.id);

describe("section edits", () => {
  it("moves with one op and reverses", () => {
    const e = moveSection(tpl, 2, 0)!;
    expect(e.ops).toEqual([{ op: "move", from: "/regions/main/2", path: "/regions/main/0" }]);
    expect(ids(roundTrip(e))).toEqual(["ben", "hero", "txt"]);
    expect(ids(tpl)).toEqual(ids({ ...tpl, regions: { main: reorder(tpl.regions.main!, 0, 0) } }));
  });
  it("matches local reorder", () => {
    const after = applyPatch(tpl, moveSection(tpl, 0, 2)!.ops);
    expect(ids(after)).toEqual(reorder(tpl.regions.main!, 0, 2).map((s) => s.id));
  });
  it("rejects no-op and out of range moves", () => {
    expect(moveSection(tpl, 1, 1)).toBeNull();
    expect(moveSection(tpl, 0, 9)).toBeNull();
  });
  it("adds with defaults and minimum blocks", () => {
    const after = roundTrip(addSection(tpl, benefits, 1, "new"));
    const s = after.regions.main![1]!;
    expect(s.id).toBe("new");
    expect(s.settings).toEqual({ heading: "", columns: 3 });
    expect(s.blocks).toHaveLength(2);
  });
  it("removes and duplicates", () => {
    expect(ids(roundTrip(removeSection(tpl, 0)))).toEqual(["txt", "ben"]);
    const dup = roundTrip(duplicateSection(tpl, 2, "copy"));
    expect(ids(dup)).toEqual(["hero", "txt", "ben", "copy"]);
    expect(dup.regions.main![3]!.blocks![0]!.id).not.toBe("b1");
  });
});

describe("settings edits", () => {
  it("replaces an existing setting", () => {
    const e = setSetting(tpl, 0, "heading", "Adiós")!;
    expect(e.ops[0]!.op).toBe("replace");
    expect(roundTrip(e).regions.main![0]!.settings!.heading).toBe("Adiós");
  });
  it("adds a new setting and creates the settings object if missing", () => {
    expect(roundTrip(setSetting(tpl, 0, "alt", "Una foto")).regions.main![0]!.settings).toEqual({ heading: "Hola", alt: "Una foto" });
    expect(roundTrip(setSetting(tpl, 1, "heading", "T")).regions.main![1]!.settings).toEqual({ heading: "T" });
  });
  it("removes a setting and skips unchanged values", () => {
    expect(roundTrip(setSetting(tpl, 0, "heading", undefined)).regions.main![0]!.settings).toEqual({});
    expect(setSetting(tpl, 0, "heading", "Hola")).toBeNull();
    expect(setSetting(tpl, 0, "missing", undefined)).toBeNull();
  });
  it("escapes JSON pointer keys", () => {
    expect(setSetting(tpl, 0, "a/b~c", "v")!.ops[0]!.path).toBe("/regions/main/0/settings/a~1b~0c");
  });
});

describe("block edits", () => {
  it("adds, edits and removes blocks", () => {
    expect(roundTrip(addBlock(tpl, 2, "benefit", "b2")).regions.main![2]!.blocks).toHaveLength(2);
    expect(roundTrip(addBlock(tpl, 1, "benefit", "b2")).regions.main![1]!.blocks).toHaveLength(1);
    expect(roundTrip(setBlockSetting(tpl, 2, 0, "title", "B")).regions.main![2]!.blocks![0]!.settings!.title).toBe("B");
    expect(roundTrip(setBlockSetting(tpl, 2, 0, "text", "t")).regions.main![2]!.blocks![0]!.settings!.text).toBe("t");
    expect(roundTrip(removeBlock(tpl, 2, 0)).regions.main![2]!.blocks).toHaveLength(0);
  });
});
