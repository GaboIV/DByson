package api

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gaboiv/dbyson/internal/audit"
	"github.com/gaboiv/dbyson/internal/sso"
	"github.com/gaboiv/dbyson/internal/tunnel"
)

func createUser(t *testing.T, admin *client, username, password, role string) {
	t.Helper()
	rec := admin.do("POST", "/api/users", map[string]string{"username": username, "password": password, "role": role})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user %s: %d %s", username, rec.Code, rec.Body.String())
	}
}

func loginAs(t *testing.T, e *testEnv, username, password string) *client {
	t.Helper()
	c := e.client()
	rec := c.do("POST", "/api/auth/login", map[string]string{"username": username, "password": password})
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", username, rec.Code, rec.Body.String())
	}
	c.loginFrom(rec)
	return c
}

func userID(t *testing.T, e *testEnv, admin *client, username string) int64 {
	t.Helper()
	rec := admin.do("GET", "/api/users", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list users: %d", rec.Code)
	}
	var users []userView
	if err := json.Unmarshal(rec.Body.Bytes(), &users); err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.Username == username {
			return u.ID
		}
	}
	t.Fatalf("user %s not found", username)
	return 0
}

func createConn(t *testing.T, c *client, name, env string, shared *bool) connView {
	t.Helper()
	body := map[string]any{"name": name, "env": env, "host": "localhost", "username": "dbuser", "tlsMode": "disabled"}
	if shared != nil {
		body["shared"] = *shared
	}
	rec := c.do("POST", "/api/connections", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create connection %s: %d %s", name, rec.Code, rec.Body.String())
	}
	var v connView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func boolPtr(b bool) *bool { return &b }

func TestTeamUserManagement(t *testing.T) {
	e := newTestEnv(t)
	admin := e.client()
	e.setupAdmin(admin)

	if rec := e.client().do("GET", "/api/users", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous user list: %d", rec.Code)
	}
	createUser(t, admin, "member1", "member-password-123", "member")
	createUser(t, admin, "viewer1", "viewer-password-123", "viewer")
	if rec := admin.do("POST", "/api/users", map[string]string{"username": "member1", "password": "member-password-123", "role": "member"}); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate username: %d", rec.Code)
	}
	if rec := admin.do("POST", "/api/users", map[string]string{"username": "bad", "password": "member-password-123", "role": "superuser"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad role: %d", rec.Code)
	}

	member := loginAs(t, e, "member1", "member-password-123")
	if rec := member.do("GET", "/api/users", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("member listed users: %d", rec.Code)
	}
	if rec := member.do("POST", "/api/users", map[string]string{"username": "x", "password": "member-password-123"}); rec.Code != http.StatusForbidden {
		t.Fatalf("member created a user: %d", rec.Code)
	}

	// Role change and password reset by the admin.
	vid := userID(t, e, admin, "viewer1")
	if rec := admin.do("PUT", "/api/users/1", map[string]string{"role": "owner"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad role update: %d", rec.Code)
	}
	if rec := admin.do("PUT", "/api/users/999999", map[string]string{"role": "member"}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown user update: %d", rec.Code)
	}
	rec := admin.do("PUT", "/api/users/"+itoa(vid), map[string]string{"role": "member", "password": "viewer-password-456"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update user: %d %s", rec.Code, rec.Body.String())
	}
	loginAs(t, e, "viewer1", "viewer-password-456")

	// Disabling locks out immediately, including existing sessions.
	viewer := loginAs(t, e, "viewer1", "viewer-password-456")
	mid := userID(t, e, admin, "member1")
	if rec := admin.do("PUT", "/api/users/"+itoa(mid), map[string]bool{"disabled": true}); rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body.String())
	}
	if rec := member.do("GET", "/api/auth/me", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled session still valid: %d", rec.Code)
	}
	bad := e.client().do("POST", "/api/auth/login", map[string]string{"username": "member1", "password": "member-password-123"})
	if bad.Code != http.StatusUnauthorized || bad.Body.String() == "" {
		t.Fatalf("disabled login: %d", bad.Code)
	}
	_ = viewer

	// The last active admin cannot go away.
	if rec := admin.do("PUT", "/api/users/1", map[string]string{"role": "member"}); rec.Code != http.StatusConflict {
		t.Fatalf("demote last admin: %d", rec.Code)
	}
	if rec := admin.do("PUT", "/api/users/1", map[string]bool{"disabled": true}); rec.Code != http.StatusConflict {
		t.Fatalf("disable self: %d", rec.Code)
	}
	if rec := admin.do("DELETE", "/api/users/1", nil); rec.Code != http.StatusConflict {
		t.Fatalf("delete self: %d", rec.Code)
	}
	// Deleting a member works and is audited.
	if rec := admin.do("DELETE", "/api/users/"+itoa(mid), nil); rec.Code != http.StatusOK {
		t.Fatalf("delete member: %d %s", rec.Code, rec.Body.String())
	}
	entries := auditEntries(t, admin)
	found := false
	for _, en := range entries {
		if en.Action == "user.delete" && en.Target == "member1" {
			found = true
		}
	}
	if !found {
		t.Fatal("user.delete not audited")
	}
}

func TestTeamLastAdminCanRotateWhenTwoExist(t *testing.T) {
	e := newTestEnv(t)
	admin := e.client()
	e.setupAdmin(admin)
	createUser(t, admin, "admin2", "admin2-password-123", "admin")
	// With a second active admin, demoting yourself is allowed.
	if rec := admin.do("PUT", "/api/users/1", map[string]string{"role": "member"}); rec.Code != http.StatusOK {
		t.Fatalf("demote with backup admin: %d %s", rec.Code, rec.Body.String())
	}
	admin2 := loginAs(t, e, "admin2", "admin2-password-123")
	if rec := admin2.do("GET", "/api/users", nil); rec.Code != http.StatusOK {
		t.Fatalf("backup admin cannot list: %d", rec.Code)
	}
}

func TestTeamSharedConnectionsAndGrants(t *testing.T) {
	e := newTestEnv(t)
	admin := e.client()
	e.setupAdmin(admin)
	createUser(t, admin, "member1", "member-password-123", "member")
	createUser(t, admin, "member2", "member2-password-123", "member")
	member1 := loginAs(t, e, "member1", "member-password-123")
	member2 := loginAs(t, e, "member2", "member2-password-123")

	shared := createConn(t, admin, "team-db", "dev", boolPtr(true))
	if !shared.Shared {
		t.Fatal("shared flag not stored")
	}
	// Members cannot share: the flag is admin-only.
	if rec := member1.do("POST", "/api/connections", map[string]any{"name": "mine", "host": "h", "username": "u", "tlsMode": "disabled", "shared": true}); rec.Code != http.StatusForbidden {
		t.Fatalf("member shared a connection: %d", rec.Code)
	}
	private := createConn(t, member1, "mine", "dev", nil)

	// Before any grant, the shared profile is invisible to members.
	if rec := member1.do("GET", "/api/connections/"+shared.ID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("ungranted access: %d", rec.Code)
	}
	m1id := userID(t, e, admin, "member1")
	if rec := admin.do("POST", "/api/connections/"+shared.ID+"/grants", map[string]any{"userId": m1id, "level": "bogus"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad grant level: %d", rec.Code)
	}
	if rec := admin.do("POST", "/api/connections/"+shared.ID+"/grants", map[string]any{"userId": m1id, "level": "use"}); rec.Code != http.StatusCreated {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body.String())
	}
	if rec := member1.do("GET", "/api/connections/"+shared.ID, nil); rec.Code != http.StatusOK {
		t.Fatalf("granted read: %d %s", rec.Code, rec.Body.String())
	}
	// A use grant does not allow editing.
	if rec := member1.do("PUT", "/api/connections/"+shared.ID, map[string]any{"name": "hijacked", "host": "h", "username": "u", "tlsMode": "disabled"}); rec.Code != http.StatusNotFound {
		t.Fatalf("use grant edited: %d", rec.Code)
	}
	// Grants on private connections are rejected.
	m2id := userID(t, e, admin, "member2")
	if rec := admin.do("POST", "/api/connections/"+private.ID+"/grants", map[string]any{"userId": m2id, "level": "use"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("grant on private connection: %d", rec.Code)
	}
	// Manage grants allow editing but not deleting.
	if rec := admin.do("POST", "/api/connections/"+shared.ID+"/grants", map[string]any{"userId": m1id, "level": "manage"}); rec.Code != http.StatusCreated {
		t.Fatalf("manage grant: %d %s", rec.Code, rec.Body.String())
	}
	if rec := member1.do("PUT", "/api/connections/"+shared.ID, map[string]any{"name": "team-db", "host": "localhost", "username": "u", "tlsMode": "disabled"}); rec.Code != http.StatusOK {
		t.Fatalf("manage edit: %d %s", rec.Code, rec.Body.String())
	}
	if rec := member1.do("DELETE", "/api/connections/"+shared.ID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("manage grant deleted: %d", rec.Code)
	}
	// Member 2 sees nothing and learns nothing about the IDs.
	if rec := member2.do("GET", "/api/connections", nil); strings.Contains(rec.Body.String(), shared.ID) {
		t.Fatal("ungranted member lists the shared connection")
	}
	if rec := member2.do("GET", "/api/connections/"+shared.ID+"/grants", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("ungranted grant list: %d", rec.Code)
	}
	// Revoking the grant hides the profile again.
	if rec := admin.do("DELETE", "/api/connections/"+shared.ID+"/grants/"+itoa(m1id), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke grant: %d", rec.Code)
	}
	if rec := member1.do("GET", "/api/connections/"+shared.ID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("revoked access still works: %d", rec.Code)
	}
}

func TestTeamViewerRestrictions(t *testing.T) {
	e := newTestEnv(t)
	admin := e.client()
	e.setupAdmin(admin)
	createUser(t, admin, "viewer1", "viewer-password-123", "viewer")
	viewer := loginAs(t, e, "viewer1", "viewer-password-123")

	if rec := viewer.do("POST", "/api/connections", map[string]any{"name": "v", "host": "h", "username": "u", "tlsMode": "disabled"}); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer created a connection: %d", rec.Code)
	}
	dev := createConn(t, admin, "team-dev", "dev", boolPtr(true))
	prod := createConn(t, admin, "team-prod", "prod", boolPtr(true))
	vid := userID(t, e, admin, "viewer1")
	for _, id := range []string{dev.ID, prod.ID} {
		if rec := admin.do("POST", "/api/connections/"+id+"/grants", map[string]any{"userId": vid, "level": "use"}); rec.Code != http.StatusCreated {
			t.Fatalf("grant: %d", rec.Code)
		}
	}
	if rec := viewer.do("GET", "/api/connections/"+dev.ID, nil); rec.Code != http.StatusOK {
		t.Fatalf("viewer dev read: %d", rec.Code)
	}
	// Viewers never open prod, even when granted.
	if rec := viewer.do("GET", "/api/connections/"+prod.ID, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer opened prod: %d", rec.Code)
	}
	if rec := viewer.do("PUT", "/api/connections/"+dev.ID, map[string]any{"name": "x", "host": "h", "username": "u", "tlsMode": "disabled"}); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer edited: %d", rec.Code)
	}
	if rec := viewer.do("DELETE", "/api/connections/"+dev.ID, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer deleted: %d", rec.Code)
	}
	if rec := viewer.do("POST", "/api/connections/transfer/import", map[string]any{"file": "{}"}); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer imported: %d", rec.Code)
	}
}

