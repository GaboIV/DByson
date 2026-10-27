package api

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gaboiv/dbyson/internal/auth"
	"github.com/gaboiv/dbyson/internal/policy"
	"github.com/gaboiv/dbyson/internal/store"
	"github.com/gaboiv/dbyson/internal/vault"
)

type testEnv struct {
	t       *testing.T
	handler http.Handler
	store   *store.Store
	srv     *Server
}

func newTestEnv(t *testing.T) *testEnv { return newTestEnvWith(t, nil) }

func newTestEnvWith(t *testing.T, configure func(*Deps)) *testEnv {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	key := make([]byte, 32)
	rand.Read(key)
	v, _ := vault.New(key)
	pol, _ := policy.ParseAllowlist("")
	assets := fstest.MapFS{"index.html": {Data: []byte("<html>spa</html>")}}
	deps := Deps{Store: st, Vault: v, Policy: pol}
	if configure != nil {
		configure(&deps)
	}
	srv := newServer("test", deps)
	return &testEnv{t: t, store: st, srv: srv, handler: srv.routes(assets)}
}

type client struct {
	env    *testEnv
	cookie *http.Cookie
	csrf   string
	// other short-lived cookies (SSO state, pending 2FA), kept like a browser would
	jar map[string]*http.Cookie
}

func (e *testEnv) client() *client { return &client{env: e} }

func (c *client) do(method, path string, body any) *httptest.ResponseRecorder {
	c.env.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	rec := httptest.NewRecorder()
	c.env.handler.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		switch {
		case ck.Name == cookieName && ck.MaxAge >= 0:
			c.cookie = ck
		case ck.Name != cookieName && ck.MaxAge < 0:
			delete(c.jar, ck.Name)
		case ck.Name != cookieName:
			if c.jar == nil {
				c.jar = map[string]*http.Cookie{}
			}
			c.jar[ck.Name] = ck
		}
	}
	return rec
}

func (c *client) loginFrom(rec *httptest.ResponseRecorder) {
	var s sessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	c.csrf = s.CSRF
}

func (e *testEnv) setupAdmin(c *client) {
	e.t.Helper()
	rec := c.do("POST", "/api/setup", map[string]string{"username": "admin", "password": "a-long-enough-password"})
	if rec.Code != http.StatusOK {
		e.t.Fatalf("setup failed: %d %s", rec.Code, rec.Body.String())
	}
	c.loginFrom(rec)
}

