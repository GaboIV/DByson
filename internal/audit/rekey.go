package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// Rekey rewrites the whole HMAC chain from oldKey to newKey and appends a
// vault.key_rotated anchor entry naming both key fingerprints (never the
// keys). It first verifies the old chain and refuses to touch a broken one.
// Run it offline with the server stopped: it takes no lock against a running
// server. Secret re-encryption is vault.RewrapSecrets, not this function.
func Rekey(ctx context.Context, db *sql.DB, oldKey, newKey []byte) error {
	oldL := &Logger{db: db, key: oldKey, Now: time.Now}
	v, err := oldL.Verify(ctx)
	if err != nil {
		return fmt.Errorf("audit rekey: %w", err)
	}
	if !v.OK {
		return fmt.Errorf("audit rekey: chain is broken at entry %d, refusing to rotate", v.BrokenAt)
	}
	newL := &Logger{db: db, key: newKey, Now: time.Now}

	rows, err := db.QueryContext(ctx, `SELECT id, at, COALESCE(user_id, 0), username, ip, action,
		connection_id, target, detail, outcome FROM audit_log ORDER BY id`)
	if err != nil {
		return fmt.Errorf("audit rekey: %w", err)
	}
	type row struct {
		id int64
		e  Entry
	}
	var entries []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.e.At, &r.e.UserID, &r.e.Username, &r.e.IP, &r.e.Action,
			&r.e.ConnectionID, &r.e.Target, &r.e.Detail, &r.e.Outcome); err != nil {
			rows.Close()
			return fmt.Errorf("audit rekey: %w", err)
		}
		entries = append(entries, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("audit rekey: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("audit rekey: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DROP TRIGGER audit_log_no_delete`); err != nil {
		return fmt.Errorf("audit rekey: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TRIGGER audit_log_no_update`); err != nil {
		return fmt.Errorf("audit rekey: %w", err)
	}
	prev := ""
	for _, r := range entries {
		r.e.Hash = newL.mac(prev, &r.e)
		if _, err := tx.ExecContext(ctx,
			`UPDATE audit_log SET prev_hash = ?, hash = ? WHERE id = ?`, prev, r.e.Hash, r.id); err != nil {
			return fmt.Errorf("audit rekey: %w", err)
		}
		prev = r.e.Hash
	}
	if _, err := tx.ExecContext(ctx, triggerNoUpdate); err != nil {
		return fmt.Errorf("audit rekey: %w", err)
	}
	if _, err := tx.ExecContext(ctx, triggerNoDelete); err != nil {
		return fmt.Errorf("audit rekey: %w", err)
	}
	anchor := Entry{
		At:      time.Now().Unix(),
		Action:  "vault.key_rotated",
		Detail:  fmt.Sprintf("master key rotated %s -> %s", shortKeyID(oldKey), shortKeyID(newKey)),
		Outcome: OutcomeOK,
	}
	anchor.Hash = newL.mac(prev, &anchor)
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log
		(at, user_id, username, ip, action, connection_id, target, detail, outcome, prev_hash, hash)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		anchor.At, nil, "", "", anchor.Action, "", "", anchor.Detail, anchor.Outcome, prev, anchor.Hash); err != nil {
		return fmt.Errorf("audit rekey: %w", err)
	}
	return tx.Commit()
}

func shortKeyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:])[:16]
}
