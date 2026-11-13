# Sandbox por tienda

Cada tienda de producción puede tener uno o varios sandboxes, según su plan: otras tiendas completas, ligadas a ella, donde el equipo prueba todo sin tocar a sus clientes. Lo que está listo se publica a producción por área, viendo antes qué cambia, y cada publicación se revierte en un clic.

Este documento es la referencia técnica. La guía de QA para personas está en `docs/qa.md` §10 y la ayuda del panel en `internal/help/ayuda.md`.

## Modelo

- **Un sandbox es un tenant** con `kind='sandbox'`, su `parent_id` apuntando a producción y un id `sbx-` seguido de 10 caracteres al azar. El prefijo `sbx-` está reservado: `CreateTenant` lo rechaza.
- **Cuántos por tienda:** los que incluya su plan (uno por omisión), cada uno con su nombre, único por tienda sin importar mayúsculas.
- **Estados:** `copying`, `ready`, `failed` y `deleting`. El avance vive en `sandboxes.progress` (tabla actual, filas copiadas y una marca de tiempo).
- **Migraciones:**
  - `160_sandbox.sql`: modelo, base de comparación, publicaciones y sus filas, con RLS forzada.
  - `161_sandbox_wipe.sql`: permite borrar historial de solo-anexar únicamente dentro de un sandbox y con `apexx.sandbox_wipe = 'on'`.
  - `162_sandbox_plans.sql`: varios sandboxes con nombre, contraseña opcional, planes (`tenant_plans`) y publicaciones programadas (`sandbox_schedules`).
  - `165_sandbox_scope.sql`: sandbox ligero o completo.
  - `166_sandbox_stock_moves.sql`: lo que tomaron los pedidos de prueba.
  - `167_operators.sql`: operadores, sus sesiones y su registro, y llaves de la plataforma.

## Sandbox ligero: traer y enviar productos

Al crearlo se elige qué se copia:
- **Ligero (recomendado):** todo menos los productos (diseño, configuración, categorías, marcas, promociones, imágenes). Ocupa muy poco disco.
- **Completo:** también el catálogo entero, con precios y existencias.

En cualquiera de los dos, la sección *Productos del sandbox* permite:
- **Traer de tu tienda real** (`POST /v1/sandboxes/{id}/products`): eliges productos y llegan con todo lo que les pertenece (variantes, precios, listas de precios, existencias, preventas, archivos digitales, imágenes, búsqueda con IA, relaciones del grafo entre ellos, campos propios y opiniones).
  - **Base:** se guarda como base la versión de producción, así que solo lo que cambies después aparece para publicar.
  - **Reinicio:** un producto que ya estaba se deja como está, salvo que se pida dejarlo como en producción (`reset`).
- **Enviar a otro sandbox** (`POST /v1/sandboxes/{id}/send`): copia los productos tal como están, reemplazando su versión en el otro sandbox.
  - **Base:** el destino recibe la base del origen, así que publica los mismos cambios.
  - **Imágenes:** las que se subieron en el origen se copian.
- **Elegir** (`GET /v1/sandboxes/{id}/pick?from=production|sandbox&q=`): busca por nombre, handle, id o SKU y dice cuáles ya están.

Un producto que nunca se trajo no aparece en la comparación: nunca se publica ni se borra desde un sandbox ligero. Refrescar un sandbox ligero vuelve a traer los mismos productos, como están en producción. El límite de productos del plan se revisa al traer y al enviar. Qué filas viajan con un producto está definido en un solo lugar (`productRows`, en `internal/store/sandbox_products.go`).

## Existencias de los pedidos de prueba

Lo que reservan o venden los pedidos de prueba de un sandbox nunca llega a producción.
- **Registro aparte:** en un sandbox, cada venta registra lo que tomó en `sandbox_stock_moves` (una devolución lo resta).
- **Comparación:** las existencias se comparan como «existencias + lo que tomaron los pedidos de prueba», sin las reservas.
- **Publicar:** se escribe ese número, y las reservas de producción (pedidos reales en curso) no se tocan. Revertir tampoco las pisa.
- **Ajustes manuales:** cuando una persona fija una cantidad (inventario, cambios masivos, producto, importación), ese número es el que vale y el registro de esa fila se reinicia.

