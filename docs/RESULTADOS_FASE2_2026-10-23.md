# Validación de Fase 2 — conectividad segura

Rama local: `codex/secure-connectivity`, basada en `1de2643` e incluyendo las mejoras previas de conexiones. Los cambios de esta fase todavía no están integrados ni publicados.

## Resultado

- **20 E2E aprobados, 0 fallos, sin reintentos**, en 2,4 minutos. Chromium y MySQL 8.4 desechables.
- **`go test -race -count=1 ./...` aprobado** en todos los paquetes, con MySQL TLS y DByson mTLS reales.
- `go vet ./...`, compilación Go/frontend, TypeScript estricto, Prettier y `git diff --check` correctos.
- `npm audit`: 0 vulnerabilidades. `govulncheck`: 0 vulnerabilidades en símbolos alcanzables y 0 en paquetes importados. Informa `GO-2026-5932` únicamente en el paquete `golang.org/x/crypto/openpgp`, que DByson no importa ni utiliza; no existe versión corregida indicada y el análisis termina correctamente.
- Job `secure-connectivity` incorporado a la CI y requerido antes del build de imagen. La CI remota de esta rama aún no se ha ejecutado.

## Evidencia de los cuatro bloques

| Bloque | Comprobación |
|---|---|
| Cancelar SQL | Un usuario MySQL con sólo `USAGE` interrumpe su consulta; también se comprueban modo solo lectura, cancelación antes del inicio y desconexión del contexto. Una cancelación tardía no afecta a la siguiente consulta del pool. |
| Aislamiento de consultas | Otro navegador de la misma cuenta recibe 404 al intentar cancelar la consulta; perfiles y sesiones distintos no pueden cruzar el alcance. Autenticación y CSRF obligatorios. |
| Scripts | Un `SLEEP(20)` real desaparece de la lista de procesos y no se envía la sentencia siguiente. Después se puede ejecutar `SELECT 42`. |
| Sesiones | Se revocan otros navegadores manteniendo la sesión actual, y la cookie revocada recibe 401. Revocar una sesión mientras consulta detiene su `SLEEP` real. Se verifican revocación individual/propia, aislamiento entre cuentas y sesiones caducadas. |
| Migración | Una base con el esquema anterior mantiene sus sesiones y CSRF y recibe IDs públicos distintos para cada sesión. |
| MySQL X509 | Cuenta `REQUIRE X509`: acepta el certificado válido y rechaza ausencia, caducidad, CA ajena, uso incorrecto, clave ausente y clave no correspondiente. Sigue verificando al servidor MySQL. |
| Handshake mTLS | Se aceptan clientes válidos y se rechazan los ausentes, caducados, de otra CA y con uso incorrecto. TLS anterior a 1.3 y configuración parcial/CA inválida se rechazan. |
| DByson mTLS real | Healthcheck y login funcionan con certificado válido. Las cabeceras falsas no sustituyen un certificado. Una cookie válida copiada a otro cliente con certificado confiable distinto recibe 401. |
| Regresión | Los 18 E2E anteriores de datos, SQL, borradores, solo lectura, exportación, SSH y distribución responsive siguen aprobados. |

El panel fue inspeccionado visualmente: [sesiones activas](qa/fase2-2026-10-23/sesiones-activas.png).

## Decisiones y límites

Las decisiones están en [ADR 0007](adr/0007-conectividad-segura.md). La configuración, emisión de certificados con OpenSSL, despliegue y repetición de pruebas están en la [guía operativa](CONECTIVIDAD_SEGURA.md).

Cancelar no revierte operaciones ya confirmadas. El registro de consultas es local a una instancia. La confianza TLS usa la CA configurada y la vigencia de certificados; el listener no consulta OCSP/CRL. Para desplegar mTLS deben instalarse certificados reales; no se instalaron certificados de prueba ni se modificaron datos en el entorno de uso diario.

La imagen `v0.1.0` publicada permanece intacta. La fase está implementada y validada localmente; quedan integrar los cambios, confirmar la CI remota y configurar los certificados reales del despliegue.
