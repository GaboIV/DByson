import { useEffect, useState } from "react";
import { api } from "./api";
import { passkeysSupported, registerPasskey } from "./passkeys";

interface Credential {
  id: string;
  name: string;
  createdAt: number;
}
export function PasskeysPanel() {
  const [status, setStatus] = useState<{ enabled: boolean; credentials: Credential[] } | null>(null),
    [name, setName] = useState("Mi dispositivo"),
    [password, setPassword] = useState(""),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [notice, setNotice] = useState("");
  async function load() {
    try {
      setStatus(await api("GET", "/api/auth/passkeys"));
    } catch (e) {
      setError(e instanceof Error ? e.message : "No se pudieron cargar las passkeys");
    }
  }
  useEffect(() => {
    void load();
  }, []);
  async function run(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await action();
      setNotice("Passkeys actualizadas.");
      await load();
    } catch (e) {
      setError(
        e instanceof DOMException && e.name === "NotAllowedError"
          ? "Operación cancelada o no autorizada por el dispositivo."
          : e instanceof Error
            ? e.message
            : "No se pudo completar la operación",
      );
    } finally {
      setPassword("");
      setBusy(false);
    }
  }
  return (
    <section className="passkeys-panel">
      <h4>Passkeys</h4>
      <p className="muted">
        Entra con huella, rostro, PIN del dispositivo o llave de seguridad. DByson conserva la clave pública. Se exige verificación del
        usuario en cada acceso.
      </p>
      {status && !status.enabled && (
        <p className="muted">El servidor aún no tiene configurados el dominio y el origen público para passkeys.</p>
      )}
      {status?.enabled && !passkeysSupported() && (
        <p className="warn">Abre DByson mediante HTTPS o localhost y usa un navegador compatible.</p>
      )}
      <div className="productivity-grid">
        <label>
          Nombre de la passkey
          <input value={name} maxLength={100} disabled={busy} onChange={(e) => setName(e.target.value)} />
        </label>
        <label>
          Contraseña actual para gestionar passkeys
          <input
            type="password"
            value={password}
            disabled={busy}
            autoComplete="current-password"
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
      </div>
      <button
        className="primary"
        disabled={busy || !status?.enabled || !passkeysSupported() || !password || !name.trim()}
        onClick={() => void run(() => registerPasskey(name, password))}
      >
        Registrar passkey
      </button>
      <ul className="passkey-list">
        {status?.credentials.map((c) => (
          <li key={c.id}>
            <div>
              <strong>{c.name}</strong>
              <small>{new Date(c.createdAt * 1000).toLocaleString()}</small>
            </div>
            <button
              disabled={busy || !password}
              aria-label={`Eliminar passkey ${c.name}`}
              onClick={() =>
                void run(async () => {
                  await api("POST", "/api/auth/passkeys/delete", { id: c.id, password });
                })
              }
            >
              Eliminar passkey
            </button>
          </li>
        ))}
      </ul>
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
    </section>
  );
}
