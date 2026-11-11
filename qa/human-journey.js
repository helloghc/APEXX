const { chromium, devices } = require('playwright');
const [B, T, OUT] = process.argv.slice(2); const SB = B + '/s/' + T + '/main';
(async () => {
  const b = await chromium.launch(); const errors = []; const notes = [];
  const admin = await (await b.newContext({ viewport: { width: 1366, height: 768 } })).newPage();
  const ph = await (await b.newContext({ ...devices['Pixel 5'] })).newPage();
  for (const p of [admin, ph]) { p.on('pageerror', e => errors.push('error de página: ' + e.message)); p.on('dialog', d => d.accept());
    p.on('response', r => { if (r.status() >= 500) errors.push(r.status() + ' ' + r.url().replace(B, '')); }); }
  const shot = async (p, n, full) => { await p.waitForTimeout(600); await p.screenshot({ path: `${OUT}/${n}.jpg`, type: 'jpeg', quality: 72, fullPage: !!full }); };
  const over = async (p, where) => { const o = await p.evaluate(() => document.documentElement.scrollWidth - window.innerWidth); if (o > 2) notes.push(`desborde de ${o}px en ${where}`); };
  const step = async (name, fn) => { const t = Date.now(); try { await fn(); console.log('✔', name, `(${Date.now() - t} ms)`); } catch (e) { console.log('✘', name, '-', String(e.message).split('\n')[0]); await ph.screenshot({ path: `${OUT}/FALLA-${name.replace(/\W+/g, '_')}-cel.jpg`, type: 'jpeg', quality: 60 }).catch(() => {}); await admin.screenshot({ path: `${OUT}/FALLA-${name.replace(/\W+/g, '_')}-panel.jpg`, type: 'jpeg', quality: 60 }).catch(() => {}); } };
  const email = 'cliente' + Date.now() % 100000 + '@example.com';
  console.log('— Cliente en celular —');
  await step('Abre la tienda', async () => { await ph.goto(SB + '/index'); await ph.waitForSelector('main'); await over(ph, 'inicio'); await shot(ph, 'q01-inicio-cel'); });
  await step('Busca con error de dedo "plaiera"', async () => { await ph.goto(SB + '/c?q=plaiera'); await ph.waitForSelector('.pcard'); const n = await ph.locator('.pcard').count(); if (!n) throw new Error('sin resultados'); await over(ph, 'búsqueda'); });
  await step('Abre un producto y lo guarda en favoritos', async () => { await ph.locator('.pcard a').first().click(); await ph.waitForSelector('h1'); await ph.locator('button.wish').first().click(); await over(ph, 'ficha'); await shot(ph, 'q02-ficha-cel'); });
  await step('Agrega 2 al carrito', async () => { await ph.fill('#qty', '2'); await ph.click('button[type=submit]:has-text("Agregar")'); await ph.waitForSelector('[data-buy-status] a'); });
  await step('Carrito: cupón inválido y luego envío', async () => { await ph.goto(SB + '/cart'); await ph.waitForSelector('table'); await ph.fill('#coupon', 'NOEXISTE'); await ph.click('button:has-text("Aplicar")'); await ph.waitForTimeout(900); const msg = await ph.locator('[data-coupon-status], #coupon-status, [role=status]').allInnerTexts(); notes.push('cupón inválido → "' + msg.join(' ').trim().slice(0, 80) + '"'); await ph.fill('#zip', '06600'); await ph.click('button:has-text("Calcular")'); await ph.waitForSelector('.shipopts'); await over(ph, 'carrito'); await shot(ph, 'q03-carrito-cel', true); });
  await step('Paga: primero olvida datos, luego tarjeta rechazada, luego aprobada', async () => {
    await ph.goto(SB + '/checkout'); await ph.waitForSelector('[data-pay]'); await ph.click('[data-pay]'); await ph.waitForTimeout(500); notes.push('sin datos → "' + (await ph.locator('[data-error]').innerText()) + '"');
    await ph.fill('#email', email); await ph.fill('#name', 'Mariana Móvil'); await ph.fill('#phone', '5512345678'); await ph.fill('#address', 'Av. Juárez 100, Centro'); await ph.fill('#postal', '06600'); await ph.locator('#postal').blur(); await ph.waitForSelector('[data-ship-options] .shipopt'); if (await ph.locator('#terms').count()) await ph.check('#terms');
    await ph.check('input[value=pm_card_chargeDeclined]'); await ph.click('[data-pay]'); await ph.waitForFunction(() => /rechazad/i.test(document.querySelector('[data-error]').textContent), null, { timeout: 15000 }); notes.push('tarjeta rechazada → "' + (await ph.locator('[data-error]').innerText()) + '"');
    await ph.check('input[value=pm_card_visa]'); await over(ph, 'checkout'); await shot(ph, 'q04-checkout-cel', true); await ph.click('[data-pay]'); await ph.waitForURL(/gracias|thanks/i, { timeout: 15000 }); await shot(ph, 'q05-gracias-cel', true);
  });
  await step('Entra a su cuenta con código y ve su pedido', async () => {
    await ph.goto(SB + '/cuenta'); await ph.waitForSelector('input[type=email]'); await ph.fill('main input[type=email]', email); await ph.getByRole('button', { name: /Enviarme un código/i }).click(); await ph.waitForSelector('#codigo'); const hw = await ph.evaluate(() => { const r = document.querySelector('.hdr-wish').getBoundingClientRect(); return r.right <= window.innerWidth; }); if (!hw) notes.push('el corazón del encabezado se sale de la pantalla');
    await admin.goto(B + '/admin/'); await admin.getByLabel(/tienda/i).fill(T); await admin.getByLabel(/correo/i).fill('dueno@qa.apexx.test'); await admin.getByLabel(/contraseña/i).fill('QA-prueba-2026!'); await admin.getByRole('button', { name: /entrar/i }).click(); await admin.waitForSelector('[data-testid=who]'); await admin.waitForTimeout(2500);
    const list = await (await admin.request.get(B + '/v1/notifications/messages?limit=8')).json(); const msg = list.items.find(m => m.template === 'access_code' && m.to === email); const full = await (await admin.request.get(B + '/v1/notifications/messages/' + msg.id)).json(); const code = (full.html.replace(/<[^>]+>/g, ' ').match(/\b\d{6}\b/) || [])[0];
    await ph.fill('#codigo', code); await ph.locator('#codigo').press('Enter'); await ph.waitForLoadState('networkidle'); const txt = await ph.locator('main').innerText(); if (!/pedido/i.test(txt)) throw new Error('no ve sus pedidos'); await over(ph, 'cuenta'); await shot(ph, 'q06-cuenta-cel', true);
    notes.push('correos que recibió: ' + list.items.filter(m => m.to === email).map(m => m.template).join(', '));
  });
  console.log('— Dueño en el panel: atiende el pedido —');
  if (!(await admin.locator('[data-testid=who]').count())) { await admin.goto(B + '/admin/'); await admin.getByLabel(/tienda/i).fill(T); await admin.getByLabel(/correo/i).fill('dueno@qa.apexx.test'); await admin.getByLabel(/contraseña/i).fill('QA-prueba-2026!'); await admin.getByRole('button', { name: /entrar/i }).click(); await admin.waitForSelector('[data-testid=who]'); }
  let orderUrl = '';
  await step('Encuentra el pedido, lo envía con guía y reembolsa una pieza', async () => {
    await admin.goto(B + '/admin/#pedidos'); await admin.waitForLoadState('networkidle'); await admin.locator('tbody tr').first().click(); await admin.waitForLoadState('networkidle'); orderUrl = admin.url();
    await admin.getByRole('button', { name: /Enviar productos/ }).click(); await admin.getByLabel(/guía/i).fill('EST99887766'); await admin.getByRole('dialog').getByRole('button', { name: /Crear envío|Enviar/ }).last().click(); await admin.waitForTimeout(1200);
    await admin.getByRole('button', { name: 'Reembolsar' }).click(); await admin.getByRole('dialog').locator('input').first().fill('1'); await admin.waitForTimeout(700); await shot(admin, 'q07-reembolso'); await admin.getByRole('dialog').getByRole('button', { name: /Reembolsar/ }).last().click(); await admin.waitForTimeout(1500); await shot(admin, 'q08-pedido-atendido', true);
  });
  console.log('— Miembro con permisos limitados —');
  await step('Editor de catálogo: ve catálogo, no ve pedidos ni equipo', async () => {
    const mail = 'editor' + Date.now() % 100000 + '@example.com'; const r = await admin.request.post(B + '/v1/team/members', { data: { email: mail, name: 'Elena Editora', password: 'Clave-larga-de-prueba-1', permissions: { catalog: 'edit' } } }); if (r.status() !== 201) throw new Error('crear miembro ' + r.status());
    const ctx = await b.newContext({ viewport: { width: 1366, height: 768 } }); const m = await ctx.newPage(); await m.goto(B + '/admin/'); await m.getByLabel(/tienda/i).fill(T); await m.getByLabel(/correo/i).fill(mail); await m.getByLabel(/contraseña/i).fill('Clave-larga-de-prueba-1'); await m.getByRole('button', { name: /entrar/i }).click(); await m.waitForSelector('[data-testid=who]');
    notes.push('menú del editor: ' + (await m.locator('nav a').allInnerTexts()).join(' | '));
    for (const r of ['pedidos', 'equipo', 'tarjetas', 'empresas', 'configuracion/tienda']) { await m.goto(B + '/admin/#' + r); await m.waitForTimeout(500); if (!/Sin acceso/.test(await m.locator('main').innerText())) throw new Error('el editor abrió #' + r); }
    for (const path of ['/v1/orders', '/v1/customers', '/v1/gift-cards', '/v1/companies', '/v1/team/members', '/v1/sellers']) { const x = await m.request.get(B + path); if (x.status() !== 403) throw new Error(path + ' respondió ' + x.status() + ' al editor'); }
    await m.goto(B + '/admin/#catalogo'); await m.waitForLoadState('networkidle'); await shot(m, 'q09-editor-catalogo');
  });
  console.log('\nObservaciones:\n - ' + notes.join('\n - ')); console.log('Errores del servidor o de página:', errors.length ? [...new Set(errors)] : 'ninguno'); await b.close();
})();
