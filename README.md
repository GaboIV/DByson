# AdminBase

IDE web de bases de datos: la sencillez de Adminer, la potencia de phpMyAdmin, UX moderna y seguridad por defecto.
Plan completo en [docs/PLAN_MAESTRO.md](docs/PLAN_MAESTRO.md).

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
| `ADMINBASE_ALLOWED_HOSTS` | vacío (todo permitido salvo link-local) | Lista separada por comas de hosts, IPs o CIDR a los que AdminBase puede conectarse. |
| `ADMINBASE_SETUP_TOKEN` | — | Si se define, es obligatorio para crear el primer administrador. |

Generar una clave maestra: `openssl rand -base64 32`.

## Modos TLS hacia MySQL/MariaDB

`verify_identity` (por defecto) · `verify_ca` · `required` (cifra pero no autentica al servidor) · `disabled`.
Acepta CA propia y certificado/clave de cliente. La prueba de conexión informa si la sesión quedó realmente cifrada.
Los certificados autogenerados de MySQL no incluyen el nombre del host, por lo que con ellos solo funcionan `verify_ca` (aportando su CA) o `required`.
