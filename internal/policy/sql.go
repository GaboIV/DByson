package policy

import (
	"errors"
	"strings"
	"unicode"
)

type SQLClassification struct {
	IsDestructive bool   `json:"isDestructive"`
	Operation     string `json:"operation"`
	Reason        string `json:"reason"`
}
type sqlToken struct {
	text   string
	depth  int
	quoted bool
}

// tokenizeSQL is conservative, not a general SQL parser. Executable comments,
// multiple statements and SQL-mode-dependent escaping are deliberately rejected.
func tokenizeSQL(sql string) ([]sqlToken, error) {
	var tokens []sqlToken
	depth, ended := 0, false
	for i := 0; i < len(sql); {
		c := sql[i]
		if c == 0 {
			return nil, errors.New("SQL contains a NUL byte")
		}
		if unicode.IsSpace(rune(c)) {
			i++
			continue
		}
		if c == '#' || (c == '-' && i+2 < len(sql) && sql[i+1] == '-' && sql[i+2] <= ' ') {
			for i < len(sql) && sql[i] != '\n' && sql[i] != '\r' {
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(sql) && sql[i+1] == '*' {
			if i+2 < len(sql) && (sql[i+2] == '!' || ((sql[i+2] == 'M' || sql[i+2] == 'm') && i+3 < len(sql) && sql[i+3] == '!')) {
				return nil, errors.New("executable SQL comments are not supported")
			}
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				return nil, errors.New("unterminated SQL comment")
			}
			i += end + 4
			continue
		}
		if ended {
			return nil, errors.New("execute one SQL statement at a time")
		}
		if c == ';' {
			if depth != 0 {
				return nil, errors.New("invalid statement separator")
			}
			ended = true
			i++
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			quote, start := c, i
			i++
			closed := false
			for i < len(sql) {
				if sql[i] == 0 || sql[i] == '\\' {
					return nil, errors.New("use doubled quotes instead of backslash escapes in SQL literals")
				}
				if sql[i] == quote {
					if i+1 < len(sql) && sql[i+1] == quote {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, errors.New("unterminated quoted SQL text")
			}
			tokens = append(tokens, sqlToken{text: sql[start:i], depth: depth, quoted: true})
			continue
		}
		if c == '(' {
			tokens = append(tokens, sqlToken{text: "(", depth: depth})
			depth++
			i++
			continue
		}
		if c == ')' {
			depth--
			if depth < 0 {
				return nil, errors.New("unbalanced SQL parentheses")
			}
			tokens = append(tokens, sqlToken{text: ")", depth: depth})
			i++
			continue
		}
		start := i
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' {
			for i < len(sql) && ((sql[i] >= 'a' && sql[i] <= 'z') || (sql[i] >= 'A' && sql[i] <= 'Z') || (sql[i] >= '0' && sql[i] <= '9') || sql[i] == '_' || sql[i] == '$') {
				i++
			}
		} else {
			i++
		}
		tokens = append(tokens, sqlToken{text: strings.ToUpper(sql[start:i]), depth: depth})
	}
	if depth != 0 {
		return nil, errors.New("unbalanced SQL parentheses")
	}
	if len(tokens) == 0 {
		return nil, errors.New("query cannot be empty")
	}
	return tokens, nil
}
func ValidateSQL(sql string) error { _, err := tokenizeSQL(sql); return err }
func operation(tokens []sqlToken) string {
	if len(tokens) == 0 || tokens[0].quoted {
		return "UNKNOWN"
	}
	if tokens[0].text != "WITH" {
		return tokens[0].text
	}
	for _, t := range tokens[1:] {
		if t.depth == 0 && !t.quoted {
			switch t.text {
			case "SELECT", "UPDATE", "DELETE", "INSERT", "REPLACE":
				return t.text
			}
		}
	}
	return "UNKNOWN"
}
func ClassifySQL(sql string) SQLClassification {
	tokens, err := tokenizeSQL(sql)
	if err != nil {
		return SQLClassification{true, "UNKNOWN", err.Error()}
	}
	op := operation(tokens)
	switch op {
	case "SELECT", "SHOW", "DESCRIBE", "DESC", "EXPLAIN":
		for _, t := range tokens {
			if !t.quoted && t.text == "INTO" {
				return SQLClassification{true, op, "La sentencia escribe fuera del resultado de consulta (INTO)."}
			}
		}
		return SQLClassification{Operation: op}
	case "UPDATE", "DELETE":
		for _, t := range tokens {
			if t.depth == 0 && !t.quoted && t.text == "WHERE" {
				return SQLClassification{true, op, "Modifica o elimina registros; revisa la condicion WHERE antes de continuar."}
			}
		}
		return SQLClassification{true, op, "Sin WHERE en la sentencia principal: puede afectar a todos los registros."}
	default:
		return SQLClassification{true, op, "La operacion puede modificar datos, estructura o estado del servidor."}
	}
}

// hasOptimizerHint reports whether sql carries a /*+ ... */ optimizer hint
// outside any string or quoted identifier. The tokenizer treats such a hint as
// an ordinary comment, so SET_VAR() inside one would otherwise slip past the
// read-only checks and change session variables; read-only refuses them.
func hasOptimizerHint(sql string) bool {
	for i := 0; i < len(sql); {
		switch c := sql[i]; {
		case c == '\'' || c == '"' || c == '`':
			i++
			for i < len(sql) {
				if sql[i] == c {
					if i+1 < len(sql) && sql[i+1] == c {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case c == '/' && i+2 < len(sql) && sql[i+1] == '*' && sql[i+2] == '+':
			return true
		default:
			i++
		}
	}
	return false
}

// Read-only uses an allowlist, backed by READ ONLY on every physical connection.
func IsReadOnlyViolation(sql string) bool {
	if hasOptimizerHint(sql) {
		return true
	}
	tokens, err := tokenizeSQL(sql)
	if err != nil {
		return true
	}
	op := operation(tokens)
	switch op {
	case "SELECT", "SHOW", "DESCRIBE", "DESC":
	case "EXPLAIN":
		for _, t := range tokens {
			if !t.quoted && (t.text == "ANALYZE" || t.text == "UPDATE" || t.text == "DELETE" || t.text == "INSERT" || t.text == "REPLACE") {
				return true
			}
		}
	default:
		return true
	}
	for i, t := range tokens {
		if t.quoted {
			// A quoted identifier used as a function call, e.g. `SLEEP`(9) or, under
			// ANSI_QUOTES, "SLEEP"(9). String literals ('...') are not function names.
			var name string
			switch {
			case strings.HasPrefix(t.text, "`"):
				name = strings.ReplaceAll(strings.Trim(t.text, "`"), "``", "`")
			case strings.HasPrefix(t.text, `"`):
				name = strings.ReplaceAll(strings.Trim(t.text, `"`), `""`, `"`)
			default:
				continue
			}
			if i+1 < len(tokens) && tokens[i+1].text == "(" && deniedFunction(strings.ToUpper(name)) {
				return true
			}
			continue
		}
		switch t.text {
		case "INTO", "OUTFILE", "DUMPFILE", "FOR", "LOCK", "SET":
			return true
		}
		if deniedFunction(t.text) {
			return true
		}
		if t.text == ":" && i+1 < len(tokens) && tokens[i+1].text == "=" {
			return true
		}
	}
	return false
}

// deniedFunction names functions that wait, lock, read files or stall the server
// and so never belong on a read-only connection, whatever quoting is in effect.
func deniedFunction(name string) bool {
	switch name {
	case "GET_LOCK", "RELEASE_LOCK", "RELEASE_ALL_LOCKS", "LOAD_FILE", "SLEEP", "BENCHMARK",
		"MASTER_POS_WAIT", "SOURCE_POS_WAIT", "WAIT_FOR_EXECUTED_GTID_SET", "WAIT_UNTIL_SQL_THREAD_AFTER_GTIDS":
		return true
	}
	return false
}
func ReturnsRows(sql string) bool {
	tokens, err := tokenizeSQL(sql)
	if err != nil {
		return false
	}
	switch operation(tokens) {
	case "SELECT", "SHOW", "DESCRIBE", "DESC", "EXPLAIN":
		return true
	}
	return false
}
