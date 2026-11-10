# Auditoría de uso humano — Apexx

> Fecha: 2026-11-10 · Alcance: Apexx operado como lo harían personas reales (dueño de tienda, administrador de equipo, comprador y operador). VTEX no se tocó.
> No se incluyen llaves, tokens ni contraseñas reales.

## 1. Método

| Parámetro | Valor |
| --- | --- |
| Binario | Compilado del repositorio actual (`go build ./cmd/apexx`), corriendo en `127.0.0.1:8095` con `APEXX_PAYMENTS=sandbox` |
| Base de datos | PostgreSQL 16 local, compartida con otras instancias de prueba |
| Tienda | `ux-test`, creada con `apexx tenant` y 200 productos de `apexx seed` |
| Navegador | Chromium headless con Playwright. Escritorio 1366×900; móvil "Moto G4" con Slow 4G (150 ms RTT, 1,6 Mbps) y CPU 4× para la tienda; "Pixel 5" para el editor |
| Accesibilidad | axe-core 4.10.2 (reglas WCAG 2.0 A/AA y 2.1 AA) y un recorrido solo con teclado (40 pulsaciones de Tab) |
| Rendimiento de la tienda | 3 corridas por modo, sin caché; se reporta la mediana |
| Compras | Mismas llamadas que haría un storefront: `simulate` → `checkout` con `Idempotency-Key` → pedido → factura emitida por el worker |

**Contexto importante para comparar con VTEX:** Apexx corrió en la misma máquina que el navegador, sin distancia de red. Las cifras de VTEX (`docs/audit/frontend.md`) se midieron contra servidores remotos. Además, la página de inicio de Apexx tiene hoy 4 secciones simples, mientras que la de VTEX tiene 601 bloques. La comparación muestra el orden de magnitud de la arquitectura, no una carrera en igualdad de contenido.

## 2. Dueño de la tienda: editar y publicar la home

| # | Tarea | Tiempo | Clics/acciones | Resultado |
| --- | --- | ---: | ---: | --- |
| 1 | Abrir `/admin/` sin haber iniciado sesión | 1,6 s | 1 | Mensaje: "No tienes acceso. Revisa tu clave de API en Acceso." No hay pantalla de inicio de sesión (ver H1) |
| 2 | Iniciar sesión | 0,24 s | — | Solo posible por API (`POST /v1/auth/login`). La cookie de sesión funciona en el editor sin pedir clave |
| 3 | Cargar el editor con sesión | 0,24 s | 1 | LCP 96 ms, TTFB 2 ms. Muestra las 4 secciones |
| 4 | Arrastrar "Texto" (Promociones) encima de "Carrusel de productos" (Novedades) | 0,72 s | 1 arrastre | Correcto; "Guardado" visible. Orden nuevo en la vista previa |
| 5 | Cambiar el título del banner | 0,91 s | 2 | La vista previa muestra "Ofertas de invierno" como `h1` |
| 6 | Escribir un texto alternativo de 3 letras | 1,29 s | 1 | Semáforo "1 bloqueo(s)", botón Publicar desactivado, mensaje "Mínimo 5 caracteres" junto al campo |
| 7 | Corregirlo | 1,22 s | 1 | Semáforo "Lista para publicar · HTML 0,7 KB / 40 KB · JS 6 KB / 80 KB" |
| 8 | Publicar | 55 ms | 1 | Aviso "Publicado en 4,56 ms" con botón "Deshacer publicación" |
| 9 | Ver la tienda en vivo | 5 ms | — | HTTP 200, nuevo orden y nuevo título visibles |
| 10 | Deshacer la publicación | 0,84 s | 1 | Error "No hay una publicación anterior": el botón aparece aunque sea la primera publicación (ver H5) |
| 11 | Ver el historial | 0,63 s | 1 | Lista la publicación con fecha, autor (id interno `member:mem_…`) y duración |

