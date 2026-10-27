import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api, ApiError, type TeamRole, type TeamUser } from "./api";
import { Dialog } from "./Dialog";
import { Select } from "./Select";
import { useConfirm } from "./Confirm";

export const ROLE_LABELS: Record<TeamRole, string> = { admin: "Administración", member: "Miembro", viewer: "Solo lectura" };
const ROLE_HELP: Record<TeamRole, string> = {
  admin: "Administra las cuentas, todas las conexiones y los permisos del equipo.",
  member: "Crea conexiones privadas y trabaja con las conexiones que le compartan.",
  viewer: "Consulta y exporta datos sin modificarlos. No puede abrir producción.",
};
const roleOptions = (["member", "viewer", "admin"] as TeamRole[]).map((value) => ({ value, label: ROLE_LABELS[value] }));

export function teamError(error: unknown) {
  const messages: Record<string, string> = {
    "username already exists": "Ya existe una cuenta con ese usuario.",
    "username must be 3-64 characters without spaces": "El usuario debe tener entre 3 y 64 caracteres, sin espacios.",
    "password must be between 12 and 256 characters": "La contraseña debe tener entre 12 y 256 caracteres.",
    "cannot disable your own account": "No puedes deshabilitar tu propia cuenta.",
    "cannot delete your own account": "No puedes eliminar tu propia cuenta.",
    "cannot demote or disable the last active admin": "Debe quedar al menos una cuenta de administración activa.",
    "cannot delete the last active admin": "No puedes eliminar la última cuenta de administración activa.",
    "unshare or reassign their shared connections first":
      "Esta persona es propietaria de conexiones compartidas. Deja de compartirlas antes de eliminar su cuenta.",
    "unknown user": "La cuenta ya no está disponible. Actualiza la lista e inténtalo de nuevo.",
    "invalid email": "Escribe un correo válido.",
    "connection or recipient access changed; refresh and retry":
      "Los accesos cambiaron mientras guardabas. Actualiza la lista e inténtalo de nuevo.",
  };
  return error instanceof ApiError ? (messages[error.message] ?? error.message) : "No se pudo completar la operación. Inténtalo de nuevo.";
}

export function MemberAvatar({ name }: { name: string }) {
  return (
    <span className="member-avatar" aria-hidden="true">
      {name.slice(0, 2).toLocaleUpperCase()}
    </span>
  );
}

