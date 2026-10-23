package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gaboiv/dbyson/internal/audit"
	"github.com/gaboiv/dbyson/internal/auth"
)

const adminPassword = "a-long-enough-password"

// enrollMFA turns on 2FA for the logged-in client and returns the secret and recovery codes.
func enrollMFA(t *testing.T, c *client) (string, []string) {
	t.Helper()
	if rec := c.do("POST", "/api/auth/mfa/setup", map[string]string{"password": "wrong-password-1"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("setup with wrong password must be 401, got %d", rec.Code)
	}
	rec := c.do("POST", "/api/auth/mfa/setup", map[string]string{"password": adminPassword})
	var setup struct{ Secret, URI, QR string }
	json.Unmarshal(rec.Body.Bytes(), &setup)
	if rec.Code != http.StatusOK || setup.Secret == "" || !strings.HasPrefix(setup.QR, "data:image/png;base64,") ||
		!strings.HasPrefix(setup.URI, "otpauth://totp/") {
		t.Fatalf("setup failed: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.do("POST", "/api/auth/mfa/enable", map[string]string{"code": "000000"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong code must be rejected, got %d", rec.Code)
	}
	code, _ := auth.TOTPCode(setup.Secret, time.Now())
	rec = c.do("POST", "/api/auth/mfa/enable", map[string]string{"code": code})
	var enabled struct{ RecoveryCodes []string }
	json.Unmarshal(rec.Body.Bytes(), &enabled)
	if rec.Code != http.StatusOK || len(enabled.RecoveryCodes) != auth.RecoveryCodeCount {
		t.Fatalf("enable failed: %d %s", rec.Code, rec.Body.String())
	}
	return setup.Secret, enabled.RecoveryCodes
}

func login(t *testing.T, e *testEnv) (*client, map[string]any) {
	t.Helper()
	c := e.client()
	rec := c.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": adminPassword})
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	}
	return c, body
}

func TestMFAEnrollmentAndTwoStepLogin(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	secret, recovery := enrollMFA(t, c)

	var me sessionResponse
	json.Unmarshal(c.do("GET", "/api/auth/me", nil).Body.Bytes(), &me)
	if !me.MFA || !me.MFAEnabled {
		t.Fatalf("enrolling session must be marked verified: %+v", me)
	}
	// Each 2FA step is audited exactly once, under its readable name.
	for _, en := range auditEntries(t, c) {
		if strings.Contains(en.Action, "/api/") {
			t.Fatalf("generic duplicate audit entry: %+v", en)
		}
	}
	if entries := auditEntries(t, c); find(entries, "auth.mfa.enable", audit.OutcomeDenied) == nil || find(entries, "auth.mfa.enable", audit.OutcomeOK) == nil {
		t.Fatalf("enable attempts not audited: %+v", entries)
	}

	// Password alone no longer yields a session.
	c2, body := login(t, e)
	if body["mfaRequired"] != true || c2.cookie != nil {
		t.Fatalf("expected second-factor challenge without cookie: %v", body)
	}
	token := body["mfaToken"].(string)

	// The code used to enable 2FA is already consumed (replay protection).
	used, _ := auth.TOTPCode(secret, time.Now())
	if rec := c2.do("POST", "/api/auth/mfa/verify", map[string]string{"mfaToken": token, "code": used}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("replayed TOTP must be rejected, got %d", rec.Code)
	}
	// A recovery code works once.
	rec := c2.do("POST", "/api/auth/mfa/verify", map[string]string{"mfaToken": token, "code": recovery[0]})
	if rec.Code != http.StatusOK || c2.cookie == nil {
		t.Fatalf("recovery code rejected: %d %s", rec.Code, rec.Body.String())
	}
	c2.loginFrom(rec)
	if !strings.Contains(rec.Body.String(), `"mfa":true`) {
		t.Fatalf("session must be MFA-verified: %s", rec.Body.String())
	}
	// The challenge token is single-use.
	if rec := e.client().do("POST", "/api/auth/mfa/verify", map[string]string{"mfaToken": token, "code": recovery[1]}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("challenge token reuse must fail, got %d", rec.Code)
	}
	c3, body := login(t, e)
	if rec := c3.do("POST", "/api/auth/mfa/verify", map[string]string{"mfaToken": body["mfaToken"].(string), "code": recovery[0]}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("used recovery code must fail, got %d", rec.Code)
	}

	var status map[string]any
	json.Unmarshal(c2.do("GET", "/api/auth/mfa", nil).Body.Bytes(), &status)
	if status["enabled"] != true || status["recoveryCodesLeft"].(float64) != float64(auth.RecoveryCodeCount-1) {
		t.Fatalf("unexpected status: %v", status)
	}

	// The secret is stored encrypted, never in clear.
	var sealed []byte
	e.store.DB.QueryRow(`SELECT totp_secret FROM users WHERE username='admin'`).Scan(&sealed)
	if strings.Contains(string(sealed), secret) {
		t.Fatal("TOTP secret stored in plaintext")
	}
}

