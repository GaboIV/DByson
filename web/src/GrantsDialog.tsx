import { useCallback, useEffect, useState } from "react";
import { api, ApiError, type Connection, type ConnectionGrant, type GrantLevel, type TeamUser } from "./api";
import { Dialog } from "./Dialog";

const LEVEL_LABELS: Record<GrantLevel, string> = {
  use: "Usar (abrir, consultar, exportar)",
  manage: "Gestionar (además, editar el perfil)",
};

/** Who can use a shared connection, and how. Admins and the owner already have full access. */
export function GrantsDialog({ connection, onClose }: { connection: Connection; onClose: () => void }) {
  const [grants, setGrants] = useState<ConnectionGrant[] | null>(null);
  const [users, setUsers] = useState<TeamUser[]>([]);
  const [userId, setUserId] = useState("");
  const [level, setLevel] = useState<GrantLevel>("use");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const [g, u] = await Promise.all([
        api<ConnectionGrant[]>("GET", `/api/connections/${connection.id}/grants`),
        api<TeamUser[]>("GET", "/api/users"),
      ]);
      setGrants(g);
      setUsers(u);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "No se pudieron cargar los permisos");
    }
  }, [connection.id]);

  useEffect(() => {
    void load();
  }, [load]);

  async function act(action: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await action();
      await load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "No se pudo guardar el permiso");
    } finally {
      setBusy(false);
    }
  }

  // Admins and the owner always have full access: offering them a grant would only confuse.
  const granted = new Set((grants ?? []).map((g) => g.userId));
  const candidates = users.filter((u) => !u.disabled && u.role !== "admin" && u.id !== connection.ownerId && !granted.has(u.id));

  return (
    <Dialog title={`Permisos · ${connection.name}`} onClose={onClose}>
      <p className="muted">
        La administración y quien creó el perfil tienen acceso completo. Las personas con rol <em>viewer</em> lo abren siempre en solo
        lectura y nunca en producción.
      </p>
      {grants === null && !error && <p className="muted">Cargando…</p>}
      {grants && grants.length === 0 && <p className="muted">Nadie más tiene acceso todavía.</p>}
      {grants && grants.length > 0 && (
        <ul className="grant-list">
          {grants.map((g) => (
            <li key={g.userId}>
              <strong>{g.username}</strong>
              <select
                aria-label={`Nivel de ${g.username}`}
                value={g.level}
                disabled={busy}
                onChange={(e) =>
                  void act(() => api("POST", `/api/connections/${connection.id}/grants`, { userId: g.userId, level: e.target.value }))
                }
              >
                {(Object.keys(LEVEL_LABELS) as GrantLevel[]).map((l) => (
                  <option key={l} value={l}>
                    {LEVEL_LABELS[l]}
                  </option>
                ))}
              </select>
              <button
                className="danger"
                disabled={busy}
                onClick={() => void act(() => api("DELETE", `/api/connections/${connection.id}/grants/${g.userId}`))}
              >
                Quitar
              </button>
            </li>
          ))}
        </ul>
      )}
      <form
        className="grant-add"
        onSubmit={(e) => {
          e.preventDefault();
          if (!userId) return;
          void act(async () => {
            await api("POST", `/api/connections/${connection.id}/grants`, { userId: Number(userId), level });
            setUserId("");
          });
        }}
      >
        <label>
          Persona
          <select value={userId} onChange={(e) => setUserId(e.target.value)} disabled={!candidates.length}>
            <option value="">{candidates.length ? "Elegir…" : "No hay más personas a las que dar acceso"}</option>
            {candidates.map((u) => (
              <option key={u.id} value={u.id}>
                {u.username}
                {u.role === "viewer" ? " (viewer)" : ""}
              </option>
            ))}
          </select>
        </label>
        <label>
          Nivel
          <select value={level} onChange={(e) => setLevel(e.target.value as GrantLevel)}>
            {(Object.keys(LEVEL_LABELS) as GrantLevel[]).map((l) => (
              <option key={l} value={l}>
                {LEVEL_LABELS[l]}
              </option>
            ))}
          </select>
        </label>
        <button className="primary" disabled={busy || !userId}>
          Dar acceso
        </button>
      </form>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
    </Dialog>
  );
}
