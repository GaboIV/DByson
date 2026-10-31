import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { api, ApiError, type AuditEntry, type Connection, type MFAStatus, type OIDCStatus } from "./api";
import { Dialog } from "./Dialog";
import { useClipboard } from "./clipboard";
import { SessionsPanel } from "./Sessions";
import { PasskeysPanel } from "./PasskeysPanel";

const errorText = (e: unknown) => (e instanceof ApiError ? e.message : "No se pudo completar la operación");

type Step = "overview" | "password" | "reauth" | "scan" | "codes" | "disable" | "regenerate";

/** Reopens the security dialog after the identity provider sends the browser back. */
export const RESUME_SECURITY_KEY = "dbyson.resume-security";

interface SecurityProps {
  username: string;
  onClose: () => void;
  /** Called after 2FA is turned on or off, with whether the current session is now verified. */
  onChanged: (sessionVerified: boolean, enabled: boolean) => void;
}

export function SecurityDialog({ username, onClose, onChanged }: SecurityProps) {
  const [status, setStatus] = useState<MFAStatus | null>(null);
  const [step, setStep] = useState<Step>("overview");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [setup, setSetup] = useState<{ secret: string; qr: string } | null>(null);
  const [codes, setCodes] = useState<string[]>([]);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [provider, setProvider] = useState("");
  const { copy, copyNotice } = useClipboard();
  const sso = status?.authMethod === "sso";

  const load = useCallback(async () => {
    try {
      setStatus(await api<MFAStatus>("GET", "/api/auth/mfa"));
    } catch (e) {
      setError(errorText(e));
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    if (!sso) return;
    api<OIDCStatus>("GET", "/api/auth/oidc/status")
      .then((s) => setProvider(s.google ? "Google" : s.provider || ""))
      .catch(() => {});
  }, [sso]);

  function go(next: Step) {
    setStep(next);
    setError("");
    setCode("");
    setPassword("");
  }

  async function run(e: FormEvent, action: () => Promise<void>) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await action();
    } catch (err) {
      if (err instanceof ApiError && err.code === "reauth_required") go("reauth");
      else setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }

  const startSetup = (e: FormEvent) =>
    run(e, async () => {
      const res = await api<{ secret: string; qr: string }>("POST", "/api/auth/mfa/setup", sso ? {} : { password });
      setSetup(res);
      go("scan");
    });

  // An SSO account may not know a local password: a recent SSO sign-in confirms it is
  // the user instead, and an older one is sent through the identity provider again.
  function beginSetup(e: FormEvent) {
    if (!sso) go("password");
    else if (!status?.ssoReauthFresh) go("reauth");
    else void startSetup(e);
  }

  function reauthenticate() {
    try {
      sessionStorage.setItem(RESUME_SECURITY_KEY, "1");
    } catch {
      /* the dialog simply won't reopen by itself */
    }
    window.location.href = "/api/auth/oidc/login";
  }

  const enable = (e: FormEvent) =>
    run(e, async () => {
      const res = await api<{ recoveryCodes: string[] }>("POST", "/api/auth/mfa/enable", { code });
      setCodes(res.recoveryCodes);
      setSetup(null);
      setSaved(false);
      go("codes");
      onChanged(true, true);
      await load();
    });

  const disable = (e: FormEvent) =>
    run(e, async () => {
      await api("POST", "/api/auth/mfa/disable", sso ? { code } : { password, code });
      go("overview");
      onChanged(false, false);
      await load();
    });

  const regenerate = (e: FormEvent) =>
    run(e, async () => {
      const res = await api<{ recoveryCodes: string[] }>("POST", "/api/auth/mfa/recovery-codes", { code });
      setCodes(res.recoveryCodes);
      setSaved(false);
      go("codes");
      await load();
    });

  function download() {
    const text = `Códigos de recuperación de DByson (${username})\nCada código sirve una sola vez.\n\n${codes.join("\n")}\n`;
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = `dbyson-recuperacion-${username}.txt`;
    a.click();
    URL.revokeObjectURL(url);
  }

  const policyNote =
    status?.policy === "all"
      ? "Este servidor exige verificación en dos pasos para todo el uso de DByson."
      : status?.policy === "sensitive"
        ? "Este servidor la exige para conexiones de producción y túneles SSH."
        : "Este servidor no la exige, pero se recomienda.";

  const errorLine = error && (
    <p className="error" role="alert">
      {error}
    </p>
  );

  return (
    <Dialog title="Seguridad de la cuenta" onClose={onClose} className="security-dialog">
      {step === "overview" && (
        <div className="settings-section">
          <h4>Verificación en dos pasos</h4>
          {!status && !error && <p className="muted">Cargando…</p>}
          {status && (
            <>
              <p className={status.enabled ? "okmsg" : "warn"}>
                {status.enabled ? `Activa · ${status.recoveryCodesLeft} códigos de recuperación disponibles` : "Desactivada"}
              </p>
              <p className="muted">
                Además de {sso ? `tu inicio de sesión con ${provider || "SSO"}` : "la contraseña"}, se pedirá un código de una app como
                Google Authenticator, 1Password, Bitwarden o Microsoft Authenticator. {policyNote}
              </p>
              {status.enabled && status.recoveryCodesLeft <= 3 && (
                <p className="warn">Te quedan pocos códigos de recuperación. Genera unos nuevos.</p>
              )}
              <div className="actions">
                {status.enabled ? (
                  <>
                    <button onClick={() => go("regenerate")}>Nuevos códigos de recuperación</button>
                    <button className="danger" onClick={() => go(sso && !status.ssoReauthFresh ? "reauth" : "disable")}>
                      Desactivar
                    </button>
                  </>
                ) : (
                  <button className="primary" disabled={busy} onClick={beginSetup}>
                    Activar verificación en dos pasos
                  </button>
                )}
              </div>
            </>
          )}
          {errorLine}
          <SessionsPanel />
          <PasskeysPanel sso={sso ? { provider, fresh: !!status?.ssoReauthFresh, reauthenticate } : undefined} />
        </div>
      )}

      {step === "password" && (
        <form className="settings-section" onSubmit={startSetup}>
          <h4>Confirma tu contraseña</h4>
          <label>
            Contraseña actual
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              required
              autoFocus
            />
          </label>
          {errorLine}
          <div className="actions">
            <button type="button" onClick={() => go("overview")}>
              Cancelar
            </button>
            <button className="primary" disabled={busy}>
              Continuar
            </button>
          </div>
        </form>
      )}

      {step === "reauth" && (
        <div className="settings-section">
          <h4>Confirma que eres tú</h4>
          <p className="muted">
            Entraste con {provider || "SSO"}, así que no hace falta contraseña. Como tu inicio de sesión no es reciente, vuelve a
            confirmarlo con {provider || "tu proveedor"}; al volver, esta ventana se abrirá sola para que continúes.
          </p>
          {errorLine}
          <div className="actions">
            <button type="button" onClick={() => go("overview")}>
              Cancelar
            </button>
            <button className={provider === "Google" ? "primary google-signin" : "primary"} onClick={reauthenticate}>
              Confirmar con {provider || "SSO"}
            </button>
          </div>
        </div>
      )}

      {step === "scan" && setup && (
        <form className="settings-section" onSubmit={enable}>
          <h4>1 · Escanea el código QR</h4>
          <div className="mfa-qr">
            <img src={setup.qr} width={192} height={192} alt="Código QR para tu app de autenticación" />
            <div>
              <p className="muted">¿No puedes escanearlo? Escribe esta clave en tu app:</p>
              <code className="mfa-secret">{setup.secret.replace(/(.{4})/g, "$1 ").trim()}</code>
              <button type="button" onClick={() => void copy(setup.secret, "Clave copiada")}>
                Copiar clave
              </button>
            </div>
          </div>
          <h4>2 · Escribe el código que muestra la app</h4>
          <label>
            Código de 6 dígitos
            <input
              className="otp-input"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              inputMode="numeric"
              autoComplete="one-time-code"
              pattern="[0-9 ]{6,7}"
              maxLength={7}
              required
              autoFocus
            />
          </label>
          {errorLine}
          <div className="actions">
            <button type="button" onClick={() => go("overview")}>
              Cancelar
            </button>
            <button className="primary" disabled={busy}>
              Activar
            </button>
          </div>
        </form>
      )}

      {step === "codes" && (
        <div className="settings-section">
          <h4>Guarda tus códigos de recuperación</h4>
          <p className="muted">
            Si pierdes el teléfono, cada uno de estos códigos te deja entrar una vez. Guárdalos en tu gestor de contraseñas. No se volverán
            a mostrar.
          </p>
          <ol className="recovery-codes">
            {codes.map((c) => (
              <li key={c}>
                <code>{c}</code>
              </li>
            ))}
          </ol>
          <div className="actions">
            <button onClick={() => void copy(codes.join("\n"), "Códigos copiados")}>Copiar</button>
            <button onClick={download}>Descargar .txt</button>
          </div>
          <label className="check">
            <input type="checkbox" checked={saved} onChange={(e) => setSaved(e.target.checked)} />
            Ya guardé estos códigos en un lugar seguro
          </label>
          <div className="actions">
            <button
              className="primary"
              disabled={!saved}
              onClick={() => {
                setCodes([]);
                go("overview");
              }}
            >
              Listo
            </button>
          </div>
        </div>
      )}

      {step === "disable" && (
        <form className="settings-section" onSubmit={disable}>
          <h4>Desactivar la verificación en dos pasos</h4>
          <p className="warn">Se cerrarán tus otras sesiones y dejarás de poder usar conexiones protegidas hasta volver a activarla.</p>
          {!sso && (
            <label>
              Contraseña actual
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                required
                autoFocus
              />
            </label>
          )}
          <label>
            Código de la app o de recuperación
            <input
              className="otp-input"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              autoComplete="one-time-code"
              maxLength={16}
              required
              autoFocus={sso}
            />
          </label>
          {errorLine}
          <div className="actions">
            <button type="button" onClick={() => go("overview")}>
              Cancelar
            </button>
            <button className="danger" disabled={busy}>
              Desactivar
            </button>
          </div>
        </form>
      )}

      {step === "regenerate" && (
        <form className="settings-section" onSubmit={regenerate}>
          <h4>Nuevos códigos de recuperación</h4>
          <p className="muted">Los códigos anteriores dejarán de funcionar.</p>
          <label>
            Código de 6 dígitos de la app
            <input
              className="otp-input"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={7}
              required
              autoFocus
            />
          </label>
          {errorLine}
          <div className="actions">
            <button type="button" onClick={() => go("overview")}>
              Cancelar
            </button>
            <button className="primary" disabled={busy}>
              Generar
            </button>
          </div>
        </form>
      )}
      {copyNotice}
    </Dialog>
  );
}

