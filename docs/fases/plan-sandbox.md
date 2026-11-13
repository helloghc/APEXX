# Plan: sandbox por tienda

Fecha: 13 de noviembre de 2026. Estado: **plan, por construir.**

## Qué es

Cada tienda puede tener su sandbox: una copia idéntica de todo lo que tiene que nunca pasa a producción.
- Incluye catálogo, precios, promociones, diseño, contenido, configuración, etiquetas y apps.
- Se ve solo en una URL temporal suya, que solo su equipo puede abrir y administrar.
- Ahí se prueba sin miedo: un rediseño, una promoción, un píxel nuevo, una integración.
- Cuando algo está bien, se pasa a producción con un clic, viendo qué cambia, y se puede revertir.

Es un servicio que se vende con el plan.

## Principios

1. **Mismo código, mismo motor.** El sandbox es una tienda más dentro de Apexx, con su propio identificador y el mismo aislamiento por fila (RLS). No hay una versión aparte del sistema. Todo lo que funciona en producción funciona igual en el sandbox, en milisegundos.
2. **Nada sale del sandbox sin querer:**
   - no cobra con tarjetas reales;
   - no manda correos a clientes;
   - no avisa a conectores de producción;
   - no publica en Mercado Libre ni Amazon;
   - no se indexa en buscadores.
3. **Credenciales propias.** El sandbox nunca usa las llaves de producción: Stripe, paqueterías, ERP, webhooks, canales de venta. Para analítica y píxeles el cliente elige entre dos caminos: poner IDs de prueba, o conservar los de producción con todo el tráfico marcado como sandbox para filtrarlo.
4. **Reversible y auditado.** Crear, refrescar, pasar a producción y borrar quedan en la auditoría. Pasar a producción guarda una foto antes y se revierte con un clic. Borrar el sandbox deja la base exactamente como estaba, verificado tabla por tabla como en la prueba de escala.

## Arquitectura

### El sandbox es otra tienda, ligada a la de producción

- `tenants` gana dos columnas: `kind` (`production` o `sandbox`) y `parent_id`, la tienda de producción. Por ejemplo, `acme` y su sandbox `acme~sbx`.
- Todas las tablas ya usan `tenant_id` con RLS forzado. La copia hereda el aislamiento sin cambiar ninguna consulta.
- La proyección en memoria lo carga como cualquier tienda. Con 50 000 productos ya medimos carga en 1–4 ms y vectores en 1,3 s.

### Motor de copia

1. Una transacción en `REPEATABLE READ` toma una foto consistente de la tienda de producción.
2. Por cada tabla de la tienda (hoy son 144), `INSERT … SELECT` reescribe `tenant_id`. Las llaves ya llevan `tenant_id`, así que no hay que renumerar nada.
3. La lista de tablas sale del catálogo de Postgres, igual que en la foto de la prueba de escala. Una tabla nueva entra sola o rompe una prueba, nunca se olvida en silencio.

Tratamiento por tipo de dato:

| Dato | En el sandbox |
|---|---|
| Catálogo, precios, existencias, promociones, diseño, sitios, contenido, blog, políticas, configuración, etiquetas, campos propios, reglas | Se copia igual |
| Imágenes y archivos | Se referencian, no se duplican: son de solo lectura y por contenido; una imagen nueva del sandbox es solo suya |
| Vectores y grafo de IA | Se copian; se recalculan solos si algo cambia |
| Clientes y pedidos | Recomendado: anonimizados (nombre, correo, teléfono y dirección falsos y consistentes) o una muestra. Opción del cliente: sin clientes ni pedidos |
| Pagos, facturas, sesiones, llaves de API, cuentas de pago, credenciales cifradas | **No se copian.** El sandbox empieza sin ellas |
| Auditoría, outbox, historiales | No se copian: el sandbox empieza su propia historia |
| Webhooks y suscripciones de conectores | Se copian **apagados** y marcados «de producción»; se encienden con URLs de prueba |

**Meta de velocidad:** crear el sandbox de una tienda con 50 000 productos en menos de 60 s, con avance visible. Las tiendas chicas, en segundos.

### Dirección y acceso

- **URL temporal propia:** `https://{token}.sandbox.apexx.app`. El token es aleatorio y se rota al refrescar. El dominio propio del cliente nunca apunta al sandbox.
- **Para entrar** hace falta una sesión del equipo de la tienda con el permiso nuevo «Sandbox» (ver o administrar), o un enlace de vista previa firmado y con vencimiento, para compartir con alguien de fuera.
- **No se indexa:** `X-Robots-Tag: noindex`, `robots.txt` cerrado, sin sitemap y sin feeds para Google Shopping ni Meta.
- **Siempre se nota:** una franja «Sandbox — no es tu tienda real» en el panel y en la tienda, para no confundirse.

### Integraciones aisladas

| Integración | Comportamiento en el sandbox |
|---|---|
| Pagos | Solo modo de prueba de Stripe, con sus tarjetas de prueba. El código impide cobrar en vivo desde una tienda `sandbox` |
| Correos | Se capturan en una bandeja del sandbox. Opcionalmente se envían solo a correos del equipo. Nunca a clientes |
| Webhooks y conectores | Apagados hasta configurar URLs de prueba |
| Mercado Libre, Amazon, ERP, paqueterías | Desconectados. Se conectan con credenciales de prueba si el proveedor las tiene |
| IA con Claude | Usa la llave de Apexx con el mismo presupuesto por tienda; el uso se reporta aparte |

