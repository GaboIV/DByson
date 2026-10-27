package api

import (
	"crypto/rand"
	"crypto/subtle"
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

const (
	// oidcStateCookie binds a pending SSO login to the browser that started it,
	// so a callback URL crafted by someone else cannot log the victim into the
	// attacker's account (login CSRF). Lax: it must survive the top-level
	// redirect back from the provider.
	oidcStateCookie = "dbyson_oidc_state"
	// mfaPendingCookie carries the second-factor challenge of an SSO login for an
	// account with local 2FA; the browser lands on /?sso=mfa and posts the code.
	mfaPendingCookie = "dbyson_mfa_pending"
	oidcStateTTL     = 10 * time.Minute
)

// oidcState is a pending SSO login: the nonce the ID token must echo back and the
// PKCE verifier for the code exchange.
type oidcState struct {
	nonce    string
	verifier string
	expires  time.Time
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

// create registers a login attempt and returns its state, nonce and PKCE challenge.
func (c *oidcStates) create() (state, nonce, challenge string, err error) {
	if state, err = randomState(); err != nil {
		return
	}
	if nonce, err = randomState(); err != nil {
		return
	}
	verifier, challenge, err := sso.NewPKCE()
	if err != nil {
		return
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
		return "", "", "", errTooManyOIDC
	}
	c.m[challengeKey(state)] = oidcState{nonce: nonce, verifier: verifier, expires: now.Add(oidcStateTTL)}
	return state, nonce, challenge, nil
}

// take consumes a state: each one works once.
func (c *oidcStates) take(state string) (oidcState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := challengeKey(state)
	v, ok := c.m[k]
	delete(c.m, k)
	if !ok || time.Now().After(v.expires) {
		return oidcState{}, false
	}
	return v, true
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
	google := enabled && p.Config().IsGoogle()
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "provider": name, "google": google, "credentialsEnabled": !s.PasswordLoginDisabled})
}

func setShortCookie(w http.ResponseWriter, r *http.Request, name, value, path string, sameSite http.SameSite, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: path, HttpOnly: true,
		Secure: isHTTPS(r), SameSite: sameSite, MaxAge: int(ttl.Seconds())})
}

