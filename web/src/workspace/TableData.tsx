import { useEffect, useRef, useState, type FormEvent } from "react";
import { api, type QueryResult, type TableSchema } from "../api";
import { ActionMenu } from "../ActionMenu";
import { Dialog } from "../Dialog";
import { Select } from "../Select";
import { useConfirm } from "../Confirm";
import { RecordDetail, type RecordSnapshot } from "../RecordDetail";
import { ResultLimitNotice } from "../ResultLimitNotice";
import { RowEditor } from "../RowEditor";
import { Spinner } from "../Spinner";
import { cellText, rowJSON } from "../clipboard";
import { usePreferences } from "../Preferences";
import { errorText, primaryKeyOf, rowValues, tablePath, writableValues, type Row, type ViewContext } from "./shared";

const INLINE_FILTER_LIMIT = 3;

export interface FilterCondition {
  col: string;
  op: string;
  val: string;
}

interface Settings {
  filters: FilterCondition[];
  sortCol: string;
  sortDesc: boolean;
  columns: string[]; // empty = all
  limit: number;
  textLength: number;
}

const defaultFilter = (): FilterCondition => ({
  col: "",
  op: "LIKE",
  val: "",
});

const defaults = (): Settings => ({
  filters: [defaultFilter()],
  sortCol: "",
  sortDesc: false,
  columns: [],
  limit: 50,
  textLength: 100,
});

function isFilterActive(f: FilterCondition): boolean {
  return !!f.val.trim() || f.op === "IS_NULL" || f.op === "IS_NOT_NULL";
}

function appendFilterParams(params: URLSearchParams, filters: FilterCondition[]) {
  const first = filters[0] ?? defaultFilter();
  params.set("filterCol", first.col);
  params.set("filterOp", first.op);
  params.set("filterVal", first.val);
  if (filters.length > 1) {
    params.set("filters", JSON.stringify(filters));
  }
}

const OPERATORS: [string, string][] = [
  ["=", "="],
  ["<", "<"],
  [">", ">"],
  ["<=", "<="],
  [">=", ">="],
  ["!=", "!="],
  ["LIKE", "LIKE"],
  ["LIKE %%", "LIKE %%"],
  ["STARTS", "STARTS"],
  ["REGEXP", "REGEXP"],
  ["IN", "IN"],
  ["IS_NULL", "IS NULL"],
  ["NOT_LIKE", "NOT LIKE"],
  ["IS_NOT_NULL", "IS NOT NULL"],
];

interface Props {
  ctx: ViewContext;
  schema?: TableSchema;
  schemaError?: string;
  onInsert: () => void;
  onDirtyChange: (dirty: boolean) => void;
}

type Editing = { mode: "insert" | "edit"; values: Record<string, unknown>; pk: Record<string, unknown> };

