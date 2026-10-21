# AdminBase — Plan Maestro

> Un IDE web de bases de datos: **la sencillez de Adminer, la potencia de phpMyAdmin, una UX moderna y seguridad por defecto.**
> Se distribuye como una sola imagen Docker para que cualquier compañero lo levante con un comando.

---

## 1. Visión y principios

| Principio | Qué significa en la práctica |
|---|---|
| **Cero fricción** | `docker run` y listo. Un solo binario, sin PHP, sin Apache, sin configuración obligatoria. |
| **Seguro por defecto** | TLS verificado, credenciales cifradas, sin `LOAD DATA LOCAL`, lista blanca de hosts, auditoría. Lo inseguro requiere activarse explícitamente. |
| **Keyboard-first** | Paleta de comandos (`Ctrl+K`), atajos para todo, pestañas como un IDE. |
| **Nunca romper producción por accidente** | Conexiones etiquetadas por entorno (prod = rojo), modo solo-lectura, confirmación de sentencias destructivas, edición en lote con vista previa del SQL antes de aplicar. |
| **Crecer sin reescribir** | Arquitectura de *drivers* desde el día 1: MySQL primero, PostgreSQL/SQLite/SQL Server después. |

**No-objetivos (por ahora):** reemplazar herramientas de BI, ser un ORM, administrar clústeres o hacer migraciones de esquema versionadas.

---

## 2. Decisión de stack (recomendado)

| Capa | Elección | Por qué |
|---|---|---|
| **Backend** | **Go** | Un binario estático (~20 MB), excelente para Docker *distroless*, SSH nativo (`golang.org/x/crypto/ssh`), driver MySQL maduro con TLS (`go-sql-driver/mysql`), concurrencia para streaming de resultados grandes. |
| **Frontend** | **React + TypeScript + Vite** | Ecosistema enorme, fácil encontrar colaboradores. |
| UI kit | Tailwind + shadcn/ui (Radix) | Accesible, moderno, temable (modo claro/oscuro). |
| Editor SQL | CodeMirror 6 (o Monaco) | Autocompletado por esquema, resaltado, multi-cursor. CodeMirror pesa mucho menos. |
| Grilla de datos | TanStack Table + virtualización (o Glide Data Grid) | Millones de filas sin congelar el navegador. |
| Estado/servidor | TanStack Query | Caché, reintentos y cancelación de consultas. |
| Persistencia interna | SQLite (embebido) | Usuarios, perfiles de conexión cifrados, historial, auditoría. Sin servicios extra. |
| Empaquetado | Frontend embebido en el binario (`go:embed`) | Un solo artefacto → una sola imagen. |
| Futuro escritorio | Wails | Reutiliza el mismo backend Go + frontend React para una app nativa. |

> **Alternativa:** si el equipo es fuertemente JS, Node.js + TypeScript (Fastify + `mysql2` + `ssh2`) también funciona; se pierde el binario único y la imagen crece (~150 MB), pero comparte lenguaje con el frontend.

---

## 3. Arquitectura

```
 Navegador (React SPA)
   │  HTTPS (TLS 1.3, opcional mTLS con certificado de cliente)
   │  Cookie de sesión HttpOnly + Secure + SameSite=Strict, token CSRF
   ▼
 ┌──────────────────────────── AdminBase (binario Go) ────────────────────────────┐
 │  HTTP API (REST + WebSocket para streaming / cancelar consultas)               │
 │  ├─ Auth: usuarios locales (Argon2id) · TOTP · Passkeys · OIDC (fase 4)        │
 │  ├─ RBAC + Políticas (solo-lectura, entornos, hosts permitidos)                │
 │  ├─ Vault: perfiles de conexión cifrados (XChaCha20-Poly1305 / AES-256-GCM)    │
 │  ├─ Session Manager: pools por usuario+conexión, timeouts, cancelación         │
 │  ├─ Tunnel Manager: SSH (clave/agente/contraseña), ProxyJump, known_hosts      │
 │  ├─ Driver API ──► mysql (MySQL/MariaDB) · postgres · sqlite · mssql (futuro)  │
 │  └─ Auditoría (quién, qué SQL, cuándo, contra qué conexión)                    │
 │  SQLite interno (/data)                                                        │
 └────────────────────────────────────────────────────────────────────────────────┘
   │                                   │
   │ TLS verificado (VERIFY_IDENTITY)  │ SSH (ed25519) → túnel local → MySQL
   ▼                                   ▼
 MySQL directo                     Bastión / servidor remoto
```

