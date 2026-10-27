package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/gaboiv/dbyson/internal/audit"
	"github.com/gaboiv/dbyson/internal/auth"
)

// MFA policies (DBYSON_REQUIRE_MFA):
//   - off:       (default in personal mode) 2FA is optional everywhere.
//   - sensitive: (default in team mode) prod connections and SSH tunnels need a session verified with 2FA.
//   - all:       every API call beyond login/2FA setup needs a 2FA-verified session.
const (
	MFAPolicyOff       = "off"
	MFAPolicySensitive = "sensitive"
	MFAPolicyAll       = "all"

	mfaIssuer         = "DByson"
	mfaChallengeTTL   = 5 * time.Minute
	mfaMaxAttempts    = 5
	mfaMaxChallenges  = 1000
	mfaRequiredCode   = "mfa_required"
	mfaRequiredReason = "Esta acción requiere verificación en dos pasos. Actívala en Seguridad y vuelve a iniciar sesión."
)

var errMFARequired = errors.New(mfaRequiredReason)

func ValidMFAPolicy(p string) bool {
	return p == MFAPolicyOff || p == MFAPolicySensitive || p == MFAPolicyAll
}

func writeMFARequired(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{"error": mfaRequiredReason, "code": mfaRequiredCode})
}

// mfaGate enforces the policy for using a connection with the given environment and transport.
func (s *Server) mfaGate(r *http.Request, env string, ssh bool) error {
	sess := sessionFrom(r)
	if sess == nil || sess.MFA {
		return nil
	}
	switch s.MFAPolicy {
	case MFAPolicyAll:
		return errMFARequired
	case MFAPolicySensitive:
		if env == "prod" || ssh {
			return errMFARequired
		}
	}
	return nil
}

// ---- login challenge (password accepted, second factor pending) ----

type mfaChallenge struct {
	userID   int64
	username string
	role     string
	expires  time.Time
	attempts int
}

type mfaChallenges struct {
	mu sync.Mutex
	m  map[string]*mfaChallenge
}

func challengeKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (c *mfaChallenges) create(userID int64, username, role string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, ch := range c.m {
		if now.After(ch.expires) {
			delete(c.m, k)
		}
	}
	if len(c.m) >= mfaMaxChallenges {
		return "", errors.New("too many pending sign-ins")
	}
	c.m[challengeKey(token)] = &mfaChallenge{userID: userID, username: username, role: role, expires: now.Add(mfaChallengeTTL)}
	return token, nil
}

// take returns the live challenge for token, counting the attempt; it is removed after the
// last allowed attempt or on success (see done).
func (c *mfaChallenges) take(token string) (*mfaChallenge, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := challengeKey(token)
	ch := c.m[k]
	if ch == nil || time.Now().After(ch.expires) {
		delete(c.m, k)
		return nil, false
	}
	ch.attempts++
	if ch.attempts >= mfaMaxAttempts {
		delete(c.m, k)
	}
	return ch, true
}

func (c *mfaChallenges) done(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, challengeKey(token))
}

// ---- stored factor ----

type totpState struct {
	enabled  bool
	secret   string // empty when no secret (pending or active) exists
	lastStep int64
}

func totpVaultID(userID int64) string { return "totp:" + strconv.FormatInt(userID, 10) }

func (s *Server) loadTOTP(ctx context.Context, userID int64) (totpState, error) {
	var st totpState
	var wrapped, sealed []byte
	err := s.Store.DB.QueryRowContext(ctx,
		`SELECT totp_enabled, totp_wrapped, totp_secret, totp_last_step FROM users WHERE id = ?`, userID).
		Scan(&st.enabled, &wrapped, &sealed, &st.lastStep)
	if err != nil {
		return st, err
	}
	if len(wrapped) > 0 && len(sealed) > 0 {
		plain, err := s.Vault.Open(totpVaultID(userID), wrapped, sealed)
		if err != nil {
			return st, err
		}
		st.secret = string(plain)
	}
	return st, nil
}

