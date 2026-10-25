package sso

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
)

type fakeProvider struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	issuer string
	token  string // id_token served by /token; tests rewrite it per case
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fp := &fakeProvider{key: key, kid: "test-key"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 fp.issuer,
			"authorization_endpoint": fp.issuer + "/auth",
			"token_endpoint":         fp.issuer + "/token",
			"jwks_uri":               fp.issuer + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(fp.key.PublicKey.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(fp.key.PublicKey.E)).Bytes())
		json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{"kty": "RSA", "kid": fp.kid, "alg": "RS256", "n": n, "e": e}},
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

func (fp *fakeProvider) mint(claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": fp.kid, "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, fp.key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func baseClaims(issuer string) map[string]any {
	now := time.Now().Unix()
	return map[string]any{
		"iss": issuer, "aud": "test-client", "sub": "user-123",
		"exp": now + 300, "iat": now, "nonce": "test-nonce",
		"email": "sso@example.com", "email_verified": true, "preferred_username": "ssonaut",
	}
}

func providerFor(t *testing.T, fp *fakeProvider) *Provider {
	t.Helper()
	return NewWithClient(Config{
		Issuer: fp.issuer, ClientID: "test-client", ClientSecret: "secret",
		RedirectURL: "http://example/callback", AutoProvision: true,
	}, fp.srv.Client())
}

func TestVerifyHappyPath(t *testing.T) {
	fp := newFakeProvider(t)
	p := providerFor(t, fp)
	fp.token = fp.mint(baseClaims(fp.issuer))
	raw, err := p.Exchange("any-code")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := p.Verify(raw, "test-nonce")
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "user-123" || claims.Email != "sso@example.com" || !claims.EmailVerified || claims.PreferredUsername != "ssonaut" {
		t.Fatalf("wrong claims: %+v", claims)
	}
}

func TestVerifyRejects(t *testing.T) {
	fp := newFakeProvider(t)
	p := providerFor(t, fp)
	cases := map[string]func(map[string]any){
		"expired":        func(c map[string]any) { c["exp"] = time.Now().Unix() - 100 },
		"wrong audience": func(c map[string]any) { c["aud"] = "someone-else" },
		"wrong issuer":   func(c map[string]any) { c["iss"] = "https://evil.example" },
		"missing sub":    func(c map[string]any) { delete(c, "sub") },
	}
	for name, mutate := range cases {
		c := baseClaims(fp.issuer)
		mutate(c)
		if _, err := p.Verify(fp.mint(c), "test-nonce"); err == nil {
			t.Fatalf("%s token accepted", name)
		}
	}
	if _, err := p.Verify(fp.mint(baseClaims(fp.issuer)), "other-nonce"); err == nil {
		t.Fatal("nonce mismatch accepted")
	}
	// Wrong algorithm fails closed even with a plausible token shape.
	header, _ := json.Marshal(map[string]string{"alg": "none", "kid": fp.kid})
	payload, _ := json.Marshal(baseClaims(fp.issuer))
	bogus := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "."
	if _, err := p.Verify(bogus, "test-nonce"); err == nil {
		t.Fatal("alg=none accepted")
	}
	// Tampered signature.
	good := fp.mint(baseClaims(fp.issuer))
	tampered := good[:len(good)-2] + "AA"
	if _, err := p.Verify(tampered, "test-nonce"); err == nil {
		t.Fatal("tampered signature accepted")
	}
	if _, err := p.Verify("not.a.token", "test-nonce"); err == nil {
		t.Fatal("malformed token accepted")
	}
}

func TestAuthorizeURL(t *testing.T) {
	fp := newFakeProvider(t)
	p := providerFor(t, fp)
	target, err := p.AuthorizeURL("state-1", "nonce-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"response_type=code", "client_id=test-client", "state=state-1", "nonce=nonce-1", "scope=openid"} {
		if !strings.Contains(target, want) {
			t.Fatalf("authorize URL missing %q: %s", want, target)
		}
	}
}

func TestDiscoveryIssuerMismatchFailsClosed(t *testing.T) {
	fp := newFakeProvider(t)
	p := NewWithClient(Config{
		Issuer: "https://expected.example", ClientID: "test-client", ClientSecret: "s",
		RedirectURL: "http://example/callback",
	}, fp.srv.Client())
	fp.token = fp.mint(baseClaims(fp.issuer))
	if _, err := p.Exchange("code"); err == nil {
		t.Fatal("issuer mismatch accepted at discovery")
	}
}
