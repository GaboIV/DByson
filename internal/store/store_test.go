package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationsAreIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		st, err := Open(dir)
		if err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}
		var v int
		st.DB.QueryRow(`PRAGMA user_version`).Scan(&v)
		if v != len(migrations) {
			t.Fatalf("user_version = %d, want %d", v, len(migrations))
		}
		st.Close()
	}
}

func TestSessionMetadataMigrationPreservesSessions(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, FileName)))
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:2] {
		if _, err := db.Exec(migration); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version=2; INSERT INTO users(username,password_hash,created_at) VALUES('existing','unused',0); INSERT INTO sessions(id_hash,user_id,csrf,created_at,last_seen,expires_at) VALUES('first-hash',1,'first-csrf',1,1,9999999999),('second-hash',1,'second-csrf',1,1,9999999999)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var count, unique int
	if err := st.DB.QueryRow(`SELECT count(*),count(DISTINCT public_id) FROM sessions WHERE public_id <> ''`).Scan(&count, &unique); err != nil || count != 2 || unique != 2 {
		t.Fatalf("migrated sessions: count=%d unique=%d err=%v", count, unique, err)
	}
	var csrf string
	if err := st.DB.QueryRow(`SELECT csrf FROM sessions WHERE id_hash='first-hash'`).Scan(&csrf); err != nil || csrf != "first-csrf" {
		t.Fatal("migration changed session authentication")
	}
}

// An installation from before the rename (adminbase.db, still in WAL mode with pending pages)
// must be adopted as dbyson.db without losing rows.
func TestAdoptsLegacyFileName(t *testing.T) {
	dir := t.TempDir()
	old, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	if err := os.Rename(filepath.Join(dir, FileName), filepath.Join(dir, legacyFileName)); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, legacyFileName))+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO users (username, password_hash, created_at) VALUES ('legacy', 'x', 0)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE username = 'legacy'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("legacy rows not carried over: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, legacyFileName)); !os.IsNotExist(err) {
		t.Fatal("legacy file should have been renamed")
	}
}

// When both names exist (e.g. a backup copied back), the new file wins and nothing is touched.
func TestKeepsNewFileWhenBothExist(t *testing.T) {
	dir := t.TempDir()
	st, _ := Open(dir)
	st.Close()
	if err := os.WriteFile(filepath.Join(dir, legacyFileName), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	if _, err := os.Stat(filepath.Join(dir, legacyFileName)); err != nil {
		t.Fatal("legacy file must be left alone when the new one exists")
	}
}

// A database created before versioning (user_version 0, original tables present) must upgrade.
func TestUpgradesPreVersioningDatabase(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, legacyFileName)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (username, password_hash, created_at) VALUES ('old', 'x', 0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var enabled int
	if err := st.DB.QueryRow(`SELECT totp_enabled FROM users WHERE username='old'`).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("existing user not migrated: %v", err)
	}
}

func TestAuditLogIsAppendOnly(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.DB.Exec(`INSERT INTO audit_log (at, action, outcome, prev_hash, hash) VALUES (1, 'x', 'ok', '', 'h')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`UPDATE audit_log SET action = 'y'`); err == nil {
		t.Fatal("update must be rejected")
	}
	if _, err := st.DB.Exec(`DELETE FROM audit_log`); err == nil {
		t.Fatal("delete must be rejected")
	}
}
