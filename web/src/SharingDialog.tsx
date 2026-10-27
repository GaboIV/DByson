import { useCallback, useEffect, useState } from "react";
import { api, type Connection, type ConnectionGrant, type GrantLevel, type TeamUser } from "./api";
import { Dialog } from "./Dialog";
import { Select } from "./Select";
import { useConfirm } from "./Confirm";
import { MemberAvatar, ROLE_LABELS, teamError } from "./Team";

const levels = [
  { value: "use", label: "Puede usar" },
  { value: "manage", label: "Puede gestionar" },
];
type Candidate = Pick<TeamUser, "id" | "username" | "role">;

export function SharingDialog({
  connection,
  isAdmin,
  onClose,
  onChanged,
}: {
  connection: Connection;
  isAdmin: boolean;
  onClose: () => void;
  onChanged: () => void;
}) {
  const confirm = useConfirm();
  const [shared, setShared] = useState(connection.shared);
  const [grants, setGrants] = useState<ConnectionGrant[] | null>(null);
  const [users, setUsers] = useState<Candidate[]>([]);
  const [loading, setLoading] = useState(true);
  const [userId, setUserId] = useState("");
  const [level, setLevel] = useState<GrantLevel>("use");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [grants, users] = await Promise.all([
        api<ConnectionGrant[]>("GET", `/api/connections/${connection.id}/grants`),
        api<Candidate[]>("GET", `/api/connections/${connection.id}/grants/users`),
      ]);
      setGrants(grants);
      setUsers(users);
      setError("");
    } catch (error) {
      setError(teamError(error));
    } finally {
      setLoading(false);
    }
  }, [connection.id]);
  useEffect(() => {
    void load();
  }, [load]);

  async function act(action: () => Promise<unknown>, message: string) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await action();
      setNotice(message);
      onChanged();
      await load();
    } catch (error) {
      setError(teamError(error));
    } finally {
      setBusy(false);
    }
  }
  const granted = new Set((grants ?? []).map((grant) => grant.userId));
  const candidates = users.filter((user) => !granted.has(user.id) && !(connection.env === "prod" && user.role === "viewer"));
  const chosen = candidates.find((user) => String(user.id) === userId);
  const ready = !busy && !loading && grants !== null;

  async function toggleSharing() {
    if (
      shared &&
      !(await confirm(
        `¿Dejar de compartir «${connection.name}»? Se quitarán todos los permisos del equipo. Su propietario y la administración conservarán el acceso.`,
      ))
    )
      return;
    await act(
      async () => {
        await api("PUT", `/api/connections/${connection.id}/sharing`, { shared: !shared });
        setShared(!shared);
        setUserId("");
      },
      shared
        ? "La conexión dejó de compartirse. Se quitaron los permisos del equipo."
        : "Conexión lista para compartir. Añade a las personas que necesiten acceso.",
    );
  }

  return (
    <Dialog
      title="Compartir con el equipo"
      onClose={() => {
        if (!busy) onClose();
      }}
      className="sharing-dialog"
    >
      <div className="sharing-connection">
        <span className="sharing-symbol" aria-hidden="true">
          ↗
        </span>
        <div>
          <h2>{connection.name}</h2>
          <span className="muted mono">
            {connection.host}:{connection.port}
          </span>
        </div>
        <span className={`badge env-${connection.env}`}>{connection.env === "prod" ? "Producción" : connection.env}</span>
      </div>
      <div className={`sharing-visibility${shared ? " is-shared" : ""}`}>
        <div>
          <strong>{shared ? "Acceso por persona" : "Conexión privada"}</strong>
          <p className="muted">
            {shared
              ? "Solo las personas con permiso pueden usarla. El resto del equipo no tiene acceso."
              : "Su propietario y la administración tienen acceso. Activa el uso compartido para añadir personas."}
          </p>
        </div>
        {isAdmin && (
          <button disabled={!ready} onClick={() => void toggleSharing()}>
            {shared ? "Dejar de compartir" : "Activar uso compartido"}
          </button>
        )}
      </div>
      {notice && (
        <p className="collab-notice" role="status">
          ✓ {notice}
        </p>
      )}
      {error && (
        <div className="collab-error" role="alert">
          <p>{error}</p>
          <button disabled={busy || loading} onClick={() => void load()}>
            Reintentar
          </button>
        </div>
      )}
      {loading && grants === null && (
        <p className="collab-empty" role="status">
          Cargando accesos…
        </p>
      )}
      {shared && grants !== null && (
        <>
          <form
            className="collab-panel"
            onSubmit={(event) => {
              event.preventDefault();
              if (!ready || !chosen) return;
              void act(async () => {
                await api("POST", `/api/connections/${connection.id}/grants`, {
                  userId: chosen.id,
                  level: chosen.role === "viewer" ? "use" : level,
                });
                setUserId("");
                setLevel("use");
              }, `Acceso concedido a ${chosen.username}.`);
            }}
          >
            <div className="collab-section-heading">
              <h4>Añadir personas</h4>
            </div>
            <div className="grant-add">
              <label>
                Persona
                <Select
                  value={userId}
                  label="Persona que recibirá acceso"
                  disabled={!ready || !candidates.length}
                  options={[
                    { value: "", label: candidates.length ? "Selecciona una persona…" : "No hay personas disponibles" },
                    ...candidates.map((user) => ({
                      value: String(user.id),
                      label: `${user.username}${user.role === "viewer" ? " · Solo lectura" : ""}`,
                    })),
                  ]}
                  onChange={(value) => {
                    setUserId(value);
                    setLevel("use");
                  }}
                />
              </label>
              <label>
                Permiso
                <Select
                  value={chosen?.role === "viewer" ? "use" : level}
                  label="Permiso que se concederá"
                  options={levels}
                  disabled={!ready || chosen?.role === "viewer"}
                  onChange={(value) => setLevel(value as GrantLevel)}
                />
              </label>
              <button className="primary" disabled={!ready || !chosen}>
                {busy ? "Guardando…" : "Dar acceso"}
              </button>
            </div>
            {!candidates.length && (
              <p className="muted">
                Todas las personas elegibles ya tienen acceso, o aún no hay miembros disponibles. La administración puede crear cuentas
                desde Equipo.
              </p>
            )}
            {chosen?.role === "viewer" && <p className="muted">Esta persona solo podrá consultar y exportar datos.</p>}
          </form>
          <section aria-label="Personas con acceso">
            <div className="collab-section-heading">
              <h4>
                Personas con acceso <span className="count-badge">{grants.length}</span>
              </h4>
            </div>
            <div className="sharing-inherited">
              <div>
                <strong>Propietario y administración</strong>
                <p className="muted">Acceso completo, incluido por su rol.</p>
              </div>
              <span className="count-badge">Siempre</span>
            </div>
            {!grants.length && (
              <div className="collab-empty">
                <strong>Aún no has añadido a nadie</strong>
                <p className="muted">Elige una persona para darle acceso a esta conexión.</p>
              </div>
            )}
            <ul className="grant-list">
              {grants.map((grant) => (
                <li key={grant.userId}>
                  <div className="member-identity">
                    <MemberAvatar name={grant.username} />
                    <div>
                      <strong>{grant.username}</strong>
                      <span className="muted member-detail">
                        {grant.disabled
                          ? "Cuenta deshabilitada · Sin acceso efectivo"
                          : grant.role === "viewer" && connection.env === "prod"
                            ? "Solo lectura · Producción bloqueada"
                            : grant.role
                              ? ROLE_LABELS[grant.role]
                              : "Miembro"}
                      </span>
                    </div>
                  </div>
                  <Select
                    value={grant.role === "viewer" ? "use" : grant.level}
                    label={`Nivel de ${grant.username}`}
                    options={levels}
                    disabled={!ready || grant.disabled || grant.role === "viewer"}
                    onChange={(value) =>
                      void act(
                        () => api("POST", `/api/connections/${connection.id}/grants`, { userId: grant.userId, level: value }),
                        `Permiso de ${grant.username} actualizado.`,
                      )
                    }
                  />
                  <button
                    className="danger"
                    disabled={!ready}
                    aria-label={`Quitar acceso a ${grant.username}`}
                    onClick={async () => {
                      if (
                        !(await confirm(
                          `¿Quitar el acceso de «${grant.username}» a «${connection.name}»? Dejará de poder usar esta conexión con este permiso.`,
                        ))
                      )
                        return;
                      await act(
                        () => api("DELETE", `/api/connections/${connection.id}/grants/${grant.userId}`),
                        `Acceso de ${grant.username} eliminado.`,
                      );
                    }}
                  >
                    Quitar acceso
                  </button>
                </li>
              ))}
            </ul>
          </section>
        </>
      )}
      <details className="collab-guide">
        <summary>Cómo funcionan los permisos</summary>
        <div className="role-guide">
          <div>
            <strong>Puede usar</strong>
            <p className="muted">
              Abre la conexión, ejecuta consultas y exporta datos. Las escrituras dependen del rol y del modo de solo lectura.
            </p>
          </div>
          <div>
            <strong>Puede gestionar</strong>
            <p className="muted">
              También edita el perfil, gestiona permisos y puede exportar credenciales. Eliminar el perfil sigue reservado al propietario y
              a la administración.
            </p>
          </div>
          <div>
            <strong>El rol marca el límite</strong>
            <p className="muted">
              Solo lectura nunca permite escribir, gestionar ni abrir producción, aunque exista un permiso mayor. Las protecciones de la
              conexión se mantienen.
            </p>
          </div>
        </div>
      </details>
      <p className="collab-footnote">
        Los cambios se guardan al momento. Para enviar una copia a otra instalación, usa <strong>Exportar archivo</strong>.
      </p>
      <div className="actions">
        <button onClick={onClose} disabled={busy}>
          Listo
        </button>
      </div>
    </Dialog>
  );
}
