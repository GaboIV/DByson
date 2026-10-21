import { useCallback, useEffect, useState } from "react";
import { api, ApiError, type Connection, type TestResponse } from "./api";
import { ConnectionForm } from "./ConnectionForm";

export function Connections() {
  const [items, setItems] = useState<Connection[] | null>(null);
  const [editing, setEditing] = useState<Connection | "new" | null>(null);
  const [status, setStatus] = useState<Record<string, string>>({});
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setItems(await api<Connection[]>("GET", "/api/connections"));
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "No se pudieron cargar las conexiones");
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function quickTest(c: Connection) {
    setStatus((s) => ({ ...s, [c.id]: "Probando…" }));
    try {
      const r = await api<TestResponse>("POST", `/api/connections/${c.id}/test`);
      setStatus((s) => ({
        ...s,
        [c.id]: r.ok && r.result ? `✔ ${r.result.serverVersion}${r.result.tls ? " · TLS" : " · sin TLS"}` : `✖ ${r.error}`,
      }));
    } catch (e) {
      setStatus((s) => ({ ...s, [c.id]: `✖ ${e instanceof ApiError ? e.message : "error"}` }));
    }
  }

  async function remove(c: Connection) {
    if (!window.confirm(`¿Eliminar la conexión "${c.name}"?`)) return;
    try {
      await api("DELETE", `/api/connections/${c.id}`);
      await load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "No se pudo eliminar");
    }
  }

  if (editing) {
    return (
      <ConnectionForm
        existing={editing === "new" ? undefined : editing}
        onCancel={() => setEditing(null)}
        onSaved={() => {
          setEditing(null);
          void load();
        }}
      />
    );
  }

  return (
    <section>
      <div className="toolbar">
        <h2>Conexiones</h2>
        <button className="primary" onClick={() => setEditing("new")}>
          Nueva conexión
        </button>
      </div>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {items === null && !error && <p className="muted">Cargando…</p>}
      {items?.length === 0 && (
        <div className="card empty">
          <p>Aún no tienes conexiones.</p>
          <button className="primary" onClick={() => setEditing("new")}>
            Crear la primera
          </button>
        </div>
      )}
      <div className="cards">
        {items?.map((c) => (
          <article key={c.id} className={`card conn env-${c.env}`}>
            <header>
              <span className={`badge env-${c.env}`}>{c.env}</span>
              <h3>{c.name}</h3>
            </header>
            <p className="muted mono">
              {c.username}@{c.host}:{c.port}
              {c.database && `/${c.database}`}
            </p>
            <p className="muted">
              TLS: {c.tlsMode}
              {c.readOnly && " · solo lectura"}
            </p>
            {status[c.id] && <p className="status-line">{status[c.id]}</p>}
            <footer>
              <button onClick={() => quickTest(c)}>Probar</button>
              <button onClick={() => setEditing(c)}>Editar</button>
              <button className="danger" onClick={() => remove(c)}>
                Eliminar
              </button>
            </footer>
          </article>
        ))}
      </div>
    </section>
  );
}
