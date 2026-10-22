// Shared cache for server metadata (databases, tables, table schemas).
//
// Every workbench tab is its own <Workspace>, so without a shared cache each tab — and each
// re-activation of a tab — fetched the same lists again. Entries here are fetched once, shared
// by every tab of the session, and only refetched on an explicit refresh or after an
// invalidation caused by a change (DDL, writes). While refetching, the previous data stays
// visible (stale-while-revalidate).
import { useCallback, useEffect, useSyncExternalStore } from "react";
import { api, ApiError, type DatabasesResponse, type DatabaseInfo, type TableInfo, type TablesResponse, type TableSchema } from "../api";

interface Loaded<T> {
  data: T;
  sql?: string;
}

interface Entry<T> {
  data?: T;
  sql?: string;
  error?: string;
  loading: boolean;
  stale: boolean;
}

type Fetcher<T> = () => Promise<Loaded<T>>;

const EMPTY: Entry<never> = { loading: false, stale: false };
const entries = new Map<string, Entry<unknown>>();
const fetchers = new Map<string, Fetcher<unknown>>();
const listeners = new Map<string, Set<() => void>>();
const inflight = new Map<string, Promise<void>>();
let generation = 0; // bumps on clearCatalog so late responses from a previous session are dropped

function set(key: string, patch: Partial<Entry<unknown>>) {
  // Snapshots must be new objects for useSyncExternalStore to notice the change.
  entries.set(key, { ...(entries.get(key) ?? EMPTY), ...patch });
  listeners.get(key)?.forEach((notify) => notify());
}

function start(key: string): Promise<void> {
  const running = inflight.get(key);
  if (running) return running;
  const fetcher = fetchers.get(key);
  if (!fetcher) return Promise.resolve();
  const gen = generation;
  set(key, { loading: true });
  const promise = fetcher()
    .then((res) => {
      if (gen === generation) set(key, { data: res.data, sql: res.sql, error: undefined, stale: false });
    })
    .catch((e: unknown) => {
      if (gen === generation) set(key, { error: e instanceof ApiError ? e.message : "No se pudo cargar", stale: false });
    })
    .finally(() => {
      inflight.delete(key);
      if (gen === generation) set(key, { loading: false });
    });
  inflight.set(key, promise);
  return promise;
}

/** Marks every entry whose key starts with prefix as stale; visible ones refetch right away. */
function invalidate(prefix: string) {
  for (const key of [...entries.keys()]) {
    if (!key.startsWith(prefix)) continue;
    if (listeners.get(key)?.size) void start(key);
    else set(key, { stale: true });
  }
}

export interface Resource<T> {
  data?: T;
  sql?: string;
  error?: string;
  /** true only while there is nothing to show yet */
  loading: boolean;
  /** true while a background refresh runs over data that is already shown */
  refreshing: boolean;
  refresh: () => Promise<void>;
}

function useResource<T>(key: string | null, fetcher: Fetcher<T>, enabled: boolean): Resource<T> {
  if (key) fetchers.set(key, fetcher as Fetcher<unknown>);
  const subscribe = useCallback(
    (notify: () => void) => {
      if (!key) return () => {};
      const subs = listeners.get(key) ?? new Set();
      subs.add(notify);
      listeners.set(key, subs);
      return () => {
        subs.delete(notify);
      };
    },
    [key],
  );
  const entry = useSyncExternalStore(subscribe, () => (key ? (entries.get(key) ?? EMPTY) : EMPTY)) as Entry<T>;
  const needsFetch = !!key && enabled && !entry.loading && ((entry.data === undefined && entry.error === undefined) || entry.stale);
  useEffect(() => {
    if (needsFetch && key) void start(key);
  }, [needsFetch, key]);
  const refresh = useCallback(() => (key ? start(key) : Promise.resolve()), [key]);
  return {
    data: entry.data,
    sql: entry.sql,
    error: entry.error,
    loading: entry.loading && entry.data === undefined,
    refreshing: entry.loading && entry.data !== undefined,
    refresh,
  };
}

const enc = encodeURIComponent;

export function useDatabases(connectionId: string, enabled = true) {
  return useResource<DatabaseInfo[]>(
    `dbs:${connectionId}`,
    async () => {
      const r = await api<DatabasesResponse>("GET", `/api/connections/${connectionId}/databases`);
      const details = r.databaseDetails?.length
        ? r.databaseDetails
        : r.databases.map((name) => ({ name, collation: "", tables: 0, size: 0 }));
      return { data: details, sql: r.executedSql };
    },
    enabled,
  );
}

export function useTables(connectionId: string, database: string, enabled = true) {
  return useResource<TableInfo[]>(
    database ? `tables:${connectionId}:${database}` : null,
    async () => {
      const r = await api<TablesResponse>("GET", `/api/connections/${connectionId}/databases/${enc(database)}/tables`);
      return { data: r.tables, sql: r.executedSql };
    },
    enabled,
  );
}

export function useSchema(connectionId: string, database: string, table: string, enabled = true) {
  return useResource<TableSchema>(
    database && table ? `schema:${connectionId}:${database}:${table}` : null,
    async () => {
      const r = await api<TableSchema>("GET", `/api/connections/${connectionId}/databases/${enc(database)}/tables/${enc(table)}/schema`);
      return { data: r, sql: r.executedSql };
    },
    enabled,
  );
}

export const invalidateDatabases = (connectionId: string) => invalidate(`dbs:${connectionId}`);
export const invalidateTables = (connectionId: string, database: string) => invalidate(`tables:${connectionId}:${database}`);
export const invalidateSchema = (connectionId: string, database: string, table: string) =>
  invalidate(`schema:${connectionId}:${database}:${table}`);

/** After a statement that may have changed anything on the server (e.g. free SQL). */
export function invalidateConnection(connectionId: string) {
  invalidate(`dbs:${connectionId}`);
  invalidate(`tables:${connectionId}:`);
  invalidate(`schema:${connectionId}:`);
}

/** Drops everything, e.g. on logout, so another user never sees this session's metadata. */
export function clearCatalog() {
  generation++;
  entries.clear();
  fetchers.clear();
  inflight.clear();
}
