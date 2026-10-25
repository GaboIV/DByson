package audit

import (
	"context"
	"crypto/rand"
	"testing"

	"database/sql"
	_ "modernc.org/sqlite"
)

func rekeyDB(t *testing.T) (*sql.DB, []byte, []byte) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE audit_log (
	  id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, user_id INTEGER,
	  username TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '', action TEXT NOT NULL,
	  connection_id TEXT NOT NULL DEFAULT '', target TEXT NOT NULL DEFAULT '',
	  detail TEXT NOT NULL DEFAULT '', outcome TEXT NOT NULL,
	  prev_hash TEXT NOT NULL, hash TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(triggerNoUpdate); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(triggerNoDelete); err != nil {
		t.Fatal(err)
	}
	oldKey := make([]byte, 32)
	rand.Read(oldKey)
	newKey := make([]byte, 32)
	rand.Read(newKey)
	oldL := New(db, oldKey)
	ctx := context.Background()
	for _, action := range []string{"auth.login", "connection.create", "query.execute"} {
		if err := oldL.Record(ctx, Entry{Username: "admin", IP: "127.0.0.1", Action: action, Outcome: OutcomeOK}); err != nil {
			t.Fatal(err)
		}
	}
	return db, oldKey, newKey
}

func TestRekeyMovesChainToNewKey(t *testing.T) {
	db, oldKey, newKey := rekeyDB(t)
	ctx := context.Background()
	if err := Rekey(ctx, db, oldKey, newKey); err != nil {
		t.Fatal(err)
	}
	v, err := New(db, newKey).Verify(ctx)
	if err != nil || !v.OK || v.Checked != 4 { // 3 entries + the rotation anchor
		t.Fatalf("new chain must verify: %+v %v", v, err)
	}
	if v, _ := New(db, oldKey).Verify(ctx); v.OK {
		t.Fatal("old key must no longer verify the chain")
	}
	var anchor string
	if err := db.QueryRow(`SELECT detail FROM audit_log WHERE action = 'vault.key_rotated'`).Scan(&anchor); err != nil {
		t.Fatalf("rotation anchor missing: %v", err)
	}
	var triggers int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name LIKE 'audit_log_no_%'`).Scan(&triggers); err != nil || triggers != 2 {
		t.Fatalf("triggers not restored: %d %v", triggers, err)
	}
}

func TestRekeyRefusesBrokenChain(t *testing.T) {
	db, oldKey, newKey := rekeyDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`DROP TRIGGER audit_log_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE audit_log SET detail = 'forged' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(triggerNoUpdate); err != nil {
		t.Fatal(err)
	}
	if err := Rekey(ctx, db, oldKey, newKey); err == nil {
		t.Fatal("rekey of a broken chain must fail")
	}
}
