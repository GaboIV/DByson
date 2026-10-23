# Validación de DByson v0.1.0

Fecha: 23 de octubre de 2026. Nombre: **DByson**, alegoría de la esfera de Dyson.

Resultado local: **1 prueba E2E completa aprobada en Chromium**, contra la imagen Docker y MySQL 8.4. Compilación del frontend, formato y construcción Docker correctos. Pendiente de ejecutar este nuevo trabajo en GitHub tras subir los cambios; no confundir con el CI anterior de main.

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

## Recorrido manual pendiente

Realizarlo sobre datos desechables y registrar navegador, commit, fecha y resultado. Una prueba automática no marca estos pasos como revisados por una persona.

- [ ] Logo legible en login y cabecera; favicon correcto; revisar temas claro y oscuro.
- [ ] Modificar: añadir una columna, cancelar primero la confirmación y comprobar que no cambia nada; confirmar después y verificar Estructura.
- [ ] Modificar: enviar SQL inválido y comprobar un error comprensible y que se puede corregir.
- [ ] Nuevo registro: insertar, verificar el registro desde Contenido y comprobar valores NULL y campos autogenerados.
- [ ] Cambiar de pestaña con cambios pendientes: verificar aviso y opciones de conservar/descartar.
- [ ] Exportar: abrir CSV, JSON y SQL; comparar valores, tildes, comillas y saltos de línea. Revisar solo estructura, solo datos y ambas opciones de SQL.
- [ ] Solo lectura: comprobar que Modificar y Nuevo registro no permiten escribir.
- [ ] Editor SQL: resaltado, autocompletado, ejecutar selección y varias sentencias, atajo Ctrl+Enter y errores.

## Comprobación del nombre

Comprobar identificadores técnicos y marca por separado. No se ha comprado ni registrado ningún nombre.

| Área | Evidencia / estado |
|---|---|
| GitHub | La búsqueda de repositorios `dbyson in:name` devolvió únicamente `GaboIV/DByson`. El repositorio ya existe bajo la cuenta del proyecto. |
| GHCR | La API de paquetes respondió 404 para `GaboIV/dbyson` (no hay un paquete visible con la cuenta actual). Pendiente de primera publicación y verificación en `ghcr.io/gaboiv/dbyson`; el nombre está bajo el espacio de la cuenta, no es un identificador global reservado por esta búsqueda. |
| Dominio | Se consultó **dbyson.com** como candidato: RDAP de Verisign respondió HTTP 404. No se encontró un registro en esa consulta; confirmar disponibilidad y precio en el registrador al comprar. Otros TLD no comprobados. |
| Marca | Pendiente de búsqueda de denominaciones y semejanzas en WIPO y registros de los mercados objetivo. WIPO presentó CAPTCHA; no se completó la consulta. La ausencia de resultados web no equivale a disponibilidad de marca. |

Fuentes: [GitHub](https://github.com/search?q=dbyson&type=repositories), [RDAP .com](https://rdap.verisign.com/com/v1/domain/dbyson.com), [WIPO](https://branddb.wipo.int/).

## Cierre

- [ ] CI verde en main con estas pruebas integradas.
- [ ] Recorrido manual completado, con resultados registrados.
- [ ] Nombre: resolver comprobaciones pendientes y decidir dominio/mercados.
- [ ] Logo nuevo integrado y revisado visualmente.
- [ ] Solo entonces crear `v0.1.0` y verificar imagen, firma y procedencia.
