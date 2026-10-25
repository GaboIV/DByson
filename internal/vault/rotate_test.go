package vault

import (
	"context"
	"crypto/rand"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func openMemory(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRewrapSecrets(t *testing.T) {
	ctx := context.Background()
	oldKey := make([]byte, 32)
	rand.Read(oldKey)
	newKey := make([]byte, 32)
	rand.Read(newKey)
	oldV, _ := New(oldKey)
	newV, _ := New(newKey)

	db := openMemory(t)
	for _, schema := range []string{
		`CREATE TABLE connections (id TEXT PRIMARY KEY, wrapped_dek BLOB, secret BLOB)`,
		`CREATE TABLE saved_queries (id TEXT PRIMARY KEY, wrapped_dek BLOB, secret BLOB)`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, totp_wrapped BLOB, totp_secret BLOB)`,
	} {
		if _, err := db.Exec(schema); err != nil {
			t.Fatal(err)
		}
	}
	wrapped, sealed, _ := oldV.Seal("conn-1", []byte("db-password"))
	if _, err := db.Exec(`INSERT INTO connections VALUES ('conn-1', ?, ?)`, wrapped, sealed); err != nil {
		t.Fatal(err)
	}
	qw, qs, _ := oldV.Seal("query/q-1", []byte("SELECT 1"))
	if _, err := db.Exec(`INSERT INTO saved_queries VALUES ('q-1', ?, ?)`, qw, qs); err != nil {
		t.Fatal(err)
	}
	tw, ts, _ := oldV.Seal("totp:7", []byte("totp-seed"))
	if _, err := db.Exec(`INSERT INTO users VALUES (7, ?, ?)`, tw, ts); err != nil {
		t.Fatal(err)
	}

	if err := RewrapSecrets(ctx, db, oldV, newV); err != nil {
		t.Fatal(err)
	}
	var w2, s2 []byte
	db.QueryRow(`SELECT wrapped_dek, secret FROM connections WHERE id = 'conn-1'`).Scan(&w2, &s2)
	if pt, err := newV.Open("conn-1", w2, s2); err != nil || string(pt) != "db-password" {
		t.Fatalf("connection not readable with new key: %v", err)
	}
	if _, err := oldV.Open("conn-1", w2, s2); err == nil {
		t.Fatal("old key still opens the rewrapped secret")
	}
	db.QueryRow(`SELECT wrapped_dek, secret FROM saved_queries WHERE id = 'q-1'`).Scan(&w2, &s2)
	if pt, err := newV.Open("query/q-1", w2, s2); err != nil || string(pt) != "SELECT 1" {
		t.Fatalf("saved query not readable with new key: %v", err)
	}
	db.QueryRow(`SELECT totp_wrapped, totp_secret FROM users WHERE id = 7`).Scan(&w2, &s2)
	if pt, err := newV.Open("totp:7", w2, s2); err != nil || string(pt) != "totp-seed" {
		t.Fatalf("totp seed not readable with new key: %v", err)
	}
}

func TestRewrapFailsClosedOnWrongOldKey(t *testing.T) {
	ctx := context.Background()
	oldKey := make([]byte, 32)
	rand.Read(oldKey)
	wrongKey := make([]byte, 32)
	rand.Read(wrongKey)
	newKey := make([]byte, 32)
	rand.Read(newKey)
	oldV, _ := New(oldKey)
	wrongV, _ := New(wrongKey)
	newV, _ := New(newKey)

	db := openMemory(t)
	for _, schema := range []string{
		`CREATE TABLE connections (id TEXT PRIMARY KEY, wrapped_dek BLOB, secret BLOB)`,
		`CREATE TABLE saved_queries (id TEXT PRIMARY KEY, wrapped_dek BLOB, secret BLOB)`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, totp_wrapped BLOB, totp_secret BLOB)`,
	} {
		if _, err := db.Exec(schema); err != nil {
			t.Fatal(err)
		}
	}
	wrapped, sealed, _ := oldV.Seal("conn-1", []byte("db-password"))
	if _, err := db.Exec(`INSERT INTO connections VALUES ('conn-1', ?, ?)`, wrapped, sealed); err != nil {
		t.Fatal(err)
	}
	if err := RewrapSecrets(ctx, db, wrongV, newV); err == nil {
		t.Fatal("rewrap with the wrong old key must fail")
	}
}
