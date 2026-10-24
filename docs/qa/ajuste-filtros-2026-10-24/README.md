# Centrado vertical de filtros

El padding inferior exclusivo desplazaba visualmente los controles hacia arriba. La barra usa ahora 3,2 px de padding tanto arriba como abajo, conservando los campos de 26 px. La búsqueda lateral recibe el mismo espaciado para conservar la alineación.

Comprobación con Chromium y respuestas API simuladas en los componentes reales: temas claro y oscuro a 1920, 1536 y 1024 px. Padding simétrico, desviación de centrado dentro de cada grupo menor de 0,01 px, alineación con búsqueda lateral a 1 px y sin desbordamiento horizontal. A 1024 px los grupos se distribuyen en dos filas. Build TypeScript/Vite y formato CSS correctos.

Capturas: [claro](filtros-light.png) y [oscuro](filtros-dark.png).
