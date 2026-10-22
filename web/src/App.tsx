import { useEffect, useState } from "react";
import { api, setCsrf, type Session } from "./api";
import { AuthScreen } from "./AuthScreen";
import { PreferencesProvider } from "./Preferences";
import { Workbench } from "./Workbench";
import { Spinner } from "./Spinner";
import { clearCatalog } from "./workspace/catalog";

type State =
  { kind: "loading" } | { kind: "error" } | { kind: "setup"; needsToken: boolean } | { kind: "login" } | { kind: "app"; session: Session };

export function App() {
  const [state, setState] = useState<State>({ kind: "loading" });

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const status = await api<{ setupRequired: boolean; setupToken: boolean }>("GET", "/api/status");
        if (cancelled) return;
        if (status.setupRequired) return setState({ kind: "setup", needsToken: status.setupToken });
        try {
          const session = await api<Session>("GET", "/api/auth/me");
          setCsrf(session.csrf);
          if (!cancelled) setState({ kind: "app", session });
        } catch {
          if (!cancelled) setState({ kind: "login" });
        }
      } catch {
        if (!cancelled) setState({ kind: "error" });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  async function logout() {
    try {
      await api("POST", "/api/auth/logout");
    } finally {
      setCsrf("");
      clearCatalog();
      setState({ kind: "login" });
    }
  }

  switch (state.kind) {
    case "loading":
      return (
        <main className="center muted">
          <Spinner label="Cargando…" />
        </main>
      );
    case "error":
      return <main className="center error">No se pudo contactar con el servidor</main>;
    case "setup":
      return (
        <AuthScreen
          mode="setup"
          needsToken={state.needsToken}
          onSession={(session) => {
            setState({ kind: "app", session });
          }}
        />
      );
    case "login":
      return (
        <AuthScreen
          mode="login"
          needsToken={false}
          onSession={(session) => {
            setState({ kind: "app", session });
          }}
        />
      );
    case "app":
      return (
        <div className="layout">
          <PreferencesProvider key={state.session.username} username={state.session.username}>
            <Workbench session={state.session} onLogout={logout} />
          </PreferencesProvider>
        </div>
      );
  }
}
