# Cierre de brechas del MVP · 21 de octubre de 2026

## Solo lectura

Los perfiles `readOnly` configuran `SET SESSION TRANSACTION READ ONLY` en **cada conexión física** del pool. Si el servidor rechaza ese ajuste, la conexión falla. Las rutas de mutación siguen rechazándose en la API; la consola admite una lista conservadora de lecturas y bloquea `CALL`, `SET`, comandos desconocidos, escrituras a archivo, asignaciones y lecturas con bloqueo.

La consola acepta una sentencia por ejecución. Rechaza comentarios ejecutables de MySQL/MariaDB, texto sin cerrar y escapes con barra inversa cuyo significado depende de `sql_mode`; para comillas dentro de literales usa comillas duplicadas, por ejemplo `'O''Reilly'`. Admite CTE con SELECT y comentarios normales. Los literales, identificadores entre comillas y subconsultas no se confunden con el WHERE principal.

La restricción de sesión complementa la política de aplicación. Para producción, sigue siendo apropiado usar cuentas MySQL con permisos mínimos: no convierte una cuenta privilegiada ni funciones externas/UDF en un entorno aislado. Referencias de comportamiento: [MySQL SET TRANSACTION](https://dev.mysql.com/doc/refman/8.4/en/set-transaction.html) y [MariaDB START TRANSACTION](https://github.com/mariadb-corporation/mariadb-docs/blob/main/server/reference/sql-statements/transactions/start-transaction.md).

## Resultados

- Consola: máximo **1.000 filas** y **8 MiB de columnas y filas codificadas como JSON**. La API no permite elevar esos topes desde el cliente.
- Grilla: conserva la paginación de hasta 500 filas por página y comparte el límite de bytes.
- Cuando falta espacio se detiene la lectura, se cancela la consulta y se devuelve `truncated`, `truncationReason`, `rowLimit`, `byteLimit` y `resultBytes`.
- Un aviso identifica el resultado parcial, incluso cuando la primera fila ya excede el límite. El usuario puede reducir columnas, filtrar o exportar.

El límite controla el resultado acumulado; el driver aún necesita recibir una fila individual antes de medirla. La ejecución mantiene los timeouts existentes. No se implementó todavía un botón de cancelación con `KILL QUERY`.

## Confirmaciones

La consola solicita confirmación para sentencias de cambio, incluidas INSERT/CREATE y UPDATE/DELETE aunque tengan WHERE. DROP, TRUNCATE y las eliminaciones de filas también la exigen en sus rutas dedicadas. ALTER ya no envía un booleano que salta la revisión.

El servidor emite un desafío aleatorio vinculado a sesión, método, ruta, sentencia exacta o conjunto de claves, y destino. Caduca en cinco minutos y se consume una sola vez. El usuario revisa el SQL o las claves y escribe el nombre mostrado: base/conexión de contexto en la consola, o base.tabla en acciones de tabla. Cambiar la operación, reutilizar el desafío o enviarlo desde otra sesión falla. El antiguo `confirmDestructive: true` por sí solo no autoriza la ejecución.

Los desafíos viven en memoria: reiniciar el servidor obliga a solicitar una confirmación nueva. Las confirmaciones por lotes de tablas se hacen individualmente; no se promete atomicidad entre sentencias DDL.

## Exportaciones

SQL, CSV y JSON leen directamente desde `sql.Rows`, fila por fila. CSV usa su búfer de escritura; SQL/JSON usan un búfer de 32 KiB, sin construir listas con la tabla completa. Los límites de la consola no se aplican a las exportaciones.

- CSV y JSON requieren una tabla explícita; nunca eligen silenciosamente la primera.
- SQL permite una tabla o todas las tablas base de la base seleccionada, con estructura, datos o ambos.
- El dump preserva binarios y decimales mediante expresiones de bytes, y fechas con microsegundos. Guarda/restaura el modo SQL y las comprobaciones FK del destinatario.
- Los errores de origen o escritura se propagan. Antes de empezar el cuerpo HTTP se devuelve un error; si la descarga ya comenzó, se aborta la transferencia para que un archivo parcial no se presente como descarga completa.
- Se conserva el timeout de exportación de 120 segundos. No se implementaron trabajos en segundo plano, reanudación ni una instantánea consistente entre tablas. CSV/JSON son formatos orientados a intercambio de datos; el dump SQL es el formato validado aquí para restaurar binarios y tipos.

También se deshabilitó `customWhere` en los filtros estructurados y se validan tipos y longitudes del formulario de creación de tablas.

## Validación reproducible

Pruebas unitarias: clasificación y límites SQL, confirmaciones ligadas a la sesión y de un solo uso, inicialización de cada conexión en solo lectura, resultados grandes y errores de lectura/escritura en exportaciones. Las pruebas de streaming comprueban que un fallo del destino detiene el recorrido antes de materializar toda la tabla.

Integración: MySQL 8.4 y MariaDB 11.4 en contenedores desechables, con 1.105 filas. Se comprueban límites exactos y excedidos, filas de más de 8 MiB, tres conexiones físicas en solo lectura, rechazo de escrituras sobre InnoDB/MyISAM y desde una función almacenada, CSV/JSON completos y restauración SQL con Unicode, saltos de línea, NUL, comillas, barras, punto y coma, decimales de alta precisión, binarios, NULL y microsegundos.

Para ejecutar sobre un servidor **desechable** con usuario root y contraseña de prueba `dbyson-tests`:

```sh
DBYSON_INTEGRATION_ADDR=servidor-de-prueba:3306 go test ./internal/drivers/mysql -run TestIntegration -count=1 -v
```

La prueba crea bases de nombre aleatorio y las elimina al terminar. Sin esa variable, las pruebas de integración se omiten. El workflow de CI ahora contiene la matriz MySQL/MariaDB; su ejecución remota se validará al enviar los cambios al repositorio.

Este hito cierra las cuatro brechas de implementación revisadas; no certifica por sí solo el release completo ni sustituye los demás pendientes del plan maestro.

Verificación de interfaz realizada en Chrome contra una instancia temporal real: bloqueo de escrituras desde un perfil solo lectura, confirmación escrita y cancelación sin cambios, ejecución confirmada, aviso de 1.000 filas, selección obligatoria de tabla y descarga JSON, y errores al exportar una tabla inexistente. Sin errores JavaScript registrados.

Despliegue local verificado: imagen reconstruida y contenedor `adminbase` sustituido en `127.0.0.1:8082`, conservando `adminbase-data`, configuración y setup existente. Salud `ok`, aplicación HTTP 200 y bundle con las nuevas protecciones. Contenedor anterior detenido como `adminbase-previous-mvp-20261021162528`.
