// Latencies and write-to-visible on the products already loaded: node measure.js N
const L = require('./lib');
const fs = require('fs');
const N = Number(process.argv[2] || 50000);
const R = { n: N, at: new Date().toISOString(), phases: {} };
const OUT = `${L.S}/escala/measure-${N}.json`;
const log = (...a) => console.log(new Date().toISOString().slice(11, 19), ...a);
const save = () => fs.writeFileSync(OUT, JSON.stringify(R, null, 1));
(async () => {
  await L.login();
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

})();
