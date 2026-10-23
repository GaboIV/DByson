# DByson — Plan Maestro

> Un IDE web de bases de datos: **la sencillez de Adminer, la potencia de phpMyAdmin, una UX moderna y seguridad por defecto.**
> Se distribuye como una sola imagen Docker que cualquier persona levanta con un comando.

**Revisión:** 23 de octubre de 2026 · **Nombre:** DByson (antes AdminBase; ver [ADR 0006](adr/0006-renombrado-a-dbyson.md)) · **Licencia:** Apache-2.0

**Documentos relacionados:** [modelo de amenazas](threat-model.md) · [decisiones de arquitectura](adr/README.md) · detalle de lo implementado en [seguridad del MVP](MVP_SEGURIDAD.md), [experiencia de consulta](EXPERIENCIA_CONSULTA.md), [interfaz y preferencias](INTERFAZ_Y_PREFERENCIAS.md) y [SSH y espacios de trabajo](SSH_Y_ESPACIOS.md). La [evaluación del 21 de octubre](EVALUACION_2026-10-21.md) es histórica.

---

## 1. Dónde estamos

DByson ya es una herramienta usable a diario para **una persona con MySQL/MariaDB**, local o remoto por SSH. Lo que falta para la meta se agrupa en tres bloques:

1. **Publicar honestamente la v0.1.0:** el CI ya pasó en `main`; falta integrar y validar el flujo E2E y completar el recorrido manual ([validación de v0.1.0](VALIDACION_V0.1.0.md)).
2. **Productividad de un cliente maduro:** editor SQL con autocompletado, consultas guardadas, EXPLAIN, navegación por claves foráneas, importación.
3. **Modo equipo:** hoy solo existe el administrador inicial; no hay gestión de usuarios, roles, conexiones compartidas ni SSO.

| Fase | Estado | Resumen |
|---|---|---|
| 0 · Fundaciones | ✅ Cerrada | CI correcto en `main` tras integrar las ramas. |
| 1 · MVP MySQL | 🟡 Funcional, sin release | Funciones completas y probadas a mano y con integración local. Falta cerrar criterios de salida y publicar v0.1.0. |
| 2 · Conectividad segura | 🟡 Mayoría hecha | SSH, 2FA y auditoría listos. Faltan mTLS, panel de sesiones, validar certificados de cliente MySQL y cancelar consultas desde la interfaz. |
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
| Acceso a DByson | Argon2id, límites por usuario e IP, sesiones con caducidad, TOTP con códigos de recuperación, política por modo | mTLS de acceso, passkeys, SSO OIDC, panel de sesiones activas |
| Conexiones | TLS con 4 modos y CA propia, SSH con huella fijada y bastión, `LOAD DATA LOCAL` deshabilitado, lista blanca anti-SSRF | Validar en integración los certificados de cliente MySQL |
| Errores humanos | Color por entorno, solo lectura en cada conexión física, confirmación escrita con desafío de un solo uso, límites de resultado | Botón para cancelar consultas en curso (`KILL QUERY`), edición en lote con vista previa transaccional |
| Datos en reposo | Vault con cifrado de sobre; clave maestra por variable, archivo o generada | Comando de rotación de la clave maestra; exigir clave externa en modo `team` |
| Auditoría | Append-only con cadena HMAC verificable, visor en la interfaz, SQL con contraseñas ocultas | Exportación a syslog/SIEM, retención, anclaje externo del último hash |
| Cadena de suministro | Distroless no-root, `govulncheck`, `npm audit`, Trivy, SBOM, procedencia y firma cosign en release | Primera ejecución real; Dependabot/Renovate; CodeQL |

> **Recomendación operativa:** no exponer DByson a Internet sin VPN/zero-trust, mTLS o SSO delante. Por defecto escucha en `127.0.0.1`.

---

## 6. Experiencia de uso

**Hecho:** inicio con tarjetas por entorno, búsqueda y favoritas; asistente de conexión con descubrimiento de MySQL en Docker; explorador de bases y tablas; filtros en borrador que solo se aplican con *Mostrar*; conteo exacto bajo demanda; detalle de registro con JSON; copiar celda/fila; editar, duplicar, insertar y borrar filas; estructura y DDL; plantillas de `ALTER TABLE`; consola SQL; exportación SQL/CSV/JSON en streaming; pestañas agrupadas por tabla; consultas con nombre propio, descarga y apertura de `.sql`; paleta `Ctrl+K`; 5 temas; modo *Sólo ver*; historial de la sesión; caché sin recargas al cambiar de pestaña; indicadores de carga.

