# DByson — Evaluación del plan maestro

> **Evaluación inicial histórica.** Las brechas de seguridad y experiencia aquí descritas se revisaron después. Para el estado vigente, consultar [plan maestro](PLAN_MAESTRO.md), [cierre MVP](MVP_SEGURIDAD.md), [experiencia de consulta](EXPERIENCIA_CONSULTA.md) y [SSH/espacios de trabajo](SSH_Y_ESPACIOS.md). Los apartados siguientes conservan el diagnóstico previo a esos cambios.

Fecha: 21 de octubre de 2026. Evaluación del árbol de trabajo local, incluidos los cambios todavía sin commit. La presencia de código indica implementación; no certifica el funcionamiento contra todos los motores ni un release publicado.

**Actualización posterior:** las cuatro brechas de solo lectura, límites, confirmaciones y streaming se abordaron en el [hito de seguridad del MVP](MVP_SEGURIDAD.md), con pruebas unitarias, integración MySQL/MariaDB y comprobación de interfaz. El diagnóstico que sigue conserva los hallazgos de la revisión inicial.

## Diagnóstico

El proyecto ya tiene una base funcional de administrador MySQL/MariaDB y supera un prototipo visual: autenticación, conexiones cifradas, explorador, edición de registros, consola y descargas. **Está en fase 1 avanzada, con fase 0 incompleta y algunos adelantos de fases 2 y 3.** La etiqueta anterior «Fase 1 completada» confundía funcionalidad existente con criterios de aceptación verificados.

DByson se implementa en Go + React; comparte la idea de uso sencillo de Adminer. No se encontró PHP ni una dependencia de Adminer en el repositorio revisado. Conviene describirlo como una herramienta inspirada en Adminer, salvo que exista código derivado fuera de este árbol.

## Avance por fase

| Fase | Estado observado | Evidencia y pendiente principal |
|---|---|---|
| 0 · Fundaciones | Parcial | Go + React/Vite embebido, Docker distroless no-root, CI con vet/tests/govulncheck/audit/Trivy y dos motores con datos de ejemplo. Faltan licencia, ADRs, modelo de amenazas, matriz real de integración y build multi-arquitectura. Los temas siguen la preferencia del sistema; no hay selector manual. |
| 1 · MVP MySQL | Avanzada, sin cierre validado | Login/setup, vault, CRUD de conexiones, TLS, bases/tablas/vistas, columnas/índices/DDL, paginación/filtros/orden, CRUD de filas, consola y exportación. Pendientes: robustez de solo lectura, límite real de resultados, confirmaciones y exportación de volumen. |
| 2 · Conectividad | Inicio parcial | `BuildTLS` ya admite certificado y clave de cliente MySQL y el formulario permite configurarlos. No se encontraron túneles SSH, ProxyJump, mTLS de acceso, TOTP, panel de sesiones ni auditoría. |
| 3 · Productividad | Adelantos puntuales | Hay creación de tablas, editor textual de ALTER con plantillas e historial SQL en memoria. No equivalen a un editor visual completo con previsualización y aprobación. Faltan importación, EXPLAIN visual, ER, navegación FK, consultas guardadas, paleta y pestañas persistentes. |
| 4 · Equipos | Pendiente | Existe campo de rol y aislamiento de perfiles por propietario; no se encontró gestión de equipo con RBAC por conexión/entorno, conexiones compartidas, OIDC ni intercambio cifrado. |
| 5+ · Expansión | Pendiente | Solo existe implementación MySQL/MariaDB. El contrato genérico de drivers del plan todavía no está implementado. |

Fuentes: [API](../internal/api/router.go), [explorador](../internal/drivers/mysql/explorer.go), [workspace](../web/src/Workspace.tsx), [TLS](../internal/drivers/mysql/connect.go), [CI](../.github/workflows/ci.yml), [Docker](../Dockerfile).

## Brechas que impiden cerrar el MVP

1. **Solo lectura no está reforzado en la sesión de base de datos.** Los handlers de mutaciones comprueban `rec.ReadOnly`, pero la consola depende de `IsReadOnlyViolation`, una lista de prefijos. No se establece la restricción de sesión prometida en el plan. Casos como comentarios ejecutables de MySQL, procedimientos o expresiones con efectos requieren una política explícita. Usar una cuenta MySQL sin permisos de escritura como barrera principal y validar las rutas con pruebas de integración. Evidencia: `openDB`, `handleQuery` en [handlers](../internal/api/explorer_handlers.go) y [política SQL](../internal/policy/sql.go).
2. **El límite de la consola es aparente.** `ExecuteSQL` recibe `maxRows` y le da un valor por defecto, pero `executeSelect` recorre y acumula todos los resultados. Aplicar límites reales de filas y bytes, informar truncamiento y probar la cancelación. El timeout de 60 segundos sí existe; no hay botón ni implementación de `KILL QUERY`.
3. **Las exportaciones aún no son streaming de extremo a extremo.** SQL y CSV llaman a `executeSelect`, que materializa la tabla; JSON además construye una lista de mapas. El SQL dump ignora algunos errores de lectura/escritura, pudiendo entregar un archivo incompleto. CSV/JSON sin tabla seleccionada exportan solo la primera tabla disponible, aunque la pantalla habla de toda la base. Exigir selección explícita o generar un archivo por tabla y comprobar restauración, errores y consumo de memoria.
4. **El clasificador SQL no es un parser.** `WHERE` dentro de un literal puede hacer que un UPDATE masivo parezca acotado. La eliminación de comentarios tampoco respeta literales. Las confirmaciones usan booleanos y diálogos del navegador; ALTER envía `confirmDestructive: true` directamente. Implementar confirmación contextual con nombre de objeto, análisis de sentencias y validación en servidor. No presentar el clasificador como una garantía de seguridad.
5. **Entrada SQL sin estructurar en rutas auxiliares.** `CustomWhere` se concatena en la consulta de lectura; `CreateTable` compone tipos, longitudes y valores por defecto enviados por el cliente. Revisar estas rutas junto con las políticas: la consola puede aceptar SQL libre, pero un filtro o formulario visual necesita contratos estructurados y validación del servidor.
6. **La CSP y los estilos inline requieren reconciliarse.** `securityHeaders` establece `style-src 'self'`, mientras el workspace usa numerosos atributos React `style`, incluida la posición del selector de columnas. Probar el frontend servido por Go con sus headers reales y mover los estilos a una solución compatible con esa política sin abrirla de forma general.
7. **Falta evidencia de integración y release.** Hay pruebas unitarias de auth, vault, hosts, utilidades SQL y API; no se encontró Playwright configurado, Testcontainers ni matriz MySQL/MariaDB ejecutada por CI. Tampoco evidencia local suficiente para certificar `v0.1.0`, tamaño de imagen o benchmarks. Tener un workflow no demuestra que haya pasado.

