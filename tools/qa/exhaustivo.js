// Exhaustive panel audit: every route, every button, every dialog, every form
// (empty and with bad data), keyboard-only use, per role and device.
// node exhaustivo.js BASE TENANT EMAIL PASS LABEL DEVICE OUT [maxRoutes]
const { chromium, devices } = require('playwright');
const fs = require('fs');
const [B, T, EMAIL, PASS, LABEL, DEVICE, OUT, MAXR] = process.argv.slice(2);
const MAX_ROUTES = Number(MAXR || 400);
const DESTRUCTIVE = /^(eliminar|borrar|quitar|desactivar|revocar|archivar|cerrar sesión|cancelar (pedido|cotización|suscripción|devolución)|reembolsar|deshacer publicación|restablecer|apagar|dejar de copiar|desconectar|rechazar|eliminar de todos modos|borrar para siempre|borrar sus datos)/i;
const SKIP = /^(cerrar sesión|cambiar de tienda|idioma|español|english|tema)$/i;
let LAST = '';
const BAD = {
  email: 'no-es-correo', number: '-99999999999', numeric: 'abc', url: 'javascript:alert(1)',
  text: '<script>window.__xss=1</script><img src=x onerror="window.__xss=1">' + 'x'.repeat(300),
};

(async () => {
  const b = await chromium.launch();
  const ctxOpts = DEVICE === 'tel' ? { ...devices['iPhone 13'] } : { viewport: { width: 1366, height: 900 } };
  const ctx = await b.newContext(ctxOpts);
  const p = await ctx.newPage();
  const res = { label: LABEL, device: DEVICE, routes: {}, summary: {} };
  let cur = null;
  const note = (kind, detail) => {
    const r = res.routes[cur] || (res.routes[cur] = { issues: [] });
    const key = kind + '|' + detail;
    if (!r.issues.some((x) => x.key === key)) r.issues.push({ key, kind, detail: String(detail).slice(0, 220) });
  };
  p.on('pageerror', (e) => note('excepcion', e.message + ' | tras: ' + LAST + ' | ' + String(e.stack||'').split('\n').slice(1,4).join(' ; ')));
  p.on('console', (m) => { if (m.type() === 'error' && !/favicon|Failed to load resource: the server responded with a status of (401|403|404|409|422|400|429)/.test(m.text())) note('consola', m.text()); });
  p.on('response', (r) => { if (r.status() >= 500) note('5xx', r.status() + ' ' + r.request().method() + ' ' + r.url().replace(B, '')); });
  p.on('dialog', async (d) => { note('dialogo-nativo', d.type() + ': ' + d.message()); await d.dismiss().catch(() => {}); });

  await p.goto(B + '/admin/');
  await p.getByLabel(/tienda/i).fill(T); await p.getByLabel(/correo/i).fill(EMAIL); await p.getByLabel(/contraseña/i).fill(PASS);
  await p.getByRole('button', { name: /^entrar$/i }).click();
  await p.waitForSelector('[data-testid=who]', { timeout: 15000, state: 'attached' });
  await p.waitForTimeout(800);

  const visible = async (loc) => loc.isVisible().catch(() => false);
  const openNav = async () => { if (DEVICE === 'tel') { const t = p.locator('[data-sidebar=trigger], button[aria-label*="menú" i], button[aria-label*="sidebar" i]').first(); if (await visible(t)) { await t.click().catch(() => {}); await p.waitForTimeout(250); } } };
  const closeOverlays = async () => { for (let i = 0; i < 3; i++) { if (await p.locator('[role=dialog]:visible, [role=alertdialog]:visible, [role=menu]:visible, [role=listbox]:visible').count()) { await p.keyboard.press('Escape'); await p.waitForTimeout(150); } } };

  // 1. Routes: sidebar + tabs, breadth first.
  await openNav();
  const seen = new Set(); const queue = [];
  const collect = async () => (await p.evaluate(() => [...document.querySelectorAll('a[href^="#"]')].map((a) => a.getAttribute('href')))).filter((h) => h && h.length > 1 && !h.includes('?'));
  for (const h of await collect()) if (!seen.has(h)) { seen.add(h); queue.push(h); }
  await closeOverlays();

  const go = async (h) => { LAST = 'ir a ' + h; await p.goto(B + '/admin/' + h); await p.waitForTimeout(700); };
  let done = 0;
  while (queue.length && done < MAX_ROUTES) {
    const h = queue.shift(); cur = h; done++;
    res.routes[h] = res.routes[h] || { issues: [] };
    const t0 = Date.now();
    await go(h);
    const R = res.routes[h];
    R.loadMs = Date.now() - t0;
    // Error boxes and empty-states.
    const shown = await p.evaluate(() => {
      const main = document.querySelector('main') || document.body;
      const txt = main.innerText;
      return {
        errorBox: [...main.querySelectorAll('[role=alert]')].map((e) => e.innerText.trim()).filter((t) => /error|no se pudo|falló|algo salió/i.test(t)).slice(0, 3),
        loadingStuck: /Cargando…/.test(txt),
        empty: txt.trim().length < 40,
        h1: (main.querySelector('h1') || {}).innerText || '',
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      };
    });
    if (shown.errorBox.length) note('error-en-pantalla', shown.errorBox.join(' / '));
    if (shown.empty) note('pantalla-en-blanco', 'casi sin contenido');
    if (shown.overflow > 2) note('desborde', shown.overflow + 'px');
    if (!shown.h1) note('ux-sin-titulo', 'la pantalla no tiene <h1>');
    await p.waitForTimeout(1200);
    if (await p.evaluate(() => /Cargando…/.test((document.querySelector('main') || document.body).innerText))) note('cargando-eterno', 'sigue diciendo Cargando… tras 2 s');
    // New routes from this screen (tabs, sub-pages, one detail per list).
    for (const l of await collect()) {
      const generic = l.replace(/\/[a-z]{2,6}_[A-Za-z0-9]+/g, '/:id').replace(/\/QA-[0-9-]+/g, '/:id');
      if (!seen.has(generic) && l.split('/').length <= 4) { seen.add(generic); seen.add(l); queue.push(l); }
    }

    // 2. Keyboard pass.
    // Start right before the main content, as a "skip to content" link would.
    await p.evaluate(() => { const m = document.querySelector('main'); if (m) { m.setAttribute('tabindex', '-1'); m.focus(); } });
    const kb = { stops: 0, hidden: 0, noName: [], noRing: [] };
    for (let i = 0; i < 60; i++) {
      await p.keyboard.press('Tab');
      const f = await p.evaluate(() => {
        const el = document.activeElement; if (!el || el === document.body) return null;
        const r = el.getBoundingClientRect(); const cs = getComputedStyle(el);
        const name = (el.getAttribute('aria-label') || el.innerText || el.value || el.getAttribute('title') || el.getAttribute('placeholder') || (el.labels && el.labels[0] && el.labels[0].innerText) || (el.id && document.querySelector('label[for="' + el.id + '"]') ? document.querySelector('label[for="' + el.id + '"]').innerText : '') || el.getAttribute('aria-labelledby') || '').trim();
        const ring = (cs.outlineStyle !== 'none' && parseFloat(cs.outlineWidth) > 0) || (cs.boxShadow && cs.boxShadow !== 'none');
        if (!el.closest('main')) return { out: true };
        return { tag: el.tagName, name: name.slice(0, 40), w: r.width, h: r.height, inView: r.bottom > 0 && r.top < innerHeight + 2000, ring, sidebar: !!el.closest('[data-sidebar], nav[aria-label]') };
      });
      if (!f) continue;
      if (f.out) break;
      kb.stops++;
      if (f.w < 2 || f.h < 2) kb.hidden++;
      if (!f.name && f.tag !== 'IFRAME') kb.noName.push(f.tag);
      if (!f.ring && !f.sidebar) kb.noRing.push(f.tag + ':' + f.name);
    }
    if (kb.hidden) note('teclado-foco-invisible', kb.hidden + ' paradas sin tamaño');
    if (kb.noName.length) note('teclado-sin-nombre', [...new Set(kb.noName)].join(','));
    if (kb.noRing.length) note('teclado-sin-indicador', [...new Set(kb.noRing)].slice(0, 5).join(' · '));
    R.tabStops = kb.stops;

    // 3. Every button on the screen.
    await go(h);
    const labels = await p.evaluate(() => [...document.querySelectorAll('main button, main [role=button], main [role=tab], main [role=switch], main [role=combobox]')]
      .filter((x) => x.offsetParent && !x.disabled).map((x) => (x.getAttribute('aria-label') || x.innerText || '').trim().replace(/\s+/g, ' ')).filter(Boolean));
    const uniq = [...new Set(labels)].filter((l) => !SKIP.test(l)).slice(0, 45);
    R.buttons = uniq.length;
    let dialogs = 0, invalidOk = 0;
    for (const lab of uniq) {
      if (!(p.url().endsWith(h))) await go(h);
      const btn = p.locator('main').getByRole('button', { name: lab, exact: true }).first();
      const alt = p.locator('main [role=tab], main [role=switch], main [role=combobox]').filter({ hasText: lab }).first();
      const target = (await btn.count()) ? btn : alt;
      if (!(await target.count()) || !(await visible(target))) continue;
      const destructive = DESTRUCTIVE.test(lab);
      const before = await p.evaluate(() => document.querySelector('main') ? document.querySelector('main').innerText.length : 0);
      let nativeAsked = false; const onD = () => { nativeAsked = true; }; p.once('dialog', onD);
      LAST = 'clic ' + lab;
      await target.click({ timeout: 2500 }).catch(() => note('no-clicable', lab));
      await p.waitForTimeout(450);
      p.off('dialog', onD);
      const dlg = p.locator('[role=dialog]:visible, [role=alertdialog]:visible').last();
      const hasDlg = await dlg.count();
      if (destructive && !hasDlg && !nativeAsked) {
        const after = await p.evaluate(() => document.querySelector('main') ? document.querySelector('main').innerText.length : 0);
        if (after !== before) note('ux-destructivo-sin-confirmar', lab);
      }
      if (hasDlg) {
        dialogs++;
        // Keyboard inside the dialog.
        const inside = await p.evaluate(() => { const d = [...document.querySelectorAll('[role=dialog],[role=alertdialog]')].pop(); return !!(d && d.contains(document.activeElement)); });
        if (!inside) note('dialogo-foco-fuera', lab);
        let escaped = false;
        for (let i = 0; i < 25; i++) { await p.keyboard.press('Tab'); const ok = await p.evaluate(() => { const d = [...document.querySelectorAll('[role=dialog],[role=alertdialog]')].pop(); return !d || d.contains(document.activeElement); }); if (!ok) { escaped = true; break; } }
        if (escaped) note('dialogo-foco-escapa', lab);
        if (!destructive) {
          // Empty submit.
          const submit = dlg.locator('button[type=submit], [data-slot=dialog-footer] button, .flex button').filter({ hasNotText: /^(cancelar|cerrar|volver|×)$/i }).last();
          const hasSubmit = (await submit.count()) && (await visible(submit)) && !(await submit.isDisabled().catch(() => true));
          if (hasSubmit) {
            const inputs = dlg.locator('input:not([type=hidden]):not([type=checkbox]):not([type=radio]):not([type=file]):not([type=color]), textarea');
            const n = await inputs.count();
            for (let i = 0; i < n; i++) await inputs.nth(i).fill('').catch(() => {});
            LAST = 'enviar diálogo ' + lab;
            await submit.click({ timeout: 2000 }).catch(() => {});
            await p.waitForTimeout(500);
            const stillOpen = await p.locator('[role=dialog]:visible').count();
            const feedback = await p.evaluate(() => !!document.querySelector('[aria-invalid=true], [role=alert], [data-sonner-toast][data-type=error], .text-bad, .text-destructive'));
            if (n > 0 && !stillOpen && !feedback) note('acepta-vacio', lab);
            if (n > 0 && stillOpen && !feedback) note('ux-vacio-sin-mensaje', lab);
            // Bad data.
            if (stillOpen && n > 0) {
              for (let i = 0; i < n; i++) {
                const inp = inputs.nth(i);
                const meta = await inp.evaluate((e) => ({ type: e.type, mode: e.inputMode, tag: e.tagName })).catch(() => null);
                if (!meta) continue;
                const v = meta.type === 'email' ? BAD.email : meta.type === 'number' ? BAD.number : (meta.mode === 'numeric' || meta.mode === 'decimal') ? BAD.numeric : meta.type === 'url' ? BAD.url : BAD.text;
                await inp.fill(v).catch(() => {});
              }
              await submit.click({ timeout: 2000 }).catch(() => {});
              await p.waitForTimeout(700);
              const xss = await p.evaluate(() => window.__xss === 1);
              if (xss) note('CRITICO-xss', lab);
              const open2 = await p.locator('[role=dialog]:visible').count();
              const fb2 = await p.evaluate(() => !!document.querySelector('[aria-invalid=true], [role=alert], [data-sonner-toast][data-type=error], .text-bad, .text-destructive'));
              if (!open2 && !fb2) { invalidOk++; note('acepta-datos-raros', lab); }
            }
          }
        }
        await closeOverlays();
        if (await p.locator('[role=dialog]:visible').count()) note('dialogo-no-cierra-con-esc', lab);
      }
      if (!p.url().endsWith(h)) await go(h);
    }
    R.dialogs = dialogs;

    // 4. Forms on the page itself (editors, settings): empty, then bad data.
    await go(h);
    const save = p.locator('main').getByRole('button', { name: /^(guardar|guardar cambios|crear|crear producto|guardar y publicar)$/i }).first();
    if (await save.count() && await visible(save)) {
      const inputs = p.locator('main input:not([type=hidden]):not([type=checkbox]):not([type=radio]):not([type=file]):not([type=color]):not([type=search]):not([readonly]):visible, main textarea:visible');
      const n = Math.min(await inputs.count(), 25);
      if (n) {
        R.form = n;
        const orig = [];
        for (let i = 0; i < n; i++) orig.push(await inputs.nth(i).inputValue().catch(() => ''));
        for (let i = 0; i < n; i++) await inputs.nth(i).fill('').catch(() => {});
        if (!(await save.isDisabled().catch(() => true))) {
          LAST = 'guardar formulario';
          await save.click({ timeout: 2000 }).catch(() => {});
          await p.waitForTimeout(700);
          const fb = await p.evaluate(() => ({ err: !!document.querySelector('[aria-invalid=true], [role=alert], [data-sonner-toast][data-type=error], .text-bad, .text-destructive'), ok: !!document.querySelector('[data-sonner-toast][data-type=success]') }));
          if (fb.ok && !fb.err) note('form-acepta-vacio', h);
        }
        await go(h);
        for (let i = 0; i < n; i++) {
          const inp = inputs.nth(i);
          const meta = await inp.evaluate((e) => ({ type: e.type, mode: e.inputMode })).catch(() => null);
          if (!meta) continue;
          const v = meta.type === 'email' ? BAD.email : meta.type === 'number' ? BAD.number : (meta.mode === 'numeric' || meta.mode === 'decimal') ? BAD.numeric : meta.type === 'url' ? BAD.url : BAD.text;
          await inp.fill(v).catch(() => {});
        }
        if (!(await save.isDisabled().catch(() => true))) {
          LAST = 'guardar formulario';
          await save.click({ timeout: 2000 }).catch(() => {});
          await p.waitForTimeout(900);
          if (await p.evaluate(() => window.__xss === 1)) note('CRITICO-xss', 'formulario de ' + h);
          const fb = await p.evaluate(() => ({ err: !!document.querySelector('[aria-invalid=true], [role=alert], [data-sonner-toast][data-type=error], .text-bad, .text-destructive'), ok: !!document.querySelector('[data-sonner-toast][data-type=success]') }));
          if (fb.ok && !fb.err) note('form-acepta-datos-raros', h);
          // Reload: data saved must show escaped, never run.
          await go(h);
          if (await p.evaluate(() => window.__xss === 1)) note('CRITICO-xss', 'al recargar ' + h);
        }
      }
    }
    const xssPage = await p.evaluate(() => window.__xss === 1).catch(() => false);
    if (xssPage) note('CRITICO-xss', 'al mostrar datos en ' + h);
    process.stdout.write('.');
  }
  const all = Object.values(res.routes).flatMap((r) => r.issues);
  res.summary = { routes: Object.keys(res.routes).length, buttons: Object.values(res.routes).reduce((a, r) => a + (r.buttons || 0), 0), dialogs: Object.values(res.routes).reduce((a, r) => a + (r.dialogs || 0), 0), byKind: all.reduce((m, i) => ((m[i.kind] = (m[i.kind] || 0) + 1), m), {}) };
  fs.writeFileSync(OUT, JSON.stringify(res, null, 1));
  console.log('\n' + LABEL, DEVICE, JSON.stringify(res.summary));
  await b.close();
})().catch((e) => { console.error('FALLO', e.message); process.exit(1); });
