package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecret(t *testing.T) {
	t.Setenv("DBYSON_OIDC_CLIENT_SECRET", "")
	t.Setenv("DBYSON_OIDC_CLIENT_SECRET_FILE", "")
	if value, err := Secret("OIDC_CLIENT_SECRET"); err != nil || value != "" {
		t.Fatalf("disabled secret: %q %v", value, err)
	}
	t.Setenv("DBYSON_OIDC_CLIENT_SECRET", "env-secret")
	if value, err := Secret("OIDC_CLIENT_SECRET"); err != nil || value != "env-secret" {
		t.Fatal("environment secret not read")
	}
	path := filepath.Join(t.TempDir(), "oauth-secret")
	if err := os.WriteFile(path, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBYSON_OIDC_CLIENT_SECRET_FILE", path)
	if _, err := Secret("OIDC_CLIENT_SECRET"); err == nil {
		t.Fatal("ambiguous secret sources accepted")
	}
	t.Setenv("DBYSON_OIDC_CLIENT_SECRET", "")
	if value, err := Secret("OIDC_CLIENT_SECRET"); err != nil || value != "file-secret" {
		t.Fatal("file secret not read")
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Secret("OIDC_CLIENT_SECRET"); err == nil {
		t.Fatal("empty secret accepted")
	}
	t.Setenv("DBYSON_OIDC_CLIENT_SECRET_FILE", path+"-missing")
	if _, err := Secret("OIDC_CLIENT_SECRET"); err == nil || strings.Contains(err.Error(), "file-secret") {
		t.Fatal("invalid secret error")
	}
}
