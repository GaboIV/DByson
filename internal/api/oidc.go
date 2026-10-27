package api

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
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
	// oidcStateID binds the sealed SSO state to its purpose (vault associated data).
	oidcStateID = "oidc-state"
)

// oidcState is a pending SSO login: the nonce the ID token must echo back and
// the PKCE verifier for the code exchange. It travels sealed inside the opaque
// state value (and its browser cookie), never in server memory, so an anonymous
// caller cannot fill a shared table and lock everyone out of SSO.
type oidcState struct {
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Expires  int64  `json:"e"`
}

func randomState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// usedStates remembers states that completed a valid provider exchange, so a
// captured state cannot be replayed within its TTL. Only fully verified logins
// add entries, so an anonymous caller cannot fill it (unlike a pending-state
// table), which is why the pending state itself stays stateless.
type usedStates struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func (u *usedStates) useOnce(key string, ttl time.Duration) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := time.Now()
	if u.m == nil {
		u.m = map[string]time.Time{}
	}
	for k, exp := range u.m {
		if now.After(exp) {
			delete(u.m, k)
		}
	}
	if _, seen := u.m[key]; seen {
		return false
	}
	u.m[key] = now.Add(ttl)
	return true
}

// newOIDCState seals a fresh pending login into an opaque, authenticated state
// value and returns it with the nonce and PKCE challenge to send the provider.
func (s *Server) newOIDCState() (state, nonce, challenge string, err error) {
	if s.Vault == nil {
		return "", "", "", errors.New("vault required for SSO state")
	}
	if nonce, err = randomState(); err != nil {
		return
	}
	verifier, challenge, err := sso.NewPKCE()
	if err != nil {
		return "", "", "", err
	}
	payload, err := json.Marshal(oidcState{Nonce: nonce, Verifier: verifier, Expires: time.Now().Add(oidcStateTTL).Unix()})
	if err != nil {
		return "", "", "", err
	}
	wrapped, ct, err := s.Vault.Seal(oidcStateID, payload)
	if err != nil {
		return "", "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(wrapped) + "." + base64.RawURLEncoding.EncodeToString(ct), nonce, challenge, nil
}

// openOIDCState authenticates and decodes a state value, rejecting a tampered or
// expired one. It keeps no server state, so the value could open more than once;
// replay is stopped by the one-time state cookie and the provider's one-time code.
func (s *Server) openOIDCState(state string) (oidcState, bool) {
	if s.Vault == nil {
		return oidcState{}, false
	}
	a, b, found := strings.Cut(state, ".")
	if !found {
		return oidcState{}, false
	}
	wrapped, err1 := base64.RawURLEncoding.DecodeString(a)
	ct, err2 := base64.RawURLEncoding.DecodeString(b)
	if err1 != nil || err2 != nil {
		return oidcState{}, false
	}
	payload, err := s.Vault.Open(oidcStateID, wrapped, ct)
	if err != nil {
		return oidcState{}, false
	}
	var st oidcState
	if json.Unmarshal(payload, &st) != nil || time.Now().Unix() > st.Expires {
		return oidcState{}, false
	}
	return st, true
}

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

func (s *Server) setShortCookie(w http.ResponseWriter, r *http.Request, name, value, path string, sameSite http.SameSite, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: path, HttpOnly: true,
		Secure: s.isHTTPS(r), SameSite: sameSite, MaxAge: int(ttl.Seconds())})
}

func (s *Server) clearShortCookie(w http.ResponseWriter, r *http.Request, name, path string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: path, HttpOnly: true, Secure: s.isHTTPS(r), MaxAge: -1})
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
	state, nonce, challenge, err := s.newOIDCState()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "try again later")
		return
	}
	target, err := p.AuthorizeURL(state, nonce, challenge)
	if err != nil {
		writeError(w, http.StatusBadGateway, "identity provider is unreachable")
		return
	}
	s.setShortCookie(w, r, oidcStateCookie, state, "/api/auth/oidc", http.SameSiteLaxMode, oidcStateTTL)
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	p := s.oidcProvider()
	if p == nil || !p.Config().Enabled() {
		writeError(w, http.StatusNotFound, "SSO is not configured on this server")
		return
	}
	s.clearShortCookie(w, r, oidcStateCookie, "/api/auth/oidc")
	attempt := audit.Entry{IP: s.clientIP(r), Action: "auth.oidc.login"}
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
	pending, ok := s.openOIDCState(state)
	if !ok {
		fail(audit.OutcomeDenied, "invalid or expired state", "failed")
		return
	}
	rawToken, err := p.Exchange(r.URL.Query().Get("code"), pending.Verifier)
	if err != nil {
		fail(audit.OutcomeDenied, "code exchange failed", "failed")
		return
	}
	claims, err := p.Verify(rawToken, pending.Nonce)
	if err != nil {
		fail(audit.OutcomeDenied, "invalid ID token", "failed")
		return
	}
	// Defense in depth beyond the provider's one-time code: a given state logs in
	// at most once, so a captured state and cookie cannot be replayed.
	if !s.oidcUsed.useOnce(challengeKey(state), oidcStateTTL) {
		fail(audit.OutcomeDenied, "state already used", "failed")
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
			s.setShortCookie(w, r, mfaPendingCookie, token, "/api/auth/mfa", http.SameSiteStrictMode, mfaChallengeTTL)
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
		auth.SessionDetails{IP: s.clientIP(r), UserAgent: truncateRunes(r.UserAgent(), 512), ClientCert: clientCertificate(r)})
	if err != nil {
		fail(audit.OutcomeError, "could not create session", "failed")
		return
	}
	s.setCookie(w, r, token)
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
	s.record(r.Context(), audit.Entry{UserID: id, Username: username, IP: s.clientIP(r),
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
