# SSH, conexiones y espacios de trabajo

## Crear una conexión

El asistente separa Destino, Ruta y SSH, Protección y Comprobar. Permite probar el borrador con las credenciales guardadas al editar, sin guardar primero ni probar por accidente el destino anterior. Los secretos vacíos conservan los existentes.

El inicio permite buscar por nombre, destino, usuario o base; filtrar por entorno, SSH, solo lectura o favoritas; y seleccionar hasta 25 perfiles para compartir. Las favoritas y las referencias de pestañas se guardan por usuario en este navegador. No se guardan contraseñas ni claves en almacenamiento del navegador.

## Túneles SSH

- Conexión directa a SSH o un bastión previo (dos saltos).
- Autenticación por contraseña, clave privada o ssh-agent del servidor (requiere SSH_AUTH_SOCK accesible).
- Archivos OpenSSH/PEM y PuTTY PPK v2/v3, cifrados o sin cifrar. El lector PPK admite Argon2id/Argon2i; aplica topes a memoria, iteraciones y concurrencia. Claves de hasta 128 KiB; errores de formato/passphrase se muestran antes de guardar.
- Obtener huella realiza únicamente el intercambio de claves: no confía automáticamente ni autentica al usuario. Hay que contrastar la huella y pulsar Confiar en esta huella, o introducir una SHA256 verificada por otro medio. Un cambio de huella bloquea el acceso.
- El host MySQL se interpreta desde el último servidor SSH. Para MySQL en esa misma máquina se usa 127.0.0.1. El bastión se interpreta desde DByson y el SSH final desde el bastión.
- No se abre un puerto local: se usa direct-tcpip por cada conexión física MySQL. Cerrar/cancelar MySQL libera el canal y la cadena SSH. Los canales tienen keepalive y límite de espera de establecimiento. Las consultas mantienen los límites y timeouts existentes.
- TLS MySQL conserva su configuración y nombre de servidor originales. El túnel SSH no cambia ni desactiva automáticamente TLS. Si se desactiva TLS, SSH protege hasta el servidor SSH; el tramo posterior a MySQL requiere una red de confianza.
- La allowlist se aplica al punto de entrada y a los destinos remotos. Los nombres resueltos por SSH requieren una entrada por nombre cuando hay allowlist; un CIDR exige una IP literal para el destino remoto. La resolución DNS remota depende del servidor SSH de confianza. No se permiten IPs de metadatos/link-local literales.

### Claves adjuntas y rutas

Adjuntar una clave la envía al backend y la almacena cifrada en el vault. La API devuelve únicamente su nombre y si hay una clave almacenada. El navegador no puede abrir libremente una ruta de Windows/Linux del receptor.

Para usar archivos del servidor, configurar `ADMINBASE_SSH_KEY_DIR=/keys` y montar un directorio de claves de solo lectura, legible por el usuario 65532 de DByson. La ruta introducida puede ser relativa a ese directorio (por ejemplo `equipo/servidor.ppk`) o absoluta dentro del mismo. La lectura usa `os.OpenRoot` para impedir escapes y symlinks hacia fuera; solo se aceptan archivos regulares.

Un perfil compartido conserva `keyFile` y `keyLabel`, pero **nunca incluye las claves privadas SSH ni sus passphrases**, ni siquiera en la opción de compartir credenciales. El receptor coloca el archivo en la ruta equivalente de su instalación o lo adjunta de nuevo. La passphrase se introduce por separado. No basta con que el archivo esté en una ruta del equipo del navegador si DByson corre en otro servidor/contenedor.

## Pestañas y accesos

Abrir una tabla crea/reutiliza su pestaña principal. Contenido, estructura, modificación, inserción, SQL y exportación se agrupan dentro de la tabla. SQL y el botón + crean consultas internas si hay una tabla activa; fuera de ella abren un espacio principal. Cada espacio conserva filtros, páginas y borradores mientras está abierto. Al cambiar de pestaña se cancelan las cargas de navegación pendientes; al volver se actualizan sus datos. Las consultas SQL lanzadas pueden continuar mientras se consulta otra pestaña.

Ctrl+K abre la paleta para conexiones, pestañas y acciones. Ctrl+Alt+N crea una consulta en la conexión activa. Las flechas/Home/End navegan por los encabezados de pestaña. Se avisa al cerrar una pestaña con SQL modificado o al recargar con borradores. El clic de la rueda también cierra pestañas. Se admiten hasta 16 principales y 48 vistas en total.

Al recargar se recuperan referencias de conexión/base/tabla, no resultados ni texto SQL: los borradores SQL son de esta sesión. Las conexiones se vuelven a autorizar en el servidor.

## Descargar y recibir perfiles

