import type { Json, Op, SectionDefinition, SectionInstance, Template } from "./types";

/** An edit is a forward patch plus the patch that undoes it. */
export interface Edit {
  label: string;
  ops: Op[];
  inverse: Op[];
}

const MAIN = "/regions/main";

export function mainSections(t: Template): SectionInstance[] {
  return t.regions.main ?? [];
}

/** The fixed regions around the page body: their sections are edited but not moved or removed. */
export type Region = "main" | "header" | "footer";

export function regionSections(t: Template, region: Region): SectionInstance[] {
  return t.regions[region] ?? [];
}

/** locate finds a section by id in any region. */
export function locate(t: Template, id: string | null): { region: Region; index: number; section: SectionInstance } | null {
  if (!id) return null;
  for (const region of ["header", "main", "footer"] as Region[]) {
    const index = regionSections(t, region).findIndex((x) => x.id === id);
    if (index >= 0) return { region, index, section: regionSections(t, region)[index] as SectionInstance };
  }
  return null;
}

export function newId(prefix: string): string {
  const rand = Math.random().toString(36).slice(2, 8);
  return `${prefix}_${Date.now().toString(36)}${rand}`;
}

const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v)) as T;

export function moveSection(t: Template, from: number, to: number): Edit | null {
  const n = mainSections(t).length;
  if (from === to || from < 0 || to < 0 || from >= n || to >= n) return null;
  return {
    label: "Mover sección",
    ops: [{ op: "move", from: `${MAIN}/${from}`, path: `${MAIN}/${to}` }],
    inverse: [{ op: "move", from: `${MAIN}/${to}`, path: `${MAIN}/${from}` }],
  };
}

/** defaultSettings fills schema defaults; required text fields start empty so validation asks for them. */
export function defaultSettings(def: SectionDefinition): Record<string, Json> {
  const out: Record<string, Json> = {};
  const required = new Set(def.settingsSchema.required ?? []);
  for (const [k, p] of Object.entries(def.settingsSchema.properties ?? {})) {
    if (p.default !== undefined) out[k] = p.default;
    else if (required.has(k) && p.type === "string") out[k] = "";
  }
  return out;
}

export function addSection(t: Template, def: SectionDefinition, index: number, id = newId("sec")): Edit {
  const n = mainSections(t).length;
  const at = Math.max(0, Math.min(index, n));
  const allowed = def.blocks.allowed ?? [];
  const blocks = allowed.length
    ? Array.from({ length: def.blocks.min }, () => ({ id: newId("blk"), type: allowed[0] as string, settings: {} }))
    : [];
  const value: SectionInstance = { id, type: def.type, typeVersion: def.version, settings: defaultSettings(def) };
  if (blocks.length) value.blocks = blocks;
  const ops: Op[] = t.regions.main
    ? [{ op: "add", path: `${MAIN}/${at}`, value: value as unknown as Json }]
    : [{ op: "add", path: MAIN, value: [value] as unknown as Json }];
  return { label: `Agregar ${def.name}`, ops, inverse: [{ op: "remove", path: `${MAIN}/${at}` }] };
}

export function removeSection(t: Template, index: number): Edit | null {
  const sec = mainSections(t)[index];
  if (!sec) return null;
  return {
    label: "Quitar sección",
    ops: [{ op: "remove", path: `${MAIN}/${index}` }],
    inverse: [{ op: "add", path: `${MAIN}/${index}`, value: clone(sec) as unknown as Json }],
  };
}

export function duplicateSection(t: Template, index: number, id = newId("sec")): Edit | null {
  const sec = mainSections(t)[index];
  if (!sec || sec.globalRef) return null;
  const copy = clone(sec);
  copy.id = id;
  copy.blocks = copy.blocks?.map((b) => ({ ...b, id: newId("blk") }));
  return {
    label: "Duplicar sección",
    ops: [{ op: "add", path: `${MAIN}/${index + 1}`, value: copy as unknown as Json }],
    inverse: [{ op: "remove", path: `${MAIN}/${index + 1}` }],
  };
}

