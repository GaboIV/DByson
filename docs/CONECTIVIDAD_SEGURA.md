# Conectividad segura — Fase 2

## Cancelar una consulta

Durante una ejecución en la consola SQL aparece **Cancelar consulta**. DByson envía `KILL QUERY` usando las mismas credenciales MySQL y una segunda conexión de control. La conexión que ejecuta el SQL permanece reservada hasta que terminan tanto la consulta como la cancelación; no se acepta un ID de proceso MySQL proporcionado por el navegador.

Una petición sólo puede cancelar una consulta de su mismo usuario, sesión DByson y perfil. No requiere `SUPER`, `PROCESS` ni `CONNECTION_ADMIN`: se interrumpe una consulta propia del usuario MySQL. También funciona con perfiles de solo lectura. Las sentencias aún no ejecutadas del script quedan omitidas. Una operación que ya terminó o se confirmó **no se revierte**.

Cerrar la pestaña o perder la petición HTTP provoca la cancelación del contexto y un intento de `KILL QUERY`. El límite de ejecución SQL sigue siendo de 60 segundos. Se admiten hasta 8 consultas simultáneas por usuario y 64 por instancia. Si el comando de control falla, se cierra el socket de ejecución y se informa del fallo; ese cierre por sí solo no confirma que MySQL haya interrumpido la operación.

## Sesiones activas

En **Seguridad → Sesiones activas** se muestran únicamente los accesos de tu propia cuenta: IP observada por el servidor, navegador, inicio, última actividad, 2FA y vinculación a certificado. La IP corresponde al socket; DByson no confía en `X-Forwarded-For`.

Puedes revocar una sesión concreta, cerrar todas las demás o cerrar la actual. La cookie revocada deja de autenticar en la siguiente petición. La revocación también cancela las consultas en curso de esa sesión en esta instancia. La sesión actual se conserva al cerrar las demás. La lista excluye accesos caducados: 2 horas sin actividad o 12 horas desde el inicio, lo que ocurra antes.

Los identificadores mostrados son aleatorios e independientes de la cookie y de su hash. No se exponen tokens ni claves CSRF en la lista. Cada revocación exige autenticación y CSRF y queda auditada. Activar/desactivar 2FA también detiene las consultas de los accesos que se revocan.

## HTTPS y mTLS nativos

Variables del servidor:

| Variable | Uso |
|---|---|
| `DBYSON_TLS_CERT_FILE` | Certificado de servidor PEM; puede incluir su cadena intermedia. |
| `DBYSON_TLS_KEY_FILE` | Clave privada PEM correspondiente. |
| `DBYSON_TLS_CLIENT_CA_FILE` | CA o conjunto de CA de confianza para certificados de cliente. |

Sin estas variables se conserva HTTP para desarrollo/loopback. Certificado y clave de servidor juntos activan HTTPS con TLS 1.3. Añadir la CA de clientes exige mTLS para **todo** el listener: aplicación, estáticos, setup, login y healthcheck. Una configuración parcial o un PEM ilegible/inválido impide arrancar. No hay puerto HTTP alternativo ni se confía en cabeceras de certificado de proxies.

El certificado de cliente debe tener uso `clientAuth`, cadena confiable y vigencia válida. mTLS permite llegar a DByson; después se exige el usuario, contraseña y la 2FA que corresponda. No asigna usuarios ni roles por el CN del certificado. La sesión se vincula a la huella SHA-256 del certificado verificado: una cookie de otro certificado, o creada antes de activar mTLS, exige iniciar sesión de nuevo.

Para despliegues administrados, usa la CA de clientes de tu organización y certificados individuales de corta duración. El listener no consulta servicios OCSP ni listas CRL: para retirar inmediatamente la confianza TLS hay que actualizar la CA de confianza y reiniciar. La revocación del panel cierra sesiones de aplicación, no revoca un certificado. La renovación del certificado también requiere un nuevo login.

### Emitir un cliente con OpenSSL

Usa una CA dedicada a clientes; conserva su clave fuera del servidor DByson. Estos comandos se ejecutan en la máquina de emisión, en un directorio privado **fuera del repositorio**. Si ya tienes CA corporativa, solicita allí el certificado con uso `clientAuth`.