**Regla de oro:** las credenciales de la base de datos **nunca llegan al navegador**. El frontend solo conoce un `connectionId`; el backend abre túneles y conexiones.

### Interfaz de driver (contrato para soportar más motores)

```go
type Driver interface {
    Name() string
    Connect(ctx context.Context, cfg ConnConfig, dialer Dialer) (Conn, error)
}

type Conn interface {
    Databases(ctx context.Context) ([]Database, error)
    Objects(ctx context.Context, db string) ([]Object, error)      // tablas, vistas, rutinas, triggers
    Describe(ctx context.Context, ref ObjectRef) (*TableSchema, error)
    Query(ctx context.Context, sql string, opts QueryOpts) (RowStream, error)
    Exec(ctx context.Context, stmts []Statement, tx bool) (*ExecResult, error)
    Explain(ctx context.Context, sql string) (*Plan, error)
    DDL(change SchemaChange) (string, error)                       // genera SQL, no lo ejecuta
    Cancel(ctx context.Context, queryID string) error
    Close() error
}
```

`Dialer` es la clave: el driver no sabe si va directo o por túnel SSH — el Tunnel Manager le entrega un `net.Conn`.

### Estructura del repositorio

```
adminbase/
├─ cmd/adminbase/          # main.go, flags, arranque
├─ internal/
│  ├─ api/                 # handlers HTTP/WS, middlewares (CSRF, rate limit, headers)
│  ├─ auth/                # usuarios, sesiones, TOTP, WebAuthn, OIDC
│  ├─ vault/               # cifrado de perfiles, derivación de claves
│  ├─ tunnel/              # SSH, ProxyJump, known_hosts
│  ├─ drivers/mysql/       # implementación MySQL/MariaDB
│  ├─ policy/              # RBAC, allowlist de hosts, clasificador de SQL destructivo
│  ├─ audit/
│  └─ store/               # SQLite interno + migraciones
├─ web/                    # React + Vite (se compila y se embebe)
├─ deploy/                 # Dockerfile, docker-compose de ejemplo, Helm (futuro)
├─ docs/                   # este plan, ADRs, modelo de amenazas
└─ .github/workflows/      # CI: tests, lint, SAST, build multi-arch, firma
```

---

## 4. Seguridad (el corazón del proyecto)

### 4.1 Acceso a AdminBase
- **Usuarios locales** con hash **Argon2id**; bloqueo progresivo por intentos fallidos y rate limiting por IP/usuario.
- **2FA**: TOTP desde la fase 2; **Passkeys/WebAuthn** como opción preferida.
- **SSO OIDC** (Google Workspace, Microsoft Entra ID, Keycloak) en modo equipo.
- **mTLS opcional**: AdminBase puede exigir **certificado de cliente** emitido por una CA propia (`adminbase cert issue --user ana`). Esa es la protección "estilo certificado": sin el certificado instalado, el navegador ni siquiera ve la pantalla de login.
- Sesiones: cookie `HttpOnly; Secure; SameSite=Strict`, expiración por inactividad, revocación desde el panel.

