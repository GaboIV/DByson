# Ajuste de pestañas y filtros

Se recupera la forma de pestaña con borde y esquinas superiores; la altura baja de 32 a 28 px. Los filtros bajan de 32 a 26 px, con anchos de 45–112 px según el campo. Las acciones laterales y la búsqueda conservan la alineación con sus filas.

Validado con Chromium usando los componentes reales y respuestas API simuladas, sin conexiones reales: temas claro y oscuro a 1920, 1280 y 1024 px. Se comprobaron alturas, anchos, bordes, ausencia de desbordamiento horizontal y navegación Contenido/Estructura. Build TypeScript/Vite correcto. No se repitió la suite completa de backend para este ajuste de CSS.

Capturas: `pestanas-light-1920.png`, `pestanas-light-1280.png`, `pestanas-light-1024.png` y sus equivalentes `dark`.
