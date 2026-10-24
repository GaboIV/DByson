# DByson — Plan Maestro

> Un IDE web de bases de datos: **la sencillez de Adminer, la potencia de phpMyAdmin, una UX moderna y seguridad por defecto.**
> Se distribuye como una sola imagen Docker que cualquier persona levanta con un comando.

**Revisión:** 24 de octubre de 2026 · **Nombre:** DByson (antes AdminBase; ver [ADR 0006](adr/0006-renombrado-a-dbyson.md)) · **Licencia:** Apache-2.0

**Documentos relacionados:** [modelo de amenazas](threat-model.md) · [decisiones de arquitectura](adr/README.md) · detalle de lo implementado en [seguridad del MVP](MVP_SEGURIDAD.md), [experiencia de consulta](EXPERIENCIA_CONSULTA.md), [interfaz y preferencias](INTERFAZ_Y_PREFERENCIAS.md) y [SSH y espacios de trabajo](SSH_Y_ESPACIOS.md). La [evaluación del 21 de octubre](EVALUACION_2026-10-21.md) es histórica.

---

## 1. Dónde estamos

DByson ya es una herramienta usable a diario para **una persona con MySQL/MariaDB**, local o remoto por SSH. Lo que falta para la meta se agrupa en tres bloques:

1. **Integrar y desplegar la Fase 2:** la v0.1.0 está publicada y firmada. Las mejoras de conectividad segura están implementadas y validadas localmente; falta su integración y despliegue ([guía](CONECTIVIDAD_SEGURA.md)). La comprobación de marca está aplazada y no bloquea publicaciones.
2. **Productividad de un cliente maduro:** editor SQL con autocompletado, consultas guardadas, EXPLAIN, navegación por claves foráneas, importación.
3. **Modo equipo:** hoy solo existe el administrador inicial; no hay gestión de usuarios, roles, conexiones compartidas ni SSO.

| Fase | Estado | Resumen |
|---|---|---|
| 0 · Fundaciones | ✅ Cerrada | CI correcto en `main` tras integrar las ramas. |
| 1 · MVP MySQL | ✅ v0.1.0 publicada | CI, aceptación, imagen multi-arquitectura firmada y escaneada. Marca aplazada sin bloquear releases. |
| 2 · Conectividad segura | 🟢 Implementada; integración pendiente | Cancelación con KILL QUERY, sesiones revocables, certificados MySQL y mTLS nativo; pruebas locales y CI configurada. |
| 3 · Productividad | 🟡 Iniciada | Pestañas, paleta y archivos `.sql` listos. El resto pendiente. |
| 4 · Equipos | ⬜ Base preparada | Modo `team`, aislamiento por usuario y exportación de perfiles. Falta lo esencial: usuarios, roles, SSO. |
| 5+ · Expansión | ⬜ Pendiente | Solo MySQL/MariaDB; el contrato de drivers aún no existe en el código. |

---

## 2. Visión y principios

| Principio | En la práctica |
|---|---|
| **Cero fricción** | `docker run` y listo. Un binario, sin PHP ni Apache, sin configuración obligatoria. |
| **Seguro por defecto** | TLS verificado, credenciales cifradas, sin `LOAD DATA LOCAL`, lista blanca de destinos, auditoría. Lo inseguro se activa explícitamente. |
| **Personal primero, equipo después** | Uso individual sin fricción (2FA opcional); requisitos estrictos declarados con `DBYSON_MODE=team`. |
| **Nunca romper producción por accidente** | Color por entorno, solo lectura reforzado en el servidor, confirmación escrita de sentencias que cambian datos. |
| **Keyboard-first** | Paleta `Ctrl+K`, `Ctrl+Enter`, `Ctrl+Alt+N`, pestañas con teclado. |
| **Crecer sin reescribir** | Contrato de drivers antes del segundo motor; vistas del frontend separadas por archivo. |

**No-objetivos:** reemplazar herramientas de BI, ser un ORM, administrar clústeres, migraciones de esquema versionadas.

---

## 3. Stack: lo planeado frente a lo real

