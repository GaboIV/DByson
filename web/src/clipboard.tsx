import { useEffect, useRef, useState } from "react";
import type { QueryResult } from "./api";

export type DataRow = QueryResult["rows"][number];
export const cellText = (value: DataRow[number]) => value === null ? "NULL" : String(value);

export function rowJSON(columns: string[], row: DataRow) {
  // Repeated column aliases in SQL results must not silently discard values.
  const value = new Set(columns).size === columns.length
    ? Object.fromEntries(columns.map((column, index) => [column, row[index]]))
    : { columns, values: row };
  return JSON.stringify(value, null, 2);
}

export function useClipboard() {
  const [notice, setNotice] = useState("");
  const timeout = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timeout.current), []);

  async function copy(text: string, label = "Contenido copiado") {
    try {
      if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(text);
      else {
        const field = document.createElement("textarea");
        field.className = "clipboard-buffer"; field.value = text;
        const previous = document.activeElement;
        document.body.append(field); field.select();
        const success = document.execCommand("copy"); field.remove();
        if (previous instanceof HTMLElement) previous.focus();
        if (!success) throw new Error("Clipboard unavailable");
      }
      setNotice(label);
    } catch { setNotice("No se pudo copiar. Selecciona el texto y usa Ctrl+C."); }
    clearTimeout(timeout.current); timeout.current = setTimeout(() => setNotice(""), 3500);
  }
  return { copy, copyNotice: <span className="copy-notice" role="status" aria-live="polite">{notice}</span> };
}
