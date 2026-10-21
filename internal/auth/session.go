package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

const (
	IdleTimeout     = 2 * time.Hour
	AbsoluteTimeout = 12 * time.Hour
)

var ErrNoSession = errors.New("no valid session")

type Session struct {
	UserID   int64
	Username string
	Role     string
	CSRF     string
}

// Sessions persists sessions in SQLite. Only a SHA-256 of the token is stored, so a leaked
// database does not yield usable session cookies.
type Sessions struct {
	DB  *sql.DB
	Now func() time.Time
}

func NewSessions(db *sql.DB) *Sessions { return &Sessions{DB: db, Now: time.Now} }

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Create starts a session and returns the raw token (for the cookie) and the CSRF token.
func (s *Sessions) Create(ctx context.Context, userID int64) (token, csrf string, err error) {
	if token, err = randomToken(); err != nil {
		return
	}
	if csrf, err = randomToken(); err != nil {
		return
	}
	now := s.Now().Unix()
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO sessions (id_hash, user_id, csrf, created_at, last_seen, expires_at) VALUES (?,?,?,?,?,?)`,
		hashToken(token), userID, csrf, now, now, now+int64(AbsoluteTimeout.Seconds()))
	return
}

// Lookup validates a token, enforces idle and absolute timeouts, and slides the idle window.
func (s *Sessions) Lookup(ctx context.Context, token string) (*Session, error) {
	if token == "" {
		return nil, ErrNoSession
	}
	h := hashToken(token)
	var sess Session
	var lastSeen, expires int64
	err := s.DB.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.role, s.csrf, s.last_seen, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.id_hash = ?`, h).
		Scan(&sess.UserID, &sess.Username, &sess.Role, &sess.CSRF, &lastSeen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, err
	}
	now := s.Now().Unix()
	if now >= expires || now-lastSeen >= int64(IdleTimeout.Seconds()) {
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, h)
		return nil, ErrNoSession
	}
	if now-lastSeen > 60 { // avoid a write on every request
		_, _ = s.DB.ExecContext(ctx, `UPDATE sessions SET last_seen = ? WHERE id_hash = ?`, now, h)
	}
	return &sess, nil
}

func (s *Sessions) Delete(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, hashToken(token))
	return err
}

// PurgeExpired removes sessions past their absolute or idle deadline.
func (s *Sessions) PurgeExpired(ctx context.Context) error {
	now := s.Now().Unix()
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ? OR last_seen <= ?`, now, now-int64(IdleTimeout.Seconds()))
	return err
}

// CSRFMatches compares the request token with the session token in constant time.
func (sess *Session) CSRFMatches(got string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRF)) == 1
}
