// Package store is the embedded SQLite database holding users, sessions and connection profiles.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go driver: keeps the binary static (CGO_ENABLED=0)
)

type Store struct {
	DB *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT 'admin',
  created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  id_hash    TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf       TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS connections (
  id          TEXT PRIMARY KEY,
  owner_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  env         TEXT NOT NULL,
  host        TEXT NOT NULL,
  port        INTEGER NOT NULL,
  username    TEXT NOT NULL,
  dbname      TEXT NOT NULL DEFAULT '',
  tls_mode    TEXT NOT NULL,
  read_only   INTEGER NOT NULL DEFAULT 0,
  wrapped_dek BLOB NOT NULL,
  secret      BLOB NOT NULL,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_connections_owner ON connections(owner_id);
`

// migrations run in order; PRAGMA user_version records how many have been applied.
// Migration 1 is the original schema (idempotent, so pre-versioning databases upgrade cleanly).
// Never edit an applied migration: append a new one.
var migrations = []string{
	schema,

	// 2: two-factor authentication and the audit log.
	`
ALTER TABLE users ADD COLUMN totp_wrapped BLOB;
ALTER TABLE users ADD COLUMN totp_secret BLOB;
ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN mfa INTEGER NOT NULL DEFAULT 0;

CREATE TABLE recovery_codes (
  user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash TEXT NOT NULL,
  used_at   INTEGER,
  PRIMARY KEY (user_id, code_hash)
);

CREATE TABLE audit_log (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  at            INTEGER NOT NULL,
  user_id       INTEGER,
  username      TEXT NOT NULL DEFAULT '',
  ip            TEXT NOT NULL DEFAULT '',
  action        TEXT NOT NULL,
  connection_id TEXT NOT NULL DEFAULT '',
  target        TEXT NOT NULL DEFAULT '',
  detail        TEXT NOT NULL DEFAULT '',
  outcome       TEXT NOT NULL,
  prev_hash     TEXT NOT NULL,
  hash          TEXT NOT NULL
);
CREATE INDEX idx_audit_at ON audit_log(at);
CREATE INDEX idx_audit_user ON audit_log(user_id);

-- Append-only: rows can be added but never changed or removed through SQL.
CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
BEGIN SELECT RAISE(ABORT, 'audit log is append-only'); END;
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
BEGIN SELECT RAISE(ABORT, 'audit log is append-only'); END;
`,

	// 3: public, non-authenticating session identifiers and sign-in metadata.
	`
ALTER TABLE sessions ADD COLUMN public_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN ip TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN user_agent TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN client_cert TEXT NOT NULL DEFAULT '';
UPDATE sessions SET public_id = lower(hex(randomblob(24)));
CREATE UNIQUE INDEX idx_sessions_public_id ON sessions(public_id);
CREATE INDEX idx_sessions_user ON sessions(user_id);
`,

	// 4: private, encrypted query library and WebAuthn public credentials.
	`
CREATE TABLE saved_queries (
  id TEXT PRIMARY KEY,
  owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  connection_id TEXT NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  dbname TEXT NOT NULL,
  wrapped_dek BLOB NOT NULL,
  secret BLOB NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX idx_saved_queries_owner ON saved_queries(owner_id, connection_id);
CREATE TABLE passkeys (
  id TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  credential BLOB NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX idx_passkeys_user ON passkeys(user_id);
CREATE TABLE passkey_handles (
  user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  handle BLOB NOT NULL UNIQUE
);
`,

	// 5: team mode. Roles member/viewer, shared connections with per-connection
	// grants, and generic OIDC accounts (Google, Microsoft Entra ID, Keycloak).
	// Never edit an applied migration: this one is appended last.
	`
ALTER TABLE users ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT '';
ALTER TABLE connections ADD COLUMN shared INTEGER NOT NULL DEFAULT 0;
CREATE TABLE connection_grants (
  connection_id TEXT NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  level TEXT NOT NULL DEFAULT 'use',
  PRIMARY KEY (connection_id, user_id)
);
CREATE INDEX idx_grants_user ON connection_grants(user_id);
CREATE TABLE oidc_accounts (
  provider TEXT NOT NULL,
  subject TEXT NOT NULL,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (provider, subject)
);
CREATE INDEX idx_oidc_user ON oidc_accounts(user_id);
`,
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	for i := version; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

const (
	FileName       = "dbyson.db"
	legacyFileName = "adminbase.db" // name used before the product was renamed
)

// adoptLegacyFile renames a database created under the old product name, together with its
// WAL and shared-memory files, so existing installations keep their data. It only runs while
// no database is open and only when the new file does not exist yet.
func adoptLegacyFile(dir string) error {
	newPath, oldPath := filepath.Join(dir, FileName), filepath.Join(dir, legacyFileName)
	if _, err := os.Stat(newPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(oldPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	// Companion files first: if the main rename failed afterwards, a stray -wal next to a
	// missing main file is harmless, while a renamed main file without its WAL would lose data.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Rename(oldPath+suffix, newPath+suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Rename(oldPath, newPath)
}

// Open opens (creating if needed) the SQLite file in dir and applies pending migrations.
func Open(dir string) (*Store, error) {
	if err := adoptLegacyFile(dir); err != nil {
		return nil, fmt.Errorf("rename legacy database file: %w", err)
	}
	path := filepath.Join(dir, FileName)
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }
