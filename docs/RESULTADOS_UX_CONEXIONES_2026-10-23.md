# Acceso y conexiones: distribución y verificación SSH

Rama: `codex/connection-ux`, a partir de `c9e8a5d` (`v0.1.0`).

## Cambios

- Acceso adaptable: dos columnas en escritorio y formulario con marca en móvil; desplazamiento disponible en pantallas bajas y en la verificación en dos pasos.
- Portada compacta: título, contadores y acciones juntos; buscador y filtros inmediatamente encima de las conexiones. Más tarjetas por fila en pantallas grandes y acciones alineadas al pie.
- Asistente más ancho, campos en columnas, accesos rápidos junto al destino y búsqueda de servicios desplegable. En móvil se reorganizan los campos y los pasos. El pie con Anterior/Continuar permanece accesible al desplazarse.
- SSH y bastión en tarjetas adaptables, con autenticación y archivos distribuidos en columnas. Los certificados MySQL también aprovechan el ancho disponible.
- Checkbox, radio, selector de archivos y campos adaptados a los colores del tema, con estados de foco, deshabilitado e indeterminado. Se conserva el uso nativo de los controles en modo de colores forzados.

## Cómo funciona la confirmación SSH

Al continuar desde Ruta y SSH, o intentar saltar a Protección/Comprobar, el asistente consulta las identidades que todavía no tienen una huella fijada. Primero solicita confirmar el bastión, si existe, y después el destino.

El modal muestra host, puerto, algoritmo y huella SHA-256. Ofrece **Cancelar** y **Confiar y continuar**. Cancelar detiene la consulta en curso y conserva el formulario. Un error permite volver al formulario y corregir o reintentar.

Las huellas aceptadas se propagan tanto a Probar como a Guardar. Cambiar el host o el puerto elimina su huella anterior y exige una nueva confirmación. Las huellas ya fijadas se conservan al editar; Revisar identidad permite contrastarlas nuevamente y avisa si cambiaron. También se puede introducir una huella conocida.

## Validación

Entorno Docker desechable con MySQL 8.4 y Chromium mediante Playwright. Compilación de frontend y backend correcta; comprobaciones de TypeScript, formato y `git diff --check` correctas.

Resultado final: **18 aprobados, 0 fallidos**, sin reintentos, en 1,4 minutos. Se inspeccionaron visualmente las capturas seleccionadas.

La suite contiene **18 casos**:

- Los nueve casos originales de aceptación con API y MySQL reales: setup, conexión, tablas, registros, estructura, SQL, exportación, borradores y solo lectura.
- Cuatro casos de interacción SSH: aceptar/cancelar, propagación de huellas a prueba y guardado, bastión y destino en orden, recuperación de errores, edición, cambio de identidad y cancelación durante la consulta.
- Cinco casos de distribución: 1920×1080, 1366×768, 768×1024, 390×844 y 320×568. Cada uno revisa acceso, portada, los cuatro pasos y el modal en los temas claro y oscuro; comprueba desbordamientos horizontales, tamaño de controles y acceso al pie del formulario.

Los casos nuevos usan perfiles y respuestas SSH simulados para comprobar el flujo del navegador de forma reproducible. No prueban un túnel SSH real. Las funciones pendientes de la Fase 2 conservan su alcance original.

## Capturas

Se generaron capturas para ambos temas y los cinco tamaños. Esta selección permite revisar el resultado:

- [Acceso en escritorio](qa/conexiones-2026-10-23/acceso-escritorio.png)
- [Portada en escritorio](qa/conexiones-2026-10-23/conexiones-escritorio.png)
- [Destino en portátil](qa/conexiones-2026-10-23/destino-portatil.png)
- [Configuración SSH](qa/conexiones-2026-10-23/ssh-portatil.png)
- [Certificados MySQL](qa/conexiones-2026-10-23/proteccion-portatil.png)
- [Portada en móvil](qa/conexiones-2026-10-23/conexiones-movil.png)
- [Destino en móvil](qa/conexiones-2026-10-23/destino-movil.png)
- [Confirmación SSH en móvil](qa/conexiones-2026-10-23/identidad-movil.png)

## Repetir la suite

Desde la raíz del repositorio:

```powershell
docker compose -p dbyson-e2e -f compose.e2e.yml up -d --build --wait --wait-timeout 180
cd web
npm ci
npx playwright install chromium
npm run test:e2e
cd ..
docker compose -p dbyson-e2e -f compose.e2e.yml down -v
```

Cada ejecución completa requiere un entorno desechable nuevo, porque el primer caso crea el administrador y los siguientes modifican sus datos de prueba.
