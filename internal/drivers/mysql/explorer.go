package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/adminbase/adminbase/internal/policy"
	"regexp"
	"strings"
	"time"
)

// EscapeIdent safely escapes a MySQL identifier (database, table, column name).
func EscapeIdent(name string) string {
	if strings.ContainsRune(name, 0) {
		name = strings.ReplaceAll(name, "\x00", "")
	}
	escaped := strings.ReplaceAll(name, "`", "``")
	return "`" + escaped + "`"
}

// TableInfo holds metadata about a table or view (Adminer style).
type TableInfo struct {
	Name          string `json:"name"`
	Type          string `json:"type"` // "BASE TABLE" or "VIEW"
	Engine        string `json:"engine"`
	Collation     string `json:"collation"`
	Rows          int64  `json:"rows"`
	DataLength    int64  `json:"dataLength"`
	IndexLength   int64  `json:"indexLength"`
	DataFree      int64  `json:"dataFree"`
	AutoIncrement *int64 `json:"autoIncrement,omitempty"`
	Comment       string `json:"comment"`
}

// ColumnInfo describes a single column.
type ColumnInfo struct {
	Field     string  `json:"field"`
	Type      string  `json:"type"`
	Collation string  `json:"collation,omitempty"`
	Null      string  `json:"null"`
	Key       string  `json:"key"`
	Default   *string `json:"default"`
	Extra     string  `json:"extra"`
	Comment   string  `json:"comment"`
}

// IndexInfo describes a table index.
type IndexInfo struct {
	Name      string   `json:"name"`
	NonUnique bool     `json:"nonUnique"`
	Columns   []string `json:"columns"`
	Type      string   `json:"type"`
}

// TableSchema contains full table structure information.
type TableSchema struct {
	Name            string       `json:"name"`
	Columns         []ColumnInfo `json:"columns"`
	Indexes         []IndexInfo  `json:"indexes"`
	PrimaryKey      []string     `json:"primaryKey"`
	CreateStatement string       `json:"createStatement"`
	ExecutedSQL     string       `json:"executedSql,omitempty"`
}

// QueryResult holds execution outcome and rows.
type QueryResult struct {
	Columns          []string `json:"columns"`
	Rows             [][]any  `json:"rows"`
	TotalRows        int64    `json:"totalRows"`
	AffectedRows     int64    `json:"affectedRows"`
	ExecutionMs      int64    `json:"executionMs"`
	ExecutedSQL      string   `json:"executedSql"`
	IsSelect         bool     `json:"isSelect"`
	Error            string   `json:"error,omitempty"`
	Truncated        bool     `json:"truncated"`
	TruncationReason string   `json:"truncationReason,omitempty"`
	RowLimit         int      `json:"rowLimit,omitempty"`
	ByteLimit        int      `json:"byteLimit,omitempty"`
	ResultBytes      int      `json:"resultBytes,omitempty"`
	HasMore          bool     `json:"hasMore"`
	NextOffset       int64    `json:"nextOffset"`
	TotalRowsKnown   bool     `json:"totalRowsKnown"`
}

// DatabaseInfo holds metadata about a database on the server.
type DatabaseInfo struct {
	Name      string `json:"name"`
	Collation string `json:"collation"`
	Tables    int64  `json:"tables"`
	Size      int64  `json:"size"`
}

// ListDatabases retrieves all databases accessible to the user.
func ListDatabases(ctx context.Context, db *sql.DB) ([]string, string, error) {
	q := "SHOW DATABASES"
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, q, err
	}
	defer rows.Close()

	var dbs []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, q, err
		}
		dbs = append(dbs, name)
	}
	return dbs, q, rows.Err()
}

