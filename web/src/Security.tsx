import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api, ApiError, type AuditEntry, type Connection, type MFAStatus } from "./api";
import { Dialog } from "./Dialog";
import { useClipboard } from "./clipboard";

const errorText = (e: unknown) => (e instanceof ApiError ? e.message : "No se pudo completar la operación");

type Step = "overview" | "password" | "scan" | "codes" | "disable" | "regenerate";

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
  const { copy, copyNotice } = useClipboard();

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
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }

  const startSetup = (e: FormEvent) =>
    run(e, async () => {
      const res = await api<{ secret: string; qr: string }>("POST", "/api/auth/mfa/setup", { password });
      setSetup(res);
      go("scan");
    });

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
      await api("POST", "/api/auth/mfa/disable", { password, code });
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
                {status.enabled
                  ? `Activa · ${status.recoveryCodesLeft} códigos de recuperación disponibles`
                  : "Desactivada"}
              </p>
              <p className="muted">
                Además de la contraseña, se pedirá un código de una app como Google Authenticator, 1Password, Bitwarden o
                Microsoft Authenticator. {policyNote}
              </p>
              {status.enabled && status.recoveryCodesLeft <= 3 && (
                <p className="warn">Te quedan pocos códigos de recuperación. Genera unos nuevos.</p>
              )}
              <div className="actions">
                {status.enabled ? (
                  <>
                    <button onClick={() => go("regenerate")}>Nuevos códigos de recuperación</button>
                    <button className="danger" onClick={() => go("disable")}>
                      Desactivar
                    </button>
                  </>
                ) : (
                  <button className="primary" onClick={() => go("password")}>
                    Activar verificación en dos pasos
                  </button>
                )}
              </div>
            </>
          )}
          {errorLine}
        </div>
      )}

      {step === "password" && (
        <form className="settings-section" onSubmit={startSetup}>
          <h4>Confirma tu contraseña</h4>
          <label>
            Contraseña actual
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required autoFocus />
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
            Si pierdes el teléfono, cada uno de estos códigos te deja entrar una vez. Guárdalos en tu gestor de contraseñas.
            No se volverán a mostrar.
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
          <p className="warn">
            Se cerrarán tus otras sesiones y dejarás de poder usar conexiones protegidas hasta volver a activarla.
          </p>
          <label>
            Contraseña actual
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required autoFocus />
          </label>
          <label>
            Código de la app o de recuperación
            <input className="otp-input" value={code} onChange={(e) => setCode(e.target.value)} autoComplete="one-time-code" maxLength={16} required />
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
            <input className="otp-input" value={code} onChange={(e) => setCode(e.target.value)} inputMode="numeric" autoComplete="one-time-code" maxLength={7} required autoFocus />
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
  "auth.mfa.setup": "2FA · QR generado",
  "auth.mfa.enable": "2FA · activada",
  "auth.mfa.disable": "2FA · desactivada",
  "auth.mfa.recovery_codes": "2FA · nuevos códigos",
  "connection.create": "Conexión creada",
  "connection.update": "Conexión editada",
  "connection.delete": "Conexión eliminada",
  "connection.test": "Prueba de conexión",
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
];

const PAGE = 100;

export function AuditDialog({ connections, onClose }: { connections: Connection[]; onClose: () => void }) {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [filter, setFilter] = useState("");
  const [more, setMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [integrity, setIntegrity] = useState<{ ok: boolean; checked: number; brokenAt?: number } | null>(null);

  const fetchPage = useCallback(
    async (before?: number) => {
      setLoading(true);
      setError("");
      try {
        const q = new URLSearchParams({ limit: String(PAGE) });
        if (filter) q.set("action", filter);
        if (before) q.set("before", String(before));
        const res = await api<{ entries: AuditEntry[] }>("GET", `/api/audit?${q}`);
        setEntries((old) => (before ? [...old, ...res.entries] : res.entries));
        setMore(res.entries.length === PAGE);
      } catch (e) {
        setError(errorText(e));
      } finally {
        setLoading(false);
      }
    },
    [filter],
  );
  useEffect(() => {
    void fetchPage();
  }, [fetchPage]);

  async function verify() {
    setIntegrity(null);
    try {
      setIntegrity(await api("GET", "/api/audit/verify"));
    } catch (e) {
      setError(errorText(e));
    }
  }

  const connectionName = (id?: string) => (id ? (connections.find((c) => c.id === id)?.name ?? `perfil ${id.slice(0, 8)}`) : "");

  return (
    <Dialog title="Registro de auditoría" onClose={onClose} className="audit-dialog">
      <div className="section-heading">
        <label>
          Mostrar
          <select value={filter} onChange={(e) => setFilter(e.target.value)}>
            {FILTERS.map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </label>
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
      <ol className="audit-list">
        {entries.map((e) => (
          <li key={e.id} className={`audit-entry outcome-${e.outcome}`}>
            <header>
              <time dateTime={new Date(e.at * 1000).toISOString()}>{new Date(e.at * 1000).toLocaleString()}</time>
              <strong>{ACTION_LABELS[e.action] ?? e.action}</strong>
              <span className={`audit-outcome outcome-${e.outcome}`}>{OUTCOME_LABELS[e.outcome] ?? e.outcome}</span>
            </header>
            <p className="muted">
              {e.username || "—"} · {e.ip}
              {e.connectionId && ` · ${connectionName(e.connectionId)}`}
              {e.target && ` · ${e.target}`}
            </p>
            {e.detail && (e.detail.length > 120 || e.detail.includes("\n") ? (
              <details>
                <summary>{e.detail.slice(0, 120)}…</summary>
                <pre>{e.detail}</pre>
              </details>
            ) : (
              <pre>{e.detail}</pre>
            ))}
          </li>
        ))}
      </ol>
      {!loading && !entries.length && !error && <p className="muted">No hay actividad registrada con este filtro.</p>}
      {loading && <p className="muted">Cargando…</p>}
      {more && !loading && <button onClick={() => void fetchPage(entries[entries.length - 1]?.id)}>Cargar más</button>}
      <p className="muted">
        Se registran accesos, cambios, consultas SQL y exportaciones; las lecturas de tablas no. Las contraseñas que
        aparecen en el SQL se ocultan.
      </p>
    </Dialog>
  );
}
