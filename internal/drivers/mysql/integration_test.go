package mysql

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Only run against a disposable database server explicitly supplied by the caller.
// Example: DBYSON_INTEGRATION_ADDR=dbyson-mvp-mysql:3306 go test ./internal/drivers/mysql -run TestIntegration -v
func TestIntegrationMVPProtections(t *testing.T) {
	address := os.Getenv("DBYSON_INTEGRATION_ADDR")
	if address == "" {
		t.Skip("set DBYSON_INTEGRATION_ADDR to an isolated MySQL/MariaDB test server")
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	cfg := Config{Host: host, Port: port, User: "root", Password: "dbyson-tests", TLSMode: TLSDisabled}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db, err := Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("dbyson_test_%d", time.Now().UnixNano())
	restored := name + "_restore"
	mustExec := func(t *testing.T, conn *sql.DB, query string, args ...any) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	mustExec(t, db, "CREATE DATABASE "+EscapeIdent(name)+" CHARACTER SET utf8mb4")
	mustExec(t, db, "CREATE DATABASE "+EscapeIdent(restored)+" CHARACTER SET utf8mb4")
	defer db.ExecContext(context.Background(), "DROP DATABASE "+EscapeIdent(name))
	defer db.ExecContext(context.Background(), "DROP DATABASE "+EscapeIdent(restored))
	cfg.Database = name
	source, err := Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	mustExec(t, source, "CREATE TABLE sample(id INT PRIMARY KEY, payload LONGTEXT, amount DECIMAL(30,10), raw LONGBLOB, moment DATETIME(6), optional TEXT NULL)")
	tx, err := source.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO sample VALUES(?,?,?,?,?,NULL)")
	if err != nil {
		t.Fatal(err)
	}
	payload := "áé 中文 ' quote \\ slash\nnewline\x00zero;semicolon"
	for i := 1; i <= 1105; i++ {
		if _, err := stmt.ExecContext(ctx, i, payload, "12345678901234567890.1234567890", []byte{0, 1, 255, 39, 92}, "2026-10-21 12:34:56.123456"); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	t.Run("row-and-byte-limits", func(t *testing.T) {
		result, err := ExecuteSQL(ctx, source, name, "/* data */ SELECT * FROM sample ORDER BY id", 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Rows) != 3 || !result.Truncated || result.TruncationReason != "rows" {
			t.Fatalf("row limit: %+v", result)
		}
		result, err = ExecuteSQL(ctx, source, name, "SELECT id FROM sample LIMIT 3", 3)
		if err != nil || result.Truncated {
			t.Fatalf("exact limit: %+v %v", result, err)
		}
		result, err = ExecuteSQL(ctx, source, name, "SELECT id FROM sample", 0)
		if err != nil || len(result.Rows) != 1000 || !result.Truncated {
			t.Fatalf("default limit: %v %+v", err, result)
		}
		result, err = ExecuteSQL(ctx, source, name, "SELECT REPEAT('x', 1048576) AS value FROM sample LIMIT 10", 1000)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Truncated || result.TruncationReason != "bytes" || result.ResultBytes > MaxResultBytes {
			t.Fatalf("byte limit: %+v", result)
		}
		result, err = ExecuteSQL(ctx, source, name, "SELECT REPEAT('x', 9437184) AS value", 1000)
		if err != nil || !result.Truncated || len(result.Rows) != 0 {
			t.Fatalf("oversized row: %+v %v", result, err)
		}
	})
	t.Run("browse-count-and-row-detail", func(t *testing.T) {
		first, err := GetTableData(ctx, source, name, "sample", TableDataOptions{PageSize: 2, SortColumn: "id", Columns: "id"})
		if err != nil {
			t.Fatal(err)
		}
		if len(first.Rows) != 2 || !first.HasMore || first.NextOffset != 2 || first.TotalRowsKnown || strings.Contains(first.ExecutedSQL, "COUNT(") {
			t.Fatalf("page metadata: %+v", first)
		}
		next, err := GetTableData(ctx, source, name, "sample", TableDataOptions{PageSize: 2, SortColumn: "id", Columns: "id", Offset: &first.NextOffset})
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(next.Rows[0][0]) != "3" {
			t.Fatalf("wrong next page: %+v", next.Rows)
		}
		total, _, err := CountTableRows(ctx, source, name, "sample", TableDataOptions{})
		if err != nil || total != 1105 {
			t.Fatalf("explicit count: %d %v", total, err)
		}
		total, _, err = CountTableRows(ctx, source, name, "sample", TableDataOptions{FilterCol: "id", FilterOp: "=", FilterValue: "1"})
		if err != nil || total != 1 {
			t.Fatalf("filtered count: %d %v", total, err)
		}
		row, err := GetRow(ctx, source, name, "sample", map[string]any{"id": 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(row.Columns) != 6 || len(row.Rows) != 1 || row.Rows[0][1] != payload {
			t.Fatal("full row detail differs from source")
		}
		if _, err := GetRow(ctx, source, name, "sample", map[string]any{"payload": payload}); err == nil {
			t.Fatal("non-primary key accepted")
		}
		cancelled, stop := context.WithCancel(ctx)
		stop()
		if _, err := GetTableData(cancelled, source, name, "sample", TableDataOptions{}); err == nil {
			t.Fatal("cancelled browse succeeded")
		}
	})
	t.Run("cancel-live-query", func(t *testing.T) {
		requestCtx, stop := context.WithCancel(ctx)
		defer stop()
		timer := time.AfterFunc(100*time.Millisecond, stop)
		defer timer.Stop()
		start := time.Now()
		_, err := ExecuteSQL(requestCtx, source, name, "SELECT SLEEP(10)", 10)
		if err == nil || time.Since(start) > 3*time.Second {
			t.Fatalf("request did not cancel promptly: elapsed=%v err=%v", time.Since(start), err)
		}
	})
	t.Run("read-only-session-and-routine", func(t *testing.T) {
		mustExec(t, source, "CREATE TABLE nontransactional(id INT) ENGINE=MyISAM")
		mustExec(t, source, "CREATE FUNCTION try_write() RETURNS INT DETERMINISTIC MODIFIES SQL DATA BEGIN UPDATE sample SET payload='changed' WHERE id=1; RETURN 1; END")
		roCfg := cfg
		roCfg.ReadOnly = true
		ro, err := Open(roCfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer ro.Close()
		var conns []*sql.Conn
		for i := 0; i < 3; i++ {
			conn, err := ro.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			conns = append(conns, conn)
			defer conn.Close()
			var value int
			if err := conn.QueryRowContext(ctx, "SELECT 1").Scan(&value); err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{"UPDATE sample SET payload='unsafe' WHERE id=1", "INSERT INTO nontransactional VALUES (1)", "SELECT try_write()"} {
				if _, err := conn.ExecContext(ctx, query); err == nil {
					t.Errorf("physical connection %d accepted %s", i, query)
				}
			}
		}
		var value string
		if err := source.QueryRowContext(ctx, "SELECT payload FROM sample WHERE id=1").Scan(&value); err != nil || value != payload {
			t.Fatalf("source changed: %q %v", value, err)
		}
	})
	t.Run("export-all-rows-and-restore", func(t *testing.T) {
		var csvOut, jsonOut, dump bytes.Buffer
		if err := ExportCSV(ctx, source, name, "sample", &csvOut); err != nil {
			t.Fatal(err)
		}
		records, err := csv.NewReader(&csvOut).ReadAll()
		if err != nil || len(records) != 1106 {
			t.Fatalf("CSV rows %d %v", len(records), err)
		}
		if err := ExportJSON(ctx, source, name, "sample", &jsonOut); err != nil {
			t.Fatal(err)
		}
		var recordsJSON []map[string]any
		if err := json.Unmarshal(jsonOut.Bytes(), &recordsJSON); err != nil || len(recordsJSON) != 1105 {
			t.Fatalf("JSON rows %d %v", len(recordsJSON), err)
		}
		if err := ExportSQL(ctx, source, name, []string{"sample"}, true, true, &dump); err != nil {
			t.Fatal(err)
		}
		restoreCfg := cfg
		restoreCfg.Database = restored
		target, err := Open(restoreCfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer target.Close()
		// Fixture values are hex encoded, so delimiters cannot appear inside values.
		for _, statement := range strings.Split(dump.String(), ";") {
			if strings.TrimSpace(statement) != "" {
				mustExec(t, target, statement)
			}
		}
		var count int
		target.QueryRowContext(ctx, "SELECT COUNT(*) FROM sample").Scan(&count)
		if count != 1105 {
			t.Fatalf("restored %d rows", count)
		}
		var text, amount, moment string
		var raw []byte
		var optional sql.NullString
		if err := target.QueryRowContext(ctx, "SELECT payload,amount,raw,DATE_FORMAT(moment,'%Y-%m-%d %H:%i:%s.%f'),optional FROM sample WHERE id=1").Scan(&text, &amount, &raw, &moment, &optional); err != nil {
			t.Fatal(err)
		}
		if text != payload || amount != "12345678901234567890.1234567890" || !bytes.Equal(raw, []byte{0, 1, 255, 39, 92}) || moment != "2026-10-21 12:34:56.123456" || optional.Valid {
			t.Fatalf("round-trip mismatch text=%q amount=%q raw=%v moment=%q optional=%+v", text, amount, raw, moment, optional)
		}
	})
}
