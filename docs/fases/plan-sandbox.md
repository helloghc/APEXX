# Plan: sandbox por tienda

Fecha: 13 de noviembre de 2026. Estado: **construido y validado** (SBX-0 a SBX-3 completos; SBX-4 y SBX-5 sin cupos por plan, métricas por sandbox ni prueba de relevo de nodos). Referencia técnica y mediciones: [`docs/sandbox.md`](../sandbox.md).

## Qué es

Cada cliente de Apexx recibe **dos tiendas completas: producción y su sandbox.** El sandbox no es una vista previa ni una copia de los píxeles: es el ambiente entero, idéntico a producción, con todo lo que Apexx hace.
- catálogo, variantes, precios, existencias e importador;
- promociones y cupones;
- diseño con el builder, temas y contenido;
- checkout, envíos, impuestos, B2B, marketplace y suscripciones;
- búsqueda con IA, etiquetas y píxeles;
- apps, automatizaciones y reportes.

En el sandbox el cliente prueba todo sin miedo y lo ve en una dirección que solo él controla. Cuando está seguro, publica a producción lo que quiera, ya sean productos, catálogos, precios, diseño o configuración, viendo antes qué cambia y con reversión en un clic.

Es un servicio que se vende con el plan.

### Por qué es distinto

- **Competencia:** en las plataformas grandes, el «staging» suele ser una tienda de desarrollo vacía, otra suscripción, o una copia que se desactualiza y no se puede regresar a producción sin rehacer el trabajo a mano.
- **En Apexx:**
  - el sandbox nace como copia idéntica;
  - responde a la misma velocidad que producción, porque usa el mismo motor;
  - se refresca cuando se quiera;
  - publica a producción por partes, con comparador, conflictos detectados y reversión exacta.

## Principios

1. **Mismo código, mismo motor.** El sandbox es una tienda más dentro de Apexx, con su propio identificador y el mismo aislamiento por fila (RLS). No hay una versión aparte del sistema: todo lo que funciona en producción funciona igual en el sandbox, en milisegundos.
2. **Nada sale del sandbox sin querer:**
   - no cobra con tarjetas reales;
   - no manda correos a clientes;
   - no avisa a conectores de producción;
   - no publica en Mercado Libre ni Amazon;
   - no se indexa en buscadores.
3. **Credenciales propias.** El sandbox nunca usa las llaves de producción (Stripe, paqueterías, ERP, webhooks, canales de venta). Para analítica y píxeles el cliente elige entre poner IDs de prueba o conservar los de producción con todo el tráfico marcado como sandbox para filtrarlo.
4. **Solo el cliente lo ve y lo administra.** Esto vale aunque use su propio subdominio.
5. **Reversible y auditado.** Crear, refrescar, publicar a producción y borrar quedan en la auditoría. Publicar a producción guarda una foto antes y se revierte con un clic. Borrar el sandbox deja la base exactamente como estaba, verificado tabla por tabla como en la prueba de escala.

## Arquitectura

### El sandbox es otra tienda, ligada a la de producción

- `tenants` gana dos columnas: `kind` (`production` o `sandbox`) y `parent_id`. Por ejemplo, `acme` y su sandbox `acme~sbx`.
- Todas las tablas ya usan `tenant_id` con RLS forzado. La copia hereda el aislamiento sin cambiar ninguna consulta.
- La proyección en memoria lo carga como cualquier tienda. Con 50 000 productos ya medimos páginas en 1–4 ms y vectores listos en 1,3 s.
- **En el panel**, un selector «Producción / Sandbox» arriba, con una franja de color siempre visible cuando se está en el sandbox. Los permisos del equipo son los mismos, más un permiso nuevo, «Publicar a producción».

### Motor de copia

1. Una transacción en `REPEATABLE READ` toma una foto consistente de producción.
2. Por cada tabla de la tienda (hoy son 144), `INSERT … SELECT` reescribe `tenant_id`. Las llaves ya llevan `tenant_id`, así que no hay que renumerar.
3. La lista de tablas sale del catálogo de Postgres, como en la foto de la prueba de escala. Una tabla nueva entra sola o rompe una prueba, nunca se olvida en silencio.

