# DByson

IDE web de bases de datos: la sencillez de Adminer, la potencia de phpMyAdmin, UX moderna y seguridad por defecto.
Plan completo en [docs/PLAN_MAESTRO.md](docs/PLAN_MAESTRO.md). Evaluación del avance y prioridades en [docs/EVALUACION_2026-10-21.md](docs/EVALUACION_2026-10-21.md).

DByson toma su visión de una esfera de Dyson para bases de datos. Actualmente implementa MySQL/MariaDB. Los comandos y las variables `ADMINBASE_*` conservan sus nombres por compatibilidad.

## Desarrollo

Requisitos: Go 1.26+, Node 22+, Docker.

```bash
make dev-db                       # MySQL 8.4 (:13306) y MariaDB 11.4 (:13307), root / dev-root, base `shop`
cd web && npm ci && npm run dev   # frontend en :5173 (proxy /api -> :8080)
go run ./cmd/adminbase            # API en 127.0.0.1:8080
```

## Imagen Docker

```bash
make docker
docker run --rm -p 127.0.0.1:8080:8080 adminbase:dev
```

Por defecto el binario escucha solo en `127.0.0.1:8080` (`ADMINBASE_LISTEN` para cambiarlo); la imagen lo expone en `0.0.0.0` dentro del contenedor, así que publica el puerto solo en loopback como arriba.

## Configuración (variables de entorno)

| Variable | Por defecto | Descripción |
|---|---|---|
| `ADMINBASE_LISTEN` | `127.0.0.1:8080` | Dirección de escucha. |
| `ADMINBASE_DATA_DIR` | `./data` (`/data` en Docker) | SQLite interno (usuarios, sesiones, perfiles cifrados). |
| `ADMINBASE_MASTER_KEY` / `ADMINBASE_MASTER_KEY_FILE` | — | Clave maestra del vault (base64, 32 bytes). Si no se define, se genera `master.key` en el directorio de datos. **En producción guárdala fuera del volumen de datos** (Docker secret). |
| `ADMINBASE_ALLOWED_HOSTS` | vacío (todo permitido salvo link-local) | Lista separada por comas de hosts, IPs o CIDR a los que DByson puede conectarse. |
| `ADMINBASE_SETUP_TOKEN` | — | Si se define, es obligatorio para crear el primer administrador. |
| `ADMINBASE_REQUIRE_MFA` | `sensitive` | Cuándo se exige verificación en dos pasos: `off` (opcional), `sensitive` (conexiones `prod` y túneles SSH, incluida su exportación con credenciales) o `all` (todo salvo el inicio de sesión y la propia configuración de 2FA). |

Generar una clave maestra: `openssl rand -base64 32`.

## Modos TLS hacia MySQL/MariaDB

`verify_identity` (por defecto) · `verify_ca` · `required` (cifra pero no autentica al servidor) · `disabled`.
Acepta CA propia y certificado/clave de cliente. La prueba de conexión informa si la sesión quedó realmente cifrada.
Los certificados autogenerados de MySQL no incluyen el nombre del host, por lo que con ellos solo funcionan `verify_ca` (aportando su CA) o `required`.

## Protecciones del MVP

Resultados limitados a 1.000 filas y 8 MiB de datos JSON, solo lectura reforzado, confirmaciones de un solo uso y exportaciones por streaming. Comportamiento, límites y pruebas en [docs/MVP_SEGURIDAD.md](docs/MVP_SEGURIDAD.md).

## Consulta diaria

Filtros aplicados con Mostrar, cargas cancelables, paginación sin conteo obligatorio, conteo exacto opcional y copia/detalle de registros. Uso y pruebas en [docs/EXPERIENCIA_CONSULTA.md](docs/EXPERIENCIA_CONSULTA.md).

## SSH y trabajo diario

Túneles con OpenSSH/PPK, bastión, asistente de conexión, pestañas, paleta Ctrl+K, detección Docker y perfiles compartibles. Véase [guía y límites](docs/SSH_Y_ESPACIOS.md). Para descubrimiento Docker con un componente de metadatos aislado: [compose local](deploy/docker-compose.local.yml).

Vistas internas por tabla, cierre con la rueda, opciones, temas e inspector de registros: [guía de interfaz](docs/INTERFAZ_Y_PREFERENCIAS.md).

## Verificación en dos pasos y auditoría

- **2FA (TOTP):** se activa en *Seguridad* con cualquier app de autenticación. Al activarla se entregan 10 códigos de recuperación de un solo uso y se cierran las demás sesiones. El secreto se guarda cifrado en el vault y cada código solo sirve una vez.
- **Política:** con `ADMINBASE_REQUIRE_MFA=sensitive` (por defecto) las conexiones `prod` y las que usan túnel SSH solo se abren desde una sesión verificada con 2FA.
- **Auditoría:** se registran inicios de sesión (también fallidos), cambios en conexiones, consultas SQL, cambios de datos y exportaciones. Las lecturas de tablas no se registran. Las filas no se pueden modificar ni borrar por SQL, y cada una lleva un HMAC encadenado con una clave derivada de la clave maestra: *Verificar integridad* detecta ediciones, borrados o reordenamientos en el archivo. Limitación conocida: truncar las últimas entradas no se detecta hasta que exista un anclaje externo del último hash.