/** setSetting writes (or removes, when value is undefined) one setting of a section. */
export function setSetting(t: Template, index: number, key: string, value: Json | undefined, region: Region = "main"): Edit | null {
  const sec = regionSections(t, region)[index];
  if (!sec) return null;
  const base = `/regions/${region}/${index}/settings`;
  const path = `${base}/${escapeKey(key)}`;
  const had = sec.settings !== undefined && key in sec.settings;
  const old = sec.settings?.[key];
  if (value === undefined) {
    if (!had) return null;
    return { label: "Cambiar ajuste", ops: [{ op: "remove", path }], inverse: [{ op: "add", path, value: old as Json }] };
  }
  if (had && JSON.stringify(old) === JSON.stringify(value)) return null;
  if (!sec.settings) {
    return {
      label: "Cambiar ajuste",
      ops: [{ op: "add", path: base, value: { [key]: value } }],
      inverse: [{ op: "remove", path: base }],
    };
  }
  return {
    label: "Cambiar ajuste",
    ops: [{ op: had ? "replace" : "add", path, value }],
    inverse: had ? [{ op: "replace", path, value: old as Json }] : [{ op: "remove", path }],
  };
}

export function addBlock(t: Template, index: number, type: string, id = newId("blk"), region: Region = "main"): Edit | null {
  const sec = regionSections(t, region)[index];
  if (!sec) return null;
  const value = { id, type, settings: {} } as unknown as Json;
  if (!sec.blocks) {
    const path = `/regions/${region}/${index}/blocks`;
    return { label: "Agregar bloque", ops: [{ op: "add", path, value: [value] }], inverse: [{ op: "remove", path }] };
  }
  const at = sec.blocks.length;
  return {
    label: "Agregar bloque",
    ops: [{ op: "add", path: `/regions/${region}/${index}/blocks/-`, value }],
    inverse: [{ op: "remove", path: `/regions/${region}/${index}/blocks/${at}` }],
  };
}

export function removeBlock(t: Template, index: number, b: number, region: Region = "main"): Edit | null {
  const blk = regionSections(t, region)[index]?.blocks?.[b];
  if (!blk) return null;
  const path = `/regions/${region}/${index}/blocks/${b}`;
  return { label: "Quitar bloque", ops: [{ op: "remove", path }], inverse: [{ op: "add", path, value: clone(blk) as unknown as Json }] };
}

export function setBlockSetting(t: Template, index: number, b: number, key: string, value: string, region: Region = "main"): Edit | null {
  const blk = regionSections(t, region)[index]?.blocks?.[b];
  if (!blk) return null;
  const base = `/regions/${region}/${index}/blocks/${b}/settings`;
  if (!blk.settings) {
    return {
      label: "Cambiar bloque",
      ops: [{ op: "add", path: base, value: { [key]: value } }],
      inverse: [{ op: "remove", path: base }],
    };
  }
  const path = `${base}/${escapeKey(key)}`;
  const had = key in blk.settings;
  const old = blk.settings[key];
  if (had && old === value) return null;
  return {
    label: "Cambiar bloque",
    ops: [{ op: had ? "replace" : "add", path, value }],
    inverse: had ? [{ op: "replace", path, value: old as Json }] : [{ op: "remove", path }],
  };
}

/** escapeKey applies JSON Pointer escaping (RFC 6901). */
export function escapeKey(k: string): string {
  return k.replace(/~/g, "~0").replace(/\//g, "~1");
}

/** reorder mirrors a move locally for optimistic UI. */
export function reorder<T>(xs: T[], from: number, to: number): T[] {
  const out = xs.slice();
  const [item] = out.splice(from, 1);
  if (item !== undefined) out.splice(to, 0, item);
  return out;
}

/** setAudience shows a section only to (or hides it from) some customer groups; null = everyone. */
export function setAudience(t: Template, index: number, audience: { mode: "only" | "hide"; groups: string[] } | null, region: Region = "main"): Edit | null {
  const sec = regionSections(t, region)[index];
  if (!sec) return null;
  const base = `/regions/${region}/${index}/visibility`;
  const old = sec.visibility;
  const next = { ...(old ?? {}), audience: audience ?? undefined } as Record<string, Json>;
  if (!audience) delete next.audience;
  if (JSON.stringify(old?.audience ?? null) === JSON.stringify(audience)) return null;
  return {
    label: "Cambiar quién ve la sección",
    ops: [{ op: old ? "replace" : "add", path: base, value: next as unknown as Json }],
    inverse: old ? [{ op: "replace", path: base, value: old as unknown as Json }] : [{ op: "remove", path: base }],
  };
}
