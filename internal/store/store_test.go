package store

import (
	"database/sql"
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

// A database created before versioning (user_version 0, original tables present) must upgrade.
func TestUpgradesPreVersioningDatabase(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "adminbase.db")))
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