### 4.2 Conexiones a la base de datos
- **TLS hacia MySQL** con modos `DISABLED / REQUIRED / VERIFY_CA / VERIFY_IDENTITY` (por defecto `VERIFY_IDENTITY` cuando hay TLS), CA personalizada y **certificado + clave de cliente** MySQL.
- **Túnel SSH**: autenticación por clave (ed25519/RSA, con passphrase), **ssh-agent**, contraseña (desaconsejada), **ProxyJump** (bastión → servidor), keep-alive y reconexión.
- **Verificación de host key**: TOFU con huella mostrada al usuario y guardada; un cambio de huella **bloquea** la conexión (anti MITM).
- **`LOAD DATA LOCAL INFILE` deshabilitado** — evita el ataque de "servidor MySQL malicioso" que lee archivos del host (un fallo histórico de herramientas tipo Adminer).
- **Allowlist de destinos** (hosts/CIDR/puertos permitidos) — evita que AdminBase se use como proxy SSRF hacia la red interna (CVE-2021-21311 en Adminer).
- **Vault**: perfiles cifrados con **envelope encryption** — una clave de datos por perfil, envuelta con una clave maestra derivada de una passphrase (Argon2id) o provista por variable de entorno / Docker secret / KMS (futuro).

### 4.3 Protección contra errores humanos
- Etiqueta de entorno por conexión (`local`, `dev`, `staging`, `prod`) con color en toda la UI.
- **Modo solo-lectura** por conexión o por rol (sesión MySQL con `SET SESSION TRANSACTION READ ONLY` + validación del lado de AdminBase).
- Clasificador de SQL: `DROP`, `TRUNCATE`, `ALTER`, `DELETE/UPDATE` sin `WHERE` → confirmación escribiendo el nombre de la base/tabla.
- Edición de datos **en lote**: los cambios quedan "pendientes", se muestra el SQL generado y se aplican en **una transacción**.
- Límite y timeout de consultas configurables; botón de **cancelar consulta** real (`KILL QUERY`).

### 4.4 Endurecimiento web y de la cadena de suministro
- Headers: CSP estricta sin `unsafe-inline`, `X-Frame-Options: DENY`, HSTS, `Referrer-Policy`, `Permissions-Policy`.
- Protección CSRF, validación estricta de entrada, sin SQL construido por concatenación en el backend (identificadores siempre escapados por el driver).
- Imagen **distroless, usuario no-root, sistema de archivos de solo lectura** (salvo `/data`).
- CI: `govulncheck`, `gosec`, `npm audit`, CodeQL, **Trivy** sobre la imagen, Dependabot/Renovate.
- **SBOM** (Syft) y **firma de imágenes** con cosign; builds reproducibles.
- **Auditoría** inmutable (append-only) de logins y sentencias ejecutadas; exportable a JSON/syslog.
- Modelo de amenazas (STRIDE) documentado en `docs/threat-model.md` y revisado en cada fase. Pentest/bug bounty antes de la v1.0.

> **Recomendación operativa:** AdminBase nunca debe exponerse a Internet sin mTLS, SSO o una VPN/zero-trust (Tailscale, Cloudflare Access) delante. El modo por defecto escucha en `127.0.0.1`.

---

## 5. UX: qué lo hace mejor que Adminer / phpMyAdmin

- **Pantalla de inicio** con conexiones guardadas como tarjetas (color por entorno, último uso, estado del túnel).
- **Árbol lateral** de bases → tablas/vistas/rutinas con búsqueda difusa.
- **Pestañas** de trabajo (tabla, consulta, estructura) que sobreviven al recargar.
- **Grilla de datos** virtualizada: filtros por columna sin escribir SQL, orden, edición inline, vista JSON/BLOB, copiar como CSV/JSON/INSERT, navegación por claves foráneas (clic en un FK → abre la fila relacionada).
- **Editor SQL** con autocompletado por esquema, formateador, ejecutar selección (`Ctrl+Enter`), múltiples resultados, historial y consultas guardadas.
- **EXPLAIN visual** con advertencias (full scan, índices no usados).
- **Editor de estructura** visual (columnas, índices, FKs) que **muestra el `ALTER TABLE` antes de ejecutarlo**.
- **Diagrama ER** auto-generado.
- **Importar/Exportar**: CSV, JSON, SQL (dump con streaming, sin límites de memoria).
- **Paleta de comandos** `Ctrl+K`, modo oscuro, i18n (español e inglés desde el inicio).

