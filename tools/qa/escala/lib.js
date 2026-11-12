// Shared helpers for the scale simulation: HTTP with timing, DB snapshots,
// catalog generation and latency statistics.
const { execFileSync } = require('child_process');
const fs = require('fs');
// Configuration comes from the environment; nothing secret lives here.
//   APEXX_QA_BASE      server (default http://127.0.0.1:8097)
//   APEXX_QA_TENANT    store made with qa-setup (default escala)
//   APEXX_QA_EMAIL     owner account (default dueno@qa.apexx.test)
//   APEXX_QA_PASSWORD  its password (required)
//   APEXX_QA_DB_URL    database URL with a role that can read every store table (required)
//   APEXX_QA_OUT       folder for results (default ./out)
const need = (k) => { const v = process.env[k]; if (!v) { console.error(`falta ${k}`); process.exit(2); } return v; };
const S = process.env.APEXX_QA_OUT || './out';
fs.mkdirSync(S + '/escala', { recursive: true });
const B = process.env.APEXX_QA_BASE || 'http://127.0.0.1:8097';
const T = process.env.APEXX_QA_TENANT || 'escala';
const DBURL = need('APEXX_QA_DB_URL');
const EMAIL = process.env.APEXX_QA_EMAIL || 'dueno@qa.apexx.test';
const PASSWORD = need('APEXX_QA_PASSWORD');

const psql = (sql) => execFileSync('psql', [DBURL, '-Atc', sql], { maxBuffer: 1 << 28 }).toString().trim();

