// Scale simulation: node run.js N
// Loads N products through the merchant's CSV import, measures how fast the
// store sees them and answers, reverts a mass price change exactly, then
// deletes everything and checks every store table is back where it started.
const L = require('./lib');
const fs = require('fs');
const N = Number(process.argv[2] || 1000);
const OUT = `${L.S}/escala/result-${N}.json`;
const R = { n: N, at: new Date().toISOString(), phases: {} };
const log = (...a) => console.log(new Date().toISOString().slice(11, 19), ...a);
const save = () => fs.writeFileSync(OUT, JSON.stringify(R, null, 1));
// Tables that only ever grow: history, by design (who did what, what changed).
const HISTORY = new Set(['audit_log', 'outbox', 'price_history', 'import_jobs', 'bulk_jobs', 'idempotency_keys', 'stock_movements', 'inventory_ledger', 'search_terms_daily', 'ai_usage']);

async function allProducts() {
  const ids = []; let cursor = '';
  for (;;) {
    const r = await L.api('GET', `/v1/products?limit=200${cursor ? '&cursor=' + encodeURIComponent(cursor) : ''}`);
    for (const p of r.items || []) ids.push(p.id);
    cursor = r.nextCursor || r.next || '';
    if (!cursor || !(r.items || []).length) break;
  }
  return ids;
}
async function pool(items, conc, f) {
  let i = 0; const errs = [];
  await Promise.all(Array.from({ length: conc }, async () => { while (i < items.length) { const x = items[i++]; try { await f(x); } catch (e) { errs.push(e.message.slice(0, 160)); } } }));
  return errs;
}
async function cleanup(label) {
  const t0 = performance.now();
  const ids = await allProducts();
  // As a merchant would: the bulk action, 500 products per request.
  const chunks = []; for (let a = 0; a < ids.length; a += 500) chunks.push(ids.slice(a, a + 500));
  const errs = await pool(chunks, 2, (c) => L.api('POST', '/v1/products/bulk', { ids: c, action: 'delete' }));
  const tDel = performance.now() - t0;
  const cats = (await L.api('GET', '/v1/catalog/categories')).items || [];
  // children first
  const depth = (c) => { let d = 0, p = c; while (p && p.parentId) { d++; p = cats.find((x) => x.id === p.parentId); } return d; };
  cats.sort((a, b) => depth(b) - depth(a));
  const e2 = []; for (const c of cats) await L.api('DELETE', `/v1/catalog/categories/${c.id}`).catch((e) => e2.push(e.message.slice(0, 120)));
  const brands = (await L.api('GET', '/v1/catalog/brands')).items || [];
  for (const b of brands) await L.api('DELETE', `/v1/catalog/brands/${b.id}`).catch((e) => e2.push(e.message.slice(0, 120)));
  log(label, 'borrados', ids.length, 'productos en', Math.round(tDel), 'ms', `(${(ids.length / (tDel / 1000)).toFixed(0)}/s)`, '·', cats.length, 'categorías,', brands.length, 'marcas', errs.length + e2.length ? 'ERRORES ' + JSON.stringify([...errs, ...e2].slice(0, 5)) : '');
  return { products: ids.length, ms: Math.round(tDel), perSec: Math.round(ids.length / (tDel / 1000)), categories: cats.length, brands: brands.length, errors: [...errs, ...e2].slice(0, 20) };
}
async function storeTotal(q = '') {
  const r = await fetch(`${L.B}/s/${L.T}/main/c${q ? '?q=' + encodeURIComponent(q) : ''}`, { headers: { 'X-Forwarded-For': L.shopperIP() } });
  const h = await r.text();
  const m = h.match(/(\d[\d,]*)\s+productos?/i);
  return m ? Number(m[1].replace(/,/g, '')) : (/(Ningún|No encontramos|0 productos)/i.test(h) ? 0 : -1);
}

