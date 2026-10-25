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
	ID         string
	ClientCert string
	UserID     int64
	Username   string
	Role       string
	CSRF       string
	MFA        bool // the session was established (or upgraded) with a second factor
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
func (s *Sessions) Create(ctx context.Context, userID int64, mfa bool) (token, csrf string, err error) {
	return s.CreateWithDetails(ctx, userID, mfa, SessionDetails{})
}

type SessionDetails struct{ IP, UserAgent, ClientCert string }

func (s *Sessions) CreateWithDetails(ctx context.Context, userID int64, mfa bool, details SessionDetails) (token, csrf string, err error) {
	if token, err = randomToken(); err != nil {
		return
	}
	if csrf, err = randomToken(); err != nil {
		return
	}
	now := s.Now().Unix()
	id, err := randomToken()
	if err != nil {
		return "", "", err
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO sessions (id_hash, user_id, csrf, created_at, last_seen, expires_at, mfa, public_id, ip, user_agent, client_cert) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		hashToken(token), userID, csrf, now, now, now+int64(AbsoluteTimeout.Seconds()), mfa, id, details.IP, details.UserAgent, details.ClientCert)
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
		SELECT u.id, u.username, u.role, s.csrf, s.mfa, s.last_seen, s.expires_at, s.public_id, s.client_cert
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.id_hash = ? AND u.disabled = 0`, h).
		Scan(&sess.UserID, &sess.Username, &sess.Role, &sess.CSRF, &sess.MFA, &lastSeen, &expires, &sess.ID, &sess.ClientCert)
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

type SessionInfo struct {
	ID               string `json:"id"`
	IP               string `json:"ip"`
	UserAgent        string `json:"userAgent"`
	CreatedAt        int64  `json:"createdAt"`
	LastSeen         int64  `json:"lastSeen"`
	ExpiresAt        int64  `json:"expiresAt"`
	MFA              bool   `json:"mfa"`
	Current          bool   `json:"current"`
	CertificateBound bool   `json:"certificateBound"`
}

func (s *Sessions) List(ctx context.Context, userID int64, currentID string) ([]SessionInfo, error) {
	now := s.Now().Unix()
	rows, err := s.DB.QueryContext(ctx, `SELECT public_id, ip, user_agent, created_at, last_seen, expires_at, mfa, client_cert <> '' FROM sessions WHERE user_id = ? AND expires_at > ? AND last_seen > ? ORDER BY created_at DESC, public_id`, userID, now, now-int64(IdleTimeout.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionInfo{}
	for rows.Next() {
		var item SessionInfo
		if err := rows.Scan(&item.ID, &item.IP, &item.UserAgent, &item.CreatedAt, &item.LastSeen, &item.ExpiresAt, &item.MFA, &item.CertificateBound); err != nil {
			return nil, err
		}
		item.Current = item.ID == currentID
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Sessions) Revoke(ctx context.Context, userID int64, publicID string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND public_id = ?`, userID, publicID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetMFA marks the session identified by token as verified with a second factor.
func (s *Sessions) SetMFA(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE sessions SET mfa = 1 WHERE id_hash = ?`, hashToken(token))
	return err
}

// DeleteOthers revokes every session of the user except the one identified by token.
// Used when the account's authentication factors change.
func (s *Sessions) DeleteOthers(ctx context.Context, userID int64, token string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id_hash <> ?`, userID, hashToken(token))
	return err
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
