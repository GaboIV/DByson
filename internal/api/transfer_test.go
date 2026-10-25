package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gaboiv/dbyson/internal/auth"
	"github.com/gaboiv/dbyson/internal/tunnel"
)

func TestProfileTransfer(t *testing.T) {
	env := newTestEnv(t)
	c := env.client()
	env.setupAdmin(c)
	create := c.do("POST", "/api/connections", map[string]any{"name": "shared", "host": "localhost", "username": "dbuser", "password": "mysql-top-secret", "tlsMode": "disabled", "ssh": map[string]any{"enabled": false, "host": "ssh.local", "port": 22, "user": "tester", "auth": "key", "keyLabel": "team.ppk", "keyFile": "team/team.ppk", "privateKey": "private-test-only", "passphrase": "key-secret"}})
	if create.Code != 201 {
		t.Fatal(create.Body.String())
	}
	var original connView
	json.Unmarshal(create.Body.Bytes(), &original)
	if strings.Contains(create.Body.String(), "private-test-only") || strings.Contains(create.Body.String(), "key-secret") {
		t.Fatal("connection API leaked SSH secret")
	}
	for _, encrypted := range []bool{false, true} {
		exported := c.do("POST", "/api/connections/transfer/export", map[string]any{"ids": []string{original.ID}, "includeSecrets": encrypted, "passphrase": "package-passphrase"})
		if exported.Code != 200 {
			t.Fatal(exported.Body.String())
		}
		var response struct{ File string }
		json.Unmarshal(exported.Body.Bytes(), &response)
		if strings.Contains(response.File, "mysql-top-secret") || strings.Contains(response.File, "private-test-only") || strings.Contains(response.File, "key-secret") {
			t.Fatal("download exposed credentials")
		}
		if encrypted {
			failed := c.do("POST", "/api/connections/transfer/import", map[string]any{"file": response.File, "passphrase": "wrong"})
			if failed.Code != 400 {
				t.Fatal("wrong phrase imported")
			}
			var damaged profilePackage
			json.Unmarshal([]byte(response.File), &damaged)
			damaged.Data = "AAAA" + damaged.Data[4:]
			bad, _ := json.Marshal(damaged)
			if c.do("POST", "/api/connections/transfer/import", map[string]any{"file": string(bad), "passphrase": "package-passphrase"}).Code != 400 {
				t.Fatal("tampered package imported")
			}
		}
		imported := c.do("POST", "/api/connections/transfer/import", map[string]any{"file": response.File, "passphrase": "package-passphrase"})
		if imported.Code != 201 {
			t.Fatal(imported.Body.String())
		}
		var views []connView
		json.Unmarshal(imported.Body.Bytes(), &views)
		if len(views) != 1 || views[0].ID == original.ID || views[0].HasPassword != encrypted {
			t.Fatalf("incorrect import: %s", imported.Body.String())
		}
		again := c.do("POST", "/api/connections/transfer/export", map[string]any{"ids": []string{views[0].ID}})
		var copied struct{ File string }
		json.Unmarshal(again.Body.Bytes(), &copied)
		var pkg profilePackage
		json.Unmarshal([]byte(copied.File), &pkg)
		if pkg.Profiles[0].SSH.KeyFile != "team/team.ppk" || pkg.Profiles[0].SSH.KeyLabel != "team.ppk" || pkg.Profiles[0].SSH.HasPrivateKey {
			t.Fatal("key reference lost or embedded key imported")
		}
	}
	// Malformed multi-profile imports are atomic, and plain files cannot inject secrets.
	var before int
	env.store.DB.QueryRow(`SELECT count(*) FROM connections`).Scan(&before)
	pkg := profilePackage{Format: profileFormat, Version: 1, Profiles: []connInput{{Name: "valid", Host: "localhost", Username: "user", TLSMode: "disabled"}, {Name: "bad", Host: "", Username: "user"}}}
	data, _ := json.Marshal(pkg)
	if c.do("POST", "/api/connections/transfer/import", map[string]any{"file": string(data)}).Code != 400 {
		t.Fatal("invalid profile imported")
	}
	var after int
	env.store.DB.QueryRow(`SELECT count(*) FROM connections`).Scan(&after)
	if before != after {
		t.Fatal("partial import left rows behind")
	}
	pkg.Profiles = pkg.Profiles[:1]
	password := "plaintext"
	pkg.Profiles[0].Password = &password
	data, _ = json.Marshal(pkg)
	if c.do("POST", "/api/connections/transfer/import", map[string]any{"file": string(data)}).Code != 400 {
		t.Fatal("plain secret allowed")
	}
	hash, _ := auth.HashPassword("another-long-password")
	env.store.DB.Exec(`INSERT INTO users(username,password_hash,role,created_at)VALUES('other',?,'member',0)`, hash)
	other := env.client()
	other.loginFrom(other.do("POST", "/api/auth/login", map[string]string{"username": "other", "password": "another-long-password"}))
	if other.do("POST", "/api/connections/transfer/export", map[string]any{"ids": []string{original.ID}}).Code != 404 {
		t.Fatal("ungranted member could export credentials")
	}
	// Central administration: admins see every profile, members only their own.
	env.store.DB.Exec(`UPDATE users SET role = 'admin' WHERE username = 'other'`)
	other.loginFrom(other.do("POST", "/api/auth/login", map[string]string{"username": "other", "password": "another-long-password"}))
	if other.do("POST", "/api/connections/transfer/export", map[string]any{"ids": []string{original.ID}}).Code != 200 {
		t.Fatal("admin could not export a profile under central administration")
	}
}

func TestNewRoutesRequireSessionAndCSRF(t *testing.T) {
	env := newTestEnv(t)
	anon := env.client()
	for _, path := range []string{"/api/discovery", "/api/ssh/fingerprint", "/api/connections/transfer/export", "/api/connections/transfer/import", "/api/connections/id/test-draft", "/api/connections/id/ssh/fingerprint"} {
		if anon.do("POST", path, map[string]any{}).Code != 401 {
			t.Fatal("missing authentication", path)
		}
	}
	c := env.client()
	env.setupAdmin(c)
	c.csrf = ""
	if c.do("POST", "/api/discovery", map[string]any{}).Code != 403 {
		t.Fatal("missing CSRF protection")
	}
}

func TestSSHSecretMergeAndRedaction(t *testing.T) {
	prev := tunnel.Config{Hop: tunnel.Hop{PrivateKey: "old", Passphrase: "oldphrase", KeyLabel: "same.ppk"}, Jump: &tunnel.Hop{Password: "jump-secret"}}
	got := tunnel.Merge(tunnel.Config{Hop: tunnel.Hop{KeyLabel: "same.ppk"}, Jump: &tunnel.Hop{}}, prev)
	if got.PrivateKey != "old" || got.Passphrase != "oldphrase" || got.Jump.Password != "jump-secret" {
		t.Fatal("editing erased credentials")
	}
	replacement := tunnel.Merge(tunnel.Config{Hop: tunnel.Hop{KeyLabel: "same.ppk", PrivateKey: "new"}}, prev)
	if replacement.Passphrase != "" {
		t.Fatal("new key inherited old passphrase")
	}
	data, _ := json.Marshal(got.Redacted())
	if strings.Contains(string(data), "oldphrase") || strings.Contains(string(data), "jump-secret") {
		t.Fatal("redaction leaked hop secret")
	}
}
