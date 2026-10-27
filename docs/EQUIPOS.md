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
| Activar o desactivar el uso compartido | ✅ | ❌ | ❌ |
| Dar y retirar permisos en conexiones compartidas | ✅ | ✅ (dueño o `manage`) | ❌ |
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
(casilla *Compartida con el equipo* al crear o editar, o botón **Compartir con
el equipo** en su tarjeta). Desde **Gestionar acceso** se conceden permisos
por persona, se cambian y se retiran con confirmación. Activar el uso
compartido no concede acceso automáticamente a todo el equipo:

- `use`: abrir, consultar, exportar.
- `manage`: lo anterior más editar el perfil y gestionar sus permisos.
  Borrar el perfil sigue reservado a su dueño y a la administración.

La vista **Equipo** muestra cuentas, estado y rol, con búsqueda y filtros.
**Añadir persona** crea una cuenta; las contraseñas se cambian dentro de la
aplicación. La última administración activa y la propia cuenta quedan
protegidas frente a acciones que no admite el servidor.

El panel de acceso también está disponible para miembros propietarios o con
`manage`. Su directorio de destinatarios solo expone identificador, usuario
y rol de cuentas activas elegibles. No permite administrar cuentas ni expone
correos. Solo lectura no se ofrece como destinatario de producción.

**Exportar archivo** descarga copias para otra instalación y no cambia los
permisos del servidor. Incluir credenciales exige gestionar todos los perfiles
seleccionados; importar siempre crea conexiones privadas.

Límites de `manage` (quien no es dueño ni `admin`):

- No puede **rebajar protecciones**: cambiar el entorno, quitar el solo
  lectura ni debilitar el modo TLS.
- Las **credenciales guardadas no viajan a otro destino**: si cambia el host,
  el puerto, el usuario MySQL o el servidor SSH/bastión, debe escribir las
  credenciales; las guardadas solo se usan con su destino original. Así un
  permiso de edición no permite llevarse la contraseña a un servidor propio.
- Probar un borrador del perfil o pedir la huella SSH con lo guardado exige
  `manage`; con `use` solo se prueba el perfil tal cual.

También por API:

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
  La visibilidad y la revocación se confirman juntas. Volver a compartir no
  restaura permisos. `PUT /api/connections/<id>/sharing {"shared": false}`
  permite hacerlo sin modificar destinos ni credenciales (solo administración).
  `GET /api/connections/<id>/grants/users` devuelve el directorio mínimo para
  compartir (propietario, administración o permiso `manage`).
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

Imagen preconstruida, Compose y configuración de Google por instalación:
[Google Workspace](GOOGLE_WORKSPACE.md). Los dominios y credenciales no se
incorporan a la imagen. La opción de deshabilitar contraseñas se aplica en el
servidor y en la pantalla de entrada.

Un proveedor OIDC genérico con flujo de código de autorización y tokens RS256
verificados con JWKS de la biblioteca estándar, sin dependencias nuevas.
Funciona con Google, Microsoft Entra ID y Keycloak (cualquier proveedor OIDC
con `authorization_code` + RS256 sirve).

```bash
DBYSON_OIDC_ISSUER=https://accounts.google.com        # o https://login.microsoftonline.com/<tenant>/v2.0
DBYSON_OIDC_CLIENT_ID=<id>                            # o https://keycloak/realms/<realm>
DBYSON_OIDC_CLIENT_SECRET=<secreto>
DBYSON_OIDC_REDIRECT_URL=https://dbyson.example.com/api/auth/oidc/callback
DBYSON_OIDC_ALLOWED_DOMAINS=empresa.com,filial.com    # solo emails verificados de estos dominios
DBYSON_OIDC_AUTO_PROVISION=true                       # crear cuenta member al primer login (exige ALLOWED_DOMAINS)
DBYSON_OIDC_TRUST_MFA=false                           # true solo si el proveedor exige MFA a todo el mundo
DBYSON_OIDC_NAME=Google                               # etiqueta del botón (opcional)
```

