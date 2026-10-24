// Hosts one workbench tab: a connection, optionally a database and a table, and one view of it.
// Data fetching for each view lives in the view itself (src/workspace/*); server metadata comes
// from the shared catalog cache so switching tabs never refetches what is already known.
import { useCallback, useEffect, useId, useState, type ReactNode } from "react";
import type { Connection } from "./api";
import { Brand } from "./Brand";
import { TabStrip } from "./TabStrip";
import { Select } from "./Select";
import { useClipboard } from "./clipboard";
import { usePreferences } from "./Preferences";
import { useConfirmedApi } from "./useConfirmedApi";
import { invalidateDatabases, invalidateSchema, invalidateTables, useDatabases, useSchema, useTables } from "./workspace/catalog";
import { CreateDatabaseDialog, CreateTableDialog } from "./workspace/CreateDialogs";
import { DatabaseOverview } from "./workspace/DatabaseOverview";
import { ExportView } from "./workspace/ExportView";
import { InsertRecord } from "./workspace/InsertRecord";
import { ModifyTable } from "./workspace/ModifyTable";
import { ServerDatabases } from "./workspace/ServerDatabases";
import { enc, errorText, type ViewContext, type WorkspaceView } from "./workspace/shared";
import { SqlConsole } from "./workspace/SqlConsole";
import { StructureView } from "./workspace/StructureView";
import { TableData } from "./workspace/TableData";
import { TableSidebar } from "./workspace/TableSidebar";
import { ProductivityTools } from "./workspace/ProductivityTools";

export type { WorkspaceView } from "./workspace/shared";

type ActiveTab = "server-dbs" | "db-overview" | Exclude<WorkspaceView, "browse">;

interface WorkspaceProps {
  connection: Connection;
  username: string;
  onLogout: () => void;
  onBack: () => void;
  initialDatabase?: string;
  initialTable?: string;
  initialView?: WorkspaceView;
  initialSQL?: string;
  /** Name of a query tab (SQL view). */
  title?: string;
  onRename?: (title: string) => void;
  isActive?: boolean;
  viewNavigation?: ReactNode;
  onOpenView?: (database: string, table: string, view: WorkspaceView) => void;
  onCloseTab?: () => void;
  onOpenTable?: (database: string, table: string) => void;
  onNewSQL?: (database: string, query?: string) => void;
  onContextChange?: (ctx: { database: string; table: string; sql: boolean; view: WorkspaceView; dirty: boolean }) => void;
}

