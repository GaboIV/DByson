package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gaboiv/dbyson/internal/auth"
)

// ssoClient signs the admin in as an SSO session would, without a password.
func ssoClient(t *testing.T, e *testEnv) (*client, string) {
	t.Helper()
	token, csrf, err := e.srv.sessions.CreateWithDetails(context.Background(), 1, false, auth.SessionDetails{AuthMethod: auth.AuthSSO})
	if err != nil {
		t.Fatal(err)
	}
	return &client{env: e, cookie: &http.Cookie{Name: cookieName, Value: token}, csrf: csrf}, token
}

func TestSSOSessionEnrollsMFAWithoutPassword(t *testing.T) {
	e := newTestEnv(t)
	e.setupAdmin(e.client())
	c, _ := ssoClient(t, e)

	var status struct {
		AuthMethod     string
		SSOReauthFresh bool
	}
	json.Unmarshal(c.do("GET", "/api/auth/mfa", nil).Body.Bytes(), &status)
	if status.AuthMethod != auth.AuthSSO || !status.SSOReauthFresh {
		t.Fatalf("fresh SSO session must be reported: %+v", status)
	}
	rec := c.do("POST", "/api/auth/mfa/setup", map[string]string{})
	if rec.Code != http.StatusOK {
		t.Fatalf("fresh SSO session must start 2FA setup without a password: %d %s", rec.Code, rec.Body.String())
	}
	var setup struct{ Secret string }
	json.Unmarshal(rec.Body.Bytes(), &setup)
	code, _ := auth.TOTPCode(setup.Secret, time.Now())
	if rec := c.do("POST", "/api/auth/mfa/enable", map[string]string{"code": code}); rec.Code != http.StatusOK {
		t.Fatalf("enable failed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestStaleSSOSessionMustReauthenticate(t *testing.T) {
	e := newTestEnv(t)
	e.setupAdmin(e.client())
	c, token := ssoClient(t, e)
	old := time.Now().Add(-ssoReauthWindow - time.Minute).Unix()
	if _, err := e.store.DB.Exec(`UPDATE sessions SET created_at = ? WHERE id_hash = ?`, old, hex.EncodeToString(sum256(token))); err != nil {
		t.Fatal(err)
	}
	rec := c.do("POST", "/api/auth/mfa/setup", map[string]string{})
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusUnauthorized || body["code"] != reauthRequiredCode {
		t.Fatalf("stale SSO session must be asked to sign in again: %d %s", rec.Code, rec.Body.String())
	}
	// A password session is never let through without its password.
	c2 := e.client()
	c2.loginFrom(c2.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": adminPassword}))
	if rec := c2.do("POST", "/api/auth/mfa/setup", map[string]string{}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("password session without password must be 401, got %d", rec.Code)
	}
}

func sum256(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}
