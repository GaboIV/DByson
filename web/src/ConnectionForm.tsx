import { useState, type FormEvent } from "react";
import { api, ApiError, type Connection, type ConnectionInput, type Env, type TestResponse, type TLSMode } from "./api";

interface Props {
  existing?: Connection;
  onSaved: (c: Connection) => void;
  onCancel: () => void;
}

const TLS_LABELS: Record<TLSMode, string> = {
  verify_identity: "Verificar certificado y nombre de host (recomendado)",
  verify_ca: "Verificar certificado (CA) sin comprobar el host",
  required: "Cifrar sin verificar el servidor (inseguro ante MITM)",
  disabled: "Sin cifrado (solo localhost)",
};

export function ConnectionForm({ existing, onSaved, onCancel }: Props) {
  const [name, setName] = useState(existing?.name ?? "");
  const [env, setEnv] = useState<Env>(existing?.env ?? "dev");
  const [host, setHost] = useState(existing?.host ?? "");
  const [port, setPort] = useState(existing?.port ?? 3306);
  const [username, setUsername] = useState(existing?.username ?? "");
  const [password, setPassword] = useState("");
  const [database, setDatabase] = useState(existing?.database ?? "");
  const [tlsMode, setTlsMode] = useState<TLSMode>(existing?.tlsMode ?? "verify_identity");
  const [readOnly, setReadOnly] = useState(existing?.readOnly ?? false);
  const [caCert, setCaCert] = useState("");
  const [clientCert, setClientCert] = useState("");
  const [clientKey, setClientKey] = useState("");
  const [error, setError] = useState("");
  const [test, setTest] = useState<TestResponse | null>(null);
  const [busy, setBusy] = useState<"" | "save" | "test">("");

  // Empty secret fields are omitted so the server keeps what is already stored.
  function payload(): ConnectionInput {
    return {
      name,
      env,
      host,
      port,
      username,
      database,
      tlsMode,
      readOnly,
      ...(password ? { password } : {}),
      ...(caCert ? { caCert } : {}),
      ...(clientCert ? { clientCert } : {}),
      ...(clientKey ? { clientKey } : {}),
    };
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy("save");
    setError("");
    try {
      const saved = existing
        ? await api<Connection>("PUT", `/api/connections/${existing.id}`, payload())
        : await api<Connection>("POST", "/api/connections", payload());
      onSaved(saved);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Error inesperado");
    } finally {
      setBusy("");
    }
  }

  async function runTest() {
    setBusy("test");
    setError("");
    setTest(null);
    try {
      // The draft endpoint cannot see stored secrets, so when editing without retyping any
      // secret, test the saved profile. Changed connection fields need saving first.
      const untouchedSecrets = existing && !password && !caCert && !clientCert && !clientKey;
      const res = untouchedSecrets
        ? await api<TestResponse>("POST", `/api/connections/${existing.id}/test`)
        : await api<TestResponse>("POST", "/api/connections/test", payload());
      setTest(res);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Error inesperado");
    } finally {
      setBusy("");
    }
  }

  const tlsOn = tlsMode !== "disabled";

  return (
    <form className="card form" onSubmit={save}>
      <h2>{existing ? "Editar conexión" : "Nueva conexión"}</h2>

      <div className="grid">
        <label className="span2">
          Nombre
          <input value={name} onChange={(e) => setName(e.target.value)} required maxLength={100} />
        </label>
        <label>
          Entorno
          <select value={env} onChange={(e) => setEnv(e.target.value as Env)}>
            <option value="local">local</option>
            <option value="dev">dev</option>
            <option value="staging">staging</option>
            <option value="prod">prod</option>
          </select>
        </label>
        <label className="span2">
          Host
          <input value={host} onChange={(e) => setHost(e.target.value)} required placeholder="db.ejemplo.com" />
        </label>
        <label>
          Puerto
          <input type="number" min={1} max={65535} value={port} onChange={(e) => setPort(Number(e.target.value))} required />
        </label>
        <label>
          Usuario
          <input value={username} onChange={(e) => setUsername(e.target.value)} required autoComplete="off" />
        </label>
        <label>
          Contraseña
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="new-password"
            placeholder={existing?.hasPassword ? "•••••• (sin cambios)" : ""}
          />
        </label>
        <label>
          Base de datos
          <input value={database} onChange={(e) => setDatabase(e.target.value)} placeholder="(opcional)" />
        </label>
      </div>

      <label>
        Seguridad TLS
        <select value={tlsMode} onChange={(e) => setTlsMode(e.target.value as TLSMode)}>
          {(Object.keys(TLS_LABELS) as TLSMode[]).map((m) => (
            <option key={m} value={m}>
              {TLS_LABELS[m]}
            </option>
          ))}
        </select>
      </label>
      {(tlsMode === "required" || tlsMode === "disabled") && env !== "local" && (
        <p className="warn">Esta opción no protege contra un atacante en la red. Úsala solo en redes de confianza.</p>
      )}

      {tlsOn && (
        <details>
          <summary>Certificados (opcional)</summary>
          <label>
            Certificado de la CA (PEM) {existing?.hasCaCert && <small className="muted">· guardado</small>}
            <textarea
              rows={4}
              value={caCert}
              onChange={(e) => setCaCert(e.target.value)}
              placeholder="-----BEGIN CERTIFICATE-----"
              spellCheck={false}
            />
          </label>
          <label>
            Certificado de cliente (PEM) {existing?.hasClientCert && <small className="muted">· guardado</small>}
            <textarea rows={4} value={clientCert} onChange={(e) => setClientCert(e.target.value)} spellCheck={false} />
          </label>
          <label>
            Clave privada del cliente (PEM)
            <textarea rows={4} value={clientKey} onChange={(e) => setClientKey(e.target.value)} spellCheck={false} />
          </label>
        </details>
      )}

      <label className="check">
        <input type="checkbox" checked={readOnly} onChange={(e) => setReadOnly(e.target.checked)} />
        Solo lectura
      </label>

      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {test && (
        <p className={test.ok ? "okmsg" : "error"} role="status">
          {test.ok && test.result
            ? `Conexión correcta · ${test.result.serverVersion} · ${test.result.latencyMs} ms · ${
                test.result.tls ? `cifrada (${test.result.tlsVersion} ${test.result.tlsCipher})` : "SIN cifrado"
              }`
            : test.error}
        </p>
      )}

      <div className="actions">
        <button type="button" onClick={onCancel}>
          Cancelar
        </button>
        <button type="button" onClick={runTest} disabled={busy !== ""}>
          {busy === "test" ? "Probando…" : "Probar conexión"}
        </button>
        <button className="primary" disabled={busy !== ""}>
          {busy === "save" ? "Guardando…" : "Guardar"}
        </button>
      </div>
    </form>
  );
}
