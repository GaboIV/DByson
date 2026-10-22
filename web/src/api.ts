export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

let csrf = "";
let viewOnly = false;
export const setViewOnly = (value: boolean) => {viewOnly = value;};
export const setCsrf = (t: string) => {
  csrf = t;
};

export async function api<T>(method: string, path: string, body?: unknown, options: { signal?: AbortSignal } = {}): Promise<T> {
  const started=performance.now();
  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    signal: options.signal,
    headers: {
      ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
      ...(viewOnly ? {"X-DByson-Read-Only":"1"} : {}),
      ...(csrf ? { "X-CSRF-Token": csrf } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch((error) => {
    if (options.signal?.aborted) throw error;
    return {};
  });
  if (options.signal?.aborted) throw new DOMException("Request cancelled", "AbortError");
  const sql = data?.result?.executedSql || data?.executedSql;
  let queryDatabase:string|undefined;
  if(body&&typeof body==="object"&&"database" in body&&typeof body.database==="string")queryDatabase=body.database;
  else {const part=new URL(path,location.origin).pathname.split("/databases/")[1]?.split("/")[0];if(part){try{queryDatabase=decodeURIComponent(part);}catch{queryDatabase=part;}}}
  if(typeof sql === "string" && sql && path.startsWith("/api/connections/")) window.dispatchEvent(new CustomEvent("dbyson-query",{detail:{id:crypto.randomUUID(),sql:sql.slice(0,12000),truncated:sql.length>12000,database:queryDatabase,path,at:Date.now(),ms:Math.round(performance.now()-started),ok:res.ok&&data.ok!==false}}));
  if (!res.ok) throw new ApiError(res.status, (data as { error?: string }).error ?? `Error ${res.status}`);
  return data as T;
}

export type Env = "local" | "dev" | "staging" | "prod";
export type TLSMode = "disabled" | "required" | "verify_ca" | "verify_identity";

export interface Session {
  username: string;
  role: string;
  csrf: string;
}

export interface SSHHop {
 host: string; port: number; user: string; auth: "key" | "password" | "agent"; fingerprint: string;
 password?: string; privateKey?: string; passphrase?: string; keyFile?: string; keyLabel?: string;
 hasPassword?: boolean; hasPrivateKey?: boolean;
}
export interface SSHConfig extends SSHHop { enabled: boolean; jump?: SSHHop }
export const emptyHop = (): SSHHop => ({ host: "", port: 22, user: "", auth: "key", fingerprint: "" });
export interface Discovery {
 containerized: boolean; dockerAvailable: boolean; note: string;
 networks: {name: string; addresses: string[]}[];
 services: {host: string; port: number; version: string; source: string}[];
}
export interface Connection {
 ssh?: SSHConfig;
  id: string;
  name: string;
  env: Env;
  host: string;
  port: number;
  username: string;
  database: string;
  tlsMode: TLSMode;
  readOnly: boolean;
  hasPassword: boolean;
  hasCaCert: boolean;
  hasClientCert: boolean;
}

export interface ConnectionInput {
 ssh?: SSHConfig;
  name: string;
  env: Env;
  host: string;
  port: number;
  username: string;
  database: string;
  tlsMode: TLSMode;
  readOnly: boolean;
  password?: string;
  caCert?: string;
  clientCert?: string;
  clientKey?: string;
}

export interface TestResponse {
  ok: boolean;
  error?: string;
  result?: { serverVersion: string; tls: boolean; tlsCipher?: string; tlsVersion?: string; latencyMs: number };
}

export interface TableInfo {
  name: string;
  type: string;
  engine: string;
  collation: string;
  rows: number;
  dataLength: number;
  indexLength: number;
  dataFree: number;
  autoIncrement?: number;
  comment: string;
}

export interface DatabaseInfo {
  name: string;
  collation: string;
  tables: number;
  size: number;
}

export interface DatabasesResponse {
  databases: string[];
  databaseDetails: DatabaseInfo[];
  executedSql: string;
}

export interface TablesResponse {
  tables: TableInfo[];
  executedSql: string;
}

export interface ColumnInfo {
  field: string;
  type: string;
  collation?: string;
  null: string;
  key: string;
  default: string | null;
  extra: string;
  comment: string;
}

export interface IndexInfo {
  name: string;
  nonUnique: boolean;
  columns: string[];
  type: string;
}

export interface TableSchema {
  name: string;
  columns: ColumnInfo[];
  indexes: IndexInfo[];
  primaryKey: string[];
  createStatement: string;
  executedSql?: string;
}

export interface QueryResult {
  hasMore?: boolean;
  nextOffset?: number;
  totalRowsKnown?: boolean;
	truncated?: boolean;
	truncationReason?: "rows" | "bytes";
	rowLimit?: number;
	byteLimit?: number;
	resultBytes?: number;
  columns: string[];
  rows: (string | number | boolean | null)[][];
  totalRows: number;
  affectedRows: number;
  executionMs: number;
  executedSql: string;
  isSelect: boolean;
  error?: string;
}

export interface QueryResponse {
  ok: boolean;
  result?: QueryResult;
  error?: string;
  executedSql?: string;
  needsConfirmation?: boolean;
  operation?: string;
  reason?: string;
}

export interface NewColumnDef {
  name: string;
  type: string;
  length?: string;
  null: boolean;
  default?: string;
  autoIncrement: boolean;
  primaryKey: boolean;
  comment?: string;
}
