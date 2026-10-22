import { useEffect, useState } from "react";
import type { QueryResponse, QueryResult } from "../api";
import { ActionMenu } from "../ActionMenu";
import { RecordDetail, type RecordSnapshot } from "../RecordDetail";
import { ResultLimitNotice } from "../ResultLimitNotice";
import { Spinner } from "../Spinner";
import { cellText, rowJSON } from "../clipboard";
import { invalidateConnection } from "./catalog";
import { errorText, type ViewContext } from "./shared";
import { downloadSql, pickSqlFile } from "./sqlFile";

const DEFAULT_SQL = "SELECT 1;";

interface Props {
  ctx: ViewContext;
  initialSQL?: string;
  /** Tab name; renaming is owned by the workbench so the tab strip shows it too. */
  title: string;
  onRename?: (title: string) => void;
  onDirtyChange: (dirty: boolean) => void;
}

export function SqlConsole({ ctx, initialSQL, title, onRename, onDirtyChange }: Props) {
  const baseline = initialSQL || DEFAULT_SQL;
  const [sql, setSql] = useState(baseline);
  const [saved, setSaved] = useState(baseline); // what the dirty marker compares against
  const [result, setResult] = useState<QueryResult | null>(null);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState("");
  const [history, setHistory] = useState<string[]>([]);
  const [detail, setDetail] = useState<RecordSnapshot | null>(null);
  const [name, setName] = useState(title);

  useEffect(() => setName(title), [title]);
  useEffect(() => onDirtyChange(sql !== saved), [sql, saved, onDirtyChange]);

  async function run() {
    if (!sql.trim() || running) return;
    setRunning(true);
    setError("");
    const submitted = sql;
    try {
      const r = await ctx.confirmedApi<QueryResponse>("POST", `/api/connections/${ctx.connection.id}/query`, {
        query: submitted,
        database: ctx.database,
      });
      if (!r) return;
      if (!r.ok) {
        setError(r.error || "Error ejecutando la consulta");
        setResult(null);
        return;
      }
      setResult(r.result ?? null);
      if (r.result) {
        ctx.reportSql(r.result.executedSql || submitted, r.result.executionMs);
        // Only statements that can change the server invalidate cached metadata.
        if (!r.result.isSelect) invalidateConnection(ctx.connection.id);
      }
      setHistory((h) => [submitted, ...h.filter((q) => q !== submitted)].slice(0, 20));
    } catch (e) {
      setError(errorText(e, "Error de red o del servidor"));
      setResult(null);
    } finally {
      setRunning(false);
    }
  }

  function download() {
    downloadSql({ title: name || "Consulta", connection: ctx.connection.name, database: ctx.database }, sql);
    setSaved(sql);
  }

  async function open() {
    if (sql !== saved && !window.confirm("La consulta tiene cambios sin guardar. ¿Reemplazarla por el archivo?")) return;
    try {
      const file = await pickSqlFile();
      if (!file) return;
      setSql(file.sql);
      setSaved(file.sql);
      setResult(null);
      setError("");
      onRename?.(file.title);
    } catch (e) {
      setError(errorText(e, "No se pudo leer el archivo"));
    }
  }

  function commitName() {
    const next = name.trim();
    if (!next) setName(title);
    else if (next !== title) onRename?.(next);
  }

  return (
    <div className="sql-console">
      <div className="sql-header">
        {onRename ? (
          <input
            className="sql-title-input"
            aria-label="Nombre de la consulta"
            value={name}
            maxLength={60}
            onChange={(e) => setName(e.target.value)}
            onBlur={commitName}
            onKeyDown={(e) => {
              if (e.key === "Enter") e.currentTarget.blur();
              if (e.key === "Escape") {
                setName(title);
                e.currentTarget.blur();
              }
            }}
          />
        ) : (
          <h2>Consola SQL</h2>
        )}
        <div className="sql-file-actions">
          <button onClick={() => void open()} title="Reemplazar el contenido con un archivo .sql">
            📂 Abrir .sql
          </button>
          <button onClick={download} disabled={!sql.trim()} title="Descargar esta consulta como archivo .sql">
            ⬇ Descargar .sql
          </button>
        </div>
      </div>
      <p className="muted">
        Una sentencia por ejecución · <strong>Ctrl + Enter</strong> para ejecutar. Máximo 1.000 filas y 8 MiB de datos; usa Exportar para
        descargar una tabla completa.
      </p>

      <div className="sql-editor-container">
        <textarea
          className="sql-editor"
          aria-label="Consulta SQL"
          value={sql}
          spellCheck={false}
          onChange={(e) => setSql(e.target.value)}
          onKeyDown={(e) => {
            if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
              e.preventDefault();
              void run();
            }
          }}
          placeholder="Escribe tu consulta SQL aquí…"
          rows={8}
        />
        <div className="sql-actions">
          <div className="sql-snippets">
            {ctx.table && (
              <>
                <button className="btn-snippet" onClick={() => setSql(`SELECT * FROM \`${ctx.table}\` LIMIT 50;`)}>
                  SELECT *
                </button>
                <button className="btn-snippet" onClick={() => setSql(`EXPLAIN SELECT * FROM \`${ctx.table}\`;`)}>
                  EXPLAIN
                </button>
              </>
            )}
            <button className="btn-snippet" onClick={() => setSql("SHOW TABLES;")}>
              SHOW TABLES
            </button>
            <button className="btn-snippet" onClick={() => setSql("SHOW PROCESSLIST;")}>
              PROCESSLIST
            </button>
          </div>
          <button className="primary" disabled={running} onClick={() => void run()}>
            {running ? <Spinner label="Ejecutando…" /> : "▶ Ejecutar consulta"}
          </button>
        </div>
      </div>

      {error && <div className="alert alert-error">{error}</div>}

      {result && (
        <div className="sql-result-section">
          <ResultLimitNotice result={result} />
          <div className="result-stats">
            {result.isSelect ? (
              <span>
                ✔ Consulta ejecutada en <strong>{result.executionMs} ms</strong> · <strong>{result.rows.length}</strong> filas devueltas
              </span>
            ) : (
              <span>
                ✔ Sentencia ejecutada en <strong>{result.executionMs} ms</strong> · <strong>{result.affectedRows}</strong> filas afectadas
              </span>
            )}
          </div>

          {result.isSelect && result.rows.length > 0 && (
            <div className="grid-container">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>Registro</th>
                    {result.columns.map((c, i) => (
                      <th key={`${c}-${i}`}>{c}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {result.rows.map((row, r) => (
                    <tr key={r}>
                      <td className="td-actions">
                        <button
                          className="btn-action-view"
                          onClick={() =>
                            setDetail({ columns: [...result.columns], row: [...row], title: "Registro del resultado SQL", pk: {} })
                          }
                          aria-label={`Ver detalle SQL ${r + 1}`}
                        >
                          ⌕
                        </button>
                        <ActionMenu
                          label={`Más acciones SQL ${r + 1}`}
                          actions={[
                            {
                              label: "Copiar fila como JSON",
                              onClick: () => void ctx.copy(rowJSON(result.columns, row), "Fila copiada como JSON"),
                            },
                          ]}
                        />
                      </td>
                      {row.map((val, c) => (
                        <td key={c} className={val === null ? "cell-null" : ""}>
                          <div className="cell-content">
                            <span>{val === null ? <em>NULL</em> : String(val)}</span>
                            <button
                              className="cell-copy"
                              aria-label={`Copiar celda SQL ${result.columns[c]}, fila ${r + 1}`}
                              onClick={() => void ctx.copy(cellText(val), "Celda copiada")}
                            >
                              ⧉
                            </button>
                          </div>
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {history.length > 0 && (
        <details className="sql-history">
          <summary>Historial de consultas recientes ({history.length})</summary>
          <div className="history-list">
            {history.map((q, i) => (
              <button key={i} className="history-item" onClick={() => setSql(q)} title="Cargar en el editor">
                <code>{q}</code>
              </button>
            ))}
          </div>
        </details>
      )}

      {detail && <RecordDetail snapshot={detail} onClose={() => setDetail(null)} />}
    </div>
  );
}
