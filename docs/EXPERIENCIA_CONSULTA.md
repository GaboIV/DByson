# Experiencia de consulta · 21 de octubre de 2026

## Filtros aplicados de forma explícita

La condición, el operador, el valor, las columnas, el orden, el límite y la longitud de texto del formulario son un borrador. Solo **Mostrar** o Enter aplican ese borrador y vuelven a la primera página. La interfaz indica «Cambios sin aplicar» cuando el formulario difiere de la consulta visible. **Limpiar** restablece y aplica los valores iniciales.

Los clics en cabeceras y el selector de filas por página son acciones directas: se aplican al resultado actual sin incorporar por accidente una condición pendiente del formulario. Cuando no se elige orden, se usa la primera columna de la clave primaria si existe.

## Cargas y cancelación

Las solicitudes de datos, tablas, bases, conteo y detalle usan `AbortController`. Una nueva carga sustituye a la anterior; abandonar la vista cancela su solicitud. Las respuestas canceladas no actualizan datos, errores ni el indicador de carga. Una búsqueda aplicada produce una sola carga de datos; inicializar el selector de columnas no dispara otra consulta.

El servidor conserva sus contextos con timeout y pasa la cancelación al driver. Se verificó la cancelación de una consulta `SELECT SLEEP(10)` en curso en MySQL y MariaDB: la llamada Go terminó aproximadamente 100 ms después de iniciar la prueba de cancelación. Esto no añade todavía un botón de `KILL QUERY` a la consola SQL.

## Navegar sin un conteo obligatorio

La carga normal de filas no ejecuta `COUNT(*)`. Pide como máximo una fila adicional para habilitar **Siguiente**. Muestra filas recibidas y posiciones, sin inventar un total conocido.

**Contar coincidencias** hace el conteo exacto solo a petición, con los filtros aplicados. El conteo tiene su propio estado, error y botón de cancelación; no bloquea los registros ya disponibles. Cambiar los filtros invalida el conteo anterior. Paginar, cambiar columnas u ordenar puede conservarlo porque las coincidencias no cambian. Las mutaciones desde el formulario invalidan el total.

El cliente conserva los desplazamientos de páginas anteriores. La siguiente página comienza tras las filas realmente devueltas, incluso si el límite de 8 MiB redujo su tamaño. Una fila individual demasiado grande muestra un aviso y no permite avanzar en un bucle sin progreso.

Esta es paginación por desplazamiento, no por cursor: páginas muy profundas todavía pueden ser costosas, y cambios concurrentes pueden desplazar registros entre páginas. La paginación por cursor y una instantánea consistente siguen siendo trabajo futuro; el conteo es exacto en el momento en que se solicita.

## Copiar y consultar registros

- Cada celda ofrece **Copiar valor completo** al pasar el cursor o enfocarla con teclado. Copia el dato original, aunque la grilla lo muestre recortado.
- Cada fila ofrece **Copiar fila como JSON**, preservando NULL, cadenas vacías y valores de las columnas recibidas.
- En resultados SQL con alias repetidos, la copia usa `{ columns, values }` para no perder campos por sobrescritura de nombres.
- **Ver detalle** abre un panel con búsqueda por nombre de campo, texto multilínea, presentación legible de JSON válido y copia por campo o registro.
- Si se dispone de la clave primaria completa de la tabla, el detalle recupera todas sus columnas con una consulta parametrizada y limitada, aunque la grilla tenga una proyección parcial. Sin clave completa, o en resultados SQL arbitrarios, muestra claramente los valores disponibles en el resultado.
- Cerrar el panel cancela su carga, y Escape también lo cierra. El foco se conserva dentro del diálogo y se devuelve al control de origen al terminar.
- Las acciones de edición/clonado se deshabilitan si faltan columnas en la proyección, para no sobrescribir campos omitidos. La eliminación exige que esté visible la clave primaria completa.

## API y pruebas

La respuesta de navegación incorpora `hasMore`, `nextOffset` y `totalRowsKnown: false`; `totalRows` ya no debe interpretarse como un conteo exacto de la tabla en esta ruta. Los clientes existentes que dependan de él deben solicitar el conteo explícito.

- `GET .../tables/{table}/data`: acepta `offset` y conserva `page` como alternativa; no cuenta filas.
- `GET .../tables/{table}/count`: cuenta con el mismo constructor de filtros estructurados.
- `POST .../tables/{table}/row`: recibe la clave primaria completa, la contrasta con el esquema y devuelve como máximo un registro con los límites de lectura existentes. Requiere sesión, propiedad de la conexión y CSRF.

Validación: compilación TypeScript/Vite, `go vet`, suite Go, integración MySQL 8.4/MariaDB 11.4 para navegación, conteo filtrado, detalle y cancelación, además de las protecciones previas del MVP. Pruebas unitarias verifican ausencia de conteos durante la navegación, detección del final de página, desplazamiento tras recorte por bytes y rechazo de offsets inválidos.

Comprobación en Chrome contra una instancia y base desechables: edición de borradores sin solicitudes, una carga al aplicar, ausencia de conteo automático, avance/retroceso sin total, cancelación y descarte de respuesta retrasada, conteo opcional/cancelable, copia completa de textos recortados, preservación de NULL y cadena vacía, recuperación de seis columnas desde una proyección de dos, búsqueda en detalle, aliases repetidos y disposición a 1366×768 y 960×480. Sin errores JavaScript registrados.

Despliegue local comprobado en `127.0.0.1:8082`: salud `ok`, aplicación HTTP 200 y bundle actualizado. Se conservaron volumen, configuración y setup; el contenedor anterior quedó detenido como `adminbase-previous-query-20261021164208`. Las instancias y la red desechables de prueba fueron retiradas.
