package sso

import (
	"net/http"
	"net/url"
	"testing"
)

type googleTestTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (tr googleTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme, u.Host = tr.target.Scheme, tr.target.Host
	copy.URL, copy.Host = &u, tr.target.Host
	return tr.base.RoundTrip(copy)
}

func TestGoogleWorkspaceSignedIdentityAndHints(t *testing.T) {
	fp := newFakeProvider(t)
	target, err := url.Parse(fp.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	fp.issuer = "https://accounts.google.com"
	provider := NewWithClient(Config{
		Issuer: fp.issuer, ClientID: "test-client", ClientSecret: "secret",
		RedirectURL:    "https://dbyson.equaly.pe/api/auth/oidc/callback",
		AllowedDomains: []string{"equaly.pe", "qconsultores.pe"},
	}, &http.Client{Transport: googleTestTransport{target: target, base: fp.srv.Client().Transport}})
	for _, tc := range []struct {
		email, domain, issuer string
		verified, allowed     bool
	}{
		{"ana@equaly.pe", "equaly.pe", fp.issuer, true, true},
		{"ana@qconsultores.pe", "qconsultores.pe", fp.issuer, true, true},
		{"ana@equaly.pe", "equaly.pe", "accounts.google.com", true, true},
		{"ana@equaly.pe", "", fp.issuer, true, false},
		{"ana@equaly.pe", "foreign.pe", fp.issuer, true, false},
		{"ana@equaly.pe", "equaly.pe.evil.com", fp.issuer, true, false},
		{"ana@gmail.com", "", fp.issuer, true, false},
		{"ana@foreign.pe", "equaly.pe", fp.issuer, true, false},
		{"ana@sub.equaly.pe", "sub.equaly.pe", fp.issuer, true, false},
		{"ana@equaly.pe", "equaly.pe", fp.issuer, false, false},
	} {
		claims := baseClaims(tc.issuer)
		claims["email"], claims["email_verified"], claims["hd"] = tc.email, tc.verified, tc.domain
		identity, err := provider.Verify(fp.mint(claims), "test-nonce")
		if err != nil {
			t.Fatalf("signed identity: %v", err)
		}
		if got := provider.Config().IdentityAllowed(identity); got != tc.allowed {
			t.Errorf("%s hd=%q verified=%v: allowed=%v want %v", tc.email, tc.domain, tc.verified, got, tc.allowed)
		}
	}
	for _, domains := range [][]string{{"equaly.pe"}, {"equaly.pe", "qconsultores.pe"}, nil} {
		provider.cfg.AllowedDomains = domains
		authorize, err := provider.AuthorizeURL("state", "nonce", "challenge")
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(authorize)
		if err != nil {
			t.Fatal(err)
		}
		want := ""
		if len(domains) == 1 {
			want = "equaly.pe"
		} else if len(domains) > 1 {
			want = "*"
		}
		if u.Query().Get("hd") != want || u.Query().Get("prompt") != "select_account" {
			t.Fatalf("wrong Workspace hint: %s", authorize)
		}
	}
	// Other providers retain the verified-email rule and do not interpret Google's hd.
	other := Config{Issuer: "https://id.example.com", AllowedDomains: []string{"equaly.pe"}}
	if !other.IdentityAllowed(&Claims{Email: "ana@equaly.pe", EmailVerified: true}) {
		t.Fatal("generic OIDC regressed")
	}
}