---

## 6. Distribución y uso en equipo

### Modo personal
```bash
docker run -d --name adminbase -p 127.0.0.1:8080:8080 -v adminbase-data:/data adminbase/adminbase:latest
```
- Imágenes en **Docker Hub** y **GHCR**, multi-arquitectura (`amd64`, `arm64` → funciona en Mac M-series y Raspberry Pi).
- Para conectar a un MySQL en tu propia máquina desde el contenedor: `host.docker.internal`.

### Modo equipo
- Un AdminBase compartido (servidor interno o VPS detrás de VPN) con **usuarios, roles y conexiones compartidas**:
  - Roles: `owner`, `admin`, `developer` (lectura/escritura en no-prod), `analyst` (solo lectura), `viewer`.
  - Permisos por conexión y por entorno ("María puede escribir en dev, solo leer en prod").
- **Compartir sin servidor**: exportar perfiles de conexión como archivo cifrado `.abx` (cifrado con passphrase vía `age`); el compañero lo importa en su propio AdminBase. Las claves SSH pueden excluirse para que cada uno use la suya.
- `docker-compose.yml` de referencia con Caddy/Traefik para HTTPS automático.
- Configuración por variables de entorno y Docker secrets (`ADMINBASE_MASTER_KEY_FILE`, `ADMINBASE_ALLOWED_HOSTS`, `ADMINBASE_OIDC_*`).

---

## 7. Roadmap por fases

Las estimaciones asumen 1 persona a tiempo parcial (~15 h/semana). Con dedicación completa, dividir aprox. entre 2,5.

### Fase 0 — Fundaciones (2–3 semanas)
- Repositorio (monorepo), licencia, convenciones, ADRs.
- Esqueleto Go + React embebido; build de imagen Docker distroless.
- CI: lint, tests, `govulncheck`, Trivy, build multi-arch.
- Sistema de diseño base (tokens, tema claro/oscuro, componentes).
- Modelo de amenazas v1.
- MySQL + MariaDB de prueba en `docker-compose` (con datos de ejemplo tipo Sakila) para desarrollo y tests de integración.

**Entregable:** contenedor que arranca, muestra login y pasa el pipeline de seguridad.

### Fase 1 — MVP MySQL (6–8 semanas)
- Login de usuario local (Argon2id), setup inicial del primer admin.
- Vault cifrado y CRUD de perfiles de conexión.
- Conexión directa a MySQL/MariaDB con TLS (`VERIFY_IDENTITY`, CA propia).
- Explorador: bases, tablas, vistas; estructura de tabla.
- Grilla de datos con paginación por cursor, filtros, orden, edición inline con lote + transacción.
- Editor SQL con autocompletado, múltiples resultados, historial, cancelación.
- Exportar CSV/JSON/SQL.
- Etiquetas de entorno, confirmación de sentencias destructivas, `LOAD DATA LOCAL` deshabilitado, allowlist de hosts.

**Entregable:** `v0.1.0` en Docker Hub — ya usable en el día a día en lugar de Adminer.

### Fase 2 — Conectividad segura (4–5 semanas)
- Túnel SSH: clave, passphrase, ssh-agent, contraseña; verificación de host key; ProxyJump.
- Certificados de cliente MySQL.
- mTLS para acceder a AdminBase (+ CLI para emitir certificados de cliente).
- TOTP 2FA, gestión de sesiones activas.
- Auditoría v1.

**Entregable:** `v0.2.0` — apto para conectar a servidores remotos y producción de forma segura.

