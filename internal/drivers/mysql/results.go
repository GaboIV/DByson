package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const MaxResultRows = 1000
const MaxResultBytes = 8 << 20

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
