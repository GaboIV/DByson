package mysql

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type fixtureConnector struct {
	queries       []string
	total, pulled int
	payload       string
	failureAt     int
	closed        bool
	setup         string
	setupErr      error
}

func (f *fixtureConnector) Connect(context.Context) (driver.Conn, error) { return &fixtureConn{f}, nil }
func (f *fixtureConnector) Driver() driver.Driver                        { return fixtureDriver{} }

type fixtureDriver struct{}

func (fixtureDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type fixtureConn struct{ f *fixtureConnector }

func (c *fixtureConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unsupported") }
func (c *fixtureConn) Close() error                        { return nil }
func (c *fixtureConn) Begin() (driver.Tx, error)           { return nil, errors.New("unsupported") }
func (c *fixtureConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.f.setup = query
	return driver.RowsAffected(0), c.f.setupErr
}
func (c *fixtureConn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.f.queries = append(c.f.queries, query)
	return &fixtureRows{f: c.f}, nil
}

type fixtureRows struct {
	f     *fixtureConnector
	index int
}

func (r *fixtureRows) Columns() []string { return []string{"value"} }
func (r *fixtureRows) Close() error      { r.f.closed = true; return nil }
func (r *fixtureRows) Next(dest []driver.Value) error {
	if r.f.failureAt > 0 && r.index == r.f.failureAt {
		return errors.New("source failed")
	}
	if r.index == r.f.total {
		return io.EOF
	}
	r.index++
	r.f.pulled++
	dest[0] = []byte(r.f.payload)
	return nil
}
func fixtureDB(t *testing.T, total int, payload string) (*sql.DB, *fixtureConnector) {
	t.Helper()
	f := &fixtureConnector{total: total, payload: payload}
	db := sql.OpenDB(f)
	t.Cleanup(func() { db.Close() })
	return db, f
}

func TestResultsStopAtRowAndEncodedByteLimits(t *testing.T) {
	ctx := context.Background()
	db, f := fixtureDB(t, 10000, "hello")
	result, err := selectLimited(ctx, db, "SELECT value", 3, 10000)
	if err != nil || len(result.Rows) != 3 || !result.Truncated || result.TruncationReason != "rows" || f.pulled != 4 || !f.closed {
		t.Fatalf("row budget: result=%+v, read=%d, err=%v", result, f.pulled, err)
	}
	db, _ = fixtureDB(t, 3, "hello")
	result, err = selectLimited(ctx, db, "SELECT value", 3, 10000)
	if err != nil || result.Truncated {
		t.Fatalf("exact row boundary: %+v %v", result, err)
	}
	db, f = fixtureDB(t, 10000, strings.Repeat("\x00\"", 10))
	result, err = selectLimited(ctx, db, "SELECT value", 1000, 150)
	encoded, _ := json.Marshal(result.Rows)
	if err != nil || !result.Truncated || result.TruncationReason != "bytes" || len(encoded) > 150 || result.ResultBytes > 150 || f.pulled > 3 {
		t.Fatalf("byte budget: %+v rowsBytes=%d pulled=%d err=%v", result, len(encoded), f.pulled, err)
	}
	db, _ = fixtureDB(t, 1, strings.Repeat("x", 200))
	result, err = selectLimited(ctx, db, "SELECT value", 10, 150)
	if err != nil || !result.Truncated || len(result.Rows) != 0 {
		t.Fatalf("oversized first row: %+v %v", result, err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("destination failed") }

func TestExportsStreamAndPropagateErrors(t *testing.T) {
	for _, format := range []string{"sql", "csv", "json"} {
		t.Run(format, func(t *testing.T) {
			db, f := fixtureDB(t, 10000, strings.Repeat("x", 1000))
			export := func(w io.Writer) error {
				switch format {
				case "sql":
					return ExportSQL(context.Background(), db, "db", []string{"t"}, true, false, w)
				case "csv":
					return ExportCSV(context.Background(), db, "db", "t", w)
				default:
					return ExportJSON(context.Background(), db, "db", "t", w)
				}
			}
			if err := export(failingWriter{}); err == nil {
				t.Fatal("writer failure ignored")
			}
			if f.pulled >= f.total || !f.closed {
				t.Fatalf("table materialized before writing: %d", f.pulled)
			}
			f.total = 5
			f.pulled = 0
			f.failureAt = 2
			if err := export(io.Discard); err == nil {
				t.Fatal("source failure ignored")
			}
		})
	}
	db, _ := fixtureDB(t, 0, "")
	var out bytes.Buffer
	if err := ExportJSON(context.Background(), db, "db", "t", &out); err != nil || strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("empty JSON: %q %v", out.String(), err)
	}
}

func TestReadOnlyInitializesEveryPhysicalConnection(t *testing.T) {
	f := &fixtureConnector{}
	connector := readOnlyConnector{f}
	for i := 0; i < 2; i++ {
		f.setup = ""
		conn, err := connector.Connect(context.Background())
		if err != nil || f.setup != "SET SESSION TRANSACTION READ ONLY" {
			t.Fatalf("read-only not set: %q %v", f.setup, err)
		}
		conn.Close()
	}
	f.setupErr = errors.New("not supported")
	if _, err := connector.Connect(context.Background()); err == nil {
		t.Fatal("failed-open when server rejected read-only")
	}
}

func TestExecBoundMillisBoundsToDeadline(t *testing.T) {
	// No deadline: the ceiling.
	if ms := execBoundMillis(context.Background()); ms != 30000 {
		t.Fatalf("no-deadline bound = %d ms, want the 30000 ms ceiling", ms)
	}
	// A deadline shorter than the ceiling bounds to roughly the time left.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if ms := execBoundMillis(ctx); ms <= 1000 || ms > 5000 {
		t.Fatalf("5 s deadline gave %d ms, want it bounded within (1000, 5000]", ms)
	}
	// An expired deadline is floored to 1 s, never zero or negative.
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel2()
	time.Sleep(time.Millisecond)
	if ms := execBoundMillis(ctx2); ms != 1000 {
		t.Fatalf("expired deadline gave %d ms, want the 1000 ms floor", ms)
	}
}

// Each flavor gets a bound its server actually honors: MySQL the optimizer hint,
// MariaDB the SET STATEMENT prefix (the hint is a no-op there). Sending MariaDB
// syntax to MySQL is a syntax error, so flavor must drive the choice.
func TestBoundedReadPerFlavor(t *testing.T) {
	ctx := context.Background()
	mysqlQ := boundedRead(ctx, false, "SELECT a FROM t WHERE id = ?")
	if !strings.HasPrefix(mysqlQ, "SELECT /*+ MAX_EXECUTION_TIME(30000) */ a FROM t WHERE id = ?") {
		t.Fatalf("mysql bound = %q", mysqlQ)
	}
	mariaQ := boundedRead(ctx, true, "SELECT a FROM t WHERE id = ?")
	if !strings.HasPrefix(mariaQ, "SET STATEMENT max_statement_time=30 FOR SELECT a FROM t WHERE id = ?") {
		t.Fatalf("maria bound = %q", mariaQ)
	}
	if strings.Contains(mariaQ, "MAX_EXECUTION_TIME") {
		t.Fatalf("maria query must not carry the MySQL hint: %q", mariaQ)
	}
}

// Browsing a table carries a server-side execution bound, so a query the caller
// has abandoned cannot keep scanning on the database server.
func TestBrowseCarriesExecutionBound(t *testing.T) {
	db, f := fixtureDB(t, 3, "value")
	if _, err := GetTableData(context.Background(), db, "db", "t", TableDataOptions{PageSize: 2, SortColumn: "id"}); err != nil {
		t.Fatalf("browse: %v", err)
	}
	if len(f.queries) == 0 || !strings.Contains(f.queries[len(f.queries)-1], "MAX_EXECUTION_TIME") {
		t.Fatalf("browse query carries no execution bound: %v", f.queries)
	}
}
