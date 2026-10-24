import { cellText } from "../clipboard";
import type { QueryResult } from "../api";

export function ExplainPlan({ result }: { result: QueryResult }) {
  const value = (row: QueryResult["rows"][number], name: string) =>
    cellText(row[result.columns.findIndex((c) => c.toLowerCase() === name.toLowerCase())]);
  const max = Math.max(1, ...result.rows.map((row) => Number(value(row, "rows")) || 0));
  return (
    <section className="explain-plan" aria-label="Plan visual EXPLAIN">
      <div>
        <strong>Plan de ejecución estimado</strong>
        <p className="muted">
          Orden de acceso y filas estimadas por MySQL. EXPLAIN no ejecuta la consulta. Las cifras no son tiempos reales.
        </p>
      </div>
      {result.rows.map((row, i) => {
        const access = value(row, "type"),
          rows = Number(value(row, "rows")) || 0;
        return (
          <article className={`plan-node ${access === "ALL" ? "plan-scan" : ""}`} key={i}>
            <span className="plan-order">{i + 1}</span>
            <div>
              <strong>{value(row, "table") || "Expresión"}</strong>
              <small>
                {value(row, "select_type")} · acceso {access || "—"}
              </small>
              <span>Índice: {value(row, "key") || "Sin índice"}</span>
              <p>{value(row, "Extra")}</p>
            </div>
            <div>
              <strong>{rows.toLocaleString()} filas</strong>
              <div className="plan-meter">
                <span style={{ width: `${Math.max(2, (rows / max) * 100)}%` }} />
              </div>
              <small>Filtradas: {value(row, "filtered") || "—"}%</small>
            </div>
          </article>
        );
      })}
      {result.truncated && <p className="warn">Plan truncado por el límite del resultado.</p>}
    </section>
  );
}
