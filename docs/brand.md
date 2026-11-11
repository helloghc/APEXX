# Marca Apexx y temas de tienda

Este documento define la identidad de **Apexx** (la plataforma) y el sistema de temas para las tiendas de nuestros clientes. Todo es original: no copia nombres, código, maquetación ni recursos de temas de otras plataformas.

## 1. Concepto

**Apexx viene de "ápice": la cumbre.** La marca transmite tres cosas:

- **Velocidad:** las tiendas cargan en menos de un segundo y los cambios se ven al instante.
- **Precisión:** cada número y cada estado es exacto y verificable.
- **Confianza:** nada se publica roto; todo se puede deshacer.

## 2. Logotipo

El símbolo es **una cumbre**: un pico grande, un pico interior más suave (la doble "x" del nombre) y un punto naranja en la cima, la **señal** de que algo está vivo y al día.

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
  <rect width="32" height="32" rx="8" fill="#2E44E8"/>
  <path d="M8.5 23 16 8.5 23.5 23" fill="none" stroke="#fff" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round"/>
  <path d="M12.6 23 16 16.4 19.4 23" fill="none" stroke="#fff" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" opacity=".55"/>
  <circle cx="16" cy="8.5" r="2.6" fill="#FF5B2E"/>
</svg>
```

- **Logotipo completo:** símbolo + "apexx" en minúsculas, Bricolage Grotesque Bold, tracking ligeramente cerrado. Componente: `web/src/components/Logo.tsx`.
- **Favicon:** `web/public/favicon.svg` (admin) y `/favicon.ico` servido por Go (tiendas).
- **Área de respeto:** la mitad de la altura del símbolo alrededor de todo el logotipo.
- **Tamaño mínimo:** 20 px de símbolo en pantalla.
- **No:** deformarlo, cambiar el naranja de la señal, ponerlo sobre fotografías sin una placa, rotarlo o añadirle sombras.

## 3. Paleta

| Token | Claro | Oscuro | Uso |
| --- | --- | --- | --- |
| Cumbre (primario) | `#2E44E8` | `#8C9BFF` | Botones principales, enlaces, foco |
| Señal (acento) | `#FF5B2E` | `#FF5B2E` | El punto del logo, novedades, indicadores en vivo. Nunca como color de texto sobre blanco |
| Tinta | `#0E1220` | `#EDEFF6` | Texto principal |
| Texto secundario | `#565E74` | `#9AA2BC` | Descripciones, ayudas |
| Fondo | `#F5F6F9` | `#0B0E18` | Fondo de la aplicación |
| Superficie | `#FFFFFF` | `#131726` | Tarjetas, paneles |
| Superficie 2 | `#ECEEF4` | `#1B2034` | Zonas secundarias |
| Línea | `#D9DDE7` | `#2A3049` | Bordes y divisores |
| Éxito | `#157F4A` | `#6FD39A` | Estados correctos |
| Aviso | `#9A5B00` | `#F2B252` | Advertencias |
| Error | `#C2281F` | `#FF8D84` | Errores y bloqueos |

### Contraste verificado (WCAG 2.1, mínimo 4.5:1 para texto)

| Combinación | Contraste |
| --- | ---: |
| Tinta sobre fondo | 17.3:1 |
| Tinta sobre superficie | 18.6:1 |
| Texto secundario sobre fondo | 6.0:1 |
| Blanco sobre Cumbre (botón) | 6.8:1 |
| Tinta sobre Señal | 6.0:1 |
| Éxito / Aviso / Error sobre superficie | 5.0 / 5.4 / 5.8:1 |
| Oscuro: texto sobre fondo | 16.8:1 |
| Oscuro: secundario sobre fondo | 7.6:1 |
| Oscuro: texto de botón sobre Cumbre claro | 7.6:1 |

Tokens en código: `web/src/tokens.css` (variables) y `web/src/index.css` (mapeo a shadcn/ui).

## 4. Tipografía

Todas las fuentes son de licencia abierta (SIL OFL 1.1), se sirven desde Apexx (sin Google Fonts) y se cargan solo en su subconjunto latino.

| Rol | Fuente | Por qué |
| --- | --- | --- |
| Títulos | **Bricolage Grotesque** (variable) | Grotesca con carácter y ligeras irregularidades: humana, no genérica |
| Texto e interfaz | **Figtree** (variable) | Muy legible en tamaños pequeños, amable sin ser blanda |
| Datos y código | **JetBrains Mono** (variable) | Cifras tabulares claras para precios, ids y métricas |

Escala de la interfaz: 12 · 14 (base) · 16 · 20 · 24 · 30 px. Los títulos usan `letter-spacing: -0.015em`.

## 5. Espaciado, formas y sombras