// ListDatabasesWithDetails retrieves all databases with collation, table count, and disk size.
func ListDatabasesWithDetails(ctx context.Context, db *sql.DB) ([]DatabaseInfo, string, error) {
	q := `SELECT s.SCHEMA_NAME, IFNULL(s.DEFAULT_COLLATION_NAME, ''),
	             COUNT(t.TABLE_NAME), IFNULL(SUM(t.DATA_LENGTH + t.INDEX_LENGTH), 0)
	      FROM INFORMATION_SCHEMA.SCHEMATA s
	      LEFT JOIN INFORMATION_SCHEMA.TABLES t ON t.TABLE_SCHEMA = s.SCHEMA_NAME
	      GROUP BY s.SCHEMA_NAME, s.DEFAULT_COLLATION_NAME
	      ORDER BY s.SCHEMA_NAME`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		dbs, sqlText, errFallback := ListDatabases(ctx, db)
		if errFallback != nil {
			return nil, q, errFallback
		}
		var fallbackList []DatabaseInfo
		for _, name := range dbs {
			fallbackList = append(fallbackList, DatabaseInfo{Name: name})
		}
		return fallbackList, sqlText, nil
	}
	defer rows.Close()

	var list []DatabaseInfo
	for rows.Next() {
		var d DatabaseInfo
		if err := rows.Scan(&d.Name, &d.Collation, &d.Tables, &d.Size); err != nil {
			return nil, q, err
		}
		list = append(list, d)
	}
	return list, "SHOW DATABASES;", rows.Err()
}

// CreateDatabase creates a new database with utf8mb4 collation.
func CreateDatabase(ctx context.Context, db *sql.DB, name string) (string, error) {
	q := fmt.Sprintf("CREATE DATABASE %s DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", EscapeIdent(name))
	_, err := db.ExecContext(ctx, q)
	return q, err
}

// DropDatabase drops a database.
func DropDatabase(ctx context.Context, db *sql.DB, name string) (string, error) {
	q := fmt.Sprintf("DROP DATABASE IF EXISTS %s", EscapeIdent(name))
	_, err := db.ExecContext(ctx, q)
	return q, err
}

// ListTables retrieves all tables and views with full status in a database.
func ListTables(ctx context.Context, db *sql.DB, database string) ([]TableInfo, string, error) {
	q := `SELECT TABLE_NAME, TABLE_TYPE, IFNULL(ENGINE, ''), IFNULL(TABLE_COLLATION, ''),
	             IFNULL(TABLE_ROWS, 0), IFNULL(DATA_LENGTH, 0), IFNULL(INDEX_LENGTH, 0),
	             IFNULL(DATA_FREE, 0), AUTO_INCREMENT, IFNULL(TABLE_COMMENT, '')
	      FROM INFORMATION_SCHEMA.TABLES
	      WHERE TABLE_SCHEMA = ?
	      ORDER BY TABLE_NAME`
	rows, err := db.QueryContext(ctx, q, database)
	if err != nil {
		return nil, q, err
	}
	defer rows.Close()

	var tables []TableInfo
	for rows.Next() {
		var t TableInfo
		var ai sql.NullInt64
		if err := rows.Scan(&t.Name, &t.Type, &t.Engine, &t.Collation,
			&t.Rows, &t.DataLength, &t.IndexLength, &t.DataFree, &ai, &t.Comment); err != nil {
			return nil, q, err
		}
		if ai.Valid {
			t.AutoIncrement = &ai.Int64
		}
		tables = append(tables, t)
	}
	return tables, fmt.Sprintf("SHOW TABLE STATUS FROM %s;", EscapeIdent(database)), rows.Err()
}

