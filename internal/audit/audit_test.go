package audit

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adminbase/adminbase/internal/store"
)

func newLogger(t *testing.T) (*Logger, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st.DB, []byte("0123456789abcdef0123456789abcdef")), dir
}

func TestRecordListVerify(t *testing.T) {
	l, _ := newLogger(t)
	ctx := context.Background()
	for _, a := range []string{"auth.login", "query.execute", "connection.delete"} {
		if err := l.Record(ctx, Entry{UserID: 1, Username: "ana", Action: a}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := l.List(ctx, Filter{})
	if err != nil || len(got) != 3 || got[0].Action != "connection.delete" {
		t.Fatalf("list = %+v, %v", got, err)
	}
	if got, _ := l.List(ctx, Filter{Action: "query."}); len(got) != 1 {
		t.Fatalf("action filter: %+v", got)
	}
	if got, _ := l.List(ctx, Filter{BeforeID: 2}); len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("paging: %+v", got)
	}
	v, err := l.Verify(ctx)
	if err != nil || !v.OK || v.Checked != 3 {
		t.Fatalf("verify = %+v, %v", v, err)
	}
}

// Tampering at the file level (bypassing the triggers) must be detected.
func TestVerifyDetectsTampering(t *testing.T) {
	l, dir := newLogger(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		l.Record(ctx, Entry{Username: "ana", Action: "query.execute", Detail: "SELECT 1"})
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "adminbase.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`DROP TRIGGER audit_log_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE audit_log SET detail = 'SELECT 2' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	v, err := l.Verify(ctx)
	if err != nil || v.OK || v.BrokenAt != 2 {
		t.Fatalf("tampering not detected: %+v %v", v, err)
	}
}

func TestVerifyDetectsDeletion(t *testing.T) {
	l, dir := newLogger(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		l.Record(ctx, Entry{Username: "ana", Action: "x"})
	}
	raw, _ := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "adminbase.db")))
	defer raw.Close()
	raw.Exec(`DROP TRIGGER audit_log_no_delete`)
	raw.Exec(`DELETE FROM audit_log WHERE id = 2`)
	if v, _ := l.Verify(ctx); v.OK || v.BrokenAt != 3 {
		t.Fatalf("deletion not detected: %+v", v)
	}
}

func TestRedactSQL(t *testing.T) {
	cases := []string{
		`CREATE USER 'app'@'%' IDENTIFIED BY 'S3cret!'`,
		`ALTER USER app IDENTIFIED WITH caching_sha2_password BY "S3cret!"`,
		`SET PASSWORD FOR 'app'@'%' = 'S3cret!'`,
		`SELECT PASSWORD('S3cret!')`,
		`CHANGE REPLICATION SOURCE TO SOURCE_PASSWORD='S3cret!'`,
		`CREATE USER x IDENTIFIED BY 'it''s S3cret!'`,
	}
	for _, c := range cases {
		if got := RedactSQL(c); strings.Contains(got, "S3cret") {
			t.Errorf("not redacted: %s -> %s", c, got)
		}
	}
	if got := RedactSQL(`SELECT * FROM users WHERE name = 'password'`); got != `SELECT * FROM users WHERE name = 'password'` {
		t.Errorf("over-redacted: %s", got)
	}
}

func TestDetailIsTruncated(t *testing.T) {
	l, _ := newLogger(t)
	l.Record(context.Background(), Entry{Action: "query.execute", Detail: strings.Repeat("a", maxDetail+500)})
	got, _ := l.List(context.Background(), Filter{})
	if n := len([]rune(got[0].Detail)); n != maxDetail+1 {
		t.Fatalf("detail length %d", n)
	}
}
