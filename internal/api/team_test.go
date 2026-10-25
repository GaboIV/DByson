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
	"strings"
	"testing"
	"time"

	"github.com/gaboiv/dbyson/internal/audit"
	"github.com/gaboiv/dbyson/internal/sso"
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
	return newTestEnvWith(t, func(d *Deps) {
		d.OIDC = sso.NewWithClient(sso.Config{
			Issuer: fp.issuer, ClientID: "test-client", ClientSecret: "secret",
			RedirectURL: "http://example/callback", AutoProvision: autoProvision,
		}, fp.srv.Client())
	})
}

func TestTeamOIDCLoginAndLink(t *testing.T) {
	fp := newFakeOIDC(t)
	e := newOIDCEnv(t, fp, true)

	rec := e.client().do("GET", "/api/auth/oidc/status", nil)
	if !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("status: %s", rec.Body.String())
	}

	c := e.client()
	state, nonce, err := e.srv.oidcStates.create()
	if err != nil {
		t.Fatal(err)
	}
	fp.token = fp.mint(oidcClaims(fp.issuer, nonce))
	cb := c.do("GET", "/api/auth/oidc/callback?code=anything&state="+state, nil)
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
	state2, nonce2, _ := e.srv.oidcStates.create()
	fp.token = fp.mint(oidcClaims(fp.issuer, nonce2))
	if cb := c2.do("GET", "/api/auth/oidc/callback?code=x&state="+state2, nil); cb.Code != http.StatusFound {
		t.Fatalf("second callback: %d", cb.Code)
	}
	e.store.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users)
	if users != 1 {
		t.Fatalf("second SSO sign-in duplicated the user (users=%d)", users)
	}

	// Replayed states and forged tokens fail closed, back in the app.
	cb = e.client().do("GET", "/api/auth/oidc/callback?code=x&state="+state, nil)
	if cb.Code != http.StatusFound || !strings.HasPrefix(cb.Header().Get("Location"), "/?sso=error") {
		t.Fatalf("replayed state: %d %q", cb.Code, cb.Header().Get("Location"))
	}
	state3, nonce3, _ := e.srv.oidcStates.create()
	bad := fp.mint(oidcClaims(fp.issuer, nonce3))
	fp.token = bad[:len(bad)-2] + "AA"
	cb = e.client().do("GET", "/api/auth/oidc/callback?code=x&state="+state3, nil)
	if cb.Code != http.StatusFound || !strings.HasPrefix(cb.Header().Get("Location"), "/?sso=error") {
		t.Fatalf("forged token: %d %q", cb.Code, cb.Header().Get("Location"))
	}
}

func TestTeamOIDCRequiresProvisioning(t *testing.T) {
	fp := newFakeOIDC(t)
	e := newOIDCEnv(t, fp, false)
	c := e.client()
	state, nonce, _ := e.srv.oidcStates.create()
	fp.token = fp.mint(oidcClaims(fp.issuer, nonce))
	cb := c.do("GET", "/api/auth/oidc/callback?code=x&state="+state, nil)
	if cb.Code != http.StatusFound || !strings.Contains(cb.Header().Get("Location"), "reason=provisioned") {
		t.Fatalf("unprovisioned SSO: %d %q", cb.Code, cb.Header().Get("Location"))
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
