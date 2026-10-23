import type { Completion, CompletionContext, CompletionResult } from "@codemirror/autocomplete";

export interface SqlTableInfo {
  name: string;
  rows?: number;
}

const COMMON_SQL_KEYWORDS = [
  "SELECT", "FROM", "WHERE", "JOIN", "LEFT JOIN", "INNER JOIN", "RIGHT JOIN",
  "CROSS JOIN", "ON", "GROUP BY", "ORDER BY", "HAVING", "LIMIT", "OFFSET",
  "INSERT INTO", "VALUES", "UPDATE", "SET", "DELETE FROM", "EXPLAIN", "SHOW TABLES",
  "AND", "OR", "NOT", "IN", "LIKE", "IS NULL", "IS NOT NULL", "BETWEEN",
  "AS", "ASC", "DESC", "DISTINCT", "CASE", "WHEN", "THEN", "ELSE", "END",
  "UNION", "UNION ALL"
];

const COMMON_SQL_FUNCTIONS = [
  "COUNT(*)", "COUNT()", "SUM()", "AVG()", "MIN()", "MAX()",
  "NOW()", "CURDATE()", "CONCAT()", "COALESCE()", "IFNULL()",
  "DATE()", "DATEDIFF()", "LOWER()", "UPPER()", "TRIM()", "LENGTH()", "ROUND()"
];

/**
 * Extracts tables and aliases from SQL text.
 * E.g. "FROM core_agreement a JOIN core_document d" ->
 * { tables: ["core_agreement", "core_document"], aliases: { a: "core_agreement", d: "core_document" } }
 */
