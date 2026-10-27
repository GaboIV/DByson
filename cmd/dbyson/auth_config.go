package main

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/gaboiv/dbyson/internal/config"
	"github.com/gaboiv/dbyson/internal/sso"
)

func authFlag(name string, fallback bool) (bool, error) {
	raw := config.Get(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("DBYSON_%s must be true or false", name)
	}
	return value, nil
}

// loadAuthConfig keeps image configuration independent of the deployment host.
func loadAuthConfig() (sso.Config, bool, error) {
	credentials, err := authFlag("CREDENTIALS_ENABLED", true)
	if err != nil {
		return sso.Config{}, false, err
	}
	enabled, err := authFlag("OIDC_ENABLED", true)
	if err != nil {
		return sso.Config{}, false, err
	}
	cfg := sso.Config{}
	if enabled {
		secret, err := config.Secret("OIDC_CLIENT_SECRET")
		if err != nil {
			return cfg, false, err
		}
		cfg = sso.Config{
			Issuer: config.Get("OIDC_ISSUER"), ClientID: config.Get("OIDC_CLIENT_ID"), ClientSecret: secret,
			RedirectURL: config.Get("OIDC_REDIRECT_URL"), ProviderName: config.Get("OIDC_NAME"),
			AutoProvision: envBool("OIDC_AUTO_PROVISION"), AllowedDomains: sso.ParseDomains(config.Get("OIDC_ALLOWED_DOMAINS")),
			TrustMFA: envBool("OIDC_TRUST_MFA"),
		}
		configured := cfg.Issuer != "" || cfg.ClientID != "" || cfg.ClientSecret != ""
		if configured && !cfg.Enabled() {
			return cfg, false, fmt.Errorf("SSO requires issuer, client ID and client secret")
		}
		if cfg.Enabled() {
			u, err := url.Parse(cfg.RedirectURL)
			if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
				return cfg, false, fmt.Errorf("DBYSON_OIDC_REDIRECT_URL must be an absolute HTTPS callback URL (HTTP only for localhost)")
			}
		}
		if err := cfg.Validate(); err != nil {
			return cfg, false, err
		}
	}
	if !credentials && !cfg.Enabled() {
		return cfg, false, fmt.Errorf("disabling password sign-in requires a configured SSO provider")
	}
	return cfg, !credentials, nil
}
