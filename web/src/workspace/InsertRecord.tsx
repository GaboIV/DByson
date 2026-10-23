import { useState, type FormEvent } from "react";
import { api, type TableSchema } from "../api";
import { RowEditor } from "../RowEditor";
import { Spinner } from "../Spinner";
import { invalidateTables } from "./catalog";
import { emptyRowValues, errorText, tablePath, writableValues, type ViewContext } from "./shared";

interface Props {
  ctx: ViewContext;
  schema?: TableSchema;
  onClose: () => void;
  onDirtyChange: (dirty: boolean) => void;
}

export function InsertRecord({ ctx, schema, onClose, onDirtyChange }: Props) {
  // A fresh form each time a record is saved (the key forces RowEditor to reset its baseline).
  const [round, setRound] = useState(0);
  const [values, setValues] = useState<Record<string, unknown> | null>(null);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");

  if (ctx.readOnly) return <div className="alert">Solo lectura activo. Desactiva «Sólo ver» para editar si tu perfil lo permite.</div>;
  if (!schema) return <Spinner block label="Preparando campos…" />;
  const current = values ?? emptyRowValues(schema);

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!schema) return;
    setError("");
    try {
      const r = await api<{ executedSql: string }>("POST", `${tablePath(ctx)}/rows`, { values: writableValues(schema, current) });
      ctx.reportSql(r.executedSql);
      setNotice("Registro guardado correctamente.");
      setValues(null);
      setRound((n) => n + 1);
      onDirtyChange(false);
      invalidateTables(ctx.connection.id, ctx.database); // row counts changed
    } catch (err) {
      setError(errorText(err, "Error guardando el registro"));
    }
  }

  return (
    <div className="insert-record-view">
      {notice && (
        <p className="okmsg" role="status">
          {notice}
        </p>
      )}
      {error && <div className="alert alert-error">{error}</div>}
      <RowEditor
        key={round}
        schema={schema}
        mode="insert"
        values={current}
        onChange={setValues}
        onSubmit={save}
        onCancel={onClose}
        onDirtyChange={onDirtyChange}
      />
    </div>
  );
}
