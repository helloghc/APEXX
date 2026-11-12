// Route prefetch: a detail screen (an order, a customer, a product…) asks
// for its record, draws it, and only then its sections ask for theirs, so
// opening it costs two network round trips one after the other.
//
// This module learns, per kind of screen, which reads carry the record's id
// as a whole path segment ("/v1/orders/{id}/warehouse-requests"). The next
// time a screen of that kind opens with another id, all of them start at
// once, at the moment of the navigation, and request() takes the answer
// already on its way: one round trip instead of two, with no change to the
// screens. Only reads (GET) are replayed, and an answer is used only within
// a few seconds of the navigation.

const LEARN_MS = 3000; // reads after a navigation that count as the screen's
const KEEP_MS = 4000; // how long a started read waits for its screen
const MAX = 12; // reads learned per kind of screen
const STORE = "apexx.routeReads";

type Shape = { shape: string; id: string };

/** shapeOf turns "#pedidos/ord_1" into the kind "pedidos/:id" and the id; a
 *  route whose last part is not an id (a tab name) has no shape. */
export function shapeOf(hash: string): Shape | null {
  const parts = hash
    .replace(/^#\/?/, "")
    .split("/")
    .filter(Boolean)
    .map((p) => {
      try {
        return decodeURIComponent(p);
      } catch {
        return p;
      }
    });
  if (parts.length < 2) return null;
  const id = parts[parts.length - 1] ?? "";
  // Ids carry a digit or an underscore; tab names ("segmentos", "nuevo") do not.
  if (id.length < 3 || !/[0-9_]/.test(id)) return null;
  return { shape: parts.slice(0, -1).join("/") + "/:id", id };
}

const seg = (id: string) => encodeURIComponent(id);

/** template replaces the id when it is a whole path segment; null otherwise. */
export function template(path: string, id: string): string | null {
  const q = path.indexOf("?");
  const p = q < 0 ? path : path.slice(0, q);
  const e = seg(id);
  const parts = p.split("/");
  if (!parts.includes(e)) return null;
  if (q >= 0 && path.slice(q).includes(e)) return null; // the id inside the query: too specific
  return parts.map((x) => (x === e ? "{id}" : x)).join("/") + (q < 0 ? "" : path.slice(q));
}

function load(): Record<string, string[]> {
  try {
    return JSON.parse(window.sessionStorage.getItem(STORE) ?? "{}") as Record<string, string[]>;
  } catch {
    return {};
  }
}

const learned: Record<string, string[]> = load();
let current: (Shape & { at: number }) | null = null;
const started = new Map<string, { at: number; res: Promise<Response> }>();

function save() {
  try {
    window.sessionStorage.setItem(STORE, JSON.stringify(learned));
  } catch {
    /* storage blocked: it is learned again */
  }
}

/** noteRead records a read made right after a navigation. */
export function noteRead(path: string) {
  if (!current || Date.now() - current.at > LEARN_MS) return;
  const t = template(path, current.id);
  if (!t) return;
  const list = (learned[current.shape] ??= []);
  if (list.includes(t) || list.length >= MAX) return;
  list.push(t);
  save();
}

/** takeStarted returns the read already started for path, once. */
export function takeStarted(path: string): Promise<Response> | null {
  const s = started.get(path);
  if (!s) return null;
  started.delete(path);
  return Date.now() - s.at <= KEEP_MS ? s.res : null;
}

/** navigated starts the reads learned for the screen of hash. */
export function navigated(hash: string, start: (path: string) => Promise<Response>) {
  const now = Date.now();
  for (const [k, s] of started) if (now - s.at > KEEP_MS) started.delete(k);
  const sh = shapeOf(hash);
  current = sh ? { ...sh, at: now } : null;
  if (!sh) return;
  for (const t of learned[sh.shape] ?? []) {
    const path = t.split("{id}").join(seg(sh.id));
    if (started.has(path)) continue;
    const res = start(path);
    res.catch(() => {}); // a failed start is retried by the screen itself
    started.set(path, { at: now, res });
  }
}

/** forget clears what was learned (tests, sign-out). */
export function forget() {
  for (const k of Object.keys(learned)) delete learned[k];
  started.clear();
  current = null;
  save();
}