**Total desde la sesión iniciada hasta la tienda actualizada (pasos 3–9): unos 5 s y 8 acciones**, incluido corregir un error. En VTEX la misma tarea requiere editar el tema y pasar por `vtex link/publish/install`: 1 a 3 horas de un desarrollador (`docs/audit/builder.md`).

**Accesibilidad y peso del editor:**

| Comprobación | Resultado |
| --- | --- |
| axe-core en el editor | 0 violaciones WCAG 2.1 AA |
| axe-core en la tienda publicada | 0 violaciones |
| Recorrido solo con teclado | 26 controles alcanzables. Solo el `iframe` de vista previa no muestra indicador de foco |
| Reordenar con teclado | Funciona (cubierto por la prueba E2E del editor) |
| Editor en Pixel 5 | Sin desborde horizontal; la lista de secciones es visible |
| Peso del editor | 1 JS de 127 KB transferido + 3,7 KB de CSS; 9 peticiones |
| Errores de consola | Solo los 401 esperados antes de iniciar sesión |

## 3. Administrador de equipo

No existe pantalla de equipo; todo se hizo por API (ver H2).

| Acción | Resultado |
| --- | --- |
| Crear 2 miembros (catálogo+inventario: editor; pedidos: lector) | 340 ms en total |
| Editora de catálogo: leer y editar productos, cambiar stock | 200 / 200 / 204 |
| Editora de catálogo: cambiar precios, ver pedidos, publicar sitio, ver equipo | 403 en los cuatro |
| Lector de pedidos: ver pedidos | 200 |
| Lector de pedidos: ver productos, cambiar stock, cancelar pedido, ver cuenta de pagos | 403 en los cuatro |
| Desactivar a la editora | Su sesión muere al instante (401) y no puede volver a entrar |
| El dueño intenta desactivarse siendo el único dueño | Bloqueado: "La tienda debe conservar al menos un dueño activo" |
| Cerrar sesión | El token deja de servir de inmediato (401) |
| Contraseña de 5 caracteres | 400 "la contraseña debe tener al menos 12 caracteres" |
| Módulo mal escrito (`catalogo`) | 400 `unknown module "catalogo"`: mensaje técnico y en inglés (H8) |
| 11 intentos fallidos con la misma IP | 429 desde el intento 8; los inicios de sesión exitosos previos cuentan contra el límite (H7) |
| 12 intentos fallidos cambiando `X-Forwarded-For` | Ningún 429: el límite se evade (H3) |

## 4. Comprador

### 4.1 Página de inicio publicada (`/s/ux-test/main/index`)

| Modo | TTFB | FCP | LCP | CLS | TBT | Load | Peticiones | Transferido | JS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Móvil (Moto G4, Slow 4G, CPU 4×) | 2,7 ms | 220 ms | **220 ms** | **0** | **0 ms** | 357 ms | 2 | 1,6 KB | 0 archivos |
| Escritorio | 2,2 ms | 28 ms | 28 ms | 0 | 0 ms | 13 ms | 2 | 1,6 KB | 0 archivos |

Una de las 2 peticiones es la imagen del banner, que responde **404** (H4). La página no tiene imágenes reales ni productos con foto, así que el LCP medido es de texto.

### 4.2 Compra por API, como lo haría un storefront

| Paso | Latencia | Resultado |
| --- | ---: | --- |
| Simular carrito (2 líneas, 3x2 aplicado) | 10,7 ms | Total 881,00 MXN con envío; cada promoción explica si aplica |
| Checkout con `pm_card_visa` | 23,3 ms | Pedido `paid`; el total coincide con la simulación |
| Repetir el checkout con la misma `Idempotency-Key` (doble clic) | 1,5 ms | Devuelve el mismo pedido, sin segundo cobro |
| Factura emitida por el worker | 1,0 s | Estado `issued`, con URL |
| Tarjeta rechazada (`pm_card_chargeDeclined`) | 11,1 ms | 402 `payment_declined`, pedido `cancelled`, stock liberado |
| Sin `Idempotency-Key` | — | 400 con instrucción clara |
| Sin código postal | — | 400 `postal_code_required` con mensaje claro |
| Pedir 999 unidades con stock insuficiente | — | 409 `cart_changed` con texto técnico en inglés (H6) |
| Enviar un número de tarjeta en lugar de `pm_…` | — | 400 "Apexx nunca recibe datos de tarjeta" |
| 20 checkouts completos seguidos | p50 9,3 ms · p95 14,7 ms · máx 17,8 ms | Todos `paid` |

