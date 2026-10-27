package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gaboiv/dbyson/internal/sso"
)

type googleOIDCTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (tr googleOIDCTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme, u.Host = tr.target.Scheme, tr.target.Host
	copy.URL, copy.Host = &u, tr.target.Host
	return tr.base.RoundTrip(copy)
}

func TestGoogleWorkspaceCallback(t *testing.T) {
	fp := newFakeOIDC(t)
	target, err := url.Parse(fp.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	fp.issuer = "https://accounts.google.com"
	e := newTestEnvWith(t, func(d *Deps) {
		d.PasswordLoginDisabled = true
		d.OIDC = sso.NewWithClient(sso.Config{
			Issuer: fp.issuer, ClientID: "test-client", ClientSecret: "secret",
			RedirectURL:    "https://dbyson.equaly.pe/api/auth/oidc/callback",
			AllowedDomains: []string{"equaly.pe", "qconsultores.pe"}, AutoProvision: true,
		}, &http.Client{Transport: googleOIDCTransport{target: target, base: fp.srv.Client().Transport}})
	})
	admin := e.client()
	e.setupAdmin(admin)
	if login := e.client().do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "fixture-password"}); login.Code != http.StatusForbidden {
		t.Fatalf("password endpoint remains enabled: %d", login.Code)
	}
	for _, tc := range []struct {
		name, email, hd string
		allowed         bool
	}{
		{"equaly", "ana@equaly.pe", "equaly.pe", true},
		{"qconsultores", "ana@qconsultores.pe", "qconsultores.pe", true},
		{"unmanaged", "ana@equaly.pe", "", false},
		{"foreign", "ana@equaly.pe", "outsider.pe", false},
		{"gmail", "ana@gmail.com", "", false},
	} {
		c := e.client()
		state, nonce := oidcBegin(t, c)
		claims := oidcClaims(fp.issuer, nonce)
		claims["sub"], claims["email"], claims["hd"] = tc.name, tc.email, tc.hd
		callback := oidcFinish(t, c, fp, state, claims)
		location := callback.Header().Get("Location")
		if tc.allowed {
			if location != "/" {
				t.Fatalf("%s login denied: %s", tc.name, location)
			}
			me := c.do("GET", "/api/auth/me", nil)
			if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"role":"member"`) || !strings.Contains(me.Body.String(), `"mfa":false`) {
				t.Fatalf("wrong Google session: %s", me.Body.String())
			}
		} else {
			if !strings.Contains(location, "reason=domain") || c.cookie != nil {
				t.Fatalf("%s admitted: %s", tc.name, location)
			}
		}
	}
	status := e.client().do("GET", "/api/auth/oidc/status", nil)
	if !strings.Contains(status.Body.String(), `"credentialsEnabled":false`) {
		t.Fatalf("password form not disabled: %s", status.Body.String())
	}
	if !strings.Contains(status.Body.String(), `"google":true`) {
		t.Fatalf("Google button unavailable: %s", status.Body.String())
	}
}
