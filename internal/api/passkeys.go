package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gaboiv/dbyson/internal/audit"
	"github.com/gaboiv/dbyson/internal/auth"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// NewPasskeys requires one explicit origin, never an untrusted Host/Forwarded header.
func NewPasskeys(rpID, origin string) (*webauthn.WebAuthn, error) {
	if rpID == "" && origin == "" {
		return nil, nil
	}
	u, err := url.Parse(origin)
	if err != nil || rpID == "" || u.Hostname() != rpID || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && rpID == "localhost")) {
		return nil, errors.New("passkeys require an explicit HTTPS origin and matching RP ID (HTTP only for localhost)")
	}
	return webauthn.New(&webauthn.Config{RPID: rpID, RPDisplayName: "DByson", RPOrigins: []string{origin}, AuthenticatorSelection: protocol.AuthenticatorSelection{ResidentKey: protocol.ResidentKeyRequirementRequired, UserVerification: protocol.VerificationRequired}})
}

type passkeyUser struct {
	id          int64
	name, role  string
	handle      []byte
	credentials []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte                         { return u.handle }
func (u *passkeyUser) WebAuthnName() string                       { return u.name }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.name }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

type passkeyCeremony struct {
	data                         webauthn.SessionData
	userID                       int64
	session, cert, purpose, name string
	expires                      time.Time
}
type passkeyCeremonies struct {
	sync.Mutex
	items map[string]passkeyCeremony
}

const passkeyCookie = "dbyson-passkey"

func (s *Server) passkeyUser(ctx context.Context, id int64) (*passkeyUser, error) {
	u := &passkeyUser{id: id, credentials: []webauthn.Credential{}}
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT username,role FROM users WHERE id=?`, id).Scan(&u.name, &u.role); err != nil {
		return nil, err
	}
	// A random, persistent user handle survives future master-key rotation.
	handle := make([]byte, 32)
	if _, err := rand.Read(handle); err != nil {
		return nil, err
	}
	if _, err := s.Store.DB.ExecContext(ctx, `INSERT INTO passkey_handles(user_id,handle) VALUES(?,?) ON CONFLICT(user_id) DO NOTHING`, id, handle); err != nil {
		return nil, err
	}
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT handle FROM passkey_handles WHERE user_id=?`, id).Scan(&u.handle); err != nil {
		return nil, err
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT credential FROM passkeys WHERE user_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var c webauthn.Credential
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		u.credentials = append(u.credentials, c)
	}
	return u, rows.Err()
}
func (s *Server) passkeysAvailable(w http.ResponseWriter) bool {
	if s.WebAuthn == nil {
		writeError(w, 503, "passkeys are not configured on this server")
		return false
	}
	return true
}

