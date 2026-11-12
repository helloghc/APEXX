# Etapa 6 — Extensibilidad (noviembre 2026)

Estado: **hecha**. Cada función con prueba de integración, recorrido en navegador y medición.

| Función | Dónde | Prueba |
|---|---|---|
| Apps de terceros con permisos por módulo, revocables | Configuración › Apps | `TestAppsScopesAndRevocation` |
| Automatizaciones «cuando pase X, haz Y» con historial y reintentos | Configuración › Automatizaciones | `TestAutomations` |
| Reglas de pago y envío, con app de la tienda (tope 600 ms) | Configuración › Reglas de pago y envío | `TestCheckoutRules` |
| Gestor de scripts con consentimiento | Configuración › Scripts y cookies; aviso en la tienda | `TestStoreScriptsAndConsent` |
| Operaciones masivas y exportación JSONL | Configuración › Cambios masivos | `TestBulkOperations` |
| API GraphQL de la tienda | `POST /s/{tienda}/api/graphql` | `TestStorefrontGraphQL`, `TestStorefrontSchemaParses` |
| Inicio de sesión con la empresa (OIDC) | Configuración › Inicio de sesión de la empresa; acceso | `TestSingleSignOn` |
| Bloques del pago declarativos | Configuración › Bloques del pago; pago; pedido | `TestCheckoutBlocks` |
| Vistas de apps en el panel (página y bloque de pedido) | Sección Apps; detalle del pedido | `TestAppExtensions` |
| Sección de tienda para contenido de apps | Diseñador › Contenido de una app | `TestAppEmbedSection` |
| SDK y tienda de ejemplo sin cabeza | `sdk/js` | `sdk/js/test`, `TestHeadlessCORS` |

## Decisiones de seguridad

- Una app nunca administra el equipo ni instala apps; cambiar o revocar sus permisos aplica en la siguiente solicitud (sin caché) y queda en el registro encadenado.
- Ningún código de terceros corre en el pago, aprobación, cotización ni cuenta: los bloques del pago son declarativos y la política de seguridad de esas páginas no admite scripts ni marcos ajenos.
- Scripts de analítica y publicidad solo con permiso del visitante; Global Privacy Control se respeta; la política de seguridad permite exactamente los orígenes declarados.
- Las vistas de apps se cargan en marcos aislados con sesión firmada de 5 minutos; el contenido de apps en la tienda también va aislado y solo por https.
- La app de reglas y el aviso de automatizaciones reciben el carrito o el pedido sin datos personales, firmados con secreto por tienda.
- SSO: PKCE, estado y nonce firmados, correo confirmado, dominios permitidos y solo miembros ya invitados; los dueños conservan su contraseña de respaldo.
- CORS solo en el API público de la tienda y sin credenciales.

## Defectos que encontraron las auditorías (todos corregidos)

- La llave pública de la tienda podía leer operaciones masivas, la exportación y `/v1/apps/me` (auditoría de rutas).
- Las respuestas en caché de la app de reglas eran globales: una prueba contaminaba a otra; ahora la caché es de cada servidor.
- El proveedor de SSO se creaba con un contexto que se cancelaba y la verificación fallaba después.
- El avance de una operación masiva no contaba las líneas repetidas combinadas.
- Dos migraciones a la vez chocaban al actualizar los roles compartidos del servidor; ahora reintentan.

## Velocidad medida (servidor de desarrollo)

- Panel: apps, automatizaciones, reglas, bloques, scripts, SSO, operaciones y vistas de apps entre 1.6 y 2.5 ms (mediana; peor p95 3.8 ms).
- Tienda: GraphQL producto 1.9 ms, 50 productos 1.6 ms, carrito 1.7 ms; carrito con reglas 1.3 ms; tienda sin cabeza en otro dominio lista en 79 ms.
- Una app de reglas lenta corta a los 600 ms sin detener el carrito (probado).