| Capa | Plan original | Hoy | Decisión |
|---|---|---|---|
| Backend | Go | **Go 1.26**, binario estático | ✅ Se mantiene ([ADR 0001](adr/0001-binario-go-con-frontend-embebido.md)). |
| Frontend | React + TS + Vite | **React 19 + TypeScript + Vite**, formato con Prettier | ✅ |
| Estilos | Tailwind + shadcn/ui | CSS propio con *tokens* y 5 temas | Se mantiene: cumple y no añade dependencias. |
| Editor SQL | CodeMirror 6 | CodeMirror 6 | ✅ Resaltado y autocompletado; pendiente de validación funcional completa. |
| Grilla | TanStack Table con virtualización | Tabla HTML paginada (≤500 filas) | Suficiente con paginación; virtualizar solo si se suben los límites. |
| Datos en el cliente | TanStack Query | Caché propia de metadatos ([ADR 0005](adr/0005-cache-de-metadatos-en-el-frontend.md)) | Se mantiene mientras sea pequeña; revisar si crece. |
| Persistencia | SQLite embebido | `modernc.org/sqlite` con migraciones versionadas | ✅ |
| Streaming / cancelación | WebSocket | HTTP + `AbortController` (cancela en el driver) | WebSocket solo si hace falta progreso en vivo (importación). |

---

## 4. Arquitectura actual

```
 Navegador (React)
   │  HTTP(S) · cookie dbyson_session (HttpOnly, SameSite=Strict) · token CSRF · CSP estricta
   ▼
 ┌──────────────────────────────── DByson (binario Go) ────────────────────────────────┐
 │ api/        handlers, CSRF, Origin, cabeceras, auditoría por petición                  │
 │ auth/       Argon2id, sesiones (hash del token), TOTP, códigos de recuperación, límites │
 │ vault/      cifrado de sobre XChaCha20-Poly1305, subclaves derivadas                    │
 │ audit/      registro append-only con cadena HMAC                                        │
 │ policy/     lista blanca de destinos (anti-SSRF), clasificador SQL, solo lectura        │
 │ tunnel/     SSH: clave/PPK/agente/contraseña, bastión, huella fijada                    │
 │ drivers/mysql/  conexión TLS, explorador, consulta limitada, exportación en streaming   │
 │ config/     variables DBYSON_* (con compatibilidad ADMINBASE_*)                         │
 │ store/      SQLite + migraciones (/data/dbyson.db)                                      │
 └──────────────────────────────────────────────────────────────────────────────────────┘
   │ TLS verify_identity por defecto        │ SSH → direct-tcpip (sin puertos locales)
   ▼                                        ▼
 MySQL / MariaDB                        Bastión → servidor → MySQL

 Opcional: dbyson-discovery (sidecar en red interna) ── socket de Docker (solo 2 GET filtrados)
```

**Regla de oro:** las credenciales nunca llegan al navegador; el frontend solo conoce un `connectionId`.

**Frontend:** `web/src/Workbench.tsx` (pestañas) → `Workspace.tsx` (una pestaña) → `web/src/workspace/*` (una vista por archivo) con la caché compartida `catalog.ts`.

### Contrato de drivers (pendiente)

El plan original definía una interfaz `Driver`/`Conn` común a todos los motores. **Aún no existe en el código**: `internal/drivers/mysql` es la única implementación y la API la usa directamente. Debe extraerse **antes** de empezar PostgreSQL (Fase 5), incluyendo un modelo de *capacidades* para que la interfaz no muestre acciones que un motor no soporta.

---

## 5. Seguridad

El estado detallado, con lo implementado, lo parcial y los riesgos aceptados, está en el [modelo de amenazas](threat-model.md). Resumen:

| Área | Hecho | Pendiente |
|---|---|---|
| Acceso a DByson | Argon2id, límites por usuario e IP, sesiones revocables, TOTP, política por modo y mTLS con cookie vinculada al certificado | Passkeys, SSO OIDC |
| Conexiones | TLS con 4 modos, CA propia y certificados de cliente validados en integración; SSH con huella fijada y bastión; lista blanca anti-SSRF | Ampliar la matriz de versiones de motores |
| Errores humanos | Entornos, solo lectura, confirmación escrita de un solo uso, límites de resultado y cancelación de SQL con KILL QUERY | Edición en lote con vista previa transaccional |
| Datos en reposo | Vault con cifrado de sobre; clave maestra por variable, archivo o generada | Comando de rotación de la clave maestra; exigir clave externa en modo `team` |
| Auditoría | Append-only con cadena HMAC verificable, visor en la interfaz, SQL con contraseñas ocultas | Exportación a syslog/SIEM, retención, anclaje externo del último hash |
| Cadena de suministro | Distroless no-root, `govulncheck`, `npm audit`, Trivy, SBOM, procedencia y firma cosign en release | Primera ejecución real; Dependabot/Renovate; CodeQL |

