# Google Workspace en una imagen transportable

La imagen de DByson incluye la aplicación completa. No contiene dominios,
credenciales OAuth, claves maestras ni certificados de una empresa. Cada
instalación configura esos datos al arrancar.

## Desplegar sin compilar

Copia únicamente `docker-compose.deploy.yml` y `deploy.env.example` al servidor.
Renombra el segundo a `.env`, por ejemplo `/opt/dbyson/.env`. El Compose descarga
`ghcr.io/gaboiv/dbyson:${IMAGE_TAG}`; no requiere Node, Go ni el repositorio.

```sh
cd /opt/dbyson
docker compose -f docker-compose.deploy.yml --env-file .env pull
docker compose -f docker-compose.deploy.yml --env-file .env up -d
```

Completa la clave maestra, token de instalación y destinos permitidos. La clave
maestra debe ser 32 bytes aleatorios en base64, guardada fuera del volumen y con
respaldo. No generes otra al reiniciar: cifra los datos persistentes. Una
instalación personal existente necesita conservar su clave y respaldar su
volumen antes de migrar.

El servicio publica solo `127.0.0.1:8082` por defecto. Conecta tu proxy HTTPS a
ese puerto, preservando Host y sobrescribiendo X-Forwarded-Proto. Si el proxy
está en otro contenedor, conecta ambos a una red Docker compartida y dirige el
tráfico a `dbyson:8080`. El proxy, DNS y certificados son del servidor, no de la
imagen. Cambiar de dominio no requiere reconstruir DByson.

## Variables de Google en el .env

El Compose traduce estas variables a la configuración DBYSON_* del contenedor:

```dotenv
PUBLIC_URL=https://dbyson.equaly.pe
GOOGLE_ENABLED=true
CREDENTIALS_ENABLED=false
GOOGLE_CLIENT_ID=<client-id-del-cliente-web-existente>
GOOGLE_CLIENT_SECRET=<secreto-solo-en-el-servidor>
ALLOWED_DOMAINS=equaly.pe,qconsultores.pe
GOOGLE_AUTO_PROVISION=true
GOOGLE_TRUST_MFA=false
```

PUBLIC_URL no debe terminar en `/`. El Client ID indicado por el usuario es
público y se puede reutilizar al desplegar, pero no se incorpora a la imagen.
El archivo real .env queda fuera de Git y del contexto de construcción.

En el cliente OAuth web existente, registra exactamente:

```text
https://dbyson.equaly.pe/api/auth/oidc/callback
```

Se utilizan únicamente los scopes `openid email profile`, sin acceso a Gmail,
Drive o administración de Workspace. Si los dominios pertenecen a organizaciones
Google distintas, utiliza audiencia External. Internal solo admite miembros de
la organización que posee el proyecto de Google Cloud.

El servidor exige correo verificado y el claim firmado hd dentro de la lista
configurada: el sufijo del correo por sí solo no acredita una cuenta Workspace.
El parámetro hd de la pantalla de selección es solo una ayuda visual.

Fuentes: [OpenID Connect de Google](https://developers.google.com/identity/openid-connect/openid-connect)
y [audiencia de aplicaciones](https://support.google.com/cloud/answer/15549945?hl=en).

## Primer inicio y permisos

1. En una instalación nueva, usa el token de instalación para crear la cuenta
   administradora inicial. El setup solo funciona antes de existir cuentas.
2. Antes de cerrar esa sesión, abre Equipo → Vincular correo y vincula su
   correo corporativo. Así Google recuperará la administración existente.
3. Al entrar aparecerá Continuar con Google. Con CREDENTIALS_ENABLED=false
   desaparecen usuario/contraseña del login y el servidor rechaza ese método.
4. Las cuentas nuevas de los dominios permitidos se crean como miembros; no
   reciben acceso automático a las conexiones. Concédelo en Gestionar acceso.
5. Comprueba una cuenta real de cada dominio, una cuenta ajena, una cuenta
   deshabilitada y los permisos efectivos antes de dar acceso al equipo.

Los errores de configuración que desactivan todas las vías de acceso impiden
el arranque. Para recuperación administrativa, una persona con acceso al
servidor puede habilitar temporalmente CREDENTIALS_ENABLED y recrear el
contenedor; esto no requiere una nueva imagen.

Google no se considera segundo factor solo por autenticar. Con
GOOGLE_TRUST_MFA=false sigue aplicándose el 2FA de DByson para producción y SSH.
Para una cuenta nueva de SSO que requiera enrolar 2FA local, la administración
puede establecer una contraseña inicial desde Equipo, que se usa para confirmar
el enrolamiento aunque el inicio por contraseña esté deshabilitado. Activa
GOOGLE_TRUST_MFA=true únicamente si Workspace exige MFA a todas las cuentas
admitidas. No se ha acreditado esa política para los dominios del despliegue.

No hay sincronización automática con grupos ni suspensión de Workspace:
administra y revoca las cuentas y sesiones de DByson desde Equipo. El inicio
real con Google y la conectividad del servidor se validan al desplegar con sus
credenciales; las pruebas del repositorio usan proveedores simulados.