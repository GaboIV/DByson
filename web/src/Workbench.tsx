// The tabbed shell: a home tab with the connection list, plus one group of tabs per opened
// table (its views and query tabs) or per database/connection browse tab.
import { useCallback, useEffect, useRef, useState } from "react";
import { api, type Connection, type Session } from "./api";
import { ActionMenu } from "./ActionMenu";
import { Brand } from "./Brand";
import { Connections } from "./Connections";
import { Dialog } from "./Dialog";
import { useConfirm } from "./Confirm";
import { PreferencesDialog, QueryHistoryDialog, usePreferences } from "./Preferences";
import { AuditDialog, SecurityDialog } from "./Security";
import { TabStrip, type TabItem } from "./TabStrip";
import { Workspace, type WorkspaceView } from "./Workspace";
import { pickSqlFile } from "./workspace/sqlFile";

const VIEW_LABELS: Partial<Record<WorkspaceView, string>> = {
  data: "Contenido",
  structure: "Estructura",
  modify: "Modificar",
  insert: "Nuevo registro",
  sql: "Consulta SQL",
  export: "Exportar",
};
const VIEWS: WorkspaceView[] = ["browse", "data", "structure", "modify", "insert", "sql", "export"];
const MAX_TABS = 48;
const MAX_GROUPS = 16;