func TestMFAChallengeAttemptsAreLimited(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	_, recovery := enrollMFA(t, c)
	c2, body := login(t, e)
	token := body["mfaToken"].(string)
	for i := 0; i < mfaMaxAttempts; i++ {
		c2.do("POST", "/api/auth/mfa/verify", map[string]string{"mfaToken": token, "code": "111111"})
	}
	if rec := c2.do("POST", "/api/auth/mfa/verify", map[string]string{"mfaToken": token, "code": recovery[0]}); rec.Code == http.StatusOK {
		t.Fatal("challenge must be burned after too many attempts")
	}
}

func TestMFADisableRevokesOtherSessions(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	_, recovery := enrollMFA(t, c)
	other, body := login(t, e)
	other.loginFrom(other.do("POST", "/api/auth/mfa/verify", map[string]string{"mfaToken": body["mfaToken"].(string), "code": recovery[0]}))

	if rec := c.do("POST", "/api/auth/mfa/disable", map[string]string{"password": adminPassword, "code": "123456"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disable with bad code must fail, got %d", rec.Code)
	}
	if rec := c.do("POST", "/api/auth/mfa/disable", map[string]string{"password": adminPassword, "code": recovery[1]}); rec.Code != http.StatusOK {
		t.Fatalf("disable failed: %d %s", rec.Code, rec.Body.String())
	}
	if rec := other.do("GET", "/api/auth/me", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("other sessions must be revoked, got %d", rec.Code)
	}
	if _, body := login(t, e); body["mfaRequired"] == true {
		t.Fatal("login must not ask for 2FA after disabling")
	}
}

func TestSensitivePolicyRequiresMFAForProd(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	conn := func(env string) string {
		rec := c.do("POST", "/api/connections", map[string]any{
			"name": env, "env": env, "host": "127.0.0.1", "port": 1, "username": "u", "tlsMode": "disabled",
		})
		var v connView
		json.Unmarshal(rec.Body.Bytes(), &v)
		return v.ID
	}
	prod, dev := conn("prod"), conn("dev")

	for _, path := range []string{
		"/api/connections/" + prod + "/test",
		"/api/connections/" + prod + "/query",
	} {
		rec := c.do("POST", path, map[string]any{"query": "SELECT 1"})
		if path == "/api/connections/"+prod+"/test" {
			rec = c.do("POST", path, nil)
		}
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), mfaRequiredCode) {
			t.Fatalf("%s without 2FA must be refused: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := c.do("POST", "/api/connections/test", map[string]any{"name": "d", "env": "prod", "host": "127.0.0.1", "port": 1, "username": "u", "tlsMode": "disabled"}); rec.Code != http.StatusForbidden {
		t.Fatalf("draft prod test without 2FA must be refused, got %d", rec.Code)
	}
	// Non-sensitive connections are unaffected (the connect itself fails: nothing listens on :1).
	if rec := c.do("POST", "/api/connections/"+dev+"/test", nil); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), mfaRequiredCode) {
		t.Fatalf("dev connection must not require 2FA: %d %s", rec.Code, rec.Body.String())
	}

	// After enrolling, this session is verified and prod opens up.
	enrollMFA(t, c)
	if rec := c.do("POST", "/api/connections/"+prod+"/test", nil); strings.Contains(rec.Body.String(), mfaRequiredCode) {
		t.Fatalf("verified session must pass the gate: %s", rec.Body.String())
	}
}