let token = '';
async function login() {
  const r = await fetch(B + '/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ store: T, email: EMAIL, password: PASSWORD }) });
  token = (await r.json()).token;
}
async function api(method, path, body, raw) {
  const h = { Authorization: 'Bearer ' + token };
  let b = body;
  if (body && !(body instanceof FormData)) { h['Content-Type'] = 'application/json'; b = JSON.stringify(body); }
  let r;
  for (let attempt = 0; ; attempt++) {
    // A keep-alive connection the server just closed fails with EPIPE /
    // "fetch failed": retry, a few times, with a short pause.
    try { r = await fetch(B + path, { method, headers: h, body: b }); break; } catch (e) { if (attempt >= 4) throw e; await new Promise((ok) => setTimeout(ok, 200 * (attempt + 1))); }
  }
  const t = await r.text();
  if (raw) return { status: r.status, text: t };
  let j; try { j = JSON.parse(t); } catch { j = t; }
  if (r.status >= 400) throw new Error(method + ' ' + path + ' ' + r.status + ' ' + t.slice(0, 300));
  return j;
}

// Every table with store data, and its row count for the scale store.
let tables = null;
function snapshot() {
  if (!tables) tables = psql(`SELECT table_name FROM information_schema.columns WHERE column_name='tenant_id' AND table_schema='public' ORDER BY 1`).split('\n');
  const q = tables.map((t) => `SELECT '${t}', count(*) FROM "${t}" WHERE tenant_id='${T}'`).join(' UNION ALL ');
  const out = {};
  for (const line of psql(q).split('\n')) { const [t, n] = line.split('|'); out[t] = Number(n); }
  return out;
}
function diff(a, b) {
  const d = {};
  for (const k of Object.keys(b)) if (a[k] !== b[k]) d[k] = [a[k], b[k]];
  return d;
}

// Realistic catalog: varied words so search has work to do.
const KIND = ['Playera', 'Sudadera', 'Pantalón', 'Chamarra', 'Vestido', 'Falda', 'Camisa', 'Short', 'Tenis', 'Bota', 'Gorra', 'Mochila', 'Calcetas', 'Bufanda', 'Cinturón', 'Lentes', 'Reloj', 'Bolsa', 'Sandalia', 'Suéter'];
const MAT = ['algodón', 'lino', 'mezclilla', 'piel', 'lana', 'seda', 'poliéster', 'bambú', 'gamuza', 'nylon'];
const COLOR = ['negro', 'blanco', 'azul marino', 'rojo', 'verde olivo', 'gris', 'beige', 'café', 'rosa', 'mostaza'];
const BRAND = ['Sierra', 'Pacífico', 'Norte', 'Bajío', 'Maya', 'Volcán', 'Laguna', 'Cenote', 'Huasteca', 'Barranca', 'Desierto', 'Selva'];
const CAT = ['Ropa > Playeras', 'Ropa > Sudaderas', 'Ropa > Pantalones', 'Ropa > Vestidos', 'Calzado > Tenis', 'Calzado > Botas', 'Accesorios > Gorras', 'Accesorios > Bolsas', 'Accesorios > Relojes', 'Ropa > Chamarras'];
const q = (s) => (/[",\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s);
function product(i) {
  const k = KIND[i % KIND.length], m = MAT[(i >> 2) % MAT.length], c = COLOR[(i >> 3) % COLOR.length], br = BRAND[(i >> 1) % BRAND.length];
  const title = `${k} ${br} de ${m} ${c} ${i}`;
  const price = (149 + (i * 37) % 2850).toFixed(2), cmp = (Number(price) * 1.25).toFixed(2);
  const handle = `esc-${i}`;
  const rows = [];
  for (const [j, talla] of [['1', 'CH'], ['2', 'G']].entries()) {
    rows.push([j ? handle : handle, j ? '' : title, j ? '' : `${k} de ${m} en color ${c}. Hecho en México, colección ${br}.`, j ? '' : 'active', j ? '' : br, j ? '' : CAT[i % CAT.length], j ? '' : k, j ? '' : `${m}, ${c}`, '', '', 'Talla', talla[1], '', '', '', '', `ESC-${i}-${talla[0]}`, '', String(200 + (i % 9) * 50), price, cmp, String((i * 7 + j) % 60)].map(q).join(','));
  }
  return rows.join('\n');
}
const HEADER = 'handle,title,description,status,brand,categories,type,tags,seo_title,seo_description,option1_name,option1_value,option2_name,option2_value,option3_name,option3_value,sku,barcode,weight_grams,price,compare_at_price,stock';

function stats(ms) {
  const t = [...ms].sort((a, b) => a - b);
  const at = (p) => t[Math.min(t.length - 1, Math.floor(p * t.length))];
  return { n: t.length, p50: +at(0.5).toFixed(2), p95: +at(0.95).toFixed(2), p99: +at(0.99).toFixed(2), max: +t[t.length - 1].toFixed(2) };
}
// Every request comes from a different shopper address (the server runs
// with APEXX_TRUST_PROXY, as behind Caddy), and any answer that is not 2xx is
// counted, never timed: a fast 429 must not pass for a fast page.
let ipN = 0;
const shopperIP = () => { ipN++; return `10.${(ipN >> 16) & 255}.${(ipN >> 8) & 255}.${ipN & 255}`; };
async function timeIt(n, f, conc = 1) {
  const ms = [];
  let i = 0, bad = 0;
  const worker = async () => { while (i < n) { i++; const s = performance.now(); const st = await f(i); if (typeof st === 'number' && (st < 200 || st >= 300)) { bad++; continue; } ms.push(performance.now() - s); } };
  await Promise.all(Array.from({ length: conc }, worker));
  return { ...stats(ms.length ? ms : [0]), errors: bad };
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
function serverRSS() {
  try { const pid = execFileSync('pgrep', ['-x', 'apexx']).toString().trim().split('\n')[0]; return Math.round(Number(fs.readFileSync(`/proc/${pid}/status`, 'utf8').match(/VmRSS:\s+(\d+)/)[1]) / 1024); } catch { return -1; }
}

module.exports = { shopperIP, S, B, T, psql, login, api, snapshot, diff, product, HEADER, stats, timeIt, sleep, serverRSS, token: () => token };
