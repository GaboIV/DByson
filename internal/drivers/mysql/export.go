package mysql

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Stream without materializing a table. Cancellation precedes Close on failure
// so the driver does not drain a huge remaining result over the network.
func streamTable(ctx context.Context, db rowQuerier, database, table string, consume func([]string, []any) error, header func([]string) error) error {
	ctx, cancel := context.WithCancel(ctx)
	rows, err := db.QueryContext(ctx, "SELECT * FROM "+EscapeIdent(database)+"."+EscapeIdent(table))
	if err != nil {
		cancel()
		return err
	}
	defer func() { cancel(); rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	if header != nil {
		if err := header(cols); err != nil {
			return err
		}
	}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		values, err := scanRow(rows, len(cols))
		if err != nil {
			return err
		}
		if err := consume(cols, values); err != nil {
			return err
		}
	}
	return rows.Err()
}

func ExportCSV(ctx context.Context, db *sql.DB, database, table string, w io.Writer) error {
	cw := csv.NewWriter(w)
	writeRow := func(fields []string) error {
		for i, f := range fields {
			fields[i] = csvSafeField(f)
		}
		return cw.Write(fields)
	}
	err := streamTable(ctx, db, database, table, func(_ []string, values []any) error {
		values = displayRow(values)
		record := make([]string, len(values))
		for i, v := range values {
			if v == nil {
				record[i] = "NULL"
			} else {
				record[i] = fmt.Sprint(v)
			}
		}
		return writeRow(record)
	}, writeRow)
	if err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// csvSafeField neutralizes spreadsheet formula injection: a cell a spreadsheet
// would evaluate (it starts with = + - @, tab or carriage return) is prefixed
// with a single quote, unless it is a plain number, which is left untouched.
func csvSafeField(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		if _, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return s
		}
		return "'" + s
	}
	return s
}

func ExportJSON(ctx context.Context, db *sql.DB, database, table string, w io.Writer) error {
	bw := bufio.NewWriterSize(w, 32<<10)
	if _, err := bw.WriteString("["); err != nil {
		return err
	}
	first := true
	err := streamTable(ctx, db, database, table, func(cols []string, values []any) error {
		if !first {
			if _, err := bw.WriteString(","); err != nil {
				return err
			}
		}
		first = false
		item := make(map[string]any, len(cols))
		values = displayRow(values)
		for i, col := range cols {
			item[col] = values[i]
		}
		return json.NewEncoder(bw).Encode(item)
	}, nil)
	if err != nil {
		return err
	}
	if _, err := bw.WriteString("]\n"); err != nil {
		return err
	}
	return bw.Flush()
}

// checkedWriter remembers the first write failure; subsequent writes are no-ops.
type checkedWriter struct {
	io.Writer
	err error
}

func (w *checkedWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.Writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	w.err = err
	return n, err
}

func dumpValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "NULL"
	case []byte:
		return "CONVERT(X'" + hex.EncodeToString(v) + "' USING binary)"
	case time.Time:
		return formatSQLValue(v.Format("2006-01-02 15:04:05.999999"))
	default:
		return formatSQLValue(value)
	}
}

func ExportSQL(ctx context.Context, db *sql.DB, database string, tables []string, includeData, includeStructure bool, writer io.Writer) error {
	bw := bufio.NewWriterSize(writer, 32<<10)
	w := &checkedWriter{Writer: bw}
	// Quotes also work for names containing newlines; avoid injecting dump comments.
	fmt.Fprintf(w, "-- DByson SQL Dump\n-- Generated: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintln(w, "SET @DBYSON_OLD_SQL_MODE=@@SQL_MODE, @DBYSON_OLD_FK=@@FOREIGN_KEY_CHECKS;")
	fmt.Fprintln(w, "SET SQL_MODE='NO_AUTO_VALUE_ON_ZERO';\nSET NAMES utf8mb4;\nSET FOREIGN_KEY_CHECKS=0;")
	for _, table := range tables {
		if err := ctx.Err(); err != nil {
			return err
		}
		if includeStructure {
			var name, ddl string
			if err := db.QueryRowContext(ctx, "SHOW CREATE TABLE "+EscapeIdent(database)+"."+EscapeIdent(table)).Scan(&name, &ddl); err != nil {
				return err
			}
			fmt.Fprintf(w, "DROP TABLE IF EXISTS %s;\n%s;\n", EscapeIdent(table), ddl)
			if w.err != nil {
				return w.err
			}
		}
		if includeData {
			err := streamTable(ctx, db, database, table, func(cols []string, values []any) error {
				names := make([]string, len(cols))
				vals := make([]string, len(values))
				for i, c := range cols {
					names[i] = EscapeIdent(c)
					vals[i] = dumpValue(values[i])
				}
				fmt.Fprintf(w, "INSERT INTO %s (%s) VALUES (%s);\n", EscapeIdent(table), strings.Join(names, ","), strings.Join(vals, ","))
				return w.err
			}, nil)
			if err != nil {
				return err
			}
		}
	}
	fmt.Fprintln(w, "SET FOREIGN_KEY_CHECKS=@DBYSON_OLD_FK;\nSET SQL_MODE=@DBYSON_OLD_SQL_MODE;")
	if w.err != nil {
		return w.err
	}
	return bw.Flush()
}
