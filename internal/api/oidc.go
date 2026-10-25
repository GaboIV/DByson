package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gaboiv/dbyson/internal/audit"
	"github.com/gaboiv/dbyson/internal/auth"
	"github.com/gaboiv/dbyson/internal/sso"
)

// oidcState is a pending SSO login: CSRF protection plus the nonce the ID
// token must echo back.
type oidcState struct {
	nonce   string
	expires time.Time
}

type oidcStates struct {
	mu sync.Mutex
	m  map[string]oidcState
}

func randomState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (c *oidcStates) create() (state, nonce string, err error) {
	if state, err = randomState(); err != nil {
		return "", "", err
	}
	if nonce, err = randomState(); err != nil {
		return "", "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]oidcState{}
	}
	now := time.Now()
	for k, v := range c.m {
		if now.After(v.expires) {
			delete(c.m, k)
		}
	}
	if len(c.m) >= 256 {
		return "", "", errTooManyOIDC
	}
	c.m[challengeKey(state)] = oidcState{nonce: nonce, expires: now.Add(10 * time.Minute)}
	return state, nonce, nil
}

func (c *oidcStates) take(state string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := challengeKey(state)
	v, ok := c.m[k]
	delete(c.m, k)
	if !ok || time.Now().After(v.expires) {
		return "", false
	}
	return v.nonce, true
}

var errTooManyOIDC = errors.New("too many pending sign-ins")

func (s *Server) oidcProvider() *sso.Provider { return s.OIDC }

func (s *Server) handleOIDCStatus(w http.ResponseWriter, r *http.Request) {
	p := s.oidcProvider()
	enabled := p != nil && p.Config().Enabled()
	var name string
	if enabled {
		name = p.Config().Name()
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "provider": name})
}

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	p := s.oidcProvider()
	if p == nil || !p.Config().Enabled() {
		writeError(w, http.StatusNotFound, "SSO is not configured on this server")
		return
	}
	if p.Config().RedirectURL == "" {
		writeError(w, http.StatusServiceUnavailable, "SSO callback URL is not configured")
		return
	}
	state, nonce, err := s.oidcStates.create()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "try again later")
		return
	}
	target, err := p.AuthorizeURL(state, nonce)
	if err != nil {
		writeError(w, http.StatusBadGateway, "identity provider is unreachable")
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	p := s.oidcProvider()
	if p == nil || !p.Config().Enabled() {
		writeError(w, http.StatusNotFound, "SSO is not configured on this server")
		return
	}
	attempt := audit.Entry{IP: clientIP(r), Action: "auth.oidc.login"}
	fail := func(outcome, detail, reason string) {
		attempt.Outcome, attempt.Detail = outcome, detail
		s.record(r.Context(), attempt)
		http.Redirect(w, r, "/?sso=error&reason="+reason, http.StatusFound)
	}
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		fail(audit.OutcomeDenied, "provider: "+truncateRunes(errMsg, 200), "provider")
		return
	}
	nonce, ok := s.oidcStates.take(r.URL.Query().Get("state"))
	if !ok {
		fail(audit.OutcomeDenied, "invalid or expired state", "failed")
		return
	}
	rawToken, err := p.Exchange(r.URL.Query().Get("code"))
	if err != nil {
		fail(audit.OutcomeDenied, "code exchange failed", "failed")
		return
	}
	claims, err := p.Verify(rawToken, nonce)
	if err != nil {
		fail(audit.OutcomeDenied, "invalid ID token", "failed")
		return
	}
	userID, username, _, err := s.linkOrProvisionOIDC(r, p.Config(), claims)
	if err != nil {
		reason := "failed"
		if err == errOIDCNotProvisioned {
			reason = "provisioned"
		}
		fail(audit.OutcomeDenied, err.Error(), reason)
		return
	}
	attempt.UserID, attempt.Username, attempt.Outcome = userID, username, audit.OutcomeOK
	s.record(r.Context(), attempt)
	// Browser flow: set the session cookie and land back in the app, which
	// picks the session up via /api/auth/me.
	token, _, err := s.sessions.CreateWithDetails(r.Context(), userID, false,
		auth.SessionDetails{IP: clientIP(r), UserAgent: truncateRunes(r.UserAgent(), 512), ClientCert: clientCertificate(r)})
	if err != nil {
		fail(audit.OutcomeError, "could not create session", "failed")
		return
	}
	setCookie(w, r, token)
	http.Redirect(w, r, "/", http.StatusFound)
}