### Etiquetas y píxeles en el sandbox

Al crear el sandbox se copian las etiquetas, y el cliente elige uno de dos modos.

1. **IDs de prueba (recomendado para anuncios).**
   - Las etiquetas de publicidad quedan apagadas con el aviso «Pon el ID de tu píxel de prueba».
   - El cliente usa otra propiedad de Analytics, un píxel de prueba de Meta, etcétera.
   - Como el sandbox es otra tienda, su configuración de etiquetas es independiente: publicar en el sandbox nunca toca producción.

2. **Mismos IDs, tráfico marcado.** Las etiquetas siguen con los IDs de producción, y todo lo que sale del sandbox lleva su marca:
   - Toda URL del sandbox lleva `?apexx_env=sandbox`, así las páginas vistas se pueden excluir por URL en cualquier herramienta.
   - Cada evento del `dataLayer` lleva `apexx_env: "sandbox"`.
   - En GA4 se envía `traffic_type: "internal"`; el filtro de tráfico interno de GA4 lo excluye.
   - En Meta se puede poner el `test_event_code` del Administrador de eventos: los eventos aparecen en «Probar eventos» y no cuentan como conversiones.
   - Adobe recibe `apexx_env` en el Data Layer, para usarlo como regla o segmento.
   - Las conversiones desde el servidor (`conversions.go`) **nunca** se envían desde el sandbox.

   El panel explica en cada proveedor cómo crear el filtro de su lado.

### Pasar a producción

- **Comparador por área:** catálogo, precios, promociones, diseño, contenido, configuración y etiquetas. Muestra qué se agrega, qué cambia y qué se quita, como el historial en palabras de las etiquetas.
- **Selección:** se elige qué áreas o qué elementos pasar.
- **Conflictos:** si producción cambió algo desde la copia, se marca y se decide caso por caso. Nunca se pisa en silencio.
- **Aplicación:**
  - pasa por las mismas operaciones del panel, con su validación, su auditoría y su evento por el outbox;
  - antes se guarda la foto de lo que se va a tocar;
  - «Revertir» la regresa exactamente;
  - el diseño se publica como una versión del builder y las etiquetas como una versión de etiquetas, cada una con su historial.
- **Nunca pasan** de sandbox a producción: credenciales, pedidos, clientes ni nada que no sea configuración o contenido.

### Ciclo de vida y observabilidad

- **Crear, refrescar y borrar.** Refrescar vuelve a copiar producción y descarta lo del sandbox, después de confirmarlo y mostrando qué se pierde. Borrar quita todo y verifica que no quede nada.
- **Vencimiento por inactividad**, configurable por plan (por ejemplo, 30 días), con aviso por correo antes.
- **Cupos por plan:** cuántos sandboxes, de qué tamaño y si incluyen clientes anonimizados.
- **Métricas y registros por sandbox:** peticiones, errores, uso de IA, tamaño y fecha de la última copia. En Operación › Estado de la tienda de producción se ve su sandbox.

## Fases

| Fase | Qué entrega | Cómo se prueba |
|---|---|---|
| SBX-0 Modelo y copia | `kind` y `parent_id`, motor de copia por tabla con su tratamiento por dato, avance de la copia | Copia fiel tabla por tabla (conteos y huellas, menos lo excluido); 1 000, 10 000 y 50 000 productos con tiempos; nada de lo excluido existe en el sandbox |
| SBX-1 URL y acceso | Dominio de sandbox, permiso «Sandbox», enlaces de vista previa firmados, franja visible, `noindex` | Sin sesión no se ve; otra tienda no lo ve; buscadores reciben `noindex`; el enlace vencido no abre |
| SBX-2 Aislamiento de integraciones | Pagos solo en prueba, bandeja de correos, webhooks y canales apagados, conversiones del servidor bloqueadas, etiquetas con sus dos modos | Prueba de «nada sale»: un recorrido completo de compra en el sandbox no produce ningún cobro real, correo a cliente, webhook ni conversión de servidor; los eventos llevan su marca |
| SBX-3 Pasar a producción | Comparador por área, selección, conflictos, aplicación auditada y reversión | Pasar, comparar con lo esperado, revertir y comparar con la foto: idéntico |
| SBX-4 Ciclo de vida y cupos | Refrescar, vencer, borrar, cupos por plan, métricas | Borrar deja la base idéntica a la foto previa; vencimiento con aviso |
| SBX-5 QA y escala | Guía de QA para personas, relevo de nodos con sandboxes activos, medición con 50 000 productos | Batería completa: pruebas, reversión, auditoría, roles, teclado y escala |

## Decisiones para el dueño del producto

Cada una tiene una recomendación.

1. **Clientes y pedidos en el sandbox.** Recomendado: copiarlos anonimizados, con la opción de no copiarlos. Así se prueban segmentos, campañas y reportes sin datos personales reales.
2. **Cuántos sandboxes por tienda.** Recomendado: uno incluido en el plan y más como complemento.
3. **Dominio.** Recomendado: `{token}.sandbox.apexx.app`, sin dominios propios del cliente en el sandbox.
4. **Vencimiento.** Recomendado: 30 días sin uso, con aviso a los 25.
5. **Etiquetas por omisión al crear.** Recomendado: modo «IDs de prueba» para publicidad y modo «tráfico marcado» para Analytics y mapas de calor.
