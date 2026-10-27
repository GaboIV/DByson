package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gaboiv/dbyson/internal/audit"
	"github.com/gaboiv/dbyson/internal/auth"
)

const cookieName = "dbyson_session"

type ctxKey struct{}

func sessionFrom(r *http.Request) *auth.Session {
	s, _ := r.Context().Value(ctxKey{}).(*auth.Session)
	return s
}

// requireAuth validates the session cookie and, for state-changing methods, the CSRF header.
func (s *Server) requireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		sess, err := s.sessions.Lookup(r.Context(), c.Value)
		if err != nil || sess.ClientCert != clientCertificate(r) {
			s.clearCookie(w, r)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, sess)
		safe := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions
		action := actionNames[r.Pattern]
		if action == "" {
			action = r.Pattern
		}
		rec := &auditRecord{entry: audit.Entry{
			UserID: sess.UserID, Username: sess.Username, IP: s.clientIP(r), Action: action,
			ConnectionID: r.PathValue("id"), Target: auditTarget(r),
		}}
		ctx = context.WithValue(ctx, auditKey{}, rec)
		r = r.WithContext(ctx)

		if !safe && !sess.CSRFMatches(r.Header.Get("X-CSRF-Token")) {
			rec.entry.Outcome, rec.entry.Detail = audit.OutcomeDenied, "invalid CSRF token"
			s.record(ctx, rec.entry)
			writeError(w, http.StatusForbidden, "invalid CSRF token")
			return
		}
		// Policy "all": only sign-in and 2FA management are reachable without a verified session.
		if s.MFAPolicy == MFAPolicyAll && !sess.MFA && !strings.HasPrefix(r.URL.Path, "/api/auth/") {
			writeMFARequired(w)
			return
		}

		sw := &statusWriter{ResponseWriter: w}
		next(sw, r)
		if rec.skip || (safe && !rec.force) {
			return
		}
		if rec.entry.Outcome == "" {
			rec.entry.Outcome = outcomeFor(sw.status)
		}
		s.record(ctx, rec.entry)
	})
}

func (s *Server) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	// Believe X-Forwarded-Proto only from a configured proxy. When none is set we
	// keep trusting it, so existing TLS-terminating deployments do not regress.
	if len(s.TrustedProxies) == 0 || s.peerIsTrustedProxy(r) {
		return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	}
	return false
}

// peerIsTrustedProxy reports whether the socket peer is a configured proxy.
func (s *Server) peerIsTrustedProxy(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return s.isTrustedProxy(host)
}

func (s *Server) isTrustedProxy(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	// A dual-stack listener reports an IPv4 peer as ::ffff:a.b.c.d; unmap it so an
	// IPv4 trusted-proxy CIDR still matches (Prefix.Contains needs same family).
	addr = addr.Unmap()
	for _, p := range s.TrustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func clientCertificate(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	sum := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
	return hex.EncodeToString(sum[:])
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(auth.AbsoluteTimeout.Seconds()),
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: s.isHTTPS(r), SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}

// clientIP returns the socket peer by default, so a spoofed X-Forwarded-For can
// neither forge an audit IP nor collapse everyone onto one throttle bucket. When
// the peer is a configured trusted proxy, it instead returns the rightmost
// forwarded address that is not itself a trusted proxy: the real client as the
// outermost trusted hop saw it.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !s.isTrustedProxy(host) {
		return host
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := strings.TrimSpace(parts[i])
		if ip != "" && !s.isTrustedProxy(ip) {
			return ip
		}
	}
	return host
}

type sessionResponse struct {
	Username   string `json:"username"`
	Role       string `json:"role"`
	CSRF       string `json:"csrf"`
	MFA        bool   `json:"mfa"`        // this session was verified with a second factor
	MFAEnabled bool   `json:"mfaEnabled"` // the account has 2FA configured
	MFAPolicy  string `json:"mfaPolicy"`
}

func (s *Server) sessionView(ctx context.Context, userID int64, username, role, csrf string, mfa bool) sessionResponse {
	var enabled bool
	_ = s.Store.DB.QueryRowContext(ctx, `SELECT totp_enabled FROM users WHERE id = ?`, userID).Scan(&enabled)
	return sessionResponse{Username: username, Role: role, CSRF: csrf, MFA: mfa, MFAEnabled: enabled, MFAPolicy: s.MFAPolicy}
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID int64, username, role string, mfa bool) {
	token, csrf, err := s.sessions.CreateWithDetails(r.Context(), userID, mfa, auth.SessionDetails{IP: s.clientIP(r), UserAgent: truncateRunes(r.UserAgent(), 512), ClientCert: clientCertificate(r)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	s.setCookie(w, r, token)
	writeJSON(w, http.StatusOK, s.sessionView(r.Context(), userID, username, role, csrf, mfa))
}

func (s *Server) userCount(ctx context.Context) (int, error) {
	var n int
	err := s.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.userCount(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"setupRequired": n == 0,
		"setupToken":    s.SetupToken != "",
	})
}

type credentials struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	SetupToken string `json:"setupToken"`
}

