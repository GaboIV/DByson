# Equipo y uso compartido · 26 de octubre de 2026

## Cambios

- Equipo: resumen, búsqueda, filtros, estados y roles con controles del tema.
- Alta de personas en un formulario desplegable; cambio de contraseña en un
  diálogo con validación y opción de mostrarla. Confirmaciones dentro de la app.
- Protección visible de la propia cuenta y la última administración activa.
- Compartir con el equipo y Gestionar acceso como acciones de la tarjeta.
- Permisos por persona, acceso heredado explicado y revocación confirmada.
- Desactivar el uso compartido revoca permisos en una transacción sin modificar
  el perfil ni las credenciales. Reactivarlo no restaura permisos anteriores.
- Directorio mínimo para miembros con permiso de gestión; sin acceso a la
  administración de cuentas. Solo lectura no gestiona permisos.
- Exportar archivo diferenciado del acceso al equipo; credenciales disponibles
  únicamente cuando se pueden gestionar todos los perfiles seleccionados.

## Verificación

- TypeScript de aplicación y pruebas: aprobado.
- Compilación de producción: aprobada. Se mantiene el aviso de tamaño del
  paquete JavaScript.
- 7 pruebas unitarias del frontend: aprobadas.
- 15 pruebas de navegador: aprobadas (7 de equipo/compartir/exportar y 8 de
  regresión de sesión/filtros), con respuestas de API simuladas.
- `go test ./... -count=1`: aprobado en el contenedor local Go 1.26. Las pruebas
  que requieren servicios externos conservan sus condiciones de ejecución.
- La prueba del servidor cubre aislamiento del directorio, niveles efectivos,
  restricciones de Solo lectura, conservación de credenciales y revocación.
- Claro y oscuro, anchos 1366, 768, 390 y 320 px: sin desbordamiento horizontal
  en ambos diálogos. Capturas inspeccionadas visualmente.

## Capturas

Las capturas usan cuentas y conexiones ficticias de las pruebas.

- [Equipo en escritorio](equipo-escritorio.png)
- [Equipo en móvil](equipo-movil.png)
- [Compartir en escritorio](compartir-escritorio.png)
- [Compartir en móvil](compartir-movil.png)
