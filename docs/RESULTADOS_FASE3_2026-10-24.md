# Verificación de productividad — 24 de octubre de 2026

Rama: `codex/productivity`, creada desde `21e050a` (Fase 2). Cambios locales, pendientes de commit, push, PR y CI remoto. No se modificó la etiqueta v0.1.0 ni se desplegaron cambios o credenciales en instalaciones reales.

## Resultado

| Comprobación | Resultado |
|---|---|
| Suite completa Playwright/Chromium | 29 aprobadas, 0 fallos, 0 reintentos · 3,5 min |
| Repetición de las pantallas de Fase 3 tras ajuste visual final y validación UTF-8 | 9 aprobadas, 0 fallos, 0 reintentos · 42,2 s |
| Pruebas unitarias del frontend | 4 aprobadas |
| Suite completa Go con MySQL 8.4, PKI desechable y servidor DByson mTLS | `go test -race -count=1 ./...` aprobado |
| Driver final con columnas calculadas y defaults automáticos, MySQL 8.4 | `go test -race -count=1 ./internal/drivers/mysql` aprobado · 7,669 s |
| Mismo driver final, MariaDB 11.4 | Aprobado · 9,502 s |
| Análisis estático Go | `go vet ./...` aprobado |
| TypeScript estricto y build de producción | Aprobados en Docker y localmente |
| Formato frontend | `npm run format:check` aprobado |
| Dependencias frontend | `npm audit --audit-level=high`: 0 vulnerabilidades |
| Dependencias Go | `govulncheck`: 0 vulnerabilidades que afecten al código; 0 en paquetes importados; aviso informativo en 1 módulo requerido cuyo código vulnerable no se usa |
| Revisión del diff | `git diff --check` aprobado |

Vite mantiene un aviso de tamaño del bundle principal (aproximadamente 826 KB, 258 KB gzip). El build termina correctamente. No se han añadido dependencias JavaScript; la dependencia nueva de backend es go-webauthn/webauthn v0.18.2 y sus dependencias transitivas.

## Cobertura de navegador de Fase 3

1. **Biblioteca:** guardar en servidor, recargar la página, volver a abrir con su base original, eliminar; EXPLAIN real con plan visual.
2. **CSV:** vista previa, cancelar sin escribir, confirmar, texto Unicode/comillas/saltos de línea, NULL, error de duplicado con rollback de todas las filas del lote, comillas incompletas y archivo con UTF-8 inválido.
3. **Detalle/FK/ER:** visor nuevo, referencia compuesta con dos columnas, ejecución de la consulta filtrada hacia el padre, referencia NULL deshabilitada, diagrama con relaciones declaradas. Alineación de botones laterales y pestañas, y de búsqueda lateral y segunda fila de filtros, comprobada con geometría real y tolerancia de 2 px.
4. **Estructura:** generar ADD COLUMN visual, confirmar y comprobar el esquema en MySQL; generar MODIFY COLUMN preservando DEFAULT NULL y comprobar el cambio real de tipo.
5. **SQL importado:** abrir archivo como borrador y ejecutar dos sentencias con resultados separados.
6. **Procesos:** SHOW FULL PROCESSLIST con SELECT SLEEP(20) real de otro navegador; confirmar KILL QUERY y comprobar su desaparición en MySQL.
7. **Cuentas MySQL:** crear, conceder SELECT para una base con guion bajo, consultar permisos, revocar, eliminar y comprobar que la contraseña no aparece en la auditoría.
8. **Responsive:** detalle en temas claro/oscuro a 1366, 390 y 320 px, sin desbordamiento horizontal del panel, foco atrapado y cierre con Escape.
9. **Passkeys:** crear credencial descubrible con un autenticador CTAP2 virtual, registrar con contraseña, rechazar una aserción cuyo origen fue alterado, acceder correctamente, comprobar MFA de sesión, rechazar replay y eliminar la credencial.

Las 20 pruebas previas permanecen en la suite: flujo principal, registros, errores, estructura, borradores sin guardar, SQL/autocompletado, exportaciones, solo lectura, temas, asistente y confianza SSH, responsive de conexiones y revocación de sesiones/cancelación real.

## Cobertura de backend y pruebas unitarias

- Consultas guardadas cifradas en SQLite, lectura y eliminación por propietario, rechazo de acceso de otra cuenta y CSRF.
- Importación, KILL QUERY y administración de cuentas bloqueados por el encabezado de solo lectura, y rechazo de identificadores de proceso inválidos.
- Configuración WebAuthn incompleta o insegura rechazada; contraseña necesaria para registrar, verificación de usuario y credencial descubrible obligatorias, desafío con cookie segura y registro sin sesión rechazado; login sin enumeración de cuentas.
- MySQL y MariaDB: metadatos de FK compuesta ordenados, importación con Unicode/multilínea/NULL, defaults automáticos admitidos, columnas calculadas rechazadas y rollback ante duplicado.
- Parámetros de CREATE USER con comillas, barras, Unicode y apariencia de SQL bajo modos normal y NO_BACKSLASH_ESCAPES. La contraseña funciona al conectar y no altera la estructura de la sentencia.
- Parser CSV: BOM/CRLF, separadores, comillas, campos vacíos, rechazo de anchura incorrecta/exceso de filas y round-trip de 128 filas con caracteres reservados. Escape de identificadores y valores FK independiente del modo SQL.

## Evidencia visual

- [Detalle de registro](qa/fase3-2026-10-24/detalle-registro.png)
- [Detalle móvil oscuro, 390 px](qa/fase3-2026-10-24/detalle-Oscuro-390.png)
- [Detalle móvil claro, 320 px](qa/fase3-2026-10-24/detalle-Claro-320.png)
- [Detalle claro de escritorio](qa/fase3-2026-10-24/detalle-Claro-1366.png)
- [EXPLAIN visual](qa/fase3-2026-10-24/explain-visual.png)
- [Importación con error y rollback](qa/fase3-2026-10-24/csv-rollback.png)
- [Diseñador de estructura](qa/fase3-2026-10-24/estructura-visual.png)
- [Diagrama ER](qa/fase3-2026-10-24/diagrama-er.png)
- [Passkeys](qa/fase3-2026-10-24/passkeys.png)

Las capturas contienen exclusivamente datos desechables. Se revisaron visualmente el detalle de escritorio, el detalle móvil y el modal del diagrama.

## Límites y puesta en servicio

La implementación y sus límites están en [PRODUCTIVIDAD.md](PRODUCTIVIDAD.md), y las decisiones en [ADR 0008](adr/0008-productividad-y-passkeys.md). CSV admite lotes pequeños transaccionales en InnoDB; SQL/DDL no garantiza atomicidad del script. El diseñador cubre operaciones habituales y el diagrama muestra un máximo de 100 tablas filtrables. Los permisos efectivos de administración siguen siendo los de MySQL.

Activar passkeys requiere definir DBYSON_WEBAUTHN_RP_ID y DBYSON_WEBAUTHN_ORIGIN para el dominio real y registrar los dispositivos. Las pruebas usan localhost y un autenticador virtual; no acreditan instalación o recuperación en dispositivos físicos concretos. No se han usado conexiones del usuario ni modificado datos de producción.
