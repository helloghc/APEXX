// The panel is written in Spanish. To show it in another language we do not
// rewrite every screen: one observer watches the page and swaps each piece of
// text for its translation as it appears. A phrase with no translation stays
// in Spanish, so nothing ever breaks; it is just not translated yet.

export const PANEL_LANGUAGES = [
  { code: "es", name: "Español" },
  { code: "en", name: "English" },
  { code: "fr", name: "Français" },
  { code: "pt", name: "Português" },
  { code: "id", name: "Bahasa Indonesia" },
  { code: "ja", name: "日本語" },
] as const;

const KEY = "apexx_lang";
const loaders: Record<string, () => Promise<{ default: Record<string, string> }>> = {
  en: () => import("./i18n/en.json"),
  fr: () => import("./i18n/fr.json"),
  pt: () => import("./i18n/pt.json"),
  id: () => import("./i18n/id.json"),
  ja: () => import("./i18n/ja.json"),
};

export function currentLanguage(): string {
  try {
    const v = window.localStorage.getItem(KEY) ?? "";
    return v in loaders ? v : "es";
  } catch {
    return "es";
  }
}

/** Saves the choice and reloads, so every screen starts in the new language. */
export function setLanguage(code: string) {
  try {
    if (code === "es") window.localStorage.removeItem(KEY);
    else window.localStorage.setItem(KEY, code);
  } catch {
    /* storage blocked: the panel stays in Spanish */
  }
  window.location.reload();
}

const NUMBER = /[0-9][0-9.,]*[0-9]|[0-9]/g;

type Pattern = { re: RegExp; out: string; kinds: ("t" | "n")[] };
const patterns = new WeakMap<Record<string, string>, Pattern[]>();

/** Phrases with variable parts are stored as "Hola, {}" (text) and "# pedidos"
 * (a number), and a key may mix both: "#% en todo el pedido. Solo en {}". */
function patternsOf(dict: Record<string, string>): Pattern[] {
  let list = patterns.get(dict);
  if (!list) {
    list = Object.keys(dict)
      .filter((k) => k.includes("{}") && k.replace(/\{\}|#/g, "").trim().length >= 3)
      .sort((a, b) => b.length - a.length)
      .map((k) => {
        const kinds: ("t" | "n")[] = [];
        const src = k.split(/(\{\}|#)/).map((x) => {
          if (x === "{}") { kinds.push("t"); return "(.+?)"; }
          if (x === "#") { kinds.push("n"); return "([0-9][0-9.,]*[0-9]|[0-9])"; }
          return x.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
        }).join("");
        return { re: new RegExp("^" + src + "$", "s"), out: dict[k] ?? "", kinds };
      });
    patterns.set(dict, list);
  }
  return list;
}

/** A variable part is translated whole, or item by item when it is a list ("a, b"). */
function part(dict: Record<string, string>, v: string): string {
  if (dict[v] !== undefined) return dict[v]!;
  if (v.includes(", ")) return v.split(", ").map((x) => dict[x] ?? x).join(", ");
  return v;
}

/** Translates one phrase; "3 pedidos" is looked up as "# pedidos". Exported for tests. */
export function translate(dict: Record<string, string>, text: string): string | null {
  const core = text.trim().replace(/\s+/g, " ");
  if (!core) return null;
  let out = dict[core];
  if (out === undefined) {
    const nums = core.match(NUMBER);
    const numbered = nums ? dict[core.replace(NUMBER, "#")] : undefined;
    if (nums && numbered !== undefined) {
      let i = 0;
      out = numbered.replace(/#/g, () => nums[i++] ?? "#");
    } else {
      for (const p of patternsOf(dict)) {
        const m = p.re.exec(core);
        if (!m) continue;
        // Text parts may themselves be known phrases ("Agregar {}" + "Marca");
        // numbers go back in their order.
        const texts: string[] = [], numbers: string[] = [];
        p.kinds.forEach((k, j) => (k === "t" ? texts : numbers).push(m[j + 1] ?? ""));
        let ti = 0, ni = 0;
        out = p.out.replace(/\{\}|#/g, (x) => (x === "#" ? numbers[ni++] ?? "#" : part(dict, texts[ti++] ?? "")));
        break;
      }
      if (out === undefined) return null;
    }
  }
  const lead = text.slice(0, text.length - text.trimStart().length);
  const trail = text.slice(text.trimEnd().length);
  return lead + out + trail;
}

const ATTRS = ["placeholder", "aria-label", "title", "alt"];

/** Starts translating the panel if the person chose a language other than Spanish. */
export async function startPanelLanguage() {
  const lang = currentLanguage();
  if (lang === "es") return;
  let dict: Record<string, string>;
  try {
    const load = loaders[lang];
    if (!load) return;
    dict = (await load()).default;
  } catch {
    return;
  }
  document.documentElement.lang = lang;
  const done = new WeakMap<Node, string>();
  const skip = (el: Element | null) => !el || !!el.closest("script,style,textarea,[data-no-translate],[contenteditable='true']");
  const textNode = (n: Node) => {
    const v = n.nodeValue ?? "";
    if (done.get(n) === v || skip(n.parentElement)) return;
    const t = translate(dict, v);
    if (t !== null && t !== v) {
      done.set(n, t);
      n.nodeValue = t;
    }
  };
  const element = (el: Element) => {
    if (skip(el)) return;
    for (const a of ATTRS) {
      const v = el.getAttribute(a);
      if (!v) continue;
      const t = translate(dict, v);
      if (t !== null && t !== v) el.setAttribute(a, t);
    }
  };
  const walk = (root: Node) => {
    if (root.nodeType === Node.TEXT_NODE) return textNode(root);
    if (root.nodeType !== Node.ELEMENT_NODE) return;
    element(root as Element);
    const w = document.createTreeWalker(root, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT);
    while (w.nextNode()) {
      if (w.currentNode.nodeType === Node.TEXT_NODE) textNode(w.currentNode);
      else element(w.currentNode as Element);
    }
  };
  walk(document.body);
  new MutationObserver((list) => {
    for (const m of list) {
      if (m.type === "characterData") textNode(m.target);
      else if (m.type === "attributes") element(m.target as Element);
      else m.addedNodes.forEach(walk);
    }
  }).observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ATTRS });
}
