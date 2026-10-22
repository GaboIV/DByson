import { useState, type FormEvent } from "react";
import { api, type NewColumnDef } from "../api";
import { Dialog } from "../Dialog";
import { enc, errorText, type ViewContext } from "./shared";

const COLUMN_TYPES = ["INT", "VARCHAR", "TEXT", "DATETIME", "TINYINT", "DECIMAL"];

const initialColumns = (): NewColumnDef[] => [
  { name: "id", type: "INT", length: "11", null: false, autoIncrement: true, primaryKey: true },
  { name: "name", type: "VARCHAR", length: "255", null: false, autoIncrement: false, primaryKey: false },
  { name: "created_at", type: "DATETIME", length: "", null: false, default: "CURRENT_TIMESTAMP", autoIncrement: false, primaryKey: false },
];

export function CreateTableDialog({
  ctx,
  onClose,
  onCreated,
}: {
  ctx: ViewContext;
  onClose: () => void;
  onCreated: (table: string) => void;
}) {
  const [name, setName] = useState("");
  const [columns, setColumns] = useState<NewColumnDef[]>(initialColumns);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const update = (i: number, patch: Partial<NewColumnDef>) => setColumns((cols) => cols.map((c, j) => (j === i ? { ...c, ...patch } : c)));

  async function submit(e: FormEvent) {
    e.preventDefault();
    const table = name.trim();
    if (!table) return;
    setBusy(true);
    setError("");
    try {
      const r = await api<{ executedSql: string }>(
        "POST",
        `/api/connections/${ctx.connection.id}/databases/${enc(ctx.database)}/tables/create`,
        { name: table, columns },
      );
      ctx.reportSql(r.executedSql);
      onCreated(table);
    } catch (err) {
      setError(errorText(err, "Error creando la tabla"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog title={`Crear nueva tabla en ${ctx.database}`} onClose={onClose} className="modal-large">
      <form onSubmit={submit} className="modal-form">
        <label>
          Nombre de la tabla
          <input type="text" required placeholder="ej. products" value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <h4>Columnas</h4>
        <div className="create-cols-list">
          {columns.map((col, i) => (
            <div key={i} className="col-def-row">
              <input
                className="col-def-name"
                type="text"
                placeholder="Nombre"
                aria-label={`Nombre de la columna ${i + 1}`}
                value={col.name}
                onChange={(e) => update(i, { name: e.target.value })}
                required
              />
              <select
                className="col-def-type"
                aria-label={`Tipo de la columna ${i + 1}`}
                value={col.type}
                onChange={(e) => update(i, { type: e.target.value })}
              >
                {COLUMN_TYPES.map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
              <input
                className="col-def-length"
                type="text"
                placeholder="Longitud"
                aria-label={`Longitud de la columna ${i + 1}`}
                value={col.length || ""}
                onChange={(e) => update(i, { length: e.target.value })}
              />
              <label className="checkbox-mini">
                <input
                  type="checkbox"
                  checked={col.autoIncrement}
                  onChange={(e) => update(i, { autoIncrement: e.target.checked, ...(e.target.checked ? { primaryKey: true } : {}) })}
                />
                AI
              </label>
              <label className="checkbox-mini">
                <input type="checkbox" checked={col.primaryKey} onChange={(e) => update(i, { primaryKey: e.target.checked })} />
                PK
              </label>
              <label className="checkbox-mini">
                <input type="checkbox" checked={col.null} onChange={(e) => update(i, { null: e.target.checked })} />
                Null
              </label>
              {columns.length > 1 && (
                <button
                  type="button"
                  className="btn-tiny danger"
                  aria-label={`Quitar la columna ${i + 1}`}
                  onClick={() => setColumns((cols) => cols.filter((_, j) => j !== i))}
                >
                  ✕
                </button>
              )}
            </div>
          ))}
        </div>
        <button
          type="button"
          className="align-start"
          onClick={() =>
            setColumns((cols) => [
              ...cols,
              { name: "", type: "VARCHAR", length: "255", null: true, autoIncrement: false, primaryKey: false },
            ])
          }
        >
          + Agregar columna
        </button>
        {error && <div className="alert alert-error">{error}</div>}
        <div className="actions">
          <button type="button" onClick={onClose}>
            Cancelar
          </button>
          <button type="submit" className="primary" disabled={busy}>
            Crear tabla
          </button>
        </div>
      </form>
    </Dialog>
  );
}

export function CreateDatabaseDialog({
  ctx,
  onClose,
  onCreated,
}: {
  ctx: ViewContext;
  onClose: () => void;
  onCreated: (db: string) => void;
}) {
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const db = name.trim();
    if (!db) return;
    setBusy(true);
    setError("");
    try {
      const r = await api<{ executedSql: string }>("POST", `/api/connections/${ctx.connection.id}/databases/create`, { name: db });
      ctx.reportSql(r.executedSql);
      onCreated(db);
    } catch (err) {
      setError(errorText(err, "Error creando la base de datos"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog title="Crear base de datos" onClose={onClose}>
      <form onSubmit={submit} className="modal-form">
        <label>
          Nombre de la base de datos
          <input type="text" required placeholder="ej. mi_nueva_bd" value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        {error && <div className="alert alert-error">{error}</div>}
        <div className="actions">
          <button type="button" onClick={onClose}>
            Cancelar
          </button>
          <button type="submit" className="primary" disabled={busy}>
            Crear base de datos
          </button>
        </div>
      </form>
    </Dialog>
  );
}
