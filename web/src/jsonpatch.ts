import type { Json, Op } from "./types";

// Minimal RFC 6902 applier, used for optimistic UI and tests. The server is authoritative.
function parse(p: string): string[] {
  return p === "" ? [] : p.slice(1).split("/").map((s) => s.replace(/~1/g, "/").replace(/~0/g, "~"));
}

function parentOf(doc: Json, path: string[]): Json {
  let cur: Json = doc;
  for (const tok of path.slice(0, -1)) {
    if (Array.isArray(cur)) cur = cur[Number(tok)] as Json;
    else if (cur && typeof cur === "object") cur = cur[tok] as Json;
    else throw new Error("bad path " + path.join("/"));
    if (cur === undefined) throw new Error("missing " + tok);
  }
  return cur;
}

function getAt(doc: Json, path: string[]): Json {
  if (!path.length) return doc;
  const parent = parentOf(doc, path);
  const last = path[path.length - 1] as string;
  const v = Array.isArray(parent) ? parent[Number(last)] : (parent as Record<string, Json>)[last];
  if (v === undefined) throw new Error("missing " + last);
  return v;
}

function setAt(doc: Json, path: string[], value: Json, insert: boolean): Json {
  if (!path.length) return value;
  const parent = parentOf(doc, path);
  const last = path[path.length - 1] as string;
  if (Array.isArray(parent)) {
    const i = last === "-" ? parent.length : Number(last);
    if (insert) parent.splice(i, 0, value);
    else parent[i] = value;
  } else if (parent && typeof parent === "object") {
    parent[last] = value;
  }
  return doc;
}

function removeAt(doc: Json, path: string[]): Json {
  const parent = parentOf(doc, path);
  const last = path[path.length - 1] as string;
  if (Array.isArray(parent)) return parent.splice(Number(last), 1)[0] as Json;
  const obj = parent as Record<string, Json>;
  const v = obj[last] as Json;
  delete obj[last];
  return v;
}

export function applyPatch<T>(input: T, ops: Op[]): T {
  let doc = JSON.parse(JSON.stringify(input)) as Json;
  for (const op of ops) {
    const path = parse(op.path);
    switch (op.op) {
      case "add":
        doc = setAt(doc, path, op.value as Json, true);
        break;
      case "replace":
        getAt(doc, path);
        doc = setAt(doc, path, op.value as Json, false);
        break;
      case "remove":
        removeAt(doc, path);
        break;
      case "move": {
        const v = removeAt(doc, parse(op.from ?? ""));
        doc = setAt(doc, path, v, true);
        break;
      }
      case "copy":
        doc = setAt(doc, path, JSON.parse(JSON.stringify(getAt(doc, parse(op.from ?? "")))) as Json, true);
        break;
      case "test":
        if (JSON.stringify(getAt(doc, path)) !== JSON.stringify(op.value)) throw new Error("test failed");
        break;
    }
  }
  return doc as unknown as T;
}
