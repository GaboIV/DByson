// Package audit keeps a tamper-evident, append-only record of security-relevant actions.
//
// Every entry carries an HMAC over its content and the previous entry's MAC, keyed with a subkey
// of the vault master key. SQLite triggers reject UPDATE and DELETE; anyone who edits, removes or
// reorders rows at the file level breaks the chain, and cannot forge it without the master key.
package audit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	OutcomeOK     = "ok"
	OutcomeError  = "error"
	OutcomeDenied = "denied"
	// OutcomeConfirmationRequested: a destructive action was asked for and a confirmation
	// challenge issued; the action itself has not run.
	OutcomeConfirmationRequested = "confirmation_requested"

	maxDetail = 8000 // characters
)

type Entry struct {
	ID           int64  `json:"id"`
	At           int64  `json:"at"`
	UserID       int64  `json:"userId,omitempty"`
	Username     string `json:"username"`
	IP           string `json:"ip"`
	Action       string `json:"action"`
	ConnectionID string `json:"connectionId,omitempty"`
	Target       string `json:"target,omitempty"`
	Detail       string `json:"detail,omitempty"`
	Outcome      string `json:"outcome"`
	Hash         string `json:"hash"`
}

type Logger struct {
	db  *sql.DB
	key []byte
	mu  sync.Mutex // serializes appends so each entry links to the true previous one
	Now func() time.Time
}

func New(db *sql.DB, key []byte) *Logger {
	return &Logger{db: db, key: key, Now: time.Now}
}

// mac binds the entry content to the previous MAC. JSON encoding gives an unambiguous
// field framing; the row id is excluded because the chain itself fixes the order.
func (l *Logger) mac(prev string, e *Entry) string { return macWith(l.key, prev, e) }

func macWith(key []byte, prev string, e *Entry) string {
	payload, _ := json.Marshal([]any{prev, e.At, e.UserID, e.Username, e.IP, e.Action, e.ConnectionID, e.Target, e.Detail, e.Outcome})
	m := hmac.New(sha256.New, key)
	m.Write(payload)
	return hex.EncodeToString(m.Sum(nil))
}

