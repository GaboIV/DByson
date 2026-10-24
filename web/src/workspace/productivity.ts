export interface ForeignKey {
  name: string;
  table: string;
  columns: string[];
  referencedDatabase: string;
  referencedTable: string;
  referencedColumns: string[];
}

export const ident = (value: string) => "`" + value.replaceAll("`", "``") + "`";
// Hex literals avoid dependence on NO_BACKSLASH_ESCAPES and connection SQL modes.
export const literal = (value: string) =>
  "CONVERT(X'" + [...new TextEncoder().encode(value)].map((v) => v.toString(16).padStart(2, "0")).join("") + "' USING utf8mb4)";

/** RFC 4180 quoting, CRLF/LF and embedded newlines. No silent field/row truncation. */
export function parseCSV(text: string, delimiter = ","): string[][] {
  if (text.charCodeAt(0) === 0xfeff) text = text.slice(1);
  const rows: string[][] = [];
  let row: string[] = [],
    value = "",
    quoted = false,
    closed = false;
  function field() {
    row.push(value);
    value = "";
    closed = false;
  }
  function line() {
    field();
    rows.push(row);
    row = [];
  }
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (quoted) {
      if (c === '"') {
        if (text[i + 1] === '"') {
          value += '"';
          i++;
        } else {
          quoted = false;
          closed = true;
        }
      } else value += c;
    } else if (c === delimiter) field();
    else if (c === "\r" || c === "\n") {
      if (c === "\r" && text[i + 1] === "\n") i++;
      line();
    } else if (c === '"' && value === "" && !closed) quoted = true;
    else {
      if (closed || c === '"') throw new Error("CSV inválido: comillas o caracteres tras el cierre de un campo.");
      value += c;
    }
  }
  if (quoted) throw new Error("CSV inválido: faltan comillas de cierre.");
  if (value || row.length || closed) line();
  if (rows.length < 2) throw new Error("Incluye una cabecera y al menos una fila.");
  if (rows.length > 5001) throw new Error("El máximo es de 5000 filas por importación.");
  if (rows.some((r) => r.length !== rows[0].length)) throw new Error("Las filas tienen distinto número de columnas.");
  return rows;
}
