import { useEffect, useState } from "react";
import { api } from "../api";
import { Dialog } from "../Dialog";
import { errorText, type ViewContext } from "./shared";

interface SavedQuery {
  id: string;
  name: string;
  database: string;
  query: string;
  updatedAt: number;
}
export function SavedQueries({
  ctx,
  sql,
  title,
  onSaved,
  onClose,
}: {
  ctx: ViewContext;
  sql: string;
  title: string;
  onSaved: () => void;
  onClose: () => void;
}) {
  const [list, setList] = useState<SavedQuery[]>([]),
    [name, setName] = useState(title),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const path = `/api/connections/${ctx.connection.id}/saved-queries`;
  async function load() {
    try {
      setList(await api<SavedQuery[]>("GET", path));
    } catch (e) {
      setError(errorText(e, "No se pudo cargar la biblioteca"));
    }
  }
  useEffect(() => {
    void load();
  }, [path]);
  async function save() {
    setBusy(true);
    setError("");
    try {
      await api("POST", path, { name, query: sql, database: ctx.database });
      onSaved();
      await load();
    } catch (e) {
      setError(errorText(e, "No se pudo guardar"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog title="Consultas guardadas" onClose={onClose} className="query-library">
      <p className="muted">Biblioteca privada en el servidor · SQL cifrado · Hasta 200 consultas por conexión.</p>
      <div className="productivity-toolbar">
        <input aria-label="Nombre para guardar" value={name} maxLength={120} onChange={(e) => setName(e.target.value)} />
        <button className="primary" disabled={busy || !name.trim() || !sql.trim()} onClick={() => void save()}>
          Guardar consulta actual
        </button>
      </div>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      <div className="library-list">
        {list.map((q) => (
          <article key={q.id}>
            <div>
              <strong>{q.name}</strong>
              <small>
                {q.database || "Sin base"} · {new Date(q.updatedAt * 1000).toLocaleString()}
              </small>
              <pre>{q.query}</pre>
            </div>
            <button
              onClick={() => {
                ctx.newSQL(q.query, q.database);
                onClose();
              }}
            >
              Abrir
            </button>
            <button
              disabled={busy}
              aria-label={`Eliminar consulta ${q.name}`}
              onClick={async () => {
                setBusy(true);
                try {
                  await api("DELETE", `${path}/${q.id}`);
                  await load();
                } catch (e) {
                  setError(errorText(e, "No se pudo eliminar"));
                } finally {
                  setBusy(false);
                }
              }}
            >
              Eliminar
            </button>
          </article>
        ))}
        {!list.length && <p className="muted">Aún no hay consultas guardadas.</p>}
      </div>
    </Dialog>
  );
}
