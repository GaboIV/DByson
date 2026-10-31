import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "./api";
import { useConfirm } from "./Confirm";

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
  authMethod?: "password" | "sso" | "passkey";
}

const date = (seconds: number) => new Date(seconds * 1000).toLocaleString();

const METHOD_LABELS: Record<string, string> = { password: "Contraseña", sso: "SSO", passkey: "Passkey" };

/** "hace 3 min", "hace 2 h", or the date for anything older than a day. */
function ago(seconds: number) {
  const diff = Math.max(0, Date.now() / 1000 - seconds);
  if (diff < 60) return "ahora";
  if (diff < 3600) return `hace ${Math.floor(diff / 60)} min`;
  if (diff < 86400) return `hace ${Math.floor(diff / 3600)} h`;
  return date(seconds);
}

/** A readable "Chrome 152 · Windows" from a user agent; the raw string stays in the tooltip. */
export function describeAgent(ua: string): { browser: string; system: string; mobile: boolean } {
  const version = (re: RegExp) => ua.match(re)?.[1]?.split(".")[0];
  let browser = "Navegador";
  if (/Edg\//.test(ua)) browser = `Edge ${version(/Edg\/([\d.]+)/) ?? ""}`;
  else if (/OPR\//.test(ua)) browser = `Opera ${version(/OPR\/([\d.]+)/) ?? ""}`;
  else if (/Firefox\//.test(ua)) browser = `Firefox ${version(/Firefox\/([\d.]+)/) ?? ""}`;
  else if (/Chrome\//.test(ua)) browser = `Chrome ${version(/Chrome\/([\d.]+)/) ?? ""}`;
  else if (/Safari\//.test(ua)) browser = `Safari ${version(/Version\/([\d.]+)/) ?? ""}`;
  else if (/curl|Go-http-client|python/i.test(ua)) browser = "Cliente de línea de comandos";
  let system = "";
  if (/Windows/.test(ua)) system = "Windows";
  else if (/Android/.test(ua)) system = "Android";
  else if (/iPhone|iPad|iOS/.test(ua)) system = "iOS";
  else if (/Mac OS X|Macintosh/.test(ua)) system = "macOS";
  else if (/Linux/.test(ua)) system = "Linux";
  return { browser: browser.trim(), system, mobile: /Mobile|Android|iPhone/.test(ua) };
}

export function SessionsPanel() {
  const confirm = useConfirm();
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
      setItems([...response.sessions].sort((a, b) => Number(b.current) - Number(a.current)));
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
      !(await confirm(
        item ? `¿Cerrar ${item.current ? "esta sesión" : "la sesión seleccionada"}?` : "¿Cerrar todas las demás sesiones de tu cuenta?",
      ))
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

  const others = items.filter((item) => !item.current).length;

  return (
    <section className="settings-section session-panel" aria-label="Sesiones activas">
      <div className="section-heading">
        <h4>
          Sesiones activas <span className="count-badge">{items.length}</span>
        </h4>
        <div className="session-toolbar">
          <button disabled={busy} onClick={() => void load()} title="Actualizar sesiones" aria-label="Actualizar sesiones">
            ↻
          </button>
          <button className="danger" disabled={busy || !others} onClick={() => void revoke()} aria-label="Cerrar las demás sesiones">
            Cerrar las demás{others ? ` (${others})` : ""}
          </button>
        </div>
      </div>
      <p className="muted">
        Caducan tras 2 horas de inactividad o un máximo de 12 horas. Cerrar una sesión también detiene sus consultas en curso.
      </p>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <ul className="session-list" aria-busy={busy}>
        {items.map((item) => {
          const agent = describeAgent(item.userAgent);
          return (
            <li key={item.id} className={`session-item${item.current ? " is-current" : ""}`}>
              <span className="session-icon" aria-hidden="true">
                {agent.mobile ? "📱" : "💻"}
              </span>
              <div className="session-main">
                <div className="session-title" title={item.userAgent || undefined}>
                  <strong>
                    {agent.browser}
                    {agent.system && ` · ${agent.system}`}
                  </strong>
                  {item.current && <span className="session-badge current">Esta sesión</span>}
                </div>
                <div className="session-meta">
                  <span className="mono">{item.ip || "IP no disponible"}</span>
                  <span title={`Último uso: ${date(item.lastSeen)}`}>Activa {ago(item.lastSeen)}</span>
                  <span title={date(item.createdAt)}>Inició {ago(item.createdAt)}</span>
                </div>
                <div className="session-badges">
                  {item.authMethod && <span className="session-badge">{METHOD_LABELS[item.authMethod] ?? item.authMethod}</span>}
                  <span className={`session-badge${item.mfa ? " ok" : ""}`}>{item.mfa ? "2FA verificada" : "Sin 2FA"}</span>
                  {item.certificateBound && <span className="session-badge ok">Certificado mTLS</span>}
                </div>
              </div>
              <button className={item.current ? "" : "danger"} disabled={busy} onClick={() => void revoke(item)}>
                {item.current ? "Salir" : "Cerrar"}
              </button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