```bash
umask 077
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -sha256 \
  -days 365 -keyout client-ca-key.pem -out client-ca.pem \
  -subj "/CN=DByson client CA" -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign"

openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -noenc \
  -keyout usuario-key.pem -out usuario.csr -subj "/CN=usuario-dbyson"

printf '%s\n' 'basicConstraints=critical,CA:FALSE' \
  'keyUsage=critical,digitalSignature' 'extendedKeyUsage=clientAuth' > client.ext

openssl x509 -req -in usuario.csr -CA client-ca.pem -CAkey client-ca-key.pem \
  -CAcreateserial -days 7 -sha256 -extfile client.ext -out usuario.pem

openssl pkcs12 -export -inkey usuario-key.pem -in usuario.pem \
  -certfile client-ca.pem -out usuario.p12 -name DByson
```

OpenSSL solicita las contraseñas de la CA y del paquete PKCS#12. Importa `usuario.p12` en el almacén de certificados personales del navegador/sistema. Distribuye cada paquete únicamente a su titular y protege su contraseña por separado.

El certificado HTTPS del servidor es independiente: usa uno de tu PKI o ACME cuyo SAN coincida con el dominio real. Para `https://localhost:8443`, debe incluir `DNS:localhost`. El navegador también debe confiar en su CA. No uses las claves/certificados de `internal/testpki` en un despliegue.

### Docker

El ejemplo [docker-compose.mtls.yml](../deploy/docker-compose.mtls.yml) construye el código de esta rama: la imagen publicada `0.1.0` todavía no incluye estas funciones. Monta un directorio con `server.pem`, `server-key.pem` y `client-ca.pem`; el UID 65532 del contenedor debe poder leerlos. Mantén las claves con permisos mínimos y el montaje de solo lectura. No montes la clave privada de la CA emisora.

```powershell
$env:DBYSON_TLS_DIR = 'C:/ruta/privada/certificados-dbyson'
docker compose -f deploy/docker-compose.mtls.yml up -d --build
```

El ejemplo reutiliza el volumen externo `dbyson-data` y publica únicamente `127.0.0.1:8443`. Ajusta deliberadamente esa publicación si necesitas acceso remoto y configura la clave maestra externa según tu despliegue. Los monitores deben usar HTTPS con un certificado cliente; no existe una excepción de healthcheck sin certificado.

```bash
curl --cacert server-ca.pem --cert usuario.pem --key usuario-key.pem \
  https://localhost:8443/api/healthz
```

## Pruebas reproducibles

```powershell
docker compose -p dbyson-security-test -f compose.security-test.yml up -d --build --wait --wait-timeout 180 database app-mtls
docker compose -p dbyson-security-test -f compose.security-test.yml run --rm --no-deps tests
docker compose -p dbyson-security-test -f compose.security-test.yml down -v
```

El entorno genera una CA y claves efímeras, usa MySQL 8.4 con TLS y una instancia DByson con mTLS, no publica puertos y no monta datos de desarrollo. Ejecuta `go test -race -count=1 ./...`. La CI incluye este entorno y mantiene además la matriz MySQL/MariaDB habitual.

Se comprueban certificados MySQL válidos, ausentes, caducados, de otra CA, con uso incorrecto, clave ausente y clave no correspondiente; se conserva también la validación de identidad del servidor. `KILL QUERY` se verifica con un usuario de privilegios `USAGE`, cancelación previa al inicio, cancelación explícita, timeout/desconexión del contexto y reutilización posterior del pool. Los tests HTTPS realizan handshakes mTLS reales y rechazan clientes no válidos y versiones TLS anteriores a 1.3.

Las pruebas API verifican CSRF, aislamiento de cuenta y sesión, revocación, caducidad y vinculación de cookies a certificados. La instancia real con mTLS comprueba login, healthcheck protegido y rechazo de una cookie copiada a otro certificado válido. Los E2E añaden dos navegadores contra MySQL real para cancelar un `SLEEP`, comprobar que no continúa el script y revocar otra sesión mientras consulta.
