# DByson — Modo equipo

Guía operativa del modo `team` (`DBYSON_MODE=team`): varias personas, DByson
accesible por red, requisitos estrictos. El modo `personal` (por defecto) no
cambia: una persona en su máquina, 2FA opcional.

## Usuarios y roles

Solo la administración crea cuentas, desde *Equipo* (botón visible para
`admin`) o la API. La primera cuenta sigue creándose con `/api/setup`.

| Capacidad | admin | member | viewer |
|---|---|---|---|
| Crear usuarios, cambiar roles, deshabilitar | ✅ | ❌ | ❌ |
| Crear conexiones privadas | ✅ | ✅ | ❌ |
| Compartir conexiones / dar permisos | ✅ | ❌ | ❌ |
| Usar conexiones propias | ✅ | ✅ | ✅ (solo lectura) |
| Usar conexiones compartidas con permiso | ✅ (todas) | ✅ (`use`) | ✅ (`use`, nunca `prod`) |
| Editar conexiones compartidas | ✅ | ✅ (`manage`) | ❌ |
| Ver toda la auditoría / exportar / purgar | ✅ | propia | propia |

Reglas de seguridad:

- La última administración activa no se puede degradar, deshabilitar ni
  eliminar. Siempre queda alguien que pueda administrar.
- Deshabilitar cierra las sesiones de la cuenta al momento; también se cierran
  al cambiar su contraseña.
- Las cuentas deshabilitadas no entran por contraseña, 2FA, passkey ni SSO.
- `viewer` trabaja siempre en solo lectura: el servidor fuerza `read_only` en
  cada conexión que abre, así que la consola rechaza sus sentencias de
  escritura igual que en una conexión de solo lectura.

## Conexiones compartidas

Una conexión tiene un dueño. Solo `admin` puede marcarla como compartida
(casilla *Compartida con el equipo* al crear o editar). Después concede
acceso por persona:

- `use`: abrir, consultar, exportar.
- `manage`: lo anterior más editar el perfil y gestionar sus permisos.
  Borrar el perfil sigue reservado a su dueño y a la administración.

```bash
# conceder uso
curl -b cookie -H "X-CSRF-Token: $CSRF" -X POST /api/connections/<id>/grants \
  -d '{"userId": 7, "level": "use"}'
# ver permisos, revocar uno
curl -b cookie /api/connections/<id>/grants
curl -b cookie -H "X-CSRF-Token: $CSRF" -X DELETE /api/connections/<id>/grants/7
```

Detalles que importan:

- Sin permiso, un ID ajeno responde 404 igual que uno inexistente: no se puede
  enumerar lo ajeno.
- Dejar de compartir revoca todos los permisos de golpe.
- Exportar el perfil necesita `use`; exportar **con secretos** necesita
  `manage` (más 2FA en `prod`/SSH, como siempre).
- Los perfiles importados desde `.dbyson` entran siempre como privados.

Gestión de usuarios (`admin`):

```bash
curl -b cookie /api/users
curl -b cookie -H "X-CSRF-Token: $CSRF" -X POST /api/users \
  -d '{"username": "ana", "password": "<12+ caracteres>", "role": "member"}'
curl -b cookie -H "X-CSRF-Token: $CSRF" -X PUT /api/users/7 \
  -d '{"role": "viewer"}'            # o {"disabled": true}, {"password": "..."}
curl -b cookie -H "X-CSRF-Token: $CSRF" -X DELETE /api/users/7
```

## SSO (OIDC)

Un proveedor OIDC genérico con flujo de código de autorización y tokens RS256
verificados con JWKS de la biblioteca estándar, sin dependencias nuevas.
Funciona con Google, Microsoft Entra ID y Keycloak (cualquier proveedor OIDC
con `authorization_code` + RS256 sirve).