**Pendiente, por valor para el uso diario:**
1. Editor SQL con resaltado y autocompletado por esquema (CodeMirror 6), ejecutar la selección, varias sentencias.
2. Consultas guardadas en el servidor (opt-in; hoy solo como archivo `.sql`).
3. Navegación por claves foráneas (clic en un valor → registro relacionado).
4. EXPLAIN visual con advertencias.
5. Importación CSV/SQL con progreso.
6. Editor visual de estructura con vista previa del DDL.
7. Diagrama ER.
8. Interfaz en inglés (i18n).

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

### Fase 1 — MVP MySQL · 🟡 funcional, pendiente de release
- [x] Setup y login, vault, perfiles de conexión con TLS.
- [x] Explorador, grilla con filtros, orden, paginación y edición de filas.
- [x] Consola SQL con límites, confirmaciones y solo lectura reforzado.
- [x] Exportación en streaming.
- [ ] **Criterios de salida de v0.1.0** (todos obligatorios):
  - CI en verde en `main`.
  - Prueba E2E automatizada (Playwright) del flujo: setup → crear y probar conexión → abrir tabla → editar fila → consulta SQL → exportar.
  - Recorrido manual de Modificar, Nuevo registro y Exportar tras la división de vistas.
  - Imagen publicada y firmada; verificación de la firma documentada.
  - Verificar disponibilidad del nombre (GitHub, GHCR, dominio, marca).
- **Entregable:** `v0.1.0`.

### Fase 2 — Conectividad segura · 🟡 mayoría hecha
- [x] Túnel SSH (OpenSSH/PPK, agente, contraseña, bastión, huella fijada).
- [x] 2FA TOTP con códigos de recuperación; modos `personal`/`team` ([ADR 0004](adr/0004-modos-personal-y-equipo.md)).
- [x] Auditoría v1 ([ADR 0003](adr/0003-auditoria-append-only-encadenada.md)).
- [ ] Cancelar una consulta en curso desde la interfaz (`KILL QUERY`).
- [ ] Panel de sesiones activas con revocación.
- [ ] Validar certificados de cliente MySQL en la matriz de integración.
- [ ] mTLS para acceder a DByson + comando para emitir certificados de cliente.
- **Entregable:** `v0.2.0`, apto para producción remota en uso personal.

### Fase 3 — Productividad · 🟡 iniciada
- [x] Pestañas agrupadas por tabla, paleta de comandos, consultas con nombre y archivos `.sql`.
- [ ] Editor SQL con CodeMirror 6: resaltado, autocompletado por esquema, ejecutar selección.
- [ ] Consultas guardadas en el servidor (opt-in) y snippets.
- [ ] Navegación por claves foráneas.
- [ ] EXPLAIN visual.
- [ ] Importación CSV/SQL con progreso.
- [ ] Editor visual de estructura con vista previa del DDL.
- [ ] Lista de procesos con *kill*; usuarios y permisos de MySQL (`GRANT`).
- [ ] Diagrama ER.
- [ ] Passkeys/WebAuthn.
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
| Integración | MySQL 8.4 y MariaDB 11.4 (local y en CI), SSH real con bastión | MySQL 8.0 y 9.x, MariaDB 10.11; certificados de cliente |
| Frontend | Tipado estricto y formato en CI | Pruebas unitarias (Vitest) de `catalog.ts` y `sqlFile.ts` |
| Extremo a extremo | Recorridos manuales en navegador | **Playwright en CI** (criterio de v0.1.0) |
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
| Nombre "DByson" ya en uso | Verificar GitHub, GHCR, dominio y marca antes de v0.1.0. |
| El CI nunca se ejecutó | Es el primer paso del próximo hito. |

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

1. **Push de la rama y primera ejecución del CI;** corregir lo que falle.
2. **Integrar en `main`** las ramas `feat/2fa-auditoria`, `feat/workspace-fase0` y el renombrado.
3. **Prueba E2E con Playwright** del flujo principal y recorrido manual de Modificar, Nuevo registro y Exportar.
4. **Verificar el nombre** y publicar `v0.1.0` firmada.
5. Fase 2 restante: cancelar consultas desde la interfaz y panel de sesiones.
6. Fase 3: CodeMirror 6 con autocompletado y consultas guardadas.
