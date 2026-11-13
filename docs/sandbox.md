# Sandbox por tienda

Cada tienda de producción puede tener un sandbox: otra tienda completa, ligada a ella, donde el equipo prueba todo sin tocar a sus clientes. Lo que está listo se publica a producción por área, viendo antes qué cambia, y cada publicación se revierte en un clic.

Este documento es la referencia técnica. La guía de QA para personas está en `docs/qa.md` §10 y la ayuda del panel en `internal/help/ayuda.md`.

## Modelo

- **Un sandbox es un tenant** con `kind='sandbox'`, su `parent_id` apuntando a producción y un id `sbx-` seguido de 10 caracteres al azar. El prefijo `sbx-` está reservado: `CreateTenant` lo rechaza.
- **Uno por tienda**, garantizado por `UNIQUE (parent_id)` en `sandboxes`.
- **Estados:** `copying`, `ready`, `failed` y `deleting`. El avance vive en `sandboxes.progress` (tabla actual, filas copiadas y una marca de tiempo).
- **Migraciones:** `160_sandbox.sql` (modelo, base de comparación, publicaciones y sus filas, RLS forzada) y `161_sandbox_wipe.sql` (permite borrar historial de solo-anexar únicamente dentro de un sandbox y con `apexx.sandbox_wipe = 'on'`).

## Copia

Cada una de las 149 tablas con `tenant_id` tiene una regla en `internal/store/sandbox.go` (`sandboxTables`). `TestSandboxEveryTableHasARule` falla si aparece una tabla nueva sin regla, así que nadie puede olvidar decidir qué pasa con ella.

| Regla | Qué hace | Ejemplos |
| --- | --- | --- |
| `copy` | Se copia igual, cambiando `tenant_id` | catálogo, precios, existencias, promociones, diseño, envíos, impuestos, automatizaciones |
| `skip` | No se copia | llaves de API, sesiones, cuentas de pago, cuentas de canales, webhooks, dominios, historial, analítica |
| `pii` | Datos personales: no se copian | clientes, pedidos, pagos, cotizaciones, empresas, suscripciones |
| `members` | El equipo se sincroniza sin contraseña (`'!sandbox'`) ni TOTP | `members` |
| `special` | Cifrado con la llave de la tienda: se descifra con la de producción y se cifra con la del sandbox | `entity_records` |

Algunas tablas copiadas llevan ajustes:
- en `custom_values` el campo `secret` queda vacío;
- las paqueterías conectadas por HTTP pasan al proveedor de prueba;
- de `tenant_docs` no viajan los documentos con credenciales (inicio de sesión único, conversiones de anuncios, origen del catálogo) ni el historial de etiquetas.

**Cómo copia:**
1. Toda la copia es una transacción `REPEATABLE READ`, con `INSERT … SELECT` por tabla en orden topológico de llaves foráneas. El sandbox ve una foto coherente de producción y producción no se bloquea.
2. Al terminar se emite `tenant.reload`. La copia drena la proyección y espera a que la tienda esté en memoria antes de marcarla `ready`: listo significa usable.

**Datos de clientes.** Por omisión el sandbox nace sin clientes ni pedidos (`dataMode: none`). Con `dataMode: demo` se generan clientes y pedidos de prueba.

No se copian clientes reales anonimizados. Los datos personales están cifrados con la tienda y el registro como datos asociados, así que una copia directa no se puede leer. Re-cifrarlos para el sandbox metería datos reales en un ambiente de pruebas, que es justo lo que el sandbox evita.

## Acceso

- **Dirección temporal:** `{id}.$APEXX_SANDBOX_HOST` cuando la variable existe, si no `/s/{id}/main/`. El host se registra como dominio verificado, así que el TLS bajo demanda de Caddy (`/internal/tls/ask`) emite su certificado.
- **Subdominio propio:** se agrega dentro del sandbox en *Configuración › Dominios*, con su registro TXT. Un sandbox con subdominio propio no vence.
- **Privado siempre:** `sandboxGate` corre después de `siteRouting` y deja pasar solo a quien tenga:
  - una sesión del equipo del propio sandbox;
  - un enlace de vista previa firmado (`?sbx=`, que deja la cookie `apexx_sbx`).

  A los demás les responde 403: una página privada en HTML, o JSON para el API y los archivos. Siempre manda `X-Robots-Tag: noindex`.
- **Enlaces de vista previa:** `base64(id|vence).hmac`. La firma usa una llave derivada por tienda (`Keys.Blind(id, "sandbox-preview")`), y un enlace vencido no abre.
- **Entrar y salir:** `POST /v1/sandbox/enter` y `/exit` cambian la sesión de tienda. La sesión anterior (cookie o `Bearer aps_`) se cierra y el cambio queda auditado como `session.switch`.
- **Franja:** una barra naranja siempre visible en la tienda, el builder y el panel. Las páginas del builder en un sandbox se sirven `no-store`.
- **Equipo:** se administra solo en producción. Dentro del sandbox, cambiar el equipo responde 409, salvo las llaves de API.

## Nada sale

