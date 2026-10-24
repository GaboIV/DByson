# ADR 0008: productividad y passkeys

- Estado: aceptada
- Fecha: 2026-10-24

## Contexto

La Fase 3 incorpora herramientas que guardan SQL, escriben lotes, cambian estructura y administran MySQL. Deben reutilizar los controles de seguridad existentes y distinguir las cuentas de MySQL de las futuras cuentas de equipo de DByson.

## Decisión

- Guardar SQL solo de forma explícita, privado por usuario y conexión, con cifrado de sobre del vault. Crear revisiones independientes y limitar la biblioteca a 200 entradas. No sincronizar borradores en segundo plano.
- Leer claves foráneas desde metadatos reales, preservando orden, claves compuestas y bases distintas. Abrir consultas filtradas para revisar antes de ejecutar. Construir identificadores escapados y literales hexadecimales, sin interpolación de texto de filas como SQL.
- Visualizar EXPLAIN tabular sin EXPLAIN ANALYZE. Mostrar estimaciones, no presentar costes como tiempos medidos.
- Importar CSV con valores parametrizados, validación de columnas y una única transacción InnoDB. Limitar tamaño y filas. No utilizar LOAD DATA LOCAL INFILE. Importar SQL a través del editor, con confirmaciones y detención al primer error; no prometer atomicidad de DDL o scripts completos.
- Generar DDL visual para un conjunto acotado de operaciones y conservar los atributos del SHOW CREATE TABLE al cambiar tipos. Bloquear generación si no puede reconstruirse con seguridad. Conservar SQL como vía para operaciones avanzadas.
- Administrar procesos mediante KILL QUERY y las autorizaciones de MySQL. Administrar cuentas con endpoint dedicado, rol administrador de DByson y lista cerrada de privilegios. No registrar contraseñas en la auditoría o vista previa. Delegar los parámetros al escape mantenido por go-sql-driver/mysql con charset utf8mb4; mantener deshabilitado multiStatements.
- Implementar passkeys con go-webauthn/webauthn y origen/RP ID explícitos. Exigir credenciales descubribles y verificación de usuario. Guardar desafíos en memoria, de un solo uso, vinculados a cookie y certificado mTLS, y a la sesión al registrar. Requerir contraseña actual para registrar/eliminar y MFA de sesión si ya existe TOTP.
- Persistir identificadores de usuario aleatorios para WebAuthn, independientes de la clave maestra. Actualizar contadores mediante comparación atómica y rechazar alertas de clonación.
- Mantener elementos HTML semánticos (inputs, selects, botones, grupos y tablas) con estilos del tema. Sustituir window.confirm por diálogos React con foco atrapado; conservar beforeunload, cuya presentación controla el navegador.

## Consecuencias

La biblioteca necesita la misma clave maestra y copia de seguridad que los perfiles. La revocación de una passkey bloquea nuevos accesos; las sesiones ya creadas se revocan desde su panel. Cambiar el RP ID exige volver a registrar credenciales.

CSV queda limitado a lotes pequeños y tablas InnoDB; los efectos externos de triggers no son reversibles por la transacción. SQL y DDL pueden quedar aplicados parcialmente. El diseñador no reemplaza todas las posibilidades de ALTER TABLE y el diagrama no infiere relaciones inexistentes.

Las herramientas se validan en MySQL/MariaDB desechables y con un autenticador WebAuthn virtual. La configuración y los dispositivos de una instalación real permanecen a cargo de su administrador. La Fase 4 debe conservar el aislamiento por propietario al introducir conexiones compartidas y roles.
