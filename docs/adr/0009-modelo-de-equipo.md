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

## Revisión (26 de octubre de 2026)

Una revisión del código de esta fase encontró y corrigió:

- **Escalada con `use`:** probar un borrador (`test-draft`) o pedir la huella SSH
  con un perfil compartido reutilizaba la contraseña guardada aunque el host
  fuera otro: con `use` bastaba apuntar a un servidor propio para recibirla.
  Ahora exige `manage`, y para quien no es dueño ni `admin` las credenciales
  guardadas solo viajan a su destino original; tampoco puede rebajar entorno,
  solo lectura ni TLS de un perfil compartido.
- **`viewer` dueño de `prod`:** la regla "nunca abre prod" se saltaba si el
  `viewer` era el dueño (p. ej. un `member` degradado). Ahora aplica siempre.
- **SSO:** `state` sin ligar al navegador (inicio de sesión forzado), sin PKCE,
  auto-aprovisionamiento abierto a cualquier cuenta del emisor, vínculo por
  email ambiguo (`LIMIT 1`, sensible a mayúsculas, solo con auto-aprovisionamiento)
  y la 2FA local se saltaba al entrar por SSO. Ahora: cookie de `state`, PKCE,
  `DBYSON_OIDC_ALLOWED_DOMAINS` (obligatorio para auto-aprovisionar), vínculo
  solo con coincidencia única, y 2FA local exigida salvo MFA declarada por el
  proveedor (`amr`) o `DBYSON_OIDC_TRUST_MFA`.
- **Rotación:** secretos y auditoría se confirmaban en transacciones separadas
  (la auditoría se verificaba *después* de re-cifrar) y la cadena se firmaba
  con la clave maestra en lugar de la subclave de auditoría, así que fallaba en
  cualquier instalación real. Ahora es una única transacción verificada, y la
  clave nueva nunca va por la línea de comandos.
- **Purga:** el ancla era `MAX(hash)` (orden alfabético, no la última entrada),
  una purga parcial dejaba la cadena inverificable y se borraba por fecha (con
  huecos si el reloj retrocedía). Ahora se borra un prefijo por id, el ancla es
  correcta y la verificación acepta el inicio solo si una entrada
  `audit.purged` firmada lo respalda.
- **Usuarios:** la protección del último `admin` era *check-then-act* (dos
  peticiones simultáneas podían dejar el servidor sin administración) y un
  campo inválido podía cerrar sesiones sin aplicar el cambio. Ahora todo se
  valida antes y se aplica en una transacción que comprueba el resultado.
- **Interfaz de permisos:** añadida (••• → Permisos… en cada conexión compartida).
