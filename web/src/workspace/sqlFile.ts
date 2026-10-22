// Query tabs are saved and opened as plain .sql files. A small comment header carries the tab's
// name and where it was written, so a file can be reopened (or shared) as the same tab; any
// ordinary .sql script opens too, named after the file.

const HEADER = "-- DByson query: ";
export const MAX_SQL_FILE_BYTES = 1024 * 1024;

export interface SqlFileMeta {
  title: string;
  connection: string;
  database: string;
}

export function buildSqlFile(meta: SqlFileMeta, sql: string): string {
  const oneLine = (s: string) => s.replace(/[\r\n]+/g, " ").trim();
  return [
    `${HEADER}${oneLine(meta.title)}`,
    `-- Connection: ${oneLine(meta.connection)}`,
    `-- Database: ${oneLine(meta.database) || "(none)"}`,
    `-- Saved: ${new Date().toISOString()}`,
    "",
    sql.trimEnd(),
    "",
  ].join("\n");
}

/** Returns the tab title stored in the header (if any) and the SQL without the header. */
export function parseSqlFile(text: string): { title?: string; sql: string } {
  const lines = text.replace(/^﻿/, "").split(/\r?\n/);
  if (!lines[0]?.startsWith(HEADER)) return { sql: lines.join("\n").trimEnd() };
  const title = lines[0].slice(HEADER.length).trim() || undefined;
  let i = 1;
  while (i < lines.length && /^-- (Connection|Database|Saved): /.test(lines[i])) i++;
  if (lines[i] === "") i++;
  return { title, sql: lines.slice(i).join("\n").trimEnd() };
}

export function fileSafeName(name: string): string {
  return (
    name
      .normalize("NFD")
      .replace(/[̀-ͯ]/g, "")
      .replace(/[^\w.-]+/g, "_")
      .replace(/^_+|_+$/g, "")
      .slice(0, 80) || "consulta"
  );
}

export function downloadSql(meta: SqlFileMeta, sql: string) {
  const url = URL.createObjectURL(new Blob([buildSqlFile(meta, sql)], { type: "application/sql" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = `${fileSafeName(meta.title)}.sql`;
  a.click();
  URL.revokeObjectURL(url);
}

/** Lets the user pick a .sql file and resolves with its parsed content, or null if cancelled. */
export function pickSqlFile(): Promise<{ title: string; sql: string } | null> {
  return new Promise((resolve, reject) => {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = ".sql,text/plain,application/sql";
    input.onchange = async () => {
      const file = input.files?.[0];
      if (!file) return resolve(null);
      if (file.size > MAX_SQL_FILE_BYTES)
        return reject(new Error("El archivo supera 1 MiB. Para scripts grandes usa la importación de la base de datos."));
      const parsed = parseSqlFile(await file.text());
      resolve({ title: parsed.title ?? file.name.replace(/\.sql$/i, ""), sql: parsed.sql });
    };
    input.oncancel = () => resolve(null);
    input.click();
  });
}