export function TableData({ ctx, schema, schemaError, onInsert, onDirtyChange }: Props) {
  const confirm = useConfirm();
  const { preferences } = usePreferences();
  const { readOnly } = ctx;

  // Draft filter form vs. settings actually applied to the request.
  const [draft, setDraft] = useState<Settings>(defaults);
  const [applied, setApplied] = useState<Settings>(defaults);
  const [shownColumns, setShownColumns] = useState<string[]>([]);
  const [showColPicker, setShowColPicker] = useState(false);
  const [showExtraFilters, setShowExtraFilters] = useState(false);
  const [showFiltersModal, setShowFiltersModal] = useState(false);
  const conditionsRef = useRef<HTMLDivElement>(null);

  const [page, setPage] = useState(1);
  const [pageOffsets, setPageOffsets] = useState([0]);
  const offset = pageOffsets[page - 1] ?? 0;

  const [result, setResult] = useState<QueryResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [selectedRows, setSelectedRows] = useState<number[]>([]);
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [exactTotal, setExactTotal] = useState<number | null>(null);
  const [counting, setCounting] = useState(false);
  const [countError, setCountError] = useState("");
  const [detail, setDetail] = useState<RecordSnapshot | null>(null);
  const [editing, setEditing] = useState<Editing | null>(null);
  const [editDirty, setEditDirty] = useState(false);

  const request = useRef<AbortController | null>(null);
  const countRequest = useRef<AbortController | null>(null);
  // The settings the current `result` was loaded with. Re-activating the tab with the same
  // settings must not hit the server again; only a real change (or an explicit reload) does.
  const loadedKey = useRef("");

  useEffect(() => onDirtyChange(editDirty), [editDirty, onDirtyChange]);
  useEffect(() => {
    if (schema) setShownColumns(schema.columns.map((c) => c.field));
  }, [schema]);
  useEffect(
    () => () => {
      request.current?.abort();
      countRequest.current?.abort();
    },
    [],
  );

  const key = JSON.stringify([ctx.connection.id, ctx.database, ctx.table, offset, applied]);
  const countScope = JSON.stringify([ctx.connection.id, ctx.database, ctx.table, applied.filters]);

  async function load() {
    if (!schema) return;
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setLoading(true);
    setError("");
    setSelectedRows([]);
    setDetail(null);
    try {
      const params = new URLSearchParams({
        offset: String(offset),
        pageSize: String(applied.limit),
        sortCol: applied.sortCol || schema.primaryKey[0] || "",
        sortOrder: applied.sortDesc ? "DESC" : "ASC",
        columns: applied.columns.length ? applied.columns.join(",") : "*",
      });
      appendFilterParams(params, applied.filters);
      const data = await api<QueryResult>("GET", `${tablePath(ctx)}/data?${params}`, undefined, { signal: controller.signal });
      if (controller.signal.aborted) return;
      setResult(data);
      setExpanded({});
      loadedKey.current = key;
      ctx.reportSql(data.executedSql, data.executionMs);
    } catch (e) {
      if (!controller.signal.aborted) setError(errorText(e, "Error cargando los registros"));
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }

  useEffect(() => {
    if (ctx.isActive && schema && loadedKey.current !== key) void load();
    // load() reads the values that make up `key`; the key is the real dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, ctx.isActive, schema]);

  useEffect(() => {
    countRequest.current?.abort();
    setExactTotal(null);
    setCounting(false);
    setCountError("");
  }, [countScope]);

  function reload() {
    loadedKey.current = "";
    setExactTotal(null);
    void load();
  }

  function resetPagination() {
    setPage(1);
    setPageOffsets([0]);
  }

  useEffect(() => {
    if (!showExtraFilters) return;
    const handlePointerDown = (e: PointerEvent) => {
      const target = e.target as Node | null;
      if (!target) return;
      if (conditionsRef.current?.contains(target)) return;
      if (document.querySelector(".select-popover")?.contains(target)) return;
      setShowExtraFilters(false);
    };
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !document.querySelector(".select-popover")) {
        setShowExtraFilters(false);
      }
    };
    document.addEventListener("pointerdown", handlePointerDown);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [showExtraFilters]);

  function applyCurrentDraft() {
    if (schema && shownColumns.length === 0) {
      setError("Selecciona al menos una columna.");
      return false;
    }
    const columns = schema && shownColumns.length === schema.columns.length ? [] : shownColumns;
    setApplied({
      ...draft,
      filters: draft.filters.map((f) => ({ ...f })),
      columns,
      limit: Math.min(500, Math.max(1, draft.limit)),
    });
    resetPagination();
    setShowColPicker(false);
    setShowExtraFilters(false);
    return true;
  }

  function submitFilter(e: FormEvent) {
    e.preventDefault();
    applyCurrentDraft();
  }

  function applyFiltersFromModal(e: FormEvent) {
    e.preventDefault();
    if (applyCurrentDraft()) {
      setShowFiltersModal(false);
    }
  }

  function clearFilter() {
    setDraft(defaults());
    setShownColumns(schema?.columns.map((c) => c.field) ?? []);
    setApplied(defaults());
    resetPagination();
    setShowExtraFilters(false);
    setShowFiltersModal(false);
  }

  function updateCondition(index: number, patch: Partial<FilterCondition>) {
    setDraft((d) => ({
      ...d,
      filters: d.filters.map((f, i) => (i === index ? { ...f, ...patch } : f)),
    }));
  }

  function removeCondition(index: number) {
    setDraft((d) => {
      const next = d.filters.filter((_, i) => i !== index);
      if (next.length <= 1) setShowExtraFilters(false);
      return { ...d, filters: next.length > 0 ? next : [defaultFilter()] };
    });
  }

  function handleAddConditionInline() {
    if (draft.filters.length < INLINE_FILTER_LIMIT) {
      setDraft((d) => ({ ...d, filters: [...d.filters, defaultFilter()] }));
      setShowExtraFilters(true);
    } else {
      setShowExtraFilters(false);
      setShowFiltersModal(true);
    }
  }

  function addConditionInModal() {
    setDraft((d) => ({ ...d, filters: [...d.filters, defaultFilter()] }));
  }

  function sortBy(col: string) {
    const sortDesc = applied.sortCol === col ? !applied.sortDesc : false;
    setDraft((d) => ({ ...d, sortCol: col, sortDesc }));
    setApplied((a) => ({ ...a, sortCol: col, sortDesc }));
    resetPagination();
  }

  async function countRows() {
    countRequest.current?.abort();
    const controller = new AbortController();
    countRequest.current = controller;
    setCounting(true);
    setCountError("");
    try {
      const q = new URLSearchParams();
      appendFilterParams(q, applied.filters);
      const r = await api<{ totalRows: number }>("GET", `${tablePath(ctx)}/count?${q}`, undefined, { signal: controller.signal });
      if (!controller.signal.aborted) setExactTotal(r.totalRows);
    } catch (e) {
      if (!controller.signal.aborted) setCountError(errorText(e, "No se pudo contar; puedes seguir navegando."));
    } finally {
      if (!controller.signal.aborted) setCounting(false);
    }
  }

  function viewRecord(row: Row) {
    if (!result) return;
    const pk = schema ? primaryKeyOf(schema, result.columns, row) : null;
    setDetail({
      columns: [...result.columns],
      row: [...row],
      title: `${ctx.database}.${ctx.table}`,
      schema,
      endpoint: pk ? `${tablePath(ctx)}/row` : undefined,
      pk: pk ?? {},
    });
  }

  async function deleteRow(row: Row) {
    if (!schema || !result) return;
    const pk = primaryKeyOf(schema, result.columns, row);
    if (!pk) return;
    try {
      const r = await ctx.confirmedApi<{ executedSql: string }>("DELETE", `${tablePath(ctx)}/rows`, { pk });
      if (!r) return;
      ctx.reportSql(r.executedSql);
      reload();
    } catch (e) {
      setError(errorText(e, "Error eliminando el registro"));
    }
  }

  async function deleteSelected() {
    if (!schema || !result) return;
    const pks = selectedRows.map((i) => primaryKeyOf(schema, result.columns, result.rows[i])).filter((pk) => pk !== null);
    try {
      const r = await ctx.confirmedApi<{ executedSql: string }>("POST", `${tablePath(ctx)}/batch-delete`, { pks });
      if (!r) return;
      ctx.reportSql(r.executedSql);
      reload();
    } catch (e) {
      setError(errorText(e, "Error eliminando los registros seleccionados"));
    }
  }

  function startEdit(row: Row, mode: "edit" | "insert") {
    if (!schema || !result) return;
    const values = rowValues(schema, result.columns, row);
    if (mode === "insert") {
      // Duplicating: let MySQL assign auto-increment keys.
      for (const col of schema.columns) {
        if (schema.primaryKey.includes(col.field) && col.extra.includes("auto_increment")) values[col.field] = null;
      }
    }
    setEditing({ mode, values, pk: mode === "edit" ? (primaryKeyOf(schema, result.columns, row) ?? {}) : {} });
  }

  async function closeEditor() {
    if (editDirty && !(await confirm("¿Descartar los cambios del registro?"))) return;
    setEditing(null);
    setEditDirty(false);
  }

  async function saveEdit(e: FormEvent) {
    e.preventDefault();
    if (!editing || !schema || readOnly) return;
    const values = writableValues(schema, editing.values);
    try {
      const r =
        editing.mode === "insert"
          ? await api<{ executedSql: string }>("POST", `${tablePath(ctx)}/rows`, { values })
          : await api<{ executedSql: string }>("PUT", `${tablePath(ctx)}/rows`, { pk: editing.pk, values });
      ctx.reportSql(r.executedSql);
      setEditing(null);
      setEditDirty(false);
      reload();
    } catch (err) {
      setError(errorText(err, "Error guardando los cambios"));
    }
  }

  if (!schema) {
    return schemaError ? (
      <div className="alert alert-error">{schemaError}</div>
    ) : (
      <Spinner block label="Leyendo la estructura de la tabla…" />
    );
  }

  const allColumns = schema.columns.map((c) => c.field);
  const columns = result?.columns ?? allColumns;
  const hasFullPK = !!schema.primaryKey.length && schema.primaryKey.every((k) => columns.includes(k));
  const hasAllColumns = schema.columns.every((c) => columns.includes(c.field));
  const pending =
    JSON.stringify({ ...draft, columns: shownColumns.length === allColumns.length ? [] : shownColumns }) !== JSON.stringify(applied);
  const filtered = applied.filters.some(isFilterActive);
  const activeFilterValues = applied.filters.map((f) => f.val.trim()).filter(Boolean);
  const firstFilter = draft.filters[0] ?? defaultFilter();
  const extraInlineFilters = draft.filters.slice(1, INLINE_FILTER_LIMIT);
  const setDraftField = <K extends keyof Settings>(k: K, v: Settings[K]) => setDraft((d) => ({ ...d, [k]: v }));

  return (
    <>
      <form onSubmit={submitFilter} className="adminer-filter-bar">
        <div className="filter-fieldset" role="group" aria-label="Mostrar columnas">
          <span className="filter-label">Mostrar</span>
          <div className="fieldset-content">
            <button
              type="button"
              className="btn-select-cols"
              onClick={() => setShowColPicker((v) => !v)}
              title="Seleccionar columnas a mostrar"
            >
              {shownColumns.length === allColumns.length
                ? "Todas (*)"
                : shownColumns.length === 0
                  ? "Ninguna"
                  : `${shownColumns.length} cols`}{" "}
              ▾
            </button>
            {showColPicker && (
              <>
                <div className="col-picker-backdrop" onClick={() => setShowColPicker(false)} />
                <div className="col-picker-dropdown">
                  <header>
                    <label className="checkbox-mini col-picker-all">
                      <input
                        type="checkbox"
                        checked={shownColumns.length === allColumns.length}
                        onChange={(e) => setShownColumns(e.target.checked ? [...allColumns] : [])}
                      />
                      Todas (*)
                    </label>
                    <span className="muted col-picker-count">
                      {shownColumns.length}/{allColumns.length}
                    </span>
                    <button
                      type="button"
                      className="btn-tiny"
                      onClick={() => setShowColPicker(false)}
                      aria-label="Cerrar selector de columnas"
                    >
                      ✕
                    </button>
                  </header>
                  <div className="col-picker-list">
                    {allColumns.map((col) => (
                      <label key={col} className="col-picker-item">
                        <input
                          type="checkbox"
                          checked={shownColumns.includes(col)}
                          onChange={(e) => setShownColumns((s) => (e.target.checked ? [...s, col] : s.filter((c) => c !== col)))}
                        />
                        <span className="mono">{col}</span>
                      </label>
                    ))}
                  </div>
                </div>
              </>
            )}
          </div>
        </div>

        <div className="filter-fieldset filter-fieldset-conditions" role="group" aria-label="Condición" ref={conditionsRef}>
          <span className="filter-label">Condición</span>
          <div className="fieldset-content">
            <div className="filter-condition-row">
              <Select
                className="filter-col-select"
                value={firstFilter.col}
                onChange={(value) => updateCondition(0, { col: value })}
                label="Columna (o donde sea)"
                options={[{ value: "", label: "(donde sea)" }, ...allColumns.map((c) => ({ value: c, label: c }))]}
              />
              <Select
                className="filter-op-select"
                value={firstFilter.op}
                onChange={(value) => updateCondition(0, { op: value })}
                label="Operador de búsqueda"
                options={OPERATORS.map(([value, label]) => ({ value, label }))}
              />
              {firstFilter.op !== "IS_NULL" && firstFilter.op !== "IS_NOT_NULL" && (
                <input
                  type="text"
                  className="filter-val-input"
                  placeholder="Valor…"
                  aria-label="Valor de búsqueda"
                  value={firstFilter.val}
                  onChange={(e) => updateCondition(0, { val: e.target.value })}
                />
              )}
              <button
                type="button"
                className="btn-filter-add"
                onClick={handleAddConditionInline}
                title={
                  draft.filters.length < INLINE_FILTER_LIMIT ? "Agregar condición" : "Ver todos los filtros configurados y agregar más"
                }
                aria-label="Agregar condición"
              >
                +
              </button>
              {draft.filters.length > 1 && (
                <button
                  type="button"
                  className={`btn-filter-more-count${showExtraFilters ? " is-open" : ""}`}
                  onClick={() => {
                    if (draft.filters.length <= INLINE_FILTER_LIMIT) {
                      setShowExtraFilters((v) => !v);
                    } else {
                      setShowFiltersModal(true);
                    }
                  }}
                  title={
                    draft.filters.length <= INLINE_FILTER_LIMIT
                      ? "Mostrar u ocultar líneas adicionales de filtro"
                      : `Ver los ${draft.filters.length} filtros configurados`
                  }
                  aria-expanded={draft.filters.length <= INLINE_FILTER_LIMIT ? showExtraFilters : undefined}
                >
                  +{draft.filters.length - 1}
                </button>
              )}
            </div>

            {showExtraFilters && extraInlineFilters.length > 0 && (
              <div className="conditions-popover" role="region" aria-label="Condiciones adicionales">
                <header className="conditions-popover-header">
                  <span>
                    Condiciones adicionales ({draft.filters.length}/{INLINE_FILTER_LIMIT})
                  </span>
                  <button
                    type="button"
                    className="btn-tiny"
                    onClick={() => setShowExtraFilters(false)}
                    aria-label="Cerrar condiciones adicionales"
                  >
                    ✕
                  </button>
                </header>
                <div className="conditions-popover-list">
                  {extraInlineFilters.map((cond, offsetIdx) => {
                    const idx = offsetIdx + 1;
                    return (
                      <div key={idx} className="filter-condition-row">
                        <span className="cond-popover-join mono">AND</span>
                        <Select
                          className="filter-col-select"
                          value={cond.col}
                          onChange={(value) => updateCondition(idx, { col: value })}
                          label={`Columna de condición ${idx + 1}`}
                          options={[{ value: "", label: "(donde sea)" }, ...allColumns.map((c) => ({ value: c, label: c }))]}
                        />
                        <Select
                          className="filter-op-select"
                          value={cond.op}
                          onChange={(value) => updateCondition(idx, { op: value })}
                          label={`Operador de condición ${idx + 1}`}
                          options={OPERATORS.map(([value, label]) => ({ value, label }))}
                        />
                        {cond.op !== "IS_NULL" && cond.op !== "IS_NOT_NULL" && (
                          <input
                            type="text"
                            className="filter-val-input"
                            placeholder="Valor…"
                            aria-label={`Valor de condición ${idx + 1}`}
                            value={cond.val}
                            onChange={(e) => updateCondition(idx, { val: e.target.value })}
                          />
                        )}
                        <button
                          type="button"
                          className="btn-filter-remove"
                          onClick={() => removeCondition(idx)}
                          title={`Quitar condición ${idx + 1}`}
                          aria-label={`Quitar condición ${idx + 1}`}
                        >
                          ✕
                        </button>
                      </div>
                    );
                  })}
                </div>
                <footer className="conditions-popover-footer">
                  <button type="button" className="btn-tiny" onClick={handleAddConditionInline}>
                    {draft.filters.length < INLINE_FILTER_LIMIT ? "+ Otra línea" : "Ver todas en modal…"}
                  </button>
                  <button type="submit" className="primary btn-tiny">
                    Mostrar
                  </button>
                </footer>
              </div>
            )}
          </div>
        </div>

        <div className="filter-fieldset" role="group" aria-label="Ordenar">
          <span className="filter-label">Ordenar</span>
          <div className="fieldset-content">
            <Select
              className="filter-sort-select"
              value={draft.sortCol}
              onChange={(value) => setDraftField("sortCol", value)}
              label="Columna de ordenación"
              options={[{ value: "", label: "(clave primaria, si existe)" }, ...allColumns.map((c) => ({ value: c, label: c }))]}
            />
            <label className="checkbox-mini filter-desc-label">
              <input type="checkbox" checked={draft.sortDesc} onChange={(e) => setDraftField("sortDesc", e.target.checked)} />
              desc
            </label>
          </div>
        </div>

        <div className="filter-fieldset" role="group" aria-label="Límite">
          <span className="filter-label">Límite</span>
          <div className="fieldset-content">
            <input
              type="number"
              className="filter-num-input"
              min="1"
              aria-label="Límite de filas"
              max="500"
              value={draft.limit}
              onChange={(e) => setDraftField("limit", Number(e.target.value) || 50)}
            />
          </div>
        </div>

        <div className="filter-fieldset" role="group" aria-label="Longitud">
          <span className="filter-label">Longitud</span>
          <div className="fieldset-content">
            <input
              type="number"
              className="filter-num-input"
              min="10"
              aria-label="Longitud de texto"
              max="5000"
              value={draft.textLength}
              onChange={(e) => setDraftField("textLength", Number(e.target.value) || 100)}
            />
          </div>
        </div>

        <div className="filter-actions">
          {pending && <span className="muted filter-pending">Cambios sin aplicar</span>}
          <button type="submit" className="primary filter-btn-submit">
            Mostrar
          </button>
          {(filtered || applied.sortCol || applied.columns.length > 0 || pending) && (
            <button type="button" onClick={clearFilter} className="filter-btn-clear" title="Limpiar todos los filtros">
              Limpiar
            </button>
          )}
          <button type="button" onClick={reload} disabled={loading} title="Volver a leer los registros del servidor">
            🔄
          </button>
        </div>
      </form>

      <div className="data-view">
        {error && (
          <div className="alert alert-error" role="alert">
            {error}
          </div>
        )}
        <ResultLimitNotice result={result} />
        {selectedRows.length > 0 && !readOnly && (
          <div className="batch-toolbar">
            <span>
              Seleccionados: <strong>{selectedRows.length}</strong> registros
            </span>
            <button className="danger" onClick={() => void deleteSelected()} disabled={!hasFullPK}>
              Eliminar seleccionados
            </button>
          </div>
        )}

        <div className="data-grid-shell" aria-busy={loading}>
          <div className="grid-container">
            <table className={`data-table${loading && result ? " is-reloading" : ""}`}>
              <thead>
                <tr>
                  <th className="col-check-narrow">
                    <input
                      type="checkbox"
                      aria-label="Seleccionar todos los registros"
                      checked={!!result && result.rows.length > 0 && selectedRows.length === result.rows.length}
                      onChange={(e) => setSelectedRows(e.target.checked && result ? result.rows.map((_, i) => i) : [])}
                    />
                  </th>
                  <th className="th-actions">Acciones</th>
                  {columns.map((col) => (
                    <th
                      key={col}
                      onClick={() => sortBy(col)}
                      className={applied.sortCol === col ? `sorted ${applied.sortDesc ? "desc" : "asc"}` : ""}
                    >
                      {col} {applied.sortCol === col ? (applied.sortDesc ? "▼" : "▲") : ""}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {!result ? null : result.rows.length === 0 ? (
                  <tr>
                    <td colSpan={columns.length + 2} className="empty-cell">
                      {result.truncated ? (
                        <p>La fila supera el límite de lectura. Selecciona menos columnas para consultarla.</p>
                      ) : filtered ? (
                        <div className="empty-search-box">
                          <p>
                            🔍 No se encontraron registros con los filtros aplicados
                            {activeFilterValues.length > 0 && (
                              <>
                                : <strong>"{activeFilterValues.join(", ")}"</strong>
                              </>
                            )}
                            .
                          </p>
                          <button onClick={clearFilter}>Limpiar búsqueda</button>
                        </div>
                      ) : (
                        <div className="empty-search-box">
                          <p>{offset ? "No hay registros en esta página." : "Esta tabla está vacía (0 registros)."}</p>
                          {!readOnly && (
                            <button className="primary" onClick={onInsert}>
                              + Insertar primer registro
                            </button>
                          )}
                        </div>
                      )}
                    </td>
                  </tr>
                ) : (
                  result.rows.map((row, r) => {
                    const checked = selectedRows.includes(r);
                    return (
                      <tr key={r} className={checked ? "row-selected" : ""}>
                        <td className="col-check-narrow">
                          <input
                            type="checkbox"
                            aria-label={`Seleccionar registro ${r + 1}`}
                            checked={checked}
                            onChange={(e) => setSelectedRows((s) => (e.target.checked ? [...s, r] : s.filter((i) => i !== r)))}
                          />
                          {preferences.rowNumbers && <span className="row-position">{offset + r + 1}</span>}
                        </td>
                        <td className="td-actions">
                          <div className="row-actions">
                            <button
                              className="btn-action-view"
                              onClick={() => viewRecord(row)}
                              title="Ver detalle"
                              aria-label={`Ver detalle del registro ${r + 1}`}
                            >
                              ⌕
                            </button>
                            {!readOnly && (
                              <button
                                className="btn-action-edit"
                                disabled={!hasFullPK || !hasAllColumns}
                                onClick={() => startEdit(row, "edit")}
                                title="Editar registro"
                                aria-label={`Editar registro ${r + 1}`}
                              >
                                ✎
                              </button>
                            )}
                            <ActionMenu
                              label={`Más acciones de fila ${r + 1}`}
                              actions={[
                                {
                                  label: "Copiar fila como JSON",
                                  onClick: () => void ctx.copy(rowJSON(result.columns, row), "Fila copiada como JSON"),
                                },
                                ...(!readOnly
                                  ? [
                                      { label: "Duplicar registro", disabled: !hasAllColumns, onClick: () => startEdit(row, "insert") },
                                      {
                                        label: "Eliminar registro",
                                        danger: true,
                                        disabled: !hasFullPK,
                                        onClick: () => void deleteRow(row),
                                      },
                                    ]
                                  : []),
                              ]}
                            />
                          </div>
                        </td>
                        {row.map((val, c) => {
                          const cellKey = `${r}-${c}`;
                          const text = val === null ? "NULL" : String(val);
                          const long = val !== null && text.length > applied.textLength;
                          const open = expanded[cellKey];
                          return (
                            <td
                              key={c}
                              className={val === null ? "cell-null" : ""}
                              onClick={() => long && setExpanded((x) => ({ ...x, [cellKey]: !open }))}
                              title={long ? (open ? "Clic para colapsar" : "Clic para ver completo") : undefined}
                            >
                              <div className="cell-content">
                                <span>{val === null ? <em>NULL</em> : long && !open ? `${text.slice(0, applied.textLength)}…` : text}</span>
                                <button
                                  className="cell-copy"
                                  aria-label={`Copiar celda ${result.columns[c]}, fila ${r + 1}`}
                                  title="Copiar valor completo"
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    void ctx.copy(cellText(val), "Celda copiada");
                                  }}
                                >
                                  ⧉
                                </button>
                              </div>
                            </td>
                          );
                        })}
                      </tr>
                    );
                  })
                )}
              </tbody>
            </table>
          </div>
          {loading && (
            <div className="grid-loading">
              <Spinner label={result ? "Actualizando registros…" : "Cargando registros…"} />
            </div>
          )}
        </div>

        <div className="pagination-bar">
          <span className="pagination-info">
            {result &&
              `${result.rows.length} ${result.rows.length === 1 ? "fila" : "filas"}${result.rows.length ? ` · posiciones ${offset + 1}–${offset + result.rows.length}` : ""}`}
            {exactTotal !== null && ` · ${exactTotal.toLocaleString()} coincidencias (conteo exacto)`}
            {countError && (
              <span className="count-error" role="alert">
                {countError}
              </span>
            )}
          </span>
          <div className="pagination-controls">
            <button
              disabled={loading || counting || !result}
              onClick={() => void countRows()}
              title="Ejecuta COUNT(*) solo cuando lo solicitas"
            >
              {counting ? <Spinner label="Contando…" /> : "Contar coincidencias"}
            </button>
            {counting && (
              <button
                onClick={() => {
                  countRequest.current?.abort();
                  setCounting(false);
                }}
              >
                Cancelar conteo
              </button>
            )}
            <button disabled={loading || page <= 1} onClick={() => setPage((p) => Math.max(1, p - 1))}>
              ◀ Anterior
            </button>
            <span>Página {page}</span>
            <button
              disabled={loading || !result?.hasMore}
              onClick={() => {
                if (!result?.nextOffset) return;
                setPageOffsets((o) => [...o.slice(0, page), result.nextOffset!]);
                setPage((p) => p + 1);
              }}
            >
              Siguiente ▶
            </button>
            <Select
              value={String(applied.limit)}
              label="Filas por página"
              onChange={(value) => {
                const limit = Number(value);
                setDraftField("limit", limit);
                setApplied((a) => ({ ...a, limit }));
                resetPagination();
              }}
              options={[...new Set([25, 50, 100, 500, applied.limit])]
                .sort((a, b) => a - b)
                .map((n) => ({ value: String(n), label: `${n} filas` }))}
            />
          </div>
        </div>
      </div>

      {showFiltersModal && (
        <Dialog title="Condiciones de filtro" onClose={() => setShowFiltersModal(false)} className="filters-modal-dialog">
          <form onSubmit={applyFiltersFromModal} className="filters-modal-form">
            <p className="muted">
              Configura las condiciones de filtrado para <strong>{ctx.table}</strong>. Todas las condiciones activas se combinan con{" "}
              <code>AND</code>.
            </p>
            <div className="filters-modal-list">
              {draft.filters.map((cond, idx) => (
                <div key={idx} className="filters-modal-row">
                  <span className="filters-modal-index mono">#{idx + 1}</span>
                  <Select
                    className="filters-modal-col"
                    value={cond.col}
                    onChange={(value) => updateCondition(idx, { col: value })}
                    label={`Columna del filtro ${idx + 1}`}
                    options={[{ value: "", label: "(donde sea)" }, ...allColumns.map((c) => ({ value: c, label: c }))]}
                  />
                  <Select
                    className="filters-modal-op"
                    value={cond.op}
                    onChange={(value) => updateCondition(idx, { op: value })}
                    label={`Operador del filtro ${idx + 1}`}
                    options={OPERATORS.map(([value, label]) => ({ value, label }))}
                  />
                  {cond.op !== "IS_NULL" && cond.op !== "IS_NOT_NULL" ? (
                    <input
                      type="text"
                      className="filters-modal-val"
                      placeholder="Valor…"
                      aria-label={`Valor del filtro ${idx + 1}`}
                      value={cond.val}
                      onChange={(e) => updateCondition(idx, { val: e.target.value })}
                    />
                  ) : (
                    <span className="filters-modal-null-placeholder muted">Sin valor requerido</span>
                  )}
                  <button
                    type="button"
                    className="btn-tiny danger"
                    onClick={() => removeCondition(idx)}
                    title={`Eliminar condición ${idx + 1}`}
                    aria-label={`Eliminar condición ${idx + 1}`}
                  >
                    ✕
                  </button>
                </div>
              ))}
            </div>
            <button type="button" className="align-start" onClick={addConditionInModal}>
              + Agregar condición
            </button>
            <div className="actions filters-modal-actions">
              <button type="button" onClick={() => setDraft((d) => ({ ...d, filters: [defaultFilter()] }))}>
                Limpiar condiciones
              </button>
              <div className="spacer" />
              <button type="button" onClick={() => setShowFiltersModal(false)}>
                Cerrar
              </button>
              <button type="submit" className="primary">
                Aplicar filtros
              </button>
            </div>
          </form>
        </Dialog>
      )}
      {detail && <RecordDetail snapshot={detail} ctx={ctx} onClose={() => setDetail(null)} />}
      {editing && (
        <Dialog
          className="row-editor-dialog"
          title={editing.mode === "insert" ? "Duplicar registro" : "Editar registro"}
          onClose={closeEditor}
        >
          <RowEditor
            schema={schema}
            mode={editing.mode}
            values={editing.values}
            onChange={(values) => setEditing({ ...editing, values })}
            onSubmit={saveEdit}
            onCancel={closeEditor}
            onDirtyChange={setEditDirty}
            disabled={readOnly}
          />
        </Dialog>
      )}
    </>
  );
}
