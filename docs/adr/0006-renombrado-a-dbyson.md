# 0006 · Renombrado completo de AdminBase a DByson

- **Estado:** aceptada · 22 de octubre de 2026

## Contexto

El producto nació como AdminBase y pasó a llamarse DByson. La interfaz y las cabeceras ya usaban el nombre nuevo, pero el módulo Go, el comando, la imagen, el volumen, el archivo de datos, la cookie y las variables `ADMINBASE_*` conservaban el anterior. Cada versión publicada con nombres mezclados encarece el cambio: más instalaciones, scripts y documentación que dependen de ellos. Aún no hay ninguna versión publicada.

## Decisión

Renombrar ahora todo lo visible, con compatibilidad hacia atrás donde una instalación existente lo necesita:

| Elemento | Nuevo nombre | Compatibilidad |
|---|---|---|
| Módulo Go | `github.com/gaboiv/dbyson` | — (no es API pública) |
| Comando y binario | `cmd/dbyson`, `/dbyson` | — |
| Imagen y volumen de ejemplo | `dbyson:dev`, `dbyson-data` | Un volumen antiguo se monta igual en `/data`. |
| Variables de entorno | `DBYSON_*` | `ADMINBASE_*` se sigue leyendo hasta la v1.0 (`internal/config`); el arranque lista las que siguen en uso. `DBYSON_*` gana si existen ambas. |
| Archivo de datos | `dbyson.db` | `adminbase.db` (y su `-wal`/`-shm`) se renombra al arrancar si el nuevo no existe. |
| Cookie de sesión | `dbyson_session` | No: cerrar sesión una vez es aceptable. |

**No se renombran** las etiquetas criptográficas `adminbase/v1/…` (vault y derivación de subclaves) ni `adminbase-recovery:` (códigos de recuperación). Están ligadas a cada texto cifrado, al MAC de cada entrada de auditoría y a cada hash de recuperación guardado: cambiarlas dejaría las credenciales indescifrables y rompería la cadena de auditoría. Un test (`TestCryptoDomainIsStable`) impide cambiarlas por error.

## Consecuencias

- ✅ Un solo nombre en código, imagen, documentación y despliegue antes de la primera versión pública.
- ✅ Las instalaciones existentes arrancan sin tocar su configuración ni su volumen.
- ⚠️ La compatibilidad con `ADMINBASE_*` es código a retirar en la v1.0.
- ⚠️ El nombre antiguo sobrevive en las etiquetas criptográficas; es intencional y está documentado.

## Actualización (26 de octubre de 2026)

La compatibilidad con `ADMINBASE_*` se retiró durante la Fase 4, antes de la v1.0: las variables antiguas ya no se leen y el arranque avisa de cuáles siguen definidas. El renombrado automático de `adminbase.db` se mantiene.
