const [B, T, H] = process.argv.slice(2);
const pct = (a, p) => !a.length ? 'n/d' : a.sort((x, y) => x - y)[Math.min(a.length - 1, Math.floor(a.length * p))].toFixed(1);
async function run(name, n, conc, fn) {
  const times = []; let fail = 0, i = 0, limited = 0; const t0 = performance.now();
  await Promise.all(Array.from({ length: conc }, async () => { while (i++ < n) { const s = performance.now(); try { const r = await fn(); const st = r.status; await r.arrayBuffer(); if (st === 429) { limited++; continue; } if (st >= 400) fail++; } catch { fail++; } times.push(performance.now() - s); } }));
  const secs = (performance.now() - t0) / 1000; n = times.length || 1;
  console.log(name.padEnd(34), `p50 ${pct(times, .5)} ms  p95 ${pct(times, .95)} ms  p99 ${pct(times, .99)} ms  ${(n / secs).toFixed(0)} req/s  fallos ${fail}${limited ? '  (frenadas por límite: ' + limited + ')' : ''}`);
}
const S = `${B}/s/${T}`;
const sim = () => fetch(`${S}/api/simulate`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ channel: 'mx', postalCode: '06600', items: [{ sku: 'QA-00001-1', qty: 1 }, { sku: 'QA-00002-1', qty: 2 }], coupons: [] }) });
await run('Inicio (página armada)', 90, 6, () => fetch(`${S}/main/index`));
await run('Catálogo con filtros', 90, 6, () => fetch(`${S}/main/c?orden=precio-asc`));
await run('Búsqueda con sinónimo', 90, 6, () => fetch(`${S}/main/c?q=remera`));
await run('Ficha de producto con opiniones', 90, 6, () => fetch(`${S}/main/p/${H}`));
await run('Sugerencias al escribir', 90, 6, () => fetch(`${S}/api/suggest?q=play`));
await run('Simular carrito (envío+impuestos)', 90, 6, sim);
