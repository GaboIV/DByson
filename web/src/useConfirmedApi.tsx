import { useEffect, useRef, useState } from "react";
import { api } from "./api";

interface Challenge {
  needsConfirmation: true;
  operation: string;
  reason: string;
  preview: string;
  confirmationTarget: string;
  confirmationToken: string;
}

export function useConfirmedApi(context: string) {
  const [challenge, setChallenge] = useState<Challenge | null>(null);
  const [name, setName] = useState("");
  const resolver = useRef<((confirmed: boolean) => void) | null>(null);

  function finish(confirmed: boolean) {
    resolver.current?.(confirmed);
    resolver.current = null;
    setChallenge(null);
    setName("");
  }

  useEffect(
    () => () => {
      resolver.current?.(false);
      resolver.current = null;
    },
    [],
  );

  async function confirmedApi<T>(method: string, path: string, body: Record<string, unknown> = {}): Promise<T | undefined> {
    const response = await api<T | Challenge>(method, path, body);
    if (!(response && typeof response === "object" && "needsConfirmation" in response && response.needsConfirmation)) {
      return response as T;
    }
    // Only one modal can own a pending confirmation at a time.
    if (resolver.current) return undefined;
    const pending = response as Challenge;
    setName("");
    setChallenge(pending);
    const confirmed = await new Promise<boolean>((resolve) => {
      resolver.current = resolve;
    });
    if (!confirmed) return undefined;
    return api<T>(method, path, { ...body, confirmationToken: pending.confirmationToken, confirmationName: pending.confirmationTarget });
  }

  const confirmationDialog = challenge && (
    <div className="modal-overlay">
      <section
        className="modal-content"
        role="dialog"
        aria-modal="true"
        aria-labelledby="confirmation-title"
        onKeyDown={(e) => {
          if (e.key === "Escape") finish(false);
          if (e.key === "Tab") {
            const fields = [...e.currentTarget.querySelectorAll<HTMLElement>("input, button:not(:disabled)")];
            const first = fields[0],
              last = fields[fields.length - 1];
            if (e.shiftKey && document.activeElement === first) {
              e.preventDefault();
              last?.focus();
            }
            if (!e.shiftKey && document.activeElement === last) {
              e.preventDefault();
              first?.focus();
            }
          }
        }}
      >
        <header className="modal-header">
          <h3 id="confirmation-title">Confirmar {challenge.operation}</h3>
        </header>
        <form
          className="modal-form"
          onSubmit={(e) => {
            e.preventDefault();
            if (name === challenge.confirmationTarget) finish(true);
          }}
        >
          <div className="modal-fields">
            <strong>{context}</strong>
            <p>{challenge.reason}</p>
            <pre className="confirmation-preview">{challenge.preview}</pre>
            <label>
              Escribe «{challenge.confirmationTarget}» para continuar
              <input autoFocus autoComplete="off" value={name} onChange={(e) => setName(e.target.value)} />
            </label>
            <small className="muted">La confirmación sirve una sola vez y caduca en 5 minutos.</small>
          </div>
          <footer className="modal-footer">
            <button type="button" onClick={() => finish(false)}>
              Cancelar
            </button>
            <button className="danger" type="submit" disabled={name !== challenge.confirmationTarget}>
              Confirmar y ejecutar
            </button>
          </footer>
        </form>
      </section>
    </div>
  );
  return { confirmedApi, confirmationDialog };
}
