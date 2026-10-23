# 0004 · Modo de despliegue `personal` / `team` para la política de 2FA

- **Estado:** aceptada · 21 de octubre de 2026

## Contexto

La 2FA protege cuando otras personas pueden alcanzar la pantalla de inicio de sesión. En uso personal (DByson publicado solo en `127.0.0.1`) apenas añade protección: quien llega al login ya controla la máquina. Exigirla siempre añade fricción sin beneficio.

La idea inicial era deducirlo de la dirección de escucha, pero **dentro de Docker el servidor siempre escucha en `0.0.0.0`** aunque el puerto solo se publique en el loopback del host. No hay forma fiable de detectar la exposición real.

## Decisión

- Variable `DBYSON_MODE`:
  - `personal` (por defecto): 2FA disponible y opcional.
  - `team`: 2FA obligatoria para usar conexiones `prod`, túneles SSH y exportar sus credenciales.
- `DBYSON_REQUIRE_MFA` (`off` | `sensitive` | `all`) fuerza la política y gana sobre el modo.
- Si el modo es `personal` y el servidor escucha fuera de loopback, se avisa en el log.
- La auditoría está activa en ambos modos.

## Consecuencias

- ✅ Sin fricción para el uso individual; requisitos estrictos explícitos para equipos.
- ⚠️ Quien publique el puerto a la red en modo `personal` queda sin 2FA obligatoria. Mitigado con el aviso y la documentación.