> **Recomendación operativa:** no exponer DByson a Internet sin VPN/zero-trust, mTLS o SSO delante. Por defecto escucha en `127.0.0.1`.

---

## 6. Experiencia de uso

**Hecho:** inicio con tarjetas por entorno, búsqueda y favoritas; asistente de conexión con descubrimiento de MySQL en Docker; explorador de bases y tablas; filtros en borrador que solo se aplican con *Mostrar*; conteo exacto bajo demanda; detalle de registro con JSON; copiar celda/fila; editar, duplicar, insertar y borrar filas; estructura y DDL; plantillas de `ALTER TABLE`; consola SQL; exportación SQL/CSV/JSON en streaming; pestañas agrupadas por tabla; consultas con nombre propio, descarga y apertura de `.sql`; paleta `Ctrl+K`; 5 temas; modo *Sólo ver*; historial de la sesión; caché sin recargas al cambiar de pestaña; indicadores de carga.

**Implementado en la rama de productividad:** CodeMirror y ejecución por sentencia; biblioteca privada cifrada en el servidor; navegación por claves foráneas desde el detalle; EXPLAIN visual; CSV transaccional y scripts revisados en SQL; diseñador DDL; diagrama ER; procesos; cuentas MySQL y passkeys. Uso y límites en [PRODUCTIVIDAD.md](PRODUCTIVIDAD.md).

**Pendiente:** integrar la rama y validar CI remoto, completar las herramientas avanzadas fuera del alcance del diseñador inicial y ofrecer interfaz en inglés (i18n).

---

## 7. Distribución y despliegue

- **Imagen:** `ghcr.io/gaboiv/dbyson`, multi-arquitectura (amd64, arm64), firmada con cosign, con SBOM. Se publica al subir una etiqueta `vX.Y.Z`.
- **Modo personal** (por defecto): una persona, puerto publicado en `127.0.0.1`, 2FA opcional.
  ```bash
  docker run -d --name dbyson -p 127.0.0.1:8080:8080 -v dbyson-data:/data ghcr.io/gaboiv/dbyson:0.1
  ```
- **Modo equipo** (`DBYSON_MODE=team`): 2FA obligatoria para `prod` y SSH. Debe ir detrás de HTTPS/VPN y con `DBYSON_MASTER_KEY_FILE` (Docker secret).
- **Compartir sin servidor:** archivos `.dbyson` con perfiles; credenciales opcionales cifradas con frase (Argon2id + XChaCha20-Poly1305); las claves SSH nunca viajan.
- **Pendiente:** guía de despliegue con proxy HTTPS (Caddy/Traefik) y, más adelante, Helm.

---

## 8. Hoja de ruta

Estimaciones para una persona a tiempo parcial (~15 h/semana).

### Fase 0 — Fundaciones · ✅ cerrada
- [x] Licencia Apache-2.0, ADRs, modelo de amenazas, Prettier.
- [x] Go + React embebido; imagen distroless no-root; amd64 + arm64.
- [x] CI: vet, tests con `-race`, `govulncheck`, formato, `npm audit`, integración MySQL 8.4 / MariaDB 11.4, build multi-arquitectura, Trivy. Release firmado.
- [x] Nombre unificado DByson ([ADR 0006](adr/0006-renombrado-a-dbyson.md)).
- [x] **Primera ejecución del CI en GitHub y corregir lo que falle.** CI correcto en `main` (`bb400e4`, ejecución 39907508409, segundo intento).

### Fase 1 — MVP MySQL · ✅ v0.1.0 publicada
- [x] Setup y login, vault, perfiles de conexión con TLS.
- [x] Explorador, grilla con filtros, orden, paginación y edición de filas.
- [x] Consola SQL con límites, confirmaciones y solo lectura reforzado.
- [x] Exportación en streaming.
- [x] **Criterios técnicos de salida de v0.1.0**:
  - CI en verde en `main`.
  - Prueba E2E automatizada (Playwright) del flujo: setup → crear y probar conexión → abrir tabla → editar fila → consulta SQL → exportar.
  - Recorrido de aceptación automatizado y revisión visual de Modificar, Nuevo registro y Exportar.
  - Imagen publicada y firmada; verificación de la firma documentada.
  - GitHub, GHCR, dominio y logo comprobados. Marca aplazada por decisión del usuario; no bloquea releases.