## Cambios realizados en esta revisión

- Identidad DByson y logo original integrados en acceso, cabecera y favicon. Los identificadores técnicos `ADMINBASE_*`, módulo Go, comando y volumen siguen vigentes para conservar compatibilidad; su migración requiere una decisión separada.
- Altura de aplicación ligada al viewport y cadena de contenedores flexibles con `min-height: 0`.
- Lista lateral y grilla con desplazamiento propio; paginación fuera de la zona desplazable de registros.
- Accesos laterales distribuidos en dos columnas, sin recortar Exportar; filtros y pestañas se ajustan a varias filas cuando falta ancho.
- Estructura, ALTER, consola y exportación cuentan con una zona de desplazamiento propia.

Validación realizada: compilación TypeScript + Vite; comprobación de la interfaz compilada en Chrome con API simulada, 45 tablas, 24 columnas y 50 filas, a 1920×968, 1366×768, 1024×768, 768×600 y 960×480. En esos tamaños no hubo desbordamiento general, la paginación quedó visible y los cuatro accesos laterales quedaron completos. También se comprobó el scroll de estructura y no se registraron errores JavaScript. Esta comprobación no sustituye una prueba de integración contra Go y MySQL ni cubre dispositivos móviles estrechos.

No se ejecutaron pruebas Go: el ejecutable no está disponible en PATH en este entorno. Docker no estuvo accesible desde el entorno restringido. No se verificó ni reinició el despliegue mostrado en `localhost:8082`.

## Orden recomendado del siguiente trabajo

### Hito A · Cerrar un MVP confiable

- [ ] Resolver las brechas 1–6 y añadir pruebas de los casos que las reproducen.
- [ ] Flujo integrado: setup → crear/probar conexión → explorar → editar fila → ejecutar SQL → exportar/restaurar.
- [ ] Ejecutar ese flujo en MySQL 8.4 y MariaDB 11.4 como primera matriz; ampliar versiones solo con resultados publicados.
- [ ] Añadir licencia, ADRs y modelo de amenazas mínimo; comprobar el pipeline y documentar cómo actualizar/resguardar SQLite y la clave maestra.
- [ ] Definir criterios de salida y publicar `v0.1.0` solo cuando se cumplan.

### Hito B · Trabajo diario cómodo

- [ ] Separar filtros en borrador de filtros aplicados: actualmente escribir cambia dependencias de carga y dispara consultas; usar Mostrar para aplicar y cancelar respuestas obsoletas.
- [ ] Evitar `COUNT(*)` obligatorio al abrir tablas grandes: ofrecer total estimado o conteo explícito y paginación por cursor cuando exista una clave estable.
- [ ] Editor SQL con selección ejecutable, errores claros y consultas guardadas; después, pestañas persistentes y paleta.
- [ ] Detalle de fila, copiar celda/fila y navegación por FK antes de un editor visual de esquema completo.
- [ ] Dividir `Workspace.tsx` por vistas y separar obtención de datos de formularios; facilitará añadir funciones y pruebas.

### Hito C · Conectividad y expansión

- [ ] SSH con verificación de huellas y pruebas, auditoría y TOTP antes del uso remoto compartido.
- [ ] Definir contrato de capacidades por driver; después añadir PostgreSQL. Evitar mostrar acciones que el motor no soporte.
- [ ] Mantener ER, IA, escritorio, plugins y administración avanzada en backlog hasta cerrar los hitos anteriores.

## Identidad y alcance

La esfera de Dyson funciona como visión de una consola que reúne herramientas alrededor de los datos. Conviene expresarla en la marca y en la navegación coherente, manteniendo una primera promesa concreta: **administrar MySQL/MariaDB con rapidez y cambios comprensibles**.

El logo completo ilustra varios motores futuros. Mostrar capacidades reales por conexión evitará que esa visión se interprete como soporte ya disponible. A 30 px se pierde parte de su detalle; una variante simplificada para favicon y cabeceras sería un trabajo de diseño posterior. No se verificaron disponibilidad legal de marca, dominios ni namespaces.
