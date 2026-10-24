# Productividad de DByson

Implementación de la Fase 3 en `codex/productivity`. Los cambios requieren reconstruir la aplicación; la imagen publicada `v0.1.0` no los incluye.

## Consultas y planes

- **Consultas guardadas**, en la cabecera de la consola SQL, abre la biblioteca privada de la conexión. Guarda nombre, base y SQL en SQLite; el SQL se cifra con una clave independiente por registro mediante el vault existente. Los nombres y la base son metadatos visibles en SQLite.
- Cada guardado crea una revisión independiente, hasta 200 por conexión y usuario. Puedes abrirla en una pestaña nueva o eliminarla. Abrir conserva la base original y no ejecuta SQL automáticamente. No hay compartición entre usuarios ni sincronización automática de borradores.
- **EXPLAIN visual** prepara `EXPLAIN` para una consulta SELECT/WITH o su selección. Muestra orden de acceso, tabla, índice, filas estimadas, porcentaje filtrado y observaciones. Destaca accesos `ALL`. El resultado tabular original también permanece disponible. Es una estimación del optimizador; no se utiliza `EXPLAIN ANALYZE`.
- El editor CodeMirror conserva resaltado, sugerencias por tablas/columnas, selección y Ctrl+Enter. Los scripts se detienen ante el primer error o cancelación; las sentencias restantes se marcan como omitidas. Una operación ya confirmada por MySQL no se revierte al detener un script.

## Relaciones y detalle de registros

El detalle de una fila presenta tarjetas de propiedades, tipos, claves, búsqueda, filtros de campos, copia individual y JSON. NULL y cadena vacía se distinguen. Los valores largos se expanden sin reemplazar el valor completo usado al copiar. El panel se adapta a móvil, atrapa el foco y se cierra con Escape.

Las relaciones se consultan en `information_schema.KEY_COLUMN_USAGE`. En el detalle, un botón por clave foránea abre una consulta filtrada hacia la tabla referenciada. Se preservan el orden de columnas, las claves compuestas y las bases distintas; una referencia con NULL o columnas ausentes queda deshabilitada. La consulta se abre como borrador para revisar y ejecutar. Los identificadores se escapan y los valores se convierten desde literales hexadecimales UTF-8.

**Herramientas → Diagrama ER** muestra las tablas y las relaciones declaradas, con líneas, tarjetas y una lista textual accesible. Puedes filtrar nombres y abrir una consulta desde una tarjeta. Hay un máximo visual de 100 tablas a la vez; no se infieren relaciones a partir de nombres y no se editan relaciones arrastrando líneas.

## Importar CSV y SQL

En el pie del menú lateral abre **Herramientas → Importar**.

### CSV

1. Selecciona una tabla y separador: coma, punto y coma o tabulador.
2. El archivo debe incluir una cabecera con los nombres exactos de las columnas. Límite: 512 000 bytes en el navegador, 5000 filas, 256 columnas y el límite JSON de 1 MiB del servidor. Un archivo con muchos caracteres que requieren escape JSON puede alcanzar el límite del servidor antes que el límite de archivo.
3. Revisa las primeras cinco filas. Se admiten BOM, CRLF/LF, comillas dobles escapadas y campos multilínea. Las anchuras inconsistentes y las comillas incompletas se rechazan.
4. El token `\N` representa NULL de forma predeterminada; puedes cambiarlo. Un campo vacío conserva la cadena vacía. No hay inferencia automática de tipos: MySQL valida y convierte los valores.
5. Confirma el destino exacto en el diálogo. La confirmación se vincula al contenido completo del lote y solo sirve una vez.

El servidor usa valores parametrizados y una transacción en una tabla InnoDB. Un error revierte las filas del lote; se indica la fila que falló. Se rechazan columnas duplicadas, inexistentes o generadas y motores no transaccionales. Los efectos externos de triggers o escrituras que esos triggers hagan en motores no transaccionales quedan fuera de esta garantía. No hay `LOAD DATA LOCAL INFILE`, sustitución silenciosa de duplicados ni división del lote en commits parciales. La importación permanece bloqueada en solo lectura.

### SQL

El archivo abre una pestaña SQL con el script y no se ejecuta durante la carga. Revisa y ejecuta las sentencias; se mantienen las confirmaciones de operaciones destructivas y las restricciones de solo lectura. El resultado por sentencia indica el avance. El script se detiene al primer error, pero el DDL puede hacer commit implícito y las sentencias anteriores pueden haber quedado aplicadas. No hay una transacción global de restauración ni soporte de directivas del cliente `mysql` como `DELIMITER` o `SOURCE`.

## Estructura

**Modificar → Diseñador de estructura** permite agregar columnas con tipos habituales y nulabilidad, cambiar el tipo de una columna, eliminar columnas, crear un índice simple o único y renombrar tablas. Genera una vista previa DDL que se prepara en el editor y exige confirmación antes de ejecutar.

