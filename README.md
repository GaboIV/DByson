# DByson

<img src="logo-dbyson.png" alt="Logo de DByson: una base de datos rodeada por una esfera" width="160" />

IDE web de bases de datos: la sencillez de Adminer, la potencia de phpMyAdmin, UX moderna y seguridad por defecto.
Plan completo en [docs/PLAN_MAESTRO.md](docs/PLAN_MAESTRO.md). Evaluación del avance y prioridades en [docs/EVALUACION_2026-10-21.md](docs/EVALUACION_2026-10-21.md).

DByson toma su visión de una esfera de Dyson para bases de datos. Actualmente implementa MySQL/MariaDB. Antes se llamó AdminBase: ver [Migrar desde AdminBase](#migrar-desde-adminbase).

## Desarrollo

Requisitos: Go 1.26+, Node 22+, Docker.

```bash
make dev-db                       # MySQL 8.4 (:13306) y MariaDB 11.4 (:13307), root / dev-root, base `shop`
cd web && npm ci && npm run dev   # frontend en :5173 (proxy /api -> :8080)
go run ./cmd/dbyson            # API en 127.0.0.1:8080
```

## Imagen Docker

```bash
make docker
docker run --rm -p 127.0.0.1:8080:8080 dbyson:dev
```

Por defecto el binario escucha solo en `127.0.0.1:8080` (`DBYSON_LISTEN` para cambiarlo); la imagen lo expone en `0.0.0.0` dentro del contenedor, así que publica el puerto solo en loopback como arriba.

## Configuración (variables de entorno)

| Variable | Por defecto | Descripción |
|---|---|---|
| `DBYSON_LISTEN` | `127.0.0.1:8080` | Dirección de escucha. |
| `DBYSON_DATA_DIR` | `./data` (`/data` en Docker) | SQLite interno (usuarios, sesiones, perfiles cifrados). |
| `DBYSON_MASTER_KEY` / `DBYSON_MASTER_KEY_FILE` | — | Clave maestra del vault (base64, 32 bytes). Si no se define, se genera `master.key` en el directorio de datos. **En producción guárdala fuera del volumen de datos** (Docker secret). |
| `DBYSON_ALLOWED_HOSTS` | vacío (todo permitido salvo link-local) | Lista separada por comas de hosts, IPs o CIDR a los que DByson puede conectarse. |
| `DBYSON_SETUP_TOKEN` | — | Si se define, es obligatorio para crear el primer administrador. |
| `DBYSON_MODE` | `personal` | `personal`: una persona en su propia máquina (2FA disponible pero no obligatoria). `team`: DByson accesible por red para varias personas (2FA obligatoria para `prod` y SSH). |
| `DBYSON_REQUIRE_MFA` | según el modo | Fuerza la política: `off` (opcional), `sensitive` (conexiones `prod` y túneles SSH, incluida su exportación con credenciales) o `all` (todo salvo el inicio de sesión y la propia configuración de 2FA). Si se define, gana sobre el modo. |
| `DBYSON_TLS_CERT_FILE` / `DBYSON_TLS_KEY_FILE` | — | Certificado de servidor y clave PEM para HTTPS nativo (TLS 1.3). Deben configurarse juntos. |
| `DBYSON_TLS_CLIENT_CA_FILE` | — | CA PEM de clientes: exige certificado válido en todo el listener HTTPS, incluido el login. Requiere certificado y clave de servidor. |
| `DBYSON_WEBAUTHN_RP_ID` / `DBYSON_WEBAUTHN_ORIGIN` | — | Dominio y origen HTTPS explícitos para passkeys; deben configurarse juntos. HTTP solo para localhost. |

Generar una clave maestra: `openssl rand -base64 32`.

## Modos TLS hacia MySQL/MariaDB

`verify_identity` (por defecto) · `verify_ca` · `required` (cifra pero no autentica al servidor) · `disabled`.
Acepta CA propia y certificado/clave de cliente. La prueba de conexión informa si la sesión quedó realmente cifrada.
Los certificados autogenerados de MySQL no incluyen el nombre del host, por lo que con ellos solo funcionan `verify_ca` (aportando su CA) o `required`.

## Protecciones del MVP

Resultados limitados a 1.000 filas y 8 MiB de datos JSON, solo lectura reforzado, confirmaciones de un solo uso y exportaciones por streaming. Comportamiento, límites y pruebas en [docs/MVP_SEGURIDAD.md](docs/MVP_SEGURIDAD.md).

## Consulta diaria

Filtros aplicados con Mostrar, cargas cancelables, paginación sin conteo obligatorio, conteo exacto opcional y copia/detalle de registros. Uso y pruebas en [docs/EXPERIENCIA_CONSULTA.md](docs/EXPERIENCIA_CONSULTA.md).

Biblioteca privada de consultas cifradas en el servidor, navegación por claves foráneas, EXPLAIN visual, importación CSV/SQL, diseñador de estructura, procesos, cuentas MySQL, diagrama ER y passkeys: [guía de productividad](docs/PRODUCTIVIDAD.md). Los cambios de esta fase requieren reconstruir la aplicación; no están en la imagen publicada v0.1.0.

## SSH y trabajo diario

Túneles con OpenSSH/PPK, bastión, asistente de conexión, pestañas, paleta Ctrl+K, detección Docker y perfiles compartibles. Véase [guía y límites](docs/SSH_Y_ESPACIOS.md). Para descubrimiento Docker con un componente de metadatos aislado: [compose local](deploy/docker-compose.local.yml).

Vistas internas por tabla, cierre con la rueda, opciones, temas e inspector de registros: [guía de interfaz](docs/INTERFAZ_Y_PREFERENCIAS.md).

## Verificación en dos pasos y auditoría

- **2FA (TOTP):** se activa en *Seguridad* con cualquier app de autenticación. Al activarla se entregan 10 códigos de recuperación de un solo uso y se cierran las demás sesiones. El secreto se guarda cifrado en el vault y cada código solo sirve una vez.
- **Política:** en modo `personal` (por defecto) la 2FA es opcional: protege poco cuando DByson solo es accesible desde tu máquina. En modo `team` (`DBYSON_MODE=team`) las conexiones `prod` y las que usan túnel SSH solo se abren desde una sesión verificada con 2FA.
- **Auditoría:** se registran inicios de sesión (también fallidos), cambios en conexiones, consultas SQL, cambios de datos y exportaciones. Las lecturas de tablas no se registran. Las filas no se pueden modificar ni borrar por SQL, y cada una lleva un HMAC encadenado con una clave derivada de la clave maestra: *Verificar integridad* detecta ediciones, borrados o reordenamientos en el archivo. Limitación conocida: truncar las últimas entradas no se detecta hasta que exista un anclaje externo del último hash.

## Conectividad segura

La consola SQL ofrece **Cancelar consulta** mediante `KILL QUERY` y detiene las sentencias pendientes del script. En **Seguridad → Sesiones activas** puedes cerrar un acceso concreto o todas las demás sesiones de tu cuenta. Las cancelaciones y revocaciones quedan auditadas.

HTTPS/mTLS nativo, certificados de cliente MySQL y despliegue: [guía de Fase 2](docs/CONECTIVIDAD_SEGURA.md). mTLS complementa el login y la 2FA; las sesiones quedan vinculadas al certificado con el que se iniciaron.

## Documentación de diseño

- [Plan maestro](docs/PLAN_MAESTRO.md)
- [Modelo de amenazas](docs/threat-model.md)
- [Decisiones de arquitectura (ADR)](docs/adr/README.md)

## Publicar una versión

Al subir una etiqueta `vX.Y.Z`, el workflow `release.yml` publica en `ghcr.io/<owner>/dbyson` una imagen **amd64 + arm64** firmada con cosign, con SBOM y atestación de procedencia.

```bash
git tag v0.1.0 && git push origin v0.1.0
```

Construir localmente para ambas arquitecturas: `docker buildx build --platform linux/amd64,linux/arm64 -t dbyson .`

## Licencia

[Apache License 2.0](LICENSE). Copyright 2026 Gabriel Caraballo.

## Migrar desde AdminBase

El proyecto se llamó AdminBase. Desde esta versión todo usa el nombre DByson; una instalación anterior sigue funcionando sin cambios:

| Antes | Ahora | Compatibilidad |
|---|---|---|
| Variables `ADMINBASE_*` | `DBYSON_*` | Las antiguas se siguen leyendo hasta la v1.0 y el log avisa de cuáles renombrar. Si existen ambas, gana `DBYSON_*`. |
| Archivo `adminbase.db` | `dbyson.db` | Se renombra solo al arrancar (con sus archivos `-wal`/`-shm`). |
| Imagen `adminbase:dev`, binario `/adminbase`, volumen `adminbase-data` | `dbyson:dev`, `/dbyson`, `dbyson-data` | El volumen antiguo sirve tal cual: basta montarlo en `/data`. |
| Cookie `ab_session` | `dbyson_session` | Hay que volver a iniciar sesión una vez. |

Las etiquetas criptográficas internas (`adminbase/v1/…`) **no cambian**: están ligadas a cada credencial cifrada y a la cadena de auditoría. Ver [ADR 0006](docs/adr/0006-renombrado-a-dbyson.md).
