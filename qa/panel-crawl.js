const { chromium } = require('playwright');
const [B, T, OUT] = process.argv.slice(2);
const ROUTES = ['inicio', 'builder', 'catalogo', 'catalogo/categorias', 'catalogo/colecciones', 'catalogo/marcas', 'catalogo/atributos', 'catalogo/medios', 'catalogo/importar', 'promociones', 'promociones/nueva', 'promociones/campanas', 'promociones/cupones', 'promociones/listas', 'promociones/masivo', 'promociones/historial', 'promociones/simulador',
  'busqueda', 'busqueda/terminos', 'busqueda/opiniones', 'busqueda/favoritos', 'pedidos', 'pedidos/devoluciones', 'pedidos/borradores', 'suscripciones', 'suscripciones/ajustes', 'clientes', 'clientes/segmentos', 'clientes/carritos', 'empresas', 'marketplace', 'correos', 'correos/enviados', 'correos/ajustes',
  'reportes', 'reportes/ventas', 'reportes/productos', 'reportes/clientes', 'reportes/embudo', 'reportes/promociones', 'reportes/inventario', 'equipo', 'pagos', 'tarjetas', 'tarjetas/meses', 'tarjetas/antifraude', 'datos',
  'configuracion/tienda', 'configuracion/checkout', 'configuracion/canales', 'configuracion/ubicaciones', 'configuracion/envios', 'configuracion/impuestos', 'configuracion/politicas', 'configuracion/navegacion', 'configuracion/redirecciones', 'configuracion/dominios', 'configuracion/anuncios', 'conectores', 'operacion', 'operacion/estado', 'cuenta'];
(async () => {
  const b = await chromium.launch(); const report = [];
  for (const [label, vp] of [['escritorio', { width: 1366, height: 768 }], ['celular', { width: 390, height: 844 }]]) {
    const p = await (await b.newContext({ viewport: vp })).newPage(); let cur = ''; const errs = {};
    const add = m => { (errs[cur] = errs[cur] || new Set()).add(m); };
    p.on('pageerror', e => add('error de página: ' + e.message.slice(0, 120))); p.on('console', m => { if (m.type() === 'error' && !/401|Failed to load resource/.test(m.text())) add('consola: ' + m.text().slice(0, 120)); });
    p.on('response', r => { if (r.status() >= 400 && r.url().includes('/v1/') && !r.url().endsWith('/v1/me')) add(r.status() + ' ' + r.url().replace(B, '').slice(0, 80)); });
    await p.goto(B + '/admin/'); await p.getByLabel(/tienda/i).fill(T); await p.getByLabel(/correo/i).fill('dueno@qa.apexx.test'); await p.getByLabel(/contraseña/i).fill('QA-prueba-2026!'); await p.getByRole('button', { name: /entrar/i }).click(); await p.waitForLoadState('networkidle');
    let bad = 0;
    for (const r of ROUTES) {
      cur = r; await p.goto(B + '/admin/#' + r); await p.waitForLoadState('networkidle').catch(() => {}); await p.waitForTimeout(350);
      const info = await p.evaluate(() => ({ h1: (document.querySelector('main h1, h1') || {}).textContent || '', over: document.documentElement.scrollWidth - window.innerWidth, blank: (document.querySelector('main') || document.body).innerText.trim().length < 20, crash: /Algo salió mal|Something went wrong|Sin acceso/.test(document.body.innerText) }));
      const problems = [...(errs[r] || [])]; if (!info.h1 && r !== 'builder') problems.push('sin título'); if (info.over > 2) problems.push('desborde horizontal de ' + info.over + ' px'); if (info.blank) problems.push('pantalla vacía'); if (info.crash) problems.push('pantalla de error o sin acceso');
      if (problems.length) { bad++; report.push(`[${label}] #${r}: ${problems.join(' | ')}`); await p.screenshot({ path: `${OUT}/problema-${label}-${r.replace(/\//g, '_')}.jpg`, type: 'jpeg', quality: 60 }); }
    }
    if (label === 'escritorio') { await p.goto(B + '/admin/#inicio'); await p.waitForTimeout(400); const nav = await p.evaluate(() => { const links = [...document.querySelectorAll('nav a, aside a')]; const last = links.filter(a => /Operación/.test(a.textContent))[0]; if (!last) return 'sin enlace Operación'; last.scrollIntoView({ block: 'nearest' }); const r = last.getBoundingClientRect(); return r.bottom <= window.innerHeight && r.top >= 0 ? 'alcanzable' : 'fuera de pantalla'; }); console.log('último enlace del menú en 768 px de alto:', nav); }
    console.log(`${label}: ${ROUTES.length} pantallas, ${bad} con problemas`);
  }
  console.log(report.join('\n') || 'sin problemas'); await b.close();
})();