Para cambiar un tipo, el diseñador conserva el resto de la definición obtenida con `SHOW CREATE TABLE`: defaults, comentarios, colación, auto-incremento y expresiones generadas. Si no puede reconocer el prefijo del tipo, bloquea la generación y remite al SQL. No rediseña automáticamente índices compuestos, claves foráneas o expresiones avanzadas. Cambiar de tipo puede perder información y MySQL puede rechazar combinaciones incompatibles; revisa la sentencia y conserva una copia de los datos.

## Procesos y cuentas de MySQL

**Herramientas → Procesos** lee `SHOW FULL PROCESSLIST`, sin actualización automática. Sin el privilegio PROCESS se ven únicamente los procesos visibles para la cuenta MySQL. **Detener consulta** solicita confirmación y envía `KILL QUERY` a un identificador decimal; MySQL comprueba los permisos efectivos. El proceso puede terminar antes de la confirmación y los identificadores pueden reutilizarse: actualiza antes de actuar. No se envía `KILL CONNECTION`.

**Herramientas → Usuarios MySQL** requiere rol administrador de DByson y los privilegios MySQL correspondientes. Permite listar cuentas, consultar SHOW GRANTS, crear y eliminar cuentas y conceder/revocar SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX y DROP para una base o tabla concreta. No administra usuarios de DByson: eso pertenece a la Fase 4.

Las contraseñas de cuentas nuevas no se incluyen en la vista previa, la respuesta ni el registro de auditoría. La biblioteca oficial del driver interpola los parámetros según el modo SQL activo y el charset fijo utf8mb4; `multiStatements` permanece deshabilitado. Se prueban contraseñas con comillas, barras, Unicode y apariencia de SQL bajo el modo normal y `NO_BACKSLASH_ESCAPES`. No se ofrecen GRANT OPTION, privilegios globales ni roles MySQL desde este formulario. El servidor vuelve a comprobar solo lectura, propiedad de la conexión, MFA y CSRF.

## Passkeys

El administrador configura dos variables explícitas antes de iniciar DByson:

```text
DBYSON_WEBAUTHN_RP_ID=db.example.org
DBYSON_WEBAUTHN_ORIGIN=https://db.example.org
```

El origen debe coincidir con el hostname de RP ID, sin ruta, query ni credenciales. HTTP solo se permite para `localhost`, por ejemplo `http://localhost:18080` para desarrollo. No se deduce el dominio de cabeceras Host/Forwarded. Si ambas variables están vacías, las passkeys quedan deshabilitadas y el acceso habitual sigue disponible.

En **Seguridad → Passkeys**, escribe un nombre y la contraseña actual, y registra el dispositivo. Si TOTP ya está activo, la sesión también debe haber superado MFA. Se permiten hasta 20 credenciales por usuario. Para eliminar una credencial también se exige la contraseña actual. La eliminación impide nuevos accesos con ella; para retirar sesiones ya iniciadas usa el panel de sesiones.

En el acceso aparece **Entrar con passkey** cuando el servidor y el navegador lo permiten. Se exige una credencial descubrible y verificación de usuario: PIN, biometría o mecanismo equivalente del autenticador. No se almacena biometría ni clave privada. El acceso verificado establece una sesión marcada como MFA; el acceso con contraseña conserva el flujo TOTP existente.

La verificación criptográfica utiliza [go-webauthn/webauthn v0.18.2](https://github.com/go-webauthn/webauthn/tree/v0.18.2). Se validan origen, RP ID, desafío, firma y verificación del usuario. Los desafíos duran cinco minutos, son de un solo uso y se vinculan a una cookie HttpOnly/SameSite Strict, al certificado mTLS verificado si existe y, en el registro, a la sesión autenticada. Hay límites globales de ceremonias y límites por IP. El contador de firma se actualiza mediante comparación atómica y se rechaza una alerta de clonación. Los identificadores de usuario son aleatorios y persistentes, independientes de la clave maestra.

Mantén estable el dominio de acceso: las credenciales WebAuthn se vinculan al RP ID. El registro de passkeys no cambia los certificados MySQL, mTLS ni las contraseñas MySQL. No se han configurado dominios o dispositivos reales del usuario durante las pruebas.

## Verificación

```powershell
cd web
npm run test:unit
npm run format:check
npm run build
cd ..
docker compose -p dbyson-e2e -f compose.e2e.yml up -d --build --wait --wait-timeout 180
cd web
npm run test:e2e
cd ..
docker compose -p dbyson-e2e -f compose.e2e.yml down -v
```

El entorno E2E es desechable y debe empezar con datos nuevos. Las credenciales de fixtures son únicamente de pruebas. La suite de integración y carreras está documentada en [Conectividad segura](CONECTIVIDAD_SEGURA.md). CI ejecuta las pruebas unitarias nuevas, las pruebas de navegador y las integraciones con MySQL/MariaDB.
