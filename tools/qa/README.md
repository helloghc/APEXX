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

## Prueba de relevo (despliegue sin corte)

```
# con el entorno del servidor ya exportado (DATABASE_URL, …) y las variables de arriba
APEXX_BIN=./apexx CADDY_BIN=/ruta/a/caddy tools/qa/relevo/run.sh
```

Levanta dos nodos y Caddy con las mismas reglas de `deploy/caddy/Caddyfile` y manda tráfico constante con `relevo/load.js` (16 clientes: tienda, API, panel y un POST). Mientras tanto reemplaza cada nodo dos veces. Pasa si ninguna petición falla.

Para el control, repítelo con `APEXX_DRAIN=0s`: deben aparecer fallos o esperas largas, lo que prueba que el drenado es lo que los evita.
