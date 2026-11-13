// Sandboxes during the relay test (APEXX_QA_SANDBOX=1 in run.sh):
//   node sandbox.js setup  -> creates sandbox 1 with the whole catalog, a
//                             preview link, a session inside it, a change and
//                             a publish scheduled to run in the middle of the
//                             relay; prints shell variables for load.js.
//   node sandbox.js copy URL -> starts a second sandbox's copy on one node
//                             (the one about to be replaced).
//   node sandbox.js check  -> the copy finished on a node being replaced, the
//                             scheduled publish ran exactly once, and deleting
//                             both sandboxes leaves no rows.
const L = require('../escala/lib');
const fs = require('fs');
const STATE = (process.env.APEXX_QA_WORK || '.') + '/sandbox-state.json';
const mode = process.argv[2];
const ready = async (id) => {
  for (let i = 0; i < 1200; i++) {
    const v = await L.api('GET', '/v1/sandboxes/' + id);
    if (v.sandbox.status !== 'copying') return v.sandbox;
    await L.sleep(250);
  }
  throw new Error('copy did not end');
};
const logout = (tok) => fetch(L.B + '/v1/auth/logout', { method: 'POST', headers: { Authorization: 'Bearer ' + tok } });
(async () => {
  if (mode === 'setup') {
    await L.login();
    for (const v of (await L.api('GET', '/v1/sandboxes')).items) await L.api('DELETE', '/v1/sandboxes/' + v.sandbox.id, undefined, true);
    const t0 = Date.now();
    const id = (await L.api('POST', '/v1/sandboxes', { name: 'Relevo' })).sandbox.id;
    const sb = await ready(id);
    if (sb.status !== 'ready') throw new Error('sandbox 1: ' + sb.status + ' ' + sb.error);
    const link = (await L.api('POST', `/v1/sandboxes/${id}/preview`, { hours: 2 })).url;
    const tok = decodeURIComponent(link.split('sbx=')[1]);
    // A separate session goes inside (entering ends the session it uses).
    const prod = L.token();
    await L.login();
    const inside = (await L.api('POST', `/v1/sandboxes/${id}/enter`)).token;
    L.setToken(inside);
    const p = (await L.api('GET', '/v1/products?limit=1')).items[0];
    const full = await L.api('GET', '/v1/products/' + p.id);
    await L.api('PUT', '/v1/products/' + p.id, { handle: full.handle, title: full.title + ' · relevo', categories: full.categories, variants: full.variants.map((x) => ({ sku: x.sku, title: x.title, options: x.options })) });
    const runAt = new Date(Date.now() + Number(process.env.SCHEDULE_IN || 65) * 1000).toISOString();
    const sch = (await L.api('POST', `/v1/sandboxes/${id}/publish`, { areas: ['catalog'], runAt })).schedule;
    fs.writeFileSync(STATE, JSON.stringify({ id, product: p.id, title: full.title, schedule: sch.id, prod, copyMs: Date.now() - t0 }));
    console.log(`export APEXX_QA_SBX_ID=${id} APEXX_QA_SBX_COOKIE='${tok}' APEXX_QA_SBX_INSIDE='${inside}'`);
    console.error(`sandbox 1 listo en ${Date.now() - t0} ms; publicación programada ${runAt}`);
  } else if (mode === 'copy') {
    const st = JSON.parse(fs.readFileSync(STATE));
    const r = await fetch(process.argv[3] + '/v1/sandboxes', { method: 'POST', headers: { Authorization: 'Bearer ' + st.prod, 'Content-Type': 'application/json' }, body: JSON.stringify({ name: 'Relevo 2' }) });
    const j = await r.json();
    if (r.status !== 202) throw new Error('copy on node: ' + r.status + ' ' + JSON.stringify(j));
    st.second = j.sandbox.id;
    fs.writeFileSync(STATE, JSON.stringify(st));
    console.error('copia del sandbox 2 iniciada en', process.argv[3]);
  } else if (mode === 'check') {
    const st = JSON.parse(fs.readFileSync(STATE));
    L.setToken(st.prod);
    const out = {};
    const s2 = await ready(st.second);
    out.secondCopy = { status: s2.status, error: s2.error || '', copyMs: s2.copyMs, products: Number(L.psql(`SELECT count(*) FROM products WHERE tenant_id='${st.second}'`)) };
    for (let i = 0; i < 240; i++) {
      const x = (await L.api('GET', '/v1/sandbox/schedules')).items.find((y) => y.id === st.schedule);
      out.schedule = { status: x.status, result: x.result };
      if (x.status !== 'pending' && x.status !== 'running') break;
      await L.sleep(500);
    }
    out.publishes = Number(L.psql(`SELECT count(*) FROM sandbox_publishes WHERE sandbox_id='${st.id}'`));
    out.productionTitle = L.psql(`SELECT title FROM products WHERE tenant_id='${L.T}' AND id='${st.product}'`);
    const pubs = (await L.api('GET', '/v1/sandbox/publishes')).items.filter((x) => x.sandboxId === st.id && !x.revertedAt);
    for (const x of pubs) await L.api('POST', `/v1/sandbox/publishes/${x.id}/revert`, {});
    out.revertedTitle = L.psql(`SELECT title FROM products WHERE tenant_id='${L.T}' AND id='${st.product}'`);
    for (const id of [st.id, st.second]) await L.api('DELETE', '/v1/sandboxes/' + id, undefined, true);
    await logout(st.prod); // the test leaves no session behind
    out.tenantsLeft = Number(L.psql(`SELECT count(*) FROM tenants WHERE parent_id='${L.T}'`));
    const ok = out.secondCopy.status === 'ready' && out.secondCopy.products > 0 && out.schedule.status === 'done' && out.publishes === 1 &&
      out.productionTitle === st.title + ' · relevo' && out.revertedTitle === st.title && out.tenantsLeft === 0;
    console.log(JSON.stringify({ ...out, ok }));
    process.exit(ok ? 0 : 1);
  }
})().catch((e) => { console.error('FALLÓ', e.message); process.exit(1); });