export function TeamDialog({ username: currentUsername, onClose }: { username: string; onClose: () => void }) {
  const confirm = useConfirm();
  const [users, setUsers] = useState<TeamUser[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState("all");
  const [creating, setCreating] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<TeamRole>("member");
  const [resetting, setResetting] = useState<TeamUser | null>(null);
  const [nextPassword, setNextPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [resetError, setResetError] = useState("");
  const [linking, setLinking] = useState<TeamUser | null>(null);
  const [email, setEmail] = useState("");
  const [emailError, setEmailError] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setUsers(await api<TeamUser[]>("GET", "/api/users"));
      setError("");
    } catch (error) {
      setError(teamError(error));
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  async function act(action: () => Promise<unknown>, message: string, done?: () => void) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await action();
      done?.();
      setNotice(message);
      await load();
    } catch (error) {
      setError(teamError(error));
    } finally {
      setBusy(false);
    }
  }

  async function changeRole(user: TeamUser, next: TeamRole) {
    if (next === user.role) return;
    if (
      !(await confirm(
        `¿Cambiar el rol de «${user.username}» a ${ROLE_LABELS[next]}? ${ROLE_HELP[next]}${user.username === currentUsername ? " Perderás el acceso a la administración del equipo." : ""}`,
      ))
    )
      return;
    await act(
      () => api("PUT", `/api/users/${user.id}`, { role: next }),
      `Rol de ${user.username} actualizado.`,
      () => {
        if (user.username === currentUsername) window.location.reload();
      },
    );
  }

  async function resetPassword(event: FormEvent) {
    event.preventDefault();
    if (!resetting) return;
    setBusy(true);
    setResetError("");
    try {
      await api("PUT", `/api/users/${resetting.id}`, { password: nextPassword });
      if (resetting.username === currentUsername) window.location.reload();
      setNotice(`Contraseña de ${resetting.username} actualizada. Sus sesiones se cerraron.`);
      setResetting(null);
      setNextPassword("");
    } catch (error) {
      setResetError(teamError(error));
    } finally {
      setBusy(false);
    }
  }

  const active = users?.filter((u) => !u.disabled).length ?? 0;
  const admins = users?.filter((u) => u.role === "admin" && !u.disabled).length ?? 0;
  const visible = (users ?? []).filter(
    (u) =>
      `${u.username} ${u.email ?? ""}`.toLocaleLowerCase().includes(search.toLocaleLowerCase()) &&
      (filter === "all" || (filter === "active" && !u.disabled) || (filter === "disabled" && u.disabled) || filter === u.role),
  );

  return (
    <Dialog
      title="Equipo"
      onClose={() => {
        if (!busy) onClose();
      }}
      className="team-dialog"
    >
      <div className="collab-intro">
        <div>
          <span className="collab-eyebrow">ESPACIO DE TRABAJO</span>
          <h2>Las personas detrás de tus datos</h2>
          <p className="muted">Gestiona las cuentas y decide cómo trabaja cada persona en DByson.</p>
        </div>
        <button
          className="primary"
          disabled={busy || loading}
          onClick={() => {
            setCreating(true);
            setError("");
          }}
        >
          ＋ Añadir persona
        </button>
      </div>
      <div className="team-stats" aria-label="Resumen del equipo">
        <div>
          <strong>{users?.length ?? "—"}</strong>
          <span>Personas</span>
        </div>
        <div>
          <strong>{users ? active : "—"}</strong>
          <span>Activas</span>
        </div>
        <div>
          <strong>{users ? admins : "—"}</strong>
          <span>Administración</span>
        </div>
      </div>
      {notice && (
        <p className="collab-notice" role="status">
          ✓ {notice}
        </p>
      )}
      {error && (
        <div className="collab-error" role="alert">
          <p>{error}</p>
          {!busy && <button onClick={() => void load()}>Actualizar lista</button>}
        </div>
      )}
      {creating && (
        <form
          className="collab-panel team-create"
          onSubmit={(event) => {
            event.preventDefault();
            void act(
              () => api("POST", "/api/users", { username: username.trim(), password, role }),
              `Cuenta de ${username.trim()} creada. Ya puedes darle acceso a conexiones.`,
              () => {
                setCreating(false);
                setUsername("");
                setPassword("");
                setRole("member");
              },
            );
          }}
        >
          <div className="collab-section-heading">
            <div>
              <h4>Nueva persona</h4>
              <p className="muted">Crea su cuenta y después comparte las conexiones que necesite.</p>
            </div>
          </div>
          <fieldset disabled={busy} className="collab-fields">
            <label>
              Usuario
              <input
                autoFocus
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                autoComplete="off"
                required
                minLength={3}
                maxLength={64}
                pattern="\S{3,64}"
                placeholder="p. ej. ana.garcia"
              />
            </label>
            <label>
              Contraseña inicial
              <input
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                autoComplete="new-password"
                required
                minLength={12}
                maxLength={256}
                placeholder="Mínimo 12 caracteres"
              />
            </label>
            <label>
              Rol
              <Select
                value={role}
                options={roleOptions}
                label="Rol de la nueva persona"
                onChange={(value) => setRole(value as TeamRole)}
                disabled={busy}
              />
            </label>
          </fieldset>
          <p className="muted role-description">{ROLE_HELP[role]}</p>
          <div className="actions">
            <button
              type="button"
              disabled={busy}
              onClick={() => {
                setCreating(false);
                setPassword("");
              }}
            >
              Cancelar
            </button>
            <button className="primary" disabled={busy}>
              {busy ? "Creando…" : "Crear cuenta"}
            </button>
          </div>
        </form>
      )}
      <section className="team-members" aria-label="Personas del equipo">
        <div className="collab-section-heading">
          <h4>
            Personas del equipo <span className="count-badge">{users?.length ?? 0}</span>
          </h4>
        </div>
        <div className="collab-toolbar">
          <label className="collab-search">
            <span className="sr-only">Buscar personas</span>
            <input
              type="search"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Buscar por usuario o correo…"
            />
          </label>
          <Select
            value={filter}
            label="Filtrar personas"
            onChange={setFilter}
            options={[
              { value: "all", label: "Todas las personas" },
              { value: "active", label: "Activas" },
              { value: "disabled", label: "Deshabilitadas" },
              ...roleOptions,
            ]}
          />
        </div>
        {loading && !users && (
          <p className="collab-empty" role="status">
            Cargando el equipo…
          </p>
        )}
        {users && !visible.length && (
          <div className="collab-empty">
            <strong>No hay personas que coincidan</strong>
            <p className="muted">Prueba con otro nombre o cambia el filtro.</p>
            <button
              onClick={() => {
                setSearch("");
                setFilter("all");
              }}
            >
              Limpiar filtros
            </button>
          </div>
        )}
        <ul className="team-list">
          {visible.map((user) => {
            const self = user.username === currentUsername;
            const lastAdmin = user.role === "admin" && !user.disabled && admins === 1;
            return (
              <li key={user.id} className={`team-member${user.disabled ? " is-disabled" : ""}`}>
                <div className="member-identity">
                  <MemberAvatar name={user.username} />
                  <div>
                    <div className="member-name">
                      <strong>{user.username}</strong>
                      {self && <span className="count-badge">Tú</span>}
                    </div>
                    <span className="muted member-detail">
                      {user.email || ROLE_LABELS[user.role]}
                      {lastAdmin ? " · Última administración" : ""}
                    </span>
                    <button
                      className="member-email-button"
                      disabled={busy}
                      aria-label={`Vincular correo de ${user.username}`}
                      onClick={() => {
                        setLinking(user);
                        setEmail(user.email ?? "");
                        setEmailError("");
                      }}
                    >
                      {user.email ? "Cambiar correo" : "Vincular correo"}
                    </button>
                  </div>
                </div>
                <span className={`member-status${user.disabled ? " inactive" : ""}`}>
                  <span aria-hidden="true">●</span> {user.disabled ? "Deshabilitada" : "Activa"}
                </span>
                <Select
                  value={user.role}
                  options={roleOptions}
                  label={`Rol de ${user.username}`}
                  disabled={busy || lastAdmin}
                  onChange={(value) => void changeRole(user, value as TeamRole)}
                />
                <div className="team-actions">
                  <button
                    disabled={busy}
                    aria-label={`Cambiar contraseña de ${user.username}`}
                    onClick={() => {
                      setResetting(user);
                      setNextPassword("");
                      setShowPassword(false);
                      setResetError("");
                    }}
                  >
                    Contraseña
                  </button>
                  <button
                    disabled={busy || self || lastAdmin}
                    title={self ? "No puedes deshabilitar tu cuenta" : lastAdmin ? "Debe quedar una administración activa" : undefined}
                    onClick={async () => {
                      if (
                        !user.disabled &&
                        !(await confirm(
                          `¿Deshabilitar a «${user.username}»? Se cerrarán sus sesiones y no podrá entrar hasta que habilites la cuenta de nuevo.`,
                        ))
                      )
                        return;
                      await act(
                        () => api("PUT", `/api/users/${user.id}`, { disabled: !user.disabled }),
                        `Cuenta de ${user.username} ${user.disabled ? "habilitada" : "deshabilitada"}.`,
                      );
                    }}
                  >
                    {user.disabled ? "Habilitar" : "Deshabilitar"}
                  </button>
                  <button
                    className="danger team-delete"
                    disabled={busy || self || lastAdmin}
                    aria-label={`Eliminar a ${user.username}`}
                    title={self ? "No puedes eliminar tu cuenta" : lastAdmin ? "Debe quedar una administración activa" : "Eliminar cuenta"}
                    onClick={async () => {
                      if (
                        !(await confirm(
                          `¿Eliminar permanentemente la cuenta de «${user.username}»? Se eliminarán sus conexiones privadas y sus permisos. Esta acción no se puede deshacer.`,
                        ))
                      )
                        return;
                      await act(() => api("DELETE", `/api/users/${user.id}`), `Cuenta de ${user.username} eliminada.`);
                    }}
                  >
                    Eliminar
                  </button>
                </div>
              </li>
            );
          })}
        </ul>
      </section>
      <details className="collab-guide">
        <summary>Qué puede hacer cada rol</summary>
        <div className="role-guide">
          {roleOptions.map(({ value, label }) => (
            <div key={value}>
              <strong>{label}</strong>
              <p className="muted">{ROLE_HELP[value as TeamRole]}</p>
            </div>
          ))}
        </div>
      </details>
      <p className="collab-footnote">
        El acceso a cada conexión se asigna desde <strong>Compartir con el equipo</strong>, en su tarjeta.
      </p>
      {linking && (
        <Dialog
          title={`Correo de Workspace · ${linking.username}`}
          onClose={() => {
            if (!busy) setLinking(null);
          }}
        >
          <p className="muted">
            Antes del primer acceso con Google, vincula el correo corporativo a esta cuenta para conservar su rol, conexiones y permisos.
            Utiliza únicamente el correo de esta persona.
          </p>
          <form
            className="settings-section"
            onSubmit={async (event) => {
              event.preventDefault();
              setBusy(true);
              setEmailError("");
              try {
                await api("PUT", `/api/users/${linking.id}`, { email: email.trim() });
                setNotice(`Correo de ${linking.username} actualizado.`);
                setLinking(null);
                await load();
              } catch (error) {
                setEmailError(teamError(error));
              } finally {
                setBusy(false);
              }
            }}
          >
            <label>
              Correo corporativo
              <input
                autoFocus
                type="email"
                value={email}
                maxLength={254}
                autoComplete="off"
                disabled={busy}
                onChange={(event) => setEmail(event.target.value)}
                placeholder="nombre@empresa.com"
              />
            </label>
            <small className="muted">
              Dejarlo vacío elimina el correo de vinculación inicial. No cambia una identidad de Google que ya se haya vinculado.
            </small>
            {emailError && (
              <p className="error" role="alert">
                {emailError}
              </p>
            )}
            <div className="actions">
              <button type="button" disabled={busy} onClick={() => setLinking(null)}>
                Cancelar
              </button>
              <button className="primary" disabled={busy}>
                {busy ? "Guardando…" : "Guardar correo"}
              </button>
            </div>
          </form>
        </Dialog>
      )}
      {resetting && (
        <Dialog
          title={`Cambiar contraseña · ${resetting.username}`}
          onClose={() => {
            if (!busy) {
              setResetting(null);
              setNextPassword("");
            }
          }}
          className="password-dialog"
        >
          <p className="muted">
            Al guardar, se cerrarán todas las sesiones de esta persona.
            {resetting.username === currentUsername ? " Tendrás que iniciar sesión de nuevo." : ""}
          </p>
          <form className="settings-section" onSubmit={(event) => void resetPassword(event)}>
            <label>
              Nueva contraseña
              <input
                autoFocus
                type={showPassword ? "text" : "password"}
                required
                minLength={12}
                maxLength={256}
                autoComplete="new-password"
                value={nextPassword}
                disabled={busy}
                onChange={(event) => setNextPassword(event.target.value)}
              />
            </label>
            <small className="muted">Entre 12 y 256 caracteres.</small>
            <label className="check">
              <input type="checkbox" checked={showPassword} onChange={(event) => setShowPassword(event.target.checked)} />
              Mostrar contraseña
            </label>
            {resetError && (
              <p className="error" role="alert">
                {resetError}
              </p>
            )}
            <div className="actions">
              <button
                type="button"
                disabled={busy}
                onClick={() => {
                  setResetting(null);
                  setNextPassword("");
                }}
              >
                Cancelar
              </button>
              <button className="primary" disabled={busy}>
                {busy ? "Guardando…" : "Guardar contraseña"}
              </button>
            </div>
          </form>
        </Dialog>
      )}
    </Dialog>
  );
}
