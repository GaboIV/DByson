import { useEffect, useState, type FormEvent } from "react";
import type { QueryResponse, TableSchema } from "../api";
import { Spinner } from "../Spinner";
import { invalidateSchema, invalidateTables } from "./catalog";
import { errorText, type ViewContext } from "./shared";

interface Props {
  ctx: ViewContext;
  schema?: TableSchema;
  onDirtyChange: (dirty: boolean) => void;
}

export function ModifyTable({ ctx, schema, onDirtyChange }: Props) {
  const t = ctx.table;
  const template = `ALTER TABLE \`${t}\` ADD COLUMN \`nueva_columna\` VARCHAR(255) NULL;`;
  const [sql, setSql] = useState(template);
  const [running, setRunning] = useState(false);
  const [status, setStatus] = useState<{ error: boolean; message: string } | null>(null);

  useEffect(() => onDirtyChange(sql.trim() !== "" && sql !== template), [sql, template, onDirtyChange]);

  const first = schema?.columns[0]?.field || "col";
  const last = schema?.columns[schema.columns.length - 1]?.field || "col";
  const snippets: [string, string][] = [
    ["+ Agregar columna", template],
    ["✎ Modificar columna", `ALTER TABLE \`${t}\` MODIFY COLUMN \`${first}\` VARCHAR(255) NOT NULL;`],
    ["✕ Eliminar columna", `ALTER TABLE \`${t}\` DROP COLUMN \`${last}\`;`],
    ["🔑 Agregar índice", `ALTER TABLE \`${t}\` ADD INDEX \`idx_${t}_col\` (\`${first}\`);`],
    ["↻ Renombrar tabla", `ALTER TABLE \`${t}\` RENAME TO \`${t}_nuevo\`;`],
  ];

  async function execute(e?: FormEvent) {
    e?.preventDefault();
    if (ctx.readOnly || !sql.trim() || running) return;
    setRunning(true);
    setStatus(null);
    const started = performance.now();
    try {
      const r = await ctx.confirmedApi<QueryResponse>("POST", `/api/connections/${ctx.connection.id}/query`, {
        query: sql,
        database: ctx.database,
      });
      if (!r) return;
      if (!r.ok) {
        setStatus({ error: true, message: r.error || "Error al ejecutar la sentencia" });
        return;
      }
      const ms = Math.round(performance.now() - started);
      ctx.reportSql(sql, ms);
      setStatus({ error: false, message: `✔ Sentencia ejecutada en ${ms} ms (${r.result?.affectedRows ?? 0} filas afectadas)` });
      invalidateSchema(ctx.connection.id, ctx.database, t);
      invalidateTables(ctx.connection.id, ctx.database);
    } catch (err) {
      setStatus({ error: true, message: errorText(err, "Error al ejecutar ALTER TABLE") });
    } finally {
      setRunning(false);
    }
  }

  return (
    <div className="alter-table-view">
      <div className="alter-header">
        <h3>
          Modificar tabla: <strong>{t}</strong>
        </h3>
        <div className="alter-snippets">
          {snippets.map(([label, statement]) => (
            <button key={label} type="button" onClick={() => setSql(statement)}>
              {label}
            </button>
          ))}
        </div>
      </div>

      <form onSubmit={execute} className="alter-form">
        <textarea
          rows={5}
          value={sql}
          aria-label="Sentencia ALTER TABLE"
          onChange={(e) => setSql(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
              e.preventDefault();
              void execute();
            }
          }}
          placeholder={`ALTER TABLE \`${t}\` ...`}
        />
        <div className="alter-actions">
          <button type="submit" className="primary" disabled={ctx.readOnly || running || !sql.trim()}>
            {running ? <Spinner label="Ejecutando…" /> : "▶ Ejecutar sentencia ALTER"}
          </button>
          <span className="muted hint">
            Atajo: <strong>Ctrl + Enter</strong> para ejecutar
          </span>
        </div>
      </form>

      {status && <div className={`alert ${status.error ? "alert-error" : "alert-success"}`}>{status.message}</div>}

      {schema && (
        <div className="alter-columns-ref">
          <h4>Columnas actuales de la tabla ({schema.columns.length})</h4>
          <div className="table-responsive">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Campo</th>
                  <th>Tipo</th>
                  <th>Colación</th>
                  <th>Nulo</th>
                  <th>Clave</th>
                  <th>Por defecto</th>
                  <th>Acciones rápidas</th>
                </tr>
              </thead>
              <tbody>
                {schema.columns.map((c) => (
                  <tr key={c.field}>
                    <td>
                      <strong>{c.field}</strong>
                    </td>
                    <td className="mono">{c.type}</td>
                    <td className="mono">{c.collation || "—"}</td>
                    <td>{c.null === "YES" ? "Sí" : "No"}</td>
                    <td>{c.key || "—"}</td>
                    <td className="mono">{c.default !== null ? c.default : <em>NULL</em>}</td>
                    <td className="quick-actions">
                      <button
                        type="button"
                        className="btn-tiny"
                        onClick={() => setSql(`ALTER TABLE \`${t}\` MODIFY COLUMN \`${c.field}\` ${c.type} NOT NULL;`)}
                      >
                        Modificar
                      </button>
                      {!ctx.readOnly && (
                        <button
                          type="button"
                          className="btn-tiny link-danger"
                          onClick={() => setSql(`ALTER TABLE \`${t}\` DROP COLUMN \`${c.field}\`;`)}
                        >
                          Eliminar
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
