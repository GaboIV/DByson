import type { TableSchema } from "../api";
import { Spinner } from "../Spinner";
import type { ViewContext } from "./shared";

export function StructureView({ ctx, schema, error }: { ctx: ViewContext; schema?: TableSchema; error?: string }) {
  if (!schema)
    return error ? <div className="alert alert-error">{error}</div> : <Spinner block label="Leyendo la estructura de la tabla…" />;

  return (
    <div className="structure-view">
      <div className="structure-header">
        <h3>Columnas</h3>
        {!ctx.readOnly && <button onClick={() => ctx.openView("modify")}>+ Modificar tabla / Agregar columna</button>}
      </div>

      <table className="data-table">
        <thead>
          <tr>
            <th>Campo</th>
            <th>Tipo</th>
            <th>Colación</th>
            <th>Nulo</th>
            <th>Clave</th>
            <th>Por defecto</th>
            <th>Extra</th>
            <th>Comentario</th>
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
              <td>
                {c.key === "PRI" ? <span className="badge badge-pk">PRIMARIA</span> : c.key ? <span className="badge">{c.key}</span> : "—"}
              </td>
              <td className="mono">{c.default !== null ? c.default : <em>NULL</em>}</td>
              <td className="mono">{c.extra || "—"}</td>
              <td>{c.comment || "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>

      {schema.indexes.length > 0 && (
        <>
          <h3 className="section-gap">Índices</h3>
          <table className="data-table">
            <thead>
              <tr>
                <th>Nombre</th>
                <th>Único</th>
                <th>Columnas</th>
                <th>Tipo</th>
              </tr>
            </thead>
            <tbody>
              {schema.indexes.map((idx) => (
                <tr key={idx.name}>
                  <td>
                    <strong>{idx.name}</strong>
                  </td>
                  <td>{idx.nonUnique ? "No" : "Sí"}</td>
                  <td className="mono">{idx.columns.join(", ")}</td>
                  <td>{idx.type}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}

      {schema.createStatement && (
        <>
          <div className="ddl-header">
            <h3>Sentencia DDL (CREATE TABLE)</h3>
            <button className="btn-tiny" onClick={() => void ctx.copy(schema.createStatement, "DDL copiado")}>
              Copiar DDL
            </button>
          </div>
          <pre className="ddl-block">{schema.createStatement}</pre>
        </>
      )}
    </div>
  );
}
