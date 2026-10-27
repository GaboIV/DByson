# DByson — Modelo de amenazas

> Versión 1 · 22 de octubre de 2026 · Método STRIDE.
> Revisar en cada fase del plan y antes de cada release. Los estados reflejan el código actual, no intenciones.

## 1. Qué protegemos

| Activo | Por qué importa |
|---|---|
| Credenciales de las bases de datos (contraseñas, certificados, claves SSH) | Dan acceso directo a datos de producción. |
| Datos que pasan por DByson (filas, exportaciones, resultados SQL) | Pueden ser personales, financieros o secretos de negocio. |
| Clave maestra del vault | Descifra todas las credenciales y firma la auditoría. |
| Sesiones de usuario y factores (contraseña, TOTP, códigos de recuperación) | Equivalen a la identidad del usuario dentro de DByson. |
| Registro de auditoría | Evidencia de quién hizo qué; su valor depende de que no se pueda alterar. |

## 2. Componentes y fronteras de confianza

```
 Navegador ──(1)── DByson (Go) ──(2)── MySQL/MariaDB
                     │   └────(3)── servidor SSH / bastión ── MySQL
                     ├── (4) /data: SQLite + master.key
                     └── (5) sidecar de descubrimiento ── socket de Docker
```

1. **Navegador ↔ DByson:** HTTP(S). Único punto de entrada de usuarios.
2. **DByson ↔ base de datos:** TCP con TLS configurable.
3. **DByson ↔ SSH:** túnel autenticado por huella fijada.
4. **Disco local:** volumen de datos y clave maestra.
5. **Descubrimiento Docker (opcional):** contenedor separado con acceso de lectura al socket.

**Actores:** usuario legítimo; atacante en la red local; sitio web malicioso que el usuario visita (CSRF/clickjacking); servidor de base de datos malicioso o comprometido; atacante con acceso al disco o a una copia de seguridad; otro usuario de la misma instancia (modo equipo).

**Supuesto base:** quien controla la máquina o el contenedor donde corre DByson (root, malware) controla DByson. Este modelo no intenta defenderse de eso.

## 3. Amenazas y mitigaciones

Estado: ✅ implementado · 🟡 parcial · ⬜ pendiente.

### S — Suplantación

| Amenaza | Mitigación | Estado |
|---|---|---|
| Adivinar o rociar contraseñas | Argon2id (64 MiB, t=3), bloqueo por usuario+IP (5/15 min) y por IP (30/15 min), respuesta idéntica para usuario inexistente | ✅ |
| Contraseña filtrada | TOTP con códigos de recuperación de un solo uso; obligatorio para `prod`/SSH en modo `team` | ✅ |
| Reutilizar un código TOTP interceptado | Se guarda el último paso aceptado; cada código vale una vez | ✅ |
| Robo de cookie de sesión | `HttpOnly`, `SameSite=Strict`, `Secure` bajo HTTPS; solo el hash del token en SQLite; expiración por inactividad (2 h) y absoluta (12 h) | ✅ |
| Servidor MySQL suplantado (MITM) | TLS `verify_identity` por defecto, CA propia, certificado de cliente | ✅ |
| Servidor SSH suplantado | Huella SHA-256 fijada; un cambio bloquea la conexión | ✅ |
| Acceso sin certificado de cliente a DByson (mTLS) | Listener TLS nativo con CA de clientes (`DBYSON_TLS_CLIENT_CA_FILE`) | ✅ |
| SSO corporativo (OIDC) | Flujo de código con PKCE, `state` ligado al navegador por cookie, `nonce` obligatorio, RS256 contra JWKS (≥2048 bits), `iss`/`aud`/`exp` verificados | ✅ |
| Cuentas ajenas entrando por SSO | `DBYSON_OIDC_ALLOWED_DOMAINS` (obligatorio para auto-aprovisionar), solo emails verificados, vínculo por email solo con coincidencia única | ✅ |
| SSO como atajo para saltarse la 2FA local | Tras el SSO se pide el código si la cuenta tiene 2FA, salvo MFA declarada por el proveedor (`amr`) o `DBYSON_OIDC_TRUST_MFA` | ✅ |

### T — Manipulación

| Amenaza | Mitigación | Estado |
|---|---|---|
| CSRF desde otra web | Token CSRF por sesión + comprobación de `Origin` + `SameSite=Strict` | ✅ |
| Clickjacking | `X-Frame-Options: DENY`, `frame-ancestors 'none'` | ✅ |
| Inyección SQL en rutas estructuradas (filtros, tablas, filas) | Identificadores escapados, valores parametrizados, `customWhere` rechazado | ✅ |
| Editar o borrar credenciales cifradas en disco | XChaCha20-Poly1305 con el ID del registro como dato asociado | ✅ |
| Alterar la auditoría | Triggers append-only + cadena HMAC con subclave de la clave maestra; botón de verificación | ✅ |
| Truncar las últimas entradas de auditoría | Anclaje externo del último hash / envío a syslog | ⬜ |

