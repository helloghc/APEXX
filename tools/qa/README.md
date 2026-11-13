# Herramientas de QA

Scripts con los que se hicieron las auditorías de `docs/audit/auditoria-exhaustiva-panel.md` y `docs/audit/escala-50k.md`, para repetirlas. Usan Node 22 y Playwright con Chromium.

Ninguno guarda contraseñas ni direcciones de base de datos: todo llega por argumentos o variables de entorno.

## Recorrido exhaustivo del panel

```
node tools/qa/exhaustivo.js BASE TIENDA CORREO CONTRASEÑA ETIQUETA pc|tel SALIDA.json [máx. rutas]
```

Recorre cada ruta del menú y sus pestañas. En cada pantalla:
- revisa errores, pantallas vacías y desborde;
- hace un pase de teclado;
- pulsa cada botón;
- abre cada diálogo y lo envía vacío y con datos erróneos o maliciosos;
- guarda cada formulario y recarga para confirmar que nada se ejecuta.

El resultado queda en `SALIDA.json`, por ruta.

Para cubrir todos los roles, corre `qa-setup` y luego una vez por cuenta: dueno, lector, catalogo, pedidos, disenador, publicador, representante y vendedor.

## Simulación de escala

```
export APEXX_QA_PASSWORD=...        # la de qa-setup
export APEXX_QA_DB_URL=postgres://… # rol que lea todas las tablas de la tienda (para la foto)
apexx qa-setup -tenant escala -n 0 -password "$APEXX_QA_PASSWORD"
APEXX_TRUST_PROXY=true apexx serve  # como detrás de Caddy: cada petición del arnés trae su propia IP
node tools/qa/escala/run.js 50000   # 1000, 10000, 50000…
```

`run.js` hace estos pasos:
1. Toma una foto de las 144 tablas de la tienda.
2. Carga N productos por CSV y mide cuándo los ve la tienda.
3. Mide latencias, con un cliente y con 16 a la vez, y cuenta como error toda respuesta que no sea 2xx.
4. Mide la propagación de un cambio de precio.
5. Revierte precios de forma exacta.
6. Borra todo y compara cada tabla con la foto.

`measure.js N` repite solo las latencias sobre una tienda ya cargada. Los resultados quedan en `$APEXX_QA_OUT` (por omisión `./out`).

Con `APEXX_QA_WITH_LOAD="<comando>"`, `run.js` corre ese comando con el catálogo completo cargado, después de los vectores, y guarda su salida en el resultado. Por ejemplo, la medición del panel de la sección siguiente.

## Carga de cada vista del panel

```
node tools/qa/carga/medir.js http://127.0.0.1:8097 audit dueno@qa.apexx.test "$APEXX_QA_PASSWORD" out/carga.json
RTT=80 MBPS=20 node tools/qa/carga/medir.js https://host-con-http2 audit dueno@qa.apexx.test "$APEXX_QA_PASSWORD" out/carga-red.json
```

Recorre en un navegador real cada vista que se alcanza desde el menú: pestañas, subpáginas y una ficha por lista.

Por cada vista mide, desde el clic hasta que queda completa (sin peticiones `/v1` en curso, sin esqueletos de carga, sin «Cargando…»):
- su tiempo total;
- las llamadas a la API y la más lenta;
- el JavaScript que descargó.

También mide la primera carga en frío.

Con `RTT` (ms de ida y vuelta) y `MBPS` simula una red real, donde cada petición que espera a otra cuesta un viaje. Para medir como en producción, apúntalo a un servidor con HTTPS y HTTP/2, como Caddy. Por HTTP/1.1 el navegador abre solo 6 conexiones y las vistas con muchas llamadas hacen cola. Resultados en `docs/audit/carga-paneles.md`.

## Prueba de relevo (despliegue sin corte)

```
# con el entorno del servidor ya exportado (DATABASE_URL, …) y las variables de arriba
APEXX_BIN=./apexx CADDY_BIN=/ruta/a/caddy tools/qa/relevo/run.sh
```

Levanta dos nodos y Caddy con las mismas reglas de `deploy/caddy/Caddyfile` y manda tráfico constante con `relevo/load.js` (16 clientes: tienda, API, panel y un POST). Mientras tanto reemplaza cada nodo dos veces. Pasa si ninguna petición falla.

Para el control, repítelo con `APEXX_DRAIN=0s`: deben aparecer fallos o esperas largas, lo que prueba que el drenado es lo que los evita.

**Con sandboxes** (`APEXX_QA_SANDBOX=1`), además:
1. Crea un sandbox con todo el catálogo y le manda tráfico durante el relevo: su tienda con un enlace de vista previa, su búsqueda, su panel desde una sesión dentro y una visita sin acceso, que debe dar 403 siempre.
2. Cambia un producto en el sandbox y programa su publicación para la mitad del relevo.
3. Justo antes de reemplazar el primer nodo, le pide ahí la copia de un segundo sandbox: el nodo debe esperar a que termine antes de apagarse.

Al final `relevo/sandbox.js check` exige:
- la segunda copia lista y completa;
- la publicación programada hecha una sola vez y visible en producción;
- la reversión exacta;
- los dos sandboxes borrados sin dejar tiendas.

Para medirlo con 50 000 productos, córrelo dentro de la prueba de escala:

```
APEXX_QA_SANDBOX=1 APEXX_QA_WITH_LOAD="SECS=100 APEXX_BIN=./apexx CADDY_BIN=/ruta/a/caddy bash ../relevo/run.sh" node run.js 50000
```
