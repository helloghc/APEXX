# Editor del builder de Apexx

Aplicación React para editar las páginas de una tienda: reordenar secciones arrastrando, editar sus ajustes con formularios generados desde el esquema de cada sección, ver la página real en vivo y publicar o deshacer en un clic.

## Stack

Vite 6 · React 19 · TypeScript estricto · TanStack Query · dnd-kit · Zod. CSS modules con tokens de diseño (`src/tokens.css`), tema claro y oscuro.

## Comandos

```bash
npm ci
npm run dev        # http://localhost:5173, proxy de /v1 a APEXX_API (por defecto http://127.0.0.1:8080)
npm test           # pruebas unitarias (Vitest)
npm run build      # compila a web/dist y copia a internal/webui/dist para embeberlo en el binario Go
npm run e2e        # Playwright: levanta el servidor Go en :8098 y el editor en :4174
```

Si el backend exige clave de API, exporta `APEXX_API_KEY` antes de `npm run e2e`; sin ella la prueba se omite con un aviso.

## Cómo funciona

- Cada cambio es un parche JSON (RFC 6902) enviado con `ifVersion`. La interfaz lo aplica al instante y, si el servidor lo rechaza, vuelve al último estado guardado.
- Los parches se envían en cola, nunca en paralelo, así que las versiones no compiten.
- Deshacer y rehacer usan el parche inverso de cada cambio (`src/patch.ts`); las pruebas verifican que aplicar un cambio y su inverso deja la plantilla idéntica.
- Un conflicto (409) muestra un aviso para recargar. Los errores usan `application/problem+json`.
- La vista previa es un iframe sin scripts (`sandbox="allow-same-origin"`) y conserva la posición de scroll entre actualizaciones.
- La barra superior muestra el estado de la página (HTML comprimido y JS frente al presupuesto, avisos y bloqueos). Publicar se desactiva mientras haya bloqueos y lista las razones.
- La clave de API se guarda solo en `sessionStorage` de la pestaña.

## Accesibilidad

Reordenar con teclado (enfoca el asa, Espacio, flechas, Espacio), anuncios para lectores de pantalla, etiquetas en todos los campos, errores asociados con `aria-describedby`, foco visible y respeto a `prefers-reduced-motion`.

## Integración en Go

`internal/webui` embebe `internal/webui/dist`:

```go
mux.Handle("/admin/", http.StripPrefix("/admin", webui.Handler()))
```

Abre `/admin/?site=demo&page=index`.
