import { useCallback, useEffect, useMemo, useState } from "react";
import { api, type QueryResponse, type QueryResult, type TableSchema } from "../api";
import { ActionMenu } from "../ActionMenu";
import { RecordDetail, type RecordSnapshot } from "../RecordDetail";
import { ResultLimitNotice } from "../ResultLimitNotice";
import { Spinner } from "../Spinner";
import { cellText, rowJSON } from "../clipboard";
import { invalidateConnection, useSchema, useTables } from "./catalog";
import { errorText, type ViewContext } from "./shared";
import { SqlEditor } from "./SqlEditor";
import { downloadSql, pickSqlFile } from "./sqlFile";
import { extractTablesAndAliases } from "./sqlMetadata";
import { deriveQueryTitle, splitSqlStatements } from "./sqlSplitter";

const DEFAULT_SQL = "SELECT 1;";

interface Props {
  ctx: ViewContext;
  initialSQL?: string;
  /** Tab name; renaming is owned by the workbench so the tab strip shows it too. */
  title: string;
  onRename?: (title: string) => void;
  onDirtyChange: (dirty: boolean) => void;
}

export interface StatementExecution {
  id: string;
  statement: string;
  title: string;
  status: "pending" | "running" | "success" | "error";
  result: QueryResult | null;
  error?: string;
}

