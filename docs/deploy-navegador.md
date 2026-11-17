# Montar Apexx en Google Cloud desde el navegador

Esta guía lo hace todo con clics en la consola web de Google Cloud (console.cloud.google.com), sin Cloud Shell ni terminal en tu computadora. Hay un solo momento en que pegas texto en una ventana negra: crear tu superadmin y tu primera tienda (paso 11). Esa ventana la abre el botón **SSH** de la consola en otra pestaña de Chrome.

Si prefieres los scripts, la guía equivalente con comandos está en `docs/deploy.md`.

## Antes de empezar

- Una cuenta de Google con facturación activa en Google Cloud.
- Un dominio para Apexx (por ejemplo `apexx.tudominio.mx`) y acceso a su DNS.
- Ser administrador del repositorio `helloghc/apexx` en GitHub, para conectar Cloud Build.
- Costo aproximado: 150 a 200 USD al mes (VM `e2-standard-4` + disco de 100 GB + respaldos).

En los pasos uso la región `us-central1` (Iowa). Si prefieres México, usa `northamerica-south1` (Querétaro) en **todos** los pasos donde aparece una región.

Los nombres de los menús pueden variar un poco según el idioma de tu consola; arriba hay un buscador que encuentra cualquier servicio por nombre.

## 1. Un proyecto solo para Apexx

1. Arriba a la izquierda, abre el selector de proyecto › **Proyecto nuevo**.
2. Nombre: `apexx-produccion`. Anota el **ID del proyecto**: aparece debajo del nombre y puede llevar números.
3. Confirma que tenga una cuenta de facturación vinculada (**Facturación** en el menú).

Un proyecto propio importa: los permisos de la VM aplican a todo el proyecto, y así solo alcanzan los secretos de Apexx.

## 2. Activar los servicios

En el buscador de arriba, abre cada uno y pulsa **Habilitar**:

- Compute Engine API
- Secret Manager API
- Artifact Registry API
- Cloud Build API

## 3. Registro de imágenes

**Artifact Registry › Repositorios › Crear repositorio**:

| Campo | Valor |
|---|---|
| Nombre | `apexx` |
| Formato | Docker |
| Modo | Estándar |
| Tipo de ubicación | Región, `us-central1` |

Crear.

## 4. Bucket de respaldos

**Cloud Storage › Buckets › Crear**:

| Campo | Valor |
|---|---|
| Nombre | `ID-DEL-PROYECTO-apexx-backups` (debe ser único en el mundo) |
| Ubicación | Región, `us-central1` |
| Clase | Standard |
| Acceso | Marca **Aplicar la prevención del acceso público** y deja el control **Uniforme** |
| Protección | Activa **Control de versiones de objetos** |

Crear. Luego, en el bucket, pestaña **Ciclo de vida › Agregar una regla**:
- Acción: **Borrar objeto**.
- Condición: **Antigüedad: 30 días**.

## 5. Cuenta de servicio de la VM

**IAM y administración › Cuentas de servicio › Crear cuenta de servicio**:

1. Nombre: `apexx-vm`.
2. Roles, uno por uno con **Agregar otro rol**:
   - Escritor de registros (Logs Writer)
   - Escritor de métricas de Monitoring (Monitoring Metric Writer)
   - Lector de Artifact Registry
   - Descriptor de acceso a secretos de Secret Manager (Secret Accessor)
   - Agregador de versiones de secretos de Secret Manager (Secret Version Adder). Con este rol, la VM guarda los valores al azar del paso 6.
3. Listo.

Ahora dale acceso **solo** al bucket de respaldos:

1. Abre el bucket del paso 4 › pestaña **Permisos › Otorgar acceso**.
2. Principal: `apexx-vm@ID-DEL-PROYECTO.iam.gserviceaccount.com`.
3. Rol: **Administrador de objetos de almacenamiento** (Storage Object Admin).

## 6. Secretos

**Secret Manager › Crear secreto**. Crea estos cuatro, con el valor exacto `generar`:

- `POSTGRES_PASSWORD`
- `APP_DB_PASSWORD`
- `SYSTEM_DB_PASSWORD`
- `APEXX_MASTER_KEY`