### Fase 3 — Productividad (6–8 semanas)
- Editor visual de estructura (crear/alterar tablas, índices, FKs) con vista previa del DDL.
- Importación CSV/SQL con streaming y barra de progreso.
- EXPLAIN visual, diagrama ER, navegación por FKs.
- Consultas guardadas, snippets, paleta de comandos, pestañas persistentes.
- Gestión de usuarios y permisos de MySQL (`GRANT`), lista de procesos (`SHOW PROCESSLIST` + kill).
- Passkeys/WebAuthn.

**Entregable:** `v0.5.0` — paridad práctica con phpMyAdmin en lo que se usa a diario.

### Fase 4 — Equipos (5–6 semanas)
- Multiusuario con RBAC y permisos por conexión/entorno.
- Conexiones compartidas y exportación/importación `.abx` cifrada.
- SSO OIDC.
- Auditoría avanzada (filtros, exportación, retención) y modo solo-lectura forzado por rol.
- Helm chart / guía de despliegue detrás de VPN o Cloudflare Access.

**Entregable:** `v1.0.0` — tras auditoría de seguridad externa / pentest.

### Fase 5+ — Expansión (continuo)
- Drivers: **PostgreSQL** → SQLite → SQL Server → (Redis/MongoDB como vistas especiales).
- App de escritorio con Wails (mismo código).
- Asistente IA opcional (texto → SQL, explicar consultas, sugerir índices), siempre **opt-in**, enviando solo el esquema y nunca datos sin permiso explícito.
- Respaldos programados, comparación de esquemas (diff entre dev y prod), API pública y sistema de plugins.

---

## 8. Calidad y pruebas
- **Unitarias** en Go (vault, clasificador de SQL, generación de DDL) y en el frontend (Vitest).
- **Integración** con Testcontainers contra MySQL 5.7, 8.0, 8.4 LTS, 9.x y MariaDB 10.11/11.x.
- **Pruebas de túnel SSH** con un contenedor `openssh-server` en CI.
- **E2E** con Playwright (flujos: conectar, editar fila, ejecutar consulta, exportar).
- **Fuzzing** del clasificador de SQL y del parser de entrada (`go test -fuzz`).
- Benchmarks: abrir tabla de 1M filas < 1 s al primer render; exportar 1 GB sin superar ~100 MB de RAM.

---

## 9. Riesgos y mitigaciones

| Riesgo | Mitigación |
|---|---|
| Alcance infinito (querer ser DataGrip desde el inicio) | Fases con entregables usables; todo lo no listado va a un backlog. |
| Una herramienta de BD comprometida = todas las BD comprometidas | Seguridad como requisito de cada fase, no al final; escucha en localhost por defecto; pentest antes de v1.0. |
| Diferencias entre versiones de MySQL/MariaDB | Matriz de tests de integración desde la fase 1. |
| Resultados enormes que tumban el navegador o el servidor | Streaming, paginación por cursor, límites por defecto, virtualización. |
| Tipos complicados (BLOB, JSON, GEOMETRY, fechas cero, charsets) | Representación tipada en la API, visores dedicados, tests específicos. |
| El nombre "AdminBase" ya en uso | Verificar marca, dominio (`adminbase.dev`/`.io`), namespace de Docker Hub y org de GitHub antes de publicar. |

---

## 10. Métricas de éxito
- De `docker run` a la primera consulta en **< 60 segundos**.
- Imagen Docker **< 30 MB**, arranque **< 1 s**.
- **0 vulnerabilidades críticas/altas** abiertas en cada release.
- El equipo deja de usar Adminer/phpMyAdmin para el trabajo diario tras la v0.5.

---

## 11. Próximos pasos inmediatos
1. Confirmar stack (Go + React recomendado) y licencia (MIT/Apache-2.0 si será open source; propietaria si es interna).
2. Reservar nombre: org de GitHub, namespace de Docker Hub, dominio.
3. Ejecutar la **Fase 0**: scaffolding del repo, Dockerfile, CI y `docker-compose` de desarrollo con MySQL/MariaDB.
4. Escribir los primeros ADRs: stack, modelo de cifrado del vault, interfaz de drivers.