export function extractTablesAndAliases(sql: string): {
  tables: string[];
  aliases: Record<string, string>;
} {
  const tables: string[] = [];
  const aliases: Record<string, string> = {};

  const clean = sql.replace(/\/\*[\s\S]*?\*\/|--.*|#.*/g, " ");

  const regex = /\b(?:FROM|JOIN|INTO|UPDATE)\s+([`"'\w.]+)(?:\s+(?:AS\s+)?([`"'\w]+))?/gi;
  let match: RegExpExecArray | null;

  const stopWords = new Set([
    "WHERE", "JOIN", "LEFT", "RIGHT", "INNER", "OUTER", "CROSS", "ON",
    "GROUP", "ORDER", "LIMIT", "HAVING", "SET", "VALUES", "SELECT", "UNION",
    "AND", "OR", "NOT", "IN", "AS", "BY"
  ]);

  while ((match = regex.exec(clean)) !== null) {
    const rawTable = match[1].replace(/[`"']/g, "");
    const tableName = rawTable.includes(".") ? rawTable.split(".").pop()! : rawTable;
    let alias = match[2] ? match[2].replace(/[`"']/g, "") : null;

    if (alias && stopWords.has(alias.toUpperCase())) {
      alias = null;
    }

    if (tableName && !stopWords.has(tableName.toUpperCase())) {
      tables.push(tableName);
      aliases[tableName.toLowerCase()] = tableName;
      if (alias) {
        aliases[alias.toLowerCase()] = tableName;
      }
    }
  }

  return {
    tables: Array.from(new Set(tables)),
    aliases,
  };
}

export type SqlContextType = "table" | "column" | "root";

export function determineSqlContext(textBeforeCursor: string): {
  type: SqlContextType;
  dotQualifier?: string;
  prefix: string;
  wordFrom: number;
} {
  // Check if after dot (qualified column): e.g. "a.", "table.", "t.co"
  const dotMatch = textBeforeCursor.match(/([`"'\w]+)\.([`"'\w]*)$/);
  if (dotMatch) {
    const qualifier = dotMatch[1].replace(/[`"']/g, "");
    const prefix = dotMatch[2].replace(/[`"']/g, "");
    const wordFrom = textBeforeCursor.length - prefix.length;
    return {
      type: "column",
      dotQualifier: qualifier,
      prefix,
      wordFrom,
    };
  }

  const wordMatch = textBeforeCursor.match(/([`"'\w]+)$/);
  const prefix = wordMatch ? wordMatch[1].replace(/[`"']/g, "") : "";
  const wordFrom = textBeforeCursor.length - prefix.length;

  const beforeWord = textBeforeCursor.slice(0, wordFrom).trimEnd();

  const cleanBefore = beforeWord
    .replace(/\/\*[\s\S]*?\*\/|--.*|#.*/g, " ")
    .replace(/'(?:[^'\\]|\\.)*'/g, "''")
    .replace(/"(?:[^"\\]|\\.)*"/g, '""');

  const lastKeywordMatch = cleanBefore.match(/\b([A-Za-z]+)\s*$/i);
  const lastKeyword = lastKeywordMatch ? lastKeywordMatch[1].toUpperCase() : "";

  // Table-expecting positions
  if (["FROM", "JOIN", "INTO", "UPDATE", "TABLE"].includes(lastKeyword)) {
    return { type: "table", prefix, wordFrom };
  }

  // Column-expecting positions
  if (
    [
      "SELECT", "WHERE", "ON", "AND", "OR", "HAVING", "BY", "SET",
      "CASE", "WHEN", "THEN", "ELSE", "LIKE", "IN", "BETWEEN",
      "DISTINCT", "ALL"
    ].includes(lastKeyword)
  ) {
    return { type: "column", prefix, wordFrom };
  }

  if (!cleanBefore || cleanBefore.endsWith(";")) {
    return { type: "root", prefix, wordFrom };
  }

  return { type: "column", prefix, wordFrom };
}

/**
 * Creates a smart, context-aware SQL autocompletion source.
 */
export function createSqlCompletionSource(options: {
  getTables: () => SqlTableInfo[];
  getColumns: (tableName: string) => string[];
  defaultTable?: string;
}) {
  return (context: CompletionContext): CompletionResult | null => {
    const textBefore = context.state.sliceDoc(0, context.pos);
    const fullDoc = context.state.doc.toString();

    // Check what word is currently being typed
    const match = context.matchBefore(/[\w`"$.]*/);
    if (!match && !context.explicit) return null;

    const { type, dotQualifier, prefix, wordFrom } = determineSqlContext(textBefore);

    const from = wordFrom;
    const lowerPrefix = prefix.toLowerCase();

    // Case 1: Dot Qualifier (e.g. "a." or "core_agreement.")
    if (dotQualifier) {
      const { aliases } = extractTablesAndAliases(fullDoc);
      const targetTable = aliases[dotQualifier.toLowerCase()] || dotQualifier;
      const columns = options.getColumns(targetTable);

      const completions: Completion[] = columns
        .filter((col) => col.toLowerCase().includes(lowerPrefix))
        .map((col) => ({
          label: col,
          type: "property",
          detail: targetTable,
          boost: 90,
        }));

      return {
        from,
        options: completions,
        validFor: /^[\w$]*$/,
      };
    }

    // Case 2: TABLE CONTEXT (after FROM, JOIN, INTO, UPDATE)
    if (type === "table") {
      const allTables = options.getTables();
      const completions: Completion[] = allTables
        .filter((t) => t.name.toLowerCase().includes(lowerPrefix))
        .map((t) => ({
          label: t.name,
          type: "class",
          detail: t.rows !== undefined ? `tabla (${t.rows.toLocaleString()} filas)` : "tabla",
          boost: 95,
        }));

      return {
        from,
        options: completions,
        validFor: /^[\w$]*$/,
      };
    }

    // Case 3: COLUMN CONTEXT (after SELECT, WHERE, ON, ORDER BY, GROUP BY, etc.)
    const { tables, aliases } = extractTablesAndAliases(fullDoc);

    // If query references tables, use those. Otherwise fallback to defaultTable.
    const activeTables = tables.length > 0 ? tables : options.defaultTable ? [options.defaultTable] : [];

    const completions: Completion[] = [];
    const seenCols = new Set<string>();

    for (const table of activeTables) {
      const columns = options.getColumns(table);
      for (const col of columns) {
        if (!col.toLowerCase().includes(lowerPrefix)) continue;
        const key = `${table}.${col}`;
        if (seenCols.has(key)) continue;
        seenCols.add(key);

        completions.push({
          label: col,
          type: "property",
          detail: activeTables.length > 1 ? `${table}` : undefined,
          boost: 90,
        });
      }
    }

    // Also suggest aliases (e.g. "a." or "d.") if there are joins
    if (tables.length > 1) {
      for (const [alias, realTable] of Object.entries(aliases)) {
        if (alias !== realTable.toLowerCase() && alias.startsWith(lowerPrefix)) {
          completions.push({
            label: `${alias}.`,
            type: "namespace",
            detail: `alias de ${realTable}`,
            boost: 85,
          });
        }
      }
    }

    // Add SQL functions matching prefix
    for (const fn of COMMON_SQL_FUNCTIONS) {
      if (fn.toLowerCase().startsWith(lowerPrefix)) {
        completions.push({
          label: fn,
          type: "function",
          detail: "función SQL",
          boost: 40,
        });
      }
    }

    // Add keywords matching prefix
    for (const kw of COMMON_SQL_KEYWORDS) {
      if (kw.toLowerCase().startsWith(lowerPrefix)) {
        completions.push({
          label: kw,
          type: "keyword",
          boost: 30,
        });
      }
    }

    return {
      from,
      options: completions,
      validFor: /^[\w$]*$/,
    };
  };
}