- **Entregable:** `v0.1.0`.

### Fase 2 — Conectividad segura · 🟢 implementada, integración pendiente
- [x] Túnel SSH (OpenSSH/PPK, agente, contraseña, bastión, huella fijada).
- [x] 2FA TOTP con códigos de recuperación; modos `personal`/`team` ([ADR 0004](adr/0004-modos-personal-y-equipo.md)).
- [x] Auditoría v1 ([ADR 0003](adr/0003-auditoria-append-only-encadenada.md)).
- [x] Cancelar una consulta en curso desde la interfaz (`KILL QUERY`), con aislamiento por sesión y detención del script.
- [x] Panel de sesiones activas propias con revocación y cancelación de sus consultas.
- [x] Certificados de cliente MySQL validados en integración real; job de seguridad añadido a CI.
- [x] mTLS nativo para todo el listener, cookie vinculada al certificado y guía de emisión con OpenSSL ([ADR 0007](adr/0007-conectividad-segura.md)).
- [ ] Integrar la rama, confirmar la CI remota y configurar los certificados reales del despliegue.
- **Entregable:** `v0.2.0`, apto para producción remota en uso personal.

### Fase 3 — Productividad · 🟡 implementada en rama; pendiente de integración
- [x] Pestañas agrupadas por tabla, paleta de comandos, consultas con nombre y archivos `.sql`.
- [x] Editor SQL con CodeMirror 6: resaltado, autocompletado por esquema, ejecutar selección.
- [x] Consultas privadas cifradas en el servidor (guardado explícito), con snippets del editor.
- [x] Navegación por claves foráneas, incluidas claves compuestas y bases distintas.
- [x] EXPLAIN visual con estimaciones y advertencias de acceso completo.
- [x] Importación CSV transaccional y SQL revisado en el editor, con estado por operación/sentencia.
- [x] Editor visual de estructura con vista previa del DDL (columnas, tipo, índice simple/único y renombrado).
- [x] Lista de procesos con KILL QUERY; cuentas y permisos acotados de MySQL.
- [x] Diagrama ER de relaciones declaradas, con filtro y lista accesible.
- [x] Passkeys/WebAuthn con verificación de usuario y origen explícito.
- Detalle de uso y límites: [PRODUCTIVIDAD.md](PRODUCTIVIDAD.md). Cambios en `codex/productivity`, pendientes de commit/PR y CI remoto. Activar passkeys en una instalación requiere configurar su origen y registrar dispositivos.
- **Entregable:** `v0.5.0`, el equipo deja Adminer/phpMyAdmin para el trabajo diario.

### Fase 4 — Equipos · ⬜ base preparada
- [x] Modo `team` y aislamiento de perfiles por usuario.
- [x] Exportación/importación de perfiles `.dbyson`.
- [ ] **Gestión de usuarios** (hoy solo existe el administrador inicial).
- [ ] Roles y permisos por conexión y entorno.
- [ ] Conexiones compartidas con administración central.
- [ ] SSO OIDC (Google, Microsoft Entra ID, Keycloak).
- [ ] Auditoría: filtros por usuario, exportación, retención, envío a SIEM.
- [ ] Exigir clave maestra externa en modo `team`; comando de rotación.
- [ ] Guía de despliegue con HTTPS y VPN/zero-trust.
- [ ] **Pentest externo** antes de la v1.0.
- [ ] Retirar la compatibilidad con `ADMINBASE_*`.
- **Entregable:** `v1.0.0`.

### Fase 5+ — Expansión · ⬜
- [ ] Extraer el contrato de drivers con capacidades por motor.
- [ ] PostgreSQL → SQLite → SQL Server.
- [ ] App de escritorio (Wails).
- [ ] Asistente IA opcional (solo esquema, opt-in explícito).
- [ ] Comparación de esquemas, respaldos programados, API pública.

---

## 9. Calidad y pruebas

