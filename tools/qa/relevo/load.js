// Constant traffic through the load balancer while nodes are replaced:
//   node load.js SECONDS
// Mix of storefront pages, storefront API, panel reads and a POST. Every
// response that is not what the same request answered before the relay
// (5xx, 502 from the proxy, connection errors) counts as a failure.
const L = require('../escala/lib');
const SECS = Number(process.argv[2] || 60);
const C = Number(process.env.APEXX_QA_CONCURRENCY || 16);
(async () => {
  await L.login();
  const sf = (p) => () => fetch(L.B + p, { headers: { 'X-Forwarded-For': L.shopperIP() } });
  const ad = (p) => () => fetch(L.B + p, { headers: { Authorization: 'Bearer ' + L.token() } });
  const cases = {
    portada: sf(`/s/${L.T}/main/index`),
    catalogo: sf(`/s/${L.T}/main/c`),
    busqueda: sf(`/s/${L.T}/main/c?q=sudadra`),
    sugerencias: sf(`/s/${L.T}/api/suggest?q=cha`),
    'panel-lista': ad('/v1/products?limit=50'),
    'panel-inicio': ad('/v1/setup'),
    'simular-carrito': () => fetch(L.B + '/v1/carts/simulate', { method: 'POST', headers: { Authorization: 'Bearer ' + L.token(), 'Content-Type': 'application/json' }, body: JSON.stringify({ channel: 'mx', items: [] }) }),
  };
  const names = Object.keys(cases);
  const expect = {};
  for (const n of names) {
    const r = await cases[n]();
    await r.arrayBuffer();
    expect[n] = r.status;
  }
  const res = { total: 0, ok: 0, fail: 0, failures: {}, slow: 0, maxMs: 0, perSecond: [] };
  const end = Date.now() + SECS * 1000;
  let i = 0;
  let bucket = { t: 0, n: 0, f: 0 };
  const worker = async () => {
    while (Date.now() < end) {
      const n = names[i++ % names.length];
      const t0 = performance.now();
      let st;
      try {
        const r = await cases[n]();
        await r.arrayBuffer();
        st = r.status;
      } catch (e) {
        st = 'error:' + (e.cause?.code || e.message);
      }
      const ms = performance.now() - t0;
      res.total++;
      res.maxMs = Math.max(res.maxMs, ms);
      if (ms > 1000) res.slow++;
      const sec = Math.floor((Date.now() - (end - SECS * 1000)) / 1000);
      if (sec !== bucket.t) { res.perSecond.push(bucket); bucket = { t: sec, n: 0, f: 0 }; }
      bucket.n++;
      if (st === expect[n]) res.ok++;
      else {
        res.fail++;
        bucket.f++;
        const k = `${n} ${st}`;
        res.failures[k] = (res.failures[k] || 0) + 1;
      }
    }
  };
  await Promise.all(Array.from({ length: C }, worker));
  res.perSecond.push(bucket);
  res.expect = expect;
  res.maxMs = Math.round(res.maxMs);
  console.log(JSON.stringify(res));
})();
