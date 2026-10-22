import { useCallback, useEffect, useRef, useState, useId, type ReactNode } from "react";
import { usePreferences } from "./Preferences";
import { TabStrip } from "./TabStrip";
import { ActionMenu } from "./ActionMenu";
import { Dialog } from "./Dialog";
import { RowEditor } from "./RowEditor";
import { Brand } from "./Brand";
import { useConfirmedApi } from "./useConfirmedApi";
import { ResultLimitNotice } from "./ResultLimitNotice";
import { RecordDetail, type RecordSnapshot } from "./RecordDetail";
import { useClipboard, rowJSON, cellText } from "./clipboard";
import {
  api,
  ApiError,
  type Connection,
  type TableInfo,
  type TableSchema,
  type QueryResult,
  type QueryResponse,
  type DatabasesResponse,
  type DatabaseInfo,
  type TablesResponse,
  type NewColumnDef,
} from "./api";

interface WorkspaceProps {
 initialDatabase?: string; initialTable?: string; initialView?: WorkspaceView; initialSQL?: string;
 isActive?: boolean;
 viewNavigation?: ReactNode;
 onOpenView?: (database:string,table:string,view:WorkspaceView)=>void;
 onCloseTab?: ()=>void;
 onOpenTable?: (database: string,table: string) => void;
 onNewSQL?: (database: string, query?: string) => void;
 onContextChange?: (ctx: {database: string; table: string; sql: boolean; view:WorkspaceView; dirty: boolean}) => void;
  connection: Connection;
  username: string;
  onLogout: () => void;
  onBack: () => void;
}

export type WorkspaceView = "browse" | "data" | "structure" | "modify" | "insert" | "sql" | "export";
type ActiveTab = "server-dbs" | "db-overview" | "data" | "structure" | "modify" | "insert" | "sql" | "export";

interface DataSettings { filterCol: string; filterOp: string; filterVal: string; sortCol: string; sortDesc: boolean; columns: string[]; limit: number; textLength: number }
const defaultSettings = (): DataSettings => ({ filterCol: "", filterOp: "LIKE", filterVal: "", sortCol: "", sortDesc: false, columns: [], limit: 50, textLength: 100 });

function cleanStr(s: string): string {
  return (s || "").normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase();
}

