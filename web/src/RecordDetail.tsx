import { useEffect, useRef, useState } from "react";
import { api, type QueryResult, type TableSchema } from "./api";
import { cellText, rowJSON, useClipboard, type DataRow } from "./clipboard";
import { useId } from "react";
import { ResultLimitNotice } from "./ResultLimitNotice";
import { enc, type ViewContext } from "./workspace/shared";
import { ident, literal, type ForeignKey } from "./workspace/productivity";

export interface RecordSnapshot {
  title: string;
  columns: string[];
  row: DataRow;
  schema?: TableSchema;
  endpoint?: string;
  pk?: Record<string, unknown>;
}

function formatted(value: DataRow[number]) {
  if (typeof value === "string" && /^[\s]*[\[{]/.test(value)) {
    try {
      return JSON.stringify(JSON.parse(value), null, 2);
    } catch {
      /* ordinary text */
    }
  }
  return cellText(value);
}

export function RecordDetail({ snapshot, onClose, ctx }: { snapshot: RecordSnapshot; onClose: () => void; ctx?: ViewContext }) {
  const [result, setResult] = useState<QueryResult | null>(null);
  const [loading, setLoading] = useState(!!snapshot.endpoint);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [view, setView] = useState<"record" | "json">("record");
  const [filter, setFilter] = useState("all");
  const [expanded, setExpanded] = useState<Record<number, boolean>>({});
  const titleId = useId();
  const { copy, copyNotice } = useClipboard();
  const panel = useRef<HTMLElement>(null);
  const [relations, setRelations] = useState<ForeignKey[]>([]);
  const [relationError, setRelationError] = useState("");
  useEffect(() => {
    if (!ctx?.database || !ctx.table) return;
    const controller = new AbortController();
    void api<ForeignKey[]>("GET", `/api/connections/${ctx.connection.id}/databases/${enc(ctx.database)}/relations`, undefined, {
      signal: controller.signal,
    })
      .then((keys) => setRelations(keys.filter((k) => k.table === ctx.table)))
      .catch((e) => {
        if (!controller.signal.aborted) setRelationError(e instanceof Error ? e.message : "No se pudieron leer las relaciones");
      });
    return () => controller.abort();
  }, [ctx?.connection.id, ctx?.database, ctx?.table]);

  useEffect(() => {
    const previous = document.activeElement;
    panel.current?.querySelector<HTMLButtonElement>("button")?.focus();
    return () => {
      if (previous instanceof HTMLElement && previous.isConnected) previous.focus();
    };
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    if (snapshot.endpoint) {
      void api<QueryResult>("POST", snapshot.endpoint, { pk: snapshot.pk }, { signal: controller.signal })
        .then((data) => {
          if (controller.signal.aborted) return;
          setResult(data);
          if (!data.rows.length && !data.truncated)
            setError("El registro ya no existe o cambió su clave. Se conserva la fila que abriste.");
        })
        .catch((e) => {
          if (!controller.signal.aborted) setError(e instanceof Error ? e.message : "No se pudo actualizar el registro.");
        })
        .finally(() => {
          if (!controller.signal.aborted) setLoading(false);
        });
    }
    return () => controller.abort();
  }, [snapshot]);

  const columns = result?.rows.length ? result.columns : snapshot.columns;
  const row = result?.rows[0] || snapshot.row;
  const fields = columns.map((column, index) => ({
    column,
    index,
    value: row[index],
    type: snapshot.schema?.columns.find((f) => f.field === column)?.type,
  }));
  const shown = fields.filter(
    (f) =>
      `${f.column} ${cellText(f.value)}`.toLowerCase().includes(search.toLowerCase()) &&
      (filter === "all" ||
        (filter === "value" && f.value !== null && f.value !== "") ||
        (filter === "null" && f.value === null) ||
        (filter === "keys" && snapshot.schema?.primaryKey.includes(f.column))),
  );
  return (
    <div className="modal-overlay record-overlay" onClick={onClose}>
      <section
        ref={panel}
        className="modal-content record-detail record-inspector"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            e.stopPropagation();
            onClose();
          }
          if (e.key === "Tab") {
            const targets = [...e.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled),input,select,[tabindex="0"]')].filter(
              (el) => el.getClientRects().length,
            );
            const first = targets[0],
              last = targets[targets.length - 1];
            if (e.shiftKey && document.activeElement === first) {
              e.preventDefault();
              last?.focus();
            }
            if (!e.shiftKey && document.activeElement === last) {
              e.preventDefault();
              first?.focus();
            }
          }
        }}
      >
        <header className="inspector-header">
          <div>
            <p className="eyebrow">DETALLE DEL REGISTRO</p>
            <h3 id={titleId}>{snapshot.title}</h3>
            <div className="record-identity">
              {snapshot.pk &&
                Object.entries(snapshot.pk).map(([key, val]) => (
                  <span key={key}>
                    <strong>{key}</strong> {cellText(val as DataRow[number])}
                  </span>
                ))}
            </div>
          </div>
          <button onClick={onClose} aria-label="Cerrar detalle">
            ✕
          </button>
        </header>
        <div className="inspector-view-tabs" role="tablist" aria-label="Presentación del registro">
          <button role="tab" aria-selected={view === "record"} onClick={() => setView("record")}>
            Ficha
          </button>
          <button role="tab" aria-selected={view === "json"} onClick={() => setView("json")}>
            JSON
          </button>
          <span className="spacer" />
          <button onClick={() => void copy(rowJSON(columns, row), "Registro copiado como JSON")}>Copiar registro JSON</button>
        </div>
        <div className="inspector-status">
          <span>{columns.length} campos</span>
          <span>{loading ? "Actualizando…" : result?.rows.length ? "Registro completo" : "Columnas del resultado"}</span>
        </div>
        {error && (
          <p className="inspector-warning" role="alert">
            {error}
          </p>
        )}
        <ResultLimitNotice result={result} />
        {relations.length > 0 && (
          <nav className="record-relations" aria-label="Registros relacionados">
            {relations.map((key) => {
              const values = key.columns.map((c) => row[columns.indexOf(c)]);
              const available = values.every((v) => v !== null && v !== undefined);
              return (
                <button
                  key={key.name}
                  disabled={!available}
                  title={`${key.name}: ${key.columns.join(", ")} → ${key.referencedColumns.join(", ")}`}
                  onClick={() => {
                    const condition = key.referencedColumns.map((c, i) => `${ident(c)} = ${literal(cellText(values[i]))}`).join(" AND ");
                    ctx?.newSQL(
                      `SELECT * FROM ${ident(key.referencedDatabase)}.${ident(key.referencedTable)} WHERE ${condition} LIMIT 50;`,
                      key.referencedDatabase,
                    );
                    onClose();
                  }}
                >
                  ↗ {key.referencedTable}
                  <small>{key.columns.join(" + ")}</small>
                </button>
              );
            })}
          </nav>
        )}
        {relationError && (
          <p className="inspector-warning" role="alert">
            Relaciones: {relationError}
          </p>
        )}
        {view === "record" ? (
          <>
            <div className="inspector-search">
              <label>
                <span className="sr-only">Buscar campo o valor</span>
                <input type="search" value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Buscar campo o valor…" />
              </label>
              <select aria-label="Filtrar campos" value={filter} onChange={(e) => setFilter(e.target.value)}>
                <option value="all">Todos los campos</option>
                <option value="value">Con valor</option>
                <option value="null">Solo NULL</option>
                <option value="keys">Claves primarias</option>
              </select>
            </div>
            <div className="inspector-properties">
              {shown.map(({ column, index, value, type }) => {
                const text = formatted(value);
                const long = text.length > 300;
                return (
                  <section className="inspector-property" key={`${column}-${index}`}>
                    <div className="property-name">
                      <strong>{column}</strong>
                      {type && <span className="mono muted">{type}</span>}
                      {snapshot.schema?.primaryKey.includes(column) && <span className="property-key">PK</span>}
                    </div>
                    <div className="property-value">
                      {value === null ? (
                        <span className="value-empty">NULL</span>
                      ) : value === "" ? (
                        <span className="value-empty">Cadena vacía</span>
                      ) : (
                        <pre tabIndex={0}>{long && !expanded[index] ? text.slice(0, 300) + "…" : text}</pre>
                      )}
                      {long && (
                        <button className="property-expand" onClick={() => setExpanded((v) => ({ ...v, [index]: !v[index] }))}>
                          {expanded[index] ? "Contraer" : "Mostrar completo"}
                        </button>
                      )}
                    </div>
                    <button
                      className="property-copy"
                      title="Copiar valor completo"
                      aria-label={`Copiar campo ${column}`}
                      onClick={() => void copy(cellText(value), `Campo ${column} copiado`)}
                    >
                      ⧉
                    </button>
                  </section>
                );
              })}
              {!shown.length && <p className="muted">No hay campos que coincidan.</p>}
            </div>
          </>
        ) : (
          <pre className="inspector-json" tabIndex={0}>
            {rowJSON(columns, row)}
          </pre>
        )}
        <footer className="inspector-footer">
          {snapshot.endpoint ? "Lectura por clave primaria · Vista de consulta" : "Valores disponibles en este resultado"}
          <span className="spacer" />
          <span>Esc para cerrar</span>
        </footer>
        {copyNotice}
      </section>
    </div>
  );
}