// GetTableSchema retrieves column metadata, indexes, and CREATE TABLE DDL.
func GetTableSchema(ctx context.Context, db *sql.DB, database, table string) (*TableSchema, error) {
	schema := &TableSchema{
		Name:       table,
		Columns:    []ColumnInfo{},
		Indexes:    []IndexInfo{},
		PrimaryKey: []string{},
	}

	colQuery := fmt.Sprintf("SHOW FULL COLUMNS FROM %s.%s", EscapeIdent(database), EscapeIdent(table))
	schema.ExecutedSQL = colQuery
	rows, err := db.QueryContext(ctx, colQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var c ColumnInfo
		var collation, privileges sql.NullString
		var def sql.NullString
		if err := rows.Scan(&c.Field, &c.Type, &collation, &c.Null, &c.Key, &def, &c.Extra, &privileges, &c.Comment); err != nil {
			return nil, err
		}
		if collation.Valid {
			c.Collation = collation.String
		}
		if def.Valid {
			c.Default = &def.String
		}
		if c.Key == "PRI" {
			schema.PrimaryKey = append(schema.PrimaryKey, c.Field)
		}
		schema.Columns = append(schema.Columns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Indexes
	idxQuery := fmt.Sprintf("SHOW INDEX FROM %s.%s", EscapeIdent(database), EscapeIdent(table))
	idxRows, err := db.QueryContext(ctx, idxQuery)
	if err == nil {
		defer idxRows.Close()
		indexMap := make(map[string]*IndexInfo)
		var indexOrder []string

		for idxRows.Next() {
			var tbl, keyName, colName, indexType, collation sql.NullString
			var nonUnique, seqInIndex int
			var cardinality, subPart, packed, null, comment, indexComment, isVisible sql.NullString

			cols, _ := idxRows.Columns()
			dest := make([]any, len(cols))
			dest[0] = &tbl
			dest[1] = &nonUnique
			dest[2] = &keyName
			dest[3] = &seqInIndex
			dest[4] = &colName
			if len(dest) > 5 {
				dest[5] = &collation
			}
			if len(dest) > 6 {
				dest[6] = &cardinality
			}
			if len(dest) > 7 {
				dest[7] = &subPart
			}
			if len(dest) > 8 {
				dest[8] = &packed
			}
			if len(dest) > 9 {
				dest[9] = &null
			}
			if len(dest) > 10 {
				dest[10] = &indexType
			}
			if len(dest) > 11 {
				dest[11] = &comment
			}
			if len(dest) > 12 {
				dest[12] = &indexComment
			}
			if len(dest) > 13 {
				dest[13] = &isVisible
			}
			for i := 14; i < len(dest); i++ {
				var dummy any
				dest[i] = &dummy
			}

			if err := idxRows.Scan(dest...); err == nil && keyName.Valid {
				name := keyName.String
				idx, exists := indexMap[name]
				if !exists {
					idx = &IndexInfo{
						Name:      name,
						NonUnique: nonUnique != 0,
						Columns:   []string{},
						Type:      indexType.String,
					}
					indexMap[name] = idx
					indexOrder = append(indexOrder, name)
				}
				if colName.Valid {
					idx.Columns = append(idx.Columns, colName.String)
				}
			}
		}
		for _, name := range indexOrder {
			schema.Indexes = append(schema.Indexes, *indexMap[name])
		}
	}

	// CREATE TABLE statement
	createTableQuery := fmt.Sprintf("SHOW CREATE TABLE %s.%s", EscapeIdent(database), EscapeIdent(table))
	var dummy, createSQL string
	if err := db.QueryRowContext(ctx, createTableQuery).Scan(&dummy, &createSQL); err == nil {
		schema.CreateStatement = createSQL
	}

	return schema, nil
}

// TableDataOptions specifies pagination, sorting and filtering.
type TableDataOptions struct {
	Offset      *int64 `json:"offset,omitempty"`
	Page        int    `json:"page"`
	PageSize    int    `json:"pageSize"`
	SortColumn  string `json:"sortColumn"`
	SortOrder   string `json:"sortOrder"` // ASC or DESC
	FilterCol   string `json:"filterCol"`
	FilterOp    string `json:"filterOp"` // LIKE, EQUALS, NOT_EQUALS, STARTS, GT, LT, IS_NULL, IS_NOT_NULL
	FilterValue string `json:"filterValue"`
	CustomWhere string `json:"customWhere"`
	Columns     string `json:"columns"` // comma separated or empty for "*"
}

// GetTableData loads rows with case-insensitive and accent-insensitive search.
func GetTableData(ctx context.Context, db *sql.DB, database, table string, opts TableDataOptions) (*QueryResult, error) {
	if opts.Page <= 0 {
		opts.Page = 1
	}
	if opts.Page > (1<<53-1001)/500 {
		return nil, errors.New("invalid page number")
	}
	if opts.PageSize <= 0 || opts.PageSize > 500 {
		opts.PageSize = 50
	}

	selectCols := "*"
	if strings.TrimSpace(opts.Columns) != "" && opts.Columns != "*" {
		parts := strings.Split(opts.Columns, ",")
		var escaped []string
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				escaped = append(escaped, EscapeIdent(trimmed))
			}
		}
		if len(escaped) > 0 {
			selectCols = strings.Join(escaped, ", ")
		}
	}

	wherePart, args, err := tableWhere(ctx, db, database, table, opts)
	if err != nil {
		return nil, err
	}
	baseQuery := fmt.Sprintf("FROM %s.%s", EscapeIdent(database), EscapeIdent(table))
	orderPart := ""
	if opts.SortColumn != "" {
		dir := "ASC"
		if strings.ToUpper(opts.SortOrder) == "DESC" {
			dir = "DESC"
		}
		orderPart = fmt.Sprintf(" ORDER BY %s %s", EscapeIdent(opts.SortColumn), dir)
	}
	offset := int64(opts.Page-1) * int64(opts.PageSize)
	if opts.Offset != nil {
		offset = *opts.Offset
	}
	if offset < 0 || offset > 1<<53-1001 {
		return nil, errors.New("invalid pagination offset")
	}
	dataQuery := fmt.Sprintf("SELECT %s ", selectCols) + baseQuery + wherePart + orderPart + fmt.Sprintf(" LIMIT %d OFFSET %d", opts.PageSize+1, offset)
	start := time.Now()
	res, err := selectLimited(ctx, db, dataQuery, opts.PageSize+1, MaxResultBytes, args...)
	if err != nil {
		return nil, err
	}
	res.HasMore = len(res.Rows) > opts.PageSize || (res.Truncated && len(res.Rows) > 0)
	if len(res.Rows) > opts.PageSize {
		res.Rows = res.Rows[:opts.PageSize]
	}
	res.NextOffset = offset + int64(len(res.Rows))
	res.RowLimit = opts.PageSize
	res.ExecutionMs = time.Since(start).Milliseconds()
	res.ExecutedSQL = dataQuery
	return res, nil
}

