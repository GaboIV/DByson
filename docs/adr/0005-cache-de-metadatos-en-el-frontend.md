# 0005 · Caché compartida de metadatos en el frontend

- **Estado:** aceptada · 22 de octubre de 2026

## Contexto

Cada pestaña del espacio de trabajo (una vista de tabla, una consulta) es una instancia independiente de `Workspace`. Cada una pedía al montarse, **y cada vez que se reactivaba**, la lista de bases de datos, la de tablas y la estructura de la tabla, y volvía a leer los registros. Con servidores remotos (SSH) eso se notaba como recargas constantes.

## Decisión

- `web/src/workspace/catalog.ts`: caché por sesión, compartida entre pestañas, para **bases de datos, tablas y estructuras**. Se piden una vez; se recargan solo con *Actualizar* o tras una invalidación.
- **Invalidación por cambios:** DDL, inserciones, borrados, `TRUNCATE` y cualquier sentencia SQL que no sea de lectura invalidan lo afectado. Mientras se recarga, los datos anteriores siguen visibles.
- Los **registros** de una vista se recuerdan con la clave de sus ajustes (filtro, orden, página): reactivar la pestaña no vuelve a consultarlos.
- La caché se vacía al cerrar sesión.

## Consecuencias

- ✅ Cambiar de pestaña o de vista no genera peticiones; abrir una tabla nueva pide solo su estructura y sus registros.
- ⚠️ Cambios hechos **fuera de DByson** (otra herramienta, otro usuario) no se ven hasta pulsar *Actualizar*. Es el comportamiento habitual de los clientes de bases de datos.