Un sandbox nunca habla con un servicio real en nombre de la tienda. Cada salida tiene su guarda, y `TestSandboxNothingLeaves` y `TestSandboxPaymentsNeverLive` las prueban.

| Salida | Qué pasa en el sandbox |
| --- | --- |
| Cobros | `PaymentAccounts.IsSandbox` fuerza llaves `sk_test_` o el proveedor simulado; guardar una llave viva se rechaza |
| Correos | Se guardan como `skipped` con «Capturado en el sandbox: no se envía» y se ven en *Correos capturados*. El envío solo toma tiendas de producción |
| Webhooks | Crearlos responde 409. El despachador no genera entregas para sandboxes aunque exista una suscripción |
| Canales de venta (Mercado Libre, Amazon) | Conectarlos responde 409; la sincronización salta sandboxes |
| Conversiones del servidor | Configurarlas responde 409; un trabajo pendiente se cierra como capturado sin enviarse |
| Automatizaciones con acción webhook (se copian) | La acción registra «Capturado en el sandbox» y no llama a nadie |
| Almacenes externos (se copian con las ubicaciones) | El pedido de surtido se rechaza como capturado |
| Paqueterías | Las conectadas por HTTP quedan en modo de prueba al copiar |
| Píxeles y etiquetas | `tags.js` lleva `"env":"sandbox"`. La URL gana `?apexx_env=sandbox`, el `dataLayer` recibe `apexx_env` y GA4 marca `traffic_type: internal`, así que Analytics puede filtrar ese tráfico |

Reglas de checkout externas y cálculo de impuestos sí se consultan: son lecturas sin efecto, y el carrito del sandbox debe calcular igual que producción.

## Publicar a producción

- **Áreas:** `catalog`, `prices`, `stock`, `promotions`, `design`, `settings` y `tags`. Las etiquetas se publican como una versión nueva de etiquetas, con su propio historial.
- **Comparación de tres vías:** al copiar se guarda la huella `md5` de cada fila publicable en `sandbox_base`. Al comparar, cada fila se clasifica como nueva, cambiada o borrada, y es **conflicto** si producción también cambió desde la copia. Los conflictos solo se aplican con `force`.
- **Aplicar:**
  1. Las filas viajan a una tabla temporal con `COPY`.
  2. Antes de tocar nada se guarda la foto de cada fila afectada en `sandbox_publish_rows.before`, incluidas las que borraría un `ON DELETE CASCADE`.
  3. Se borra de hijos a padres y se actualiza e inserta de padres a hijos.
  4. Queda la huella posterior de cada fila y la base avanza.
  5. Se emiten `tenant.reload` y la reindexación de vectores, y se copian los archivos nuevos al almacenamiento de producción.
- **Revertir:**
  1. Se comprueba fila por fila que producción no cambió después de publicar. Si cambió, responde `ErrRevertConflict` y solo sigue con `force`.
  2. Se restaura con upsert: las filas borradas o cambiadas vuelven y las insertadas por la publicación se borran.
  3. Las revisiones del sitio, que son de solo-anexar, se conservan, y la base vuelve a como estaba.

## Ciclo de vida

- **Refrescar** vuelve a copiar producción. Conserva la llave, los dominios, el equipo, las sesiones y la auditoría, así que la dirección y la sesión siguen sirviendo.
- **Borrar** quita todo y verifica que no quede ninguna fila. La auditoría, el historial de precios y la analítica del sandbox se borran gracias a la migración 161. Lo ya publicado se queda en producción.
- **`RunSandboxLifecycle`**, cada 15 minutos con lease:
  - una copia atorada más de 30 minutos queda `failed`;
  - a los 25 días sin uso se avisa a los dueños por correo (`TplSandboxExpiring`, en 6 idiomas);
  - a los 30 días se borra.

## Medición a 50 000 productos

Medido con `tools/qa/escala/run.js 50000` y `APEXX_QA_SANDBOX=1`, en el entorno de desarrollo, con un servidor y Postgres en la misma máquina.

| Paso | Resultado |
| --- | --- |
| Copiar 50 000 productos y 100 000 precios | 14.4 s hasta usable |
| Subir 10% los 100 000 precios dentro del sandbox | 16.0 s |
| Calcular la diferencia | 3.0 s: 100 000 cambios, 0 conflictos |
| Publicar los precios a producción | 12.4 s; visibles en la tienda 4.4 s después |
| Producción **durante** la publicación (16 compradores a la vez, 16 943 peticiones) | p50 14.3 ms · p95 33.1 ms · p99 54.1 ms |
| Revertir la publicación | 7.1 s, 100 000 filas, idéntico a la foto |
| Borrar el sandbox | 5.9 s, ninguna fila restante |
| Base de datos al final de toda la corrida | Ninguna tabla distinta a la foto inicial |

## Lo que falta

Estas partes del plan no están construidas:
- cupos por plan (más de un sandbox, tamaño máximo);
- programar una publicación a futuro;
- contraseña opcional para la tienda del sandbox;
- métricas por sandbox en *Operación › Estado*.

La prueba de relevo de nodos con sandboxes activos no se ha corrido.

Los fragmentos de cada proveedor de píxeles siguen sin probarse con cuentas reales.
