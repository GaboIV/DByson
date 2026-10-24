import { useState } from "react";
import { api, type TableInfo } from "../api";
import { enc, errorText, type ViewContext } from "./shared";

type Format = "sql" | "csv" | "json";

export function ExportView({ ctx, tables }: { ctx: ViewContext; tables: TableInfo[] }) {
  const [format, setFormat] = useState<Format>("sql");
  const [table, setTable] = useState(ctx.table);
  const [structure, setStructure] = useState(true);
  const [data, setData] = useState(true);
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState("");

  const ready = !!ctx.database && (format === "sql" ? structure || data : !!table);
  const href = ready
    ? `/api/connections/${ctx.connection.id}/export?db=${enc(ctx.database)}${table ? `&table=${enc(table)}` : ""}&format=${format}&structure=${structure ? "1" : "0"}&data=${data ? "1" : "0"}`
    : undefined;

  return (
    <div className="export-view">
      <h2>Exportar base de datos: {ctx.database || "todas"}</h2>
      <div className="card form export-card">
        <p className="muted">
          Descarga un volcado de <strong>{ctx.database || ctx.connection.name}</strong>.
        </p>
        <label>
          Formato de salida
          <select value={format} onChange={(e) => setFormat(e.target.value as Format)}>
            <option value="sql">SQL Dump (CREATE + INSERT)</option>
            <option value="csv">CSV (solo datos de tabla)</option>
            <option value="json">JSON (colección de registros)</option>
          </select>
        </label>
        <label>
          Tabla a exportar
          <select value={table} onChange={(e) => setTable(e.target.value)}>
            <option value="">{format === "sql" ? "Todas las tablas de la base" : "Selecciona una tabla"}</option>
            {tables.map((t) => (
              <option key={t.name} value={t.name}>
                {t.name}
              </option>
            ))}
          </select>
        </label>
        <p className="muted">La descarga recorre los datos fila por fila. CSV y JSON requieren una tabla seleccionada.</p>
        {format === "sql" && (
          <div className="export-checkboxes">
            <label className="check">
              <input type="checkbox" checked={structure} onChange={(e) => setStructure(e.target.checked)} />
              Incluir estructura (CREATE TABLE)
            </label>
            <label className="check">
              <input type="checkbox" checked={data} onChange={(e) => setData(e.target.checked)} />
              Incluir datos (INSERT INTO)
            </label>
          </div>
        )}
        <div className="actions">
          <a
            className="primary button-link"
            href={href}
            aria-disabled={!ready || checking}
            download
            onClick={async (event) => {
              event.preventDefault();
              if (!href || checking) return;
              setChecking(true);
              setError("");
              try {
                // Keep the download streamed by the browser; verify the session first
                // so a stale cookie redirects the app instead of downloading a 401 JSON.
                await api("GET", "/api/auth/me");
                const link = document.createElement("a");
                link.href = href;
                // The export endpoint supplies Content-Disposition and its filename.
                document.body.append(link);
                link.click();
                link.remove();
              } catch (e) {
                setError(errorText(e, "No se pudo iniciar la descarga"));
              } finally {
                setChecking(false);
              }
            }}
          >
            {checking ? "Comprobando sesión…" : "⬇ Descargar archivo"}
          </a>
        </div>
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
      </div>
    </div>
  );
}