const ACTION_LABELS: Record<string, string> = {
  "auth.setup": "Instalación inicial",
  "auth.login": "Inicio de sesión",
  "auth.login.mfa": "Segundo paso del inicio de sesión",
  "auth.logout": "Cierre de sesión",
  "auth.oidc.start": "Inicio de sesión SSO · redirección",
  "auth.oidc.login": "Inicio de sesión SSO",
  "auth.passkey.login": "Inicio de sesión con passkey",
  "auth.session.revoke": "Sesión cerrada a distancia",
  "auth.sessions.revoke_others": "Otras sesiones cerradas",
  "auth.mfa.setup": "2FA · QR generado",
  "auth.mfa.enable": "2FA · activada",
  "auth.mfa.disable": "2FA · desactivada",
  "auth.mfa.recovery_codes": "2FA · nuevos códigos",
  "connection.create": "Conexión creada",
  "connection.update": "Conexión editada",
  "connection.delete": "Conexión eliminada",
  "connection.test": "Prueba de conexión",
  "connection.sharing.update": "Uso compartido actualizado",
  "connection.grant.create": "Permiso concedido o actualizado",
  "connection.grant.delete": "Permiso revocado",
  "connection.grants.list": "Permisos consultados",
  "connection.grants.candidates": "Directorio para compartir consultado",
  "ssh.fingerprint": "Huella SSH consultada",
  "discovery.scan": "Descubrimiento de servicios",
  "profiles.export": "Perfiles exportados",
  "profiles.import": "Perfiles importados",
  "database.create": "Base creada",
  "database.drop": "Base eliminada",
  "table.create": "Tabla creada",
  "table.truncate": "Tabla vaciada",
  "table.drop": "Tabla eliminada",
  "table.optimize": "Tabla optimizada",
  "row.read": "Registro abierto",
  "row.insert": "Registro insertado",
  "row.update": "Registro editado",
  "row.delete": "Registro eliminado",
  "row.delete_batch": "Registros eliminados",
  "query.execute": "Consulta SQL",
  "query.cancel": "Consulta cancelada",
  "query.save": "Consulta guardada",
  "query.saved.delete": "Consulta guardada eliminada",
  "data.import.csv": "Datos importados (CSV)",
  "data.import.sql": "Base importada (SQL)",
  "user.list": "Personas consultadas",
  "user.create": "Persona añadida",
  "user.update": "Persona actualizada",
  "user.delete": "Persona eliminada",
  "audit.export": "Auditoría exportada",
  "audit.purge": "Auditoría depurada",
  "mysql.account.manage": "Cuenta MySQL gestionada",
  "mysql.process.kill": "Proceso MySQL detenido",
  "data.export": "Datos exportados",
};