| Dato | En el sandbox |
|---|---|
| Catálogo, precios, existencias, promociones, diseño, sitios, contenido, blog, políticas, configuración, etiquetas, campos propios, reglas, apps, automatizaciones | Se copia igual |
| Imágenes y archivos | Se referencian, no se duplican (son de solo lectura y por contenido); una imagen nueva del sandbox es solo suya |
| Vectores y grafo de IA | Se copian; se recalculan solos si algo cambia |
| Clientes y pedidos | Recomendado: anonimizados (nombre, correo, teléfono y dirección falsos pero consistentes) o una muestra. Opción del cliente: sin clientes ni pedidos |
| Pagos, facturas, sesiones, llaves de API, cuentas de pago, credenciales cifradas | **No se copian:** el sandbox empieza sin ellas |
| Auditoría, outbox, historiales | No se copian: el sandbox empieza su propia historia |
| Webhooks y suscripciones de conectores | Se copian **apagados** y marcados «de producción»; se encienden con URLs de prueba |
| Dominios de producción | No se copian; el sandbox tiene los suyos (ver abajo) |

**Meta de velocidad:** crear el sandbox de una tienda con 50 000 productos en menos de 60 s, con avance visible. Las tiendas chicas, en segundos.

### Dirección del sandbox: temporal o con su propio subdominio

El cliente elige, y puede cambiarlo cuando quiera:

1. **URL temporal de Apexx**, por omisión, sin configurar nada: `https://{token}.sandbox.apexx.app`.
   - El token es aleatorio y se puede rotar.
   - Sirve para empezar a probar en segundos.

2. **Su propio subdominio**, por ejemplo `pruebas.mitienda.com`. Usa el mismo mecanismo que hoy tienen los dominios de producción (*Configuración › Dominios*):
   1. El cliente escribe el subdominio en el sandbox.
   2. Apexx le muestra dos registros DNS: un **TXT** que prueba que el dominio es suyo y un **CNAME** que apunta al sandbox.
   3. Al verificarse el TXT, Caddy emite el certificado HTTPS automáticamente.
   4. El subdominio queda ligado **solo** al sandbox: no puede apuntar a producción por error, ni la tienda de producción puede tomarlo.

   Esto también resuelve el caso «staging» de los clientes que ya trabajan con ambientes: su equipo y sus agencias prueban en `staging.mitienda.com`, como están acostumbrados.

**En las dos opciones el sandbox sigue siendo privado:**
- Para verlo hace falta una sesión del equipo de la tienda con el permiso «Sandbox», o un enlace de vista previa firmado y con vencimiento para alguien de fuera (agencia, socio, cliente B2B que prueba un catálogo).
- Opcional: una contraseña de acceso para testers sin cuenta.
- Siempre `X-Robots-Tag: noindex`, `robots.txt` cerrado, sin sitemap y sin feeds de Google Shopping ni Meta.
- Una franja «Sandbox — no es tu tienda real» en la tienda, para que nadie la confunda.

### Integraciones aisladas

| Integración | En el sandbox |
|---|---|
| Pagos | Solo modo de prueba de Stripe, con sus tarjetas de prueba. El código impide cobrar en vivo desde una tienda `sandbox` |
| Correos | Se capturan en una bandeja del sandbox (se leen en el panel). Opción: enviarlos solo a correos del equipo. Nunca a clientes |
| Webhooks y conectores | Apagados hasta configurar URLs de prueba |
| Mercado Libre, Amazon, ERP, paqueterías | Desconectados; se conectan con credenciales de prueba si el proveedor las tiene |
| Conversiones desde el servidor | Nunca se envían desde el sandbox |
| IA con Claude | La misma, con su presupuesto; el uso se reporta aparte |

### Etiquetas y píxeles en el sandbox

Al crear el sandbox se copian las etiquetas, y el cliente elige un modo por cada una.

1. **IDs de prueba.** El cliente pone otra propiedad de Analytics, un píxel de prueba, etcétera. Como el sandbox es otra tienda, su configuración de etiquetas es independiente: publicar en el sandbox nunca toca producción.

2. **Mismos IDs, tráfico marcado.** Las etiquetas siguen con los IDs de producción y todo lo que sale del sandbox lleva su marca:
   - Toda URL del sandbox lleva `?apexx_env=sandbox`, así las páginas vistas se excluyen por URL en cualquier herramienta.
   - Cada evento del `dataLayer` lleva `apexx_env: "sandbox"`.
   - GA4 recibe `traffic_type: "internal"`; su filtro de tráfico interno lo excluye.
   - Meta recibe el `test_event_code` del Administrador de eventos: los eventos aparecen en «Probar eventos» y no cuentan como conversiones.
   - Adobe, Hotjar, Clarity y los demás reciben `apexx_env`, para usarlo como filtro o segmento.

   El panel explica, para cada proveedor, cómo crear el filtro de su lado.

### Publicar a producción