| Tipo | Hoy | Falta |
|---|---|---|
| Unitarias Go | Auth, TOTP (vectores RFC 6238), vault, auditoría, migraciones, política SQL y de hosts, API, túneles, configuración | — |
| Integración | MySQL 8.4, MariaDB 11.4, SSH con bastión, certificados de cliente, KILL QUERY y acceso mTLS real | MySQL 8.0 y 9.x, MariaDB 10.11 |
| Frontend | Tipado estricto y formato en CI | Pruebas unitarias (Vitest) de `catalog.ts` y `sqlFile.ts` |
| Extremo a extremo | Playwright en CI; 20 casos locales de aceptación, interfaz y seguridad | Ampliar cobertura junto a nuevas funciones |
| Fuzzing | — | Clasificador SQL y lector de archivos `.dbyson` |
| Rendimiento | — | Abrir una tabla de 1M filas < 1 s; exportar 1 GB con < 100 MB de RAM |

---

## 10. Riesgos

| Riesgo | Mitigación |
|---|---|
| Alcance infinito | Criterios de salida por versión; lo no listado va al backlog. |
| Una herramienta de BD comprometida da acceso a todas las BD | Seguridad en cada fase, localhost por defecto, modelo de amenazas vivo, pentest antes de v1.0. |
| Publicar en modo `personal` con el puerto abierto a la red | Aviso en el log y documentación; recomendar `team` + HTTPS. |
| `master.key` junto a los datos | Documentado; exigir clave externa en modo `team`. |
| Diferencias entre versiones de MySQL/MariaDB | Ampliar la matriz de integración. |
| Resultados enormes | Límites de filas y bytes, exportación en streaming. |
| Frontend difícil de mantener | Vistas separadas, Prettier en CI; añadir pruebas unitarias del frontend. |
| Nombre "DByson" ya en uso | GitHub/GHCR/dominio revisados. Marca aplazada sin bloquear releases. |
| Cambios nuevos sin CI remota | Integrar por PR y confirmar los jobs, incluido secure-connectivity. |

---

## 11. Métricas de éxito

| Métrica | Objetivo | Hoy |
|---|---|---|
| De `docker run` a la primera consulta | < 60 s | Cumple en local (sin medición formal). |
| Tamaño de imagen | < 40 MB | ~38 MB (el logo de 1,7 MB está pendiente de sustituir). |
| Vulnerabilidades críticas/altas en cada release | 0 | Pendiente de la primera ejecución de Trivy en CI. |
| Recargas al cambiar de pestaña o vista | 0 | 0 (medido contando peticiones). |
| Adopción | El equipo deja Adminer/phpMyAdmin tras la v0.5 | — |

---

## 12. Cambios respecto al plan original

| Plan original | Cambio | Motivo |
|---|---|---|
| Nombre AdminBase | **DByson** en todo, salvo etiquetas criptográficas | Identidad del producto; renombrar antes de publicar es más barato ([ADR 0006](adr/0006-renombrado-a-dbyson.md)). |
| 2FA obligatoria para producción | Depende de `DBYSON_MODE` | En uso personal no protege y añade fricción ([ADR 0004](adr/0004-modos-personal-y-equipo.md)). |
| Archivo `.abx` cifrado con `age` | Archivo `.dbyson` con Argon2id + XChaCha20-Poly1305 | Sin dependencia externa; claves SSH siempre fuera del paquete. |
| TanStack Query, Tailwind, CodeMirror | CSS propio y caché propia; CodeMirror aún pendiente | Menos dependencias; adoptar CodeMirror cuando llegue el editor avanzado. |
| WebSocket para streaming y cancelación | HTTP con cancelación vía `AbortController` | Suficiente hasta tener tareas largas con progreso. |
| Contrato de drivers desde el día 1 | Implementación directa de MySQL | Se extraerá antes del segundo motor. |
| Docker Hub y GHCR | GHCR primero | Integrado con GitHub Actions y la firma keyless. |
| Grilla virtualizada | Paginación con límites de filas y bytes | Evita cargar millones de filas en el navegador. |
| — | Descubrimiento de MySQL en Docker con sidecar aislado | Facilita conectar a bases locales en contenedores. |
| — | Caché compartida de metadatos | Eliminar recargas entre pestañas ([ADR 0005](adr/0005-cache-de-metadatos-en-el-frontend.md)). |

---

## 13. Próximos pasos inmediatos

1. Integrar las mejoras de interfaz y la Fase 2, y confirmar su CI remota.
2. Configurar los certificados reales para activar mTLS siguiendo la guía de despliegue.
3. Publicar la siguiente versión cuando se apruebe la integración; no mover la etiqueta v0.1.0 existente.
4. Integrar la Fase 3 desde `codex/productivity`, revisar sus límites documentados y después iniciar la gestión de usuarios de DByson de la Fase 4.
