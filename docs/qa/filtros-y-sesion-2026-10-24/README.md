# Filtros y sesión vencida — 24 de octubre de 2026

La barra de filtros queda pegada a las pestañas, con padding únicamente lateral y separadores verticales entre grupos. Conserva los controles de 26 px y las pestañas compactas. Este ajuste sustituye el padding vertical descrito en `ajuste-filtros-2026-10-24`.

Los selectores de columna, operador, ordenación, base de datos y filas por página usan un desplegable del tema, con navegación por teclado, foco, selección visible y búsqueda en listas largas. Los demás formularios conservan sus controles actuales.

Al recibir el rechazo de una sesión vencida o revocada, la aplicación cancela las peticiones pendientes, cierra las confirmaciones y vuelve al login con el aviso correspondiente. También comprueba la sesión al regresar a una ventana tras al menos 30 segundos. No usa un sondeo periódico que mantenga viva una sesión inactiva. Una contraseña incorrecta en 2FA o un error de permisos 403 no provocan el cierre de sesión. Exportar comprueba la sesión antes de iniciar la descarga por streaming.

## Verificación

- Compilación TypeScript/Vite, formato y cuatro pruebas unitarias: correctos.
- Suite completa: **35 pruebas E2E aprobadas, sin fallos ni reintentos**, contra la imagen Docker reconstruida y una base MySQL 8.4 desechable; duración 1,7 minutos.
- Los seis casos nuevos usan respuestas API simuladas en la interfaz real: geometría en temas claro y oscuro a 1920, 1280 y 1024 px; desplegables, búsqueda y teclado; reingreso; errores 401/403; regreso a la ventana y cierre de confirmaciones; respuestas antiguas; descarga válida y sesión vencida al exportar.
- La prueba existente de revocación comprueba además el retorno al login en un segundo navegador con una sesión real del servidor, manteniendo válida la sesión que revoca.
- Las pruebas existentes cubren también edición, inserción, estructura, borradores, exportaciones CSV/JSON/SQL, solo lectura, SSH, cancelación SQL, herramientas de productividad y passkeys.

Capturas con datos ficticios: [tema claro](filtros-light.png), [tema oscuro](filtros-dark.png) y [aviso de sesión vencida](sesion-vencida.png).

Reproducción desde la raíz:

```powershell
docker compose -p dbyson-e2e -f compose.e2e.yml up -d --build --wait
Push-Location web
npm run test:e2e
Pop-Location
docker compose -p dbyson-e2e -f compose.e2e.yml down -v
```

La suite completa requiere un entorno desechable nuevo, sin un administrador configurado previamente.
