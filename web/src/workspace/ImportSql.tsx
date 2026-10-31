import { useEffect, useRef, useState } from "react";
import { apiUpload, ApiError } from "../api";
import { invalidateConnection } from "./catalog";
import { enc, errorText, formatBytes, type ViewContext } from "./shared";
import { sha256Hex } from "./sha256";

const MAX_UPLOAD = 64 * 1024 * 1024;

interface ImportSummary {
  statements: number;
  failed: number;
  affectedRows: number;
  errors: { statement: number; line: number; sql: string; error: string }[] | null;
  stopped: boolean;
  executionMs: number;
}

interface Challenge {
  confirmationToken: string;
  confirmationTarget: string;
  reason: string;
  preview: string;
}

type Phase = "pick" | "confirm" | "running" | "done";

/**
 * Adminer-style database import: a .sql or .sql.gz file (mysqldump or any script) runs on
 * the server, statement by statement, in the open database. Large dumps are fine; the
 * editor is only offered for small scripts the user wants to review first.
 */
export function ImportSql({ ctx, onClose }: { ctx: ViewContext; onClose: () => void }) {
  const [file, setFile] = useState<File | null>(null);
  const [continueOnError, setContinueOnError] = useState(false);
  const [phase, setPhase] = useState<Phase>("pick");
  const [challenge, setChallenge] = useState<Challenge | null>(null);
  const [typed, setTyped] = useState("");
  const [progress, setProgress] = useState<{ statements: number; failed: number; bytesRead: number } | null>(null);
  const [summary, setSummary] = useState<ImportSummary | null>(null);
  const [runError, setRunError] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const pending = useRef<{ data: ArrayBuffer; digest: string } | null>(null);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);

  const gz = !!file && /\.gz$/i.test(file.name);
  const url = (digest: string) =>
    `/api/connections/${ctx.connection.id}/databases/${enc(ctx.database)}/import-sql?` +
    new URLSearchParams({
      sha256: digest,
      name: file?.name ?? "",
      size: String(file?.size ?? 0),
      ...(continueOnError ? { continueOnError: "1" } : {}),
    });

  function pick(f?: File) {
    setError("");
    setSummary(null);
    setRunError("");
    setChallenge(null);
    setPhase("pick");
    if (!f) return setFile(null);
    if (f.size > MAX_UPLOAD) {
      setFile(null);
      return setError(`El archivo ocupa ${formatBytes(f.size)}; el máximo es 64 MB (comprime el volcado en .gz si hace falta).`);
    }
    setFile(f);
  }

  // Step 1: hash the file and ask the server for a confirmation bound to it.
  async function prepare() {
    if (!file) return;
    setBusy(true);
    setError("");
    try {
      const data = await file.arrayBuffer();
      const digest = await sha256Hex(data);
      pending.current = { data, digest };
      const res = await apiUpload(url(digest), null, {});
      const body = (await res.json()) as Challenge & { needsConfirmation?: boolean };
      if (!body.needsConfirmation) throw new Error("El servidor no pidió confirmación");
      setChallenge(body);
      setTyped("");
      setPhase("confirm");
    } catch (e) {
      setError(errorText(e, "No se pudo preparar la importación"));
    } finally {
      setBusy(false);
    }
  }

  // Step 2: upload once with the confirmation and follow the progress stream.
  async function run() {
    const p = pending.current;
    if (!p || !challenge || !file) return;
    const abort = new AbortController();
    controller.current = abort;
    setPhase("running");
    setProgress({ statements: 0, failed: 0, bytesRead: 0 });
    setRunError("");
    try {
      const res = await apiUpload(
        url(p.digest),
        new Blob([p.data]),
        {
          "X-DByson-Confirmation-Token": challenge.confirmationToken,
          "X-DByson-Confirmation-Name": challenge.confirmationTarget,
        },
        abort.signal,
      );
      const reader = res.body!.pipeThrough(new TextDecoderStream()).getReader();
      let buffer = "";
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += value;
        let nl: number;
        while ((nl = buffer.indexOf("\n")) >= 0) {
          const line = buffer.slice(0, nl).trim();
          buffer = buffer.slice(nl + 1);
          if (!line) continue;
          const event = JSON.parse(line);
          if (event.type === "progress") setProgress(event);
          if (event.type === "done") {
            setSummary(event.summary);
            if (event.error) setRunError(event.error);
          }
        }
      }
      invalidateConnection(ctx.connection.id);
      setPhase("done");
    } catch (e) {
      if (abort.signal.aborted) setRunError("Importación cancelada. Las sentencias ya ejecutadas no se revierten.");
      else setRunError(errorText(e, "La importación falló"));
      if (e instanceof ApiError && e.status === 409) setChallenge(null);
      invalidateConnection(ctx.connection.id);
      setPhase("done");
    } finally {
      pending.current = null;
      controller.current = null;
    }
  }

  if (ctx.readOnly) return <p className="muted">Solo lectura: la importación está bloqueada en esta conexión.</p>;
  if (!ctx.database) return <p className="muted">Abre una base de datos para importar en ella.</p>;

  // A .gz reports decompressed bytes, so its progress has no known end.
  const percent = file && progress && !gz ? Math.min(100, Math.round((progress.bytesRead / file.size) * 100)) : null;

  return (
    <div className="import-sql">
      <p className="muted">
        Ejecuta un volcado o script <strong>.sql</strong> o <strong>.sql.gz</strong> (hasta 64 MB) en <strong>{ctx.database}</strong>, como
        «Importar» de Adminer o <code>mysql &lt; volcado.sql</code>: admite <code>DELIMITER</code>, comentarios de mysqldump y archivos
        comprimidos. Lo que se ejecute no se revierte si una sentencia falla.
      </p>

      {(phase === "pick" || phase === "confirm") && (
        <>
          <label className={`file-drop${file ? " has-file" : ""}`}>
            <strong>{file ? `${file.name} · ${formatBytes(file.size)}` : "Arrastra aquí un archivo .sql o .sql.gz"}</strong>
            <small className="muted">{file ? "Haz clic para elegir otro" : "o haz clic para elegirlo"}</small>
            <input
              type="file"
              accept=".sql,.gz,application/sql,application/gzip,text/plain"
              disabled={busy}
              onChange={(e) => pick(e.target.files?.[0])}
            />
          </label>
          <label className="check">
            <input
              type="checkbox"
              checked={continueOnError}
              disabled={busy || phase === "confirm"}
              onChange={(e) => setContinueOnError(e.target.checked)}
            />
            Continuar aunque falle alguna sentencia
          </label>
          <small className="muted">
            {continueOnError
              ? "Se ejecutan todas las sentencias y al final verás las que fallaron."
              : "La importación se detiene en la primera sentencia que falle (recomendado)."}
          </small>
        </>
      )}

      {phase === "confirm" && challenge && (
        <form
          className="import-confirm"
          onSubmit={(e) => {
            e.preventDefault();
            if (typed === challenge.confirmationTarget) void run();
          }}
        >
          <p className="warn">{challenge.reason}</p>
          <pre className="confirmation-preview">{challenge.preview}</pre>
          <label>
            Escribe «{challenge.confirmationTarget}» para importar
            <input autoFocus autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
          </label>
          <div className="actions">
            <button type="button" onClick={() => setPhase("pick")}>
              Volver
            </button>
            <button className="danger" disabled={typed !== challenge.confirmationTarget}>
              Importar en {challenge.confirmationTarget}
            </button>
          </div>
        </form>
      )}

      {phase === "running" && progress && (
        <div className="import-progress" role="status">
          <div className={`progress-track${percent === null ? " indeterminate" : ""}`}>
            <div className="progress-bar" style={percent === null ? undefined : { width: `${Math.max(3, percent)}%` }} />
          </div>
          <p>
            {progress.statements.toLocaleString()} sentencias ejecutadas
            {progress.failed > 0 && ` · ${progress.failed} con error`}
          </p>
          <button type="button" onClick={() => controller.current?.abort()}>
            Cancelar importación
          </button>
        </div>
      )}

      {phase === "done" && (
        <div className={`test-result ${runError || summary?.failed ? "failure" : "success"}`} role="status">
          <strong>
            {runError
              ? "La importación no terminó"
              : summary?.failed
                ? summary.stopped
                  ? "Importación detenida por un error"
                  : "Importación completada con errores"
                : "Importación completada"}
          </strong>
          {summary && (
            <p>
              {summary.statements.toLocaleString()} sentencias · {summary.affectedRows.toLocaleString()} filas afectadas ·{" "}
              {(summary.executionMs / 1000).toFixed(1)} s{summary.failed > 0 && ` · ${summary.failed} con error`}
            </p>
          )}
          {runError && <p>{runError}</p>}
          {!!summary?.errors?.length && (
            <ol className="import-errors">
              {summary.errors.map((e) => (
                <li key={e.statement}>
                  <span className="muted">
                    Sentencia {e.statement} · línea {e.line}
                  </span>
                  <code>{e.error}</code>
                  <pre>{e.sql}</pre>
                </li>
              ))}
            </ol>
          )}
        </div>
      )}

      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}

      <div className="actions">
        {phase === "pick" && file && !gz && file.size <= 1024 * 1024 && (
          <button
            type="button"
            disabled={busy}
            onClick={async () => {
              ctx.newSQL(await file.text());
              onClose();
            }}
            title="Abre el script en una pestaña SQL para revisarlo antes de ejecutarlo"
          >
            Revisar en el editor
          </button>
        )}
        {phase === "pick" && (
          <button className="primary" disabled={!file || busy} onClick={() => void prepare()}>
            {busy ? "Preparando…" : "Importar…"}
          </button>
        )}
        {phase === "done" && (
          <>
            <button type="button" onClick={() => pick(undefined)}>
              Importar otro archivo
            </button>
            <button className="primary" type="button" onClick={onClose}>
              Cerrar
            </button>
          </>
        )}
      </div>
    </div>
  );
}