## Plan de la tienda

Lo que incluye el plan de cada tienda lo fija el operador de la plataforma, nunca la tienda. Se hace desde el superadmin (abajo) o con el comando:

```
apexx tenant plan --id mitienda --plan pro --sandboxes 3 --sandbox-products 100000
apexx tenant plan --id mitienda --show
```

| Opción | Qué limita | Por omisión |
| --- | --- | --- |
| `--sandboxes` | Cuántos sandboxes puede tener la tienda (0: ninguno) | 1 |
| `--sandbox-products` | Cuántos productos caben en cada sandbox (0: sin límite) | 0 |
| `--sandbox-demo-data` | Si puede llenarlos con clientes y pedidos de prueba | sí |
| `--sandbox-schedule` | Si puede programar publicaciones | sí |

- **Número de sandboxes:** se revisa al crear, dentro de la misma transacción que bloquea la fila de la tienda, así que dos clics a la vez no pasan el límite.
- **Tamaño:** se revisa al copiar y al refrescar (contra los productos de producción). Dentro del sandbox se revisa en cada producto que se guarda o importa, en la misma transacción, que se deshace si se pasa. Producción nunca paga esta revisión: solo corre para ids `sbx-`.
- **Respuesta:** lo que el plan no incluye responde 409 `plan_limit` con el motivo en palabras.
- **En el panel:** se muestra arriba del sandbox («Plan pro: incluye 3 sandboxes, usas 1»).
- **No hay asientos:** las personas del equipo son ilimitadas en todas las tiendas. El plan solo limita los sandboxes, que son lo que ocupa disco.

## Superadmin

Los operadores de la plataforma (el equipo de Apexx) entran en `/admin/#superadmin`. Es una identidad aparte: no es miembro de ninguna tienda, y una sesión o llave de tienda responde 403 ahí.

- **Cuentas:** solo por comando. La contraseña es de 12 o más caracteres y la verificación en dos pasos es obligatoria. El secreto se muestra una sola vez y se guarda cifrado con una llave de la plataforma.

  ```
  apexx operator create --email ana@apexx.mx --name "Ana" --password '…'
  apexx operator disable --email ana@apexx.mx
  ```

- **Entrar:** correo, contraseña y código de 6 dígitos.
  - Cada código sirve una sola vez.
  - Hay 10 intentos por minuto por dirección y 5 por cuenta.
  - Un correo desconocido tarda lo mismo que una contraseña equivocada.
  - La sesión dura 8 horas y vive en su propia cookie, limitada a `/v1/operator`.
- **Tiendas:** plan, personas, productos y tamaño estimado. El tamaño son las filas de las tablas grandes por el tamaño medio de una muestra de filas vivas. Se listan los sandboxes de cada tienda con tipo, productos y tamaño. «Cambiar plan» fija cuántos sandboxes, el máximo de productos por sandbox, los datos de prueba y la programación.
- **Registro:** entradas, intentos fallidos y cambios de plan quedan en `operator_log`. El cambio de plan también aparece en la Actividad de la tienda, con el operador como autor.
- **Carga:** el panel de las tiendas no carga el código del superadmin (es un archivo aparte que solo se pide en `#superadmin`).

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
- **Contraseña opcional:** se guarda como hash argon2id.
  - Quien abre la tienda sin sesión ni enlace ve un formulario accesible. La contraseña correcta deja una cookie firmada por 12 horas que lleva una huella del hash: cambiar la contraseña invalida las cookies anteriores.
  - Los intentos se limitan a 10 por minuto por dirección y 60 por minuto por sandbox (429 después).
