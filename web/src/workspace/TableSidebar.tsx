import type { TableInfo } from "../api";
import { Spinner } from "../Spinner";
import type { Resource } from "./catalog";
import { cleanStr, type ViewContext, type WorkspaceView } from "./shared";

interface Props {
  ctx: ViewContext;
  tables: Resource<TableInfo[]>;
  filter: string;
  onFilter: (value: string) => void;
  activeView: WorkspaceView;
  onOpenTable: (table: string) => void;
  onCreateTable: () => void;
  onTools: () => void;
}

export function TableSidebar({ ctx, tables, filter, onFilter, activeView, onOpenTable, onCreateTable, onTools }: Props) {
  const visible = (tables.data ?? []).filter((t) => cleanStr(t.name).includes(cleanStr(filter)));

  return (
    <aside className="ws-sidebar">
      <div className="sidebar-actions">
        <button
          className={`action-btn ${activeView === "sql" ? "active" : ""}`}
          onClick={() => ctx.newSQL(ctx.table ? `SELECT * FROM \`${ctx.table}\` LIMIT 50;` : undefined)}
          title="Nueva consulta SQL · Ctrl+Alt+N"
        >
          ⚡ SQL
        </button>
        <button
          className={`action-btn ${activeView === "export" ? "active" : ""}`}
          onClick={() => ctx.openView("export")}
          title="Exportar base de datos"
        >
          📦 Exportar
        </button>
      </div>

      <button className="sidebar-tools" onClick={onTools}>
        Herramientas · Importar / ER / Administración
      </button>

      <div className="sidebar-search">
        <input
          type="search"
          placeholder="Filtrar tablas…"
          aria-label="Filtrar tablas"
          value={filter}
          onChange={(e) => onFilter(e.target.value)}
        />
      </div>

      <div className="table-list">
        <div className="table-list-header">
          <span>
            Tablas ({visible.length}) {tables.refreshing && <Spinner />}
          </span>
          {ctx.database && !ctx.readOnly && (
            <button className="btn-add-table" onClick={onCreateTable} title="Crear nueva tabla">
              + Nueva
            </button>
          )}
        </div>
        {!ctx.database ? (
          <p className="sidebar-empty">Selecciona una base de datos para ver sus tablas</p>
        ) : tables.loading ? (
          <Spinner block label="Cargando tablas…" />
        ) : tables.error && !tables.data ? (
          <p className="sidebar-empty error">{tables.error}</p>
        ) : visible.length === 0 ? (
          <p className="sidebar-empty">{filter ? "Ninguna tabla coincide" : "No hay tablas"}</p>
        ) : (
          visible.map((t) => (
            <button key={t.name} className={`table-item ${ctx.table === t.name ? "selected" : ""}`} onClick={() => onOpenTable(t.name)}>
              <span className="table-icon">{t.type === "VIEW" ? "👁" : "▤"}</span>
              <span className="table-name" title={t.name}>
                {t.name}
              </span>
              {t.rows > 0 && <span className="table-rows">{t.rows.toLocaleString()}</span>}
            </button>
          ))
        )}
      </div>
    </aside>
  );
}
