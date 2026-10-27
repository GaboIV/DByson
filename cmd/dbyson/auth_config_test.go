package main

import "testing"

func TestLoadAuthConfig(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		values                           map[string]string
		enabled, passwordDisabled, fails bool
	}{
		{name: "personal defaults"},
		{name: "google only", values: map[string]string{"CREDENTIALS_ENABLED": "false", "OIDC_ENABLED": "true", "OIDC_ISSUER": "https://accounts.google.com", "OIDC_CLIENT_ID": "fixture-client", "OIDC_CLIENT_SECRET": "fixture-secret", "OIDC_REDIRECT_URL": "https://dbyson.example.com/api/auth/oidc/callback", "OIDC_AUTO_PROVISION": "true", "OIDC_ALLOWED_DOMAINS": "empresa.com,otraempresa.com"}, enabled: true, passwordDisabled: true},
		{name: "lockout prevented", values: map[string]string{"CREDENTIALS_ENABLED": "false", "OIDC_ENABLED": "false"}, fails: true},
		{name: "incomplete Google", values: map[string]string{"OIDC_ISSUER": "https://accounts.google.com"}, fails: true},
		{name: "invalid flag", values: map[string]string{"CREDENTIALS_ENABLED": "fasle"}, fails: true},
		{name: "SSO explicitly off", values: map[string]string{"OIDC_ENABLED": "false", "OIDC_CLIENT_SECRET_FILE": "/missing/secret"}},
		{name: "insecure callback", values: map[string]string{"OIDC_ISSUER": "https://accounts.google.com", "OIDC_CLIENT_ID": "fixture-client", "OIDC_CLIENT_SECRET": "fixture-secret", "OIDC_REDIRECT_URL": "http://dbyson.example.com/api/auth/oidc/callback"}, fails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"CREDENTIALS_ENABLED", "OIDC_ENABLED", "OIDC_ISSUER", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "OIDC_CLIENT_SECRET_FILE", "OIDC_REDIRECT_URL", "OIDC_NAME", "OIDC_AUTO_PROVISION", "OIDC_ALLOWED_DOMAINS", "OIDC_TRUST_MFA"} {
				t.Setenv("DBYSON_"+key, tc.values[key])
			}
			cfg, disabled, err := loadAuthConfig()
			if (err != nil) != tc.fails {
				t.Fatalf("unexpected config result: %v", err)
			}
			if err == nil && (cfg.Enabled() != tc.enabled || disabled != tc.passwordDisabled) {
				t.Fatalf("wrong flags: SSO=%v passwordDisabled=%v", cfg.Enabled(), disabled)
			}
		})
	}
}