- **Entrar y salir:** `POST /v1/sandboxes/{id}/enter` y `/v1/sandbox/exit` cambian la sesión de tienda. Se puede pasar de un sandbox a otro de la misma tienda sin volver a producción, si la persona sigue en el equipo de producción. La sesión anterior (cookie o `Bearer aps_`) se cierra y el cambio queda auditado como `session.switch`.
- **Rutas:** `/v1/sandboxes` y `/v1/sandboxes/{id}/…` actúan sobre un sandbox por su id. Las rutas `/v1/sandbox/…` siguen funcionando: dentro de un sandbox actúan sobre ese sandbox, y desde producción sobre el más antiguo. El id de un sandbox de otra tienda responde 404.
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
- **Programar:** con `runAt` (de 1 minuto a 90 días) la publicación queda en `sandbox_schedules` y se publica lo que tenga el sandbox a esa hora.
  - **Quién ejecuta:** el trabajo `sandbox-schedules`, cada minuto. Cada publicación la toma un solo nodo (`FOR UPDATE SKIP LOCKED`).
  - **Permisos:** al ejecutarse se revisa de nuevo que quien la programó siga siendo administrador de producción.
  - **Cancelar y resultado:** se puede cancelar mientras esté pendiente. Su resultado (publicada, sin cambios o el motivo del fallo) queda en la lista y en Actividad.
  - **Al borrar el sandbox:** sus publicaciones programadas se borran con él.
- **Datos derivados:** el grafo de conocimiento (`kg_edges`) se construye en los dos lados con ids propios. Una relación del sandbox reemplaza a la misma relación de producción (misma llave natural) en vez de chocar, y la reversión regresa la de producción.
- **Velocidad:** las filas se buscan por las columnas de su llave, con índice, y no por un texto calculado. Publicar o revertir unas pocas filas en una tienda de 50 000 productos toma milisegundos. Si una llave contiene el separador, se usa la comparación exacta, que es más lenta.
- **Tiempo:** publicar, revertir y comparar tienen hasta 5 minutos (no los 15 s de una petición normal) y terminan completos aunque se cierre el navegador.
- **Revertir:**
  1. Se comprueba fila por fila que producción no cambió después de publicar. Si cambió, responde `ErrRevertConflict` y solo sigue con `force`.
  2. Se restaura con upsert: las filas borradas o cambiadas vuelven y las insertadas por la publicación se borran.
  3. Las revisiones del sitio, que son de solo-anexar, se conservan, y la base vuelve a como estaba.

## Ciclo de vida

- **Refrescar** vuelve a copiar producción. Conserva la llave, los dominios, el equipo, las sesiones y la auditoría, así que la dirección y la sesión siguen sirviendo.
- **Borrar** quita todo y verifica que no quede ninguna fila. La auditoría, el historial de precios y la analítica del sandbox se borran gracias a la migración 161. Lo ya publicado se queda en producción.
- **Reemplazo de un nodo:** un nodo que se apaga deja de tomar trabajo nuevo y espera hasta 2 minutos a que terminen las copias y publicaciones programadas que lleva (`FinishSandboxWork`). Lo que no alcance queda marcado como fallido con su motivo, nunca «copiando» para siempre. Una copia de 50 000 productos tarda unos 15 s.
- **`RunSandboxLifecycle`**, cada 15 minutos con lease:
  - una copia atorada más de 30 minutos queda `failed`;
  - a los 25 días sin uso se avisa a los dueños por correo (`TplSandboxExpiring`, en 6 idiomas);
  - a los 30 días se borra.

## Métricas

*Operación › Estado* muestra cada sandbox de la tienda (`GET /v1/ops/sandboxes`). Las mismas cifras de peticiones salen en `/metrics` como `apexx_sandbox_requests_total`, `apexx_sandbox_errors_total` y `apexx_sandbox_denied_total`, con la etiqueta `sandbox`.

- **Estado y vencimiento:** listo, copiando, fallido o borrando; si tiene contraseña; cuándo vence.
- **Tamaño:** productos, precios y pedidos.
- **Copias:** cuándo fue la última y cuánto tardó.
- **Peticiones:** de este nodo desde que arrancó, contando errores (5xx) y rechazos (403, visitantes sin acceso).
- **Publicaciones:** cuántas, la última y las programadas pendientes.
- **Correos capturados.**
- **IA de los últimos 30 días:** peticiones y costo.

## Medición a 50 000 productos

Medido con `tools/qa/escala/run.js 50000` y `APEXX_QA_SANDBOX=1`, en el entorno de desarrollo, con los servidores y Postgres en la misma máquina. Es la última corrida completa, con todo lo de este documento.

