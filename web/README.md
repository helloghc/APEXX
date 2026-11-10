# Apexx Admin

Panel de la plataforma para comerciantes y sus equipos: login, inicio con estado de la tienda,
diseño de la tienda (builder visual), catálogo (productos, precios por volumen, existencias),
pedidos (detalle, envío, cancelación con reembolso), equipo (puestos ilimitados con permisos
por módulo), pagos (Stripe Connect o llaves propias) y conectores (webhooks, entregas,
reenvío y eventos).

El binario de Go lo sirve en **`/admin/`** (`internal/webui`, embebido con `go:embed`).

## Stack

- React 19 + TypeScript estricto + Vite 6
- **shadcn/ui** (escritos a mano en `src/components/ui`, estructura de shadcn: `cva` + `tailwind-merge` + `clsx`,
  helper `cn` en `src/lib/utils.ts`) sobre primitivas de **Radix**, iconos **lucide-react**, avisos **sonner**
- **Tailwind CSS v4** (`@tailwindcss/vite`); las variables de shadcn se mapean a los tokens de `src/tokens.css`
  en `src/index.css` (modo claro y oscuro con `prefers-color-scheme`)
- TanStack Query, dnd-kit (arrastrar y soltar del builder, también con teclado), Zod (errores de la API)
- Versiones exactas en `package.json`; `package-lock.json` versionado

El registro de shadcn (`ui.shadcn.com`) no es accesible desde el entorno de construcción, por eso los
componentes se escribieron a mano siguiendo su código fuente. Componentes: Button, Input, Textarea,
Label, Select, Table, Dialog, AlertDialog, Sheet, Tabs, Badge, Card, DropdownMenu, Checkbox, Separator,
Sonner (Toaster), Sidebar y FormField (`form.tsx`, al estilo de `FormItem/FormLabel/FormMessage` sin react-hook-form).

## Sesión y permisos

- `POST /v1/auth/login {store,email,password}` deja una cookie `HttpOnly` (`apexx_session`); todas las
  llamadas usan `credentials: "same-origin"`. Un 401 en cualquier pantalla vuelve al login.
- "Entrar con una API key" es una opción avanzada (integraciones y pruebas); la llave vive solo en `sessionStorage`.
- `GET /v1/me` trae los permisos por módulo (`none < read < edit < admin`). El menú y las acciones se ocultan
  o desactivan según esos permisos (`src/perms.ts`); un 403 muestra un mensaje claro en español.

## Rutas (hash)

`#inicio`, `#builder[/sitio[/página]]`, `#catalogo[/id|nuevo]`, `#pedidos[/id]`, `#equipo`, `#pagos`, `#conectores`.
Cada sección se carga bajo demanda (`React.lazy`).

## Desarrollo

```bash
npm ci
npm run dev        # Vite en :5173; /v1 se envía al Go server en APEXX_API (por defecto :8080)
npm run typecheck
npm test           # Vitest: dinero, permisos, router, formularios, parches y esquemas
npm run build      # tsc + vite build + copia a internal/webui/dist (lo que embebe Go)
```

En desarrollo con Vite, la cookie de sesión se emite para el host del Go server; usa el proxy de Vite
(mismo origen) o la opción de API key.

## E2E (Playwright, servidor real)

```bash
npm run build && npm run e2e
```

`e2e/start-server.sh` compila `./cmd/apexx` (con el admin recién construido), aplica migraciones, crea una
tienda nueva con dueño, carga 50 productos y sirve en `127.0.0.1:8094` con pagos en sandbox. Lee la
configuración de base de datos de `APEXX_E2E_ENV` (archivo con `export VAR=...`: `ADMIN_DATABASE_URL`,
`APP_DB_PASSWORD`, `SYSTEM_DB_PASSWORD`, `DATABASE_URL`, `SYSTEM_DATABASE_URL`, `APEXX_MASTER_KEY`).

Flujo: contraseña incorrecta → login del dueño → cambiar un precio (y verificarlo en la simulación de carrito)
→ invitar a un miembro "Editor de catálogo" → ese miembro no ve Pedidos y la API le responde 403 → un comprador
paga con la llave de storefront → el dueño cancela y reembolsa → builder: primera publicación sin
"Deshacer publicación", mover una sección con teclado, publicar y deshacer.

## Tamaño (gzip)

| | Antes (solo editor) | Ahora |
| --- | ---: | ---: |
| Carga inicial (login, shell, inicio) | 127 KB JS | 128 KB JS + 6 KB CSS |
| Builder (bajo demanda) | — | +26 KB |
| Catálogo / Pedidos / Equipo / Pagos / Conectores | — | +2 a 5 KB cada uno (+17 KB Select compartido) |
