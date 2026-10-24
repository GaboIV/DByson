# 0007 — Cancelación de consultas, sesiones y mTLS

Estado: aceptada para la implementación de Fase 2. Fecha: 2026-10-23.

## Contexto

Abortar una petición del navegador no confirma que MySQL haya dejado de ejecutar SQL. La aplicación necesita cancelar únicamente sus propias consultas, revocar accesos de forma efectiva y ofrecer protección por certificado sin confiar en identidad declarada por cabeceras HTTP.

## Decisión

- Cada consulta SQL reserva una conexión física; su `CONNECTION_ID()` procede del servidor. `KILL QUERY` usa una segunda conexión con las mismas credenciales. Se serializan cancelación y liberación de la conexión. Timeout y desconexión del contexto también intentan interrumpir el SQL antes de cerrar su socket.
- El registro en memoria se vincula a usuario, sesión y conexión DByson, con límites de concurrencia. Los clientes antiguos que no mandan ID reciben uno interno. Después del registro se valida nuevamente la sesión para cerrar la carrera con su revocación.
- Revocar una sesión elimina su autenticación persistida y detiene sus consultas registradas. La lista sólo muestra sesiones de la cuenta propia y usa IDs públicos aleatorios independientes de las cookies y sus hashes. Revocación y cancelación están protegidas por CSRF y auditadas.
- HTTPS usa el listener de Go con TLS 1.3. mTLS exige cadena, vigencia y uso cliente válidos en todos los endpoints. La cookie se vincula a la huella del certificado verificado; no se toma identidad de cabeceras de un proxy. La configuración parcial falla antes de abrir el servidor.
- La emisión de certificados se delega en una CA corporativa o en OpenSSL, con guía operativa; no se añade un emisor de CA propio al binario de la aplicación.

## Consecuencias

Cancelar no deshace operaciones ya confirmadas. Si falla el canal de control, se informa y se cierra el socket, sin asegurar que el servidor haya terminado la consulta. La consola omite las sentencias pendientes del script.

mTLS complementa contraseña y 2FA y no asigna usuarios por certificado. Habilitarlo o renovar el certificado requiere otro login. Cambiar certificados/CA requiere reiniciar. La revocación de sesiones de aplicación es independiente de la confianza TLS: el listener no consulta OCSP/CRL y se recomiendan certificados individuales de corta duración y una CA dedicada.

El registro de consultas pertenece a una sola instancia. El modo distribuido, SSO y los permisos compartidos siguen dentro de la fase de equipos.

La validación incluye MySQL real con `REQUIRE X509`, usuario de sólo `USAGE` para cancelación, handshakes TLS reales, pruebas de aislamiento/CSRF/migración y E2E con dos navegadores.