func TestPolicyAllGatesEverything(t *testing.T) {
	e := newTestEnvWith(t, func(d *Deps) { d.MFAPolicy = MFAPolicyAll })
	c := e.client()
	e.setupAdmin(c)
	if rec := c.do("GET", "/api/connections", nil); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), mfaRequiredCode) {
		t.Fatalf("policy all must gate the API: %d", rec.Code)
	}
	if rec := c.do("GET", "/api/auth/mfa", nil); rec.Code != http.StatusOK {
		t.Fatalf("2FA management must stay reachable: %d", rec.Code)
	}
	enrollMFA(t, c)
	if rec := c.do("GET", "/api/connections", nil); rec.Code != http.StatusOK {
		t.Fatalf("verified session must pass: %d", rec.Code)
	}
}

func auditEntries(t *testing.T, c *client) []audit.Entry {
	t.Helper()
	var out struct{ Entries []audit.Entry }
	rec := c.do("GET", "/api/audit?limit=200", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit list: %d", rec.Code)
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Entries
}

func find(entries []audit.Entry, action, outcome string) *audit.Entry {
	for i := range entries {
		if entries[i].Action == action && (outcome == "" || entries[i].Outcome == outcome) {
			return &entries[i]
		}
	}
	return nil
}

func TestAuditTrail(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	e.client().do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "wrong-password-1"})
	create := c.do("POST", "/api/connections", map[string]any{
		"name": "dev", "env": "dev", "host": "127.0.0.1", "port": 1, "username": "u", "password": "TopSecretPw", "tlsMode": "disabled",
	})
	var cv connView
	json.Unmarshal(create.Body.Bytes(), &cv)
	c.do("POST", "/api/connections/"+cv.ID+"/query", map[string]any{"query": "CREATE USER x IDENTIFIED BY 'S3cretPw'"})
	c.do("POST", "/api/connections/"+cv.ID+"/query", map[string]any{"query": "SELECT 1"}) // nothing listens on :1
	good := c.csrf
	c.csrf = "forged"
	c.do("DELETE", "/api/connections/"+cv.ID, nil)
	c.csrf = good
	c.do("GET", "/api/connections", nil) // plain reads are not audited

	entries := auditEntries(t, c)
	for _, want := range []struct{ action, outcome string }{
		{"auth.setup", audit.OutcomeOK},
		{"auth.login", audit.OutcomeDenied},
		{"connection.create", audit.OutcomeOK},
		{"query.execute", audit.OutcomeError},
		{"query.execute", audit.OutcomeConfirmationRequested},
		{"connection.delete", audit.OutcomeDenied},
	} {
		if find(entries, want.action, want.outcome) == nil {
			t.Errorf("missing audit entry %s/%s in %+v", want.action, want.outcome, entries)
		}
	}
	if cr := find(entries, "connection.create", ""); cr == nil || cr.ConnectionID != cv.ID {
		t.Errorf("create entry must carry the new connection id: %+v", cr)
	}
	q := find(entries, "query.execute", audit.OutcomeConfirmationRequested)
	if q == nil || q.ConnectionID != cv.ID || strings.Contains(q.Detail, "S3cretPw") || !strings.Contains(q.Detail, "IDENTIFIED BY '***'") {
		t.Errorf("query entry wrong or unredacted: %+v", q)
	}
	for _, en := range entries {
		if strings.Contains(en.Detail, "TopSecretPw") {
			t.Fatal("connection password leaked into the audit log")
		}
		if strings.HasPrefix(en.Action, "GET ") {
			t.Fatalf("plain read was audited: %+v", en)
		}
	}
	var v audit.Verification
	json.Unmarshal(c.do("GET", "/api/audit/verify", nil).Body.Bytes(), &v)
	if !v.OK || v.Checked != len(entries) {
		t.Fatalf("chain must verify: %+v (entries %d)", v, len(entries))
	}
}

func TestAuditListIsScopedForNonAdmins(t *testing.T) {
	e := newTestEnv(t)
	admin := e.client()
	e.setupAdmin(admin)
	hash, _ := auth.HashPassword("viewer-password-123")
	e.store.DB.Exec(`INSERT INTO users (username, password_hash, role, created_at) VALUES ('vera', ?, 'viewer', 0)`, hash)
	vera := e.client()
	vera.loginFrom(vera.do("POST", "/api/auth/login", map[string]string{"username": "vera", "password": "viewer-password-123"}))

	for _, en := range auditEntries(t, vera) {
		if en.Username != "vera" {
			t.Fatalf("non-admin saw another user's entry: %+v", en)
		}
	}
	if rec := vera.do("GET", "/api/audit/verify", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("verify must be admin-only, got %d", rec.Code)
	}
}