const OUTCOME_LABELS: Record<AuditEntry["outcome"], string> = {
  ok: "Correcto",
  error: "Error",
  denied: "Denegado",
  confirmation_requested: "Pidió confirmación",
};

const FILTERS: [string, string][] = [
  ["", "Toda la actividad"],
  ["auth.", "Accesos y 2FA"],
  ["query.", "Consultas SQL"],
  ["data.export", "Exportaciones"],
  ["row.", "Registros"],
  ["table.", "Tablas"],
  ["database.", "Bases de datos"],
  ["connection.", "Conexiones"],
  ["profiles.", "Perfiles compartidos"],
  ["user.", "Personas del equipo"],
  ["data.import", "Importaciones"],
];

const PAGE = 100;

export function AuditDialog({ connections, onClose }: { connections: Connection[]; onClose: () => void }) {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [filter, setFilter] = useState("");
  const [outcome, setOutcome] = useState("");
  const [person, setPerson] = useState("");
  const [personDraft, setPersonDraft] = useState("");
  const [more, setMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [integrity, setIntegrity] = useState<{ ok: boolean; checked: number; brokenAt?: number } | null>(null);
  const sentinel = useRef<HTMLDivElement>(null);
  const loadingRef = useRef(false);

  const fetchPage = useCallback(
    async (before?: number) => {
      if (loadingRef.current && before) return;
      loadingRef.current = true;
      setLoading(true);
      setError("");
      try {
        const q = new URLSearchParams({ limit: String(PAGE) });
        if (filter) q.set("action", filter);
        if (outcome) q.set("outcome", outcome);
        if (person) q.set("username", person);
        if (before) q.set("before", String(before));
        const res = await api<{ entries: AuditEntry[] }>("GET", `/api/audit?${q}`);
        setEntries((old) => (before ? [...old, ...res.entries] : res.entries));
        setMore(res.entries.length === PAGE);
      } catch (e) {
        setError(errorText(e));
      } finally {
        loadingRef.current = false;
        setLoading(false);
      }
    },
    [filter, outcome, person],
  );
  useEffect(() => {
    void fetchPage();
  }, [fetchPage]);

  // Endless list: the next page loads when the end of the list scrolls into view.
  const lastId = entries[entries.length - 1]?.id;
  useEffect(() => {
    const el = sentinel.current;
    if (!el || !more || !lastId) return;
    const observer = new IntersectionObserver((items) => {
      if (items.some((i) => i.isIntersecting)) void fetchPage(lastId);
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, [more, lastId, fetchPage]);

  async function verify() {
    setIntegrity(null);
    try {
      setIntegrity(await api("GET", "/api/audit/verify"));
    } catch (e) {
      setError(errorText(e));
    }
  }

  const connectionName = (id?: string) => (id ? (connections.find((c) => c.id === id)?.name ?? `perfil ${id.slice(0, 8)}`) : "");
  const filtered = !!(filter || outcome || person);
  const groups = groupByDay(entries);

  return (
    <Dialog title="Registro de auditoría" onClose={onClose} className="audit-dialog">
      <div className="audit-toolbar">
        <label>
          <span>Actividad</span>
          <select value={filter} onChange={(e) => setFilter(e.target.value)}>
            {FILTERS.map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span>Resultado</span>
          <select value={outcome} onChange={(e) => setOutcome(e.target.value)}>
            <option value="">Todos</option>
            {Object.entries(OUTCOME_LABELS).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </label>
        <form
          className="audit-person"
          onSubmit={(e) => {
            e.preventDefault();
            setPerson(personDraft.trim());
          }}
        >
          <label>
            <span>Persona</span>
            <input
              type="search"
              placeholder="Usuario exacto y Enter"
              value={personDraft}
              onChange={(e) => {
                setPersonDraft(e.target.value);
                if (!e.target.value) setPerson("");
              }}
            />
          </label>
        </form>
        <span className="spacer" />
        {filtered && (
          <button
            className="link-button"
            onClick={() => {
              setFilter("");
              setOutcome("");
              setPerson("");
              setPersonDraft("");
            }}
          >
            Limpiar filtros
          </button>
        )}
        <button onClick={() => void verify()}>Verificar integridad</button>
      </div>
      {integrity && (
        <p className={integrity.ok ? "okmsg" : "error"} role="status">
          {integrity.ok
            ? `Registro íntegro · ${integrity.checked} entradas verificadas`
            : `El registro fue alterado a partir de la entrada #${integrity.brokenAt}`}
        </p>
      )}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {entries.length > 0 && (
        <div className="audit-table" role="table" aria-label="Eventos de auditoría">
          <div className="audit-head" role="row">
            <span role="columnheader">Hora</span>
            <span role="columnheader">Persona</span>
            <span role="columnheader">Acción</span>
            <span role="columnheader">Sobre</span>
            <span role="columnheader">Resultado</span>
          </div>
          {groups.map(([day, items]) => (
            <section key={day} className="audit-day" role="rowgroup" aria-label={day}>
              <h5>{day}</h5>
              {items.map((e) => {
                const where = [connectionName(e.connectionId), e.target].filter(Boolean).join(" · ");
                return (
                  <details key={e.id} className={`audit-row outcome-${e.outcome}`} role="row">
                    <summary>
                      <time dateTime={new Date(e.at * 1000).toISOString()}>{new Date(e.at * 1000).toLocaleTimeString()}</time>
                      <span className="audit-user">{e.username || "—"}</span>
                      <strong className="audit-action" title={e.action}>
                        {ACTION_LABELS[e.action] ?? e.action}
                      </strong>
                      <span className="audit-where" title={[where, e.detail].filter(Boolean).join("\n")}>
                        {where && <span>{where}</span>}
                        {e.detail && <code>{e.detail.split("\n")[0]}</code>}
                      </span>
                      <span className={`audit-outcome outcome-${e.outcome}`}>{OUTCOME_LABELS[e.outcome] ?? e.outcome}</span>
                    </summary>
                    <dl className="audit-detail">
                      <dt>Fecha</dt>
                      <dd>{new Date(e.at * 1000).toLocaleString()}</dd>
                      <dt>IP</dt>
                      <dd>{e.ip || "—"}</dd>
                      <dt>Evento</dt>
                      <dd>
                        <code>{e.action}</code> · #{e.id}
                      </dd>
                      {e.detail && (
                        <>
                          <dt>Detalle</dt>
                          <dd>
                            <pre>{e.detail}</pre>
                          </dd>
                        </>
                      )}
                    </dl>
                  </details>
                );
              })}
            </section>
          ))}
        </div>
      )}
      {!loading && !entries.length && !error && <p className="muted audit-empty">No hay actividad registrada con estos filtros.</p>}
      <div ref={sentinel} className="audit-more">
        {loading ? (
          <span className="muted">Cargando…</span>
        ) : more ? (
          <button onClick={() => void fetchPage(lastId)}>Cargar más</button>
        ) : (
          entries.length > 0 && <span className="muted">{entries.length} eventos · no hay más</span>
        )}
      </div>
      <div className="audit-footer">
        <span className="muted">
          Se registran accesos, cambios, consultas SQL y exportaciones; las lecturas de tablas no. Las contraseñas que aparecen en el SQL se
          ocultan.
        </span>
        <span>
          Exportar: <a href="/api/audit/export?format=jsonl">JSONL</a> · <a href="/api/audit/export?format=csv">CSV</a>
        </span>
      </div>
    </Dialog>
  );
}

/** Groups entries (newest first) under "Hoy", "Ayer" or the full date. */
function groupByDay(entries: AuditEntry[]): [string, AuditEntry[]][] {
  const key = (d: Date) => d.toDateString();
  const today = new Date();
  const yesterday = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 1);
  const groups = new Map<string, AuditEntry[]>();
  for (const e of entries) {
    const d = new Date(e.at * 1000);
    const label =
      key(d) === key(today)
        ? "Hoy"
        : key(d) === key(yesterday)
          ? "Ayer"
          : d.toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long", year: "numeric" });
    const list = groups.get(label);
    if (list) list.push(e);
    else groups.set(label, [e]);
  }
  return [...groups];
}
