// Registry of open work tabs persisted across reloads (localStorage).
// Kept free of React so the restore decisions stay unit-testable.
import type { WorkspaceView } from "./workspace/shared";

export const MAX_TABS = 48;
export const MAX_GROUPS = 16;

const VIEWS: WorkspaceView[] = ["browse", "data", "structure", "modify", "insert", "sql", "export"];

export interface WorkTab {
  id: string;
  connectionId: string;
  database: string;
  table: string;
  sql: boolean;
  view: WorkspaceView;
  title: string;
  /** Name the user gave a query tab. */
  customTitle?: string;
  dirty?: boolean;
  groupId: string;
  groupTable: string;
  groupDatabase: string;
}

export function scope(connectionId: string, db: string, table: string) {
  return `table:${connectionId}:${encodeURIComponent(db)}:${encodeURIComponent(table)}`;
}

/** Rebuilds the persisted tab list, dropping malformed entries. Never throws. */
export function restoreTabs(raw: string | null): WorkTab[] {
  try {
    const values = JSON.parse(raw || "[]");
    if (!Array.isArray(values)) return [];
    return values
      .filter(
        (t) =>
          t &&
          typeof t.id === "string" &&
          typeof t.connectionId === "string" &&
          typeof t.database === "string" &&
          typeof t.table === "string" &&
          typeof t.sql === "boolean",
      )
      .slice(0, MAX_TABS)
      .map((t) => {
        const view: WorkspaceView = VIEWS.includes(t.view) ? t.view : t.sql ? "sql" : t.table ? "data" : "browse";
        const groupTable = typeof t.groupTable === "string" ? t.groupTable : t.table;
        const groupDatabase = typeof t.groupDatabase === "string" ? t.groupDatabase : t.database;
        return {
          ...t,
          view,
          dirty: false,
          customTitle: typeof t.customTitle === "string" ? t.customTitle.slice(0, 60) : undefined,
          groupTable,
          groupDatabase,
          groupId: groupTable ? scope(t.connectionId, groupDatabase, groupTable) : t.id,
        };
      });
  } catch {
    return [];
  }
}

/** Picks the tab to show after a reload: the stored one while it still exists, else home. */
export function restoreActiveTab(stored: string | null, tabs: WorkTab[]): string {
  if (stored && stored !== "home" && tabs.some((t) => t.id === stored)) return stored;
  return "home";
}