El archivo `.dbyson` es un formato versionado, independiente de IDs, sesiones y claves maestras de la instalación.

- Por defecto: destinos, usuarios, entorno, TLS/CA pública, solo lectura y referencias SSH, sin credenciales.
- Opcional: contraseñas MySQL/SSH y material cliente TLS en paquete cifrado con XChaCha20-Poly1305 y Argon2id (64 MiB, tres pasadas, salt aleatorio). Frase de al menos 12 caracteres. Las claves SSH siguen separadas.
- Importar requiere sesión y CSRF, valida tamaño/formato/versión y crea perfiles nuevos del usuario, sin sobrescribir otros. La importación es transaccional. Una frase incorrecta o archivo alterado falla de forma completa.
- Máximo 25 perfiles y 750 KiB por archivo. Los perfiles sin credenciales necesitan completarse antes de conectar.

## Identificación de MySQL y Docker

Los accesos Host de Docker y Red de Docker ofrecen host.docker.internal y mysql. Al usarlos, o pulsar Buscar servicios, se consultan destinos acotados: nombres habituales, puertos 3306–3308 del host y destinos MySQL del inventario Docker. Se lee el saludo MySQL sin enviar credenciales. También se puede identificar un host/puerto concreto. Se limita el número de destinos, concurrencia y duración; no se barre la subred.

Se muestran interfaces/redes visibles desde el servidor y, si está configurado el componente de descubrimiento, las redes Docker. Los accesos a redes copian el comando de unión; no modifican automáticamente la red del contenedor. Solo aparecen servicios cuyo protocolo MySQL responde desde DByson; un puerto cerrado, bloqueado o inaccesible entre redes no aparece como servicio disponible.

### Componente Docker opcional

`deploy/docker-compose.local.yml` incluye un componente que recibe el socket Docker y expone únicamente GET /containers/json y GET /networks, sin parámetros, operaciones de escritura, inspección de secretos o rutas arbitrarias. Filtra los datos a nombres, puertos, IPs y subredes. La aplicación principal no recibe el socket.

La aplicación lo consulta mediante `ADMINBASE_DOCKER_DISCOVERY_URL=http://discovery:8090`. El componente debe permanecer en una red interna privada, sin publicar su puerto; corre con raíz para acceder al socket, sin capacidades adicionales, sin privilegios nuevos y con filesystem de solo lectura. El socket sigue siendo un recurso privilegiado: este componente es opcional y debe aislarse como en el ejemplo. Al deshabilitarlo, siguen funcionando los destinos manuales y nombres habituales.

## Validación

Pruebas Go: forwarding TCP y bastión, pin incorrecto, allowlist, cancelación de handshake, OpenSSH cifrado, PPK v2/v3 con y sin passphrase, aislamiento del directorio de claves, redacción, paquetes cifrados, archivos alterados, importación atómica, propiedad y CSRF; además de la suite previa. Fixtures generadas con PuTTYgen/OpenSSH para tests, sin uso en servidores reales.

Integración desechable: consultas MySQL 8.4 mediante OpenSSH real con contraseña, OpenSSH, PPK v2/v3, variantes cifradas y bastión; cancelación de consultas en curso. El transporte conserva las comprobaciones previas de solo lectura.


Recorrido de interfaz verificado en Chrome contra Go/MySQL/SSH reales: alta por asistente con PPK v3 cifrada, aceptación de huella, prueba y guardado; identificación MySQL y redes Docker; filtro preservado entre pestañas y borrador SQL independiente; paleta; descarga sin clave y reimportación con aviso de adjuntarla. Sin errores JavaScript/HTTP de aplicación tras corregir la cola de procesamiento de claves. La paginación permanece visible y sin scroll general a 1440×900, 1366×768, 1024×768 y 960×600. También se verificó una PPK cifrada por ruta montada y el rechazo de rutas fuera del directorio autorizado.


## Despliegue local

Actualizado en `127.0.0.1:8082` con salud `ok`, HTTP 200 y bundle `index-DTJ3Ysmp.js`. Se conservaron el volumen `adminbase-data`, el setup y la configuración existente. El contenedor anterior a SSH está detenido como `adminbase-previous-ssh-20261021183916`; la revisión previa al último ajuste visual como `adminbase-previous-ssh-polish-20261021184220`.

El componente `adminbase-discovery` está en la red interna `dbyson-discovery`, sin puertos publicados. El contenedor principal conserva su red bridge y el acceso al host Docker. Se comprobaron metadatos de redes con el componente desplegado. Los contenedores MySQL/SSH/app temporales y su red se retiraron junto con los datos anónimos de pruebas.

SSH está implementado; mTLS de acceso, TOTP y auditoría de fase 2 siguen pendientes. Este cambio no publica ni certifica v0.2.
