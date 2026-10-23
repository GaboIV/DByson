import { useEffect, useRef, useState } from "react";
import { api, type Connection } from "./api";
import { Dialog } from "./Dialog";
export function ConnectionTransfer({
  mode,
  connections,
  onClose,
  onImported,
}: {
  mode: "import" | "export";
  connections: Connection[];
  onClose: () => void;
  onImported: () => void;
}) {
  const [secrets, setSecrets] = useState(false),
    [passphrase, setPassphrase] = useState(""),
    [file, setFile] = useState(""),
    [filename, setFilename] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const request = useRef<AbortController | null>(null);
  useEffect(() => () => request.current?.abort(), []);
  let preview: { encrypted?: boolean; profiles?: { name: string; host: string; port: number }[] } | null = null;
  try {
    preview = JSON.parse(file);
  } catch {}
  async function run() {
    setError("");
    setBusy(true);
    const controller = new AbortController();
    request.current = controller;
    try {
      if (mode === "export") {
        const result = await api<{ file: string; filename: string }>(
          "POST",
          "/api/connections/transfer/export",
          { ids: connections.map((c) => c.id), includeSecrets: secrets, passphrase },
          { signal: controller.signal },
        );
        const url = URL.createObjectURL(new Blob([result.file], { type: "application/json" }));
        const a = document.createElement("a");
        a.href = url;
        a.download = result.filename;
        a.click();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
        onClose();
      } else {
        await api<Connection[]>("POST", "/api/connections/transfer/import", { file, passphrase }, { signal: controller.signal });
        onImported();
        onClose();
      }
    } catch (e) {
      if (!controller.signal.aborted) setError(e instanceof Error ? e.message : "Error de transferencia");
    } finally {
      if (!controller.signal.aborted) setBusy(false);
    }
  }
  return (
    <Dialog title={mode === "export" ? "Compartir conexiones" : "Importar conexiones"} onClose={onClose}>
      {mode === "export" ? (
        <>
          <p>Se descargarán {connections.length} perfiles. Las claves privadas SSH y sus passphrases se adjuntan por separado.</p>
          <ul className="transfer-preview">
            {connections.map((c) => (
              <li key={c.id}>
                <strong>{c.name}</strong>
                <span className="muted">
                  {c.host}:{c.port}
                  {c.ssh?.enabled ? " · SSH" : ""}
                </span>
              </li>
            ))}
          </ul>
          <label className="check">
            <input type="checkbox" checked={secrets} onChange={(e) => setSecrets(e.target.checked)} disabled={busy} />
            Incluir contraseñas y certificados TLS en un paquete cifrado
          </label>
          <p className="muted">
            {secrets
              ? "El receptor necesitará la frase de cifrado y su clave SSH. Comparte la frase por otro medio."
              : "Se comparten destinos, usuarios, configuración y referencias de claves. El receptor completa las credenciales y adjunta su archivo OpenSSH o PPK."}
          </p>
        </>
      ) : (
        <>
          <label>
            Archivo .dbyson
            <input
              type="file"
              accept=".dbyson,.json"
              disabled={busy}
              onChange={async (e) => {
                const f = e.target.files?.[0];
                if (!f) return;
                if (f.size > 750 * 1024) {
                  setError("Archivo demasiado grande (máximo 750 KiB)");
                  return;
                }
                setFile(await f.text());
                setFilename(f.name);
                setError("");
              }}
            />
          </label>
          {filename && <p>{filename}</p>}
          {preview && (
            <div className="transfer-preview">
              {preview.encrypted ? (
                <p>Paquete cifrado. Introduce la frase para importarlo.</p>
              ) : (
                preview.profiles?.map((c, i) => (
                  <p key={i}>
                    <strong>{c.name}</strong> · {c.host}:{c.port}
                  </p>
                ))
              )}
            </div>
          )}
          <p className="muted">
            Se crean copias nuevas. Las claves SSH no vienen incluidas: adjúntalas en Editar o usa la ruta del archivo en el servidor
            receptor.
          </p>
        </>
      )}
      {((mode === "export" && secrets) || (mode === "import" && preview?.encrypted)) && (
        <label>
          Frase de cifrado
          <input
            type="password"
            value={passphrase}
            onChange={(e) => setPassphrase(e.target.value)}
            autoComplete="new-password"
            disabled={busy}
          />
          {mode === "export" && <small>Mínimo 12 caracteres.</small>}
        </label>
      )}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <div className="actions">
        <button onClick={onClose}>Cancelar</button>
        <button
          className="primary"
          onClick={() => void run()}
          disabled={busy || (mode === "export" && secrets && passphrase.length < 12) || (mode === "import" && !file)}
        >
          {busy ? "Procesando…" : mode === "export" ? "Descargar archivo" : "Importar perfiles"}
        </button>
      </div>
    </Dialog>
  );
}
