# 0009 · Modelo de equipo: roles, permisos compartidos, SSO y retención

- **Estado:** aceptada · 25 de octubre de 2026

## Contexto

La Fase 4 pedía gestión de usuarios, roles por conexión y entorno, conexiones
compartidas, SSO, auditoría exportable y clave externa con rotación. El punto
de partida era aislamiento total por dueño: cada usuario solo veía lo suyo,
sin roles efectivos (`role` existía pero `viewer` no se aplicaba) y sin forma
de compartir.

## Decisión

- **Roles globales** `admin` / `member` / `viewer`, aplicados en el servidor:
  `viewer` abre todo en solo lectura forzada y nunca abre `prod`.
- **Conexiones compartidas** con permiso por persona (`use` / `manage`).
  La administración central lo ve todo; sin permiso, un ID ajeno responde 404
  igual que uno inexistente. Borrar sigue reservado a dueño y administración.
  Exportar con secretos exige `manage`.
- **SSO OIDC genérico** (Google, Microsoft Entra ID, Keycloak) con la
  biblioteca estándar: flujo de código, verificación RS256 contra JWKS,
  `iss`/`aud`/`exp`/`nonce` validados; otro algoritmo falla cerrado.
  Auto-aprovisionamiento opcional como `member`, reutilizando el email
  verificado cuando ya existe la cuenta.
- **Retención explícita**: purga bajo demanda o al arrancar, siempre con
  entrada `audit.purged` que ancla el hash de la última entrada eliminada
  para quien conserve exportaciones. Sin borrado silencioso en segundo plano.
- **Rotación offline** (`rotate-key`): re-cifra secretos y re-firma la cadena
  tras verificarla; una cadena rota aborta. En modo `team` el servidor no
  arranca con la clave dentro del volumen.

## Consecuencias

- ✅ Desbloquea el uso en equipo sin reescribir el modelo de conexiones.
- ✅ Sin dependencias nuevas: SSO y rotación usan stdlib + SQLite.
- ⚠️ Los permisos por conexión se gestionan por API (`grants`); falta su
  interfaz. Riesgo aceptado hasta completarla: la API ya aplica el modelo.
- ⚠️ Cambia el contrato anterior: la administración ahora ve todos los
  perfiles (antes, ni siquiera otra administración los veía). Decisión
  consciente para la administración central; cubierta por pruebas.