func TestTeamAuditExportAndPurge(t *testing.T) {
	e := newTestEnv(t)
	admin := e.client()
	e.setupAdmin(admin)
	createUser(t, admin, "member1", "member-password-123", "member")
	member := loginAs(t, e, "member1", "member-password-123")

	rec := admin.do("GET", "/api/audit/export?format=jsonl", nil)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/x-ndjson") {
		t.Fatalf("jsonl export: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected several audit rows, got %d", len(lines))
	}
	for _, line := range lines {
		var en audit.Entry
		if err := json.Unmarshal([]byte(line), &en); err != nil || en.Action == "" || en.Hash == "" {
			t.Fatalf("bad jsonl row: %q", line)
		}
	}
	rec = admin.do("GET", "/api/audit/export?format=csv", nil)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "id,at,user_id") {
		t.Fatalf("csv export: %d %q", rec.Code, rec.Body.String())
	}
	if rec := admin.do("GET", "/api/audit/export?format=xml", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad export format: %d", rec.Code)
	}
	// Members export only their own trail.
	rec = member.do("GET", "/api/audit/export?format=jsonl", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("member export: %d", rec.Code)
	}
	for _, line := range strings.Split(strings.TrimSpace(rec.Body.String()), "\n") {
		var en audit.Entry
		json.Unmarshal([]byte(line), &en)
		if en.Username != "member1" {
			t.Fatalf("member exported another user's row: %+v", en)
		}
	}
	// Purge validates its input and is a no-op when nothing is old.
	if rec := admin.do("POST", "/api/audit/purge", map[string]int{"olderThanDays": 0}); rec.Code != http.StatusBadRequest {
		t.Fatalf("purge validation: %d", rec.Code)
	}
	if rec := member.do("POST", "/api/audit/purge", map[string]int{"olderThanDays": 30}); rec.Code != http.StatusForbidden {
		t.Fatalf("member purged: %d", rec.Code)
	}
	rec = admin.do("POST", "/api/audit/purge", map[string]int{"olderThanDays": 30})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted":0`) {
		t.Fatalf("purge no-op: %d %s", rec.Code, rec.Body.String())
	}
	// A real purge keeps the chain verifiable and the triggers in place.
	res, err := e.srv.Audit.PurgeOlderThan(t.Context(), time.Now().Unix()+1)
	if err != nil || res.Deleted < 2 || res.Anchor == "" {
		t.Fatalf("purge: %+v %v", res, err)
	}
	v, err := e.srv.Audit.Verify(t.Context())
	if err != nil || !v.OK {
		t.Fatalf("chain broken after purge: %+v %v", v, err)
	}
	var triggers int
	if err := e.store.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name IN ('audit_log_no_update', 'audit_log_no_delete')`).Scan(&triggers); err != nil || triggers != 2 {
		t.Fatalf("append-only triggers missing after purge: %d %v", triggers, err)
	}
}

// --- OIDC ---

type fakeOIDC struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	issuer string
	token  string
}

func newFakeOIDC(t *testing.T) *fakeOIDC {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fp := &fakeOIDC{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer": fp.issuer, "authorization_endpoint": fp.issuer + "/auth",
			"token_endpoint": fp.issuer + "/token", "jwks_uri": fp.issuer + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(fp.key.PublicKey.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(fp.key.PublicKey.E)).Bytes())
		json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{"kty": "RSA", "kid": "k", "n": n, "e": e}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id_token": fp.token})
	})
	fp.srv = httptest.NewServer(mux)
	t.Cleanup(fp.srv.Close)
	fp.issuer = fp.srv.URL
	return fp
}

func (fp *fakeOIDC) mint(claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, fp.key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func oidcClaims(issuer, nonce string) map[string]any {
	now := time.Now().Unix()
	return map[string]any{
		"iss": issuer, "aud": "test-client", "sub": "sso-user-1",
		"exp": now + 300, "iat": now, "nonce": nonce,
		"email": "sso@example.com", "email_verified": true, "preferred_username": "ssonaut",
	}
}

func newOIDCEnv(t *testing.T, fp *fakeOIDC, autoProvision bool) *testEnv {
	t.Helper()
	return newOIDCEnvWith(t, fp, sso.Config{AutoProvision: autoProvision, AllowedDomains: []string{"example.com"}})
}

func newOIDCEnvWith(t *testing.T, fp *fakeOIDC, cfg sso.Config) *testEnv {
	t.Helper()
	cfg.Issuer, cfg.ClientID, cfg.ClientSecret, cfg.RedirectURL = fp.issuer, "test-client", "secret", "http://example/callback"
	return newTestEnvWith(t, func(d *Deps) { d.OIDC = sso.NewWithClient(cfg, fp.srv.Client()) })
}

// oidcBegin starts an SSO login through the real endpoint, as a browser would:
// the state cookie lands in c's jar. It returns the state and nonce sent to the provider.
func oidcBegin(t *testing.T, c *client) (state, nonce string) {
	t.Helper()
	rec := c.do("GET", "/api/auth/oidc/login", nil)
	loc, err := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || err != nil {
		t.Fatalf("oidc login: %d %v", rec.Code, err)
	}
	q := loc.Query()
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorize request without PKCE: %s", loc)
	}
	return q.Get("state"), q.Get("nonce")
}

// oidcFinish completes the login with the given claims and returns the callback response.
func oidcFinish(t *testing.T, c *client, fp *fakeOIDC, state string, claims map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	fp.token = fp.mint(claims)
	return c.do("GET", "/api/auth/oidc/callback?code=x&state="+state, nil)
}

func TestTeamOIDCLoginAndLink(t *testing.T) {
	fp := newFakeOIDC(t)
	e := newOIDCEnv(t, fp, true)

	rec := e.client().do("GET", "/api/auth/oidc/status", nil)
	if !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("status: %s", rec.Body.String())
	}

	c := e.client()
	state, nonce := oidcBegin(t, c)
	cb := oidcFinish(t, c, fp, state, oidcClaims(fp.issuer, nonce))
	if cb.Code != http.StatusFound || cb.Header().Get("Location") != "/" {
		t.Fatalf("callback: %d %q", cb.Code, cb.Header().Get("Location"))
	}
	if me := c.do("GET", "/api/auth/me", nil); !strings.Contains(me.Body.String(), `"role":"member"`) {
		t.Fatalf("provisioned session is not a member: %s", me.Body.String())
	}
	var users, links int
	e.store.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users)
	e.store.DB.QueryRow(`SELECT COUNT(*) FROM oidc_accounts`).Scan(&links)
	if users != 1 || links != 1 {
		t.Fatalf("users=%d links=%d", users, links)
	}

	// Signing in again links the same account instead of creating another user.
	c2 := e.client()
	state2, nonce2 := oidcBegin(t, c2)
	if cb := oidcFinish(t, c2, fp, state2, oidcClaims(fp.issuer, nonce2)); cb.Code != http.StatusFound || cb.Header().Get("Location") != "/" {
		t.Fatalf("second callback: %d %q", cb.Code, cb.Header().Get("Location"))
	}
	e.store.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users)
	if users != 1 {
		t.Fatalf("second SSO sign-in duplicated the user (users=%d)", users)
	}

	// Forged tokens fail closed, back in the app.
	c3 := e.client()
	state3, nonce3 := oidcBegin(t, c3)
	bad := fp.mint(oidcClaims(fp.issuer, nonce3))
	fp.token = bad[:len(bad)-2] + "AA"
	cb = c3.do("GET", "/api/auth/oidc/callback?code=x&state="+state3, nil)
	if cb.Code != http.StatusFound || !strings.HasPrefix(cb.Header().Get("Location"), "/?sso=error") {
		t.Fatalf("forged token: %d %q", cb.Code, cb.Header().Get("Location"))
	}
}

// A callback only completes in the browser that started the login: otherwise an
// attacker could send a victim their own callback URL and sign them in as the
// attacker (login CSRF). States are also single-use.
func TestTeamOIDCStateIsBoundToTheBrowser(t *testing.T) {
	fp := newFakeOIDC(t)
	e := newOIDCEnv(t, fp, true)
	attacker := e.client()
	state, nonce := oidcBegin(t, attacker)
	victim := e.client()
	cb := oidcFinish(t, victim, fp, state, oidcClaims(fp.issuer, nonce))
	if !strings.HasPrefix(cb.Header().Get("Location"), "/?sso=error") || victim.cookie != nil {
		t.Fatalf("callback accepted without the state cookie: %q", cb.Header().Get("Location"))
	}
	// A consumed state cannot be replayed, even with its cookie.
	ok := e.client()
	s2, n2 := oidcBegin(t, ok)
	stateCookie := ok.jar[oidcStateCookie]
	if cb := oidcFinish(t, ok, fp, s2, oidcClaims(fp.issuer, n2)); cb.Header().Get("Location") != "/" {
		t.Fatalf("legit login failed: %q", cb.Header().Get("Location"))
	}
	replay := e.client()
	replay.jar = map[string]*http.Cookie{oidcStateCookie: stateCookie}
	if cb := oidcFinish(t, replay, fp, s2, oidcClaims(fp.issuer, n2)); !strings.HasPrefix(cb.Header().Get("Location"), "/?sso=error") {
		t.Fatalf("replayed state accepted: %q", cb.Header().Get("Location"))
	}
}

func TestTeamOIDCRequiresProvisioning(t *testing.T) {
	fp := newFakeOIDC(t)
	e := newOIDCEnv(t, fp, false)
	c := e.client()
	state, nonce := oidcBegin(t, c)
	cb := oidcFinish(t, c, fp, state, oidcClaims(fp.issuer, nonce))
	if cb.Code != http.StatusFound || !strings.Contains(cb.Header().Get("Location"), "reason=provisioned") {
		t.Fatalf("unprovisioned SSO: %d %q", cb.Code, cb.Header().Get("Location"))
	}
}

// Without auto-provisioning, an admin prepares the account with the person's email
// and SSO links to it (case-insensitively). Ambiguous emails and foreign domains never link.
func TestTeamOIDCLinksPreparedAccounts(t *testing.T) {
	fp := newFakeOIDC(t)
	e := newOIDCEnv(t, fp, false)
	admin := e.client()
	e.setupAdmin(admin)
	if rec := admin.do("POST", "/api/users", map[string]string{"username": "ana", "password": "ana-password-123", "role": "member", "email": "SSO@Example.com"}); rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d", rec.Code)
	}
	c := e.client()
	state, nonce := oidcBegin(t, c)
	if cb := oidcFinish(t, c, fp, state, oidcClaims(fp.issuer, nonce)); cb.Header().Get("Location") != "/" {
		t.Fatalf("prepared account not linked: %q", cb.Header().Get("Location"))
	}
	if me := c.do("GET", "/api/auth/me", nil); !strings.Contains(me.Body.String(), `"username":"ana"`) {
		t.Fatalf("linked to the wrong account: %s", me.Body.String())
	}

	// Two accounts with the same email make the match ambiguous: no link.
	for _, u := range []string{"dup1", "dup2"} {
		if rec := admin.do("POST", "/api/users", map[string]string{"username": u, "password": u + "-password-123", "role": "member", "email": "Dup@example.com"}); rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d", u, rec.Code)
		}
	}
	c2 := e.client()
	s2, n2 := oidcBegin(t, c2)
	dup := oidcClaims(fp.issuer, n2)
	dup["sub"], dup["email"] = "someone-else", "dup@example.com"
	if cb := oidcFinish(t, c2, fp, s2, dup); !strings.Contains(cb.Header().Get("Location"), "reason=provisioned") {
		t.Fatalf("ambiguous email linked: %q", cb.Header().Get("Location"))
	}

	// Emails outside the allowed domains are refused before any lookup.
	c3 := e.client()
	s3, n3 := oidcBegin(t, c3)
	foreign := oidcClaims(fp.issuer, n3)
	foreign["sub"], foreign["email"] = "outsider", "eve@evil.com"
	if cb := oidcFinish(t, c3, fp, s3, foreign); !strings.Contains(cb.Header().Get("Location"), "reason=domain") {
		t.Fatalf("foreign domain accepted: %q", cb.Header().Get("Location"))
	}
}

// SSO proves the identity, not the account's own second factor: an account with
// local 2FA is asked for it unless the provider reports MFA in amr.
func TestTeamOIDCKeepsLocalTwoFactor(t *testing.T) {
	fp := newFakeOIDC(t)
	e := newOIDCEnv(t, fp, false)
	admin := e.client()
	e.setupAdmin(admin)
	_, recovery := enrollMFA(t, admin)
	if rec := admin.do("PUT", "/api/users/"+itoa(userID(t, e, admin, "admin")), map[string]string{"email": "sso@example.com"}); rec.Code != http.StatusOK {
		t.Fatalf("set email: %d %s", rec.Code, rec.Body.String())
	}

	c := e.client()
	state, nonce := oidcBegin(t, c)
	cb := oidcFinish(t, c, fp, state, oidcClaims(fp.issuer, nonce))
	if cb.Header().Get("Location") != "/?sso=mfa" || c.cookie != nil || c.jar[mfaPendingCookie] == nil {
		t.Fatalf("local 2FA skipped by SSO: %q cookie=%v", cb.Header().Get("Location"), c.cookie)
	}
	rec := c.do("POST", "/api/auth/mfa/verify", map[string]string{"code": recovery[0]})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"mfa":true`) {
		t.Fatalf("second factor after SSO: %d %s", rec.Code, rec.Body.String())
	}

	// A provider that reports MFA signs in directly with a verified session.
	c2 := e.client()
	s2, n2 := oidcBegin(t, c2)
	withMFA := oidcClaims(fp.issuer, n2)
	withMFA["amr"] = []string{"pwd", "mfa"}
	if cb := oidcFinish(t, c2, fp, s2, withMFA); cb.Header().Get("Location") != "/" {
		t.Fatalf("provider MFA not honoured: %q", cb.Header().Get("Location"))
	}
	if me := c2.do("GET", "/api/auth/me", nil); !strings.Contains(me.Body.String(), `"mfa":true`) {
		t.Fatalf("session not marked as MFA: %s", me.Body.String())
	}
}