// verifySecondFactor accepts a current TOTP code or an unused recovery code.
// Both paths are single-use: the TOTP step and the recovery code are consumed atomically.
func (s *Server) verifySecondFactor(ctx context.Context, userID int64, code string, allowRecovery bool) (bool, string, error) {
	if auth.LooksLikeTOTP(code) {
		st, err := s.loadTOTP(ctx, userID)
		if err != nil || !st.enabled || st.secret == "" {
			return false, "totp", err
		}
		step, ok := auth.VerifyTOTP(st.secret, code, time.Now(), st.lastStep)
		if !ok {
			return false, "totp", nil
		}
		res, err := s.Store.DB.ExecContext(ctx,
			`UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?`, step, userID, step)
		if err != nil {
			return false, "totp", err
		}
		n, _ := res.RowsAffected()
		return n == 1, "totp", nil
	}
	if !allowRecovery {
		return false, "recovery", nil
	}
	res, err := s.Store.DB.ExecContext(ctx,
		`UPDATE recovery_codes SET used_at = ? WHERE user_id = ? AND code_hash = ? AND used_at IS NULL
		 AND EXISTS (SELECT 1 FROM users WHERE id = ? AND totp_enabled = 1)`,
		time.Now().Unix(), userID, auth.HashRecoveryCode(code), userID)
	if err != nil {
		return false, "recovery", err
	}
	n, _ := res.RowsAffected()
	return n == 1, "recovery", nil
}

func (s *Server) replaceRecoveryCodes(ctx context.Context, tx *sql.Tx, userID int64) ([]string, error) {
	codes, err := auth.NewRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	for _, c := range codes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_codes (user_id, code_hash) VALUES (?, ?)`,
			userID, auth.HashRecoveryCode(c)); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

// checkPassword re-authenticates the session user before changing their factors.
func (s *Server) checkPassword(w http.ResponseWriter, r *http.Request, password string) bool {
	sess := sessionFrom(r)
	key := "reauth|" + strconv.FormatInt(sess.UserID, 10)
	if blocked, wait := s.throttle.Blocked(key); blocked {
		tooManyAttempts(w, wait)
		return false
	}
	var hash string
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE id = ?`, sess.UserID).Scan(&hash); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return false
	}
	ok := false
	if len(password) <= auth.MaxPasswordLen {
		ok, _ = auth.VerifyPassword(password, hash)
	}
	if !ok {
		s.throttle.Fail(key)
		writeError(w, http.StatusUnauthorized, "Contraseña incorrecta")
		return false
	}
	s.throttle.Reset(key)
	return true
}

// ---- handlers ----