- **Comparador por área:** productos, catálogos y categorías, precios, existencias, promociones, diseño, contenido, configuración y etiquetas. Muestra qué se agrega, qué cambia y qué se quita, en palabras, como el historial de etiquetas.
- **Selección:** se elige qué publicar: un área completa, algunos productos, una categoría, una promoción, el diseño.
- **Conflictos:** si producción cambió algo desde la copia, se marca y se decide caso por caso. Nunca se pisa en silencio.
- **Aplicación:**
  - pasa por las mismas operaciones del panel, con su validación, auditoría y evento por el outbox. Producción la ve al instante;
  - antes se guarda la foto exacta de lo que se toca, y «Revertir» la regresa;
  - el diseño se publica como una versión del builder y las etiquetas como una versión de etiquetas, cada una con su historial;
  - también se puede programar, por ejemplo, el catálogo de temporada a las 00:00.
- **Nunca pasan** de sandbox a producción: credenciales, pedidos, clientes ni nada que no sea configuración o contenido.

### Ciclo de vida y observabilidad

- **Crear, refrescar y borrar.** Refrescar vuelve a copiar producción y descarta lo del sandbox, después de confirmarlo y mostrando qué se pierde. Borrar quita todo y verifica que no quede nada.
- **Vencimiento por inactividad** según el plan, con aviso previo.
- **Cupos por plan:** cuántos sandboxes, su tamaño y si incluyen clientes anonimizados.
- **Métricas y registros por sandbox:** peticiones, errores, uso de IA, tamaño, última copia y última publicación. Se ven en Operación › Estado.

## Fases

| Fase | Qué entrega | Cómo se prueba |
|---|---|---|
| SBX-0 Modelo y copia | `kind` y `parent_id`, motor de copia por tabla con su tratamiento por dato, avance visible, selector Producción / Sandbox en el panel | Copia fiel tabla por tabla (conteos y huellas, menos lo excluido); 1 000, 10 000 y 50 000 productos con tiempos; lo excluido no existe en el sandbox |
| SBX-1 Dirección y acceso | URL temporal, subdominio propio con TXT, CNAME y certificado automático, permiso «Sandbox», enlaces de vista previa firmados, contraseña opcional, franja y `noindex` | Sin sesión no se ve (ni en la URL temporal ni en el subdominio); otra tienda no lo ve; el subdominio no puede apuntar a producción; buscadores reciben `noindex`; un enlace vencido no abre |
| SBX-2 Aislamiento de integraciones | Pagos solo en prueba, bandeja de correos, webhooks y canales apagados, conversiones del servidor bloqueadas, etiquetas con sus dos modos | Prueba de «nada sale»: una compra completa en el sandbox no produce ningún cobro real, correo a cliente, webhook ni conversión de servidor; los eventos llevan su marca |
| SBX-3 Publicar a producción | Comparador por área, selección por elemento, conflictos, aplicación auditada, programación y reversión | Publicar, comparar con lo esperado, revertir y comparar con la foto: idéntico. Producción sigue respondiendo en milisegundos durante una publicación de 50 000 productos |
| SBX-4 Ciclo de vida y cupos | Refrescar, vencer, borrar, cupos por plan, métricas | Borrar deja la base idéntica a la foto previa; vencimiento con aviso |
| SBX-5 QA y escala | Guía de QA para personas, relevo de nodos con sandboxes activos, medición con 50 000 productos | Batería completa: pruebas, reversión, auditoría, roles, teclado y escala |

## Decisiones para el dueño del producto

1. **Clientes y pedidos en el sandbox.** Recomendado: copiarlos anonimizados, con la opción de no copiarlos.
2. **Cuántos sandboxes por tienda.** Recomendado: uno incluido en el plan y más como complemento.
3. **Dirección.** Decidido: URL temporal por omisión y subdominio propio opcional, siempre privado.
4. **Vencimiento.** Recomendado: 30 días sin uso, con aviso a los 25. Con subdominio propio, sin vencimiento mientras el plan esté activo.
5. **Etiquetas al crearlo.** Recomendado: IDs de prueba para publicidad, y tráfico marcado para Analytics y mapas de calor.

## Resultado

Lo construido, con sus pruebas y mediciones, está en [`docs/sandbox.md`](../sandbox.md). Diferencias con este plan:
- **Clientes y pedidos:** no se copian anonimizados. Los datos personales están cifrados con la tienda y el registro como datos asociados, así que el sandbox nace sin ellos o con datos de prueba generados.
- **Cupos por plan, publicación programada, contraseña opcional y métricas por sandbox:** quedan pendientes. Hoy hay un sandbox por tienda.
- **Webhooks, canales y conversiones del servidor:** no se copian y conectarlos dentro del sandbox se rechaza. Las automatizaciones y los almacenes externos copiados registran «Capturado en el sandbox» en vez de llamar afuera.