- La URL de retorno debe estar registrada tal cual en el proveedor.
- **Dominios:** con `DBYSON_OIDC_ALLOWED_DOMAINS` solo entran emails
  *verificados* de esos dominios exactos. Es **obligatorio** para
  auto-aprovisionar: con un emisor público como Google, cualquier cuenta del
  mundo podría crearse una cuenta `member` (y usar DByson como punto de salida
  hacia tu red). El servidor no arranca sin él.
- **Vinculación:** la administración crea la cuenta con el email de la persona;
  en Google se exige además el dominio corporativo firmado en `hd`, no solo
  el sufijo del correo. En Equipo, **Vincular correo** prepara cuentas existentes;
  su primer inicio con SSO se vincula a ella (sin distinguir mayúsculas), con o
  sin auto-aprovisionamiento. Si el email coincide con varias cuentas no se
  vincula ninguna. El auto-aprovisionamiento solo crea cuentas `member` nuevas.
- **2FA:** el SSO prueba la identidad, no el segundo factor de la cuenta. Si la
  cuenta tiene 2FA local, tras el SSO se pide el código. La sesión cuenta como
  verificada si el proveedor informa MFA (claim `amr`) o si
  `DBYSON_OIDC_TRUST_MFA=true`; si no, en `prod`/SSH se aplica la política de
  2FA igual que al resto.
- **Protecciones del flujo:** `state` ligado al navegador por cookie (evita el
  inicio de sesión forzado), PKCE, `nonce` obligatorio, `iss`/`aud`/`exp`
  verificados y claves RSA de al menos 2048 bits.
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
(solo `admin`, validado 1–3650). La purga borra solo el **tramo inicial** de la
cadena (por orden, no por fecha, para no abrir huecos si el reloj retrocedió)
y deja una entrada `audit.purged` con el conteo y el hash de la última entrada
eliminada (`anchor=`). La verificación acepta que la cadena empiece en ese
ancla solo si una entrada `audit.purged` firmada lo respalda: borrar el inicio
a mano sigue detectándose. Un SIEM con una exportación anterior puede
comprobar la continuidad con el ancla. Exporte antes de purgar: truncar el
**final** de la cadena sigue sin detectarse, como ya documenta el README.

## Clave maestra en modo equipo

En modo `team` el servidor **no arranca** con `master.key` dentro del volumen
de datos: exige `DBYSON_MASTER_KEY` o `DBYSON_MASTER_KEY_FILE` fuera de
`/data` (Docker secret). La huella de la clave activa queda en el log.

Rotación, **con el servidor detenido** (mientras corre, tiene la clave vieja
en memoria y seguiría escribiendo con ella):

```bash
docker stop dbyson
# --new-key-file: si el archivo no existe, se genera ahí (0600); si existe, se usa su clave.
docker run --rm -v dbyson-data:/data -v /ruta/segura:/keys \
  -e DBYSON_MASTER_KEY_FILE=/keys/actual.key \
  --entrypoint /dbyson dbyson:dev rotate-key --data-dir /data --new-key-file /keys/nueva.key
# después: DBYSON_MASTER_KEY_FILE=/keys/nueva.key y arrancar
```

Todo ocurre en **una transacción**: verifica la cadena de auditoría, la
re-firma, re-cifra conexiones, consultas guardadas y semillas TOTP, comprueba
la cadena nueva y solo entonces confirma. Si algo falla (por ejemplo, una
cadena manipulada) no cambia nada y se borra la clave nueva si se había
generado. Deja una entrada `vault.key_rotated` con las huellas. La clave nunca
se pasa por la línea de comandos (quedaría en el historial y en la lista de
procesos).

## Lista de despliegue en equipo

1. `DBYSON_MODE=team`, puerto detrás de HTTPS/VPN/zero-trust (ver
   [conectividad segura](CONECTIVIDAD_SEGURA.md)).
2. Clave maestra externa (secret, fuera del volumen).
3. Crear cuentas y conceder `use`/`manage` sobre conexiones compartidas.
4. SSO configurado si el equipo lo usa; 2FA activa en cuentas con `prod`/SSH.
5. `DBYSON_AUDIT_RETENTION_DAYS` y exportación periódica al SIEM/archivo.

## Pendiente de esta fase

- Pentest externo antes de la v1.0: bloqueador que requiere contratación
  externa, ver [PENTEST.md](PENTEST.md).