func (s *Server) handleMFAStatus(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	st, err := s.loadTOTP(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	var left int
	_ = s.Store.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM recovery_codes WHERE user_id = ? AND used_at IS NULL`, sess.UserID).Scan(&left)
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": st.enabled, "recoveryCodesLeft": left, "policy": s.MFAPolicy, "sessionVerified": sess.MFA,
	})
}

func (s *Server) handleMFASetup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) || !s.checkPassword(w, r, in.Password) {
		return
	}
	sess := sessionFrom(r)
	st, err := s.loadTOTP(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if st.enabled {
		writeError(w, http.StatusConflict, "La verificación en dos pasos ya está activa")
		return
	}
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	wrapped, sealed, err := s.Vault.Seal(totpVaultID(sess.UserID), []byte(secret))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Pending until confirmed with a code: totp_enabled stays 0.
	if _, err := s.Store.DB.ExecContext(r.Context(),
		`UPDATE users SET totp_wrapped = ?, totp_secret = ?, totp_last_step = 0 WHERE id = ? AND totp_enabled = 0`,
		wrapped, sealed, sess.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	uri := auth.TOTPURI(mfaIssuer, sess.Username, secret)
	png, err := qrcode.Encode(uri, qrcode.Medium, 256)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.recordFor(r, "auth.mfa.setup", "", "", audit.OutcomeOK)
	writeJSON(w, http.StatusOK, map[string]string{
		"secret": secret, "uri": uri, "qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	})
}

func (s *Server) handleMFAEnable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	sess := sessionFrom(r)
	st, err := s.loadTOTP(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if st.enabled || st.secret == "" {
		writeError(w, http.StatusConflict, "Primero genera un código QR nuevo")
		return
	}
	key := "mfa-enable|" + strconv.FormatInt(sess.UserID, 10)
	if blocked, wait := s.throttle.Blocked(key); blocked {
		tooManyAttempts(w, wait)
		return
	}
	step, ok := auth.VerifyTOTP(st.secret, in.Code, time.Now(), 0)
	if !ok {
		s.throttle.Fail(key)
		s.recordFor(r, "auth.mfa.enable", "", "invalid code", audit.OutcomeDenied)
		writeError(w, http.StatusBadRequest, "Código incorrecto. Revisa la hora de tu dispositivo e inténtalo de nuevo.")
		return
	}
	s.throttle.Reset(key)

	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(r.Context(),
		`UPDATE users SET totp_enabled = 1, totp_last_step = ? WHERE id = ? AND totp_enabled = 0`, step, sess.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		writeError(w, http.StatusConflict, "La verificación en dos pasos ya está activa")
		return
	}
	codes, err := s.replaceRecoveryCodes(r.Context(), tx, sess.UserID)
	if err != nil || tx.Commit() != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// This session just proved the factor; every other session predates it and is revoked.
	if c, err := r.Cookie(cookieName); err == nil {
		_ = s.sessions.SetMFA(r.Context(), c.Value)
		if err := s.sessions.DeleteOthers(r.Context(), sess.UserID, c.Value); err == nil {
			s.queries.revokeSessions(sess.UserID, func(id string) bool { return id != sess.ID })
		}
	}
	s.recordFor(r, "auth.mfa.enable", "", "", audit.OutcomeOK)
	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

func (s *Server) handleMFADisable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !readJSON(w, r, &in) || !s.checkPassword(w, r, in.Password) {
		return
	}
	sess := sessionFrom(r)
	ok, method, err := s.verifySecondFactor(r.Context(), sess.UserID, in.Code, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !ok {
		s.recordFor(r, "auth.mfa.disable", "", "invalid "+method+" code", audit.OutcomeDenied)
		writeError(w, http.StatusUnauthorized, "Código incorrecto")
		return
	}
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback()
	_, err1 := tx.ExecContext(r.Context(),
		`UPDATE users SET totp_enabled = 0, totp_wrapped = NULL, totp_secret = NULL, totp_last_step = 0 WHERE id = ?`, sess.UserID)
	_, err2 := tx.ExecContext(r.Context(), `DELETE FROM recovery_codes WHERE user_id = ?`, sess.UserID)
	_, err3 := tx.ExecContext(r.Context(), `UPDATE sessions SET mfa = 0 WHERE user_id = ?`, sess.UserID)
	if err1 != nil || err2 != nil || err3 != nil || tx.Commit() != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if c, err := r.Cookie(cookieName); err == nil {
		if err := s.sessions.DeleteOthers(r.Context(), sess.UserID, c.Value); err == nil {
			s.queries.revokeSessions(sess.UserID, func(id string) bool { return id != sess.ID })
		}
	}
	s.recordFor(r, "auth.mfa.disable", "", "", audit.OutcomeOK)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMFARecoveryCodes(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	sess := sessionFrom(r)
	ok, _, err := s.verifySecondFactor(r.Context(), sess.UserID, in.Code, false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !ok {
		s.recordFor(r, "auth.mfa.recovery_codes", "", "invalid code", audit.OutcomeDenied)
		writeError(w, http.StatusUnauthorized, "Código incorrecto")
		return
	}
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback()
	codes, err := s.replaceRecoveryCodes(r.Context(), tx, sess.UserID)
	if err != nil || tx.Commit() != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.recordFor(r, "auth.mfa.recovery_codes", "", "", audit.OutcomeOK)
	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

// handleMFAVerify completes a sign-in whose password step already succeeded.
func (s *Server) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"mfaToken"`
		Code  string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	// After an SSO sign-in the challenge travels in an HttpOnly cookie instead of
	// the response body, because the browser arrives through a redirect.
	fromCookie := false
	if in.Token == "" {
		if c, err := r.Cookie(mfaPendingCookie); err == nil {
			in.Token, fromCookie = c.Value, true
		}
	}
	ch, ok := s.mfaPending.take(in.Token)
	if !ok {
		if fromCookie {
			s.clearShortCookie(w, r, mfaPendingCookie, "/api/auth/mfa")
		}
		writeError(w, http.StatusUnauthorized, "La verificación expiró. Vuelve a iniciar sesión.")
		return
	}
	key := "mfa|" + strconv.FormatInt(ch.userID, 10)
	if blocked, wait := s.throttle.Blocked(key); blocked {
		tooManyAttempts(w, wait)
		return
	}
	e := audit.Entry{UserID: ch.userID, Username: ch.username, IP: s.clientIP(r), Action: "auth.login.mfa"}
	valid, method, err := s.verifySecondFactor(r.Context(), ch.userID, in.Code, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	e.Detail = method
	if !valid {
		s.throttle.Fail(key)
		e.Outcome = audit.OutcomeDenied
		s.record(r.Context(), e)
		writeError(w, http.StatusUnauthorized, "Código incorrecto")
		return
	}
	s.throttle.Reset(key)
	var disabled bool
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT disabled FROM users WHERE id = ?`, ch.userID).Scan(&disabled); err != nil || disabled {
		e.Outcome = audit.OutcomeDenied
		s.record(r.Context(), e)
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	s.mfaPending.done(in.Token)
	if fromCookie {
		s.clearShortCookie(w, r, mfaPendingCookie, "/api/auth/mfa")
	}
	s.record(r.Context(), e)
	s.startSession(w, r, ch.userID, ch.username, ch.role, true)
}