// Data and explicit counts share exactly the same structured filter builder.
func tableWhere(ctx context.Context, db *sql.DB, database, table string, opts TableDataOptions) (string, []any, error) {
	var whereClauses []string
	var args []any

	val := strings.TrimSpace(opts.FilterValue)
	op := strings.ToUpper(strings.TrimSpace(opts.FilterOp))
	if op == "" {
		op = "LIKE"
	}

	if val != "" || op == "IS_NULL" || op == "IS_NOT_NULL" {
		col := strings.TrimSpace(opts.FilterCol)
		if col != "" && col != "_all_" {
			clause, arg := buildFilterExpr(col, op, val)
			if clause != "" {
				whereClauses = append(whereClauses, clause)
				if arg != nil {
					args = append(args, arg)
				}
			}
		} else if val != "" {
			// Search across all text/char columns in table
			cols, err := getTableColumnNames(ctx, db, database, table)
			if err != nil {
				return "", nil, err
			}
			if len(cols) > 0 {
				var orClauses []string
				for _, c := range cols {
					clause, arg := buildFilterExpr(c, "LIKE", val)
					if clause != "" {
						orClauses = append(orClauses, clause)
						args = append(args, arg)
					}
				}
				if len(orClauses) > 0 {
					whereClauses = append(whereClauses, "("+strings.Join(orClauses, " OR ")+")")
				}
			}
		}
	}

	if strings.TrimSpace(opts.CustomWhere) != "" {
		return "", nil, errors.New("customWhere is not supported; use structured column filters")
	}

	wherePart := ""
	if len(whereClauses) > 0 {
		wherePart = " WHERE " + strings.Join(whereClauses, " AND ")
	}

	return wherePart, args, nil
}

