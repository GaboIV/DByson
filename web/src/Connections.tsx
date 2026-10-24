import { useCallback, useEffect, useState } from "react";
import { api, ApiError, type Connection, type TestResponse } from "./api";
import { ConnectionForm } from "./ConnectionForm";
import { ConnectionTransfer } from "./ConnectionTransfer";
import { useConfirm } from "./Confirm";
interface ConnectionsProps {
  onSelect?: (conn: Connection) => void;
  storageKey?: string;
  startNew?: boolean;
}
export function Connections({ onSelect, storageKey = "dbyson", startNew = false }: ConnectionsProps) {
  const confirm = useConfirm();
  const [items, setItems] = useState<Connection[] | null>(null),
    [editing, setEditing] = useState<Connection | "new" | null>(startNew ? "new" : null),
    [status, setStatus] = useState<Record<string, string>>({}),
    [error, setError] = useState("");
  const [search, setSearch] = useState(""),
    [filter, setFilter] = useState("all"),
    [selected, setSelected] = useState<string[]>([]),
    [transfer, setTransfer] = useState<"import" | "export" | null>(null);
  const [favorites, setFavorites] = useState<string[]>(() => {
    try {
      return JSON.parse(localStorage.getItem(storageKey + ":favorites") || "[]");
    } catch {
      return [];
    }
  });
  const load = useCallback(async () => {
    try {
      setItems(await api<Connection[]>("GET", "/api/connections"));
      setError("");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "No se pudieron cargar las conexiones");
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  function favorite(id: string) {
    setFavorites((old) => {
      const next = old.includes(id) ? old.filter((x) => x !== id) : [...old, id];
      try {
        localStorage.setItem(storageKey + ":favorites", JSON.stringify(next));
      } catch {}
      return next;
    });
  }
  async function quickTest(c: Connection) {
    setStatus((s) => ({ ...s, [c.id]: "Probando…" }));
    try {
      const r = await api<TestResponse>("POST", `/api/connections/${c.id}/test`);
      setStatus((s) => ({
        ...s,
        [c.id]:
          r.ok && r.result
            ? `✔ ${r.result.serverVersion} · ${r.result.latencyMs} ms${c.ssh?.enabled ? " · SSH" : ""}${r.result.tls ? " · TLS" : ""}`
            : `✖ ${r.error}`,
      }));
    } catch (e) {
      setStatus((s) => ({ ...s, [c.id]: e instanceof Error ? e.message : "Error al comprobar" }));
    }
  }
  async function remove(c: Connection) {
    if (!(await confirm(`¿Eliminar el perfil «${c.name}»?`))) return;
    try {
      await api("DELETE", `/api/connections/${c.id}`);
      setSelected((s) => s.filter((id) => id !== c.id));
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "No se pudo eliminar");
    }
  }
  if (editing)
    return (
      <ConnectionForm
        existing={editing === "new" ? undefined : editing}
        onCancel={() => setEditing(null)}
        onSaved={() => {
          setEditing(null);
          void load();
        }}
      />
    );
  const visible = (items || [])
    .filter(
      (c) =>
        `${c.name} ${c.host} ${c.username} ${c.database} ${c.ssh?.host || ""}`.toLowerCase().includes(search.toLowerCase()) &&
        (filter === "all" ||
          filter === c.env ||
          (filter === "ssh" && c.ssh?.enabled) ||
          (filter === "readonly" && c.readOnly) ||
          (filter === "favorites" && favorites.includes(c.id))),
    )
    .sort((a, b) => Number(favorites.includes(b.id)) - Number(favorites.includes(a.id)));
  return (
    <section className="connections-home">
      <div className="home-intro">
        <div>
          <h1>Conexiones</h1>
          <div className="home-metrics" aria-label="Resumen de conexiones">
            <span>
              <strong>{items?.length || 0}</strong> {items?.length === 1 ? "conexión" : "conexiones"}
            </span>
            <span>
              <strong>{items?.filter((c) => c.ssh?.enabled).length || 0}</strong> con SSH
            </span>
            <span>
              <strong>{items?.filter((c) => c.readOnly).length || 0}</strong> de solo lectura
            </span>
          </div>
        </div>
        <div className="actions">
          <button onClick={() => setTransfer("import")}>↑ Importar perfiles</button>
          <button className="primary" onClick={() => setEditing("new")}>
            ＋ Nueva conexión
          </button>
        </div>
      </div>
      <div className="connections-toolbar">
        <label className="connection-search">
          <span className="sr-only">Buscar conexiones</span>
          <input
            type="search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Buscar por nombre, host, usuario o base…"
          />
        </label>
        <select aria-label="Filtrar conexiones" value={filter} onChange={(e) => setFilter(e.target.value)}>
          {[
            ["all", "Todos los entornos"],
            ["favorites", "Favoritas"],
            ["local", "Local"],
            ["dev", "Desarrollo"],
            ["staging", "Staging"],
            ["prod", "Producción"],
            ["ssh", "Con SSH"],
            ["readonly", "Solo lectura"],
          ].map(([v, label]) => (
            <option key={v} value={v}>
              {label}
            </option>
          ))}
        </select>
        <button
          disabled={!items?.length}
          onClick={() => {
            setSelected((items || []).slice(0, 25).map((c) => c.id));
            setTransfer("export");
          }}
        >
          ↓ Compartir perfiles
        </button>
      </div>
      {selected.length > 0 && (
        <div className="batch-toolbar">
          <span>{selected.length} seleccionadas</span>
          <button onClick={() => setTransfer("export")}>Compartir selección</button>
          <button onClick={() => setSelected([])}>Limpiar selección</button>
        </div>
      )}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {items === null && !error && (
        <p className="muted" role="status">
          Cargando conexiones…
        </p>
      )}
      {items?.length === 0 && (
        <div className="card onboarding">
          <strong>Tu primera conexión, en cuatro pasos</strong>
          <p>Elige un destino local, Docker o SSH. Comprueba su identidad, prueba el acceso y guarda el perfil.</p>
          <button className="primary" onClick={() => setEditing("new")}>
            Crear mi primera conexión
          </button>
          <small className="muted">¿Ya tienes un archivo de un compañero? Usa Importar perfiles.</small>
        </div>
      )}
      {items && items.length > 0 && visible.length === 0 && <p className="muted">No hay conexiones que coincidan con esta búsqueda.</p>}
      <div className="cards">
        {visible.map((c) => (
          <article key={c.id} className={`card conn env-${c.env}`}>
            <div className="conn-card-tools">
              <label className="check">
                <input
                  type="checkbox"
                  aria-label={`Seleccionar ${c.name}`}
                  checked={selected.includes(c.id)}
                  onChange={(e) => setSelected((s) => (e.target.checked ? [...s, c.id].slice(0, 25) : s.filter((id) => id !== c.id)))}
                />
                <span className={`badge env-${c.env}`}>{c.env}</span>
              </label>
              <button
                className="favorite-button"
                aria-label={`${favorites.includes(c.id) ? "Quitar de" : "Añadir a"} favoritas: ${c.name}`}
                aria-pressed={favorites.includes(c.id)}
                onClick={() => favorite(c.id)}
              >
                {favorites.includes(c.id) ? "★" : "☆"}
              </button>
            </div>
            <button className="connection-open" onClick={() => onSelect?.(c)}>
              <h3>{c.name}</h3>
              <span className="muted mono">
                {c.username}@{c.host}:{c.port}
              </span>
              {c.database && <span className="muted">{c.database}</span>}
            </button>
            <div className="connection-tags">
              <span>{c.ssh?.enabled ? "↗ Túnel SSH" : "↔ Directa"}</span>
              <span>{c.readOnly ? "Solo lectura" : "Lectura / escritura"}</span>
              <span>{c.tlsMode === "disabled" ? "Sin TLS MySQL" : "TLS MySQL"}</span>
            </div>
            {c.ssh?.enabled && (
              <p className="muted mono">
                vía {c.ssh.user}@{c.ssh.host}
                {c.ssh.auth === "key" && !c.ssh.hasPrivateKey && !c.ssh.keyFile ? " · adjunta la clave SSH" : ""}
              </p>
            )}
            {status[c.id] && (
              <p className="status-line" role="status">
                {status[c.id]}
              </p>
            )}
            <footer>
              <button className="primary" onClick={() => onSelect?.(c)}>
                Conectar
              </button>
              <button disabled={status[c.id] === "Probando…"} onClick={() => void quickTest(c)}>
                Probar
              </button>
              <button onClick={() => setEditing(c)}>Editar</button>
              <details className="connection-menu">
                <summary aria-label={`Más acciones de ${c.name}`}>•••</summary>
                <div>
                  <button
                    onClick={() => {
                      setSelected([c.id]);
                      setTransfer("export");
                    }}
                  >
                    Compartir
                  </button>
                  <button className="danger" onClick={() => void remove(c)}>
                    Eliminar perfil
                  </button>
                </div>
              </details>
            </footer>
          </article>
        ))}
      </div>
      {transfer && (
        <ConnectionTransfer
          mode={transfer}
          connections={(items || []).filter((c) => selected.includes(c.id))}
          onClose={() => setTransfer(null)}
          onImported={() => void load()}
        />
      )}
    </section>
  );
}
