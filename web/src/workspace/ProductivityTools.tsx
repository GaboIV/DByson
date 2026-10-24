import { useEffect, useRef, useState } from "react";
import { api, type QueryResponse, type QueryResult, type TableInfo } from "../api";
import { Dialog } from "../Dialog";
import { cellText } from "../clipboard";
import { enc, errorText, type ViewContext } from "./shared";
import { ident, parseCSV, type ForeignKey } from "./productivity";
import { invalidateConnection } from "./catalog";

type Tool = "import" | "processes" | "accounts" | "diagram";
export function ProductivityTools({ ctx, onClose }: { ctx: ViewContext; onClose: () => void }) {
  const [tool, setTool] = useState<Tool>("import");
  return (
    <Dialog title="Herramientas de productividad" onClose={onClose} className="productivity-dialog">
      <nav className="segmented-control" aria-label="Herramientas">
        {(
          [
            ["import", "Importar"],
            ["processes", "Procesos"],
            ["accounts", "Usuarios MySQL"],
            ["diagram", "Diagrama ER"],
          ] as [Tool, string][]
        ).map(([id, label]) => (
          <button key={id} aria-pressed={tool === id} onClick={() => setTool(id)}>
            {label}
          </button>
        ))}
      </nav>
      <div className="productivity-tool-body" key={tool}>
        {tool === "import" ? (
          <ImportData ctx={ctx} onClose={onClose} />
        ) : tool === "processes" ? (
          <Processes ctx={ctx} />
        ) : tool === "accounts" ? (
          <Accounts ctx={ctx} />
        ) : (
          <Diagram ctx={ctx} />
        )}
      </div>
    </Dialog>
  );
}