// CountTableRows is intentionally separate from browsing, which never counts.
func CountTableRows(ctx context.Context, db *sql.DB, database, table string, opts TableDataOptions) (int64, string, error) {
	where, args, err := tableWhere(ctx, db, database, table, opts)
	if err != nil {
		return 0, "", err
	}
	query := "SELECT COUNT(*) FROM " + EscapeIdent(database) + "." + EscapeIdent(table) + where
	var total int64
	err = db.QueryRowContext(ctx, query, args...).Scan(&total)
	return total, query, err
}

// GetRow reads full values only when the client supplies the actual complete PK.
func GetRow(ctx context.Context, db *sql.DB, database, table string, pk map[string]any) (*QueryResult, error) {
	schema, err := GetTableSchema(ctx, db, database, table)
	if err != nil {
		return nil, err
	}
	if len(schema.PrimaryKey) == 0 || len(pk) != len(schema.PrimaryKey) {
		return nil, errors.New("complete primary key required")
	}
	var clauses []string
	var args []any
	for _, name := range schema.PrimaryKey {
		value, ok := pk[name]
		if !ok || value == nil {
			return nil, errors.New("complete primary key required")
		}
		clauses = append(clauses, EscapeIdent(name)+" = ?")
		args = append(args, value)
	}
	query := "SELECT * FROM " + EscapeIdent(database) + "." + EscapeIdent(table) + " WHERE " + strings.Join(clauses, " AND ") + " LIMIT 1"
	result, err := selectLimited(ctx, db, query, 1, MaxResultBytes, args...)
	if err != nil {
		return nil, err
	}
	result.ExecutedSQL = query
	return result, nil
}

// buildFilterExpr constructs a case-insensitive and accent-insensitive WHERE expression.
func buildFilterExpr(column string, op string, val string) (string, any) {
	colExpr := fmt.Sprintf("CAST(%s AS CHAR) COLLATE utf8mb4_general_ci", EscapeIdent(column))
	switch op {
	case "EQUALS", "=":
		return fmt.Sprintf("%s = CONVERT(? USING utf8mb4) COLLATE utf8mb4_general_ci", colExpr), val
	case "NOT_EQUALS", "!=", "<>":
		return fmt.Sprintf("%s != CONVERT(? USING utf8mb4) COLLATE utf8mb4_general_ci", colExpr), val
	case "STARTS", "LIKE ...%":
		return fmt.Sprintf("%s LIKE CONVERT(? USING utf8mb4) COLLATE utf8mb4_general_ci", colExpr), val + "%"
	case "GT", ">":
		return fmt.Sprintf("%s > ?", EscapeIdent(column)), val
	case "GTE", ">=":
		return fmt.Sprintf("%s >= ?", EscapeIdent(column)), val
	case "LT", "<":
		return fmt.Sprintf("%s < ?", EscapeIdent(column)), val
	case "LTE", "<=":
		return fmt.Sprintf("%s <= ?", EscapeIdent(column)), val
	case "REGEXP":
		return fmt.Sprintf("%s REGEXP ?", EscapeIdent(column)), val
	case "NOT_LIKE", "NOT LIKE":
		return fmt.Sprintf("%s NOT LIKE CONVERT(? USING utf8mb4) COLLATE utf8mb4_general_ci", colExpr), "%" + val + "%"
	case "IN":
		// Split comma-separated values into parameters
		items := strings.Split(val, ",")
		var placeholders []string
		for range items {
			placeholders = append(placeholders, "?")
		}
		if len(placeholders) > 0 {
			return fmt.Sprintf("%s IN (%s)", EscapeIdent(column), strings.Join(placeholders, ",")), val
		}
		return fmt.Sprintf("%s = ?", EscapeIdent(column)), val
	case "IS_NULL":
		return fmt.Sprintf("%s IS NULL", EscapeIdent(column)), nil
	case "IS_NOT_NULL":
		return fmt.Sprintf("%s IS NOT NULL", EscapeIdent(column)), nil
	case "SQL":
		// Direct expression
		return val, nil
	default: // LIKE / contains / LIKE %%
		return fmt.Sprintf("%s LIKE CONVERT(? USING utf8mb4) COLLATE utf8mb4_general_ci", colExpr), "%" + val + "%"
	}
}

