# Resultados de aceptación de DByson v0.1.0

Fecha: 23 de octubre de 2026. Base: `main` en `22c7c76`, más las correcciones locales descritas abajo. Rama: `codex/acceptance-validation`.

## Entorno y método

Windows, Chromium de Playwright 1.64.1, imagen Docker real de DByson y MySQL 8.4. Puerto local 18080. Contenedores y datos desechables del proyecto Compose `dbyson-e2e`; sin volúmenes de desarrollo. Cada ejecución completa recreó los datos y la cuenta de prueba.

El agente recorrió la interfaz con Playwright e inspeccionó capturas. Las operaciones que debían cambiar o conservar datos se contrastaron además mediante consultas directas a MySQL. Esta es una validación asistida por el agente, no una declaración de revisión por una persona.

**Ejecución final: 9 aprobadas, 0 fallidas, 28,0 segundos**, sin reintentos automáticos. Comprobación de tipos E2E, formato y construcción Docker aprobados.

## Resultados

| Grupo | Comprobaciones | Resultado |
|---|---|---|
| Flujo principal | Setup, crear/probar conexión, abrir tabla, editar, insertar, ALTER, SQL y descargar tres formatos | Aprobado |
| Nuevo registro | ID automático, tildes, comillas, texto multilínea, NULL real, código único duplicado, conservar formulario y corregir | Aprobado tras corregir un mensaje de éxito obsoleto |
| Editar y cancelar | Guardar solo la fila elegida; cancelar descarte conserva texto; aceptar descarte conserva el valor anterior en MySQL | Aprobado |
| Estructura | Cancelar no añade columna; destino incorrecto deshabilita confirmación; confirmar añade columna; error duplicado y recuperación; campos actualizados | Aprobado |
| Borradores | Conservar SQL/INSERT/ALTER al cambiar de vista; cancelar cierre; aceptar descarte de consulta y grupo; cancelar recarga del navegador | Aprobado |
| Editor SQL | Ejecutar selección, dos sentencias con resultados separados, Ctrl+Enter, error y recuperación, sugerencias de tabla y columnas de alias, descargar/abrir archivo, resaltado | Aprobado tras corregir prioridad del atajo |
| CSV | Interpretar campos con comas, comillas y saltos de línea; comparar dos registros completos | Aprobado. El formato representa NULL con el texto `NULL` |
| JSON | Comparar valores Unicode y multilínea, dos registros y `null` JSON | Aprobado |
| SQL exportado | Estructura+datos, solo estructura, solo datos y ninguna opción; restaurar descarga en otro esquema y comparar bytes y NULL | Aprobado |
| Exportación incompleta | CSV/JSON sin tabla y SQL sin estructura ni datos no tienen destino de descarga | Aprobado |
| Sólo ver global | Desaparecen controles de escritura; UPDATE rechazado; comprobar datos intactos; SELECT y exportación permitidos | Aprobado |
| Perfil solo lectura | Preferencia global desactivada; escritura sigue bloqueada por el perfil; datos intactos y SELECT permitido | Aprobado |
| Presentación | Logo, favicon y capturas de tabla, formulario, confirmación y error en claro y oscuro | Aprobado en 1280×720, sin recortes ni superposiciones observadas |

## Evidencia visual

Se inspeccionaron las once capturas guardadas: [login](qa/2026-10-23/logo-login.png), [editor SQL](qa/2026-10-23/editor-sql.png), [exportación](qa/2026-10-23/exportaciones.png), [tabla clara](qa/2026-10-23/tabla-Claro.png), [tabla oscura](qa/2026-10-23/tabla-Oscuro.png), [formulario claro](qa/2026-10-23/formulario-Claro.png), [formulario oscuro](qa/2026-10-23/formulario-Oscuro.png), [confirmación clara](qa/2026-10-23/confirmacion-Claro.png), [confirmación oscura](qa/2026-10-23/confirmacion-Oscuro.png), [error claro](qa/2026-10-23/error-Claro.png) y [error oscuro](qa/2026-10-23/error-Oscuro.png).

## Fallos encontrados y corregidos

1. **Mensaje de éxito obsoleto en Nuevo registro.** Tras una inserción correcta, un intento duplicado mostraba el error junto al éxito de la operación anterior. `InsertRecord.tsx` limpia el aviso al iniciar cada intento. La prueba reproduce éxito → duplicado → recuperación y exige que desaparezca el aviso anterior.
2. **Ctrl+Enter insertaba una línea en el editor SQL.** El mapa de teclas de `basicSetup` atendía primero el atajo. `SqlEditor.tsx` asigna prioridad máxima al comando de ejecutar. La prueba selecciona una sentencia, comprueba su resultado y luego ejecuta todo con Ctrl+Enter y verifica dos resultados.

Durante la preparación se corrigió también el fixture SQL con `SET NAMES utf8mb4`, y el lector de comprobación declara UTF-8 para comparar los bytes reales. Eso pertenece al entorno de pruebas.

## Reproducción

Seguir [VALIDACION_V0.1.0.md](VALIDACION_V0.1.0.md). `npm run test:e2e` comprueba los tipos del código de pruebas y ejecuta nueve casos. El flujo principal prepara la cuenta/conexión que utilizan los ocho casos adicionales; ejecutar la batería completa desde un servidor fresco.

Los datos de Contenido se actualizaron con el botón **🔄**, «Volver a leer los registros del servidor». **Mostrar** aplica filtros; repetir los mismos filtros no obliga a recargar los datos. Cambiar de vista conserva borradores; la advertencia aparece al cerrar o recargar.

## Estado para integrar

Las correcciones y la batería ampliada están locales, sin commit. Antes de etiquetar la versión, integrarlas y confirmar el CI de ese resultado en `main`. El CI anterior aprobado no incluye estas dos correcciones. El entorno desechable se retira al terminar.
