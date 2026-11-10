// Recorrido E2E del comprador (móvil): búsqueda con error, ficha, carrito, código postal,
// pago aprobado con doble clic, pago rechazado, reintento y validación.
// Uso: APEXX_SHOP_URL=https://tu-dominio/s/<tienda>/<sitio> node qa/shopper-journey.js  (requiere playwright)
const { chromium, devices } = require('playwright');
const B = process.env.APEXX_SHOP_URL || 'http://127.0.0.1:8080/s/demo/main';
(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ...devices['Pixel 5'] });
  const page = await ctx.newPage();
  const errors = []; page.on('pageerror', e => errors.push(e.message)); page.on('console', m => m.type() === 'error' && errors.push(m.text()));
  const t = async (label, fn) => { const s = Date.now(); await fn(); console.log(label.padEnd(48), (Date.now() - s) + ' ms'); };
  await t('1. Home', () => page.goto(B + '/index'));
  await t('2. Ir a Productos', async () => { await page.click('nav >> text=Productos'); await page.waitForSelector('.grid'); });
  const handles = await page.$$eval('.card a', as => as.map(a => a.getAttribute('href')));
  const words = (await page.locator('.card h2').first().textContent()).trim().split(/\s+/);
  const typo = words[0].slice(0, 2) + words[0].slice(3); // drop one letter
  await t('3. Buscar con error "' + typo + '"', async () => { await page.fill('#q', typo); await page.press('#q', 'Enter'); await page.waitForSelector('.card'); });
  console.log('   resultados:', await page.locator('.card').count());
  await t('4. Abrir ficha', async () => { await page.locator('.card a').first().click(); await page.waitForSelector('h1'); });
  console.log('   producto:', await page.textContent('h1'), '| precio:', (await page.textContent('[data-price-out]')).trim());
  await t('5. Agregar 2 al carrito', async () => { await page.fill('#qty', '2'); await page.click('button[type=submit]:has-text("Agregar")'); await page.waitForSelector('[data-buy-status] a'); });
  console.log('   contador del carrito:', await page.textContent('[data-cart-count]'));
  await page.goto(new URL(handles[1], page.url()).href); await page.fill('#qty', '3'); await page.click('button:has-text("Agregar")'); await page.waitForSelector('[data-buy-status] a');
  await t('6. Ver carrito', async () => { await page.click('.cartlink'); await page.waitForSelector('table'); });
  await t('7. Código postal 53129', async () => { await page.fill('#zip', '53129'); await page.click('button:has-text("Calcular")'); await page.waitForFunction(() => document.querySelector('.totals') && /días/.test(document.querySelector('.totals').textContent)); });
  console.log('   promociones:', (await page.locator('.promo').allTextContents()).join(' | '));
  console.log('   total:', (await page.textContent('.grand')).replace(/\s+/g, ' ').trim());
  await t('8. Quitar 1 unidad', async () => { await page.locator('[data-dec]').first().click(); await page.waitForTimeout(150); });
  await t('9. Ir a pagar', async () => { await page.click('[data-to-checkout]'); await page.waitForSelector('input[name=pm]'); });
  await page.fill('#email', 'qa.comprador@example.com'); await page.fill('#name', 'QA Comprador'); await page.fill('#address', 'Av. Siempre Viva 742');
  await t('10. Pagar (tarjeta aprobada, doble clic)', async () => { await Promise.all([page.waitForURL(/gracias/), page.dblclick('[data-pay]')]); await page.waitForSelector('text=está confirmado'); });
  console.log('   confirmación:', (await page.textContent('[data-thanks] p')).trim());
  console.log('   carrito tras la compra:', await page.textContent('[data-cart-count]'));
  // Declined card
  await page.goto(new URL(handles[2], page.url()).href); await page.click('button:has-text("Agregar")'); await page.waitForSelector('[data-buy-status] a');
  await page.goto(B + '/checkout'); await page.waitForSelector('input[name=pm]');
  await page.fill('#email', 'qa.comprador@example.com'); await page.fill('#name', 'QA Comprador'); await page.fill('#address', 'Calle 1');
  await page.check('input[value=pm_card_chargeDeclined]');
  await t('11. Pagar con tarjeta rechazada', async () => { await page.click('[data-pay]'); await page.waitForFunction(() => document.querySelector('[data-error]').textContent.length > 0); });
  console.log('   mensaje:', await page.textContent('[data-error]'));
  await page.check('input[value=pm_card_visa]');
  await t('12. Reintentar con tarjeta aprobada', async () => { await Promise.all([page.waitForURL(/gracias/), page.click('[data-pay]')]); });
  await t('13. Validación: formulario vacío', async () => { await page.goto(new URL(handles[3], page.url()).href); await page.click('button:has-text("Agregar")'); await page.goto(B + '/checkout'); await page.waitForSelector('input[name=pm]'); await page.click('[data-pay]'); });
  console.log('   mensaje:', await page.textContent('[data-error]'));
  console.log('errores de JS/consola:', errors.length ? errors : 'ninguno');
  await browser.close();
})();