function formatBytes(bytes: number): string {
  if (!bytes || bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + " " + (sizes[i] || "B");
}

export function Workspace({ connection, username, onLogout, onBack, initialDatabase, initialTable, initialView, initialSQL, isActive = true, viewNavigation, onOpenView, onCloseTab, onOpenTable, onNewSQL, onContextChange }: WorkspaceProps) {
  const {preferences}=usePreferences();
  const readOnly=connection.readOnly||preferences.viewOnly;
  const [rowDirty,setRowDirty]=useState(false),[rowNotice,setRowNotice]=useState("");
  const { confirmedApi, confirmationDialog } = useConfirmedApi(`${connection.name} · ${connection.env.toUpperCase()}`);
  const { copy, copyNotice } = useClipboard();
 const dbSelectId = useId();
  const [detail, setDetail] = useState<RecordSnapshot | null>(null);
  const [applied, setApplied] = useState<DataSettings>(defaultSettings);
  const [pageOffsets, setPageOffsets] = useState([0]);
  const [exactTotal, setExactTotal] = useState<number | null>(null);
  const [counting, setCounting] = useState(false);
  const [countError, setCountError] = useState("");
  const dataRequest = useRef<AbortController | null>(null);
  const countRequest = useRef<AbortController | null>(null);
  const tablesRequest = useRef<AbortController | null>(null);
  const databasesRequest = useRef<AbortController | null>(null);
  const schemaCache = useRef<{ key: string; schema: TableSchema } | null>(null);
  // Databases & Server State
  const [databases, setDatabases] = useState<string[]>([]);
  const [databaseDetails, setDatabaseDetails] = useState<DatabaseInfo[]>([]);
  const [selectedDb, setSelectedDb] = useState<string>(initialDatabase ?? connection.database ?? "");
  const [creatingDb, setCreatingDb] = useState(false);
  const [newDbName, setNewDbName] = useState("");

  // Tables State
  const [tables, setTables] = useState<TableInfo[]>([]);
  const [tableFilter, setTableFilter] = useState("");
  const [selectedTable, setSelectedTable] = useState<string | null>(initialTable || null);
  const [activeTab, setActiveTab] = useState<ActiveTab>(initialView && initialView !== "browse" ? initialView : initialTable ? "data" : (initialDatabase ?? connection.database) ? "db-overview" : "server-dbs");

  const currentView = useRef(activeTab); currentView.current = activeTab;

  // SQL Query Banner
  const [lastExecutedSql, setLastExecutedSql] = useState<string>("");
  const [lastExecutionMs, setLastExecutionMs] = useState<number | null>(null);

  // Table Data State
  const [schema, setSchema] = useState<TableSchema | null>(null);
  const [dataResult, setDataResult] = useState<QueryResult | null>(null);
  const [page, setPage] = useState(1);
  const [limit, setLimit] = useState(50);
  const [textLength, setTextLength] = useState(100);
  const [selectedColumns, setSelectedColumns] = useState<string[]>([]);
  const [showColPicker, setShowColPicker] = useState(false);

  // Adminer Filters State (Point 5)
  const [filterCol, setFilterCol] = useState("");
  const [filterOp, setFilterOp] = useState("LIKE");
  const [filterVal, setFilterVal] = useState("");
  const [activeFilterText, setActiveFilterText] = useState("");
  const [sortCol, setSortCol] = useState("");
  const [sortDesc, setSortDesc] = useState(false);

  // Selection states
  const [selectedRows, setSelectedRows] = useState<number[]>([]);
  const [selectedTables, setSelectedTables] = useState<string[]>([]);
  const [selectedDbs, setSelectedDbs] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  // Expanded cells (for text length truncation toggle)
  const [expandedCells, setExpandedCells] = useState<Record<string, boolean>>({});

  // Edit / Insert Row Modal State
  const [editingRow, setEditingRow] = useState<{
    mode: "insert" | "edit";
    values: Record<string, any>;
    pk: Record<string, any>;
  } | null>(null);

  // Create Table Modal State
  const [creatingTable, setCreatingTable] = useState(false);
  const [newTableName, setNewTableName] = useState("");
  const [newTableCols, setNewTableCols] = useState<NewColumnDef[]>([
    { name: "id", type: "INT", length: "11", null: false, autoIncrement: true, primaryKey: true },
    { name: "name", type: "VARCHAR", length: "255", null: false, autoIncrement: false, primaryKey: false },
    { name: "created_at", type: "DATETIME", length: "", null: false, default: "CURRENT_TIMESTAMP", autoIncrement: false, primaryKey: false },
  ]);

  // SQL Console State
  const [sqlQuery, setSqlQuery] = useState(initialSQL || "SELECT 1;");
  const [sqlResult, setSqlResult] = useState<QueryResult | null>(null);
  const [sqlLoading, setSqlLoading] = useState(false);
  const [sqlError, setSqlError] = useState("");
  const [queryHistory, setQueryHistory] = useState<string[]>([]);

  // Export State
  const [exportFormat, setExportFormat] = useState<"sql" | "csv" | "json">("sql");
  const [exportIncludeStructure, setExportIncludeStructure] = useState(true);
  const [exportIncludeData, setExportIncludeData] = useState(true);

  // Alter Table State (Point 4)
  const [alterSql, setAlterSql] = useState<string>(initialTable ? `ALTER TABLE \`${initialTable}\` ADD COLUMN \`nueva_columna\` VARCHAR(255) NULL;` : "");
  const [alterStatus, setAlterStatus] = useState<{ isError: boolean; message: string } | null>(null);

  // Point 1: Clean all filters, sort, pagination, and selection on changing table
  useEffect(() => { onContextChange?.({database:selectedDb,table:selectedTable||"",sql:activeTab==="sql",view:activeTab==="server-dbs"||activeTab==="db-overview"?"browse":activeTab,dirty:rowDirty||sqlQuery!==(initialSQL||"SELECT 1;")||(activeTab==="modify"&&!!alterSql&&alterSql!==`ALTER TABLE \`${selectedTable}\` ADD COLUMN \`nueva_columna\` VARCHAR(255) NULL;`)}); },[selectedDb,selectedTable,activeTab,sqlQuery,rowDirty,alterSql]);
  function openView(view:WorkspaceView){if(selectedTable&&onOpenView)onOpenView(selectedDb,selectedTable,view);else if(view!=="browse")setActiveTab(view);}

  function openTable(tableName: string) {
   if (selectedTable === tableName) {openView("data");return;}
   if (onOpenTable) { onOpenTable(selectedDb,tableName); return; }
    setSelectedTable(tableName);
    setActiveTab("data");
    setFilterCol("");
    setFilterOp("LIKE");
    setFilterVal("");
    setActiveFilterText("");
    setSortCol("");
    setSortDesc(false);
    setSelectedColumns([]);
    setLimit(50);
    setTextLength(100);
    setPage(1);
    setPageOffsets([0]);
    setApplied(defaultSettings());
    setDataResult(null);
    setSchema(null);
    setSelectedRows([]);
    setExpandedCells({});
    setAlterSql(`ALTER TABLE \`${tableName}\` ADD COLUMN \`nueva_columna\` VARCHAR(255) NULL;`);
    setAlterStatus(null);
  }

  // Execute Alter Table statement (Point 4)
  const handleExecuteAlter = async (e?: React.FormEvent) => {
    if (e) e.preventDefault();
    if (readOnly || !alterSql.trim() || !selectedDb || loading) return;
    setLoading(true);
    setAlterStatus(null);
    setError("");
    const start = performance.now();
    try {
      const resp = await confirmedApi<QueryResponse>("POST", `/api/connections/${connection.id}/query`, {
        query: alterSql,
        database: selectedDb,
      });
      if (!resp) return;
      const ms = Math.round(performance.now() - start);
      if (!resp.ok) {
        setAlterStatus({ isError: true, message: resp.error || "Error al ejecutar la sentencia" });
        return;
      }
      setLastExecutedSql(alterSql);
      setLastExecutionMs(ms);
      setAlterStatus({
        isError: false,
        message: `✔ Sentencia ejecutada con éxito en ${ms} ms (${resp.result?.affectedRows ?? 0} filas afectadas)`,
      });
      schemaCache.current = null;
      // Refresh schema and tables
      if (selectedTable) {
        const sch = await api<TableSchema>(
          "GET",
          `/api/connections/${connection.id}/databases/${selectedDb}/tables/${selectedTable}/schema`
        );
        setSchema(sch);
      }
      void loadTables();
    } catch (err) {
      setAlterStatus({
        isError: true,
        message: err instanceof ApiError ? err.message : "Error al ejecutar ALTER TABLE",
      });
    } finally {
      setLoading(false);
    }
  };

  // 1. Load Databases
  const loadDatabases = useCallback(async () => {
    databasesRequest.current?.abort();
    const controller = new AbortController(); databasesRequest.current = controller;
    try {
      const resp = await api<DatabasesResponse>("GET", `/api/connections/${connection.id}/databases`, undefined, { signal: controller.signal });
      if (controller.signal.aborted) return;
      setDatabases(resp.databases);
      if (resp.databaseDetails) {
        setDatabaseDetails(resp.databaseDetails);
      }
      if (resp.executedSql && currentView.current === "server-dbs") {
        setLastExecutedSql(resp.executedSql);setLastExecutionMs(null);
      }
    } catch (e) {
      if (controller.signal.aborted) return;
      setError(e instanceof ApiError ? e.message : "Error cargando bases de datos");
    }
  }, [connection.id]);

  useEffect(() => {
    if (isActive) void loadDatabases();
    return () => databasesRequest.current?.abort();
  }, [loadDatabases, isActive]);

  // 2. Load Tables when selectedDb changes
  const loadTables = useCallback(async () => {
    tablesRequest.current?.abort();
    const controller = new AbortController(); tablesRequest.current = controller;
    if (!selectedDb) {
      setTables([]);
      return;
    }
    try {
      setError("");
      const resp = await api<TablesResponse>("GET", `/api/connections/${connection.id}/databases/${selectedDb}/tables`, undefined, { signal: controller.signal });
      if (controller.signal.aborted) return;
      setTables(resp.tables);
      if (resp.executedSql && currentView.current === "db-overview") {
        setLastExecutedSql(resp.executedSql);setLastExecutionMs(null);
      }
    } catch (e) {
      if (controller.signal.aborted) return;
      setError(e instanceof ApiError ? e.message : "Error cargando tablas");
    }
  }, [connection.id, selectedDb]);

  useEffect(() => {
    if (isActive) void loadTables();
    return () => tablesRequest.current?.abort();
  }, [loadTables, isActive]);

  const offset = pageOffsets[page - 1] ?? 0;
  const countScope = JSON.stringify([connection.id, selectedDb, selectedTable, applied.filterCol, applied.filterOp, applied.filterVal]);

  useEffect(() => {
    countRequest.current?.abort(); setExactTotal(null); setCounting(false); setCountError("");
    return () => countRequest.current?.abort();
  }, [countScope]);

  useEffect(() => { setDetail(null); }, [connection.id, selectedDb, selectedTable, activeTab, page, applied]);

  // The callback depends only on applied settings, never on the draft form.
  const loadTableData = useCallback(async () => {
    dataRequest.current?.abort();
    const controller = new AbortController(); dataRequest.current = controller;
    if (!selectedDb || !selectedTable || !["data", "structure", "modify", "insert"].includes(activeTab)) { setLoading(false); return; }
    setLoading(true); setError(""); setSelectedRows([]); setDataResult(null);
    try {
      const key = JSON.stringify([connection.id, selectedDb, selectedTable]);
      const sch = schemaCache.current?.key === key && activeTab === "data"
        ? schemaCache.current.schema
        : await api<TableSchema>("GET", `/api/connections/${connection.id}/databases/${selectedDb}/tables/${selectedTable}/schema`, undefined, { signal: controller.signal });
      if (controller.signal.aborted) return;
      schemaCache.current = { key, schema: sch }; setSchema(sch);
      if (activeTab === "data") {
        const queryParams = new URLSearchParams({
          offset: String(offset), pageSize: String(applied.limit),
          sortCol: applied.sortCol || sch.primaryKey[0] || "", sortOrder: applied.sortDesc ? "DESC" : "ASC",
          filterCol: applied.filterCol, filterOp: applied.filterOp, filterVal: applied.filterVal,
          columns: applied.columns.length ? applied.columns.join(",") : "*",
        });
        const data = await api<QueryResult>("GET", `/api/connections/${connection.id}/databases/${selectedDb}/tables/${selectedTable}/data?${queryParams}`, undefined, { signal: controller.signal });
        if (controller.signal.aborted) return;
        setDataResult(data); setLastExecutedSql(data.executedSql); setLastExecutionMs(data.executionMs);
        setActiveFilterText(applied.filterVal);
      }
    } catch (e) {
      if (!controller.signal.aborted) setError(e instanceof ApiError ? e.message : "Error cargando datos de la tabla");
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }, [connection.id, selectedDb, selectedTable, activeTab, offset, applied]);

  useEffect(() => { if (isActive) void loadTableData(); return () => dataRequest.current?.abort(); }, [loadTableData,isActive]);
 useEffect(() => { if (!isActive) { countRequest.current?.abort();setCounting(false);setDetail(null); } },[isActive]);

  useEffect(() => {
    if (schema) setSelectedColumns(schema.columns.map((column) => column.field));
  }, [schema]);

  function refreshTableData() {
    countRequest.current?.abort(); setExactTotal(null); setCounting(false);
    schemaCache.current = null;
    void loadTableData();
  }

  function resetPagination() { setPage(1); setPageOffsets([0]); }

  function handleHeaderSort(col: string) {
    const descending = applied.sortCol === col ? !applied.sortDesc : false;
    setSortCol(col); setSortDesc(descending); setApplied((current) => ({ ...current, sortCol: col, sortDesc: descending })); resetPagination();
  }

  function draftSettings(): DataSettings {
    const columns = schema && selectedColumns.length === schema.columns.length ? [] : selectedColumns;
    return { filterCol, filterOp, filterVal, sortCol, sortDesc, columns, limit: Math.min(500, Math.max(1, limit)), textLength };
  }

  function handleFilterSubmit(e?: React.FormEvent) {
    e?.preventDefault();
    if (schema && selectedColumns.length === 0) { setError("Selecciona al menos una columna."); return; }
    setApplied(draftSettings()); resetPagination(); setShowColPicker(false);
  }

  function clearFilter() {
    setFilterCol(""); setFilterOp("LIKE"); setFilterVal(""); setActiveFilterText("");
    setSortCol(""); setSortDesc(false); setLimit(50); setTextLength(100);
    setSelectedColumns(schema?.columns.map((column) => column.field) || []);
    setApplied(defaultSettings()); resetPagination();
  }

  async function countRows() {
    if (!selectedDb || !selectedTable) return;
    countRequest.current?.abort();
    const controller = new AbortController(); countRequest.current = controller;
    setCounting(true); setCountError("");
    try {
      const query = new URLSearchParams({ filterCol: applied.filterCol, filterOp: applied.filterOp, filterVal: applied.filterVal });
      const result = await api<{ totalRows: number }>("GET", `/api/connections/${connection.id}/databases/${selectedDb}/tables/${selectedTable}/count?${query}`, undefined, { signal: controller.signal });
      if (!controller.signal.aborted) setExactTotal(result.totalRows);
    } catch (e) {
      if (!controller.signal.aborted) setCountError(e instanceof Error ? e.message : "No se pudo contar; puedes seguir navegando.");
    } finally { if (!controller.signal.aborted) setCounting(false); }
  }

  function viewRecord(row: QueryResult["rows"][number], columns = dataResult?.columns || [], fromSQL = false) {
    const keys: Record<string, unknown> = {};
    if (!fromSQL && schema?.primaryKey.length) {
      for (const key of schema.primaryKey) { const index = columns.indexOf(key); if (index >= 0) keys[key] = row[index]; }
    }
    const fullKey = !fromSQL && schema?.primaryKey.length && Object.keys(keys).length === schema.primaryKey.length;
    setDetail({ columns: [...columns], row: [...row], title: fromSQL ? "Registro del resultado SQL" : `${selectedDb}.${selectedTable}`,
      schema: fromSQL ? undefined : schema || undefined,
      endpoint: fullKey ? `/api/connections/${connection.id}/databases/${selectedDb}/tables/${selectedTable}/row` : undefined,
      pk: keys,
    });
  }
  // Handle SQL Execution
  async function runQuery() {
    if (!sqlQuery.trim() || sqlLoading) return;
    setSqlLoading(true);
    setSqlError("");
    const submittedQuery = sqlQuery;
    try {
      const resp = await confirmedApi<QueryResponse>("POST", `/api/connections/${connection.id}/query`, {
        query: submittedQuery,
        database: selectedDb,
      });
      if (!resp) return;

      if (!resp.ok) {
        setSqlError(resp.error || "Error ejecutando la consulta");
        setSqlResult(null);
        return;
      }

      setSqlResult(resp.result || null);
      if (resp.result) {
        setLastExecutedSql(resp.result.executedSql || sqlQuery);
        setLastExecutionMs(resp.result.executionMs);
      }
      setQueryHistory((prev) => [submittedQuery, ...prev.filter((q) => q !== submittedQuery)].slice(0, 20));
      void loadTables();
      void loadDatabases();
    } catch (e) {
      setSqlError(e instanceof ApiError ? e.message : "Error de red o del servidor");
      setSqlResult(null);
    } finally {
      setSqlLoading(false);
    }
  }

  // Create Database Handler
  async function handleCreateDb(e: React.FormEvent) {
    e.preventDefault();
    if (!newDbName.trim()) return;
    try {
      const r = await api<{ ok: boolean; executedSql: string }>(
        "POST",
        `/api/connections/${connection.id}/databases/create`,
        { name: newDbName.trim() }
      );
      if (r.executedSql) setLastExecutedSql(r.executedSql);
      setCreatingDb(false);
      setNewDbName("");
      void loadDatabases();
      setSelectedDb(newDbName.trim());
      setActiveTab("db-overview");
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "Error creando la base de datos");
    }
  }

  // Drop Database Handler
  async function handleDropDb(dbToDrop: string) {
    try {
      const r = await confirmedApi<{ ok: boolean; executedSql: string }>(
        "POST",
        `/api/connections/${connection.id}/databases/drop`,
        { name: dbToDrop }
      );
      if (!r) return;
      if (r.executedSql) setLastExecutedSql(r.executedSql);
      if (selectedDb === dbToDrop) {
        setSelectedDb("");
        setSelectedTable(null);
        setActiveTab("server-dbs");
      }
      void loadDatabases();
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "Error eliminando la base de datos");
    }
  }

  // Delete Row
  async function handleDeleteRow(row: any[]) {
    if (!schema || schema.primaryKey.length === 0) {
      alert("No se puede eliminar la fila: la tabla no tiene Clave Primaria definida.");
      return;
    }

    const pkMap: Record<string, any> = {};
    for (const pkCol of schema.primaryKey) {
      const idx = dataResult?.columns.indexOf(pkCol) ?? -1;
      if (idx !== -1) pkMap[pkCol] = row[idx];
    }

    try {
      const r = await confirmedApi<{ ok: boolean; executedSql: string }>(
        "DELETE",
        `/api/connections/${connection.id}/databases/${selectedDb}/tables/${selectedTable}/rows`,
        { pk: pkMap }
      );
      if (!r) return;
      if (r.executedSql) setLastExecutedSql(r.executedSql);
      refreshTableData();
    } catch (e) {
      alert(e instanceof ApiError ? e.message : "Error eliminando el registro");
    }
  }

  // Batch Delete Rows
  async function handleBatchDelete() {
    if (!schema || schema.primaryKey.length === 0 || selectedRows.length === 0) return;

    const pks: Record<string, any>[] = [];
    selectedRows.forEach((rIdx) => {
      const row = dataResult?.rows[rIdx];
      if (!row) return;
      const pkMap: Record<string, any> = {};
      for (const pkCol of schema.primaryKey) {
        const cIdx = dataResult?.columns.indexOf(pkCol) ?? -1;
        if (cIdx !== -1) pkMap[pkCol] = row[cIdx];
      }
      pks.push(pkMap);
    });

    try {
      const r = await confirmedApi<{ ok: boolean; affectedRows: number; executedSql: string }>(
        "POST",
        `/api/connections/${connection.id}/databases/${selectedDb}/tables/${selectedTable}/batch-delete`,
        { pks }
      );
      if (!r) return;
      if (r.executedSql) setLastExecutedSql(r.executedSql);
      setSelectedRows([]);
      refreshTableData();
    } catch (e) {
      alert(e instanceof ApiError ? e.message : "Error eliminando los registros seleccionados");
    }
  }

  // Truncate Table
  async function handleTruncateTable(tableToTruncate = selectedTable) {
    if (!tableToTruncate) return;
    try {
      const r = await confirmedApi<{ ok: boolean; executedSql: string }>(
        "POST",
        `/api/connections/${connection.id}/databases/${selectedDb}/tables/${tableToTruncate}/truncate`
      );
      if (!r) return;
      if (r.executedSql) setLastExecutedSql(r.executedSql);
      if (selectedTable === tableToTruncate) refreshTableData();
      void loadTables();
    } catch (e) {
      alert(e instanceof ApiError ? e.message : "Error vaciando la tabla");
    }
  }

  // Drop Table
  async function handleDropTable(tableToDrop: string) {
    try {
      const r = await confirmedApi<{ ok: boolean; executedSql: string }>(
        "POST",
        `/api/connections/${connection.id}/databases/${selectedDb}/tables/${tableToDrop}/drop`
      );
      if (!r) return;
      if (r.executedSql) setLastExecutedSql(r.executedSql);
      if (selectedTable === tableToDrop) {
        setSelectedTable(null);
        setActiveTab("db-overview");
      }
      void loadTables();
    } catch (e) {
      alert(e instanceof ApiError ? e.message : "Error eliminando la tabla");
    }
  }

  // Start Edit Row
  function startEditRow(row: any[]) {
    if (!schema) return;
    const values: Record<string, any> = {};
    const pk: Record<string, any> = {};

    schema.columns.forEach((col) => {
      const idx = dataResult?.columns.indexOf(col.field) ?? -1;
      const val = idx !== -1 ? row[idx] : null;
      values[col.field] = val;
      if (schema.primaryKey.includes(col.field)) {
        pk[col.field] = val;
      }
    });

    setEditingRow({ mode: "edit", values, pk });
  }

  // Start Clone Row
  function startCloneRow(row: any[]) {
    if (!schema) return;
    const values: Record<string, any> = {};
    schema.columns.forEach((col) => {
      const idx = dataResult?.columns.indexOf(col.field) ?? -1;
      const val = idx !== -1 ? row[idx] : null;
      if (schema.primaryKey.includes(col.field) && col.extra.includes("auto_increment")) {
        values[col.field] = null;
      } else {
        values[col.field] = val;
      }
    });
    setEditingRow({ mode: "insert", values, pk: {} });
  }

  // Start Insert Row
  function startInsertRow() {
    if(readOnly)return;
    if(onOpenView && activeTab!=="insert" && selectedTable){onOpenView(selectedDb,selectedTable,"insert");return;}
    if (!schema) return;
    const values: Record<string, any> = {};
    schema.columns.forEach((col) => {
      values[col.field] = col.default !== null ? col.default : (col.null==="YES"||col.extra.includes("auto_increment") ? null : "");
    });
    setEditingRow({ mode: "insert", values, pk: {} });
  }

  useEffect(()=>{if(activeTab==="insert"&&schema&&!editingRow)startInsertRow();},[activeTab,schema,readOnly]);

  // Save Row
  async function saveRow(e: React.FormEvent) {
    e.preventDefault();
    if (!editingRow || !selectedTable || readOnly) return;
    const values=Object.fromEntries(Object.entries(editingRow.values).filter(([field])=>!schema?.columns.find(c=>c.field===field)?.extra.toLowerCase().includes("generated")));
    try {
      if (editingRow.mode === "insert") {
        const r = await api<{ ok: boolean; executedSql: string }>(
          "POST",
          `/api/connections/${connection.id}/databases/${selectedDb}/tables/${selectedTable}/rows`,
          { values }
        );
        if (r.executedSql) setLastExecutedSql(r.executedSql);
      } else {
        const r = await api<{ ok: boolean; executedSql: string }>(
          "PUT",
          `/api/connections/${connection.id}/databases/${selectedDb}/tables/${selectedTable}/rows`,
          { pk: editingRow.pk, values }
        );
        if (r.executedSql) setLastExecutedSql(r.executedSql);
      }
      setEditingRow(null);setRowDirty(false);setRowNotice("Registro guardado correctamente.");
      refreshTableData();
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "Error guardando los cambios");
    }
  }

  // Create Table
  async function handleCreateTableSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!newTableName.trim()) return alert("El nombre de la tabla es obligatorio");
    try {
      const r = await api<{ ok: boolean; executedSql: string }>(
        "POST",
        `/api/connections/${connection.id}/databases/${selectedDb}/tables/create`,
        { name: newTableName.trim(), columns: newTableCols }
      );
      if (r.executedSql) setLastExecutedSql(r.executedSql);
      setCreatingTable(false);
      void loadTables();
      openTable(newTableName.trim());
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "Error creando la tabla");
    }
  }

  // Filtered tables for sidebar and overview (accent and case insensitive)
  const visibleTables = tables.filter((t) =>
    cleanStr(t.name).includes(cleanStr(tableFilter))
  );

  // Totals for Database Overview
  const totalRowsCount = tables.reduce((acc, t) => acc + (t.rows || 0), 0);
  const totalDataBytes = tables.reduce((acc, t) => acc + (t.dataLength || 0), 0);
  const totalIndexBytes = tables.reduce((acc, t) => acc + (t.indexLength || 0), 0);

  // Available Columns for active table & Popover handler
  const allAvailableCols = schema?.columns.map((c) => c.field) || dataResult?.columns || [];

  const toggleColPicker = () => {
    setShowColPicker((prev) => !prev);
  };

  return (
    <div className="workspace">
      {/* Point 3: Unified Single-Row App & Navigation Topbar */}
      <header className="ws-topbar unified-app-bar">
        <button className="brand brand-home" onClick={onBack} title="Ir a Conexiones">
          <Brand />
        </button>

        <button className="btn-back" onClick={onBack} title="Volver al listado de conexiones">
          ← Conexiones
        </button>

        {/* Breadcrumb Navigation */}
        <nav className="breadcrumb">
          <span className={`badge env-${connection.env}`}>{connection.env}</span>
          <span
            className={`bc-item ${!selectedDb ? "bc-active" : ""}`}
            onClick={() => {
              if(viewNavigation && onOpenView){onOpenView("","","browse");return;}
              setSelectedDb("");
              setSelectedTable(null);
              setActiveTab("server-dbs");
            }}
            title="Ver lista de bases de datos del servidor"
          >
            <strong>{connection.name}</strong>
          </span>

          {selectedDb && (
            <>
              <span className="bc-sep">»</span>
              <span
                className={`bc-item ${!selectedTable ? "bc-active" : ""}`}
                onClick={() => {
                  if(viewNavigation && onOpenView){onOpenView(selectedDb,"","browse");return;}
                  setSelectedTable(null);
                  setActiveTab("db-overview");
                }}
                title="Ver lista de tablas de la base de datos"
              >
                {selectedDb}
              </span>
            </>
          )}

          {selectedTable && (
            <>
              <span className="bc-sep">»</span>
              <span className="bc-item bc-active">
                <strong>{selectedTable}</strong>
              </span>
            </>
          )}
        </nav>

        {connection.ssh?.enabled && <span className="connection-route-badge" title={`SSH · ${connection.ssh.host}`}>↗ SSH</span>}
        {readOnly && <span className="badge badge-ro">🔒 Solo Lectura</span>}

        <div className="db-selector">
          <label htmlFor={dbSelectId}>BD:</label>
          <select
            id={dbSelectId}
            value={selectedDb}
            onChange={(e) => {
              const newDb = e.target.value;
              if(viewNavigation && onOpenView){onOpenView(newDb,"","browse");return;}
              setSelectedDb(newDb);
              setSelectedTable(null);
              if (newDb) {
                setActiveTab("db-overview");
              } else {
                setActiveTab("server-dbs");
              }
              setPage(1);
            }}
          >
            <option value="">(Seleccionar BD)</option>
            {databases.map((db) => (
              <option key={db} value={db}>
                {db}
              </option>
            ))}
          </select>
        </div>

        <div className="spacer" />

        <div className="user-section">
          <span className="muted">{username}</span>
          <button onClick={onLogout}>Salir</button>
        </div>
      </header>

      {/* Main Workspace Body */}
      <div className="ws-body">
        {/* Point 4: Left Sidebar with dedicated scroll */}
        <aside className="ws-sidebar">
          <div className="sidebar-actions">
            <button
              className={`action-btn ${activeTab === "sql" ? "active" : ""}`}
              onClick={() => {
                if (onNewSQL) { onNewSQL(selectedDb,selectedTable ? `SELECT * FROM ` + "`" + `${selectedTable}` + "`" + ` LIMIT 50;` : undefined); return; }
                setActiveTab("sql");
                if (selectedTable) {
                  setSqlQuery(`SELECT * FROM \`${selectedTable}\` LIMIT 50;`);
                }
              }}
              title="Ejecutar comandos SQL"
            >
              ⚡ SQL
            </button>
            <button
              className={`action-btn ${activeTab === "export" ? "active" : ""}`}
              onClick={() => onOpenView ? onOpenView(selectedDb,selectedTable||"","export") : setActiveTab("export")}
              title="Exportar base de datos"
            >
              📦 Exportar
            </button>
          </div>

          <div className="sidebar-search">
            <input
              type="search"
              placeholder="Filtrar tablas…"
              value={tableFilter}
              onChange={(e) => setTableFilter(e.target.value)}
            />
          </div>

          {/* Table list with its own dedicated scroll (Point 4) */}
          <div className="table-list">
            <div className="table-list-header">
              <span>Tablas ({visibleTables.length})</span>
              {selectedDb && !readOnly && (
                <button
                  className="btn-add-table"
                  onClick={() => setCreatingTable(true)}
                  title="Crear nueva tabla"
                >
                  + Nueva
                </button>
              )}
            </div>
            {!selectedDb ? (
              <p className="sidebar-empty">Selecciona una base de datos para ver sus tablas</p>
            ) : visibleTables.length === 0 ? (
              <p className="sidebar-empty">No hay tablas</p>
            ) : (
              visibleTables.map((t) => (
                <button
                  key={t.name}
                  className={`table-item ${selectedTable === t.name ? "selected" : ""}`}
                  onClick={() => openTable(t.name)}
                >
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

        {/* Content Area */}
        <main className="ws-content">
          {/* Point 3: SQL Banner inside Content Area only (not stretching across sidebar) */}
          {preferences.showQueries && lastExecutedSql && (
            <div className="sql-banner">
              <div className="sql-banner-text">
                <code>{lastExecutedSql}</code>
                {lastExecutionMs !== null && (
                  <span className="sql-time">({(lastExecutionMs / 1000).toFixed(3)} s)</span>
                )}
              </div>
              <div className="sql-banner-actions">
                <button
                  className="btn-tiny"
                  onClick={() => navigator.clipboard.writeText(lastExecutedSql)}
                  title="Copiar SQL"
                >
                  Copiar
                </button>
                <button
                  className="btn-tiny"
                  onClick={() => {
                    if(onNewSQL){onNewSQL(selectedDb,lastExecutedSql);return;}
                    setSqlQuery(lastExecutedSql);
                    setActiveTab("sql");
                  }}
                  title="Editar en consola SQL"
                >
                  Editar en SQL
                </button>
              </div>
            </div>
          )}

          {viewNavigation && <div className="table-view-toolbar">{viewNavigation}{!readOnly && selectedTable && <div className="table-danger-actions"><button className="adminer-tab-link link-danger" onClick={()=>void handleTruncateTable(selectedTable)}>Vaciar</button><button className="adminer-tab-link link-danger" onClick={()=>void handleDropTable(selectedTable)}>Eliminar</button></div>}</div>}

          {error && <div className="alert alert-error">{error}</div>}

          {/* VIEW: SERVER DATABASES LIST (POINT 2: SELECCIONAR BASE DE DATOS ESTILO ADMINER) */}
          {(!selectedDb || activeTab === "server-dbs") && activeTab !== "sql" && activeTab !== "export" && (
            <div className="server-dbs-view">
              <div className="unified-top-bar">
                <div className="bar-left">
                  <h2>Seleccionar Base de datos</h2>
                  <div className="btn-group">
                                      {!readOnly && (
                      <button className="primary" onClick={() => setCreatingDb(true)}>
                        + Crear Base de datos
                      </button>
                    )}
                    <button onClick={() => void loadDatabases()}>🔄 Actualizar</button>
                  </div>
                </div>

                <div className="bar-right">
                  <span className="info-pill">{databaseDetails.length || databases.length} bases de datos</span>
                  <span className="info-pill">
                    {connection.username}@{connection.host}:{connection.port}
                  </span>
                </div>
              </div>

              <div className="grid-container">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th style={{ width: "2.5rem", textAlign: "center" }}>
                        <input
                          type="checkbox"
                          checked={selectedDbs.length === databases.length && databases.length > 0}
                          onChange={(e) => {
                            if (e.target.checked) {
                              setSelectedDbs([...databases]);
                            } else {
                              setSelectedDbs([]);
                            }
                          }}
                        />
                      </th>
                      <th>Base de datos</th>
                      <th>Acciones</th>
                      <th>Colación</th>
                      <th style={{ textAlign: "right" }}>Tablas</th>
                      <th style={{ textAlign: "right" }}>Tamaño Total</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(databaseDetails.length > 0 ? databaseDetails : databases.map((d) => ({ name: d, collation: "—", tables: 0, size: 0 }))).map((d) => {
                      const isSelected = selectedDbs.includes(d.name);
                      return (
                        <tr key={d.name} className={isSelected ? "row-selected" : ""}>
                          <td style={{ textAlign: "center" }}>
                            <input
                              type="checkbox"
                              checked={isSelected}
                              onChange={(e) => {
                                if (e.target.checked) {
                                  setSelectedDbs([...selectedDbs, d.name]);
                                } else {
                                  setSelectedDbs(selectedDbs.filter((name) => name !== d.name));
                                }
                              }}
                            />
                          </td>
                          <td>
                            <a
                              href="#enter"
                              className="table-link"
                              onClick={(e) => {
                                e.preventDefault();
                                setSelectedDb(d.name);
                                setSelectedTable(null);
                                setActiveTab("db-overview");
                              }}
                            >
                              <strong>{d.name}</strong>
                            </a>
                          </td>
                          <td>
                            <div className="row-action-links">
                              <a
                                href="#open"
                                onClick={(e) => {
                                  e.preventDefault();
                                  setSelectedDb(d.name);
                                  setSelectedTable(null);
                                  setActiveTab("db-overview");
                                }}
                              >
                                Abrir
                              </a>
                              {!readOnly && !["information_schema", "performance_schema", "mysql", "sys"].includes(d.name.toLowerCase()) && (
                                <>
                                  <span>|</span>
                                  <a
                                    href="#drop"
                                    className="link-danger"
                                    onClick={(e) => {
                                      e.preventDefault();
                                      void handleDropDb(d.name);
                                    }}
                                  >
                                    Eliminar
                                  </a>
                                </>
                              )}
                            </div>
                          </td>
                          <td className="mono">{d.collation || "—"}</td>
                          <td style={{ textAlign: "right" }} className="mono">
                            {d.tables > 0 ? d.tables.toLocaleString() : "—"}
                          </td>
                          <td style={{ textAlign: "right" }} className="mono">
                            {d.size > 0 ? formatBytes(d.size) : "—"}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {/* VIEW: DATABASE OVERVIEW (LISTA DE TABLAS ESTILO ADMINER) */}
          {selectedDb && !selectedTable && activeTab === "db-overview" && (
            <div className="db-overview">
              <div className="unified-top-bar">
                <div className="bar-left">
                  <h2>Base de datos: <strong>{selectedDb}</strong></h2>
                  <div className="btn-group">
                    {!readOnly && (
                      <button className="primary" onClick={() => setCreatingTable(true)}>
                        + Crear nueva tabla
                      </button>
                    )}
                    <button onClick={() => void loadTables()}>🔄 Actualizar</button>
                    <button onClick={() => onOpenView ? onOpenView(selectedDb,selectedTable||"","export") : setActiveTab("export")}>📦 Exportar BD</button>
                  </div>
                </div>

                <div className="bar-right">
                  <span className="info-pill">{tables.length} tablas</span>
                  <span className="info-pill">{totalRowsCount.toLocaleString()} filas</span>
                  <span className="info-pill">{formatBytes(totalDataBytes + totalIndexBytes)}</span>
                </div>
              </div>

              <div className="grid-container">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th style={{ width: "2.5rem", textAlign: "center" }}>
                        <input
                          type="checkbox"
                          checked={selectedTables.length === visibleTables.length && visibleTables.length > 0}
                          onChange={(e) => {
                            if (e.target.checked) {
                              setSelectedTables(visibleTables.map((t) => t.name));
                            } else {
                              setSelectedTables([]);
                            }
                          }}
                        />
                      </th>
                      <th>Tabla</th>
                      <th>Acciones</th>
                      <th>Tipo</th>
                      <th>Motor</th>
                      <th>Colación</th>
                      <th style={{ textAlign: "right" }}>Filas</th>
                      <th style={{ textAlign: "right" }}>Tamaño Datos</th>
                      <th style={{ textAlign: "right" }}>Tamaño Índices</th>
                      <th>Comentario</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visibleTables.length === 0 ? (
                      <tr>
                        <td colSpan={10} style={{ textAlign: "center", padding: "2rem" }}>
                          No se encontraron tablas en esta base de datos.
                        </td>
                      </tr>
                    ) : (
                      visibleTables.map((t) => {
                        const isSelected = selectedTables.includes(t.name);
                        return (
                          <tr key={t.name} className={isSelected ? "row-selected" : ""}>
                            <td style={{ textAlign: "center" }}>
                              <input
                                type="checkbox"
                                checked={isSelected}
                                onChange={(e) => {
                                  if (e.target.checked) {
                                    setSelectedTables([...selectedTables, t.name]);
                                  } else {
                                    setSelectedTables(selectedTables.filter((name) => name !== t.name));
                                  }
                                }}
                              />
                            </td>
                            <td>
                              <a
                                href="#view"
                                className="table-link"
                                onClick={(e) => {
                                  e.preventDefault();
                                  openTable(t.name);
                                }}
                              >
                                <strong>{t.name}</strong>
                              </a>
                            </td>
                            <td>
                              <div className="row-action-links">
                                <a
                                  href="#data"
                                  onClick={(e) => {
                                    e.preventDefault();
                                    openTable(t.name);
                                  }}
                                >
                                  Ver
                                </a>
                                <span>|</span>
                                <a
                                  href="#structure"
                                  onClick={(e) => {
                                    e.preventDefault();
                                    if(onOpenView){onOpenView(selectedDb,t.name,"structure");return;}
                                    setSelectedTable(t.name);
                                    setActiveTab("structure");
                                  }}
                                >
                                  Estructura
                                </a>
                                {!readOnly && (
                                  <>
                                    <span>|</span>
                                    <a
                                      href="#truncate"
                                      className="link-danger"
                                      onClick={(e) => {
                                        e.preventDefault();
                                        void handleTruncateTable(t.name);
                                      }}
                                    >
                                      Vaciar
                                    </a>
                                    <span>|</span>
                                    <a
                                      href="#drop"
                                      className="link-danger"
                                      onClick={(e) => {
                                        e.preventDefault();
                                        void handleDropTable(t.name);
                                      }}
                                    >
                                      Eliminar
                                    </a>
                                  </>
                                )}
                              </div>
                            </td>
                            <td>{t.type === "VIEW" ? "Vista" : "Tabla"}</td>
                            <td className="mono">{t.engine || "—"}</td>
                            <td className="mono">{t.collation || "—"}</td>
                            <td style={{ textAlign: "right" }} className="mono">
                              {t.rows.toLocaleString()}
                            </td>
                            <td style={{ textAlign: "right" }} className="mono">
                              {formatBytes(t.dataLength)}
                            </td>
                            <td style={{ textAlign: "right" }} className="mono">
                              {formatBytes(t.indexLength)}
                            </td>
                            <td>{t.comment || "—"}</td>
                          </tr>
                        );
                      })
                    )}
                  </tbody>
                  {visibleTables.length > 0 && (
                    <tfoot>
                      <tr className="tfoot-totals">
                        <td />
                        <td>
                          <strong>{visibleTables.length} tablas</strong>
                        </td>
                        <td colSpan={4} />
                        <td style={{ textAlign: "right" }} className="mono">
                          <strong>{totalRowsCount.toLocaleString()}</strong>
                        </td>
                        <td style={{ textAlign: "right" }} className="mono">
                          <strong>{formatBytes(totalDataBytes)}</strong>
                        </td>
                        <td style={{ textAlign: "right" }} className="mono">
                          <strong>{formatBytes(totalIndexBytes)}</strong>
                        </td>
                        <td />
                      </tr>
                    </tfoot>
                  )}
                </table>
              </div>

              {/* Batch Actions for Tables */}
              {selectedTables.length > 0 && !readOnly && (
                <div className="batch-toolbar">
                  <span>Con las <strong>{selectedTables.length}</strong> tablas seleccionadas:</span>
                  <button
                    className="danger"
                    onClick={async () => {
                      try {
                        for (const tbl of selectedTables) {
                          const result = await confirmedApi("POST", `/api/connections/${connection.id}/databases/${selectedDb}/tables/${tbl}/drop`);
                          if (!result) break;
                        }
                      } catch (e) { setError(e instanceof Error ? e.message : "Error eliminando tablas"); }
                      finally { setSelectedTables([]); void loadTables(); }
                    }}
                  >
                    Eliminar (DROP)
                  </button>
                  <button
                    onClick={async () => {
                      try {
                        for (const tbl of selectedTables) {
                          const result = await confirmedApi("POST", `/api/connections/${connection.id}/databases/${selectedDb}/tables/${tbl}/truncate`);
                          if (!result) break;
                        }
                      } catch (e) { setError(e instanceof Error ? e.message : "Error vaciando tablas"); }
                      finally { setSelectedTables([]); void loadTables(); }
                    }}
                  >
                    Vaciar (TRUNCATE)
                  </button>
                </div>
              )}
            </div>
          )}

          {/* VIEW: TABLE VIEW (REGISTROS / ESTRUCTURA / MODIFICAR) */}
          {selectedTable && (activeTab === "data" || activeTab === "structure" || activeTab === "modify" || activeTab === "insert") && (
            <div className="table-view">
              {!viewNavigation && <>
              <div className="table-view-toolbar">
                <TabStrip label="Vistas de la tabla" className="table-tab-strip" active={activeTab} items={([['data','Contenido'],['structure','Estructura'],...(!readOnly?[['modify','Modificar'],['insert','Nuevo registro']]:[])] as [WorkspaceView,string][]).map(([view,label])=>({id:view,label,onSelect:()=>openView(view)}))}/>
                <span className="spacer"/>{!readOnly&&<div className="table-danger-actions"><button className="adminer-tab-link link-danger" onClick={()=>void handleTruncateTable(selectedTable)}>Vaciar</button><button className="adminer-tab-link link-danger" onClick={()=>void handleDropTable(selectedTable)}>Eliminar</button></div>}
              </div>
              </>}
              {readOnly&&(activeTab==="modify"||activeTab==="insert")&&<div className="alert">Solo lectura activo. Desactiva «Sólo ver» para editar si tu perfil lo permite.</div>}
              {activeTab==="insert"&&<div className="insert-record-view">{rowNotice&&<p className="okmsg" role="status">{rowNotice}</p>}{editingRow&&schema?<RowEditor schema={schema} mode="insert" values={editingRow.values} onChange={values=>setEditingRow({...editingRow,values})} onSubmit={saveRow} onCancel={()=>onCloseTab?.()} onDirtyChange={setRowDirty} disabled={readOnly}/>:<p className="muted">{readOnly?"La inserción está bloqueada.":"Preparando campos…"}</p>}</div>}

              {/* Point 2: Adminer-style Fieldset Toolbar (Fixed size inputs, adaptive single row) */}
              {activeTab === "data" && (
                <form onSubmit={handleFilterSubmit} className="adminer-filter-bar">
                  {/* Fieldset: Mostrar (Columns selection) */}
                  <fieldset className="filter-fieldset">
                    <legend>Mostrar</legend>
                    <div className="fieldset-content">
                      <button
                        type="button"
                        className="btn-select-cols"
                        onClick={toggleColPicker}
                        title="Seleccionar columnas a mostrar"
                      >
                        {allAvailableCols.length > 0 && selectedColumns.length === allAvailableCols.length
                          ? "Todas (*)"
                          : selectedColumns.length === 0
                          ? "Ninguna"
                          : `${selectedColumns.length} cols`} ▾
                      </button>

                      {showColPicker && (
                        <>
                          <div className="col-picker-backdrop" onClick={() => setShowColPicker(false)} />
                          <div
                            className="col-picker-dropdown"
                          >
                            <header>
                              <label className="checkbox-mini" style={{ cursor: "pointer", fontWeight: 600 }}>
                                <input
                                  type="checkbox"
                                  checked={allAvailableCols.length > 0 && selectedColumns.length === allAvailableCols.length}
                                  onChange={(e) => {
                                    if (e.target.checked) {
                                      setSelectedColumns([...allAvailableCols]);
                                    } else {
                                      setSelectedColumns([]);
                                    }
                                  }}
                                />
                                Todas (*)
                              </label>
                              <span className="muted" style={{ fontSize: "0.75rem" }}>
                                {selectedColumns.length}/{allAvailableCols.length}
                              </span>
                              <button
                                type="button"
                                className="btn-tiny"
                                onClick={() => setShowColPicker(false)}
                              >
                                ✕
                              </button>
                            </header>
                            <div className="col-picker-list">
                              {allAvailableCols.map((col) => {
                                const isChecked = selectedColumns.includes(col);
                                return (
                                  <label key={col} className="col-picker-item">
                                    <input
                                      type="checkbox"
                                      checked={isChecked}
                                      onChange={(e) => {
                                        if (e.target.checked) {
                                          setSelectedColumns([...selectedColumns, col]);
                                        } else {
                                          setSelectedColumns(selectedColumns.filter((c) => c !== col));
                                        }
                                      }}
                                    />
                                    <span className="mono">{col}</span>
                                  </label>
                                );
                              })}
                            </div>
                          </div>
                        </>
                      )}
                    </div>
                  </fieldset>

                  {/* Fieldset: Condición */}
                  <fieldset className="filter-fieldset">
                    <legend>Condición</legend>
                    <div className="fieldset-content">
                      <select
                        className="filter-col-select"
                        value={filterCol}
                        onChange={(e) => setFilterCol(e.target.value)}
                        title="Columna (o donde sea)"
                      >
                        <option value="">(donde sea)</option>
                        {allAvailableCols.map((c) => (
                          <option key={c} value={c}>
                            {c}
                          </option>
                        ))}
                      </select>

                      <select
                        className="filter-op-select"
                        value={filterOp}
                        onChange={(e) => setFilterOp(e.target.value)}
                        title="Operador de búsqueda"
                      >
                        <option value="=">=</option>
                        <option value="<">&lt;</option>
                        <option value=">">&gt;</option>
                        <option value="<=">&lt;=</option>
                        <option value=">=">&gt;=</option>
                        <option value="!=">!=</option>
                        <option value="LIKE">LIKE</option>
                        <option value="LIKE %%">LIKE %%</option>
                        <option value="STARTS">STARTS</option>
                        <option value="REGEXP">REGEXP</option>
                        <option value="IN">IN</option>
                        <option value="IS_NULL">IS NULL</option>
                        <option value="NOT_LIKE">NOT LIKE</option>
                        <option value="IS_NOT_NULL">IS NOT NULL</option>
                      </select>

                      {filterOp !== "IS_NULL" && filterOp !== "IS_NOT_NULL" && (
                        <input
                          type="text"
                          className="filter-val-input"
                          placeholder="Valor…"
                          value={filterVal}
                          onChange={(e) => setFilterVal(e.target.value)}
                        />
                      )}
                    </div>
                  </fieldset>

                  {/* Fieldset: Ordenar */}
                  <fieldset className="filter-fieldset">
                    <legend>Ordenar</legend>
                    <div className="fieldset-content">
                      <select
                        className="filter-sort-select"
                        value={sortCol}
                        onChange={(e) => setSortCol(e.target.value)}
                        title="Columna de ordenación"
                      >
                        <option value="">(clave primaria, si existe)</option>
                        {allAvailableCols.map((c) => (
                          <option key={c} value={c}>
                            {c}
                          </option>
                        ))}
                      </select>

                      <label className="checkbox-mini filter-desc-label">
                        <input
                          type="checkbox"
                          checked={sortDesc}
                          onChange={(e) => {
                            setSortDesc(e.target.checked);
                            }}
                        />
                        desc
                      </label>
                    </div>
                  </fieldset>

                  {/* Fieldset: Límite */}
                  <fieldset className="filter-fieldset">
                    <legend>Límite</legend>
                    <div className="fieldset-content">
                      <input
                        type="number"
                        className="filter-num-input"
                        min="1"
                        max="500"
                        value={limit}
                        onChange={(e) => setLimit(Number(e.target.value) || 50)}
                      />
                    </div>
                  </fieldset>

                  {/* Fieldset: Longitud de texto */}
                  <fieldset className="filter-fieldset">
                    <legend>Longitud</legend>
                    <div className="fieldset-content">
                      <input
                        type="number"
                        className="filter-num-input"
                        min="10"
                        max="5000"
                        value={textLength}
                        onChange={(e) => setTextLength(Number(e.target.value) || 100)}
                      />
                    </div>
                  </fieldset>

                  {/* Action buttons */}
                  <div className="filter-actions">
                    {JSON.stringify(draftSettings()) !== JSON.stringify(applied) && <span className="muted filter-pending">Cambios sin aplicar</span>}
                    <button type="submit" className="primary filter-btn-submit">
                      Mostrar
                    </button>
                    {(activeFilterText || filterVal || sortCol || selectedColumns.length > 0) && (
                      <button type="button" onClick={clearFilter} className="filter-btn-clear" title="Limpiar todos los filtros">
                        Limpiar
                      </button>
                    )}
                  </div>
                </form>
              )}

              {/* DATA VIEW GRID */}
              {activeTab === "data" && (
                <div className="data-view">
                  <ResultLimitNotice result={dataResult} />
                  {selectedRows.length > 0 && !readOnly && (
                    <div className="batch-toolbar">
                      <span>Seleccionados: <strong>{selectedRows.length}</strong> registros</span>
                      <button className="danger" onClick={handleBatchDelete} disabled={!schema?.primaryKey.length || schema.primaryKey.some((key) => !dataResult?.columns.includes(key))}>
                        Eliminar seleccionados
                      </button>
                    </div>
                  )}

                  <div className="data-grid-shell" aria-busy={loading}>
                  <div className="grid-container">
                    <table className="data-table">
                      <thead>
                        <tr>
                          <th style={{ width: "2rem", textAlign: "center" }}>
                            <input
                              type="checkbox"
                              checked={Boolean(
                                dataResult &&
                                dataResult.rows.length > 0 &&
                                selectedRows.length === dataResult.rows.length
                              )}
                              onChange={(e) => {
                                if (e.target.checked && dataResult) {
                                  setSelectedRows(dataResult.rows.map((_, idx) => idx));
                                } else {
                                  setSelectedRows([]);
                                }
                              }}
                            />
                          </th>
                          <th className="th-actions">Acciones</th>
                          {(dataResult?.columns || schema?.columns.map((c) => c.field) || []).map((col) => (
                            <th
                              key={col}
                              onClick={() => handleHeaderSort(col)}
                              className={applied.sortCol === col ? `sorted ${applied.sortDesc ? "desc" : "asc"}` : ""}
                            >
                              {col} {applied.sortCol === col ? (applied.sortDesc ? "▼" : "▲") : ""}
                            </th>
                          ))}
                        </tr>
                      </thead>
                      <tbody>
                        {loading ? null : !dataResult || dataResult.rows.length === 0 ? (
                          <tr>
                            <td
                              colSpan={(dataResult?.columns.length || schema?.columns.length || 2) + 2}
                              style={{ textAlign: "center", padding: "3rem 1rem" }}
                            >
                              {!dataResult ? <p>No se pudieron cargar los registros.</p> : dataResult.truncated ? <p>La fila supera el límite de lectura. Selecciona menos columnas para consultarla.</p> : activeFilterText || applied.filterOp === "IS_NULL" || applied.filterOp === "IS_NOT_NULL" ? (
                                <div className="empty-search-box">
                                  <p>
                                    🔍 No se encontraron registros con los filtros aplicados{activeFilterText && <>: <strong>"{activeFilterText}"</strong></>}.
                                  </p>
                                  <button onClick={clearFilter}>Limpiar búsqueda</button>
                                </div>
                              ) : (
                                <div className="empty-search-box">
                                  <p>{offset ? "No hay registros en esta página." : "Esta tabla está vacía (0 registros)."}</p>
                                  {!readOnly && (
                                    <button className="primary" onClick={startInsertRow}>
                                      + Insertar primer registro
                                    </button>
                                  )}
                                </div>
                              )}
                            </td>
                          </tr>
                        ) : (
                          dataResult.rows.map((row, rIdx) => {
                            const isChecked = selectedRows.includes(rIdx);
                            return (
                              <tr key={rIdx} className={isChecked ? "row-selected" : ""}>
                                <td style={{ textAlign: "center" }}>
                                  <input
                                    type="checkbox"
                                    checked={isChecked}
                                    onChange={(e) => {
                                      if (e.target.checked) {
                                        setSelectedRows([...selectedRows, rIdx]);
                                      } else {
                                        setSelectedRows(selectedRows.filter((i) => i !== rIdx));
                                      }
                                    }}
                                  />
                                  {preferences.rowNumbers&&<span className="row-position">{offset+rIdx+1}</span>}
                                </td>
                                <td className="td-actions"><div className="row-actions">
                                  <button className="btn-action-view" onClick={()=>viewRecord(row)} title="Ver detalle" aria-label={`Ver detalle del registro ${rIdx+1}`}>⌕</button>
                                  {!readOnly&&<button className="btn-action-edit" disabled={!schema?.primaryKey.length||schema?.columns.some(c=>!dataResult.columns.includes(c.field))} onClick={()=>startEditRow(row)} title="Editar registro" aria-label={`Editar registro ${rIdx+1}`}>✎</button>}
                                  <ActionMenu label={`Más acciones de fila ${rIdx+1}`} actions={[
                                    {label:"Copiar fila como JSON",onClick:()=>void copy(rowJSON(dataResult.columns,row),"Fila copiada como JSON")},
                                    ...(!readOnly?[{label:"Duplicar registro",disabled:schema?.columns.some(c=>!dataResult.columns.includes(c.field)),onClick:()=>startCloneRow(row)}, {label:"Eliminar registro",danger:true,disabled:!schema?.primaryKey.length||schema.primaryKey.some(k=>!dataResult.columns.includes(k)),onClick:()=>void handleDeleteRow(row)}]:[])
                                  ]}/>
                                </div></td>
                                {row.map((val, cIdx) => {
                                  const cellKey = `${rIdx}-${cIdx}`;
                                  const isExpanded = expandedCells[cellKey];
                                  const strVal = val === null ? "NULL" : String(val);
                                  const isTruncated = val !== null && strVal.length > applied.textLength && !isExpanded;
                                  const displayVal = isTruncated ? strVal.slice(0, applied.textLength) + "…" : strVal;

                                  return (
                                    <td
                                      key={cIdx}
                                      className={val === null ? "cell-null" : ""}
                                      onClick={() => {
                                        if (strVal.length > applied.textLength) {
                                          setExpandedCells({ ...expandedCells, [cellKey]: !isExpanded });
                                        }
                                      }}
                                      title={strVal.length > applied.textLength ? (isExpanded ? "Clic para colapsar" : "Clic para ver completo") : undefined}
                                    >
                                      <div className="cell-content"><span>{val === null ? <em>NULL</em> : displayVal}</span>
                                        <button className="cell-copy" aria-label={`Copiar celda ${dataResult.columns[cIdx]}, fila ${rIdx + 1}`} title="Copiar valor completo"
                                          onClick={(e) => { e.stopPropagation(); void copy(cellText(val), "Celda copiada"); }}>⧉</button>
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
                    <div className="grid-loading" role="status">
                      <span className="grid-loading-spinner" aria-hidden="true" />
                      <span>Cargando registros…</span>
                    </div>
                  )}
                  </div>

                  {/* Pagination Bar */}
                  <div className="pagination-bar">
                    <span className="pagination-info">
                      {loading ? "" : `${dataResult?.rows.length || 0} ${dataResult?.rows.length === 1 ? "fila" : "filas"}${dataResult?.rows.length ? ` · posiciones ${offset + 1}–${offset + dataResult.rows.length}` : ""}`}
                      {exactTotal !== null && ` · ${exactTotal.toLocaleString()} coincidencias (conteo exacto)`}
                      {countError && <span className="count-error" role="alert">{countError}</span>}
                    </span>
                    <div className="pagination-controls">
                      <button disabled={loading || counting || !dataResult} onClick={() => void countRows()} title="Ejecuta COUNT(*) solo cuando lo solicitas">{counting ? "Contando…" : "Contar coincidencias"}</button>
                      {counting && <button onClick={() => { countRequest.current?.abort(); setCounting(false); }}>Cancelar conteo</button>}
                      <button
                        disabled={loading || page <= 1}
                        onClick={() => setPage((p) => Math.max(1, p - 1))}
                      >
                        ◀ Anterior
                      </button>
                      <span>Página {page}</span>
                      <button
                        disabled={loading || !dataResult?.hasMore}
                        onClick={() => { if (!dataResult) return; setPageOffsets((offsets) => [...offsets.slice(0, page), dataResult.nextOffset!]); setPage((p) => p + 1); }}
                      >
                        Siguiente ▶
                      </button>
                      <select
                        value={applied.limit}
                        aria-label="Filas por página"
                        onChange={(e) => {
                          setLimit(Number(e.target.value));
                          setApplied((current) => ({ ...current, limit: Number(e.target.value) }));
                          resetPagination();
                        }}
                      >
                        <option value={25}>25 filas</option>
                        <option value={50}>50 filas</option>
                        <option value={100}>100 filas</option>
                        <option value={500}>500 filas</option>
                      </select>
                    </div>
                  </div>
                </div>
              )}

              {/* STRUCTURE VIEW */}
              {activeTab === "structure" && schema && (
                <div className="structure-view">
                  <div className="structure-header">
                    <h3>Columnas</h3>
                    <button
                      onClick={() => {
                        setAlterSql(`ALTER TABLE \`${selectedTable}\` ADD COLUMN \`new_column\` VARCHAR(255) NOT NULL;`);
                        openView("modify");
                      }}
                    >
                      + Modificar tabla / Agregar columna
                    </button>
                  </div>

                  <table className="data-table">
                    <thead>
                      <tr>
                        <th>Campo</th>
                        <th>Tipo</th>
                        <th>Colación</th>
                        <th>Nulo</th>
                        <th>Clave</th>
                        <th>Por Defecto</th>
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
                            {c.key === "PRI" ? (
                              <span className="badge badge-pk">PRIMARIA</span>
                            ) : c.key ? (
                              <span className="badge">{c.key}</span>
                            ) : (
                              "—"
                            )}
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
                      <h3 style={{ marginTop: "1.5rem" }}>Índices</h3>
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
                        <button
                          className="btn-tiny"
                          onClick={() => navigator.clipboard.writeText(schema.createStatement)}
                        >
                          Copiar DDL
                        </button>
                      </div>
                      <pre className="ddl-block">{schema.createStatement}</pre>
                    </>
                  )}
                </div>
              )}

              {/* POINT 4: ALTER / MODIFY TABLE VIEW (TABS INTACT) */}
              {activeTab === "modify" && (
                <div className="alter-table-view">
                  <div className="alter-header">
                    <h3>
                      Modificar tabla: <strong>{selectedTable}</strong>
                    </h3>
                    <div className="alter-snippets">
                      <button
                        type="button"
                        onClick={() =>
                          setAlterSql(
                            `ALTER TABLE \`${selectedTable}\` ADD COLUMN \`nueva_columna\` VARCHAR(255) NULL;`
                          )
                        }
                      >
                        + Agregar columna
                      </button>
                      <button
                        type="button"
                        onClick={() =>
                          setAlterSql(
                            `ALTER TABLE \`${selectedTable}\` MODIFY COLUMN \`${schema?.columns[0]?.field || "col"}\` VARCHAR(255) NOT NULL;`
                          )
                        }
                      >
                        ✎ Modificar columna
                      </button>
                      <button
                        type="button"
                        onClick={() =>
                          setAlterSql(
                            `ALTER TABLE \`${selectedTable}\` DROP COLUMN \`${schema?.columns[schema?.columns.length - 1]?.field || "col"}\`;`
                          )
                        }
                      >
                        ✕ Eliminar columna
                      </button>
                      <button
                        type="button"
                        onClick={() =>
                          setAlterSql(
                            `ALTER TABLE \`${selectedTable}\` ADD INDEX \`idx_${selectedTable}_col\` (\`${schema?.columns[0]?.field || "col"}\`);`
                          )
                        }
                      >
                        🔑 Agregar índice
                      </button>
                      <button
                        type="button"
                        onClick={() =>
                          setAlterSql(
                            `ALTER TABLE \`${selectedTable}\` RENAME TO \`${selectedTable}_nuevo\`;`
                          )
                        }
                      >
                        ↻ Renombrar tabla
                      </button>
                    </div>
                  </div>

                  <form onSubmit={handleExecuteAlter} className="alter-form">
                    <textarea
                      rows={5}
                      value={alterSql}
                      onChange={(e) => setAlterSql(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
                          e.preventDefault();
                          void handleExecuteAlter();
                        }
                      }}
                      placeholder={`ALTER TABLE \`${selectedTable}\` ...`}
                    />

                    <div className="alter-actions">
                      <button type="submit" className="primary" disabled={readOnly || loading || !alterSql.trim()}>
                        ▶ Ejecutar sentencia ALTER
                      </button>
                      <span className="muted" style={{ fontSize: "0.775rem" }}>
                        Atajo: <strong>Ctrl + Enter</strong> para ejecutar
                      </span>
                    </div>
                  </form>

                  {alterStatus && (
                    <div className={`alert ${alterStatus.isError ? "alert-error" : "alert-success"}`}>
                      {alterStatus.message}
                    </div>
                  )}

                  {/* Column Reference Table */}
                  {schema && (
                    <div className="alter-columns-ref">
                      <h4>Columnas actuales de la tabla ({schema.columns.length})</h4>
                      <div className="table-responsive">
                        <table className="data-table">
                          <thead>
                            <tr>
                              <th>Campo</th>
                              <th>Tipo</th>
                              <th>Colación</th>
                              <th>Nulo</th>
                              <th>Clave</th>
                              <th>Por Defecto</th>
                              <th>Acciones rápidas</th>
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
                                <td>{c.key || "—"}</td>
                                <td className="mono">{c.default !== null ? c.default : <em>NULL</em>}</td>
                                <td>
                                  <button
                                    type="button"
                                    className="btn-tiny"
                                    onClick={() =>
                                      setAlterSql(
                                        `ALTER TABLE \`${selectedTable}\` MODIFY COLUMN \`${c.field}\` ${c.type} NOT NULL;`
                                      )
                                    }
                                  >
                                    Modificar
                                  </button>
                                  {!readOnly && (
                                    <button
                                      type="button"
                                      className="btn-tiny link-danger"
                                      style={{ marginLeft: "0.3rem" }}
                                      onClick={() =>
                                        setAlterSql(
                                          `ALTER TABLE \`${selectedTable}\` DROP COLUMN \`${c.field}\`;`
                                        )
                                      }
                                    >
                                      Eliminar
                                    </button>
                                  )}
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    </div>
                  )}
                </div>
              )}
            </div>
          )}

          {/* VIEW: SQL CONSOLE */}
          {activeTab === "sql" && (
            <div className="sql-console">
              <div className="sql-header">
                <h2>Consola SQL</h2>
                <div className="sql-shortcuts">
                  <span>Atajo: <strong>Ctrl + Enter</strong> para ejecutar</span>
                </div>
              </div>
              <p className="muted">Una sentencia por ejecución. Máximo 1.000 filas y 8 MiB de datos JSON; usa Exportar para descargar una tabla completa.</p>

              <div className="sql-editor-container">
                <textarea
                  className="sql-editor"
                  value={sqlQuery}
                  onChange={(e) => setSqlQuery(e.target.value)}
                  onKeyDown={(e) => {
                    if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
                      e.preventDefault();
                      void runQuery();
                    }
                  }}
                  placeholder="Escribe tu consulta SQL aquí…"
                  rows={8}
                />
                <div className="sql-actions">
                  <div className="sql-snippets">
                    {selectedTable && (
                      <>
                        <button
                          className="btn-snippet"
                          onClick={() => setSqlQuery(`SELECT * FROM \`${selectedTable}\` LIMIT 50;`)}
                        >
                          SELECT *
                        </button>
                        <button
                          className="btn-snippet"
                          onClick={() => setSqlQuery(`EXPLAIN SELECT * FROM \`${selectedTable}\`;`)}
                        >
                          EXPLAIN
                        </button>
                      </>
                    )}
                    <button className="btn-snippet" onClick={() => setSqlQuery("SHOW TABLES;")}>
                      SHOW TABLES
                    </button>
                    <button className="btn-snippet" onClick={() => setSqlQuery("SHOW PROCESSLIST;")}>
                      PROCESSLIST
                    </button>
                  </div>

                  <button
                    className="primary"
                    disabled={sqlLoading}
                    onClick={() => void runQuery()}
                  >
                    {sqlLoading ? "Ejecutando…" : "▶ Ejecutar consulta"}
                  </button>
                </div>
              </div>

              {sqlError && <div className="alert alert-error">{sqlError}</div>}

              {sqlResult && (
                <div className="sql-result-section">
                  <ResultLimitNotice result={sqlResult} />
                  <div className="result-stats">
                    {sqlResult.isSelect ? (
                      <span>
                        ✔ Consulta ejecutada en <strong>{sqlResult.executionMs} ms</strong> ·{" "}
                        <strong>{sqlResult.rows.length}</strong> filas devueltas
                      </span>
                    ) : (
                      <span>
                        ✔ Sentencia ejecutada en <strong>{sqlResult.executionMs} ms</strong> ·{" "}
                        <strong>{sqlResult.affectedRows}</strong> filas afectadas
                      </span>
                    )}
                  </div>

                  {sqlResult.isSelect && sqlResult.rows.length > 0 && (
                    <div className="grid-container">
                      <table className="data-table">
                        <thead>
                          <tr><th>Registro</th>
                            {sqlResult.columns.map((c, index) => (
                              <th key={`${c}-${index}`}>{c}</th>
                            ))}
                          </tr>
                        </thead>
                        <tbody>
                          {sqlResult.rows.map((row, rIdx) => (
                            <tr key={rIdx}>
 <td className="td-actions">
  <button className="btn-action-view" onClick={() => viewRecord(row, sqlResult.columns, true)} aria-label={`Ver detalle SQL ${rIdx + 1}`}>⌕</button>
  <ActionMenu label={`Más acciones SQL ${rIdx+1}`} actions={[{label:"Copiar fila como JSON",onClick:()=>void copy(rowJSON(sqlResult.columns,row),"Fila copiada como JSON")}]} />
 </td>
                              {row.map((val, cIdx) => (
                                <td key={cIdx} className={val === null ? "cell-null" : ""}>
                                  <div className="cell-content"><span>{val === null ? <em>NULL</em> : String(val)}</span><button className="cell-copy" aria-label={`Copiar celda SQL ${sqlResult.columns[cIdx]}, fila ${rIdx + 1}`} onClick={() => void copy(cellText(val), "Celda copiada")}>⧉</button></div>
                                </td>
                              ))}
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  )}
                </div>
              )}

              {queryHistory.length > 0 && (
                <details className="sql-history">
                  <summary>Historial de consultas recientes ({queryHistory.length})</summary>
                  <div className="history-list">
                    {queryHistory.map((q, idx) => (
                      <div
                        key={idx}
                        className="history-item"
                        onClick={() => setSqlQuery(q)}
                        title="Haga clic para cargar en el editor"
                      >
                        <code>{q}</code>
                      </div>
                    ))}
                  </div>
                </details>
              )}
            </div>
          )}

          {/* VIEW: EXPORT VIEW */}
          {activeTab === "export" && (
            <div className="export-view">
              <h2>Exportar base de datos: {selectedDb || "todas"}</h2>
              <div className="card form export-card">
                <p className="muted">
                  Descarga un volcado de <strong>{selectedDb || connection.name}</strong>.
                </p>

                <label>
                  Formato de salida
                  <select
                    value={exportFormat}
                    onChange={(e) => setExportFormat(e.target.value as any)}
                  >
                    <option value="sql">SQL Dump (CREATE + INSERT)</option>
                    <option value="csv">CSV (solo datos de tabla)</option>
                    <option value="json">JSON (colección de registros)</option>
                  </select>
                </label>

                <label>Tabla a exportar
                  <select value={selectedTable || ""} onChange={(e) => setSelectedTable(e.target.value || null)}>
                    <option value="">{exportFormat === "sql" ? "Todas las tablas de la base" : "Selecciona una tabla"}</option>
                    {tables.map((table) => <option key={table.name} value={table.name}>{table.name}</option>)}
                  </select>
                </label>
                <p className="muted">La descarga recorre los datos fila por fila. CSV y JSON requieren una tabla seleccionada.</p>

                {exportFormat === "sql" && (
                  <div className="export-checkboxes">
                    <label className="check">
                      <input
                        type="checkbox"
                        checked={exportIncludeStructure}
                        onChange={(e) => setExportIncludeStructure(e.target.checked)}
                      />
                      Incluir estructura (CREATE TABLE)
                    </label>
                    <label className="check">
                      <input
                        type="checkbox"
                        checked={exportIncludeData}
                        onChange={(e) => setExportIncludeData(e.target.checked)}
                      />
                      Incluir datos (INSERT INTO)
                    </label>
                  </div>
                )}

                <div className="actions" style={{ marginTop: "1rem" }}>
                  <a
                    className="primary button-link"
                    href={selectedDb && (exportFormat === "sql" ? exportIncludeStructure || exportIncludeData : selectedTable) ? `/api/connections/${connection.id}/export?db=${encodeURIComponent(selectedDb)}${selectedTable ? `&table=${encodeURIComponent(selectedTable)}` : ""}&format=${exportFormat}&structure=${exportIncludeStructure ? "1" : "0"}&data=${exportIncludeData ? "1" : "0"}` : undefined}
                    aria-disabled={!selectedDb || (exportFormat === "sql" ? !exportIncludeStructure && !exportIncludeData : !selectedTable)}
                    download
                  >
                    ⬇ Descargar archivo
                  </a>
                </div>
              </div>
            </div>
          )}
        </main>
      </div>

      {confirmationDialog}
      {detail && <RecordDetail snapshot={detail} onClose={() => setDetail(null)} />}
      {copyNotice}
      {editingRow&&schema&&activeTab!=="insert"&&<Dialog className="row-editor-dialog" title={editingRow.mode==="insert"?"Duplicar registro":"Editar registro"} onClose={()=>{if(!rowDirty||window.confirm("¿Descartar los cambios del registro?")){setEditingRow(null);setRowDirty(false);}}}><RowEditor schema={schema} mode={editingRow.mode} values={editingRow.values} onChange={values=>setEditingRow({...editingRow,values})} onSubmit={saveRow} onCancel={()=>{if(!rowDirty||window.confirm("¿Descartar los cambios del registro?")){setEditingRow(null);setRowDirty(false);}}} onDirtyChange={setRowDirty} disabled={readOnly}/></Dialog>}

      {/* Create Table Modal */}
      {creatingTable && (
        <div className="modal-overlay" onClick={() => setCreatingTable(false)}>
          <div className="modal-content modal-large" onClick={(e) => e.stopPropagation()}>
            <header className="modal-header">
              <h3>Crear nueva tabla en {selectedDb}</h3>
              <button className="btn-close" onClick={() => setCreatingTable(false)}>
                ✕
              </button>
            </header>

            <form onSubmit={handleCreateTableSubmit} className="modal-form">
              <div className="modal-fields">
                <label>
                  Nombre de la tabla:
                  <input
                    type="text"
                    required
                    placeholder="ej. products"
                    value={newTableName}
                    onChange={(e) => setNewTableName(e.target.value)}
                  />
                </label>

                <h4 style={{ margin: "0.5rem 0 0.25rem" }}>Columnas:</h4>
                <div className="create-cols-list">
                  {newTableCols.map((col, idx) => (
                    <div key={idx} className="col-def-row">
                      <input
                        type="text"
                        placeholder="Nombre"
                        value={col.name}
                        onChange={(e) => {
                          const updated = [...newTableCols];
                          updated[idx].name = e.target.value;
                          setNewTableCols(updated);
                        }}
                        style={{ flex: 2 }}
                        required
                      />
                      <select
                        value={col.type}
                        onChange={(e) => {
                          const updated = [...newTableCols];
                          updated[idx].type = e.target.value;
                          setNewTableCols(updated);
                        }}
                        style={{ flex: 1.5 }}
                      >
                        <option value="INT">INT</option>
                        <option value="VARCHAR">VARCHAR</option>
                        <option value="TEXT">TEXT</option>
                        <option value="DATETIME">DATETIME</option>
                        <option value="TINYINT">TINYINT</option>
                        <option value="DECIMAL">DECIMAL</option>
                      </select>
                      <input
                        type="text"
                        placeholder="Longitud"
                        value={col.length || ""}
                        onChange={(e) => {
                          const updated = [...newTableCols];
                          updated[idx].length = e.target.value;
                          setNewTableCols(updated);
                        }}
                        style={{ width: "5rem" }}
                      />
                      <label className="checkbox-mini">
                        <input
                          type="checkbox"
                          checked={col.autoIncrement}
                          onChange={(e) => {
                            const updated = [...newTableCols];
                            updated[idx].autoIncrement = e.target.checked;
                            if (e.target.checked) updated[idx].primaryKey = true;
                            setNewTableCols(updated);
                          }}
                        />
                        AI
                      </label>
                      <label className="checkbox-mini">
                        <input
                          type="checkbox"
                          checked={col.primaryKey}
                          onChange={(e) => {
                            const updated = [...newTableCols];
                            updated[idx].primaryKey = e.target.checked;
                            setNewTableCols(updated);
                          }}
                        />
                        PK
                      </label>
                      <label className="checkbox-mini">
                        <input
                          type="checkbox"
                          checked={col.null}
                          onChange={(e) => {
                            const updated = [...newTableCols];
                            updated[idx].null = e.target.checked;
                            setNewTableCols(updated);
                          }}
                        />
                        Null
                      </label>
                      {newTableCols.length > 1 && (
                        <button
                          type="button"
                          className="btn-tiny danger"
                          onClick={() => {
                            setNewTableCols(newTableCols.filter((_, i) => i !== idx));
                          }}
                        >
                          ✕
                        </button>
                      )}
                    </div>
                  ))}
                </div>

                <button
                  type="button"
                  onClick={() => {
                    setNewTableCols([
                      ...newTableCols,
                      { name: "", type: "VARCHAR", length: "255", null: true, autoIncrement: false, primaryKey: false },
                    ]);
                  }}
                  style={{ alignSelf: "flex-start", marginTop: "0.5rem" }}
                >
                  + Agregar columna
                </button>
              </div>

              <footer className="modal-footer">
                <button type="button" onClick={() => setCreatingTable(false)}>
                  Cancelar
                </button>
                <button type="submit" className="primary">
                  Crear tabla
                </button>
              </footer>
            </form>
          </div>
        </div>
      )}

      {/* Create Database Modal */}
      {creatingDb && (
        <div className="modal-overlay" onClick={() => setCreatingDb(false)}>
          <div className="modal-content" onClick={(e) => e.stopPropagation()}>
            <header className="modal-header">
              <h3>Crear Base de datos</h3>
              <button className="btn-close" onClick={() => setCreatingDb(false)}>
                ✕
              </button>
            </header>
            <form onSubmit={handleCreateDb} className="modal-form">
              <div className="modal-fields">
                <label>
                  Nombre de la base de datos:
                  <input
                    type="text"
                    required
                    placeholder="ej. mi_nueva_bd"
                    value={newDbName}
                    onChange={(e) => setNewDbName(e.target.value)}
                  />
                </label>
              </div>
              <footer className="modal-footer">
                <button type="button" onClick={() => setCreatingDb(false)}>
                  Cancelar
                </button>
                <button type="submit" className="primary">
                  Crear Base de datos
                </button>
              </footer>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