(async () => {
  await L.login();
  // 0. Clean start and baseline.
  R.phases.preclean = await cleanup('limpieza inicial');
  await L.sleep(1500);
  const base = L.snapshot();
  R.base = base; save();
  R.rssBase = L.serverRSS();
  log('foto inicial tomada; memoria del servidor', R.rssBase, 'MB');

  // 1. Load through CSV import, 8 000 products per file (< 5 MB).
  const per = 8000; const files = [];
  for (let a = 1; a <= N; a += per) files.push([a, Math.min(N, a + per - 1)]);
  const t0 = performance.now(); const imp = [];
  for (const [a, b] of files) {
    const csv = L.HEADER + '\n' + Array.from({ length: b - a + 1 }, (_, k) => L.product(a + k)).join('\n') + '\n';
    const fd = new FormData(); fd.append('file', new Blob([csv], { type: 'text/csv' }), `p-${a}.csv`);
    const tp = performance.now();
    const pv = await L.api('POST', '/v1/catalog/imports', fd);
    const tPreview = performance.now() - tp;
    const ta = performance.now();
    await L.api('POST', `/v1/catalog/imports/${pv.id}/apply`, {});
    let g;
    for (;;) { g = await L.api('GET', `/v1/catalog/imports/${pv.id}`); if (g.status === 'done' || g.status === 'failed') break; await L.sleep(250); }
    const tApply = performance.now() - ta;
    const n = b - a + 1;
    imp.push({ file: `${a}-${b}`, bytes: csv.length, previewMs: Math.round(tPreview), applyMs: Math.round(tApply), perSec: Math.round(n / (tApply / 1000)), status: g.status, created: g.created, failed: g.failed });
    log('importados', `${a}-${b}`, `(${(csv.length / 1048576).toFixed(1)} MB)`, 'vista previa', Math.round(tPreview), 'ms · aplicar', Math.round(tApply), 'ms ·', Math.round(n / (tApply / 1000)), 'productos/s', g.status, g.failed ? 'FALLIDOS ' + g.failed : '');
  }
  const tLoad = performance.now() - t0;
  // 2. Visible in the store (no indexer: the projection follows the outbox).
  const tv = performance.now(); let seen = -1;
  for (let i = 0; i < 600; i++) { seen = await storeTotal(); if (seen >= N) break; await L.sleep(100); }
  const visibleAfter = performance.now() - tv;
  R.phases.load = { files: imp, totalMs: Math.round(tLoad), perSec: Math.round(N / (tLoad / 1000)), visibleAfterImportMs: Math.round(visibleAfter), storeCount: seen, rssMB: L.serverRSS() };
  log('carga total', Math.round(tLoad), 'ms ·', R.phases.load.perSec, 'productos/s · la tienda muestra', seen, 'productos', Math.round(visibleAfter), 'ms después · memoria', R.phases.load.rssMB, 'MB');
  save();

  // 2b. Vectors (search by meaning): how long until every product has one.
  {
    const tv0 = performance.now();
    let sem = {};
    for (let i = 0; i < 1200; i++) { sem = (await L.api('GET', '/v1/ai/settings')).semantic || {}; if (sem.vectors >= N || sem.on === false) break; await L.sleep(250); }
    R.phases.vectors = { vectors: sem.vectors, products: sem.products, afterVisibleMs: Math.round(performance.now() - tv0), memoryMB: Math.round((sem.memoryBytes || 0) / 1048576 * 10) / 10, model: sem.model, rssMB: L.serverRSS() };
    log('vectores:', JSON.stringify(R.phases.vectors));
    save();
  }

  // 2c. Optional: with the whole catalog loaded, run another measurement
  // (APEXX_QA_WITH_LOAD, a shell command; for example the load time of every
  // panel view, tools/qa/carga/medir.js). Its output is kept in the result.
  if (process.env.APEXX_QA_WITH_LOAD) {
    const tw = performance.now();
    const out = require('child_process').execSync(process.env.APEXX_QA_WITH_LOAD, { maxBuffer: 1 << 26, stdio: ['ignore', 'pipe', 'inherit'] }).toString();
    R.phases.withLoad = { ms: Math.round(performance.now() - tw), output: out.slice(0, 20000) };
    log('medición con la carga completa:\n' + out.split('\n').slice(0, 30).join('\n'));
    save();
  }

  // 3. Latencies, one client and 16 at once.
  const mid = Math.ceil(N / 2);
  const pdpId = (await L.api('GET', `/v1/products?q=esc-${mid}&limit=1`)).items?.[0]?.id;
  const sf = (p) => () => fetch(L.B + p, { headers: { 'X-Forwarded-For': L.shopperIP() } }).then(async (r) => { await r.arrayBuffer(); return r.status; });
  const ad = (p) => () => fetch(L.B + p, { headers: { Authorization: 'Bearer ' + L.token() } }).then(async (r) => { await r.arrayBuffer(); return r.status; });
  const cases = {
    'tienda: portada': sf(`/s/${L.T}/main/index`),
    'tienda: catálogo pág. 1': sf(`/s/${L.T}/main/c`),
    'tienda: catálogo pág. 40': sf(`/s/${L.T}/main/c?page=40`),
    'tienda: categoría': sf(`/s/${L.T}/main/c/sudaderas`),
    'tienda: filtro marca+precio': sf(`/s/${L.T}/main/c?marca=sierra&min=500&max=1500`),
    'tienda: búsqueda exacta': sf(`/s/${L.T}/main/c?q=sudadera%20lino`),
    'tienda: búsqueda con error': sf(`/s/${L.T}/main/c?q=sudadra%20lnio`),
    'tienda: búsqueda sin resultados': sf(`/s/${L.T}/main/c?q=zzqxv`),
    'tienda: sugerencias (prefijo)': sf(`/s/${L.T}/api/suggest?q=chama`),
    'tienda: ficha de producto': sf(`/s/${L.T}/main/p/esc-${mid}`),
    'api: simular carrito': () => fetch(L.B + '/v1/carts/simulate', { method: 'POST', headers: { Authorization: 'Bearer ' + L.token(), 'Content-Type': 'application/json' }, body: JSON.stringify({ channel: 'mx', items: [{ sku: `ESC-${mid}-1`, qty: 2 }, { sku: `ESC-${mid}-2`, qty: 1 }] }) }).then(async (r) => { await r.arrayBuffer(); return r.status; }),
    'panel: lista de productos': ad('/v1/products?limit=50'),
    'panel: buscar producto': ad('/v1/products?q=chamarra%20maya&limit=50'),
    'panel: abrir producto': ad(`/v1/products/${pdpId}`),
    'panel: inicio (dashboard)': ad('/v1/reports/dashboard?range=7d'),
  };
  R.phases.latency = {};
  for (const [k, f] of Object.entries(cases)) {
    await f(); // warm
    const one = await L.timeIt(200, f, 1);
    const many = await L.timeIt(400, f, 16);
    R.phases.latency[k] = { serie: one, concurrente16: many };
    log(k.padEnd(32), 'p50', one.p50, 'p95', one.p95, 'p99', one.p99, 'ms | 16 a la vez: p50', many.p50, 'p95', many.p95, 'p99', many.p99, one.errors + many.errors ? `ERRORES ${one.errors + many.errors}` : '');
  }
  save();

  // 4. Write → visible: change a price, wait until the product page shows it.
  const prop = [];
  for (let i = 0; i < 20; i++) {
    const k = 1 + ((i * 7919) % N), sku = `ESC-${k}-1`;
    const cents = 100000 + i * 100 + 1; const want = (cents / 100).toLocaleString('en-US', { minimumFractionDigits: 2 });
    const t = performance.now();
    await L.api('POST', '/v1/bulk-operations', { kind: 'prices', channel: 'mx', items: [{ sku, amount: cents }] });
    for (let j = 0; j < 400; j++) { const h = await (await fetch(`${L.B}/s/${L.T}/main/p/esc-${k}`, { headers: { 'X-Forwarded-For': L.shopperIP() } })).text(); if (h.includes(want)) break; await L.sleep(5); }
    prop.push(performance.now() - t);
  }
  R.phases.propagation = L.stats(prop);
  log('cambio de precio → visible en la ficha:', JSON.stringify(R.phases.propagation), 'ms');
  save();

  // 5. Reversion of a mass price change: snapshot every price, +10 % on 500
  // products, check the store shows it, restore the exact original prices.
  const priceSnap = () => L.psql(`SELECT sku||'|'||channel||'|'||amount||'|'||coalesce(compare_at::text,'')||'|'||min_qty FROM prices WHERE tenant_id='${L.T}' ORDER BY 1`);
  const before = priceSnap();
  const ids500 = (await allProducts()).slice(0, 500);
  const tb = performance.now();
  const bulk = await L.api('POST', '/v1/products/bulk', { ids: ids500, action: 'price_percent', percent: 10 });
  const tBulk = performance.now() - tb;
  const changed = priceSnap() !== before;
  // restore exactly from the snapshot, through the merchant's bulk price job
  const items = before.split('\n').map((l) => { const [sku, , amount, cmp] = l.split('|'); const it = { sku, amount: Number(amount) }; if (cmp) it.compareAt = Number(cmp); return it; });
  const tr = performance.now(); const jobs = [];
  for (let a = 0; a < items.length; a += 20000) jobs.push((await L.api('POST', '/v1/bulk-operations', { kind: 'prices', channel: 'mx', items: items.slice(a, a + 20000) })).id);
  for (const id of jobs) for (;;) { const j = await L.api('GET', `/v1/bulk-operations/${id}`); if (j.status === 'done' || j.status === 'failed') break; await L.sleep(250); }
  const tRestore = performance.now() - tr;
  const after = priceSnap();
  const diffLines = before.split('\n').filter((l, i) => after.split('\n')[i] !== l).length;
  R.phases.priceReversion = { bulkChanged: bulk.changed ?? bulk.count ?? bulk, bulkMs: Math.round(tBulk), changedInDB: changed, restoreMs: Math.round(tRestore), restoredExactly: after === before, linesDifferent: diffLines };
  log('reversión de precios:', JSON.stringify(R.phases.priceReversion));
  save();

  // 6. Delete everything and compare every store table with the baseline.
  R.phases.delete = await cleanup('borrado total');
  for (let i = 0; i < 100 && (await storeTotal()) !== 0; i++) await L.sleep(100);
  await L.sleep(1500);
  const end = L.snapshot();
  const d = L.diff(base, end);
  const leftovers = Object.fromEntries(Object.entries(d).filter(([k]) => !HISTORY.has(k)));
  const history = Object.fromEntries(Object.entries(d).filter(([k]) => HISTORY.has(k)));
  R.phases.reversion = { storeCountAfter: await storeTotal(), searchAfter: await storeTotal('sudadera'), suggestAfter: (await (await fetch(`${L.B}/s/${L.T}/api/suggest?q=chama`, { headers: { 'X-Forwarded-For': L.shopperIP() } })).json()).products?.length ?? -1, leftovers, historyGrowth: history, rssMB: L.serverRSS() };
  log('después del borrado: la tienda muestra', R.phases.reversion.storeCountAfter, '· búsqueda', R.phases.reversion.searchAfter, '· sugerencias', R.phases.reversion.suggestAfter);
  log('tablas que NO volvieron a la foto inicial:', Object.keys(leftovers).length ? JSON.stringify(leftovers) : 'ninguna');
  log('historial que crece a propósito:', JSON.stringify(history));
  save();
})().catch((e) => { console.error('FALLÓ', e); R.error = String(e); save(); process.exit(1); });
