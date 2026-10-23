# Validación de DByson v0.1.0

Fecha: 23 de octubre de 2026. Nombre: **DByson**, alegoría de la esfera de Dyson.

Resultado local: **9 pruebas aprobadas en Chromium**, contra la imagen Docker y MySQL 8.4, incluida la batería de aceptación y revisión visual de capturas. Detalle y correcciones: [resultados del 23 de octubre](RESULTADOS_ACEPTACION_2026-10-23.md). El CI de `main` en `22c7c76` ya pasó, según la confirmación y captura del usuario. Las dos correcciones nuevas y la batería ampliada aún deben integrarse y pasar CI.

## Pruebas automatizadas

El flujo Playwright usa Chromium contra la imagen real y MySQL 8.4. No usa respuestas simuladas ni los volúmenes de desarrollo. Crea administrador y conexión desde la interfaz, prueba la conexión, abre una tabla, edita e inserta registros, cancela y confirma un ALTER TABLE, consulta SQL y comprueba el contenido descargado en CSV, JSON y SQL.

Desde la raíz, con Docker disponible:

```powershell
docker compose -p dbyson-e2e -f compose.e2e.yml up -d --build --wait --wait-timeout 180
cd web
npm ci
npx playwright install chromium
npm run test:e2e
cd ..
docker compose -p dbyson-e2e -f compose.e2e.yml down -v
```

Esperar a que `http://127.0.0.1:18080/api/healthz` responda antes de ejecutar las pruebas. Cada ejecución requiere recrear ambos contenedores (`down -v` y `up`): el setup y los registros parten de cero. El puerto 18080 debe estar libre. Las credenciales del fixture son exclusivamente para este entorno local desechable.

El CI ejecuta este flujo antes del trabajo de imagen y guarda informe, captura y traza si falla. Los artefactos de diagnóstico pueden contener los datos de prueba; no usar credenciales reales.

## Recorrido de aceptación completado por el agente

Ejecutado sobre datos desechables mediante Chromium automatizado e inspección de capturas. Fecha, base y resultados registrados en el informe enlazado. No se atribuye una revisión personal al usuario.

- [x] Logo legible en login y cabecera; favicon correcto; temas claro y oscuro.
- [x] Modificar: cancelar, comprobar que no cambia nada, confirmar y verificar Estructura.
- [x] Modificar: error de columna duplicada y recuperación con otra sentencia.
- [x] Nuevo registro: insertar, actualizar Contenido, comprobar NULL y campos autogenerados; duplicado y recuperación.
- [x] Cambiar de pestaña conserva borradores; cerrar o recargar advierte; cancelar conserva y aceptar descarta.
- [x] CSV, JSON y SQL: valores, tildes, comillas, saltos de línea y opciones de estructura/datos. SQL restaurado y comparado con el original.
- [x] Solo lectura: preferencia global y perfil independiente bloquean escritura; lectura permitida.
- [x] Editor SQL: resaltado, sugerencias de tablas y columnas, selección, varias sentencias, Ctrl+Enter, errores y archivos.

## Comprobación del nombre

Comprobar identificadores técnicos y marca por separado. No se ha comprado ni registrado ningún nombre.

| Área | Evidencia / estado |
|---|---|
| GitHub | La búsqueda de repositorios `dbyson in:name` devolvió únicamente `GaboIV/DByson`. El repositorio ya existe bajo la cuenta del proyecto. |
| GHCR | La API de paquetes respondió 404 para `GaboIV/dbyson` (no hay un paquete visible con la cuenta actual). Pendiente de primera publicación y verificación en `ghcr.io/gaboiv/dbyson`; el nombre está bajo el espacio de la cuenta, no es un identificador global reservado por esta búsqueda. |
| Dominio | **dbyson.com** disponible según comprobación del usuario en tld-list y captura aportada el 23 de octubre. La consulta previa de RDAP respondió 404. Compra no reportada. |
| Marca | Pendiente de búsqueda de denominaciones y semejanzas en WIPO y registros de los mercados objetivo. WIPO presentó CAPTCHA; no se completó la consulta. La ausencia de resultados web no equivale a disponibilidad de marca. |

Fuentes: [GitHub](https://github.com/search?q=dbyson&type=repositories), [RDAP .com](https://rdap.verisign.com/com/v1/domain/dbyson.com), [WIPO](https://branddb.wipo.int/).

## Cierre

- [x] CI verde en main con el primer flujo E2E integrado (`22c7c76`, confirmado por el usuario).
- [ ] Integrar las dos correcciones y la batería ampliada; confirmar su CI en main.
- [x] Recorrido asistido por el agente completado, con resultados y capturas registrados.
- [ ] Nombre: resolver comprobaciones pendientes y decidir dominio/mercados.
- [x] Logo nuevo integrado y revisado visualmente.
- [ ] Solo entonces crear `v0.1.0` y verificar imagen, firma y procedencia.
