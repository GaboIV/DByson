// Package sso is a minimal generic OpenID Connect client (authorization code
// flow) using only the standard library. It verifies RS256 ID tokens, the
// default issued by Google, Microsoft Entra ID and Keycloak, and deliberately
// supports nothing else: a provider that negotiates a different algorithm
// fails closed instead of downgrading.
package sso

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Config wires one OIDC provider. Issuer is the provider's base URL, e.g.
// https://accounts.google.com, https://login.microsoftonline.com/<tenant>/v2.0
// or https://keycloak.example.com/realms/<realm>.
type Config struct {
	Issuer        string
	ClientID      string
	ClientSecret  string
	RedirectURL   string // this server's callback URL, registered at the provider
	AutoProvision bool   // create a member account on first SSO sign-in
	ProviderName  string // label shown in the UI; defaults to the issuer host
}

// Enabled reports whether SSO was configured at all.
func (c Config) Enabled() bool { return c.Issuer != "" && c.ClientID != "" && c.ClientSecret != "" }

// Name returns the display label for the provider.
func (c Config) Name() string {
	if c.ProviderName != "" {
		return c.ProviderName
	}
	if u, err := url.Parse(c.Issuer); err == nil && u.Host != "" {
		return u.Host
	}
	return c.Issuer
}

// Provider is a discovered OIDC relying party. Discovery and JWKS documents
// are fetched lazily and cached; use NewWithClient in tests.
type Provider struct {
	cfg    Config
	client *http.Client

	mu        sync.Mutex
	discovery *discoveryDoc
	keys      map[string]*rsa.PublicKey
	keysExp   time.Time
}

// New builds a Provider; it performs no network I/O.
func New(cfg Config) *Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: 15 * time.Second}}
}

// NewWithClient is New with an injectable HTTP client (tests).
func NewWithClient(cfg Config, client *http.Client) *Provider {
	return &Provider{cfg: cfg, client: client}
}

// Config returns the provider's configuration.
func (p *Provider) Config() Config { return p.cfg }

type discoveryDoc struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

func (p *Provider) getDiscovery() (*discoveryDoc, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.discovery != nil {
		return p.discovery, nil
	}
	endpoint := strings.TrimSuffix(p.cfg.Issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc discovery: status %d", resp.StatusCode)
	}
	var doc discoveryDoc
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	if doc.Issuer != p.cfg.Issuer {
		return nil, fmt.Errorf("oidc discovery: issuer mismatch (got %q)", doc.Issuer)
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return nil, fmt.Errorf("oidc discovery: incomplete document")
	}
	p.discovery = &doc
	return p.discovery, nil
}

// AuthorizeURL starts the code flow: the browser is redirected there.
func (p *Provider) AuthorizeURL(state, nonce string) (string, error) {
	doc, err := p.getDiscovery()
	if err != nil {
		return "", err
	}
	q := url.Values{
		"response_type": {"code"},
		"client_id":     {p.cfg.ClientID},
		"redirect_uri":  {p.cfg.RedirectURL},
		"scope":         {"openid email profile"},
		"state":         {state},
		"nonce":         {nonce},
	}
	return doc.AuthorizationEndpoint + "?" + q.Encode(), nil
}

type tokenResponse struct {
	IDToken string `json:"id_token"`
}

// Exchange trades an authorization code for an ID token.
func (p *Provider) Exchange(code string) (string, error) {
	doc, err := p.getDiscovery()
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.cfg.RedirectURL},
		"client_id":     {p.cfg.ClientID},
		"client_secret": {p.cfg.ClientSecret},
	}
	resp, err := p.client.PostForm(doc.TokenEndpoint, form)
	if err != nil {
		return "", fmt.Errorf("oidc token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oidc token: status %d", resp.StatusCode)
	}
	var tok tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&tok); err != nil {
		return "", fmt.Errorf("oidc token: %w", err)
	}
	if tok.IDToken == "" {
		return "", fmt.Errorf("oidc token: missing id_token")
	}
	return tok.IDToken, nil
}

// Claims are the verified ID-token claims DByson relies on.
type Claims struct {
	Subject           string
	Email             string
	EmailVerified     bool
	PreferredUsername string
	Name              string
}

type rawClaims struct {
	Issuer            string   `json:"iss"`
	Audience          audience `json:"aud"`
	Subject           string   `json:"sub"`
	Expiry            int64    `json:"exp"`
	IssuedAt          int64    `json:"iat"`
	Nonce             string   `json:"nonce"`
	Email             string   `json:"email"`
	EmailVerified     bool     `json:"email_verified"`
	PreferredUsername string   `json:"preferred_username"`
	Name              string   `json:"name"`
}

