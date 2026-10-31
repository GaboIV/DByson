package mysql

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxImportStatementBytes bounds one statement of an imported script (mysqldump
// writes extended INSERTs of a few MiB; MySQL's own max_allowed_packet is 64 MiB).
const MaxImportStatementBytes = 64 << 20

// ScriptSplitter reads a SQL script the way the mysql command-line client does:
// statements end at the current delimiter (";" by default, changed with DELIMITER),
// quotes may use backslash escapes as mysqldump writes them, plain comments are
// dropped and executable comments (/*!40101 … */) are kept for the server.
type ScriptSplitter struct {
	r         *bufio.Reader
	delimiter string
	line      int // 1-based line where the next statement starts
	readBytes int64
}

func NewScriptSplitter(r io.Reader) *ScriptSplitter {
	return &ScriptSplitter{r: bufio.NewReaderSize(r, 64<<10), delimiter: ";", line: 1}
}

// BytesRead reports how much of the input has been consumed (for progress).
func (s *ScriptSplitter) BytesRead() int64 { return s.readBytes }

// Next returns the next statement without its delimiter and the line it starts on,
// or io.EOF once the script is exhausted.
func (s *ScriptSplitter) Next() (string, int, error) {
	var stmt bytes.Buffer
	var quote byte   // ', " or ` while inside one
	content := false // stmt holds more than whitespace
	atLineStart := true
	startLine, line := 0, s.line
	read := func() (byte, error) {
		c, err := s.r.ReadByte()
		if err == nil {
			s.readBytes++
		}
		return c, err
	}
	emit := func() (string, int, error) {
		s.line = line
		text := strings.TrimSpace(stmt.String())
		if !utf8.ValidString(text) {
			return "", startLine, fmt.Errorf("línea %d: el archivo no está en UTF-8", startLine)
		}
		return text, startLine, nil
	}
	for {
		if stmt.Len() > MaxImportStatementBytes {
			return "", startLine, fmt.Errorf("línea %d: la sentencia supera %d MiB", startLine, MaxImportStatementBytes>>20)
		}
		c, err := read()
		if err == io.EOF {
			if quote != 0 {
				return "", startLine, fmt.Errorf("línea %d: texto entre comillas sin cerrar", startLine)
			}
			if content {
				return emit()
			}
			s.line = line
			return "", 0, io.EOF
		}
		if err != nil {
			return "", 0, err
		}
		if quote != 0 {
			stmt.WriteByte(c)
			switch {
			case c == '\n':
				line++
			case c == '\\' && quote != '`':
				n, err := read()
				if err != nil {
					return "", startLine, fmt.Errorf("línea %d: texto entre comillas sin cerrar", startLine)
				}
				if n == '\n' {
					line++
				}
				stmt.WriteByte(n)
			case c == quote:
				quote = 0
			}
			continue
		}
		// DELIMITER is a client command, valid only as the first word of a statement.
		if atLineStart && !content && (c == 'd' || c == 'D') {
			rest, _ := s.r.Peek(9)
			if len(rest) == 9 && strings.EqualFold(string(rest[:8]), "ELIMITER") && strings.IndexByte(" \t\r\n", rest[8]) >= 0 {
				lineText, err := s.r.ReadString('\n')
				s.readBytes += int64(len(lineText))
				if err != nil && err != io.EOF {
					return "", 0, err
				}
				if lineText != "" && lineText[len(lineText)-1] == '\n' {
					line++
				}
				d := strings.TrimSpace(lineText[8:])
				if d == "" || strings.ContainsAny(d, "'\"`\\") {
					return "", line, fmt.Errorf("línea %d: DELIMITER no válido", line-1)
				}
				s.delimiter = d
				atLineStart = true
				continue
			}
		}
		if c != ' ' && c != '\t' && c != '\r' {
			atLineStart = c == '\n'
		}
		switch {
		case c == '\n':
			line++
			if content {
				stmt.WriteByte(c)
			}
			continue
		case c == ' ' || c == '\t' || c == '\r':
			if content {
				stmt.WriteByte(c)
			}
			continue
		case c == '#' || (c == '-' && s.peekIs("- ", "-\t", "-\n", "-\r")):
			// Line comment: dropped up to (not including) the newline.
			for {
				n, err := s.r.Peek(1)
				if err != nil || n[0] == '\n' {
					break
				}
				_, _ = read()
			}
			continue
		case c == '/' && s.peekIs("*"):
			_, _ = read()
			executable := s.peekIs("!", "M!", "+") // executable comment or optimizer hint
			var comment bytes.Buffer
			comment.WriteString("/*")
			closed := false
			for prev := byte(0); ; {
				n, err := read()
				if err != nil {
					break
				}
				comment.WriteByte(n)
				if n == '\n' {
					line++
				}
				if prev == '*' && n == '/' {
					closed = true
					break
				}
				prev = n
			}
			if !closed {
				return "", startLine, fmt.Errorf("línea %d: comentario sin cerrar", line)
			}
			if executable {
				if !content {
					startLine = line
				}
				stmt.Write(comment.Bytes())
				content = true
			} else if content {
				stmt.WriteByte(' ')
			}
			continue
		}
		if c == s.delimiter[0] && s.peekIs(s.delimiter[1:]) {
			for range len(s.delimiter) - 1 {
				_, _ = read()
			}
			if content {
				return emit()
			}
			continue
		}
		if !content {
			content, startLine = true, line
		}
		if c == '\'' || c == '"' || c == '`' {
			quote = c
		}
		stmt.WriteByte(c)
	}
}

// peekIs reports whether the upcoming bytes start with any of the given prefixes
// (an empty prefix always matches).
func (s *ScriptSplitter) peekIs(prefixes ...string) bool {
	for _, p := range prefixes {
		if p == "" {
			return true
		}
		if b, err := s.r.Peek(len(p)); err == nil && string(b) == p {
			return true
		}
	}
	return false
}

// ImportError is one failed statement of an import.
type ImportError struct {
	Statement int    `json:"statement"`
	Line      int    `json:"line"`
	SQL       string `json:"sql"`
	Error     string `json:"error"`
}

// ImportSummary is the outcome of running a script.
type ImportSummary struct {
	Statements   int           `json:"statements"`
	Failed       int           `json:"failed"`
	AffectedRows int64         `json:"affectedRows"`
	Errors       []ImportError `json:"errors"`
	Stopped      bool          `json:"stopped"` // stopped at the first error
	ExecutionMs  int64         `json:"executionMs"`
}

const maxReportedImportErrors = 50

// RunScript executes every statement of r on one connection, in order, after selecting
// database. It stops at the first failure when stopOnError is set; progress is called
// after each statement with the summary so far and the bytes consumed.
func RunScript(ctx context.Context, conn *sql.Conn, database string, r io.Reader, stopOnError bool, progress func(ImportSummary, int64)) (ImportSummary, error) {
	var sum ImportSummary
	start := time.Now()
	defer func() { sum.ExecutionMs = time.Since(start).Milliseconds() }()
	if database != "" {
		if _, err := conn.ExecContext(ctx, "USE "+EscapeIdent(database)); err != nil {
			return sum, err
		}
	}
	split := NewScriptSplitter(r)
	for {
		stmt, line, err := split.Next()
		if errors.Is(err, io.EOF) {
			sum.ExecutionMs = time.Since(start).Milliseconds()
			return sum, nil
		}
		if err != nil {
			return sum, err
		}
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		sum.Statements++
		res, execErr := conn.ExecContext(ctx, stmt)
		if execErr == nil {
			if n, err := res.RowsAffected(); err == nil && n > 0 {
				sum.AffectedRows += n
			}
		} else {
			if ctx.Err() != nil {
				return sum, ctx.Err()
			}
			sum.Failed++
			if len(sum.Errors) < maxReportedImportErrors {
				sum.Errors = append(sum.Errors, ImportError{Statement: sum.Statements, Line: line, SQL: preview(stmt, 300), Error: execErr.Error()})
			}
			if stopOnError {
				sum.Stopped = true
				sum.ExecutionMs = time.Since(start).Milliseconds()
				return sum, nil
			}
		}
		if progress != nil {
			progress(sum, split.BytesRead())
		}
	}
}

func preview(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