```bash
DBYSON_OIDC_ISSUER=https://accounts.google.com        # o https://login.microsoftonline.com/<tenant>/v2.0
DBYSON_OIDC_CLIENT_ID=<id>                            # o https://keycloak/realms/<realm>
DBYSON_OIDC_CLIENT_SECRET=<secreto>
DBYSON_OIDC_REDIRECT_URL=https://dbyson.example.com/api/auth/oidc/callback
DBYSON_OIDC_AUTO_PROVISION=true                       # crear cuenta member al primer login
DBYSON_OIDC_NAME=Google                               # etiqueta del botón (opcional)
```

- La URL de retorno debe estar registrada tal cual en el proveedor.
- Con auto-aprovisionamiento, el primer inicio crea una cuenta `member`; si el
  email verificado coincide con una cuenta local existente, se vincula a ella
  (conserva conexiones y permisos). Sin auto-aprovisionamiento, solo entran
  identidades vinculadas por la administración.
- La sesión SSO nace sin 2FA verificada: en `prod`/SSH se aplica la política
  de 2FA igual que al resto.
- Límites conocidos: solo RS256 (el defecto de los tres proveedores); un
  proveedor que negocie otro algoritmo falla cerrado.

## Auditoría exportable y retención

Filtros nuevos en `GET /api/audit`: `username`, `outcome`, `since`, `until`
(segundos unix), además de los existentes. Exportación para sistemas externos:

```bash
curl -b cookie /api/audit/export?format=jsonl > auditoria.ndjson  # o ?format=csv
```

Cada miembro solo exporta su rastro; la administración, todo. El detalle sigue
saneado (sin secretos). Para un SIEM, ingerir el JSONL en orden cronológico.

Retención: `DBYSON_AUDIT_RETENTION_DAYS=N` purga al arrancar lo anterior a N
días; `POST /api/audit/purge {"olderThanDays": 90}` lo hace bajo demanda
(solo `admin`, validado 1–3650). Cada purga deja una entrada `audit.purged`
con el conteo y el hash de la última entrada eliminada (`anchor`): un SIEM con
una exportación anterior puede comprobar continuidad. Exporte antes de purgar:
la cola truncada después de una purga sigue sin detectarse, como ya documenta
el README.

## Clave maestra en modo equipo

En modo `team` el servidor **no arranca** con `master.key` dentro del volumen
de datos: exige `DBYSON_MASTER_KEY` o `DBYSON_MASTER_KEY_FILE` fuera de
`/data` (Docker secret). La huella de la clave activa queda en el log.

Rotación (con el servidor detenido):

```bash
docker exec dbyson /dbyson rotate-key --data-dir /data --new-key-file /run/secrets/dbyson-master.key
# o con clave generada aparte:
openssl rand -base64 32 > nueva.key
dbyson rotate-key --data-dir ./data --new-key "$(cat nueva.key)"
```

Re-cifra conexiones, consultas guardadas y semillas TOTP, re-firma la cadena
de auditoría (verificada antes y después; una cadena rota aborta la rotación)
y deja una entrada `vault.key_rotated` con las huellas. Después, apuntar la
variable o el secret a la nueva clave y arrancar.

## Lista de despliegue en equipo

1. `DBYSON_MODE=team`, puerto detrás de HTTPS/VPN/zero-trust (ver
   [conectividad segura](CONECTIVIDAD_SEGURA.md)).
2. Clave maestra externa (secret, fuera del volumen).
3. Crear cuentas y conceder `use`/`manage` sobre conexiones compartidas.
4. SSO configurado si el equipo lo usa; 2FA activa en cuentas con `prod`/SSH.
5. `DBYSON_AUDIT_RETENTION_DAYS` y exportación periódica al SIEM/archivo.

## Pendiente de esta fase

- Interfaz de permisos por conexión (hoy por API `grants`).
- Pentest externo antes de la v1.0: bloqueador que requiere contratación
  externa, ver [PENTEST.md](PENTEST.md).
