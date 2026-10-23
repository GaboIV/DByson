# 0003 · Auditoría append-only con cadena HMAC en SQLite

- **Estado:** aceptada · 21 de octubre de 2026

## Contexto

Con túneles SSH, DByson llega a servidores de producción. Hace falta saber quién hizo qué, y que ese registro no se pueda retocar sin que se note. No queremos añadir infraestructura (otra base de datos, un servicio de logs) para la versión personal.

## Decisión

- Tabla `audit_log` en el mismo SQLite, con **triggers que rechazan `UPDATE` y `DELETE`**.
- Cada entrada lleva un **HMAC-SHA256** sobre su contenido y el HMAC de la entrada anterior, con una subclave derivada de la clave maestra. `GET /api/audit/verify` recorre la cadena.
- Se registran accesos (incluidos fallidos y bloqueados), cambios de 2FA, cambios en conexiones, sentencias SQL, cambios de datos y exportaciones. **No** se registran lecturas de tablas (ruido sin valor proporcional).
- El SQL se guarda con **contraseñas ocultas** (`IDENTIFIED BY '***'`, etc.) y truncado a 8.000 caracteres.

## Consecuencias

- ✅ Editar, borrar o reordenar entradas a nivel de archivo rompe la cadena y se detecta; falsificarla exige la clave maestra.
- ⚠️ **Truncar el final** de la cadena no se detecta todavía: hace falta anclar el último hash fuera de la máquina.
- ⚠️ Quien controla el host puede borrar el archivo completo. Para cumplimiento real la auditoría debe copiarse a un sistema externo (pendiente, Fase 4).
- ⚠️ Si la escritura de auditoría falla, la operación ya ocurrió: se registra en el log del servidor en lugar de revertirla.
