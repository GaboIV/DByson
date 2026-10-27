package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/gaboiv/dbyson/internal/audit"
	"github.com/gaboiv/dbyson/internal/store"
	"github.com/gaboiv/dbyson/internal/vault"
)

// seed creates a database with one encrypted connection secret and a short audit
// chain, all under the generated master key of dir.
func seed(t *testing.T, dir string) []byte {
	t.Helper()
	t.Setenv("DBYSON_MASTER_KEY", "")
	t.Setenv("DBYSON_MASTER_KEY_FILE", "")
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	key, _, _, err := vault.LoadKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := vault.New(key)
	wrapped, sealed, err := v.Seal("c1", []byte(`{"password":"s3cret"}`))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, st, `INSERT INTO users (id, username, password_hash, created_at) VALUES (1, 'ana', 'x', 0)`)
	mustExec(t, st, `INSERT INTO connections (id, owner_id, name, env, host, port, username, tls_mode, wrapped_dek, secret, created_at, updated_at)
		VALUES ('c1', 1, 'db', 'dev', 'h', 3306, 'u', 'disabled', ?, ?, 0, 0)`, wrapped, sealed)
	l := audit.New(st.DB, v.DeriveKey("audit"))
	for i := 0; i < 3; i++ {
		if err := l.Record(context.Background(), audit.Entry{Username: "ana", Action: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	return key
}

func mustExec(t *testing.T, st *store.Store, q string, args ...any) {
	t.Helper()
	if _, err := st.DB.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

// decrypts reports whether the stored secret opens with key, and whether the audit chain verifies with it.
func decrypts(t *testing.T, dir string, key []byte) (secretOK, auditOK bool) {
	t.Helper()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	v, _ := vault.New(key)
	var wrapped, sealed []byte
	if err := st.DB.QueryRow(`SELECT wrapped_dek, secret FROM connections WHERE id = 'c1'`).Scan(&wrapped, &sealed); err != nil {
		t.Fatal(err)
	}
	_, err = v.Open("c1", wrapped, sealed)
	res, _ := audit.New(st.DB, v.DeriveKey("audit")).Verify(context.Background())
	return err == nil, res.OK
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestRotateKeyMovesEverything(t *testing.T) {
	dir := t.TempDir()
	oldKey := seed(t, dir)
	newFile := filepath.Join(t.TempDir(), "new.key")

	if err := runRotateKey(quiet, []string{"--data-dir", dir, "--new-key-file", newFile}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(newFile)
	if err != nil {
		t.Fatal(err)
	}
	newKey, err := vault.DecodeKey(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if s, a := decrypts(t, dir, newKey); !s || !a {
		t.Fatalf("new key: secret=%v audit=%v", s, a)
	}
	if s, _ := decrypts(t, dir, oldKey); s {
		t.Fatal("old key must no longer decrypt")
	}
}

// A broken audit chain must abort the rotation before any secret is rewrapped:
// the old key keeps working and no orphan key file is left behind.
func TestRotateKeyIsAllOrNothing(t *testing.T) {
	dir := t.TempDir()
	oldKey := seed(t, dir)
	st, _ := store.Open(dir)
	mustExec(t, st, `DROP TRIGGER audit_log_no_update`)
	mustExec(t, st, `UPDATE audit_log SET detail = 'tampered' WHERE id = 2`)
	st.Close()

	newFile := filepath.Join(t.TempDir(), "new.key")
	if err := runRotateKey(quiet, []string{"--data-dir", dir, "--new-key-file", newFile}); err == nil {
		t.Fatal("rotation over a broken chain must fail")
	}
	if s, _ := decrypts(t, dir, oldKey); !s {
		t.Fatal("secrets must still open with the old key after an aborted rotation")
	}
	if _, err := os.Stat(newFile); !os.IsNotExist(err) {
		t.Fatal("the generated key file must be removed when the rotation aborts")
	}
}

func TestRotateKeyRefusesSameKey(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir)
	current := filepath.Join(dir, "master.key")
	if err := runRotateKey(quiet, []string{"--data-dir", dir, "--new-key-file", current}); err == nil {
		t.Fatal("rotating to the current key must be refused")
	}
}
