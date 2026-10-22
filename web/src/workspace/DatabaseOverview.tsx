import { useState } from "react";
import type { TableInfo } from "../api";
import { Spinner } from "../Spinner";
import { invalidateTables, type Resource } from "./catalog";
import { cleanStr, enc, errorText, formatBytes, type ViewContext } from "./shared";

interface Props {
  ctx: ViewContext;
  tables: Resource<TableInfo[]>;
  filter: string;
  onOpenTable: (table: string) => void;
  onOpenStructure: (table: string) => void;
  onTruncate: (table: string) => void;
  onDrop: (table: string) => void;
  onCreateTable: () => void;
}

export function DatabaseOverview({ ctx, tables, filter, onOpenTable, onOpenStructure, onTruncate, onDrop, onCreateTable }: Props) {
  const [selected, setSelected] = useState<string[]>([]);
  const [error, setError] = useState("");
  const all = tables.data ?? [];
  const visible = all.filter((t) => cleanStr(t.name).includes(cleanStr(filter)));
  const totalRows = all.reduce((acc, t) => acc + (t.rows || 0), 0);
  const totalData = all.reduce((acc, t) => acc + (t.dataLength || 0), 0);
  const totalIndex = all.reduce((acc, t) => acc + (t.indexLength || 0), 0);

  async function batch(action: "drop" | "truncate") {
    setError("");
    try {
      for (const table of selected) {
        const done = await ctx.confirmedApi(
          "POST",
          `/api/connections/${ctx.connection.id}/databases/${enc(ctx.database)}/tables/${enc(table)}/${action}`,
        );
        if (!done) break; // the user cancelled a confirmation
      }
    } catch (e) {
      setError(errorText(e, action === "drop" ? "Error eliminando tablas" : "Error vaciando tablas"));
    } finally {
      setSelected([]);
      invalidateTables(ctx.connection.id, ctx.database);
    }
  }

  return (
    <div className="db-overview">
      <div className="unified-top-bar">
        <div className="bar-left">
          <h2>
            Base de datos: <strong>{ctx.database}</strong>
          </h2>
          <div className="btn-group">
            {!ctx.readOnly && (
              <button className="primary" onClick={onCreateTable}>
                + Crear nueva tabla
              </button>
            )}
            <button onClick={() => void tables.refresh()} disabled={tables.loading || tables.refreshing}>
              🔄 Actualizar
            </button>
            <button onClick={() => ctx.openView("export")}>📦 Exportar BD</button>
            {tables.refreshing && <Spinner label="Actualizando…" />}
          </div>
        </div>
        <div className="bar-right">
          <span className="info-pill">{all.length} tablas</span>
          <span className="info-pill">{totalRows.toLocaleString()} filas</span>
          <span className="info-pill">{formatBytes(totalData + totalIndex)}</span>
        </div>
      </div>

      {error && <div className="alert alert-error">{error}</div>}

      {tables.loading ? (
        <Spinner block label="Cargando tablas…" />
      ) : tables.error && !tables.data ? (
        <div className="alert alert-error">{tables.error}</div>
      ) : (
        <div className="grid-container">
          <table className="data-table">
            <thead>
              <tr>
                <th className="col-check">
                  <input
                    type="checkbox"
                    aria-label="Seleccionar todas las tablas"
                    checked={selected.length === visible.length && visible.length > 0}
                    onChange={(e) => setSelected(e.target.checked ? visible.map((t) => t.name) : [])}
                  />
                </th>
                <th>Tabla</th>
                <th>Acciones</th>
                <th>Tipo</th>
                <th>Motor</th>
                <th>Colación</th>
                <th className="num">Filas</th>
                <th className="num">Tamaño datos</th>
                <th className="num">Tamaño índices</th>
                <th>Comentario</th>
              </tr>
            </thead>
            <tbody>
              {visible.length === 0 ? (
                <tr>
                  <td colSpan={10} className="empty-cell">
                    {filter ? "Ninguna tabla coincide con el filtro." : "No se encontraron tablas en esta base de datos."}
                  </td>
                </tr>
              ) : (
                visible.map((t) => {
                  const isSelected = selected.includes(t.name);
                  return (
                    <tr key={t.name} className={isSelected ? "row-selected" : ""}>
                      <td className="col-check">
                        <input
                          type="checkbox"
                          aria-label={`Seleccionar ${t.name}`}
                          checked={isSelected}
                          onChange={(e) => setSelected((s) => (e.target.checked ? [...s, t.name] : s.filter((n) => n !== t.name)))}
                        />
                      </td>
                      <td>
                        <button className="link-button table-link" onClick={() => onOpenTable(t.name)}>
                          <strong>{t.name}</strong>
                        </button>
                      </td>
                      <td>
                        <div className="row-action-links">
                          <button className="link-button" onClick={() => onOpenTable(t.name)}>
                            Ver
                          </button>
                          <span>|</span>
                          <button className="link-button" onClick={() => onOpenStructure(t.name)}>
                            Estructura
                          </button>
                          {!ctx.readOnly && (
                            <>
                              <span>|</span>
                              <button className="link-button link-danger" onClick={() => onTruncate(t.name)}>
                                Vaciar
                              </button>
                              <span>|</span>
                              <button className="link-button link-danger" onClick={() => onDrop(t.name)}>
                                Eliminar
                              </button>
                            </>
                          )}
                        </div>
                      </td>
                      <td>{t.type === "VIEW" ? "Vista" : "Tabla"}</td>
                      <td className="mono">{t.engine || "—"}</td>
                      <td className="mono">{t.collation || "—"}</td>
                      <td className="mono num">{t.rows.toLocaleString()}</td>
                      <td className="mono num">{formatBytes(t.dataLength)}</td>
                      <td className="mono num">{formatBytes(t.indexLength)}</td>
                      <td>{t.comment || "—"}</td>
                    </tr>
                  );
                })
              )}
            </tbody>
            {visible.length > 0 && (
              <tfoot>
                <tr className="tfoot-totals">
                  <td />
                  <td>
                    <strong>{all.length} tablas</strong>
                  </td>
                  <td colSpan={4} />
                  <td className="mono num">
                    <strong>{totalRows.toLocaleString()}</strong>
                  </td>
                  <td className="mono num">
                    <strong>{formatBytes(totalData)}</strong>
                  </td>
                  <td className="mono num">
                    <strong>{formatBytes(totalIndex)}</strong>
                  </td>
                  <td />
                </tr>
              </tfoot>
            )}
          </table>
        </div>
      )}

      {selected.length > 0 && !ctx.readOnly && (
        <div className="batch-toolbar">
          <span>
            Con las <strong>{selected.length}</strong> tablas seleccionadas:
          </span>
          <button className="danger" onClick={() => void batch("drop")}>
            Eliminar (DROP)
          </button>
          <button onClick={() => void batch("truncate")}>Vaciar (TRUNCATE)</button>
        </div>
      )}
    </div>
  );
}