En el primer arranque, la VM cambia `generar` por un valor al azar y lo guarda como versión nueva. Nadie lo escribe ni lo ve. En el paso 10 respaldas la llave maestra.

Opcionales:

| Secreto | Valor | Para qué |
|---|---|---|
| `APEXX_ANTHROPIC_API_KEY` | Tu llave de Anthropic | IA con Claude. Sin ella, la IA funciona en modo determinista |
| `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET` | Las de tu plataforma en Stripe | Solo si las tiendas cobran con Stripe Connect. Con sus propias llaves no hacen falta |

Pega las llaves solo en el formulario de Google, nunca en un chat ni en un correo.

## 7. Construir la imagen

1. **Cloud Build › Activadores › Conectar repositorio**.
   - Región: `us-central1`.
   - Proveedor: GitHub (Cloud Build GitHub App).
   - Autoriza e instala la app en el repositorio `helloghc/apexx`.
2. **Crear activador**:

   | Campo | Valor |
   |---|---|
   | Nombre | `apexx-imagen` |
   | Región | `us-central1` |
   | Evento | Invocación manual |
   | Repositorio | `helloghc/apexx` |
   | Rama | `claude/hola-c650ea` |
   | Configuración | **Dockerfile**, directorio `/`, archivo `Dockerfile` |
   | Nombre de la imagen | `us-central1-docker.pkg.dev/ID-DEL-PROYECTO/apexx/apexx:latest` |
   | Cuenta de servicio | La predeterminada de Compute Engine |

3. Guarda y pulsa **Ejecutar**. Tarda de 5 a 10 minutos. Al terminar, en **Artifact Registry › apexx** debe aparecer la imagen `apexx` con la etiqueta `latest`.

Si la compilación falla por permisos:
1. Ve a **IAM**.
2. Busca la cuenta `NÚMERO-compute@developer.gserviceaccount.com`.
3. Dale el rol **Cuenta de servicio de Cloud Build** (Cloud Build Service Account).
4. Vuelve a ejecutar el activador.

## 8. Crear la VM

**Compute Engine › Instancias de VM › Crear instancia**:

**Configuración de la máquina**
- Nombre: `apexx`.
- Región `us-central1`, zona `us-central1-a`.
- Serie **E2**, tipo `e2-standard-4` (4 vCPU, 16 GB).

**SO y almacenamiento › Cambiar**
- Sistema operativo **Ubuntu**, versión **Ubuntu 24.04 LTS** (x86/64, amd64).
- Disco **Persistente balanceado**, **100 GB**.

**Protección de datos** (si aparece)
- Programa de instantáneas diario, con retención de 14 días.

**Herramientas de redes**
- Marca **Permitir tráfico HTTP** y **Permitir tráfico HTTPS**.
- En la interfaz de red › **Dirección IPv4 externa** › **Reservar dirección IP externa estática**, con el nombre `apexx-ip`. Anota la IP.

**Seguridad**
- VM protegida: marca **Arranque seguro**, **vTPM** y **Supervisión de integridad**.
- Identidad y acceso a la API › Cuenta de servicio: `apexx-vm`.
- Permisos de acceso: **Permitir el acceso completo a todas las APIs de Cloud**. Lo que de verdad limita a la VM son los roles del paso 5.

**Avanzado › Metadatos › Agregar elemento.** Uno por fila:

| Clave | Valor |
|---|---|
| `apexx-domain` | `apexx.tudominio.mx` |
| `apexx-acme-email` | tu correo (para el certificado HTTPS) |
| `apexx-image` | `us-central1-docker.pkg.dev/ID-DEL-PROYECTO/apexx/apexx:latest` |
| `apexx-backup-bucket` | `ID-DEL-PROYECTO-apexx-backups` |
| `apexx-payments` | `stripe`, o `sandbox` para una prueba privada que **nunca cobra**. Cámbialo a `stripe` antes de recibir compradores reales |
| `enable-oslogin` | `TRUE` |

**Avanzado › Automatización › Script de inicio**
1. En GitHub abre `deploy/gcp/startup-navegador.sh` en la rama `claude/hola-c650ea`.
2. Pulsa **Raw**, selecciona todo (Ctrl+A), copia (Ctrl+C) y pégalo en el campo.