export function Workspace(props: WorkspaceProps) {
  const {
    connection,
    username,
    onLogout,
    onBack,
    isActive = true,
    viewNavigation,
    onOpenView,
    onOpenTable,
    onNewSQL,
    onCloseTab,
    onContextChange,
  } = props;
  const { preferences } = usePreferences();
  const readOnly = connection.readOnly || preferences.viewOnly;
  const { confirmedApi, confirmationDialog } = useConfirmedApi(`${connection.name} · ${connection.env.toUpperCase()}`);
  const { copy, copyNotice } = useClipboard();
  const dbSelectId = useId();

  const [database, setDatabase] = useState(props.initialDatabase ?? connection.database ?? "");
  const [table, setTable] = useState(props.initialTable ?? "");
  const [activeTab, setActiveTab] = useState<ActiveTab>(() => {
    const v = props.initialView;
    if (v && v !== "browse") return v;
    if (props.initialTable) return "data";
    return (props.initialDatabase ?? connection.database) ? "db-overview" : "server-dbs";
  });
  const [tableFilter, setTableFilter] = useState("");
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState("");
  const [lastSql, setLastSql] = useState<{ sql: string; ms: number | null } | null>(null);
  const [creating, setCreating] = useState<"table" | "database" | null>(null);
  const [tools, setTools] = useState(false);

  const databases = useDatabases(connection.id, isActive);
  const tables = useTables(connection.id, database, isActive && !!database);
  const tableView = activeTab === "data" || activeTab === "structure" || activeTab === "modify" || activeTab === "insert";
  const schema = useSchema(connection.id, database, table, isActive && !!table && tableView);

  const view: WorkspaceView = activeTab === "server-dbs" || activeTab === "db-overview" ? "browse" : activeTab;

  useEffect(() => {
    onContextChange?.({ database, table, sql: activeTab === "sql", view, dirty });
    // onContextChange is recreated by the parent on every render; the values are what matter.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [database, table, activeTab, dirty]);

  // Show the listing query in the banner when a listing is what the user is looking at.
  useEffect(() => {
    if (activeTab === "server-dbs" && databases.sql) setLastSql({ sql: databases.sql, ms: null });
  }, [activeTab, databases.sql]);
  useEffect(() => {
    if (activeTab === "db-overview" && tables.sql) setLastSql({ sql: tables.sql, ms: null });
  }, [activeTab, tables.sql]);

  const reportSql = useCallback((sql: string, ms: number | null = null) => {
    if (sql) setLastSql({ sql, ms });
  }, []);

  function openView(next: WorkspaceView) {
    if (onOpenView) onOpenView(database, table, next);
    else if (next !== "browse") setActiveTab(next);
  }

  function newSQL(query?: string, targetDatabase = database) {
    if (onNewSQL) onNewSQL(targetDatabase, query);
    else setActiveTab("sql");
  }

  function openDatabase(db: string) {
    if (viewNavigation && onOpenView) return onOpenView(db, "", "browse");
    setDatabase(db);
    setTable("");
    setActiveTab(db ? "db-overview" : "server-dbs");
  }

  function openTable(name: string) {
    if (table === name) return openView("data");
    if (onOpenTable) return onOpenTable(database, name);
    setTable(name);
    setActiveTab("data");
  }

  function openStructure(name: string) {
    if (onOpenView) return onOpenView(database, name, "structure");
    setTable(name);
    setActiveTab("structure");
  }

  async function tableAction(name: string, action: "truncate" | "drop") {
    setError("");
    try {
      const r = await confirmedApi<{ executedSql: string }>(
        "POST",
        `/api/connections/${connection.id}/databases/${enc(database)}/tables/${enc(name)}/${action}`,
      );
      if (!r) return;
      reportSql(r.executedSql);
      invalidateTables(connection.id, database);
      invalidateSchema(connection.id, database, name);
      if (action === "drop" && table === name) {
        if (onCloseTab && viewNavigation) onCloseTab();
        else {
          setTable("");
          setActiveTab("db-overview");
        }
      }
    } catch (e) {
      setError(errorText(e, action === "drop" ? "Error eliminando la tabla" : "Error vaciando la tabla"));
    }
  }

  async function dropDatabase(db: string) {
    setError("");
    try {
      const r = await confirmedApi<{ executedSql: string }>("POST", `/api/connections/${connection.id}/databases/drop`, { name: db });
      if (!r) return;
      reportSql(r.executedSql);
      invalidateDatabases(connection.id);
      if (database === db) openDatabase("");
    } catch (e) {
      setError(errorText(e, "Error eliminando la base de datos"));
    }
  }

  const ctx: ViewContext = { connection, database, table, readOnly, isActive, confirmedApi, copy, reportSql, openView, newSQL };

  // Table tabs always show their view navigation (also on query tabs of the table);
  // the fallback strip and the table-level actions only make sense on the table views.
  const tableToolbar = (viewNavigation || (table && tableView)) && (
    <div className="table-view-toolbar">
      {viewNavigation ?? (
        <TabStrip
          label="Vistas de la tabla"
          className="table-tab-strip"
          active={activeTab}
          items={(
            [
              ["data", "Contenido"],
              ["structure", "Estructura"],
              ...(!readOnly
                ? [
                    ["modify", "Modificar"],
                    ["insert", "Nuevo registro"],
                  ]
                : []),
            ] as [WorkspaceView, string][]
          ).map(([v, label]) => ({ id: v, label, onSelect: () => openView(v) }))}
        />
      )}
      {!readOnly && table && tableView && (
        <div className="table-danger-actions">
          <button className="adminer-tab-link link-danger" onClick={() => void tableAction(table, "truncate")}>
            Vaciar
          </button>
          <button className="adminer-tab-link link-danger" onClick={() => void tableAction(table, "drop")}>
            Eliminar
          </button>
        </div>
      )}
    </div>
  );

  return (
    <div className="workspace">
      <header className="ws-topbar unified-app-bar">
        <button className="brand brand-home" onClick={onBack} title="Ir a Conexiones">
          <Brand />
        </button>
        <button className="btn-back" onClick={onBack} title="Volver al listado de conexiones">
          ← Conexiones
        </button>

        <nav className="breadcrumb" aria-label="Ubicación">
          <span className={`badge env-${connection.env}`}>{connection.env}</span>
          <button
            className={`bc-item ${!database ? "bc-active" : ""}`}
            onClick={() => openDatabase("")}
            title="Ver las bases de datos del servidor"
          >
            <strong>{connection.name}</strong>
          </button>
          {database && (
            <>
              <span className="bc-sep">»</span>
              <button
                className={`bc-item ${!table ? "bc-active" : ""}`}
                onClick={() => (viewNavigation && onOpenView ? onOpenView(database, "", "browse") : openDatabase(database))}
                title="Ver las tablas de la base de datos"
              >
                {database}
              </button>
            </>
          )}
          {table && (
            <>
              <span className="bc-sep">»</span>
              <span className="bc-item bc-active">
                <strong>{table}</strong>
              </span>
            </>
          )}
        </nav>

        {connection.ssh?.enabled && (
          <span className="connection-route-badge" title={`SSH · ${connection.ssh.host}`}>
            ↗ SSH
          </span>
        )}
        {readOnly && <span className="badge badge-ro">🔒 Solo lectura</span>}

        <div className="db-selector">
          <label htmlFor={dbSelectId}>BD:</label>
          <Select
            id={dbSelectId}
            value={database}
            onChange={openDatabase}
            label="BD:"
            options={[
              { value: "", label: databases.loading ? "Cargando…" : "(Seleccionar BD)" },
              ...(database && !databases.data?.some((d) => d.name === database) ? [{ value: database, label: database }] : []),
              ...(databases.data ?? []).map((d) => ({ value: d.name, label: d.name })),
            ]}
          />
        </div>

        <div className="spacer" />
        <div className="user-section">
          <span className="muted">{username}</span>
          <button onClick={onLogout}>Salir</button>
        </div>
      </header>

      <div className="ws-body">
        <TableSidebar
          ctx={ctx}
          tables={tables}
          filter={tableFilter}
          onFilter={setTableFilter}
          activeView={view}
          onOpenTable={openTable}
          onCreateTable={() => setCreating("table")}
          onTools={() => setTools(true)}
        />

        <main className={`ws-content${table && activeTab === "data" ? " ws-content-table-data" : ""}`}>
          {tableToolbar}
          {error && (
            <div className="alert alert-error" role="alert">
              {error}
            </div>
          )}

          {(activeTab === "server-dbs" || (!database && activeTab === "db-overview")) && (
            <ServerDatabases
              ctx={ctx}
              databases={databases}
              onOpen={openDatabase}
              onCreate={() => setCreating("database")}
              onDrop={(db) => void dropDatabase(db)}
            />
          )}
          {activeTab === "db-overview" && database && (
            <DatabaseOverview
              ctx={ctx}
              tables={tables}
              filter={tableFilter}
              onOpenTable={openTable}
              onOpenStructure={openStructure}
              onTruncate={(t) => void tableAction(t, "truncate")}
              onDrop={(t) => void tableAction(t, "drop")}
              onCreateTable={() => setCreating("table")}
            />
          )}
          {table && activeTab === "data" && (
            <TableData
              ctx={ctx}
              schema={schema.data}
              schemaError={schema.error}
              onInsert={() => openView("insert")}
              onDirtyChange={setDirty}
            />
          )}
          {table && activeTab === "structure" && <StructureView ctx={ctx} schema={schema.data} error={schema.error} />}
          {table && activeTab === "modify" && <ModifyTable ctx={ctx} schema={schema.data} onDirtyChange={setDirty} />}
          {table && activeTab === "insert" && (
            <InsertRecord ctx={ctx} schema={schema.data} onClose={() => onCloseTab?.()} onDirtyChange={setDirty} />
          )}
          {activeTab === "sql" && (
            <SqlConsole
              ctx={ctx}
              initialSQL={props.initialSQL}
              title={props.title || "Consulta SQL"}
              onRename={props.onRename}
              onDirtyChange={setDirty}
            />
          )}
          {activeTab === "export" && <ExportView ctx={ctx} tables={tables.data ?? []} />}
          {preferences.showQueries && lastSql && (
            <div className="sql-banner sql-banner-footer">
              <div className="sql-banner-text">
                <code>{lastSql.sql}</code>
                {lastSql.ms !== null && <span className="sql-time">({(lastSql.ms / 1000).toFixed(3)} s)</span>}
              </div>
              <div className="sql-banner-actions">
                <button className="btn-tiny" onClick={() => void copy(lastSql.sql, "SQL copiado")} title="Copiar SQL">
                  Copiar
                </button>
                <button className="btn-tiny" onClick={() => newSQL(lastSql.sql)} title="Editar en una consulta SQL">
                  Editar en SQL
                </button>
              </div>
            </div>
          )}
        </main>
      </div>

      {tools && <ProductivityTools ctx={ctx} onClose={() => setTools(false)} />}
      {confirmationDialog}
      {copyNotice}
      {creating === "table" && (
        <CreateTableDialog
          ctx={ctx}
          onClose={() => setCreating(null)}
          onCreated={(name) => {
            setCreating(null);
            invalidateTables(connection.id, database);
            openTable(name);
          }}
        />
      )}
      {creating === "database" && (
        <CreateDatabaseDialog
          ctx={ctx}
          onClose={() => setCreating(null)}
          onCreated={(db) => {
            setCreating(null);
            invalidateDatabases(connection.id);
            openDatabase(db);
          }}
        />
      )}
    </div>
  );
}