func TestTeamOIDCDisabled(t *testing.T) {
	e := newTestEnv(t)
	rec := e.client().do("GET", "/api/auth/oidc/status", nil)
	if !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("status: %s", rec.Body.String())
	}
	if rec := e.client().do("GET", "/api/auth/oidc/login", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("login without config: %d", rec.Code)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// A manage grant lets someone edit a shared profile, not walk away with its stored
// password: a draft or an edit aimed at another destination never reuses it.
func TestTeamGranteesCannotRedirectStoredSecrets(t *testing.T) {
	e := newTestEnv(t)
	admin := e.client()
	e.setupAdmin(admin)
	createUser(t, admin, "member1", "member-password-123", "member")
	member := loginAs(t, e, "member1", "member-password-123")
	rec := admin.do("POST", "/api/connections", map[string]any{
		"name": "team-db", "env": "dev", "host": "db.internal", "username": "app",
		"password": "TopSecret-123", "tlsMode": "verify_identity", "readOnly": true, "shared": true,
	})
	var shared connView
	json.Unmarshal(rec.Body.Bytes(), &shared)
	m1 := userID(t, e, admin, "member1")
	grant := func(level string) {
		t.Helper()
		if rec := admin.do("POST", "/api/connections/"+shared.ID+"/grants", map[string]any{"userId": m1, "level": level}); rec.Code != http.StatusCreated {
			t.Fatalf("grant %s: %d", level, rec.Code)
		}
	}
	draft := map[string]any{"name": "x", "env": "dev", "host": "attacker.example", "username": "app", "tlsMode": "required"}

	grant(GrantUse)
	if rec := member.do("POST", "/api/connections/"+shared.ID+"/test-draft", draft); rec.Code != http.StatusNotFound {
		t.Fatalf("a use grant must not test drafts with the stored secrets: %d", rec.Code)
	}
	if rec := member.do("POST", "/api/connections/"+shared.ID+"/ssh/fingerprint", map[string]any{"host": "attacker.example"}); rec.Code != http.StatusNotFound {
		t.Fatalf("a use grant must not reach the stored bastion: %d", rec.Code)
	}

	grant(GrantManage)
	base := map[string]any{"name": "team-db", "env": "dev", "host": "db.internal", "username": "app", "tlsMode": "verify_identity", "readOnly": true}
	for field, value := range map[string]any{"env": "staging", "readOnly": false, "tlsMode": "required"} {
		body := map[string]any{}
		for k, v := range base {
			body[k] = v
		}
		body[field] = value
		if rec := member.do("PUT", "/api/connections/"+shared.ID, body); rec.Code != http.StatusForbidden {
			t.Fatalf("manage grantee lowered %s: %d %s", field, rec.Code, rec.Body.String())
		}
	}
	// Re-pointing the profile drops the stored password instead of carrying it along.
	moved := map[string]any{"name": "team-db", "env": "dev", "host": "elsewhere.example", "username": "app", "tlsMode": "verify_identity", "readOnly": true}
	rec = member.do("PUT", "/api/connections/"+shared.ID, moved)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"hasPassword":false`) {
		t.Fatalf("stored password must not follow a new destination: %d %s", rec.Code, rec.Body.String())
	}
	// Owners and admins keep full control of their own profiles.
	rec = admin.do("PUT", "/api/connections/"+shared.ID, map[string]any{"name": "team-db", "env": "staging", "host": "db.internal", "username": "app", "password": "TopSecret-123", "tlsMode": "required"})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin edit: %d %s", rec.Code, rec.Body.String())
	}
}

// Viewers never open prod, including connections they owned before a demotion.
func TestTeamDemotedViewerCannotOpenOwnProd(t *testing.T) {
	e := newTestEnv(t)
	admin := e.client()
	e.setupAdmin(admin)
	createUser(t, admin, "dana", "dana-password-1234", "member")
	dana := loginAs(t, e, "dana", "dana-password-1234")
	prod := createConn(t, dana, "dana-prod", "prod", nil)
	if rec := admin.do("PUT", "/api/users/"+itoa(userID(t, e, admin, "dana")), map[string]any{"role": "viewer"}); rec.Code != http.StatusOK {
		t.Fatalf("demote: %d", rec.Code)
	}
	if rec := dana.do("POST", "/api/connections/"+prod.ID+"/test", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("demoted viewer opened their prod connection: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDestinationChanged(t *testing.T) {
	rec := &connRecord{Host: "db", Port: 3306, Username: "app"}
	jump := tunnel.Hop{Host: "bastion", Port: 22, User: "ops"}
	stored := secretBlob{SSH: tunnel.Config{Enabled: true, Hop: tunnel.Hop{Host: "ssh", Port: 22, User: "ops"}, Jump: &jump}}
	same := connInput{Host: "db", Port: 3306, Username: "app",
		SSH: &tunnel.Config{Enabled: true, Hop: tunnel.Hop{Host: "ssh", Port: 22, User: "ops"}, Jump: &tunnel.Hop{Host: "bastion", Port: 22, User: "ops"}}}
	if destinationChanged(rec, stored, same) {
		t.Fatal("identical destination reported as changed")
	}
	for name, mutate := range map[string]func(*connInput){
		"mysql host": func(in *connInput) { in.Host = "evil" },
		"mysql user": func(in *connInput) { in.Username = "root" },
		"ssh host":   func(in *connInput) { s := *in.SSH; s.Hop.Host = "evil"; in.SSH = &s },
		"jump host":  func(in *connInput) { s := *in.SSH; j := *s.Jump; j.Host = "evil"; s.Jump = &j; in.SSH = &s },
		"jump drop":  func(in *connInput) { s := *in.SSH; s.Jump = nil; in.SSH = &s },
	} {
		in := same
		mutate(&in)
		if !destinationChanged(rec, stored, in) {
			t.Errorf("%s change not detected", name)
		}
	}
}

// An update is all-or-nothing: an invalid field must not leave the account
// half-changed or log its sessions out.
func TestTeamUpdateUserValidatesBeforeApplying(t *testing.T) {
	e := newTestEnv(t)
	admin := e.client()
	e.setupAdmin(admin)
	createUser(t, admin, "bea", "bea-password-12345", "member")
	bea := loginAs(t, e, "bea", "bea-password-12345")
	id := itoa(userID(t, e, admin, "bea"))
	rec := admin.do("PUT", "/api/users/"+id, map[string]any{"password": "new-password-12345", "email": "not-an-email"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid email accepted: %d", rec.Code)
	}
	if rec := bea.do("GET", "/api/auth/me", nil); rec.Code != http.StatusOK {
		t.Fatalf("a rejected update logged the user out: %d", rec.Code)
	}
	loginAs(t, e, "bea", "bea-password-12345") // password unchanged
	if rec := admin.do("PUT", "/api/users/"+itoa(userID(t, e, admin, "admin")), map[string]any{"role": "member"}); rec.Code != http.StatusConflict {
		t.Fatalf("last admin demoted: %d", rec.Code)
	}
}