Debe empezar con `#!/usr/bin/env bash` y terminar con `systemctl restart apexx.service`.

**Crear.**

## 9. Apuntar tu dominio

1. En tu proveedor de dominio crea un registro **A**: nombre `apexx` (o el que elegiste) → la IP del paso 8.
2. Espera unos 5 minutos. En el primer arranque, la VM:
   - instala Docker;
   - genera los secretos;
   - descarga la imagen;
   - levanta la base de datos y Apexx.
3. Abre `https://apexx.tudominio.mx/readyz` en Chrome. Debe decir **ready**. El candado HTTPS se obtiene solo en la primera visita.

Si no responde:
1. Ve a **Compute Engine › apexx › Registros** (o **Logging**).
2. Filtra por `apexx-startup`.

Ahí se ve en qué paso va o qué falló.

## 10. Respaldar la llave maestra (no te lo saltes)

1. **Secret Manager › `APEXX_MASTER_KEY`** › la versión más reciente (la 2).
2. Menú **⋮ › Ver el valor del secreto**.
3. Cópiala a tu gestor de contraseñas y a un lugar fuera de línea.

Sin esta llave, los datos cifrados de las tiendas no se pueden leer nunca.

## 11. Tu superadmin y tu primera tienda

En **Compute Engine › Instancias de VM**, pulsa **SSH** en la fila de `apexx`. Se abre una ventana en Chrome. Pega, cambiando lo que está en mayúsculas:

```
cd /opt/apexx
sudo docker compose run --rm apexx operator create --email TU@CORREO.mx --name "Tu nombre" --password 'UNA-CONTRASEÑA-DE-12-O-MÁS'
```

Imprime un secreto de doble verificación **una sola vez**: agrégalo a Google Authenticator o a tu app de códigos.

```
sudo docker compose run --rm apexx tenant -id mitienda -name "Mi Tienda" -owner-email dueno@mitienda.mx -owner-password 'OTRA-CONTRASEÑA-DE-12-O-MÁS'
```

Imprime la llave admin de la tienda **una sola vez**: guárdala en tu gestor de contraseñas.

Para probar que el respaldo funciona:

```
sudo systemctl start apexx-backup.service
```

En el bucket del paso 4 debe aparecer `pg/apexx-….dump`. Desde ahí se respalda solo todos los días a las 03:15 UTC.

Cierra la ventana. Ya puedes entrar:

| Qué | Dirección |
|---|---|
| Panel de la tienda | `https://apexx.tudominio.mx/admin/` |
| Superadmin | `https://apexx.tudominio.mx/admin/#superadmin` |
| Tienda pública, después de publicar el sitio | `https://apexx.tudominio.mx/s/mitienda/main/index` |

## 12. Alerta si la tienda se cae

**Monitoring › Verificaciones de tiempo de actividad › Crear**:

| Campo | Valor |
|---|---|
| Protocolo | HTTPS |
| Host | `apexx.tudominio.mx` |
| Ruta | `/readyz` |
| Frecuencia | 1 minuto |
| Alerta | Crea un canal de notificación con tu correo |

## Después

- **Actualizar Apexx:**
  1. En **Cloud Build › Activadores**, ejecuta `apexx-imagen`.
  2. Al terminar, en **Compute Engine** abre `apexx` y pulsa **Detener**; cuando diga detenida, **Iniciar**.

  Al arrancar baja la imagen nueva y corre las migraciones. El corte dura de uno a dos minutos.

  No uses **Restablecer**: apaga de golpe, sin cerrar la base de datos en orden.
- **Activar la IA más tarde:**
  1. Crea el secreto `APEXX_ANTHROPIC_API_KEY`.
  2. **Detén** e **inicia** la VM.
- **Cambiar de `sandbox` a `stripe`:**
  1. En la VM › **Editar › Metadatos**, cambia `apexx-payments`.
  2. Guarda, **detén** e **inicia** la VM.
- **Stripe por tienda, restaurar respaldos, alta disponibilidad y runbook de incidentes:** `docs/deploy.md`.
