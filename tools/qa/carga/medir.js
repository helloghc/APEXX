// Load time of every panel view, measured in a real browser.
//   node tools/qa/carga/medir.js BASE STORE EMAIL PASSWORD OUT.json [maxRoutes]
//   RTT=80 MBPS=20 node …  simulates a real network (round trip in ms and
//   bandwidth), where requests chained one after another cost a trip each.
// For each route reachable from the menu (and one detail per list) it
// navigates in-app (hash change, as a person clicking) and measures until
// the view is ready: no /v1 request in flight, no skeleton, no "Cargando".
// It also records each API call of the view with its time, and the cold
// first load (HTML + JS + first data) with the JavaScript downloaded.
const { chromium } = require('playwright');
const fs = require('fs');
const [BASE, STORE, EMAIL, PASSWORD, OUT, MAX] = process.argv.slice(2);
const maxRoutes = Number(MAX || 250);
const now = () => Number(process.hrtime.bigint() / 1000n) / 1000; // ms

(async () => {
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport: { width: 1366, height: 900 }, ignoreHTTPSErrors: true }); // local TLS proxy for HTTP/2
  const p = await ctx.newPage();
  if (process.env.RTT) {
    const cdp = await ctx.newCDPSession(p);
    const bps = (Number(process.env.MBPS || 20) * 1e6) / 8;
    await cdp.send('Network.emulateNetworkConditions', { offline: false, latency: Number(process.env.RTT), downloadThroughput: bps, uploadThroughput: bps });
  }
  const r = await p.request.post(BASE + '/v1/auth/login', { data: { store: STORE, email: EMAIL, password: PASSWORD } });
  const tok = (await r.json()).token;
  await ctx.addCookies([{ name: 'apexx_session', value: tok, url: BASE }]);

  const inflight = new Set();
  let calls = [];
  let jsBytes = 0;
  p.on('request', (q) => { if (q.url().includes('/v1/')) inflight.add(q); });
  const done = async (q) => {
    if (!q.url().includes('/v1/')) return;
    inflight.delete(q);
    const t = q.timing();
    const u = new URL(q.url());
    calls.push({ api: q.method() + ' ' + u.pathname + (u.search.length > 1 ? '?…' : ''), ms: Math.round((t.responseEnd > 0 ? t.responseEnd : 0) - Math.max(t.requestStart, 0)) });
  };
  p.on('requestfinished', done);
  p.on('requestfailed', done);
  p.on('response', async (res) => {
    if (res.url().endsWith('.js')) { try { jsBytes += (await res.body()).length; } catch {} }
  });
  const busy = () => p.evaluate(() => {
    const vis = (e) => !!(e.offsetWidth || e.offsetHeight || e.getClientRects().length);
    if ([...document.querySelectorAll('[aria-busy="true"], .animate-pulse')].some(vis)) return true;
    const main = document.querySelector('main') || document.body;
    return /Cargando…|Cargando\.\.\./.test(main.innerText || '');
  });
  const waitReady = async (t0) => {
    let quietSince = 0;
    for (;;) {
      const t = now();
      if (t - t0 > 15000) return { ms: t - t0, timeout: true };
      const idle = inflight.size === 0 && !(await busy());
      if (idle) {
        if (!quietSince) quietSince = t;
        if (now() - quietSince >= 120) return { ms: quietSince - t0 };
      } else quietSince = 0;
      await new Promise((r) => setTimeout(r, 10));
    }
  };
  const res = { rtt: Number(process.env.RTT || 0), base: BASE, store: STORE, at: new Date().toISOString(), cold: {}, routes: [] };

  // Cold first load.
  calls = []; jsBytes = 0;
  let t0 = now();
  await p.goto(BASE + '/admin/#inicio');
  const cold = await waitReady(t0);
  res.cold = { ms: Math.round(cold.ms), jsKB: Math.round(jsBytes / 1024), calls };

  const collect = async () => (await p.evaluate(() => [...document.querySelectorAll('a[href^="#"]')].map((a) => a.getAttribute('href')))).filter((h) => h && h.length > 1 && !h.includes('?'));
  const seen = new Set(['#inicio']);
  const queue = await collect();
  const detailOf = new Set(); // one detail page per list
  while (queue.length && res.routes.length < maxRoutes) {
    const h = queue.shift();
    if (seen.has(h)) continue;
    const parts = h.slice(1).split('/');
    const key = parts.length >= 2 && /[_-][0-9a-f]{6,}|^\d+$/.test(parts[parts.length - 1]) ? parts.slice(0, -1).join('/') + '/:id' : h;
    if (key !== h && detailOf.has(key)) { seen.add(h); continue; }
    if (key !== h) detailOf.add(key);
    seen.add(h);
    if (/salir|logout/.test(h)) continue;
    calls = []; jsBytes = 0;
    t0 = now();
    await p.evaluate((x) => { location.hash = x; }, h);
    const w = await waitReady(t0);
    const apiMax = calls.reduce((m, c) => Math.max(m, c.ms), 0);
    res.routes.push({ route: key, ms: Math.round(w.ms), timeout: !!w.timeout, jsKB: Math.round(jsBytes / 1024), calls: calls.length, apiMaxMs: apiMax, slowest: calls.sort((a, b) => b.ms - a.ms).slice(0, 3) });
    for (const n of await collect()) if (!seen.has(n)) queue.push(n);
  }
  res.routes.sort((a, b) => b.ms - a.ms);
  const ms = res.routes.map((r) => r.ms).sort((a, b) => a - b);
  res.summary = { routes: ms.length, p50: ms[Math.floor(ms.length / 2)], p95: ms[Math.floor(ms.length * 0.95)], max: ms[ms.length - 1], over200: ms.filter((x) => x > 200).length, timeouts: res.routes.filter((r) => r.timeout).length };
  fs.writeFileSync(OUT, JSON.stringify(res, null, 1));
  console.log(JSON.stringify({ cold: { ms: res.cold.ms, jsKB: res.cold.jsKB }, ...res.summary }));
  for (const r of res.routes.slice(0, 25)) console.log(String(r.ms).padStart(6), 'ms', r.route.padEnd(42), 'api máx', String(r.apiMaxMs).padStart(5), 'ms', r.calls, 'llamadas', r.jsKB ? r.jsKB + ' KB JS' : '', r.slowest[0] ? '· ' + r.slowest[0].api + ' ' + r.slowest[0].ms + ' ms' : '');
  await b.close();
})().catch((e) => { console.error(e); process.exit(1); });