type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var single string
	if err := json.Unmarshal(b, &single); err == nil {
		*a = []string{single}
		return nil
	}
	var multi []string
	if err := json.Unmarshal(b, &multi); err != nil {
		return err
	}
	*a = multi
	return nil
}

// Verify checks the ID token signature (RS256 via the provider JWKS) and its
// iss/aud/exp claims, and binds it to the nonce from the login request.
func (p *Provider) Verify(rawToken, nonce string) (*Claims, error) {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("oidc: malformed token")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeSegment(parts[0], &header); err != nil {
		return nil, fmt.Errorf("oidc: bad header: %w", err)
	}
	if header.Alg != "RS256" {
		return nil, fmt.Errorf("oidc: unexpected algorithm %q", header.Alg)
	}
	if header.Kid == "" {
		return nil, fmt.Errorf("oidc: missing key id")
	}
	key, err := p.signingKey(header.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("oidc: bad signature: %w", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return nil, fmt.Errorf("oidc: invalid signature")
	}
	var claims rawClaims
	if err := decodeSegment(parts[1], &claims); err != nil {
		return nil, fmt.Errorf("oidc: bad claims: %w", err)
	}
	now := time.Now().Unix()
	if claims.Issuer != p.cfg.Issuer {
		return nil, fmt.Errorf("oidc: issuer mismatch")
	}
	audOK := false
	for _, a := range claims.Audience {
		if a == p.cfg.ClientID {
			audOK = true
		}
	}
	if !audOK {
		return nil, fmt.Errorf("oidc: audience mismatch")
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("oidc: missing subject")
	}
	if claims.Expiry == 0 || now > claims.Expiry+30 {
		return nil, fmt.Errorf("oidc: token expired")
	}
	if claims.IssuedAt != 0 && claims.IssuedAt > now+300 {
		return nil, fmt.Errorf("oidc: token issued in the future")
	}
	if nonce != "" && claims.Nonce != nonce {
		return nil, fmt.Errorf("oidc: nonce mismatch")
	}
	return &Claims{
		Subject: claims.Subject, Email: strings.TrimSpace(claims.Email),
		EmailVerified:     claims.EmailVerified,
		PreferredUsername: strings.TrimSpace(claims.PreferredUsername),
		Name:              strings.TrimSpace(claims.Name),
	}, nil
}

func decodeSegment(seg string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	if len(raw) > 32*1024 {
		return fmt.Errorf("segment too large")
	}
	return json.Unmarshal(raw, v)
}

type jwksDoc struct {
	Keys []struct {
		Type string `json:"kty"`
		Kid  string `json:"kid"`
		Alg  string `json:"alg"`
		N    string `json:"n"`
		E    string `json:"e"`
	} `json:"keys"`
}

func (p *Provider) signingKey(kid string) (*rsa.PublicKey, error) {
	p.mu.Lock()
	cached, exp := p.keys, p.keysExp
	p.mu.Unlock()
	if cached != nil && time.Now().Before(exp) {
		if k := cached[kid]; k != nil {
			return k, nil
		}
		return nil, fmt.Errorf("oidc: unknown key id %q", kid)
	}
	doc, err := p.getDiscovery()
	if err != nil {
		return nil, err
	}
	keys, err := p.fetchJWKS(doc.JWKSURI)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.keys, p.keysExp = keys, time.Now().Add(time.Hour)
	p.mu.Unlock()
	if k := keys[kid]; k != nil {
		return k, nil
	}
	return nil, fmt.Errorf("oidc: unknown key id %q", kid)
}

func (p *Provider) fetchJWKS(jwksURI string) (map[string]*rsa.PublicKey, error) {
	resp, err := p.client.Get(jwksURI)
	if err != nil {
		return nil, fmt.Errorf("oidc jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc jwks: status %d", resp.StatusCode)
	}
	var jwks jwksDoc
	if err := json.NewDecoder(io.LimitReader(resp.Body, 128*1024)).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("oidc jwks: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range jwks.Keys {
		if k.Type != "RSA" || k.Kid == "" {
			continue
		}
		nb, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eb, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		e := 0
		for _, b := range eb {
			e = e<<8 + int(b)
		}
		if e == 0 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("oidc jwks: no RSA keys")
	}
	return keys, nil
}
