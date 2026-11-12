# Auditoría final de las etapas 0 a 9 (12 de noviembre de 2026)

## Resultado

Las diez etapas del plan están terminadas. Cada una tiene su documento en `docs/fases/` con funciones, pruebas, decisiones, defectos corregidos y velocidad.

## Pruebas

- Suite completa de Go (`go test ./...`) contra Postgres 16 con RLS forzado: verde en cada commit (se sube solo lo que compila y pasa).
- Suite del panel (vitest): 48 pruebas en verde.
- Auditorías incluidas en la suite:
  - Acceso: cada ruta `/v1` exige credenciales y una llave pública de tienda no puede administrar.
  - Aislamiento: cada tabla con `tenant_id` tiene RLS forzado.
  - Idiomas: frases de la tienda, del guion y del panel completas, con sus cifras y partes variables.
- Pruebas nuevas de las etapas 8, 9 y 0:
  - `TestPunchout`
  - `TestRecommendationsAndLearningSearch`
  - `TestMCPServer`
  - `TestBuyerEmailsInTheirLanguage`
  - `TestPhrasesWithVariableParts`
  - `TestStorefrontScriptHasEveryPhrase`

## Velocidad (servidor de desarrollo, 12 mediciones por ruta)

- 183 rutas (todo el panel y las páginas principales de las cinco tiendas de QA):
  - Mediana de medianas: **1.8 ms**.
  - Peor p95: **13 ms**, en `/v1/audit/verify`, que recalcula la cadena de hashes de la bitácora completa.
  - Ningún error 5xx.
- Herramientas MCP: de 1.2 a 2.8 ms.
- Traducir un mensaje con partes variables: unos 8 µs.

## Recorridos en navegador

- Panel en escritorio y en teléfono:
  - Punchout.
  - Recomendaciones e IA.
  - Correos con vista previa por idioma.
  - Diseñador por pestañas en iPhone.
- Panel en japonés e indonesio, 160 rutas: lo único en español son datos de la tienda (nombres de productos, promociones, apps).
- Tienda en los cuatro temas, en teléfono y escritorio (inicio, catálogo, producto, carrito y pago):
  - Sin desbordes horizontales.
  - Sin imágenes rotas.
  - Sin errores en la consola.
  - Contraste y tamaño de toque revisados.
- Asistente de IA por MCP: busca, cotiza con envío y crea un enlace de carrito. El cliente abre el enlace en el teléfono y paga en el pago de siempre.

## Lo que necesita cuentas o personas fuera de este entorno

- Pentest externo y prueba de carga en Google Cloud.
- Stripe, paqueterías y facturación CFDI en vivo.
- Cuentas reales de Mercado Libre y Amazon.
- Ariba, Coupa o Jaggaer reales para certificar el punchout.
- Asistentes de IA comerciales conectados a una tienda publicada.
- Revisión de las traducciones por hablantes nativos.
- Indexación activa en VTEX (requiere autorización).
- Rotación del AppToken de VTEX.