Textos para el comprador que hay que mejorar (H6):
- "Faltan 204400 (unidades menores) para alcanzar el mínimo": debería decir "Te faltan $2,044.00 para el 10% de descuento".
- "3x2 aplicado 1 vez/veces".
- "orders: cart could not be fully allocated".

## 5. Operador

| Comprobación | Resultado |
| --- | --- |
| `/readyz` | `ready` |
| `/metrics` | 378 series Prometheus; desfase del proyector: último 1,2 ms, promedio 2,0 ms, máximo 6,1 ms; 0 pánicos, 0 reinicios de workers |
| `/metrics` sin autenticación | **200**: expone rutas, volumen y número de tiendas a cualquiera (H3) |
| `/v1/system/freshness` | 95 eventos aplicados, 0 errores, 4 tiendas cargadas |
| `/v1/audit/verify` | Cadena de auditoría íntegra |
| `kill -9` del proceso y relanzamiento | Listo de nuevo en **509 ms**. La sesión del dueño sigue válida y la página publicada se conserva. En este entorno el relanzamiento fue manual; en producción lo hace Docker o systemd (`docs/deploy.md`) |
| Consumidor de webhooks caído 25 s | Intento 1: 500. Intento 2 (+12 s): 500. Intento 3 (+21 s): 200. El operador ve `pending`, intentos, último código y próximo intento en `/v1/webhooks/{id}/deliveries` |
| Replay desde 0 | Reencola 450 eventos de inventario de golpe, sin vista previa ni confirmación (H9) |

## 6. Hallazgos

| # | Severidad | Hallazgo | Evidencia / ubicación | Arreglo propuesto |
| --- | --- | --- | --- | --- |
| H1 | **Alta** | No hay pantalla de inicio de sesión. Un dueño sin conocimientos técnicos que abre `/admin/` ve "Revisa tu clave de API en Acceso" y no tiene forma de entrar | `web/src/useEditor.ts:138`; el login solo existe como `POST /v1/auth/login` | Pantalla de login en el editor (tienda, correo, contraseña) que use la cookie; dejar "Clave de API" solo para desarrolladores |
| H2 | **Alta** | No hay interfaz para equipo, pedidos, catálogo, pagos ni webhooks: todo es API | — | Admin web por módulos, empezando por Equipo y Pedidos; respetar los permisos ya implementados |
| H3 | **Alta** (seguridad) | El límite de intentos de login confía en `X-Forwarded-For` sin validar: cambiando el encabezado se evita por completo. Además, `/metrics` es público | `internal/api/server.go:232` (`clientIP`), `server.go:267`; `/metrics` en `server.go:71` | Tomar la IP real solo de proxies de confianza (lista configurable, p. ej. el balanceador de GCP) y limitar también por correo sin IP. Proteger `/metrics` con red interna o token |
| H4 | Media | La plantilla inicial apunta a `/media/hero.avif`, que no existe (404), y no hay ruta para servir imágenes: una tienda nueva arranca con una imagen rota | `internal/seed/site.go:14` | Servir medios (subida a GCS/R2 + `imgproxy`) y usar una imagen incluida por defecto |
| H5 | Media | "Deshacer publicación" aparece también en la primera publicación y falla con "No hay una publicación anterior" | `web/src/App.tsx:85` (siempre `undo: true`) | Mostrar el botón solo si existe una publicación previa (`releases.length > 1`) |
| H6 | Media | Textos para el comprador técnicos o en inglés | `internal/promo/promo.go:80` ("unidades menores"), `promo.go:116` ("vez/veces"), `internal/orders/orders.go:50` (mensaje en inglés usado en `orders.go:243`) | Formatear montos con moneda ("$2,044.00"), pluralizar bien y traducir los mensajes de `problem` |
| H7 | Baja | Los inicios de sesión exitosos cuentan contra el límite: un usuario que entra varias veces puede quedar bloqueado | `internal/api/server.go:267` | Contar solo los intentos fallidos y limpiar el contador tras un éxito |
| H8 | Baja | Errores de validación de permisos en inglés y con nombre técnico | `internal/auth/auth.go:66`, `auth.go:69` | Mensajes en español que listen los módulos válidos |
| H9 | Baja | El replay reencola todo sin vista previa (450 eventos de una vez) | `internal/webhooks/webhooks.go:259` | Devolver primero el conteo (`dryRun=true`), permitir "desde fecha" y limitar la tasa por conector |
| H10 | Baja | El historial muestra el id interno del autor (`member:mem_…`) en lugar de su nombre | Historial del editor | Resolver el nombre del miembro al listar `releases` |
| H11 | Baja | La emisión de la factura no aparece en el historial del pedido | `internal/orders/orders.go:689` (no agrega `order_events`) | Registrar `invoice.issued` y `invoice.failed` como eventos del pedido y del outbox |
| H12 | Muy baja | El `iframe` de vista previa no muestra indicador de foco | Recorrido con teclado | Estilo `:focus-visible` para el marco |

