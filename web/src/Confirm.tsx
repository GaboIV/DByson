import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { Dialog } from "./Dialog";
import { SESSION_EXPIRED_EVENT } from "./api";

const ConfirmContext = createContext<(message: string) => Promise<boolean>>(async () => false);
export const useConfirm = () => useContext(ConfirmContext);
export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [message, setMessage] = useState("");
  const pending = useRef<((result: boolean) => void) | null>(null);
  const confirm = useCallback((message: string) => {
    if (pending.current) return Promise.resolve(false);
    setMessage(message);
    return new Promise<boolean>((resolve) => {
      pending.current = resolve;
    });
  }, []);
  function finish(value: boolean) {
    pending.current?.(value);
    pending.current = null;
    setMessage("");
  }
  useEffect(() => {
    const expired = () => {
      pending.current?.(false);
      pending.current = null;
      setMessage("");
    };
    window.addEventListener(SESSION_EXPIRED_EVENT, expired);
    return () => {
      window.removeEventListener(SESSION_EXPIRED_EVENT, expired);
      pending.current?.(false);
      pending.current = null;
    };
  }, []);
  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      {message && (
        <div className="app-confirm">
          <Dialog title="Confirmar acción" onClose={() => finish(false)}>
            <p>{message}</p>
            <div className="actions">
              <button onClick={() => finish(false)}>Cancelar</button>
              <button className="danger" onClick={() => finish(true)}>
                Continuar
              </button>
            </div>
          </Dialog>
        </div>
      )}
    </ConfirmContext.Provider>
  );
}