function ImportData({ ctx, onClose }: { ctx: ViewContext; onClose: () => void }) {
  const [target, setTarget] = useState(ctx.table),
    [text, setText] = useState(""),
    [kind, setKind] = useState("csv"),
    [delimiter, setDelimiter] = useState(","),
    [nullToken, setNullToken] = useState("\\N"),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [done, setDone] = useState("");
  const [tables, setTables] = useState<TableInfo[]>([]);
  const fileGeneration = useRef(0);
  useEffect(() => {
    if (ctx.database)
      void api<{ tables: TableInfo[] }>("GET", `/api/connections/${ctx.connection.id}/databases/${enc(ctx.database)}/tables`)
        .then((r) => setTables(r.tables))
        .catch((e) => setError(errorText(e, "No se pudieron leer las tablas")));
  }, [ctx.database, ctx.connection.id]);
  let parsed: string[][] = [];
  let parseError = "";
  if (text && kind === "csv")
    try {
      parsed = parseCSV(text, delimiter);
    } catch (e) {
      parseError = errorText(e, "CSV inválido");
    }
  async function execute() {
    setBusy(true);
    setError("");
    setDone("");
    try {
      const result = await ctx.confirmedApi<{ inserted: number }>(
        "POST",
        `/api/connections/${ctx.connection.id}/databases/${enc(ctx.database)}/tables/${enc(target)}/import-csv`,
        { columns: parsed[0], rows: parsed.slice(1).map((row) => row.map((value) => (value === nullToken ? null : value))) },
      );
      if (result) {
        setDone(`${result.inserted} filas importadas. La transacción se completó.`);
        setText("");
        invalidateConnection(ctx.connection.id);
      }
    } catch (e) {
      setError(errorText(e, "No se pudo importar"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section>
      <h4>Importar datos</h4>
      <p className="muted">
        CSV: hasta 5000 filas y 512 KB, cabecera con nombres exactos, transacción InnoDB. SQL: abre un borrador para revisar y ejecutar; el
        script puede completar parcialmente si una sentencia falla.
      </p>
      <div className="productivity-grid">
        <label>
          Formato
          <select
            value={kind}
            disabled={busy}
            onChange={(e) => {
              fileGeneration.current++;
              setKind(e.target.value);
              setText("");
              setError("");
            }}
          >
            <option value="csv">CSV</option>
            <option value="sql">Script SQL</option>
          </select>
        </label>
        {kind === "csv" && (
          <>
            <label>
              Tabla de destino
              <select value={target} disabled={busy} onChange={(e) => setTarget(e.target.value)}>
                <option value="">Selecciona una tabla</option>
                {tables
                  .filter((t) => t.type !== "VIEW")
                  .map((t) => (
                    <option key={t.name}>{t.name}</option>
                  ))}
              </select>
            </label>
            <label>
              Separador
              <select value={delimiter} disabled={busy} onChange={(e) => setDelimiter(e.target.value)}>
                <option value=",">Coma</option>
                <option value=";">Punto y coma</option>
                <option value={"\t"}>Tabulador</option>
              </select>
            </label>
            <label>
              Valor que representa NULL
              <input value={nullToken} disabled={busy} onChange={(e) => setNullToken(e.target.value)} />
            </label>
          </>
        )}
      </div>
      <label className="file-drop">
        Archivo {kind.toUpperCase()}
        <input
          type="file"
          disabled={busy}
          accept={kind === "csv" ? ".csv,text/csv" : ".sql,text/plain"}
          onChange={async (e) => {
            const generation = ++fileGeneration.current;
            const f = e.target.files?.[0];
            setError("");
            setText("");
            if (!f) return;
            if (f.size > 512000) {
              setError("El archivo supera 512 KB.");
              return;
            }
            try {
              const content = new TextDecoder("utf-8", { fatal: true }).decode(await f.arrayBuffer());
              if (generation === fileGeneration.current) setText(content);
            } catch {
              if (generation === fileGeneration.current) setError("No se pudo leer el archivo. Usa codificación UTF-8.");
            }
          }}
        />
      </label>
      {parseError && (
        <p role="alert" className="error">
          {parseError}
        </p>
      )}
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      {done && (
        <p role="status" className="okmsg">
          {done}
        </p>
      )}
      {parsed.length > 0 && (
        <>
          <p>
            {parsed.length - 1} filas · {parsed[0].length} columnas · Vista previa de las primeras 5
          </p>
          <div className="tool-table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  {parsed[0].map((c, i) => (
                    <th key={i}>{c}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {parsed.slice(1, 6).map((row, i) => (
                  <tr key={i}>
                    {row.map((v, j) => (
                      <td key={j}>{v === nullToken ? <em>NULL</em> : v}</td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
      {kind === "sql" && text && <pre className="import-preview">{text.slice(0, 8000)}</pre>}
      <div className="actions">
        <button
          className="primary"
          disabled={busy || !text || !!parseError || (kind === "csv" && (ctx.readOnly || !target || !ctx.database || !parsed.length))}
          onClick={() => {
            if (kind === "sql") {
              ctx.newSQL(text);
              onClose();
            } else void execute();
          }}
        >
          {busy ? "Importando…" : kind === "sql" ? "Revisar script en editor SQL" : "Importar CSV"}
        </button>
      </div>
      {ctx.readOnly && <p className="muted">Solo lectura: la importación CSV está bloqueada.</p>}
    </section>
  );
}

function Processes({ ctx }: { ctx: ViewContext }) {
  const [result, setResult] = useState<QueryResult | null>(null),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  async function load() {
    setBusy(true);
    setError("");
    try {
      const res = await api<QueryResponse>("POST", `/api/connections/${ctx.connection.id}/query`, {
        query: "SHOW FULL PROCESSLIST",
        database: "",
      });
      if (!res.ok) throw new Error(res.error);
      setResult(res.result ?? null);
    } catch (e) {
      setError(errorText(e, "No se pudieron leer los procesos"));
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    void load();
  }, [ctx.connection.id]);
  async function kill(row: QueryResult["rows"][number]) {
    setBusy(true);
    setError("");
    try {
      const thread = cellText(row[result!.columns.findIndex((c) => c.toLowerCase() === "id")]);
      if (await ctx.confirmedApi("POST", `/api/connections/${ctx.connection.id}/processes/kill`, { thread })) await load();
    } catch (e) {
      setError(errorText(e, "No se pudo detener la consulta"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section>
      <div className="productivity-toolbar">
        <h4>Procesos MySQL</h4>
        <button disabled={busy} onClick={() => void load()}>
          Actualizar procesos
        </button>
      </div>
      <p className="muted">
        Sin PROCESS verás solo tus procesos. Detener envía KILL QUERY y no desconecta al cliente. Los identificadores de MySQL pueden
        reutilizarse; actualiza antes de actuar.
      </p>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {result && (
        <div className="tool-table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                {result.columns.map((c) => (
                  <th key={c}>{c}</th>
                ))}
                <th>Acción</th>
              </tr>
            </thead>
            <tbody>
              {result.rows.map((row, i) => (
                <tr key={i}>
                  {row.map((v, j) => (
                    <td key={j}>{cellText(v)}</td>
                  ))}
                  <td>
                    <button disabled={ctx.readOnly || busy} onClick={() => void kill(row)}>
                      Detener consulta
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function Accounts({ ctx }: { ctx: ViewContext }) {
  const [list, setList] = useState<QueryResult | null>(null),
    [grants, setGrants] = useState<QueryResult | null>(null),
    [user, setUser] = useState(""),
    [host, setHost] = useState("localhost"),
    [password, setPassword] = useState(""),
    [database, setDatabase] = useState(ctx.database),
    [table, setTable] = useState(""),
    [privileges, setPrivileges] = useState(["SELECT"]),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [notice, setNotice] = useState("");
  const path = `/api/connections/${ctx.connection.id}/mysql-accounts`;
  async function action(action: string) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const body = { action, user, host, ...(action === "create" ? { password } : {}), database, table, privileges };
      if (action === "list" || action === "grants") {
        const r = await api<QueryResult>("POST", path, body);
        if (action === "list") setList(r);
        else setGrants(r);
      } else if (await ctx.confirmedApi("POST", path, body)) {
        setPassword("");
        setGrants(null);
        setNotice("Cambio aplicado en MySQL.");
      }
    } catch (e) {
      setError(errorText(e, "No se pudo gestionar la cuenta"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section>
      <h4>Cuentas y permisos de MySQL</h4>
      <p className="muted">
        Cuentas del servidor de bases de datos. Requiere administrador de DByson y privilegios MySQL adecuados. No concede GRANT OPTION ni
        privilegios globales.
      </p>
      <button disabled={busy} onClick={() => void action("list")}>
        Listar cuentas MySQL
      </button>
      {list && (
        <div className="account-list">
          {list.rows.map((r, i) => (
            <button
              key={i}
              onClick={() => {
                setUser(cellText(r[0]));
                setHost(cellText(r[1]));
                setGrants(null);
              }}
            >
              {cellText(r[0])}@{cellText(r[1])}
            </button>
          ))}
        </div>
      )}
      <div className="productivity-grid">
        <label>
          Usuario MySQL
          <input value={user} onChange={(e) => setUser(e.target.value)} />
        </label>
        <label>
          Host permitido
          <input value={host} onChange={(e) => setHost(e.target.value)} />
        </label>
        <label>
          Contraseña para nueva cuenta
          <input type="password" value={password} autoComplete="new-password" onChange={(e) => setPassword(e.target.value)} />
        </label>
        <label>
          Base exacta
          <input value={database} onChange={(e) => setDatabase(e.target.value)} />
        </label>
        <label>
          Tabla (vacío = todas en la base)
          <input value={table} onChange={(e) => setTable(e.target.value)} />
        </label>
      </div>
      <div className="privilege-options">
        {["SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "ALTER", "INDEX", "DROP"].map((p) => (
          <label key={p}>
            <input
              type="checkbox"
              checked={privileges.includes(p)}
              onChange={(e) => setPrivileges((old) => (e.target.checked ? [...old, p] : old.filter((x) => x !== p)))}
            />
            {p}
          </label>
        ))}
      </div>
      <div className="actions">
        <button disabled={busy || !user || !host} onClick={() => void action("grants")}>
          Ver permisos
        </button>
        <button disabled={busy || ctx.readOnly || !user || password.length < 12} onClick={() => void action("create")}>
          Crear cuenta
        </button>
        <button disabled={busy || ctx.readOnly || !user || !database || !privileges.length} onClick={() => void action("grant")}>
          Conceder permisos
        </button>
        <button disabled={busy || ctx.readOnly || !user || !database || !privileges.length} onClick={() => void action("revoke")}>
          Revocar permisos
        </button>
        <button className="danger" disabled={busy || ctx.readOnly || !user} onClick={() => void action("drop")}>
          Eliminar cuenta
        </button>
      </div>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {notice && (
        <p className="okmsg" role="status">
          {notice}
        </p>
      )}
      {grants && <pre className="import-preview">{grants.rows.map((row) => row.map(cellText).join(" ")).join("\n")}</pre>}
    </section>
  );
}

function Diagram({ ctx }: { ctx: ViewContext }) {
  const [keys, setKeys] = useState<ForeignKey[]>([]),
    [tables, setTables] = useState<TableInfo[]>([]),
    [error, setError] = useState(""),
    [search, setSearch] = useState("");
  useEffect(() => {
    const c = new AbortController();
    if (ctx.database)
      void Promise.all([
        api<ForeignKey[]>("GET", `/api/connections/${ctx.connection.id}/databases/${enc(ctx.database)}/relations`, undefined, {
          signal: c.signal,
        }),
        api<{ tables: TableInfo[] }>("GET", `/api/connections/${ctx.connection.id}/databases/${enc(ctx.database)}/tables`, undefined, {
          signal: c.signal,
        }),
      ])
        .then(([keys, tables]) => {
          setKeys(keys);
          setTables(tables.tables);
        })
        .catch((e) => {
          if (!c.signal.aborted) setError(errorText(e, "No se pudieron leer las relaciones"));
        });
    return () => c.abort();
  }, [ctx.database, ctx.connection.id]);
  const names = tables
    .filter((t) => t.name.toLowerCase().includes(search.toLowerCase()))
    .slice(0, 100)
    .map((t) => t.name);
  const width = names.length > 1 ? 840 : 420,
    height = Math.max(180, Math.ceil(names.length / 2) * 150);
  const pos = (name: string) => {
    const i = names.indexOf(name);
    return { x: (i % 2) * 420 + 10, y: Math.floor(i / 2) * 150 + 10 };
  };
  return (
    <section>
      <h4>Relaciones · {ctx.database || "Selecciona una base"}</h4>
      <p className="muted">
        Claves foráneas declaradas, incluidas claves compuestas. Abre una tabla desde su tarjeta. Se muestran hasta 100 tablas; filtra para
        explorar bases grandes.
      </p>
      <input
        type="search"
        aria-label="Filtrar diagrama"
        placeholder="Filtrar tablas del diagrama…"
        value={search}
        onChange={(e) => setSearch(e.target.value)}
      />
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <div className="er-canvas" style={{ height: Math.min(560, height + 20) }}>
        <div style={{ width, height, position: "relative" }}>
          <svg width={width} height={height} role="img" aria-label="Líneas de claves foráneas">
            <defs>
              <marker id="er-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
                <path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" />
              </marker>
            </defs>
            {keys
              .filter((k) => k.referencedDatabase === ctx.database && names.includes(k.table) && names.includes(k.referencedTable))
              .map((k) => {
                const a = pos(k.table),
                  b = pos(k.referencedTable);
                return (
                  <path
                    key={k.table + k.name}
                    d={`M ${a.x + 180} ${a.y + 105} C ${a.x + 390} ${a.y + 135}, ${b.x + 390} ${b.y + 135}, ${b.x + 180} ${b.y + 105}`}
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                    markerEnd="url(#er-arrow)"
                  >
                    <title>
                      {k.name}: {k.columns.join(", ")} → {k.referencedTable} ({k.referencedColumns.join(", ")})
                    </title>
                  </path>
                );
              })}
          </svg>
          {names.map((name) => {
            const p = pos(name);
            return (
              <article className="er-table" key={name} style={{ left: p.x, top: p.y }}>
                <button onClick={() => ctx.newSQL(`SELECT * FROM ${ident(ctx.database)}.${ident(name)} LIMIT 50;`)}>{name}</button>
                <small>
                  {keys
                    .filter((k) => k.table === name)
                    .map(
                      (k) => `${k.columns.join(" + ")} → ${k.referencedDatabase}.${k.referencedTable} (${k.referencedColumns.join(" + ")})`,
                    )
                    .join("\n") || "Sin referencias salientes"}
                </small>
              </article>
            );
          })}
        </div>
      </div>
      <details>
        <summary>Lista accesible de relaciones ({keys.length})</summary>
        {keys.map((k) => (
          <p key={k.table + k.name}>
            <strong>{k.table}</strong> ({k.columns.join(", ")}) →{" "}
            <strong>
              {k.referencedDatabase}.{k.referencedTable}
            </strong>{" "}
            ({k.referencedColumns.join(", ")})
          </p>
        ))}
      </details>
    </section>
  );
}