| Paso | Resultado |
| --- | --- |
| Copiar 50 000 productos y 100 000 precios | 13.5 s hasta usable |
| Subir 10% los 100 000 precios dentro del sandbox | 14.3 s |
| Calcular la diferencia | 1.0 s: 100 000 cambios, 0 conflictos |
| Publicar los precios a producción | 11.6 s; visibles en la tienda 3.8 s después |
| Producción **durante** la publicación (16 compradores a la vez, 16 295 peticiones) | p50 13.4 ms · p95 31.8 ms · p99 58.3 ms |
| Revertir la publicación | 8.8 s, 100 000 filas, idéntico a la foto |
| Borrar el sandbox | 5.8 s, ninguna fila restante |
| Base de datos al final de toda la corrida | Ninguna tabla distinta a la foto inicial |

**Sandbox ligero en la misma tienda de 50 000 productos**

| Paso | Resultado |
| --- | --- |
| Crearlo (sin productos) | 0.5 s |
| Traer 500 productos (1 000 variantes, 1 000 precios) | 0.3 s |
| Comparar con producción sin cambios | 1.3 s, 0 cambios falsos |
| Borrarlo | 0.2 s, ninguna fila restante |

**Lo que estas mediciones encontraron y se corrigió:** publicar 100 000 precios llegó a tardar 5 minutos en vez de 11 s. La tabla temporal de la publicación (`_pub`) no tenía estadísticas, porque Postgres nunca las calcula solo para tablas temporales, y según el momento el planificador elegía un plan cuadrático. Ahora se analiza al cargarla, y la publicación tarda 11 s siempre.

## Relevo de nodos con sandboxes activos

`tools/qa/relevo/run.sh` con `APEXX_QA_SANDBOX=1`, dentro de la misma corrida de 50 000 productos.

**Montaje**
- Dos nodos detrás de Caddy, con 16 clientes durante 100 s. Cada nodo se reemplaza dos veces.
- El tráfico incluye la tienda del sandbox (con enlace), su búsqueda, su panel y visitas sin acceso (403 siempre), además de la tienda y el panel de producción.
- Una publicación programada vence a mitad del relevo.
- Justo antes de reemplazar el primer nodo, se le pide la copia de un segundo sandbox de 50 000 productos.

| Qué | Resultado |
| --- | --- |
| Peticiones | 129 889, **0 fallos**, la más lenta 1.4 s |
| Copia en el nodo reemplazado | Terminó lista (16.7 s, 50 000 productos). El nodo tardó 17 s en apagarse en lugar de 6 porque esperó la copia |
| Publicación programada | Publicada una sola vez durante el relevo (4 cambios), visible en producción y revertida exacta |
| Al final | Los dos sandboxes borrados, ninguna tienda restante |

**Lo que la prueba encontró y se corrigió**
- **Publicar en una tienda grande agotaba el tiempo.** Publicar un solo producto en una tienda de 50 000 tardaba más de 15 s: las filas se buscaban por un texto calculado, sin índice. Ahora se buscan por las columnas de la llave y tardan milisegundos.
- **Choques en el grafo de conocimiento.** El grafo, construido en paralelo en los dos lados, chocaba al publicar y además inflaba el conteo («4661 cambios» por un título). Ahora solo viajan las relaciones que decidió una persona, y la misma relación con otro id reemplaza a la de producción.
- **Reversión de 5 minutos.** Revertir llegó a tardar 5 minutos porque Postgres planeaba con estadísticas viejas tras la copia masiva. Ahora la copia y las publicaciones grandes actualizan las estadísticas (`apexx_analyze_sandbox()`, que corre como dueño de las tablas y solo las analiza).
- **Relaciones huérfanas del grafo.** El trabajo del grafo podía insertar relaciones de productos que se estaban borrando al mismo tiempo. Ahora bloquea esos productos en modo compartido antes de insertar.
- **Nodos que se apagaban a media copia.** Un nodo podía apagarse con una copia en curso y dejarla «copiando». Ahora la espera o la marca como fallida.

## Lo que falta

Los fragmentos de cada proveedor de píxeles (Meta, TikTok, Google Ads, Pinterest, Analytics, Tag Manager) siguen sin probarse con cuentas reales: hace falta una cuenta de cada uno para confirmar que el evento llega a su panel.