func (s *Server) passkeyPassword(w http.ResponseWriter, r *http.Request, password string) bool {
	sess := sessionFrom(r)
	var hash string
	var enabled bool
	key := "passkey-manage|" + clientIP(r) + "|" + strconv.FormatInt(sess.UserID, 10)
	if blocked, wait := s.throttle.Blocked(key); blocked {
		tooManyAttempts(w, wait)
		return false
	}
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT password_hash,totp_enabled FROM users WHERE id=?`, sess.UserID).Scan(&hash, &enabled); err != nil {
		writeError(w, 401, "authentication required")
		return false
	}
	ok := false
	if len(password) <= auth.MaxPasswordLen {
		ok, _ = auth.VerifyPassword(password, hash)
	}
	if !ok {
		s.throttle.Fail(key)
		writeError(w, 403, "current password required")
		return false
	}
	if enabled && !sess.MFA {
		writeMFARequired(w)
		return false
	}
	return true
}

func (s *Server) storeCeremony(w http.ResponseWriter, r *http.Request, c passkeyCeremony) bool {
	s.passkeyPending.Lock()
	defer s.passkeyPending.Unlock()
	if s.passkeyPending.items == nil {
		s.passkeyPending.items = map[string]passkeyCeremony{}
	}
	for id, item := range s.passkeyPending.items {
		if time.Now().After(item.expires) {
			delete(s.passkeyPending.items, id)
		}
	}
	if len(s.passkeyPending.items) >= 256 {
		writeError(w, 429, "too many pending ceremonies")
		return false
	}
	token := newID()
	c.expires = time.Now().Add(5 * time.Minute)
	c.cert = clientCertificate(r)
	s.passkeyPending.items[token] = c
	http.SetCookie(w, &http.Cookie{Name: passkeyCookie, Value: token, Path: "/api/auth/passkeys", MaxAge: 300, HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteStrictMode})
	return true
}
func (s *Server) takeCeremony(w http.ResponseWriter, r *http.Request, purpose string) (passkeyCeremony, bool) {
	cookie, err := r.Cookie(passkeyCookie)
	if err != nil {
		writeError(w, 400, "passkey challenge missing or expired")
		return passkeyCeremony{}, false
	}
	s.passkeyPending.Lock()
	c, ok := s.passkeyPending.items[cookie.Value]
	delete(s.passkeyPending.items, cookie.Value)
	s.passkeyPending.Unlock()
	http.SetCookie(w, &http.Cookie{Name: passkeyCookie, Value: "", Path: "/api/auth/passkeys", MaxAge: -1, HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteStrictMode})
	if !ok || c.purpose != purpose || time.Now().After(c.expires) || c.cert != clientCertificate(r) || (purpose == "register" && c.session != sessionFrom(r).ID) {
		writeError(w, 400, "invalid or expired passkey challenge")
		return c, false
	}
	return c, true
}

func (s *Server) listPasskeys(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT id,name,created_at FROM passkeys WHERE user_id=? ORDER BY created_at DESC`, sessionFrom(r).UserID)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer rows.Close()
	type entry struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		CreatedAt int64  `json:"createdAt"`
	}
	list := []entry{}
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.ID, &e.Name, &e.CreatedAt); err != nil {
			s.fail(w, err)
			return
		}
		list = append(list, e)
	}
	if err = rows.Err(); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": s.WebAuthn != nil, "credentials": list})
}
func (s *Server) beginPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysAvailable(w) {
		return
	}
	var in struct {
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !s.passkeyPassword(w, r, in.Password) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) == 0 || len(in.Name) > 100 {
		writeError(w, 400, "passkey name must be 1-100 bytes")
		return
	}
	sess := sessionFrom(r)
	user, err := s.passkeyUser(r.Context(), sess.UserID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(user.credentials) >= 20 {
		writeError(w, 409, "maximum 20 passkeys")
		return
	}
	options, data, err := s.WebAuthn.BeginRegistration(user, webauthn.WithRegistrationOrigin(s.WebAuthn.Config.RPOrigins[0]))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !s.storeCeremony(w, r, passkeyCeremony{data: *data, userID: user.id, session: sess.ID, purpose: "register", name: in.Name}) {
		return
	}
	writeJSON(w, 200, options)
}
func (s *Server) finishPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysAvailable(w) {
		return
	}
	c, ok := s.takeCeremony(w, r, "register")
	if !ok {
		return
	}
	user, err := s.passkeyUser(r.Context(), c.userID)
	if err != nil {
		s.fail(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	credential, err := s.WebAuthn.FinishRegistration(user, c.data, r)
	if err != nil {
		writeError(w, 400, "passkey verification failed")
		return
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		s.fail(w, err)
		return
	}
	result, err := s.Store.DB.ExecContext(r.Context(), `INSERT INTO passkeys(id,user_id,name,credential,created_at) SELECT ?,?,?,?,? WHERE (SELECT COUNT(*) FROM passkeys WHERE user_id=?)<20`, base64.RawURLEncoding.EncodeToString(credential.ID), user.id, c.name, raw, time.Now().Unix(), user.id)
	if err != nil {
		writeError(w, 409, "credential already registered or could not be saved")
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		writeError(w, 409, "maximum 20 passkeys")
		return
	}
	writeJSON(w, 201, map[string]bool{"ok": true})
}
func (s *Server) deletePasskey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
		ID       string `json:"id"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !s.passkeyPassword(w, r, in.Password) {
		return
	}
	result, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM passkeys WHERE id=? AND user_id=?`, in.ID, sessionFrom(r).UserID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		writeError(w, 404, "not found")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) beginPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysAvailable(w) {
		return
	}
	key := "passkey|" + clientIP(r)
	if blocked, wait := s.ipThrottle.Blocked(key); blocked {
		tooManyAttempts(w, wait)
		return
	}
	s.ipThrottle.Fail(key)
	options, data, err := s.WebAuthn.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired), webauthn.WithLoginOrigin(s.WebAuthn.Config.RPOrigins[0]))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !s.storeCeremony(w, r, passkeyCeremony{data: *data, purpose: "login"}) {
		return
	}
	writeJSON(w, 200, options)
}
func (s *Server) finishPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysAvailable(w) {
		return
	}
	c, ok := s.takeCeremony(w, r, "login")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var stored []byte
	user, credential, err := s.WebAuthn.FinishPasskeyLogin(func(rawID, handle []byte) (webauthn.User, error) {
		var uid int64
		if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT user_id,credential FROM passkeys WHERE id=?`, base64.RawURLEncoding.EncodeToString(rawID)).Scan(&uid, &stored); err != nil {
			return nil, errors.New("unknown credential")
		}
		u, err := s.passkeyUser(r.Context(), uid)
		if err != nil {
			return nil, err
		}
		if subtle.ConstantTimeCompare(handle, u.handle) != 1 {
			return nil, errors.New("invalid handle")
		}
		return u, nil
	}, c.data, r)
	if err != nil || credential == nil || credential.Authenticator.CloneWarning {
		writeError(w, 401, "passkey authentication failed")
		s.record(r.Context(), audit.Entry{Action: "auth.passkey.login", IP: clientIP(r), Outcome: audit.OutcomeDenied})
		return
	}
	u := user.(*passkeyUser)
	raw, _ := json.Marshal(credential)
	// Compare-and-swap prevents a concurrent login overwriting a newer signature counter.
	result, err := s.Store.DB.ExecContext(r.Context(), `UPDATE passkeys SET credential=? WHERE id=? AND user_id=? AND credential=?`, raw, base64.RawURLEncoding.EncodeToString(credential.ID), u.id, stored)
	if err != nil {
		s.fail(w, err)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		writeError(w, 409, "credential changed; retry authentication")
		return
	}
	s.record(r.Context(), audit.Entry{Action: "auth.passkey.login", UserID: u.id, Username: u.name, IP: clientIP(r), Outcome: audit.OutcomeOK})
	s.startSession(w, r, u.id, u.name, u.role, true)
}