func getTableColumnNames(ctx context.Context, db *sql.DB, database, table string) ([]string, error) {
	q := fmt.Sprintf("SHOW COLUMNS FROM %s.%s", EscapeIdent(database), EscapeIdent(table))
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var field, typ, null, key sql.NullString
		var def, extra sql.NullString
		if err := rows.Scan(&field, &typ, &null, &key, &def, &extra); err == nil && field.Valid {
			cols = append(cols, field.String)
		}
	}
	return cols, nil
}

// ExecuteSQL executes an arbitrary SQL string (either SELECT or mutation/DDL).
func ExecuteSQL(ctx context.Context, db *sql.DB, database string, query string, maxRows int) (*QueryResult, error) {
	if err := policy.ValidateSQL(query); err != nil {
		return nil, err
	}
	if maxRows <= 0 {
		maxRows = 1000
	}
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, errors.New("query is empty")
	}

	// Pin USE and execution to the same physical connection.
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if database != "" {
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("USE %s", EscapeIdent(database))); err != nil {
			return nil, err
		}
	}

	start := time.Now()
	isQuery := isRowReturning(trimmed)

	if isQuery {
		res, err := selectLimited(ctx, conn, query, maxRows, MaxResultBytes)
		if err != nil {
			return nil, err
		}
		res.ExecutionMs = time.Since(start).Milliseconds()
		res.ExecutedSQL = trimmed
		res.TotalRows = int64(len(res.Rows))
		return res, nil
	}

	execRes, err := conn.ExecContext(ctx, query)
	if err != nil {
		return nil, err
	}
	elapsed := time.Since(start).Milliseconds()
	affected, _ := execRes.RowsAffected()

	return &QueryResult{
		IsSelect:     false,
		AffectedRows: affected,
		ExecutionMs:  elapsed,
		ExecutedSQL:  trimmed,
		Columns:      []string{},
		Rows:         [][]any{},
	}, nil
}

func isRowReturning(query string) bool {
	return policy.ReturnsRows(query)
}

func executeSelect(ctx context.Context, db *sql.DB, query string, args ...any) (*QueryResult, error) {
	return selectLimited(ctx, db, query, MaxResultRows, MaxResultBytes, args...)
}

// InsertRow inserts a record into a table.
func InsertRow(ctx context.Context, db *sql.DB, database, table string, values map[string]any) (string, error) {
	if len(values) == 0 {
		return "", errors.New("no values provided")
	}

	var cols []string
	var placeholders []string
	var args []any

	for col, val := range values {
		cols = append(cols, EscapeIdent(col))
		placeholders = append(placeholders, "?")
		args = append(args, val)
	}

	q := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES (%s)",
		EscapeIdent(database), EscapeIdent(table),
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
	)

	_, err := db.ExecContext(ctx, q, args...)
	return q, err
}