var errOIDCNotProvisioned = errors.New("account not provisioned")

// linkOrProvisionOIDC resolves an SSO identity to a local user: an existing
// link wins, otherwise (only when auto-provisioning is on) a member account is
// created, reusing a matching verified email when one exists.
func (s *Server) linkOrProvisionOIDC(r *http.Request, cfg sso.Config, claims *sso.Claims) (int64, string, string, error) {
	ctx := r.Context()
	var userID int64
	err := s.Store.DB.QueryRowContext(ctx,
		`SELECT user_id FROM oidc_accounts WHERE provider = ? AND subject = ?`, cfg.Issuer, claims.Subject).
		Scan(&userID)
	if err == nil {
		var username, role string
		var disabled bool
		if err := s.Store.DB.QueryRowContext(ctx,
			`SELECT username, role, disabled FROM users WHERE id = ?`, userID).
			Scan(&username, &role, &disabled); err != nil {
			return 0, "", "", errOIDCNotProvisioned
		}
		if disabled {
			return 0, "", "", errOIDCNotProvisioned
		}
		return userID, username, role, nil
	}
	if err != sql.ErrNoRows {
		return 0, "", "", err
	}
	if !cfg.AutoProvision {
		return 0, "", "", errOIDCNotProvisioned
	}
	// Reuse a local account whose email matches a verified SSO email, so teams
	// created before SSO keep their connections and grants.
	if claims.Email != "" && claims.EmailVerified {
		_ = s.Store.DB.QueryRowContext(ctx,
			`SELECT id FROM users WHERE email = ? AND disabled = 0 LIMIT 1`, claims.Email).Scan(&userID)
	}
	username := ""
	if userID == 0 {
		username = ssoUsername(claims)
		hash, herr := auth.HashPassword(randomPassword())
		if herr != nil {
			return 0, "", "", herr
		}
		res, herr := s.Store.DB.ExecContext(ctx,
			`INSERT INTO users (username, password_hash, role, email, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			username, hash, RoleMember, claims.Email, time.Now().Unix())
		if herr != nil {
			if strings.Contains(strings.ToLower(herr.Error()), "unique") {
				username += "-" + shortID()
				res, herr = s.Store.DB.ExecContext(ctx,
					`INSERT INTO users (username, password_hash, role, email, created_at)
					 VALUES (?, ?, ?, ?, ?)`,
					username, hash, RoleMember, claims.Email, time.Now().Unix())
			}
			if herr != nil {
				return 0, "", "", herr
			}
		}
		userID, _ = res.LastInsertId()
		s.record(ctx, audit.Entry{UserID: userID, Username: username, IP: clientIP(r),
			Action: "user.create", Target: username, Detail: "sso auto-provision", Outcome: audit.OutcomeOK})
	}
	if _, err := s.Store.DB.ExecContext(ctx,
		`INSERT INTO oidc_accounts (provider, subject, user_id) VALUES (?,?,?)
		 ON CONFLICT(provider, subject) DO NOTHING`, cfg.Issuer, claims.Subject, userID); err != nil {
		return 0, "", "", err
	}
	var name, role string
	var disabled bool
	if err := s.Store.DB.QueryRowContext(ctx,
		`SELECT username, role, disabled FROM users WHERE id = ?`, userID).
		Scan(&name, &role, &disabled); err != nil || disabled {
		return 0, "", "", errOIDCNotProvisioned
	}
	return userID, name, role, nil
}

// ssoUsername derives a unique-enough local username from SSO claims.
func ssoUsername(claims *sso.Claims) string {
	base := claims.PreferredUsername
	if base == "" && claims.Email != "" {
		base, _, _ = strings.Cut(claims.Email, "@")
	}
	if base == "" && claims.Name != "" {
		base = strings.ToLower(strings.Join(strings.Fields(claims.Name), "."))
	}
	base = strings.TrimSpace(base)
	if !validUsername(base) {
		base = "sso-user"
	}
	return base
}

func randomPassword() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b) + "0aA!"
}

func shortID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