interface WorkTab {
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

function scope(connectionId: string, db: string, table: string) {
  return `table:${connectionId}:${encodeURIComponent(db)}:${encodeURIComponent(table)}`;
}

function restore(key: string): WorkTab[] {
  try {
    const values = JSON.parse(localStorage.getItem(key) || "[]");
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

export function Workbench({ session, onLogout }: { session: Session; onLogout: () => void }) {
  const confirm = useConfirm();
  const { preferences } = usePreferences();
  const storageKey = `dbyson:${session.username}`;
  const initialTabs = restore(storageKey + ":tabs");
  const [tabs, setTabs] = useState<WorkTab[]>(() => initialTabs);
  const [active, setActive] = useState<string>(() => {
    try {
      const saved = localStorage.getItem(storageKey + ":activeTab");
      if (saved && (saved === "home" || initialTabs.some((t) => t.id === saved))) {
        return saved;
      }
    } catch {
      /* ignore */
    }
    return "home";
  });
  const [visited, setVisited] = useState<string[]>(() => {
    try {
      const saved = localStorage.getItem(storageKey + ":activeTab");
      if (saved && saved !== "home" && initialTabs.some((t) => t.id === saved)) {
        return [saved];
      }
    } catch {
      /* ignore */
    }
    return [];
  });
  const [connections, setConnections] = useState<Connection[]>([]);
  const [palette, setPalette] = useState(false);
  const [search, setSearch] = useState("");
  const [error, setError] = useState("");
  const [newConnection, setNewConnection] = useState(0);
  const [settings, setSettings] = useState(false);
  const [history, setHistory] = useState(false);
  const [security, setSecurity] = useState(false);
  const [audit, setAudit] = useState(false);
  const [mfa, setMfa] = useState(session.mfa);
  const [renaming, setRenaming] = useState<{ id: string; value: string } | null>(null);
  const seeds = useRef<Record<string, string>>({});
  const lastPane = useRef<Record<string, string>>(
    initialTabs.reduce<Record<string, string>>((acc, t) => {
      acc[t.groupId] = t.id;
      return acc;
    }, {}),
  );

  useEffect(() => {
    const ask = () => setSecurity(true);
    window.addEventListener("dbyson-mfa-required", ask);
    return () => window.removeEventListener("dbyson-mfa-required", ask);
  }, []);

  const refresh = useCallback(async (signal?: AbortSignal) => {
    try {
      setConnections(await api<Connection[]>("GET", "/api/connections", undefined, { signal }));
    } catch (e) {
      if (!signal?.aborted) setError(e instanceof Error ? e.message : "No se pudieron cargar las conexiones");
    }
  }, []);
  useEffect(() => {
    const c = new AbortController();
    if (active === "home" || palette) void refresh(c.signal);
    return () => c.abort();
  }, [active, palette, refresh]);

  useEffect(() => {
    try {
      localStorage.setItem(storageKey + ":tabs", JSON.stringify(tabs.map(({ dirty: _dirty, ...t }) => t)));
    } catch {
      /* storage unavailable: tabs simply won't be restored */
    }
  }, [tabs, storageKey]);

  useEffect(() => {
    try {
      localStorage.setItem(storageKey + ":activeTab", active);
    } catch {
      /* ignore */
    }
  }, [active, storageKey]);

  const groups = [...new Map(tabs.map((t) => [t.groupId, tabs.find((p) => p.groupId === t.groupId)!])).values()];
  const current = tabs.find((t) => t.id === active);
  const currentConnection = connections.find((c) => c.id === current?.connectionId);

  function activate(id: string) {
    const pane = tabs.find((t) => t.id === id);
    if (pane) lastPane.current[pane.groupId] = id;
    setActive(id);
    if (id !== "home") setVisited((old) => (old.includes(id) ? old : [...old, id]));
    setError("");
  }

  function open(
    c: Connection,
    database = c.database,
    table = "",
    view: WorkspaceView = "browse",
    query?: string,
    parent?: WorkTab,
    customTitle?: string,
  ) {
    setConnections((old) => [...old.filter((x) => x.id !== c.id), c]);
    const groupId = parent?.groupId || (table ? scope(c.id, database, table) : "");
    const match =
      view !== "sql" &&
      tabs.find((t) =>
        groupId
          ? t.groupId === groupId && t.view === view
          : t.connectionId === c.id && t.database === database && !t.table && t.view === view,
      );
    if (match) return activate(match.id);
    if (tabs.length >= MAX_TABS || (!tabs.some((t) => t.groupId === groupId) && groups.length >= MAX_GROUPS)) {
      setError(`Cierra una pestaña para continuar (máximo ${MAX_GROUPS} principales y ${MAX_TABS} vistas).`);
      return;
    }
    const id = crypto.randomUUID();
    if (query) seeds.current[id] = query;
    const pane: WorkTab = {
      id,
      connectionId: c.id,
      database,
      table,
      view,
      sql: view === "sql",
      title: view === "sql" ? "Consulta SQL" : table || database || c.name,
      customTitle,
      groupId: groupId || id,
      groupTable: parent?.groupTable ?? table,
      groupDatabase: parent?.groupDatabase ?? database,
    };
    setTabs((old) => [...old, pane]);
    lastPane.current[pane.groupId] = id;
    activate(id);
  }

  /** Opens a query tab in the context of `pane` (same table group when it has one). */
  function newQuery(pane: WorkTab | undefined, query?: string, title?: string) {
    const c = connections.find((x) => x.id === pane?.connectionId);
    if (!pane || !c) return;
    open(c, pane.groupDatabase || pane.database, pane.groupTable, "sql", query, pane.groupTable ? pane : undefined, title);
  }

  async function importQuery(pane: WorkTab) {
    try {
      const file = await pickSqlFile();
      if (file) newQuery(pane, file.sql, file.title);
    } catch (e) {
      setError(e instanceof Error ? e.message : "No se pudo leer el archivo");
    }
  }

  function rename(id: string, title: string) {
    const value = title.trim().slice(0, 60);
    setTabs((old) => old.map((t) => (t.id === id ? { ...t, customTitle: value || undefined } : t)));
  }

  async function remove(ids: string[]) {
    if (
      tabs.some((t) => ids.includes(t.id) && t.dirty) &&
      !(await confirm("Esta pestaña tiene cambios sin guardar. ¿Cerrarla y descartar los borradores?"))
    )
      return;
    setTabs(tabs.filter((t) => !ids.includes(t.id)));
    setVisited((old) => old.filter((id) => !ids.includes(id)));
    for (const id of ids) delete seeds.current[id];
    if (ids.includes(active)) {
      const i = tabs.findIndex((t) => t.id === active);
      const neighbors = [...tabs.slice(0, i).reverse(), ...tabs.slice(i + 1)].filter((t) => !ids.includes(t.id));
      activate((neighbors.find((t) => t.groupId === current?.groupId) || neighbors[0])?.id || "home");
    }
  }

  async function closeGroup(groupId: string) {
    const children = tabs.filter((t) => t.groupId === groupId);
    const index = groups.findIndex((g) => g.groupId === groupId);
    const neighbor = groups[index - 1] || groups[index + 1];
    if (
      children.some((t) => t.dirty) &&
      !(await confirm("Esta pestaña tiene cambios sin guardar. ¿Cerrarla y descartar los borradores de sus vistas?"))
    )
      return;
    const ids = children.map((t) => t.id);
    setTabs((old) => old.filter((t) => t.groupId !== groupId));
    setVisited((old) => old.filter((id) => !ids.includes(id)));
    for (const id of ids) delete seeds.current[id];
    delete lastPane.current[groupId];
    if (current?.groupId === groupId) {
      const target =
        neighbor && (tabs.some((t) => t.id === lastPane.current[neighbor.groupId]) ? lastPane.current[neighbor.groupId] : neighbor.id);
      activate(target || "home");
    }
  }

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const dialogOpen = [...document.querySelectorAll<HTMLElement>('[role="dialog"]')].some((el) => el.getClientRects().length);
      if (!palette && dialogOpen) return;
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPalette((p) => !p);
        setSearch("");
      }
      if ((e.ctrlKey || e.metaKey) && e.altKey && e.key.toLowerCase() === "n") {
        e.preventDefault();
        newQuery(current);
      }
    };
    const onUnload = (e: BeforeUnloadEvent) => {
      if (tabs.some((t) => t.dirty)) e.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    window.addEventListener("beforeunload", onUnload);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("beforeunload", onUnload);
    };
  });

  const queryLabel = (child: WorkTab, n: number) => child.customTitle || `Consulta ${n}`;

  function navigation(pane: WorkTab, c: Connection) {
    if (!pane.groupTable) return undefined;
    const siblings = tabs.filter((t) => t.groupId === pane.groupId);
    const fixed: WorkspaceView[] = [
      "data",
      "structure",
      ...(!c.readOnly && !preferences.viewOnly ? (["modify", "insert"] as WorkspaceView[]) : []),
    ];
    if ((pane.view === "modify" || pane.view === "insert") && !fixed.includes(pane.view)) fixed.push(pane.view);
    const items: TabItem[] = fixed.map((view) => {
      const target = siblings.find((t) => t.view === view);
      return {
        id: view,
        label: VIEW_LABELS[view]!,
        control: target ? `panel-${target.id}` : undefined,
        dirty: target?.dirty,
        onSelect: () => (target ? activate(target.id) : open(c, pane.groupDatabase, pane.groupTable, view, undefined, pane)),
      };
    });
    let n = 0;
    for (const child of siblings.filter((t) => !fixed.includes(t.view))) {
      const label = child.view === "sql" ? queryLabel(child, ++n) : VIEW_LABELS[child.view] || child.title;
      items.push({
        id: child.id,
        label,
        control: `panel-${child.id}`,
        dirty: child.dirty,
        onSelect: () => activate(child.id),
        onClose: () => remove([child.id]),
        onRename: child.view === "sql" ? () => setRenaming({ id: child.id, value: label }) : undefined,
      });
    }
    return (
      <TabStrip
        label="Vistas de la tabla"
        className="table-tab-strip"
        idPrefix={`view-${pane.id}`}
        active={fixed.includes(pane.view) ? pane.view : pane.id}
        items={items}
        inlineActions={
          <ActionMenu
            label={`Nueva consulta en ${pane.groupTable}`}
            title="Nueva consulta · Ctrl+Alt+N"
            trigger="＋"
            triggerClass="tab-add"
            actions={[
              { label: "Nueva consulta SQL", onClick: () => newQuery(pane, `SELECT * FROM \`${pane.groupTable}\` LIMIT 50;`) },
              { label: "Abrir script .sql…", onClick: () => void importQuery(pane) },
            ]}
          />
        }
      />
    );
  }

  // Query tabs in a table group are numbered in order; their name in the console follows the tab.
  function paneTitle(pane: WorkTab) {
    if (pane.view !== "sql") return undefined;
    const queries = tabs.filter((t) => t.groupId === pane.groupId && t.view === "sql");
    return queryLabel(pane, queries.indexOf(pane) + 1);
  }

  const mainItems: TabItem[] = [
    { id: "home", label: "Conexiones", leading: "⌂", control: "panel-home", onSelect: () => activate("home") },
    ...groups.map((g) => {
      const c = connections.find((x) => x.id === g.connectionId);
      const child = tabs.find((t) => t.id === lastPane.current[g.groupId]) || g;
      const standaloneQuery = !g.groupTable && g.view === "sql";
      return {
        id: g.groupId,
        label: g.groupTable || (standaloneQuery ? g.customTitle || g.title : g.title),
        subtitle: c?.name,
        title: `${c?.name || "Conexión"} · ${g.groupDatabase}${g.groupTable ? " · " + g.groupTable : ""}`,
        leading: <span className={`tab-env env-${c?.env || "local"}`} />,
        control: `panel-${child.id}`,
        dirty: tabs.some((t) => t.groupId === g.groupId && t.dirty),
        onSelect: () => activate(child.id),
        onClose: () => closeGroup(g.groupId),
        onRename: standaloneQuery ? () => setRenaming({ id: g.id, value: g.customTitle || g.title }) : undefined,
      };
    }),
  ];

  const query = search.toLowerCase();
  const showMfaHint = !mfa && session.mfaPolicy !== "off";

  return (
    <div className="workbench">
      <TabStrip
        label="Espacios de trabajo"
        className="main-tab-strip"
        active={current?.groupId || "home"}
        items={mainItems}
        actions={
          <>
            <button
              className={preferences.viewOnly ? "settings-button protected" : "settings-button"}
              aria-label="Preferencias del espacio"
              onClick={() => setSettings(true)}
            >
              ⚙{preferences.viewOnly && <span className="settings-lock">●</span>}
            </button>
            <button
              onClick={() => {
                setPalette(true);
                setSearch("");
              }}
              title="Buscar y abrir · Ctrl+K"
            >
              ⌕ <span className="shortcut-hint">Ctrl K</span>
            </button>
          </>
        }
      />
      {error && (
        <div className="workbench-error" role="alert">
          {error}
          <button onClick={() => setError("")}>Cerrar</button>
        </div>
      )}

      <div id="panel-home" role="tabpanel" aria-labelledby="tab-home" className="workbench-panel" hidden={active !== "home"}>
        <header className="topbar">
          <strong className="brand">
            <Brand />
          </strong>
          <span className="spacer" />
          <span className="muted">{session.username}</span>
          <button onClick={() => setAudit(true)}>Auditoría</button>
          <button onClick={() => setSecurity(true)}>
            Seguridad
            {showMfaHint && (
              <span className="settings-lock" aria-label="2FA pendiente">
                ●
              </span>
            )}
          </button>
          <button onClick={onLogout}>Salir</button>
        </header>
        {showMfaHint && (
          <div className="mfa-banner" role="status">
            <span>
              {session.mfaPolicy === "all"
                ? "Este servidor exige verificación en dos pasos para trabajar."
                : "Activa la verificación en dos pasos para usar conexiones de producción y túneles SSH."}
            </span>
            <button className="primary" onClick={() => setSecurity(true)}>
              Activar 2FA
            </button>
          </div>
        )}
        <main className="content home-content">
          <Connections key={newConnection} storageKey={storageKey} startNew={newConnection > 0} onSelect={(c) => open(c)} />
        </main>
      </div>

      {tabs
        .filter((t) => visited.includes(t.id))
        .map((pane) => {
          const c = connections.find((x) => x.id === pane.connectionId);
          return (
            <div
              id={`panel-${pane.id}`}
              role="tabpanel"
              aria-labelledby={`tab-${pane.groupId}`}
              key={pane.id}
              className="workbench-panel"
              hidden={active !== pane.id}
            >
              {c ? (
                <Workspace
                  connection={c}
                  username={session.username}
                  onLogout={onLogout}
                  onBack={() => activate("home")}
                  initialDatabase={pane.database}
                  initialTable={pane.table}
                  initialView={pane.view}
                  initialSQL={seeds.current[pane.id]}
                  title={paneTitle(pane)}
                  onRename={pane.view === "sql" ? (title) => rename(pane.id, title) : undefined}
                  isActive={active === pane.id}
                  viewNavigation={navigation(pane, c)}
                  onOpenTable={(db, table) => open(c, db, table, "data")}
                  onOpenView={(db, table, view) =>
                    open(
                      c,
                      db,
                      table,
                      view,
                      undefined,
                      pane.groupTable && table === pane.groupTable && db === pane.groupDatabase ? pane : undefined,
                    )
                  }
                  onCloseTab={() => remove([pane.id])}
                  onNewSQL={(db, seed) => open(c, db, pane.groupTable, "sql", seed, pane.groupTable ? pane : undefined)}
                  onContextChange={(ctx) =>
                    setTabs((old) =>
                      old.map((t) =>
                        t.id === pane.id
                          ? {
                              ...t,
                              database: ctx.database,
                              table: ctx.table,
                              view: ctx.view,
                              sql: ctx.sql,
                              dirty: ctx.dirty,
                              title: ctx.sql ? "Consulta SQL" : ctx.table || ctx.database || c.name,
                            }
                          : t,
                      ),
                    )
                  }
                />
              ) : (
                <div className="content">
                  <p>Este perfil ya no está disponible.</p>
                  <button onClick={() => activate("home")}>Ir a conexiones</button>
                  <button onClick={() => remove([pane.id])}>Cerrar pestaña</button>
                </div>
              )}
            </div>
          );
        })}

      {security && (
        <SecurityDialog
          username={session.username}
          onClose={() => setSecurity(false)}
          onChanged={(verified) => {
            setMfa(verified);
            if (verified) void refresh();
          }}
        />
      )}
      {audit && <AuditDialog connections={connections} onClose={() => setAudit(false)} />}
      {settings && (
        <PreferencesDialog
          onClose={() => setSettings(false)}
          onHistory={() => {
            setSettings(false);
            setHistory(true);
          }}
        />
      )}
      {history && (
        <QueryHistoryDialog
          onClose={() => setHistory(false)}
          onOpen={(entry) => {
            const c = connections.find((x) => x.id === entry.path.split("/")[3]);
            if (!c) return setError("El perfil de esta consulta ya no está disponible.");
            const db = entry.database ?? c.database;
            const parent = current?.groupTable && current.connectionId === c.id && current.groupDatabase === db ? current : undefined;
            open(c, db, parent?.groupTable || "", "sql", entry.sql, parent);
            setHistory(false);
          }}
        />
      )}
      {renaming && (
        <Dialog title="Renombrar consulta" onClose={() => setRenaming(null)}>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              rename(renaming.id, renaming.value);
              setRenaming(null);
            }}
          >
            <label>
              Nombre
              <input
                autoFocus
                maxLength={60}
                value={renaming.value}
                onChange={(e) => setRenaming({ ...renaming, value: e.target.value })}
              />
            </label>
            <div className="actions">
              <button type="button" onClick={() => setRenaming(null)}>
                Cancelar
              </button>
              <button className="primary">Guardar</button>
            </div>
          </form>
        </Dialog>
      )}
      {palette && (
        <Dialog title="Buscar y abrir" onClose={() => setPalette(false)}>
          <label>
            Conexión, pestaña o acción
            <input autoFocus type="search" value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Escribe un nombre…" />
          </label>
          <div className="command-results">
            <button
              onClick={() => {
                activate("home");
                setPalette(false);
              }}
            >
              ⌂ Ir a conexiones
            </button>
            <button
              onClick={() => {
                activate("home");
                setNewConnection((n) => n + 1);
                setPalette(false);
              }}
            >
              ＋ Nueva conexión
            </button>
            <button
              onClick={() => {
                setSecurity(true);
                setPalette(false);
              }}
            >
              🔒 Seguridad y verificación en dos pasos
            </button>
            <button
              onClick={() => {
                setAudit(true);
                setPalette(false);
              }}
            >
              ☰ Registro de auditoría
            </button>
            {currentConnection && current && (
              <>
                <button
                  onClick={() => {
                    newQuery(current);
                    setPalette(false);
                  }}
                >
                  ⌘ Nueva consulta SQL <small>Ctrl+Alt+N</small>
                </button>
                <button
                  onClick={() => {
                    setPalette(false);
                    void importQuery(current);
                  }}
                >
                  📂 Abrir script .sql
                </button>
              </>
            )}
            {tabs
              .filter((t) => `${t.customTitle ?? ""} ${t.title} ${t.database} ${VIEW_LABELS[t.view] || ""}`.toLowerCase().includes(query))
              .map((t) => (
                <button
                  key={t.id}
                  onClick={() => {
                    activate(t.id);
                    setPalette(false);
                  }}
                >
                  Pestaña · {t.customTitle || t.groupTable || t.title}
                  <small>{VIEW_LABELS[t.view] || connections.find((x) => x.id === t.connectionId)?.name}</small>
                </button>
              ))}
            {connections
              .filter((c) => `${c.name} ${c.host}`.toLowerCase().includes(query))
              .map((c) => (
                <button
                  key={c.id}
                  onClick={() => {
                    open(c);
                    setPalette(false);
                  }}
                >
                  <span className={`badge env-${c.env}`}>{c.env}</span>
                  {c.name}
                  <small>
                    {c.host}
                    {c.ssh?.enabled ? " · SSH" : ""}
                  </small>
                </button>
              ))}
          </div>
        </Dialog>
      )}
    </div>
  );
}
