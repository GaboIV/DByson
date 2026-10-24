package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPasskeyConfiguration(t *testing.T) {
	for _, input := range [][2]string{{"", ""}, {"localhost", "http://localhost:18080"}, {"db.example.org", "https://db.example.org"}} {
		if _, err := NewPasskeys(input[0], input[1]); err != nil {
			t.Fatal(input, err)
		}
	}
	for _, input := range [][2]string{{"", "https://example.org"}, {"example.org", ""}, {"example.org", "http://example.org"}, {"example.org", "https://evil.example"}, {"example.org", "https://example.org/path"}, {"example.org", "https://user@example.org"}, {"example.org", "https://example.org?query"}} {
		if _, err := NewPasskeys(input[0], input[1]); err == nil {
			t.Fatal("unsafe config accepted", input)
		}
	}
}

func TestPasskeyCeremoniesRequirePasswordSessionAndSingleUse(t *testing.T) {
	wa, err := NewPasskeys("localhost", "http://localhost:18080")
	if err != nil {
		t.Fatal(err)
	}
	e := newTestEnvWith(t, func(deps *Deps) { deps.WebAuthn = wa })
	c := e.client()
	e.setupAdmin(c)
	path := "/api/auth/passkeys/register/begin"
	if res := c.do("POST", path, map[string]string{"name": "key", "password": "wrong"}); res.Code != 403 {
		t.Fatal("registration without password", res.Code)
	}
	res := c.do("POST", path, map[string]string{"name": "key", "password": "a-long-enough-password"})
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	var options struct {
		PublicKey struct {
			AuthenticatorSelection struct {
				ResidentKey      string `json:"residentKey"`
				UserVerification string `json:"userVerification"`
			} `json:"authenticatorSelection"`
		} `json:"publicKey"`
	}
	if err = json.Unmarshal(res.Body.Bytes(), &options); err != nil {
		t.Fatal(err)
	}
	if options.PublicKey.AuthenticatorSelection.UserVerification != "required" || options.PublicKey.AuthenticatorSelection.ResidentKey != "required" {
		t.Fatal("weak registration", res.Body.String())
	}
	var challenge *http.Cookie
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == passkeyCookie {
			challenge = cookie
		}
	}
	if challenge == nil || !challenge.HttpOnly || challenge.SameSite != http.SameSiteStrictMode {
		t.Fatal("unbound registration challenge")
	}
	// A challenge cookie is not an authenticated application session.
	stranger := e.client()
	stranger.cookie = challenge
	if res = stranger.do("POST", "/api/auth/passkeys/register/finish", map[string]string{}); res.Code != 401 {
		t.Fatal("registered without application cookie", res.Code)
	}
	// A finish call without a ceremony must fail before parsing credentials.
	if res = c.do("POST", "/api/auth/passkeys/register/finish", map[string]string{}); res.Code != 400 {
		t.Fatal("missing challenge accepted", res.Code)
	}
	// Discoverable login discloses no account/credential list.
	guest := e.client()
	res = guest.do("POST", "/api/auth/passkeys/login/begin", nil)
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	var login map[string]any
	json.Unmarshal(res.Body.Bytes(), &login)
	pub := login["publicKey"].(map[string]any)
	if pub["userVerification"] != "required" {
		t.Fatal("weak login", res.Body.String())
	}
	if allowed, ok := pub["allowCredentials"].([]any); ok && len(allowed) > 0 {
		t.Fatal("user enumeration")
	}
}