// UpdateRow updates a record matching primary keys.
func UpdateRow(ctx context.Context, db *sql.DB, database, table string, pk map[string]any, values map[string]any) (string, error) {
	if len(pk) == 0 {
		return "", errors.New("primary key is required for updates")
	}
	if len(values) == 0 {
		return "", errors.New("no values to update")
	}

	var sets []string
	var args []any

	for col, val := range values {
		sets = append(sets, fmt.Sprintf("%s = ?", EscapeIdent(col)))
		args = append(args, val)
	}

	var where []string
	for col, val := range pk {
		where = append(where, fmt.Sprintf("%s = ?", EscapeIdent(col)))
		args = append(args, val)
	}

	q := fmt.Sprintf("UPDATE %s.%s SET %s WHERE %s LIMIT 1",
		EscapeIdent(database), EscapeIdent(table),
		strings.Join(sets, ", "),
		strings.Join(where, " AND "),
	)

	_, err := db.ExecContext(ctx, q, args...)
	return q, err
}

// DeleteRow deletes a single record matching primary key values.
func DeleteRow(ctx context.Context, db *sql.DB, database, table string, pk map[string]any) (string, error) {
	if len(pk) == 0 {
		return "", errors.New("primary key is required for deletion")
	}

	var where []string
	var args []any
	for col, val := range pk {
		where = append(where, fmt.Sprintf("%s = ?", EscapeIdent(col)))
		args = append(args, val)
	}

	q := fmt.Sprintf("DELETE FROM %s.%s WHERE %s LIMIT 1",
		EscapeIdent(database), EscapeIdent(table),
		strings.Join(where, " AND "),
	)

	_, err := db.ExecContext(ctx, q, args...)
	return q, err
}

// BatchDeleteRows deletes multiple records by primary key in a single transaction.
func BatchDeleteRows(ctx context.Context, db *sql.DB, database, table string, pks []map[string]any) (int64, string, error) {
	if len(pks) == 0 {
		return 0, "", nil
	}

	var orClauses []string
	var args []any

	for _, pk := range pks {
		var andParts []string
		for col, val := range pk {
			andParts = append(andParts, fmt.Sprintf("%s = ?", EscapeIdent(col)))
			args = append(args, val)
		}
		if len(andParts) > 0 {
			orClauses = append(orClauses, "("+strings.Join(andParts, " AND ")+")")
		}
	}

	if len(orClauses) == 0 {
		return 0, "", errors.New("no valid keys provided")
	}

	q := fmt.Sprintf("DELETE FROM %s.%s WHERE %s",
		EscapeIdent(database), EscapeIdent(table),
		strings.Join(orClauses, " OR "),
	)

	res, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, q, err
	}
	affected, _ := res.RowsAffected()
	return affected, q, nil
}

// TruncateTable empties all rows from a table.
func TruncateTable(ctx context.Context, db *sql.DB, database, table string) (string, error) {
	q := fmt.Sprintf("TRUNCATE TABLE %s.%s", EscapeIdent(database), EscapeIdent(table))
	_, err := db.ExecContext(ctx, q)
	return q, err
}

// DropTable drops a table or view.
func DropTable(ctx context.Context, db *sql.DB, database, table string) (string, error) {
	q := fmt.Sprintf("DROP TABLE IF EXISTS %s.%s", EscapeIdent(database), EscapeIdent(table))
	_, err := db.ExecContext(ctx, q)
	return q, err
}

// OptimizeTable runs OPTIMIZE TABLE on specified tables.
func OptimizeTable(ctx context.Context, db *sql.DB, database, table string) (string, error) {
	q := fmt.Sprintf("OPTIMIZE TABLE %s.%s", EscapeIdent(database), EscapeIdent(table))
	_, err := db.ExecContext(ctx, q)
	return q, err
}

// NewColumnDef describes a column for table creation or alteration.
type NewColumnDef struct {
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	Length        string  `json:"length,omitempty"`
	Null          bool    `json:"null"`
	Default       *string `json:"default,omitempty"`
	AutoIncrement bool    `json:"autoIncrement"`
	PrimaryKey    bool    `json:"primaryKey"`
	Comment       string  `json:"comment,omitempty"`
}

