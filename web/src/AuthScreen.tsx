import { useState, type FormEvent } from "react";
import { api, ApiError, setCsrf, type Session } from "./api";
import { Brand } from "./Brand";

interface Props {
  mode: "setup" | "login";
  needsToken: boolean;
  onSession: (s: Session) => void;
}

export function AuthScreen({ mode, needsToken, onSession }: Props) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const s = await api<Session>("POST", mode === "setup" ? "/api/setup" : "/api/auth/login", {
        username,
        password,
        ...(mode === "setup" && needsToken ? { setupToken: token } : {}),
      });
      setCsrf(s.csrf);
      onSession(s);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "No se pudo contactar con el servidor");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="auth-page">
      <aside className="auth-intro"><Brand /><h2>Tus bases de datos.<br />Un solo espacio.</h2><p>Explora, consulta y conecta tus servidores con la claridad que necesitas para trabajar cada día.</p><ul><li>MySQL y MariaDB, locales o remotos</li><li>Conexiones directas y túneles SSH</li><li>Consultas en pestañas y perfiles compartibles</li></ul></aside>
      <form className="card auth" onSubmit={submit}>
        <h1>{mode === "setup" ? "Bienvenido a DByson" : "Entra a DByson"}</h1>

        <p className="muted">
          {mode === "setup" ? "Crea la cuenta de administrador para empezar." : "Inicia sesión para continuar."}
        </p>
        <label>
          Usuario
          <input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" required autoFocus />
        </label>
        <label>
          Contraseña
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete={mode === "setup" ? "new-password" : "current-password"}
            minLength={mode === "setup" ? 12 : undefined}
            required
          />
          {mode === "setup" && <small className="muted">Mínimo 12 caracteres.</small>}
        </label>
        {mode === "setup" && needsToken && (
          <label>
            Token de instalación
            <input type="password" value={token} onChange={(e) => setToken(e.target.value)} required />
            <small className="muted">Definido en ADMINBASE_SETUP_TOKEN.</small>
          </label>
        )}
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <button className="primary" disabled={busy}>
          {mode === "setup" ? "Crear administrador" : "Entrar"}
        </button>
      </form>
    </main>
  );
}
