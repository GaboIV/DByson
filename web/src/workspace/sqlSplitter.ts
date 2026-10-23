/**
 * Splits an SQL script into individual statements, respecting single quotes,
 * double quotes, backticks, line comments (-- and #), and block comments.
 */
export function splitSqlStatements(sqlText: string): string[] {
  const statements: string[] = [];
  let current = "";
  let inSingleQuote = false;
  let inDoubleQuote = false;
  let inBacktick = false;
  let inLineComment = false; // -- or #
  let inBlockComment = false; // /* */
  let i = 0;
  const len = sqlText.length;

  while (i < len) {
    const char = sqlText[i];
    const next = i + 1 < len ? sqlText[i + 1] : "";

    if (inLineComment) {
      current += char;
      if (char === "\n" || char === "\r") {
        inLineComment = false;
      }
      i++;
      continue;
    }

    if (inBlockComment) {
      current += char;
      if (char === "*" && next === "/") {
        current += "/";
        i += 2;
        inBlockComment = false;
        continue;
      }
      i++;
      continue;
    }

    if (inSingleQuote) {
      current += char;
      if (char === "\\" && i + 1 < len) {
        current += sqlText[++i];
      } else if (char === "'") {
        if (next === "'") {
          current += "'";
          i++;
        } else {
          inSingleQuote = false;
        }
      }
      i++;
      continue;
    }

    if (inDoubleQuote) {
      current += char;
      if (char === "\\" && i + 1 < len) {
        current += sqlText[++i];
      } else if (char === '"') {
        if (next === '"') {
          current += '"';
          i++;
        } else {
          inDoubleQuote = false;
        }
      }
      i++;
      continue;
    }

    if (inBacktick) {
      current += char;
      if (char === "`") {
        if (next === "`") {
          current += "`";
          i++;
        } else {
          inBacktick = false;
        }
      }
      i++;
      continue;
    }

    // Comment detection outside strings
    if ((char === "-" && next === "-") || char === "#") {
      inLineComment = true;
      current += char;
      i++;
      continue;
    }
    if (char === "/" && next === "*") {
      inBlockComment = true;
      current += "/*";
      i += 2;
      continue;
    }

    // Quote detection
    if (char === "'") {
      inSingleQuote = true;
      current += char;
      i++;
      continue;
    }
    if (char === '"') {
      inDoubleQuote = true;
      current += char;
      i++;
      continue;
    }
    if (char === "`") {
      inBacktick = true;
      current += char;
      i++;
      continue;
    }

    // Semicolon separator
    if (char === ";") {
      current += ";";
      const trimmed = current.trim();
      if (trimmed) {
        statements.push(trimmed);
      }
      current = "";
      i++;
      continue;
    }

    current += char;
    i++;
  }

  const lastTrimmed = current.trim();
  if (lastTrimmed) {
    statements.push(lastTrimmed);
  }

  return statements;
}

/**
 * Extracts a concise human-readable title for a tab from an SQL statement.
 * E.g. "1: SELECT catalogs_country" or "2: UPDATE users"
 */
export function deriveQueryTitle(sql: string, index: number): string {
  // Strip leading comments
  const clean = sql.replace(/\/\*[\s\S]*?\*\/|--.*|#.*/g, "").trim();
  const match = clean.match(/^([A-Za-z]+)\s+(?:.*?FROM|INTO|TABLE|UPDATE)?\s*[`"]?([A-Za-z0-9_]+)?[`"]?/i);
  if (match) {
    const verb = match[1].toUpperCase();
    const target = match[2];
    if (target && !["FROM", "INTO", "WHERE", "SET", "SELECT", "ALL", "DISTINCT", "JOIN"].includes(target.toUpperCase())) {
      return `${index}: ${verb} ${target}`;
    }
    return `${index}: ${verb}`;
  }
  const firstWord = clean.split(/\s+/)[0] || "Query";
  return `${index}: ${firstWord.toUpperCase()}`;
}
