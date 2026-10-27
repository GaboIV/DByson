package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// Rekey rewrites the whole HMAC chain from oldKey to newKey in its own
// transaction. Key rotation uses RekeyTx instead, so the chain and the vault
// secrets change together or not at all.
func Rekey(ctx context.Context, db *sql.DB, oldKey, newKey []byte) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("audit rekey: %w", err)
	}
	defer tx.Rollback()
	if err := RekeyTx(ctx, tx, oldKey, newKey); err != nil {
		return err
	}
	return tx.Commit()
}

// RekeyTx re-signs the chain inside tx and appends a vault.key_rotated entry
// naming both key fingerprints (never the keys). oldKey and newKey are the audit
// MAC keys (vault.DeriveKey("audit")), not the master keys. It verifies the old
// chain first, refuses to touch a broken one, and verifies the new chain before
// returning.
// The head of a purged chain points at an external anchor (the hash of the last
// purged entry, recorded in an audit.purged entry): it is kept as-is, because it
// identifies data that no longer exists and cannot be re-signed.
func RekeyTx(ctx context.Context, tx *sql.Tx, oldKey, newKey []byte) error {
	v, err := verifyChain(ctx, tx, oldKey)
	if err != nil {
		return fmt.Errorf("audit rekey: %w", err)
	}
	if !v.OK {
		return fmt.Errorf("audit rekey: chain is broken at entry %d, refusing to rotate", v.BrokenAt)
	}

	rows, err := tx.QueryContext(ctx, `SELECT id, at, COALESCE(user_id, 0), username, ip, action,
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

	for _, stmt := range []string{`DROP TRIGGER audit_log_no_delete`, `DROP TRIGGER audit_log_no_update`} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("audit rekey: %w", err)
		}
	}
	prev := v.AnchoredAt // "" for an intact chain
	for _, r := range entries {
		r.e.Hash = macWith(newKey, prev, &r.e)
		if _, err := tx.ExecContext(ctx,
			`UPDATE audit_log SET prev_hash = ?, hash = ? WHERE id = ?`, prev, r.e.Hash, r.id); err != nil {
			return fmt.Errorf("audit rekey: %w", err)
		}
		prev = r.e.Hash
	}
	for _, stmt := range []string{triggerNoUpdate, triggerNoDelete} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("audit rekey: %w", err)
		}
	}
	anchor := Entry{
		At:      time.Now().Unix(),
		Action:  "vault.key_rotated",
		Detail:  fmt.Sprintf("master key rotated %s -> %s", shortKeyID(oldKey), shortKeyID(newKey)),
		Outcome: OutcomeOK,
	}
	anchor.Hash = macWith(newKey, prev, &anchor)
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log
		(at, user_id, username, ip, action, connection_id, target, detail, outcome, prev_hash, hash)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		anchor.At, nil, "", "", anchor.Action, "", "", anchor.Detail, anchor.Outcome, prev, anchor.Hash); err != nil {
		return fmt.Errorf("audit rekey: %w", err)
	}
	after, err := verifyChain(ctx, tx, newKey)
	if err != nil || !after.OK {
		return fmt.Errorf("audit rekey: rewritten chain does not verify (entry %d): %v", after.BrokenAt, err)
	}
	return nil
}

func shortKeyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:])[:16]
}
