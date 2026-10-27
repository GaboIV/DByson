package audit

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"
)

// Export formats for shipping the audit trail to an external system (SIEM,
// object storage, compliance archive).
const (
	ExportJSONL = "jsonl"
	ExportCSV   = "csv"

	// MaxExportRows caps a single export so one request cannot stream the
	// whole history of a busy server. Page with Until/BeforeID for more.
	MaxExportRows = 50000
)

// Export streams matching entries oldest-first in format, so a SIEM ingesting
// the output sees a chronological feed. Detail keeps its redacted form: it
// never carries plaintext credentials (see RedactSQL).
func (l *Logger) Export(ctx context.Context, w io.Writer, format string, f Filter) (int, error) {
	if format != ExportJSONL && format != ExportCSV {
		return 0, fmt.Errorf("format must be jsonl or csv")
	}
	limit := f.Limit
	if limit <= 0 || limit > MaxExportRows {
		limit = MaxExportRows
	}
	q := `SELECT id, at, COALESCE(user_id, 0), username, ip, action, connection_id, target, detail, outcome, hash
	      FROM audit_log WHERE 1=1`
	var args []any
	if f.BeforeID > 0 {
		q += ` AND id < ?`
		args = append(args, f.BeforeID)
	}
	if f.UserID != 0 {
		q += ` AND user_id = ?`
		args = append(args, f.UserID)
	}
	if f.Action != "" {
		q += ` AND action LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(f.Action)+"%")
	}
	if f.Username != "" {
		q += ` AND username = ?`
		args = append(args, f.Username)
	}
	if f.Outcome != "" {
		q += ` AND outcome = ?`
		args = append(args, f.Outcome)
	}
	if f.Since != 0 {
		q += ` AND at >= ?`
		args = append(args, f.Since)
	}
	if f.Until != 0 {
		q += ` AND at <= ?`
		args = append(args, f.Until)
	}
	q += ` ORDER BY id ASC LIMIT ?`
	args = append(args, limit)

	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var (
		cw *csv.Writer
		n  int
	)
	if format == ExportCSV {
		cw = csv.NewWriter(w)
		_ = cw.Write([]string{"id", "at", "user_id", "username", "ip", "action",
			"connection_id", "target", "detail", "outcome", "hash"})
	}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.At, &e.UserID, &e.Username, &e.IP, &e.Action,
			&e.ConnectionID, &e.Target, &e.Detail, &e.Outcome, &e.Hash); err != nil {
			return n, err
		}
		if format == ExportCSV {
			_ = cw.Write([]string{
				strconv.FormatInt(e.ID, 10),
				time.Unix(e.At, 0).UTC().Format(time.RFC3339),
				strconv.FormatInt(e.UserID, 10),
				e.Username, e.IP, e.Action, e.ConnectionID, e.Target, e.Detail, e.Outcome, e.Hash,
			})
		} else {
			line, _ := json.Marshal(e)
			if _, err := w.Write(append(line, '\n')); err != nil {
				return n, err
			}
		}
		n++
	}
	if cw != nil {
		cw.Flush()
	}
	return n, rows.Err()
}

// Trigger bodies duplicated from the store migration (internal/store/store.go):
// Purge must drop them to delete, then recreate them byte-identical.
const (
	triggerNoUpdate = `CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
BEGIN SELECT RAISE(ABORT, 'audit log is append-only'); END;`
	triggerNoDelete = `CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
BEGIN SELECT RAISE(ABORT, 'audit log is append-only'); END;`
)

// PurgeResult describes a retention purge.
type PurgeResult struct {
	Deleted int64  `json:"deleted"`
	Anchor  string `json:"anchor"` // hash of the last deleted entry, "" when nothing was deleted
}

// PurgeOlderThan deletes entries older than cutoff (unix seconds) and appends
// an audit.purged anchor entry recording how many rows went away and the hash
// of the last one. A SIEM holding an earlier export can verify continuity:
// the anchor's detail names the hash its copy must end with. Truncating the
// tail afterwards still breaks the chain, which is why exports should land
// outside DByson before purging.
func (l *Logger) PurgeOlderThan(ctx context.Context, cutoff int64) (PurgeResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return PurgeResult{}, err
	}
	defer tx.Rollback()

	// Remove only the leading run of old entries, by id. Selecting by timestamp
	// alone would punch holes in the chain whenever the clock moved backwards.
	var lastID int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM audit_log
		WHERE id < COALESCE((SELECT MIN(id) FROM audit_log WHERE at >= ?), 9223372036854775807)`, cutoff).
		Scan(&lastID)
	if err != nil {
		return PurgeResult{}, err
	}
	if lastID == 0 {
		return PurgeResult{}, nil
	}
	var (
		count  int64
		anchor string
	)
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE id <= ?`, lastID).Scan(&count); err != nil {
		return PurgeResult{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT hash FROM audit_log WHERE id = ?`, lastID).Scan(&anchor); err != nil {
		return PurgeResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `DROP TRIGGER audit_log_no_delete`); err != nil {
		return PurgeResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `DROP TRIGGER audit_log_no_update`); err != nil {
		return PurgeResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM audit_log WHERE id <= ?`, lastID); err != nil {
		return PurgeResult{}, err
	}
	if _, err := tx.ExecContext(ctx, triggerNoUpdate); err != nil {
		return PurgeResult{}, err
	}
	if _, err := tx.ExecContext(ctx, triggerNoDelete); err != nil {
		return PurgeResult{}, err
	}
	// Continue the chain from the newest remaining entry; when everything was
	// purged, from the anchor itself, so the head still points at the purge.
	prev := anchor
	err = tx.QueryRowContext(ctx, `SELECT hash FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&prev)
	if err != nil && err != sql.ErrNoRows {
		return PurgeResult{}, err
	}
	e := Entry{
		At:      l.Now().Unix(),
		Action:  "audit.purged",
		Detail:  fmt.Sprintf("retention: deleted %d entries before %s %s%s", count, time.Unix(cutoff, 0).UTC().Format(time.RFC3339), purgeAnchorPrefix, anchor),
		Outcome: OutcomeOK,
	}
	e.Hash = l.mac(prev, &e)
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log
		(at, user_id, username, ip, action, connection_id, target, detail, outcome, prev_hash, hash)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		e.At, nil, "", "", e.Action, "", "", e.Detail, e.Outcome, prev, e.Hash); err != nil {
		return PurgeResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return PurgeResult{}, err
	}
	return PurgeResult{Deleted: count, Anchor: anchor}, nil
}
