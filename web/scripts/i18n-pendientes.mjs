// Panel phrases: JSX text and user-facing string literals, minus those already translated.
import fs from 'fs'; import path from 'path';
const root = process.argv[2], dict = JSON.parse(fs.readFileSync(path.join(root, 'i18n/en.json'), 'utf8'));
const files = []; const walk = (d) => { for (const f of fs.readdirSync(d)) { const p = path.join(d, f); if (fs.statSync(p).isDirectory()) { if (f !== 'i18n') walk(p); } else if (/\.tsx?$/.test(f) && !/\.test\./.test(f)) files.push(p); } }; walk(root);
const out = new Map();
const spanish = (s) => /[a-záéíóúñ]{3}/i.test(s) && /\s|[áéíóúñ¿¡]/.test(s) || /^[A-ZÁÉÍÓÚ][a-záéíóúñ]+$/.test(s);
const codeish = (s) => /^[a-z0-9_.\-/:#]+$/.test(s) || /[{};=<>]|=>|\bclassName\b|^(GET|POST|PUT|DELETE) |^\/v1|^https?:|^[a-z-]+:[a-z]|^\w+\(/.test(s) || /^(bg|text|flex|grid|border|rounded|px|py|p|m|w|h|gap|items|justify|font|min|max|sm|md|lg|xl)-/.test(s);
const tw = (s) => s.split(' ').filter((t) => /^!?([a-z]+:)*-?[a-z]+(-[a-z0-9\[\]\/.%_()#,]+)+$/.test(t) || /^(grid|flex|block|hidden|truncate|relative|absolute|sticky|underline|italic|uppercase|border|rounded|shadow|inline|contents|grow|shrink-0|tabular-nums)$/.test(t)).length;
const SKIP = new Set(fs.existsSync(path.join(root, 'i18n/sin-traducir.txt')) ? fs.readFileSync(path.join(root, 'i18n/sin-traducir.txt'), 'utf8').split('\n') : []);
const add = (s, f) => { if (SKIP.has(s.replace(/\s+/g, ' ').trim())) return; s = s.replace(/\s+/g, ' ').trim(); if (tw(s) >= 1 && tw(s) >= s.split(' ').length / 3) return; if (/^[)(,:?&|!.]/.test(s) || /\?\.|&&|\|\||: string|Promise|React\.|=>/.test(s)) return; if (s.length < 2 || s.length > 400 || !spanish(s) || codeish(s)) return; if (dict[s] !== undefined) return; if (!out.has(s)) out.set(s, f); };
for (const f of files) {
  const src = fs.readFileSync(f, 'utf8'), rel = path.relative(root, f);
  for (const m of src.matchAll(/>([^<>{}]*[A-Za-zÁÉÍÓÚáéíóúñ][^<>{}]*)</g)) add(m[1], rel);           // JSX text
  for (const m of src.matchAll(/"((?:[^"\\\n]|\\.)*)"/g)) add(m[1].replace(/\\"/g, '"'), rel);          // "..."
  for (const m of src.matchAll(/`((?:[^`\\]|\\.)*)`/g)) if (!/className|\bbg-|\btext-/.test(m[1])) add(m[1].replace(/\$\{[^}]*\}/g, '{}'), rel); // `... ${x}` -> {}
}
const list = [...out.keys()].sort();
fs.writeFileSync(process.argv[3], list.join('\n') + '\n');
console.log(list.length, 'pendientes de', files.length, 'archivos; ya traducidas', Object.keys(dict).length);
