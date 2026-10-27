package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

const MaxResultRows = 1000
const MaxResultBytes = 8 << 20

// MaxBrowseExecution caps how long the browse and count queries may run on the
// database server. Those paths scan user-chosen filters over a whole table, so
// a caller that has already given up (its HTTP context cancelled) must not leave
// the query scanning on the server. See boundedRead.
const MaxBrowseExecution = 30 * time.Second

// execBoundMillis is the server-side execution bound for browse/count: the
// request's remaining deadline, capped at MaxBrowseExecution and floored at 1 s
// (an already-expired deadline yields a non-positive remainder, hence the floor).
func execBoundMillis(ctx context.Context) int64 {
	ms := MaxBrowseExecution.Milliseconds()
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl).Milliseconds(); rem < ms {
			ms = rem
		}
	}
	if ms < 1000 {
		ms = 1000
	}
	return ms
}

// boundedRead wraps a plain SELECT so the database server aborts it at the
// execution bound even if the client has disconnected. MySQL honors the
// MAX_EXECUTION_TIME optimizer hint; MariaDB ignores that hint and instead
// honors SET STATEMENT max_statement_time (seconds). Sending SET STATEMENT to
// MySQL is a syntax error, so the hint — which both accept and only MySQL acts
// on — is the default, and SET STATEMENT is used only for a confirmed MariaDB.
func boundedRead(ctx context.Context, mariaDB bool, selectStmt string) string {
	ms := execBoundMillis(ctx)
	if mariaDB {
		secs := strconv.FormatFloat(float64(ms)/1000, 'f', -1, 64)
		return "SET STATEMENT max_statement_time=" + secs + " FOR " + selectStmt
	}
	return "SELECT /*+ MAX_EXECUTION_TIME(" + strconv.FormatInt(ms, 10) + ") */ " + strings.TrimPrefix(selectStmt, "SELECT ")
}

type rowQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func scanRow(rows *sql.Rows, count int) ([]any, error) {
	values, ptrs := make([]any, count), make([]any, count)
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	return values, nil
}

func displayRow(values []any) []any {
	for i, v := range values {
		switch x := v.(type) {
		case []byte:
			values[i] = string(x)
		case time.Time:
			values[i] = x.Format("2006-01-02 15:04:05.999999")
		}
	}
	return values
}

// Stop and cancel the underlying query as soon as the first excess row is seen.
// The byte budget counts JSON-encoded column names and rows, including escaping.
func selectLimited(ctx context.Context, db rowQuerier, query string, maxRows, maxBytes int, args ...any) (*QueryResult, error) {
	if maxRows <= 0 || maxRows > MaxResultRows {
		maxRows = MaxResultRows
	}
	if maxBytes <= 0 || maxBytes > MaxResultBytes {
		maxBytes = MaxResultBytes
	}
	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { cancel(); rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(cols)
	if err != nil {
		return nil, err
	}
	used := len(encoded) + 2
	if used > maxBytes {
		return nil, errors.New("column metadata exceeds result byte limit")
	}
	result := &QueryResult{Columns: cols, Rows: [][]any{}, IsSelect: true, RowLimit: maxRows, ByteLimit: maxBytes}
	for rows.Next() {
		if len(result.Rows) >= maxRows {
			result.Truncated = true
			result.TruncationReason = "rows"
			cancel()
			break
		}
		values, err := scanRow(rows, len(cols))
		if err != nil {
			return nil, err
		}
		values = displayRow(values)
		encoded, err := json.Marshal(values)
		if err != nil {
			return nil, err
		}
		if used+len(encoded)+1 > maxBytes {
			result.Truncated = true
			result.TruncationReason = "bytes"
			cancel()
			break
		}
		used += len(encoded) + 1
		result.Rows = append(result.Rows, values)
	}
	if !result.Truncated {
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	result.ResultBytes = used
	return result, nil
}