// Record appends an entry. Detail is redacted and truncated before it is stored.
func (l *Logger) Record(ctx context.Context, e Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	e.At = l.Now().Unix()
	e.Detail = truncate(RedactSQL(e.Detail), maxDetail)
	e.Target = truncate(e.Target, 512)
	if e.Outcome == "" {
		e.Outcome = OutcomeOK
	}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var prev string
	err = tx.QueryRowContext(ctx, `SELECT hash FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&prev)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	e.Hash = l.mac(prev, &e)
	var uid any
	if e.UserID != 0 {
		uid = e.UserID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log
		(at, user_id, username, ip, action, connection_id, target, detail, outcome, prev_hash, hash)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		e.At, uid, e.Username, e.IP, e.Action, e.ConnectionID, e.Target, e.Detail, e.Outcome, prev, e.Hash); err != nil {
		return err
	}
	return tx.Commit()
}

type Filter struct {
	Limit    int
	BeforeID int64  // page backwards: entries with id < BeforeID
	UserID   int64  // 0 = all users
	Action   string // prefix match
	Username string // exact match
	Outcome  string // exact match: ok, error, denied, confirmation_requested
	Since    int64  // unix seconds, inclusive; 0 = no lower bound
	Until    int64  // unix seconds, inclusive; 0 = no upper bound
}

func (l *Logger) List(ctx context.Context, f Filter) ([]Entry, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
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
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit)

	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.At, &e.UserID, &e.Username, &e.IP, &e.Action, &e.ConnectionID,
			&e.Target, &e.Detail, &e.Outcome, &e.Hash); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type Verification struct {
	OK       bool  `json:"ok"`
	Checked  int   `json:"checked"`
	BrokenAt int64 `json:"brokenAt,omitempty"` // id of the first entry that does not verify
	// Anchored is set when older entries were removed by a recorded retention purge:
	// the chain then starts at AnchoredAt, the hash of the last purged entry.
	Anchored   bool   `json:"anchored,omitempty"`
	AnchoredAt string `json:"anchoredAt,omitempty"`
}

// querier is satisfied by *sql.DB and *sql.Tx, so the chain can be verified
// inside the transaction that rewrites it (key rotation).
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// purgeAnchorPrefix marks, inside an audit.purged entry's detail, the hash of the
// last entry the purge removed. Verify trusts a chain head only if one of these
// MAC-protected entries vouches for it.
const purgeAnchorPrefix = "anchor="

// Verify walks the whole chain from the first entry.
func (l *Logger) Verify(ctx context.Context) (Verification, error) {
	return verifyChain(ctx, l.db, l.key)
}

func verifyChain(ctx context.Context, q querier, key []byte) (Verification, error) {
	head, err := chainHead(ctx, q)
	if err != nil {
		return Verification{}, err
	}
	v := Verification{OK: true}
	prev := ""
	if head != "" {
		// The oldest entries are gone. Accept that only when a purge entry inside the
		// chain records exactly this anchor; a silent deletion of the head has none.
		var n int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE action = 'audit.purged'
			AND detail LIKE ? ESCAPE '\'`, "%"+escapeLike(purgeAnchorPrefix+head)+"%").Scan(&n); err != nil {
			return v, err
		}
		if n == 0 {
			v.OK = false
			_ = q.QueryRowContext(ctx, `SELECT id FROM audit_log ORDER BY id LIMIT 1`).Scan(&v.BrokenAt)
			return v, nil
		}
		prev, v.Anchored, v.AnchoredAt = head, true, head
	}
	rows, err := q.QueryContext(ctx, `SELECT id, at, COALESCE(user_id, 0), username, ip, action,
		connection_id, target, detail, outcome, prev_hash, hash FROM audit_log ORDER BY id`)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		var storedPrev string
		if err := rows.Scan(&e.ID, &e.At, &e.UserID, &e.Username, &e.IP, &e.Action, &e.ConnectionID,
			&e.Target, &e.Detail, &e.Outcome, &storedPrev, &e.Hash); err != nil {
			return v, err
		}
		v.Checked++
		if storedPrev != prev || !hmac.Equal([]byte(macWith(key, prev, &e)), []byte(e.Hash)) {
			v.OK, v.BrokenAt = false, e.ID
			return v, nil
		}
		prev = e.Hash
	}
	return v, rows.Err()
}

// chainHead is the prev_hash of the oldest remaining entry: "" for an intact
// chain, the purge anchor after a retention purge.
func chainHead(ctx context.Context, q querier) (string, error) {
	var head string
	err := q.QueryRowContext(ctx, `SELECT prev_hash FROM audit_log ORDER BY id LIMIT 1`).Scan(&head)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return head, err
}

var secretPatterns = []*regexp.Regexp{
	// CREATE/ALTER USER ... IDENTIFIED [WITH plugin] BY 'secret'  and  SET PASSWORD ... = 'secret'
	regexp.MustCompile(`(?is)(IDENTIFIED\s+(?:WITH\s+\S+\s+)?BY\s+)('(?:[^'\\]|\\.|'')*'|"(?:[^"\\]|\\.|"")*")`),
	regexp.MustCompile(`(?is)(PASSWORD\s*(?:FOR\s+\S+\s*)?=\s*)('(?:[^'\\]|\\.|'')*'|"(?:[^"\\]|\\.|"")*")`),
	regexp.MustCompile(`(?is)(PASSWORD\s*\(\s*)('(?:[^'\\]|\\.|'')*'|"(?:[^"\\]|\\.|"")*")`),
	regexp.MustCompile(`(?is)(MASTER_PASSWORD\s*=\s*|SOURCE_PASSWORD\s*=\s*)('(?:[^'\\]|\\.|'')*'|"(?:[^"\\]|\\.|"")*")`),
}

// RedactSQL masks credentials that commonly appear in administrative SQL.
func RedactSQL(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, "${1}'***'")
	}
	return s
}

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