export function SqlConsole({ ctx, initialSQL, title, onRename, onDirtyChange }: Props) {
  const baseline = initialSQL || DEFAULT_SQL;
  const [sql, setSql] = useState(baseline);
  const [saved, setSaved] = useState(baseline); // what the dirty marker compares against
  const [selectedText, setSelectedText] = useState("");
  const [executions, setExecutions] = useState<StatementExecution[]>([]);
  const [activeTabIndex, setActiveTabIndex] = useState(0);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState("");
  const [history, setHistory] = useState<string[]>([]);
  const [detail, setDetail] = useState<RecordSnapshot | null>(null);
  const [name, setName] = useState(title);

  // Fetch tables and current table schema for autocomplete
  const tables = useTables(ctx.connection.id, ctx.database, !!ctx.database);
  const currentTableSchema = useSchema(ctx.connection.id, ctx.database, ctx.table, !!ctx.table);

  const [columnsCache, setColumnsCache] = useState<Record<string, string[]>>({});

  // When active table schema loads, cache its columns
  useEffect(() => {
    if (ctx.table && currentTableSchema.data?.columns) {
      const cols = currentTableSchema.data.columns.map((c) => c.field);
      setColumnsCache((prev) => ({
        ...prev,
        [ctx.table]: cols,
        [ctx.table.toLowerCase()]: cols,
      }));
    }
  }, [ctx.table, currentTableSchema.data]);

  // Extract referenced tables from SQL and fetch their schemas if missing
  useEffect(() => {
    if (!ctx.database) return;
    const { tables: referenced } = extractTablesAndAliases(sql);
    const toCheck = [...referenced];
    if (ctx.table && !toCheck.includes(ctx.table)) {
      toCheck.push(ctx.table);
    }

    const missing = toCheck.filter((t) => !columnsCache[t.toLowerCase()]);
    if (missing.length === 0) return;

    let active = true;
    for (const t of missing) {
      api<TableSchema>(
        "GET",
        `/api/connections/${ctx.connection.id}/databases/${encodeURIComponent(ctx.database)}/tables/${encodeURIComponent(t)}/schema`
      )
        .then((res) => {
          if (!active || !res?.columns) return;
          const cols = res.columns.map((c) => c.field);
          setColumnsCache((prev) => ({
            ...prev,
            [t]: cols,
            [t.toLowerCase()]: cols,
          }));
        })
        .catch(() => {});
    }

    return () => {
      active = false;
    };
  }, [sql, ctx.connection.id, ctx.database, ctx.table, columnsCache]);

  // Preload columns for first 20 tables in database in background
  useEffect(() => {
    if (!ctx.database || !tables.data) return;
    const sample = tables.data.slice(0, 20);
    for (const t of sample) {
      if (columnsCache[t.name.toLowerCase()]) continue;
      api<TableSchema>(
        "GET",
        `/api/connections/${ctx.connection.id}/databases/${encodeURIComponent(ctx.database)}/tables/${encodeURIComponent(t.name)}/schema`
      )
        .then((res) => {
          if (!res?.columns) return;
          const cols = res.columns.map((c) => c.field);
          setColumnsCache((prev) => ({
            ...prev,
            [t.name]: cols,
            [t.name.toLowerCase()]: cols,
          }));
        })
        .catch(() => {});
    }
  }, [tables.data, ctx.connection.id, ctx.database, columnsCache]);

  const getColumns = useCallback(
    (tableName: string) => {
      const lower = tableName.toLowerCase();
      return columnsCache[lower] || columnsCache[tableName] || [];
    },
    [columnsCache]
  );

  const tablesList = useMemo(() => {
    return (tables.data ?? []).map((t) => ({ name: t.name, rows: t.rows }));
  }, [tables.data]);

  useEffect(() => setName(title), [title]);
  useEffect(() => onDirtyChange(sql !== saved), [sql, saved, onDirtyChange]);

  const hasSelection = selectedText.trim().length > 0;

  async function run() {
    if (running) return;
    const targetText = (hasSelection ? selectedText : sql).trim();
    if (!targetText) return;

    const stmts = splitSqlStatements(targetText);
    if (stmts.length === 0) return;

    setRunning(true);
    setError("");

    const initialItems: StatementExecution[] = stmts.map((stmt, idx) => ({
      id: `stmt-${Date.now()}-${idx}`,
      statement: stmt,
      title: deriveQueryTitle(stmt, idx + 1),
      status: "pending",
      result: null,
    }));

    setExecutions(initialItems);
    setActiveTabIndex(0);

    for (let i = 0; i < stmts.length; i++) {
      const stmt = stmts[i];
      // Mark current as running
      setExecutions((prev) =>
        prev.map((item, idx) => (idx === i ? { ...item, status: "running" } : item))
      );

      try {
        const r = await ctx.confirmedApi<QueryResponse>("POST", `/api/connections/${ctx.connection.id}/query`, {
          query: stmt,
          database: ctx.database,
        });

        if (!r) {
          // User cancelled confirmation
          setExecutions((prev) =>
            prev.map((item, idx) =>
              idx === i ? { ...item, status: "error", error: "Ejecución cancelada por el usuario" } : item
            )
          );
          break;
        }

        if (!r.ok) {
          const errMsg = r.error || "Error ejecutando la consulta";
          setExecutions((prev) =>
            prev.map((item, idx) =>
              idx === i ? { ...item, status: "error", error: errMsg } : item
            )
          );
        } else {
          setExecutions((prev) =>
            prev.map((item, idx) =>
              idx === i ? { ...item, status: "success", result: r.result ?? null } : item
            )
          );
          if (r.result) {
            ctx.reportSql(r.result.executedSql || stmt, r.result.executionMs);
            if (!r.result.isSelect) invalidateConnection(ctx.connection.id);
          }
        }
      } catch (e) {
        const errMsg = errorText(e, "Error de red o del servidor");
        setExecutions((prev) =>
          prev.map((item, idx) =>
            idx === i ? { ...item, status: "error", error: errMsg } : item
          )
        );
      }
    }

    setHistory((h) => [targetText, ...h.filter((q) => q !== targetText)].slice(0, 20));
    setRunning(false);
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
      setExecutions([]);
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

  const currentExecution = executions[activeTabIndex];

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

      <div className="sql-editor-container">
        <SqlEditor
          value={sql}
          onChange={setSql}
          onSelectionChange={setSelectedText}
          onRun={() => void run()}
          tables={tablesList}
          getColumns={getColumns}
          defaultTable={ctx.table}
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
          <span className="sql-info-hint" title="Autocompletado predictivo activo. Selecciona sentencias o ejecuta todo con Ctrl+Enter.">
            {hasSelection ? (
              <>⚡ <strong>Selección activa</strong> · Ctrl+Enter</>
            ) : (
              <>Selecciona para ejecutar o pulsa <strong>Ctrl+Enter</strong></>
            )}
          </span>
          <button
            className="primary"
            disabled={running || !(hasSelection ? selectedText.trim() : sql.trim())}
            onClick={() => void run()}
            title={hasSelection ? "Ejecutar la selección de SQL (Ctrl+Enter)" : "Ejecutar todo el script SQL (Ctrl+Enter)"}
          >
            {running ? (
              <Spinner label="Ejecutando…" />
            ) : hasSelection ? (
              "▶ Ejecutar selección"
            ) : (
              "▶ Ejecutar todo"
            )}
          </button>
        </div>
      </div>

      {error && <div className="alert alert-error">{error}</div>}

      {/* Result Tabs and Result Section */}
      {executions.length > 0 && (
        <div className="sql-results-container">
          <div className="sql-results-header">
            <div className="sql-results-tab-bar" role="tablist" aria-label="Resultados de las consultas">
              {executions.map((item, idx) => (
                <button
                  key={item.id}
                  role="tab"
                  className={`sql-result-tab ${activeTabIndex === idx ? "selected" : ""} ${item.status}`}
                  aria-selected={activeTabIndex === idx}
                  onClick={() => setActiveTabIndex(idx)}
                >
                  {item.status === "running" && <Spinner />}
                  {item.status === "error" && <span className="tab-status-icon error">✕</span>}
                  {item.status === "success" && <span className="tab-status-icon success">✓</span>}
                  <span className="sql-result-tab-title">{item.title}</span>
                  {item.result && item.result.isSelect && (
                    <span className="sql-result-tab-count">{item.result.rows.length} filas</span>
                  )}
                  {item.result && !item.result.isSelect && (
                    <span className="sql-result-tab-count">{item.result.affectedRows} af.</span>
                  )}
                </button>
              ))}
            </div>
            <div className="sql-results-actions">
              <button
                className="btn-link"
                onClick={() => setExecutions([])}
                title="Limpiar pestañas de resultados"
              >
                Limpiar resultados
              </button>
            </div>
          </div>

          {currentExecution && (
            <div className="sql-result-pane" role="tabpanel">
              <div className="sql-statement-banner">
                <code title={currentExecution.statement}>{currentExecution.statement}</code>
                <button
                  className="btn-tiny"
                  onClick={() => void ctx.copy(currentExecution.statement, "SQL copiado")}
                  title="Copiar sentencia SQL"
                >
                  ⧉ Copiar SQL
                </button>
              </div>

              {currentExecution.error && (
                <div className="alert alert-error">{currentExecution.error}</div>
              )}

              {currentExecution.result && (
                <div className="sql-result-section">
                  <ResultLimitNotice result={currentExecution.result} />
                  <div className="result-stats">
                    {currentExecution.result.isSelect ? (
                      <span>
                        ✔ Consulta ejecutada en <strong>{currentExecution.result.executionMs} ms</strong> ·{" "}
                        <strong>{currentExecution.result.rows.length}</strong> filas devueltas
                      </span>
                    ) : (
                      <span>
                        ✔ Sentencia ejecutada en <strong>{currentExecution.result.executionMs} ms</strong> ·{" "}
                        <strong>{currentExecution.result.affectedRows}</strong> filas afectadas
                      </span>
                    )}
                  </div>

                  {currentExecution.result.isSelect && currentExecution.result.rows.length > 0 ? (
                    <div className="grid-container">
                      <table className="data-table">
                        <thead>
                          <tr>
                            <th>Registro</th>
                            {currentExecution.result.columns.map((c, i) => (
                              <th key={`${c}-${i}`}>{c}</th>
                            ))}
                          </tr>
                        </thead>
                        <tbody>
                          {currentExecution.result.rows.map((row, r) => (
                            <tr key={r}>
                              <td className="td-actions">
                                <button
                                  className="btn-action-view"
                                  onClick={() =>
                                    setDetail({
                                      columns: [...currentExecution.result!.columns],
                                      row: [...row],
                                      title: `Registro ${r + 1} (${currentExecution.title})`,
                                      pk: {},
                                    })
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
                                      onClick: () =>
                                        void ctx.copy(
                                          rowJSON(currentExecution.result!.columns, row),
                                          "Fila copiada como JSON"
                                        ),
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
                                      aria-label={`Copiar celda SQL ${currentExecution.result!.columns[c]}, fila ${r + 1}`}
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
                  ) : currentExecution.result.isSelect ? (
                    <p className="muted" style={{ padding: "0.5rem 0" }}>
                      La consulta se ejecutó correctamente pero no devolvió filas.
                    </p>
                  ) : null}
                </div>
              )}
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
