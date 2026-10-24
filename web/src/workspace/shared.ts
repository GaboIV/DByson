import type { ReactNode } from "react";
import type { Connection, QueryResult, TableSchema } from "../api";

export type WorkspaceView = "browse" | "data" | "structure" | "modify" | "insert" | "sql" | "export";

export type ConfirmedApi = <T>(
  method: string,
  path: string,
  body?: Record<string, unknown>,
  options?: { signal?: AbortSignal },
) => Promise<T | undefined>;

/** Everything a view needs from the workspace that hosts it. */
export interface ViewContext {
  connection: Connection;
  database: string;
  table: string;
  readOnly: boolean;
  isActive: boolean;
  confirmedApi: ConfirmedApi;
  copy: (text: string, label?: string) => Promise<void>;
  /** Shows the statement in the SQL banner. */
  reportSql: (sql: string, ms?: number | null) => void;
  openView: (view: WorkspaceView) => void;
  newSQL: (query?: string, database?: string) => void;
  /** Rendered above the view: table view navigation and table-level actions. */
  toolbar?: ReactNode;
}

export const enc = encodeURIComponent;

export function tablePath(ctx: ViewContext, table = ctx.table) {
  return `/api/connections/${ctx.connection.id}/databases/${enc(ctx.database)}/tables/${enc(table)}`;
}

export function cleanStr(s: string): string {
  return (s || "").normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase();
}

export function formatBytes(bytes: number): string {
  if (!bytes) return "0 B";
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(sizes.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  return `${parseFloat((bytes / 1024 ** i).toFixed(1))} ${sizes[i]}`;
}

export const errorText = (e: unknown, fallback: string) => (e instanceof Error && e.message ? e.message : fallback);

export const SYSTEM_DATABASES = ["information_schema", "performance_schema", "mysql", "sys"];

export type Row = QueryResult["rows"][number];

/** Primary-key values of a result row, or null when the row lacks part of the key. */
export function primaryKeyOf(schema: TableSchema, columns: string[], row: Row): Record<string, unknown> | null {
  if (!schema.primaryKey.length) return null;
  const pk: Record<string, unknown> = {};
  for (const key of schema.primaryKey) {
    const i = columns.indexOf(key);
    if (i < 0) return null;
    pk[key] = row[i];
  }
  return pk;
}

/** Values for the row editor, keyed by column. */
export function rowValues(schema: TableSchema, columns: string[], row: Row): Record<string, unknown> {
  const values: Record<string, unknown> = {};
  for (const col of schema.columns) {
    const i = columns.indexOf(col.field);
    values[col.field] = i >= 0 ? row[i] : null;
  }
  return values;
}

export function emptyRowValues(schema: TableSchema): Record<string, unknown> {
  const values: Record<string, unknown> = {};
  for (const col of schema.columns) {
    values[col.field] = col.default !== null ? col.default : col.null === "YES" || col.extra.includes("auto_increment") ? null : "";
  }
  return values;
}

/** Generated columns are computed by MySQL and must never be written. */
export function writableValues(schema: TableSchema, values: Record<string, unknown>) {
  return Object.fromEntries(
    Object.entries(values).filter(
      ([field]) =>
        !schema.columns
          .find((c) => c.field === field)
          ?.extra.toLowerCase()
          .includes("generated"),
    ),
  );
}