## 7. VTEX frente a Apexx

| Métrica | VTEX (`elpaps`) | Apexx (`ux-test`) | Fuente VTEX |
| --- | ---: | ---: | --- |
| LCP home, móvil Slow 4G | 3,5 s | 0,22 s | `frontend.md` |
| CLS home, móvil | 0,40 | 0 | `frontend.md` |
| TBT home, móvil | 844 ms | 0 ms | `frontend.md` |
| JS de la home | 556 KB en 33 archivos | 0 KB | `frontend.md` |
| Peticiones de la home | 88 | 2 | `frontend.md` |
| HTML de la home | 1,19 MB | 1,6 KB | `frontend.md` |
| Mover un módulo y publicar | 1 a 3 h de un desarrollador | ~5 s, sin desarrollador | `builder.md` |
| Deshacer una publicación | Volver a desplegar el tema | 1 clic (solo mueve un puntero en el servidor) | `builder.md` |
| Simulación de carrito | 174–535 ms | 10,7 ms (p50 del checkout completo: 9,3 ms) | `backend.md` |
| Compra completa | 8 viajes de red; 7 a 7,7 s por paso en móvil | 1 llamada; p95 14,7 ms (sandbox) | `checkout-payments.md` |
| Cambio de stock/precio visible en tienda | Hasta 5 min de caché; stock 100 contra 14 reales | Desfase del proyector: promedio 2 ms | `backend.md` |
| Reintento seguro de una compra | No hay idempotencia | `Idempotency-Key`; el doble clic no cobra dos veces | `checkout-payments.md` |
| Errores | 5 formatos distintos | Un formato (RFC 9457), aunque con textos a pulir (H6) | `backend.md` |
| Accesibilidad del editor | No medida | 0 violaciones WCAG 2.1 AA | — |

**Conclusión:** el núcleo cumple lo que promete en velocidad, permisos, idempotencia y recuperación. Lo que separa a Apexx de un producto que una persona no técnica pueda usar sola es la capa de interfaz: falta la pantalla de inicio de sesión (H1) y faltan las pantallas de administración (H2). Antes de exponerlo a Internet hay que corregir el límite de intentos y proteger `/metrics` (H3).