- **Espaciado:** múltiplos de 4 px (4, 8, 12, 16, 24, 32, 48).
- **Radio:** 10 px en tarjetas y campos; 6 px en botones pequeños; 999 px en etiquetas.
- **Sombras:** `--shadow-1` (sutil, tarjetas) y `--shadow-2` (diálogos y login). Nada de sombras de colores.
- **Iconos:** [lucide](https://lucide.dev), trazo 2 px, siempre con texto visible o `aria-label`.

## 6. Voz y tono

Escribimos como habla una persona que sabe y quiere ayudar: **claro, directo y en español de México**.

| Hacemos | Evitamos |
| --- | --- |
| "Publicado en 2 ms." | "¡Operación completada exitosamente!" |
| "No se publicó: falta el título para buscadores." | "Error 422: validation failed" |
| "Tu acceso a pedidos no permite cancelar. Pide nivel Editor." | "Permisos insuficientes" |
| "Guardamos tu versión anterior; puedes volver a ella." | "Esta acción es irreversible" |
| "Cambiar estilo", "Ver tienda", "Deshacer publicación" | "Gestionar configuración de tema", "Rollback" |

- Los botones dicen exactamente lo que pasa.
- Los errores explican qué falló y cómo arreglarlo, sin disculpas.
- Sin tecnicismos para el comerciante: "tienda", "página", "sección", "pedido".

## 7. Temas de tienda

Cada tienda elige un tema y lo ajusta. Un tema no es solo una paleta: define cómo se ve cada sección (héroe, tarjetas de producto, colecciones, encabezado, pie, botones) y el arte que aparece mientras el comerciante no sube fotos.

| Tema | Giro | Títulos + texto | Personalidad |
| --- | --- | --- | --- |
| **Lienzo** | Moda y accesorios | Cormorant + Jost | Editorial y silencioso: serifa ligera con cursivas, logotipo espaciado, botones con contorno, mucho aire, arcos en el arte |
| **Voltio** | Tecnología y electrónica | Sora + JetBrains Mono | Oscuro y preciso: grotesca geométrica, precios y etiquetas en monoespaciada, azul eléctrico, tarjetas con borde y brillo |
| **Mercadito** | Súper, abarrotes y frescos | Fredoka + Nunito | Cercano y rápido: letras redondas, encabezado verde, pasillos en píldoras, grillas densas con botón "Agregar" en cada producto |
| **Bálsamo** | Belleza y bienestar | Instrument Serif + Manrope | Suave y refinado: serifa fina, tonos empolvados, arcos y frascos en el arte, botones en píldora |

Paletas completas (todas cumplen 4.5:1 en sus pares de texto; lo verifica `TestThemesMeetContrast`):

| Token | Lienzo | Voltio | Mercadito | Bálsamo |
| --- | --- | --- | --- | --- |
| Fondo | `#F6F4F0` | `#0B0D12` | `#FFFFFF` | `#FBF5F2` |
| Tarjetas | `#FFFFFF` | `#141821` | `#F2F7EF` | `#FFFFFF` |
| Texto | `#191817` | `#F1F3F8` | `#1B271C` | `#2D2729` |
| Secundario | `#625C55` | `#9AA3B6` | `#566557` | `#6D6265` |
| Botón / texto | `#191817` / `#FFFFFF` | `#5B8CFF` / `#05070C` | `#1C7536` / `#FFFFFF` | `#74495A` / `#FFFFFF` |
| Acento / texto | `#8A5536` / `#FFFFFF` | `#2DD4EE` / `#05070C` | `#F5A524` / `#2A1A00` | `#C9D3C2` / `#26302A` |
| Oferta | `#9E3426` | `#FF6B81` | `#C62E1C` | `#9C3448` |

### Cómo funciona

- **Modelo** (`internal/builder/theme.go`): `Theme` define colores, fuentes, radio, estilo de botón, tarjeta, encabezado, densidad y proporción de imagen. Cada sitio guarda `ThemeSettings` (tema base + cambios del comerciante) en la plantilla de inicio.
- **Validación:** colores en `#RRGGBB`, fuentes de la biblioteca incluida y radio de 0 a 32 px. Si un par de texto baja de 4.5:1, la publicación **se bloquea** con un mensaje que dice qué colores corregir.
- **CSS:** `/s/assets/store.css` (base común) + `/s/assets/theme-<id>.css` (personalidad, 3–4 KB cada uno) + variables en línea por sitio.
- **Fuentes:** `/s/fonts/<archivo>.woff2`, caché de un año, `font-display: swap`; se precarga solo la fuente de títulos. Máximo 2 archivos por tema en la primera carga.
- **Páginas del comprador:** catálogo, producto, carrito, pago y confirmación usan el encabezado, pie y tema de la versión publicada, así toda la tienda se ve coherente.
- **Plantillas iniciales** (`internal/builder/starters.go`): 7 a 8 secciones por giro con textos reales, más barra de anuncios, encabezado, boletín y pie.
- **Secciones disponibles:** barra de anuncios, encabezado, banner principal (completo, dividido o solo texto), grilla y carrusel de productos (filtro por marca o categoría), colecciones, imagen con texto, texto, beneficios, testimonios, preguntas frecuentes, marcas, boletín y pie de página.
- **Editor:** botón **Estilo** en el builder: elegir tema (solo estilo o plantilla completa, con "volver a como estaba"), ajustar colores con avisos de contraste en vivo, fuentes y redondeo.

### Medición

| Medición (móvil, Slow 4G, CPU 4×, mediana de 3) | Lienzo | Mercadito |
| --- | ---: | ---: |
| LCP | 0.69 s | 0.68 s |
| CLS | 0 | 0 |
| Transferido (HTML, CSS, fuentes, JS) | 80 KB | 87 KB |
| Peticiones | 6 | 6 |

### Pendiente

- El boletín muestra confirmación pero todavía no envía a una herramienta de correo (llega con los conectores).
- Carga de imágenes propias (hoy se usa el arte del tema).
- Editar encabezado y pie desde la lista de secciones del editor (hoy se editan por API o cambiando de plantilla).
