import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "./api";

interface ActiveSession {
  id: string;
  ip: string;
  userAgent: string;
  createdAt: number;
  lastSeen: number;
  expiresAt: number;
  mfa: boolean;
  current: boolean;
  certificateBound: boolean;
}

const date = (seconds: number) => new Date(seconds * 1000).toLocaleString();

export function SessionsPanel() {
  const [items, setItems] = useState<ActiveSession[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const controller = useRef<AbortController | null>(null);
  const load = useCallback(async () => {
    controller.current?.abort();
    const request = new AbortController();
    controller.current = request;
    setBusy(true);
    setError("");
    try {
      const response = await api<{ sessions: ActiveSession[] }>("GET", "/api/auth/sessions", undefined, { signal: request.signal });
      setItems(response.sessions);
    } catch (e) {
      if (!request.signal.aborted) setError(e instanceof Error ? e.message : "No se pudieron cargar las sesiones");
    } finally {
      if (!request.signal.aborted) setBusy(false);
    }
  }, []);
  useEffect(() => {
    void load();
    return () => controller.current?.abort();
  }, [load]);

  async function revoke(item?: ActiveSession) {
    if (
      !window.confirm(
        item ? `¿Cerrar ${item.current ? "esta sesión" : "la sesión seleccionada"}?` : "¿Cerrar todas las demás sesiones de tu cuenta?",
      )
    )
      return;
    setBusy(true);
    setError("");
    try {
      await api(item ? "DELETE" : "POST", item ? `/api/auth/sessions/${encodeURIComponent(item.id)}` : "/api/auth/sessions/revoke-others");
      if (item?.current) {
        window.location.reload();
        return;
      }
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "No se pudo revocar la sesión");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="settings-section session-panel" aria-label="Sesiones activas">
      <div className="section-heading">
        <h4>Sesiones activas</h4>
        <button disabled={busy} onClick={() => void load()}>
          Actualizar sesiones
        </button>
      </div>
      <p className="muted">
        Accesos a tu cuenta de DByson. Caducan tras 2 horas de inactividad o un máximo de 12 horas. Revocarlos también detiene sus consultas
        en curso.
      </p>
      <button className="danger" disabled={busy || !items.some((item) => !item.current)} onClick={() => void revoke()}>
        Cerrar las demás sesiones
      </button>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {busy && (
        <p className="muted" role="status">
          Actualizando sesiones…
        </p>
      )}
      <ul className="session-list">
        {items.map((item) => (
          <li key={item.id} className="session-item">
            <div className="section-heading">
              <strong>{item.current ? "Esta sesión" : "Otra sesión"}</strong>
              <span className="muted mono">{item.ip || "IP no disponible"}</span>
            </div>
            <p className="session-agent">{item.userAgent || "Navegador no registrado"}</p>
            <small className="muted">
              Inicio: {date(item.createdAt)} · Última actividad: {date(item.lastSeen)}
            </small>
            <small>
              {item.mfa ? "2FA verificada" : "Sin verificación 2FA"}
              {item.certificateBound ? " · Vinculada a certificado mTLS" : ""}
            </small>
            <button className="danger" disabled={busy} onClick={() => void revoke(item)}>
              {item.current ? "Cerrar esta sesión" : "Revocar sesión"}
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
