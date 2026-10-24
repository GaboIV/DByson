package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gaboiv/dbyson/internal/auth"
)

func TestSessionListAndRevocationOwnership(t *testing.T) {
	e := newTestEnv(t)
	first := e.client()
	e.setupAdmin(first)
	second := e.client()
	res := second.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "a-long-enough-password"})
	second.loginFrom(res)
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	var list struct {
		Sessions []auth.SessionInfo `json:"sessions"`
	}
	res = first.do("GET", "/api/auth/sessions", nil)
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil || len(list.Sessions) != 2 {
		t.Fatalf("sessions: %s %v", res.Body, err)
	}
	if strings.Contains(res.Body.String(), first.cookie.Value) || strings.Contains(res.Body.String(), first.csrf) || strings.Contains(res.Body.String(), "id_hash") {
		t.Fatal("leaked authentication material")
	}
	var current, other string
	for _, item := range list.Sessions {
		if item.Current {
			current = item.ID
		} else {
			other = item.ID
		}
	}
	if current == "" || other == "" {
		t.Fatal("missing current session marker")
	}
	// A different account cannot even discover this session via the delete result.
	result, err := e.store.DB.Exec(`INSERT INTO users(username,password_hash,created_at) VALUES('other','unused',0)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	tokens := auth.NewSessions(e.store.DB)
	token, csrf, err := tokens.Create(context.Background(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	foreign := e.client()
	foreign.cookie = &http.Cookie{Name: cookieName, Value: token}
	foreign.csrf = csrf
	if res := foreign.do("DELETE", "/api/auth/sessions/"+other, nil); res.Code != 404 {
		t.Fatalf("foreign revoke: %d", res.Code)
	}
	previous := first.csrf
	first.csrf = ""
	if res := first.do("DELETE", "/api/auth/sessions/"+other, nil); res.Code != 403 {
		t.Fatal("revoke bypassed CSRF")
	}
	first.csrf = previous
	if res := first.do("DELETE", "/api/auth/sessions/"+other, nil); res.Code != 200 {
		t.Fatalf("revoke: %s", res.Body)
	}
	if res := second.do("GET", "/api/auth/me", nil); res.Code != 401 {
		t.Fatal("revoked cookie remained valid")
	}
	if res := first.do("GET", "/api/auth/me", nil); res.Code != 200 {
		t.Fatal("revoked the wrong session")
	}
	if res := first.do("DELETE", "/api/auth/sessions/"+current, nil); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	if res := first.do("GET", "/api/auth/me", nil); res.Code != 401 {
		t.Fatal("self revocation failed")
	}
}

func TestRevokeOthersAndHideExpiredSessions(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	tokens := auth.NewSessions(e.store.DB)
	sess, _ := tokens.Lookup(context.Background(), c.cookie.Value)
	otherToken, _, _ := tokens.Create(context.Background(), sess.UserID, false)
	tokens.Now = func() time.Time { return time.Now().Add(-13 * time.Hour) }
	expired, _, err := tokens.Create(context.Background(), sess.UserID, false)
	if err != nil {
		t.Fatal(err)
	}
	tokens.Now = time.Now
	items, err := tokens.List(context.Background(), sess.UserID, sess.ID)
	if err != nil || len(items) != 2 {
		t.Fatalf("expired session was listed: %+v %v", items, err)
	}
	if _, err := tokens.Lookup(context.Background(), expired); err == nil {
		t.Fatal("expired session accepted")
	}
	if res := c.do("POST", "/api/auth/sessions/revoke-others", nil); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	if _, err := tokens.Lookup(context.Background(), otherToken); err == nil {
		t.Fatal("other session still valid")
	}
	if _, err := tokens.Lookup(context.Background(), c.cookie.Value); err != nil {
		t.Fatal("current session revoked")
	}
}

func TestSessionBoundToVerifiedClientCertificate(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	cert := &x509.Certificate{Raw: []byte("verified-certificate-A")}
	state := &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}
	request := httptest.NewRequest("GET", "/", nil)
	request.TLS = state
	fingerprint := clientCertificate(request)
	tokens := auth.NewSessions(e.store.DB)
	sess, _ := tokens.Lookup(context.Background(), c.cookie.Value)
	token, csrf, err := tokens.CreateWithDetails(context.Background(), sess.UserID, false, auth.SessionDetails{ClientCert: fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	call := func(connection *tls.ConnectionState) int {
		req := httptest.NewRequest("GET", "/api/auth/me", nil)
		req.TLS = connection
		req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("X-SSL-Client-Verify", "SUCCESS")
		res := httptest.NewRecorder()
		e.handler.ServeHTTP(res, req)
		return res.Code
	}
	if call(state) != 200 {
		t.Fatal("same certificate rejected")
	}
	if call(nil) != 401 {
		t.Fatal("certificate-bound cookie accepted without certificate")
	}
	other := &x509.Certificate{Raw: []byte("verified-certificate-B")}
	if call(&tls.ConnectionState{PeerCertificates: []*x509.Certificate{other}, VerifiedChains: [][]*x509.Certificate{{other}}}) != 401 {
		t.Fatal("cookie replayed with a different client certificate")
	}
	if call(&tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}) != 401 {
		t.Fatal("unverified certificate accepted")
	}
	req := httptest.NewRequest("GET", "/api/auth/me", nil)
	req.TLS = state
	req.AddCookie(c.cookie)
	res := httptest.NewRecorder()
	e.handler.ServeHTTP(res, req)
	if res.Code != 401 {
		t.Fatal("legacy unbound cookie was accepted after enabling mTLS")
	}
}

func TestCancelQueryRequiresAuthenticationAndCSRF(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	path := "/api/connections/example/queries/request-0000000001/cancel"
	if res := c.do("POST", path, nil); res.Code != 401 {
		t.Fatal("unauthenticated cancellation accepted")
	}
	e.setupAdmin(c)
	c.csrf = ""
	if res := c.do("POST", path, nil); res.Code != 403 {
		t.Fatal("cancellation bypassed CSRF")
	}
}

func TestCancellationOwnershipAndRevocation(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	tokens := auth.NewSessions(e.store.DB)
	sess, _ := tokens.Lookup(context.Background(), c.cookie.Value)
	// A small isolated registry tests scoping without contacting a database.
	s := &Server{}
	reg := &s.queries
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := &activeQuery{userID: sess.UserID, sessionID: sess.ID, connectionID: "connection-A", cancel: cancel}
	if err := reg.add("request-0000000001", q); err != nil {
		t.Fatal(err)
	}
	call := func(sessionID, connection string) int {
		req := httptest.NewRequest("POST", "/", nil)
		req.SetPathValue("id", connection)
		req.SetPathValue("queryID", "request-0000000001")
		copy := *sess
		copy.ID = sessionID
		req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, &copy))
		res := httptest.NewRecorder()
		s.cancelQuery(res, req)
		return res.Code
	}
	if call("other-session", "connection-A") != 404 || call(sess.ID, "connection-B") != 404 {
		t.Fatal("query cancellation crossed its scope")
	}
	if ctx.Err() != nil {
		t.Fatal("foreign request stopped query")
	}
	if call(sess.ID, "connection-A") != 200 || ctx.Err() == nil {
		t.Fatal("own query was not cancelled")
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	reg = &s.queries
	reg.add("request-0000000002", &activeQuery{userID: sess.UserID, sessionID: "other-session", connectionID: "connection-A", cancel: cancel2})
	reg.revokeSessions(sess.UserID, func(id string) bool { return id != sess.ID })
	select {
	case <-ctx2.Done():
	case <-time.After(time.Second):
		t.Fatal("revocation left query running")
	}
}
