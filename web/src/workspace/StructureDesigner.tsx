import { useState } from "react";
import type { TableSchema } from "../api";
import { ident } from "./productivity";
import type { ViewContext } from "./shared";

export function StructureDesigner({ ctx, schema, onSQL }: { ctx: ViewContext; schema?: TableSchema; onSQL: (sql: string) => void }) {
  const [action, setAction] = useState("add"),
    [name, setName] = useState(""),
    [column, setColumn] = useState(""),
    [type, setType] = useState("VARCHAR(255)"),
    [nullable, setNullable] = useState(true),
    [unique, setUnique] = useState(false);
  const target = ident(ctx.database) + "." + ident(ctx.table);
  const validName = !!name.trim() && name.length <= 64 && !name.includes("\0");
  const selected = column || schema?.columns[0]?.field || "";
  // Preserve MySQL's complete definition (defaults, comments, collation, generated
  // expression, auto-increment), replacing only the known type prefix.
  const current = schema?.columns.find((c) => c.field === selected);
  const definition = schema?.createStatement
    .split("\n")
    .map((line) => line.trim())
    .find((line) => line.startsWith(ident(selected) + " "))
    ?.slice(ident(selected).length + 1)
    .replace(/,$/, "");
  const typeMatches =
    current &&
    definition?.toLowerCase().startsWith(current.type.toLowerCase()) &&
    /\s|$/.test(definition.slice(current.type.length, current.type.length + 1));
  const modified = current && definition && typeMatches ? type + definition.slice(current.type.length) : "";
  const sql =
    action === "add" && validName
      ? `ALTER TABLE ${target} ADD COLUMN ${ident(name)} ${type} ${nullable ? "NULL" : "NOT NULL"};`
      : action === "modify" && modified
        ? `ALTER TABLE ${target} MODIFY COLUMN ${ident(selected)} ${modified};`
        : action === "drop" && selected
          ? `ALTER TABLE ${target} DROP COLUMN ${ident(selected)};`
          : action === "index" && validName && selected
            ? `ALTER TABLE ${target} ADD ${unique ? "UNIQUE " : ""}INDEX ${ident(name)} (${ident(selected)});`
            : action === "rename" && validName
              ? `RENAME TABLE ${target} TO ${ident(ctx.database)}.${ident(name)};`
              : "";
  return (
    <section className="structure-designer">
      <div>
        <h4>Diseñador de estructura</h4>
        <p className="muted">
          Configura el cambio y genera una sentencia para revisar antes de aplicarla. MySQL confirma DDL de forma implícita; conserva una
          copia de los datos.
        </p>
      </div>
      <div className="productivity-grid">
        <label>
          Operación
          <select disabled={ctx.readOnly} value={action} onChange={(e) => setAction(e.target.value)}>
            <option value="add">Agregar columna</option>
            <option value="modify">Cambiar tipo de columna</option>
            <option value="drop">Eliminar columna</option>
            <option value="index">Crear índice</option>
            <option value="rename">Renombrar tabla</option>
          </select>
        </label>
        {action !== "drop" && action !== "modify" && (
          <label>
            {action === "index" ? "Nombre del índice" : action === "rename" ? "Nuevo nombre de tabla" : "Nombre de columna"}
            <input disabled={ctx.readOnly} value={name} maxLength={64} onChange={(e) => setName(e.target.value)} />
          </label>
        )}
        {(action === "drop" || action === "index" || action === "modify") && (
          <label>
            Columna
            <select disabled={ctx.readOnly} value={selected} onChange={(e) => setColumn(e.target.value)}>
              {schema?.columns.map((c) => (
                <option key={c.field}>{c.field}</option>
              ))}
            </select>
          </label>
        )}
        {(action === "add" || action === "modify") && (
          <>
            <label>
              Tipo
              <select disabled={ctx.readOnly} value={type} onChange={(e) => setType(e.target.value)}>
                {[
                  "VARCHAR(255)",
                  "VARCHAR(100)",
                  "TEXT",
                  "BIGINT",
                  "INT",
                  "INT UNSIGNED",
                  "DECIMAL(12,2)",
                  "BOOLEAN",
                  "DATE",
                  "DATETIME",
                  "TIMESTAMP",
                  "JSON",
                  "BLOB",
                ].map((t) => (
                  <option key={t}>{t}</option>
                ))}
              </select>
            </label>
            {action === "add" && (
              <label className="inline-check">
                <input type="checkbox" disabled={ctx.readOnly} checked={nullable} onChange={(e) => setNullable(e.target.checked)} />
                Permitir NULL
              </label>
            )}
          </>
        )}
        {action === "index" && (
          <label className="inline-check">
            <input type="checkbox" disabled={ctx.readOnly} checked={unique} onChange={(e) => setUnique(e.target.checked)} />
            Índice único
          </label>
        )}
      </div>
      {action === "modify" && !modified && (
        <p className="warn">No se pudo reconstruir esta definición con seguridad. Usa el editor SQL y conserva sus atributos originales.</p>
      )}
      {sql && <pre>{sql}</pre>}
      <button disabled={ctx.readOnly || !sql} onClick={() => onSQL(sql)}>
        Preparar cambio en SQL
      </button>
    </section>
  );
}