### R — Repudio

| Amenaza | Mitigación | Estado |
|---|---|---|
| Negar haber ejecutado un cambio | Auditoría de accesos, cambios, SQL (contraseñas ocultas) y exportaciones, con usuario e IP | ✅ |
| La auditoría vive en la misma máquina | Exportación a un sistema externo (SIEM/syslog) | ⬜ Fase 4 |
| Fallo al escribir la auditoría no bloquea la operación | Se registra en el log del servidor | 🟡 |

### I — Divulgación de información

| Amenaza | Mitigación | Estado |
|---|---|---|
| Credenciales en respuestas de la API | La API solo expone indicadores (`hasPassword`…), nunca secretos | ✅ |
| Robo del volumen o de una copia de seguridad | Credenciales cifradas; **pero** si `master.key` está en el mismo volumen, el cifrado no protege esa copia. Usar `DBYSON_MASTER_KEY_FILE` (Docker secret) | 🟡 |
| Servidor MySQL malicioso lee archivos locales (`LOAD DATA LOCAL`) | Deshabilitado en el driver | ✅ |
| Contraseñas en el SQL auditado | Redacción de `IDENTIFIED BY`, `PASSWORD(...)`, etc. | ✅ |
| Usuario ve conexiones de otro | Cada perfil filtrado por propietario; 404 para ajenos | ✅ |
| Metadatos de otra sesión en el navegador | La caché del frontend se vacía al cerrar sesión | ✅ |
| Exportar credenciales a un archivo | Cifrado con frase (Argon2id + XChaCha20); exige 2FA en `prod`/SSH en modo `team` | ✅ |

### D — Denegación de servicio

| Amenaza | Mitigación | Estado |
|---|---|---|
| Resultados enormes agotan memoria | Límite de 1.000 filas y 8 MiB en la consola; exportación por streaming | ✅ |
| Consultas que no terminan | Timeout de 60 s en consola | 🟡 (falta cancelar con `KILL QUERY`) |
| Cuerpos de petición gigantes | Límite de 1 MiB por petición JSON | ✅ |
| Hash Argon2id manipulado para consumir memoria | Parámetros acotados al verificar | ✅ |
| Muchos retos de 2FA o confirmaciones pendientes | Límites en memoria (1.000 / 1.024) con caducidad | ✅ |

### E — Elevación de privilegios

| Amenaza | Mitigación | Estado |
|---|---|---|
| Usar DByson como pivote SSRF hacia la red interna o el endpoint de metadatos de la nube | Link-local siempre bloqueado; lista blanca `DBYSON_ALLOWED_HOSTS`; resolución DNS una sola vez (anti rebinding) | ✅ |
| Escribir en una conexión de solo lectura | `SET SESSION TRANSACTION READ ONLY` en cada conexión física + lista de sentencias permitidas | ✅ (la barrera real es un usuario MySQL sin permisos de escritura) |
| Acceso al socket de Docker (= root en el host) | Sidecar aislado en red interna, solo dos GET de lectura con campos filtrados; opcional | ✅ |
| Contenedor comprometido escala al host | Imagen distroless, usuario no-root, sin shell; recomendado `read_only` y `cap_drop: ALL` | ✅ |
| Roles y permisos por conexión en modo equipo | Roles `admin`/`member`/`viewer` y permisos `use`/`manage` aplicados en el servidor; IDs ajenos responden 404 | ✅ |
| Un permiso `use`/`manage` usado para llevarse credenciales guardadas | Probar borradores con lo guardado exige `manage`; para quien no es dueño ni `admin`, las credenciales guardadas solo viajan a su destino original y no puede rebajar entorno, solo lectura ni TLS | ✅ |
| `viewer` abriendo `prod` | Prohibido siempre, también en conexiones propias | ✅ |

## 4. Riesgos residuales aceptados (v0.1)

1. **`master.key` junto a los datos** en el modo por defecto: cómodo para uso personal, débil frente al robo del volumen. Documentado; en `team` debe usarse un secreto externo.
2. **HTTP sin TLS** cuando se publica solo en `127.0.0.1`: aceptable en local; en red debe ir detrás de un proxy con HTTPS.
3. **El clasificador de SQL no es un parser completo.** Es una ayuda contra errores humanos, no una frontera de seguridad.
4. **Auditoría local** truncable por quien controla el host.

## 5. Antes de la v1.0

- Pentest externo y revisión de este documento.
- Anclaje externo del último hash de auditoría (detectar el truncado del final).