// CreateTable creates a new table with column definitions.
var columnLength = regexp.MustCompile(`^[0-9]{1,6}(,[0-9]{1,2})?$`)

func CreateTable(ctx context.Context, db *sql.DB, database, tableName string, cols []NewColumnDef) (string, error) {
	if tableName == "" || len(cols) == 0 {
		return "", errors.New("table name and at least one column are required")
	}

	var colDefs []string
	var pkCols []string

	for _, c := range cols {
		typeStr := strings.ToUpper(c.Type)
		switch typeStr {
		case "INT", "INTEGER", "BIGINT", "SMALLINT", "MEDIUMINT", "TINYINT", "VARCHAR", "CHAR", "TEXT", "TINYTEXT", "MEDIUMTEXT", "LONGTEXT", "DATETIME", "TIMESTAMP", "DATE", "TIME", "YEAR", "DECIMAL", "NUMERIC", "FLOAT", "DOUBLE", "BOOLEAN", "BOOL", "JSON", "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "BINARY", "VARBINARY", "BIT":
		default:
			return "", errors.New("unsupported column type")
		}
		if c.Length != "" && !columnLength.MatchString(c.Length) {
			return "", errors.New("invalid column length or precision")
		}
		if c.Name == "" || strings.ContainsRune(c.Name, 0) {
			return "", errors.New("invalid column name")
		}
		if c.Length != "" {
			typeStr = fmt.Sprintf("%s(%s)", typeStr, c.Length)
		}
		nullStr := "NOT NULL"
		if c.Null {
			nullStr = "NULL"
		}
		defStr := ""
		if c.Default != nil {
			defStr = fmt.Sprintf("DEFAULT %s", formatSQLValue(*c.Default))
			if (strings.EqualFold(c.Type, "DATETIME") || strings.EqualFold(c.Type, "TIMESTAMP")) && strings.EqualFold(*c.Default, "CURRENT_TIMESTAMP") {
				defStr = "DEFAULT CURRENT_TIMESTAMP"
			}
		}
		aiStr := ""
		if c.AutoIncrement {
			aiStr = "AUTO_INCREMENT"
		}
		commentStr := ""
		if c.Comment != "" {
			commentStr = fmt.Sprintf("COMMENT %s", formatSQLValue(c.Comment))
		}

		parts := []string{EscapeIdent(c.Name), typeStr, nullStr}
		if defStr != "" {
			parts = append(parts, defStr)
		}
		if aiStr != "" {
			parts = append(parts, aiStr)
		}
		if commentStr != "" {
			parts = append(parts, commentStr)
		}

		colDefs = append(colDefs, strings.Join(parts, " "))
		if c.PrimaryKey {
			pkCols = append(pkCols, EscapeIdent(c.Name))
		}
	}

	if len(pkCols) > 0 {
		colDefs = append(colDefs, fmt.Sprintf("PRIMARY KEY (%s)", strings.Join(pkCols, ", ")))
	}

	q := fmt.Sprintf("CREATE TABLE %s.%s (\n  %s\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci",
		EscapeIdent(database), EscapeIdent(tableName),
		strings.Join(colDefs, ",\n  "),
	)

	_, err := db.ExecContext(ctx, q)
	return q, err
}

func formatSQLValue(v any) string {
	switch val := v.(type) {
	case string:
		escaped := strings.ReplaceAll(val, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "'", "''")
		return "'" + escaped + "'"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", val)
	case float32, float64:
		return fmt.Sprintf("%v", val)
	case bool:
		if val {
			return "1"
		}
		return "0"
	default:
		s := fmt.Sprintf("%v", val)
		escaped := strings.ReplaceAll(s, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "'", "''")
		return "'" + escaped + "'"
	}
}
