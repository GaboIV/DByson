package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ForeignKey preserves column order, including compound and cross-schema keys.
type ForeignKey struct {
	Name               string   `json:"name"`
	Table              string   `json:"table"`
	Columns            []string `json:"columns"`
	ReferencedDatabase string   `json:"referencedDatabase"`
	ReferencedTable    string   `json:"referencedTable"`
	ReferencedColumns  []string `json:"referencedColumns"`
}

func ForeignKeys(ctx context.Context, db *sql.DB, database string) ([]ForeignKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT CONSTRAINT_NAME,TABLE_NAME,COLUMN_NAME,REFERENCED_TABLE_SCHEMA,REFERENCED_TABLE_NAME,REFERENCED_COLUMN_NAME FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA=? AND REFERENCED_TABLE_NAME IS NOT NULL ORDER BY TABLE_NAME,CONSTRAINT_NAME,ORDINAL_POSITION`, database)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []ForeignKey{}
	for rows.Next() {
		var name, table, col, rd, rt, rc string
		if err := rows.Scan(&name, &table, &col, &rd, &rt, &rc); err != nil {
			return nil, err
		}
		if len(keys) == 0 || keys[len(keys)-1].Name != name || keys[len(keys)-1].Table != table {
			keys = append(keys, ForeignKey{Name: name, Table: table, ReferencedDatabase: rd, ReferencedTable: rt, Columns: []string{}, ReferencedColumns: []string{}})
		}
		key := &keys[len(keys)-1]
		key.Columns = append(key.Columns, col)
		key.ReferencedColumns = append(key.ReferencedColumns, rc)
	}
	return keys, rows.Err()
}

// ImportCSV uses a transaction and parameters, never SQL concatenated from file values.
// Only InnoDB is supported so a failed batch cannot leave a partial import.
func ImportCSV(ctx context.Context, db *sql.DB, database, table string, columns []string, records [][]*string) (int, error) {
	if len(columns) == 0 || len(columns) > 256 || len(records) == 0 || len(records) > 5000 {
		return 0, errors.New("import requires 1-256 columns and 1-5000 rows")
	}
	schema, err := GetTableSchema(ctx, db, database, table)
	if err != nil {
		return 0, err
	}
	var engine string
	if err = db.QueryRowContext(ctx, `SELECT COALESCE(ENGINE,'') FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_NAME=?`, database, table).Scan(&engine); err != nil {
		return 0, err
	}
	if !strings.EqualFold(engine, "InnoDB") {
		return 0, errors.New("atomic CSV imports require an InnoDB table")
	}
	seen := map[string]bool{}
	quoted := []string{}
	placeholders := []string{}
	for _, col := range columns {
		found := false
		for _, c := range schema.Columns {
			extra := strings.ToLower(c.Extra)
			generated := strings.Contains(extra, "virtual generated") || strings.Contains(extra, "stored generated") || strings.Contains(extra, "persistent")
			if c.Field == col && !generated {
				found = true
			}
		}
		if !found || seen[col] {
			return 0, fmt.Errorf("invalid, duplicate or generated column: %s", col)
		}
		seen[col] = true
		quoted = append(quoted, EscapeIdent(col))
		placeholders = append(placeholders, "?")
	}
	for _, row := range records {
		if len(row) != len(columns) {
			return 0, errors.New("inconsistent CSV row width")
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO "+EscapeIdent(database)+"."+EscapeIdent(table)+" ("+strings.Join(quoted, ",")+") VALUES ("+strings.Join(placeholders, ",")+")")
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	for i, row := range records {
		values := make([]any, len(row))
		for j, v := range row {
			if v != nil {
				values[j] = *v
			}
		}
		if _, err = stmt.ExecContext(ctx, values...); err != nil {
			return 0, fmt.Errorf("row %d: %w", i+1, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return len(records), nil
}
