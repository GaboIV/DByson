# Vistas, preferencias y registros · 21 de octubre de 2026

## Pestañas principales y vistas de tabla

Cada tabla tiene una única pestaña principal por conexión y base de datos. Contenido, Estructura, Modificar y Nuevo registro son pestañas internas: cambiar de vista reutiliza la existente. SQL, el botón +, Ctrl+Alt+N y Editar en SQL crean consultas internas si el espacio activo pertenece a una tabla. Exportar también se abre dentro de esa tabla. Fuera de una tabla, una consulta conserva su propio espacio principal.

Los filtros, resultados, formularios y borradores se conservan mientras sus vistas están abiertas. Al volver a la pestaña principal se recupera su última vista activa. Las referencias guardadas con el formato anterior se agrupan por conexión/base/tabla. Al recargar se restauran destinos y tipos de vista; los borradores y resultados solo viven en memoria.

El botón × o el clic de la rueda cierran una pestaña principal y sus vistas. Si alguna vista tiene cambios pendientes, se solicita una sola confirmación; cancelar conserva todo el grupo. Cerrar una pestaña inactiva con la rueda mantiene la pestaña activa. Las consultas internas y Exportar también tienen cierre individual; cerrar una consulta deja activa otra vista de la misma tabla cuando existe. Conexiones permanece como acceso fijo.

Ambos niveles utilizan el mismo componente, altura, bordes, fondo y estado activo. Las flechas/Home/End navegan por cada barra; si hay muchas pestañas, se desplaza solo su barra horizontal para mostrar la activa. Se admiten 16 pestañas principales y 48 vistas en total. Cambiar la base mediante el selector o las migas de una tabla abre el explorador correspondiente, conservando la tabla original.

## Rueda de preferencias

La rueda está después del botón +. Guarda preferencias por usuario y origen del navegador:

- **Ver queries:** muestra u oculta el SQL devuelto para la vista actual.
- **Sólo ver:** protección voluntaria adicional para las nuevas solicitudes a bases de datos. Oculta controles de escritura y envía una marca que el servidor aplica tanto a sus comprobaciones como a la sesión MySQL. No cambia la configuración persistida de las conexiones ni cancela una operación ya enviada. Desactivarlo nunca anula el solo lectura propio del perfil. Esta preferencia no es un rol ni un mecanismo de autorización.
- **Consultas recientes:** hasta 50 sentencias que devuelve el servidor durante esta sesión, con tiempo y resultado. No se guardan en localStorage ni se exportan con conexiones. No sustituye la auditoría. Las vistas previas mayores de 12.000 caracteres se marcan como recortadas y no se pueden abrir como SQL ejecutable desde el historial. Abrir una consulta conserva la conexión/base de su origen. Las consultas parametrizadas pueden conservar marcadores `?` que requieren completar los valores.
- **Apariencia:** Sistema, Claro, Oscuro, Medianoche y Payaso. Los temas explícitos prevalecen sobre la preferencia del sistema operativo. Payaso diferencia pestañas, cabecera, lateral, SQL, filtros y paginación; conserva neutros los valores y el significado de los colores de peligro.
- **Tabla:** filas compactas, filas alternadas, acciones fijas al desplazar horizontalmente y numeración de filas.

Restablecer apariencia y tabla conserva la elección de Sólo ver. Las preferencias solo contienen opciones de interfaz; no incluyen contraseñas ni datos de registros.

## Acciones de fila

Se mantienen visibles **Detalle** y **Editar**. Copiar como JSON, Duplicar y Eliminar pasan a **Más acciones**, con texto explícito. El menú se coloca fuera del contenedor que desplaza la tabla para no quedar recortado, admite teclado y se cierra al desplazar/cambiar de tamaño.

Editar exige clave primaria y columnas completas en el resultado; duplicar exige las columnas completas; eliminar exige la clave completa y mantiene la confirmación del servidor. En solo lectura se conservan únicamente detalle y copia. Los resultados SQL libres también ofrecen detalle y un menú de copia.

## Inspector y edición

El detalle es una ficha lateral: identidad del registro, clave primaria, columnas de nombre/tipo y valor, búsqueda por campo o valor, filtros para NULL/claves/campos con valor, copia por campo y vista JSON. Los textos largos se pueden expandir; copiar utiliza el valor completo. Escape cierra la ficha y el foco regresa al control de origen.

La recuperación por clave primaria, los límites de resultado y la cancelación al cerrar conservan su comportamiento anterior. Sin una clave completa se identifican como columnas disponibles en la consulta, sin prometer recuperar campos omitidos.

Editar y Nuevo registro usan una distribución de nombre/tipo frente a control de valor, con búsqueda de campos, texto multilínea y controles NULL. Los campos autoincrementales/generados se identifican; se omiten las columnas generadas en los valores enviados al guardar. Los campos obligatorios sin valor predeterminado se pueden completar al insertar.

## Criterios de diseño

Se priorizaron tareas frecuentes visibles, opciones secundarias con texto, separación entre presentación y edición, y preferencias que afectan la consulta diaria. Se contrastaron las presentaciones de [DBeaver](https://dbeaver.com/docs/dbeaver/Data-View-and-Format/), su [editor de datos](https://dbeaver.com/docs/dbeaver/Data-Editor/) y las [preferencias de pgAdmin](https://www.pgadmin.org/docs/pgadmin4/latest/preferences.html); las decisiones concretas de interfaz son propias de DByson.

## Comprobaciones

Compilación TypeScript/Vite, go vet y suite Go. Una prueba adicional comprueba que Sólo ver bloquea SQL de escritura, creación de base, inserción y truncado sin conectarse al destino ni modificar el perfil.

Recorrido inicial Chrome contra Go y MySQL 8.4 desechables: vistas con estado independiente, filtro y DDL en borrador conservados, ficha/JSON/NULL, menús por fila, editar e insertar con columna generada, rechazo de un UPDATE en modo Sólo ver y comprobación final de los datos. Preferencias y tema sobreviven a recarga; consultas recientes y protección de SQL recortado comprobadas. Sin errores JavaScript registrados.

Se comprobó ausencia de scroll general, paginación visible y acciones fijas a 1440×900, 1366×768, 1024×768 y 960×600. Claro, oscuro y Payaso inspeccionados visualmente.


## Validación de la agrupación de pestañas

Recorrido adicional de Chrome contra MySQL 8.4 y Go desechables: una sola pestaña principal al abrir estructura, DDL, inserción, dos consultas y exportación; accesos +, Ctrl+Alt+N, SQL del menú y Editar en SQL. Conservación de filtro, DDL, formulario y consultas en borrador. Cierre con rueda de pestañas activas e inactivas y consultas internas; cancelación y aceptación del aviso de cambios, cierre con ×, regreso a la pestaña vecina y acceso fijo a Conexiones.

Migración de referencias antiguas y recarga, selección de otra base sin alterar el grupo original, navegación por teclado y coincidencia de altura/radio/borde/fondo de ambos niveles. Sin errores JavaScript. Sin scroll general y con paginación visible a 1440×900, 1366×768, 1024×768 y 960×600.

## Despliegue

Actualizado con pestañas agrupadas en `127.0.0.1:8082`: salud `ok`, HTTP 200 y bundle `index-BTegP37b.js`. Se conservaron configuración, volumen, setup y redes bridge/dbyson-discovery. El contenedor anterior quedó detenido como `adminbase-previous-tab-groups-20261021202054`. La detección Docker sigue activa. Las instancias de app/MySQL, red y datos anónimos de pruebas se retiraron. No se ejecutaron mutaciones contra las conexiones existentes del usuario.
