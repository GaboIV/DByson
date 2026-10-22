import type { QueryResult } from "./api";

export function ResultLimitNotice({ result }: { result: QueryResult | null }) {
  if (!result?.truncated) return null;
  return <div className="alert alert-warning result-limit-warning" role="status">
    Resultado parcial: se alcanzó el límite de {result.truncationReason === "bytes" ? `${Math.round((result.byteLimit || 0) / 1048576)} MiB` : `${result.rowLimit} filas`}. Se muestran {result.rows.length} filas.
    Usa filtros más precisos, reduce las columnas o exporta la tabla para obtener todos los datos.
  </div>;
}
