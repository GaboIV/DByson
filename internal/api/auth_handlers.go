package api

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adminbase/adminbase/internal/auth"
)

const cookieName = "ab_session"

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
		if err != nil {
			clearCookie(w, r)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !sess.CSRFMatches(r.Header.Get("X-CSRF-Token")) {
				writeError(w, http.StatusForbidden, "invalid CSRF token")
				return
			}
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, sess)))
	})
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func setCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(auth.AbsoluteTimeout.Seconds()),
	})
}

func clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: isHTTPS(r), SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}

// clientIP uses the socket address only. X-Forwarded-For is deliberately ignored: trusting it
// would let an attacker rotate the header to dodge login throttling.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type sessionResponse struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	CSRF     string `json:"csrf"`
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID int64, username, role string) {
	token, csrf, err := s.sessions.Create(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	setCookie(w, r, token)
	writeJSON(w, http.StatusOK, sessionResponse{Username: username, Role: role, CSRF: csrf})
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
	key := "setup|" + clientIP(r)
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
	s.startSession(w, r, id, in.Username, "admin")
}

func tooManyAttempts(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !readJSON(w, r, &in) {
		return
	}
	ip := clientIP(r)
	userKey := "login|" + ip + "|" + strings.ToLower(in.Username)
	ipKey := "login|" + ip
	if blocked, wait := s.throttle.Blocked(userKey); blocked {
		tooManyAttempts(w, wait)
		return
	}
	if blocked, wait := s.ipThrottle.Blocked(ipKey); blocked {
		tooManyAttempts(w, wait)
		return
	}

	var (
		id   int64
		hash string
		role string
		name string
	)
	err := s.Store.DB.QueryRowContext(r.Context(),
		`SELECT id, username, password_hash, role FROM users WHERE username = ?`, in.Username).
		Scan(&id, &name, &hash, &role)

	ok := false
	if err == nil && len(in.Password) <= auth.MaxPasswordLen {
		ok, _ = auth.VerifyPassword(in.Password, hash)
	} else {
		auth.BurnPasswordCheck(in.Password) // same cost whether or not the user exists
	}
	if !ok {
		s.throttle.Fail(userKey)
		s.ipThrottle.Fail(ipKey) // slows username spraying from a single address
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	s.throttle.Reset(userKey)
	s.startSession(w, r, id, name, role)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		_ = s.sessions.Delete(r.Context(), c.Value)
	}
	clearCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	writeJSON(w, http.StatusOK, sessionResponse{Username: sess.Username, Role: sess.Role, CSRF: sess.CSRF})
}
