# 0002 · Vault con cifrado de sobre y clave maestra externa opcional

- **Estado:** aceptada · 21 de octubre de 2026

## Contexto

DByson guarda credenciales de bases de datos de producción (contraseñas, certificados, claves SSH) y secretos TOTP. Un robo del archivo SQLite no debe entregarlas en claro, y debe ser posible rotar la protección sin reescribir el diseño.

## Decisión

- **Cifrado de sobre:** cada registro tiene su propia clave de datos aleatoria (DEK), envuelta con la clave maestra (KEK). Algoritmo **XChaCha20-Poly1305** (nonce de 192 bits aleatorio, sin riesgo práctico de colisión).
- El **ID del registro** se usa como dato asociado: un texto cifrado no se puede mover a otro registro.
- **Clave maestra**, por orden de prioridad: `ADMINBASE_MASTER_KEY` → `ADMINBASE_MASTER_KEY_FILE` (Docker secret) → `master.key` generada en el directorio de datos (modo 0600).
- Subclaves para otros fines (p. ej. la auditoría) se derivan con HMAC-SHA256 de la clave maestra; la clave maestra nunca se usa directamente fuera del vault.

## Consecuencias

- ✅ Robar solo la base de datos no revela secretos.
- ✅ Rotar la KEK solo exige reenvolver las DEK, no recifrar los datos (pendiente de implementar como comando).
- ⚠️ Con el valor por defecto, `master.key` vive en el mismo volumen que los datos: una copia completa del volumen sí revela secretos. Aceptado para uso personal; en modo equipo debe usarse una clave externa.
- ⚠️ Perder la clave maestra hace irrecuperables las credenciales guardadas (los perfiles siguen existiendo, sin secretos).

## Alternativas descartadas

- **Clave derivada de una frase de paso al arrancar:** más segura en reposo, pero impide reinicios automáticos del contenedor. Puede añadirse como opción.
- **KMS en la nube:** apropiado para despliegues de empresa; queda como extensión de `LoadKey`.