func TestHealthz(t *testing.T) {
	rec := newTestEnv(t).client().do("GET", "/api/healthz", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("unexpected response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUnknownAPIRouteIs404JSON(t *testing.T) {
	rec := newTestEnv(t).client().do("GET", "/api/nope", nil)
	if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected response: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestSPAFallback(t *testing.T) {
	rec := newTestEnv(t).client().do("GET", "/connections/42", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "spa") {
		t.Fatalf("expected index.html fallback, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := newTestEnv(t).client().do("GET", "/api/healthz", nil)
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("missing header %s", h)
		}
	}
}

func TestSetupOnlyOnce(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	if rec := c.do("GET", "/api/status", nil); !strings.Contains(rec.Body.String(), `"setupRequired":true`) {
		t.Fatalf("expected setup required: %s", rec.Body.String())
	}
	e.setupAdmin(c)
	rec := e.client().do("POST", "/api/setup", map[string]string{"username": "evil", "password": "another-long-password"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("second setup must be rejected, got %d", rec.Code)
	}
	var hash string
	if err := e.store.DB.QueryRow(`SELECT password_hash FROM users WHERE username='admin'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") || strings.Contains(hash, "a-long-enough-password") {
		t.Fatalf("password not stored as argon2id hash: %s", hash)
	}
}

func TestSetupValidation(t *testing.T) {
	c := newTestEnv(t).client()
	if rec := c.do("POST", "/api/setup", map[string]string{"username": "admin", "password": "short"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("short password must be rejected, got %d", rec.Code)
	}
	if rec := c.do("POST", "/api/setup", map[string]string{"username": "a b", "password": "a-long-enough-password"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad username must be rejected, got %d", rec.Code)
	}
}

func TestLoginFlow(t *testing.T) {
	e := newTestEnv(t)
	e.setupAdmin(e.client())

	if rec := e.client().do("GET", "/api/connections", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request must get 401, got %d", rec.Code)
	}
	bad := e.client().do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "wrong-password-123"})
	unknown := e.client().do("POST", "/api/auth/login", map[string]string{"username": "ghost", "password": "wrong-password-123"})
	if bad.Code != http.StatusUnauthorized || bad.Body.String() != unknown.Body.String() {
		t.Fatalf("bad password and unknown user must be indistinguishable: %d %q vs %q", bad.Code, bad.Body, unknown.Body)
	}

	c := e.client()
	rec := c.do("POST", "/api/auth/login", map[string]string{"username": "ADMIN", "password": "a-long-enough-password"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	}
	c.loginFrom(rec)
	ck := c.cookie
	if !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie must be HttpOnly + SameSite=Strict: %+v", ck)
	}
	if rec := c.do("GET", "/api/auth/me", nil); rec.Code != http.StatusOK {
		t.Fatalf("me failed: %d", rec.Code)
	}
	if rec := c.do("POST", "/api/auth/logout", nil); rec.Code != http.StatusOK {
		t.Fatalf("logout failed: %d", rec.Code)
	}
	c.cookie = ck // replay the old cookie
	if rec := c.do("GET", "/api/auth/me", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session must be invalid after logout, got %d", rec.Code)
	}
}

func TestLoginThrottle(t *testing.T) {
	e := newTestEnv(t)
	e.setupAdmin(e.client())
	c := e.client()
	for i := 0; i < 5; i++ {
		c.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "wrong-password-123"})
	}
	// Even the right password is refused while locked out.
	rec := c.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "a-long-enough-password"})
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("expected 429 with Retry-After, got %d", rec.Code)
	}
}

func TestCSRFAndOriginProtection(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	body := map[string]any{"name": "x", "host": "db.local", "username": "u"}

	good := c.csrf
	c.csrf = ""
	if rec := c.do("POST", "/api/connections", body); rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF token must be 403, got %d", rec.Code)
	}
	c.csrf = "wrong"
	if rec := c.do("POST", "/api/connections", body); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong CSRF token must be 403, got %d", rec.Code)
	}
	c.csrf = good

	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/connections", bytes.NewReader(b))
	req.AddCookie(c.cookie)
	req.Header.Set("X-CSRF-Token", good)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin request must be 403, got %d", rec.Code)
	}
}

func TestConnectionLifecycleAndSecrecy(t *testing.T) {
	// This test exercises the create/update/delete lifecycle and secret handling,
	// not the MFA policy: run it with the policy off so editing a prod profile is
	// not gated. The gate itself is covered by TestUpdateProdConnectionRequiresMFA.
	e := newTestEnvWith(t, func(d *Deps) { d.MFAPolicy = MFAPolicyOff })
	c := e.client()
	e.setupAdmin(c)

	create := c.do("POST", "/api/connections", map[string]any{
		"name": "Prod DB", "env": "prod", "host": "db.example.com", "port": 3306,
		"username": "app", "password": "SuperSecretPw!", "tlsMode": "verify_identity",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create failed: %d %s", create.Code, create.Body.String())
	}
	if strings.Contains(create.Body.String(), "SuperSecretPw!") {
		t.Fatal("password leaked in create response")
	}
	var created connView
	_ = json.Unmarshal(create.Body.Bytes(), &created)
	if !created.HasPassword || created.TLSMode != "verify_identity" {
		t.Fatalf("unexpected view: %+v", created)
	}

	// Stored encrypted: the password must not appear anywhere in the raw row.
	var wrapped, sealed []byte
	if err := e.store.DB.QueryRow(`SELECT wrapped_dek, secret FROM connections WHERE id=?`, created.ID).Scan(&wrapped, &sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("SuperSecretPw!")) || bytes.Contains(wrapped, []byte("SuperSecretPw!")) {
		t.Fatal("password stored in plaintext")
	}

	list := c.do("GET", "/api/connections", nil)
	if strings.Contains(list.Body.String(), "SuperSecretPw!") || !strings.Contains(list.Body.String(), "Prod DB") {
		t.Fatalf("bad list response: %s", list.Body.String())
	}

	// Update without a password keeps the stored one.
	upd := c.do("PUT", "/api/connections/"+created.ID, map[string]any{
		"name": "Prod DB 2", "env": "prod", "host": "db.example.com", "username": "app", "tlsMode": "verify_ca",
	})
	if upd.Code != http.StatusOK || !strings.Contains(upd.Body.String(), `"hasPassword":true`) {
		t.Fatalf("update failed or lost password: %d %s", upd.Code, upd.Body.String())
	}
	// Explicit empty string clears it.
	clr := c.do("PUT", "/api/connections/"+created.ID, map[string]any{
		"name": "Prod DB 2", "env": "prod", "host": "db.example.com", "username": "app", "password": "",
	})
	if !strings.Contains(clr.Body.String(), `"hasPassword":false`) {
		t.Fatalf("password not cleared: %s", clr.Body.String())
	}

	if rec := c.do("DELETE", "/api/connections/"+created.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete failed: %d", rec.Code)
	}
	if rec := c.do("GET", "/api/connections/"+created.ID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", rec.Code)
	}
}

// Under the sensitive policy, editing or testing a production profile is as
// sensitive as using it: a session with no second factor must be refused, and
// the env in the request body cannot move a prod profile out of the policy.
func TestUpdateProdConnectionRequiresMFA(t *testing.T) {
	e := newTestEnvWith(t, func(d *Deps) { d.MFAPolicy = MFAPolicySensitive })
	c := e.client()
	e.setupAdmin(c)

	create := c.do("POST", "/api/connections", map[string]any{
		"name": "Prod DB", "env": "prod", "host": "db.example.com", "port": 3306,
		"username": "app", "password": "SuperSecretPw!", "tlsMode": "verify_identity",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create failed: %d %s", create.Code, create.Body.String())
	}
	var created connView
	_ = json.Unmarshal(create.Body.Bytes(), &created)

	// Editing the prod profile without a second factor is refused.
	upd := c.do("PUT", "/api/connections/"+created.ID, map[string]any{
		"name": "Prod DB 2", "env": "prod", "host": "db.example.com", "username": "app", "tlsMode": "verify_ca",
	})
	if upd.Code != http.StatusForbidden || !strings.Contains(upd.Body.String(), mfaRequiredCode) {
		t.Fatalf("editing prod without MFA must be gated: %d %s", upd.Code, upd.Body.String())
	}

	// Relabelling the env to dev in the body cannot sidestep the gate: the policy
	// follows the stored prod destination, both for the update and for test-draft.
	relabel := c.do("PUT", "/api/connections/"+created.ID, map[string]any{
		"name": "Prod DB", "env": "dev", "host": "db.example.com", "username": "app", "tlsMode": "verify_identity",
	})
	if relabel.Code != http.StatusForbidden || !strings.Contains(relabel.Body.String(), mfaRequiredCode) {
		t.Fatalf("relabelling prod to dev must still be gated: %d %s", relabel.Code, relabel.Body.String())
	}
	draft := c.do("POST", "/api/connections/"+created.ID+"/test-draft", map[string]any{
		"name": "Prod DB", "env": "dev", "host": "db.example.com", "username": "app", "tlsMode": "verify_identity",
	})
	if draft.Code != http.StatusForbidden || !strings.Contains(draft.Body.String(), mfaRequiredCode) {
		t.Fatalf("test-draft of a prod profile with env=dev must be gated: %d %s", draft.Code, draft.Body.String())
	}

	// With a verified second factor the same edit succeeds.
	enrollMFA(t, c)
	ok := c.do("PUT", "/api/connections/"+created.ID, map[string]any{
		"name": "Prod DB 2", "env": "prod", "host": "db.example.com", "username": "app", "tlsMode": "verify_ca",
	})
	if ok.Code != http.StatusOK {
		t.Fatalf("verified session must be able to edit prod: %d %s", ok.Code, ok.Body.String())
	}
}

func TestConnectionValidation(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	cases := map[string]map[string]any{
		"missing host":   {"name": "x", "username": "u"},
		"bad port":       {"name": "x", "host": "h", "username": "u", "port": 70000},
		"bad tls mode":   {"name": "x", "host": "h", "username": "u", "tlsMode": "maybe"},
		"bad env":        {"name": "x", "host": "h", "username": "u", "env": "qa-ish"},
		"host with path": {"name": "x", "host": "h/evil", "username": "u"},
		"bad CA PEM":     {"name": "x", "host": "h", "username": "u", "caCert": "not a pem"},
		"unknown field":  {"name": "x", "host": "h", "username": "u", "isAdmin": true},
	}
	for name, body := range cases {
		if rec := c.do("POST", "/api/connections", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestConnectionsAreIsolatedPerUser(t *testing.T) {
	e := newTestEnv(t)
	owner := e.client()
	e.setupAdmin(owner)
	create := owner.do("POST", "/api/connections", map[string]any{"name": "mine", "host": "h", "username": "u"})
	var cv connView
	_ = json.Unmarshal(create.Body.Bytes(), &cv)

	hash, _ := auth.HashPassword("second-user-password")
	if _, err := e.store.DB.Exec(`INSERT INTO users (username, password_hash, role, created_at) VALUES ('bob', ?, 'member', 0)`, hash); err != nil {
		t.Fatal(err)
	}
	bob := e.client()
	rec := bob.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": "second-user-password"})
	bob.loginFrom(rec)

	if rec := bob.do("GET", "/api/connections/"+cv.ID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("other user's connection must be 404, got %d", rec.Code)
	}
	if rec := bob.do("DELETE", "/api/connections/"+cv.ID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("other user must not delete, got %d", rec.Code)
	}
	if rec := bob.do("POST", "/api/connections/"+cv.ID+"/test", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("other user must not test, got %d", rec.Code)
	}
	if rec := bob.do("GET", "/api/connections", nil); strings.Contains(rec.Body.String(), "mine") {
		t.Fatalf("list leaked another user's connection: %s", rec.Body.String())
	}
	// Central administration: admins see every profile.
	if _, err := e.store.DB.Exec(`UPDATE users SET role = 'admin' WHERE username = 'bob'`); err != nil {
		t.Fatal(err)
	}
	if rec := bob.do("GET", "/api/connections/"+cv.ID, nil); rec.Code != http.StatusOK {
		t.Fatalf("admin must see every connection, got %d", rec.Code)
	}
}

func TestTestEndpointBlocksMetadataHost(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	rec := c.do("POST", "/api/connections/test", map[string]any{
		"name": "ssrf", "host": "169.254.169.254", "username": "u", "tlsMode": "disabled",
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":false`) || !strings.Contains(rec.Body.String(), "not allowed by policy") {
		t.Fatalf("metadata host must be refused by policy: %d %s", rec.Code, rec.Body.String())
	}
}

func TestExplorerEndpointsRequireAuth(t *testing.T) {
	e := newTestEnv(t)
	anon := e.client()

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/connections/test-id/databases"},
		{"GET", "/api/connections/test-id/databases/testdb/tables"},
		{"GET", "/api/connections/test-id/databases/testdb/tables/users/schema"},
		{"GET", "/api/connections/test-id/databases/testdb/tables/users/data"},
		{"GET", "/api/connections/test-id/databases/testdb/tables/users/count"},
		{"POST", "/api/connections/test-id/databases/testdb/tables/users/row"},
		{"POST", "/api/connections/test-id/databases/testdb/tables/users/rows"},
		{"PUT", "/api/connections/test-id/databases/testdb/tables/users/rows"},
		{"DELETE", "/api/connections/test-id/databases/testdb/tables/users/rows"},
		{"POST", "/api/connections/test-id/query"},
		{"GET", "/api/connections/test-id/export?db=testdb"},
	}

	for _, rt := range routes {
		rec := anon.do(rt.method, rt.path, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s unauthenticated expected 401, got %d", rt.method, rt.path, rec.Code)
		}
	}
}

func TestExplorerEndpointsReturn404ForUnknownConnection(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)

	rec := c.do("GET", "/api/connections/non-existent/databases", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown connection, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestQueryDestructiveClassificationCheck(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)

	create := c.do("POST", "/api/connections", map[string]any{"name": "test-db", "host": "127.0.0.1", "username": "u"})
	var cv connView
	_ = json.Unmarshal(create.Body.Bytes(), &cv)

	// Send DROP TABLE without confirmDestructive
	rec := c.do("POST", "/api/connections/"+cv.ID+"/query", map[string]any{
		"query":              "DROP TABLE users",
		"confirmDestructive": false,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with confirmation json, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"needsConfirmation":true`) {
		t.Fatalf("expected needsConfirmation: true, got %s", rec.Body.String())
	}
}