func clearShortCookie(w http.ResponseWriter, r *http.Request, name, path string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: path, HttpOnly: true, Secure: isHTTPS(r), MaxAge: -1})
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
	state, nonce, challenge, err := s.oidcStates.create()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "try again later")
		return
	}
	target, err := p.AuthorizeURL(state, nonce, challenge)
	if err != nil {
		writeError(w, http.StatusBadGateway, "identity provider is unreachable")
		return
	}
	setShortCookie(w, r, oidcStateCookie, state, "/api/auth/oidc", http.SameSiteLaxMode, oidcStateTTL)
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	p := s.oidcProvider()
	if p == nil || !p.Config().Enabled() {
		writeError(w, http.StatusNotFound, "SSO is not configured on this server")
		return
	}
	clearShortCookie(w, r, oidcStateCookie, "/api/auth/oidc")
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
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie(oidcStateCookie)
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		fail(audit.OutcomeDenied, "state does not belong to this browser", "failed")
		return
	}
	pending, ok := s.oidcStates.take(state)
	if !ok {
		fail(audit.OutcomeDenied, "invalid or expired state", "failed")
		return
	}
	rawToken, err := p.Exchange(r.URL.Query().Get("code"), pending.verifier)
	if err != nil {
		fail(audit.OutcomeDenied, "code exchange failed", "failed")
		return
	}
	claims, err := p.Verify(rawToken, pending.nonce)
	if err != nil {
		fail(audit.OutcomeDenied, "invalid ID token", "failed")
		return
	}
	cfg := p.Config()
	if !cfg.IdentityAllowed(claims) {
		fail(audit.OutcomeDenied, "email outside the allowed SSO domains", "domain")
		return
	}
	userID, username, role, err := s.linkOrProvisionOIDC(r, cfg, claims)
	if err != nil {
		reason := "failed"
		if errors.Is(err, errOIDCNotProvisioned) {
			reason = "provisioned"
		}
		fail(audit.OutcomeDenied, err.Error(), reason)
		return
	}
	attempt.UserID, attempt.Username = userID, username

	mfa := cfg.TrustMFA || claims.MFA()
	if !mfa {
		var localMFA bool
		if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT totp_enabled FROM users WHERE id = ?`, userID).Scan(&localMFA); err != nil {
			fail(audit.OutcomeError, "could not read account", "failed")
			return
		}
		if localMFA {
			// SSO proved the identity, not the second factor this account requires.
			token, err := s.mfaPending.create(userID, username, role)
			if err != nil {
				fail(audit.OutcomeError, "could not start second factor", "failed")
				return
			}
			attempt.Outcome, attempt.Detail = audit.OutcomeOK, "identity verified by SSO; second factor pending"
			s.record(r.Context(), attempt)
			setShortCookie(w, r, mfaPendingCookie, token, "/api/auth/mfa", http.SameSiteStrictMode, mfaChallengeTTL)
			http.Redirect(w, r, "/?sso=mfa", http.StatusFound)
			return
		}
	}
	if mfa {
		attempt.Detail = "second factor asserted by the identity provider"
	}
	attempt.Outcome = audit.OutcomeOK
	s.record(r.Context(), attempt)
	token, _, err := s.sessions.CreateWithDetails(r.Context(), userID, mfa,
		auth.SessionDetails{IP: clientIP(r), UserAgent: truncateRunes(r.UserAgent(), 512), ClientCert: clientCertificate(r)})
	if err != nil {
		fail(audit.OutcomeError, "could not create session", "failed")
		return
	}
	setCookie(w, r, token)
	http.Redirect(w, r, "/", http.StatusFound)
}

var errOIDCNotProvisioned = errors.New("account not provisioned")

// linkOrProvisionOIDC resolves an SSO identity to a local user, in this order:
//  1. an existing link (provider + subject);
//  2. the one enabled local account whose email matches the verified SSO email,
//     as set by an admin — linked even without auto-provisioning, which is how
//     teams created before SSO keep their connections and grants;
//  3. a new member account, only with auto-provisioning on.
//
// An ambiguous email (several accounts) never links: it would be a guess.
func (s *Server) linkOrProvisionOIDC(r *http.Request, cfg sso.Config, claims *sso.Claims) (int64, string, string, error) {
	ctx := r.Context()
	var userID int64
	err := s.Store.DB.QueryRowContext(ctx,
		`SELECT user_id FROM oidc_accounts WHERE provider = ? AND subject = ?`, cfg.Issuer, claims.Subject).
		Scan(&userID)
	if err != nil && err != sql.ErrNoRows {
		return 0, "", "", err
	}
	if err == sql.ErrNoRows {
		if claims.Email != "" && claims.EmailVerified {
			var matches int
			var match int64
			if err := s.Store.DB.QueryRowContext(ctx,
				`SELECT COUNT(*), COALESCE(MAX(id), 0) FROM users WHERE lower(email) = lower(?) AND disabled = 0`,
				claims.Email).Scan(&matches, &match); err != nil {
				return 0, "", "", err
			}
			if matches == 1 {
				userID = match
			}
		}
		if userID == 0 {
			if !cfg.AutoProvision {
				return 0, "", "", errOIDCNotProvisioned
			}
			if userID, err = s.provisionOIDCUser(r, claims); err != nil {
				return 0, "", "", err
			}
		}
		if _, err := s.Store.DB.ExecContext(ctx,
			`INSERT INTO oidc_accounts (provider, subject, user_id) VALUES (?,?,?)
			 ON CONFLICT(provider, subject) DO NOTHING`, cfg.Issuer, claims.Subject, userID); err != nil {
			return 0, "", "", err
		}
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

// provisionOIDCUser creates a member account for a first SSO sign-in. Its random
// password is never shown: the account signs in through SSO.
func (s *Server) provisionOIDCUser(r *http.Request, claims *sso.Claims) (int64, error) {
	hash, err := auth.HashPassword(randomPassword())
	if err != nil {
		return 0, err
	}
	username := ssoUsername(claims)
	insert := func(name string) (sql.Result, error) {
		return s.Store.DB.ExecContext(r.Context(),
			`INSERT INTO users (username, password_hash, role, email, created_at) VALUES (?, ?, ?, ?, ?)`,
			name, hash, RoleMember, claims.Email, time.Now().Unix())
	}
	res, err := insert(username)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		username += "-" + shortID()
		res, err = insert(username)
	}
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	s.record(r.Context(), audit.Entry{UserID: id, Username: username, IP: clientIP(r),
		Action: "user.create", Target: username, Detail: "sso auto-provision", Outcome: audit.OutcomeOK})
	return id, nil
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