func validUsername(u string) bool {
	n := utf8.RuneCountInString(u)
	if n < 3 || n > 64 {
		return false
	}
	for _, c := range u {
		if c <= ' ' || c == 0x7f {
			return false
		}
	}
	return true
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !readJSON(w, r, &in) {
		return
	}
	key := "setup|" + s.clientIP(r)
	if blocked, wait := s.throttle.Blocked(key); blocked {
		tooManyAttempts(w, wait)
		return
	}
	if s.SetupToken != "" && subtle.ConstantTimeCompare([]byte(in.SetupToken), []byte(s.SetupToken)) != 1 {
		s.throttle.Fail(key)
		writeError(w, http.StatusForbidden, "invalid setup token")
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if !validUsername(in.Username) {
		writeError(w, http.StatusBadRequest, "username must be 3-64 characters without spaces")
		return
	}
	if n := utf8.RuneCountInString(in.Password); n < auth.MinPasswordLen || n > auth.MaxPasswordLen {
		writeError(w, http.StatusBadRequest, "password must be between "+strconv.Itoa(auth.MinPasswordLen)+" and "+strconv.Itoa(auth.MaxPasswordLen)+" characters")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Atomic "only if no user exists yet": two racing setups cannot both win.
	res, err := s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO users (username, password_hash, role, created_at)
		 SELECT ?, ?, 'admin', ? WHERE NOT EXISTS (SELECT 1 FROM users)`,
		in.Username, hash, time.Now().Unix())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusConflict, "setup already completed")
		return
	}
	id, _ := res.LastInsertId()
	s.record(r.Context(), audit.Entry{UserID: id, Username: in.Username, IP: s.clientIP(r), Action: "auth.setup", Outcome: audit.OutcomeOK})
	s.startSession(w, r, id, in.Username, "admin", false)
}

func tooManyAttempts(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.PasswordLoginDisabled {
		s.record(r.Context(), audit.Entry{IP: s.clientIP(r), Action: "auth.login", Outcome: audit.OutcomeDenied, Detail: "password sign-in disabled"})
		writeError(w, http.StatusForbidden, "password sign-in is disabled; use the configured identity provider")
		return
	}
	var in credentials
	if !readJSON(w, r, &in) {
		return
	}
	ip := s.clientIP(r)
	userKey := "login|" + ip + "|" + strings.ToLower(in.Username)
	ipKey := "login|" + ip
	attempt := audit.Entry{Username: truncateRunes(in.Username, 64), IP: ip, Action: "auth.login"}
	blockedUser, wait := s.throttle.Blocked(userKey)
	blockedIP, waitIP := s.ipThrottle.Blocked(ipKey)
	if blockedUser || blockedIP {
		if blockedIP && waitIP > wait {
			wait = waitIP
		}
		attempt.Outcome, attempt.Detail = audit.OutcomeDenied, "throttled"
		s.record(r.Context(), attempt)
		tooManyAttempts(w, wait)
		return
	}

	var (
		id       int64
		hash     string
		role     string
		name     string
		disabled bool
	)
	err := s.Store.DB.QueryRowContext(r.Context(),
		`SELECT id, username, password_hash, role, disabled FROM users WHERE username = ?`, in.Username).
		Scan(&id, &name, &hash, &role, &disabled)

	ok := false
	if err == nil && !disabled && len(in.Password) <= auth.MaxPasswordLen {
		ok, _ = auth.VerifyPassword(in.Password, hash)
	} else {
		auth.BurnPasswordCheck(in.Password) // same cost whether or not the user exists
	}
	if !ok {
		s.throttle.Fail(userKey)
		s.ipThrottle.Fail(ipKey) // slows username spraying from a single address
		attempt.Outcome, attempt.Detail = audit.OutcomeDenied, "invalid credentials"
		s.record(r.Context(), attempt)
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	s.throttle.Reset(userKey)
	attempt.UserID, attempt.Username = id, name

	var mfaEnabled bool
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT totp_enabled FROM users WHERE id = ?`, id).Scan(&mfaEnabled); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if mfaEnabled {
		// Password accepted, but no session until the second factor is verified.
		token, err := s.mfaPending.create(id, name, role)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "try again later")
			return
		}
		attempt.Outcome, attempt.Detail = audit.OutcomeOK, "password accepted; second factor pending"
		s.record(r.Context(), attempt)
		writeJSON(w, http.StatusOK, map[string]any{"mfaRequired": true, "mfaToken": token})
		return
	}
	attempt.Outcome = audit.OutcomeOK
	s.record(r.Context(), attempt)
	s.startSession(w, r, id, name, role, false)
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	s.queries.revokeSessions(sess.UserID, func(id string) bool { return id == sess.ID })
	if c, err := r.Cookie(cookieName); err == nil {
		_ = s.sessions.Delete(r.Context(), c.Value)
	}
	s.clearCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	writeJSON(w, http.StatusOK, s.sessionView(r.Context(), sess.UserID, sess.Username, sess.Role, sess.CSRF, sess.MFA))
}
