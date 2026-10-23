import { useState } from "react";
import type { DatabaseInfo } from "../api";
import { Spinner } from "../Spinner";
import { formatBytes, SYSTEM_DATABASES, type ViewContext } from "./shared";
import type { Resource } from "./catalog";

interface Props {
  ctx: ViewContext;
  databases: Resource<DatabaseInfo[]>;
  onOpen: (db: string) => void;
  onCreate: () => void;
  onDrop: (db: string) => void;
}

export function ServerDatabases({ ctx, databases, onOpen, onCreate, onDrop }: Props) {
  const [selected, setSelected] = useState<string[]>([]);
  const list = databases.data ?? [];
  const { connection, readOnly } = ctx;

  return (
    <div className="server-dbs-view">
      <div className="unified-top-bar">
        <div className="bar-left">
          <h2>Seleccionar base de datos</h2>
          <div className="btn-group">
            {!readOnly && (
              <button className="primary" onClick={onCreate}>
                + Crear base de datos
              </button>
            )}
            <button onClick={() => void databases.refresh()} disabled={databases.loading || databases.refreshing}>
              🔄 Actualizar
            </button>
            {databases.refreshing && <Spinner label="Actualizando…" />}
          </div>
        </div>
        <div className="bar-right">
          <span className="info-pill">{list.length} bases de datos</span>
          <span className="info-pill">
            {connection.username}@{connection.host}:{connection.port}
          </span>
        </div>
      </div>

      {databases.loading ? (
        <Spinner
          block
          label={connection.ssh?.enabled ? "Conectando por SSH y leyendo las bases de datos…" : "Conectando y leyendo las bases de datos…"}
        />
      ) : databases.error && !list.length ? (
        <div className="alert alert-error">{databases.error}</div>
      ) : (
        <div className="grid-container">
          <table className="data-table">
            <thead>
              <tr>
                <th className="col-check">
                  <input
                    type="checkbox"
                    aria-label="Seleccionar todas las bases de datos"
                    checked={selected.length === list.length && list.length > 0}
                    onChange={(e) => setSelected(e.target.checked ? list.map((d) => d.name) : [])}
                  />
                </th>
                <th>Base de datos</th>
                <th>Acciones</th>
                <th>Colación</th>
                <th className="num">Tablas</th>
                <th className="num">Tamaño total</th>
              </tr>
            </thead>
            <tbody>
              {list.map((d) => {
                const isSelected = selected.includes(d.name);
                return (
                  <tr key={d.name} className={isSelected ? "row-selected" : ""}>
                    <td className="col-check">
                      <input
                        type="checkbox"
                        aria-label={`Seleccionar ${d.name}`}
                        checked={isSelected}
                        onChange={(e) => setSelected((s) => (e.target.checked ? [...s, d.name] : s.filter((n) => n !== d.name)))}
                      />
                    </td>
                    <td>
                      <button className="link-button table-link" onClick={() => onOpen(d.name)}>
                        <strong>{d.name}</strong>
                      </button>
                    </td>
                    <td>
                      <div className="row-action-links">
                        <button className="link-button" onClick={() => onOpen(d.name)}>
                          Abrir
                        </button>
                        {!readOnly && !SYSTEM_DATABASES.includes(d.name.toLowerCase()) && (
                          <>
                            <span>|</span>
                            <button className="link-button link-danger" onClick={() => onDrop(d.name)}>
                              Eliminar
                            </button>
                          </>
                        )}
                      </div>
                    </td>
                    <td className="mono">{d.collation || "—"}</td>
                    <td className="mono num">{d.tables > 0 ? d.tables.toLocaleString() : "—"}</td>
                    <td className="mono num">{d.size > 0 ? formatBytes(d.size) : "—"}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
