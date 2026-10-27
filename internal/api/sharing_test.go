package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestSharingVisibilityAndDirectory(t *testing.T) {
	e := newTestEnv(t)
	admin := e.client()
	e.setupAdmin(admin)
	createUser(t, admin, "manager", "manager-password-123", RoleMember)
	createUser(t, admin, "reader", "reader-password-123", RoleMember)
	createUser(t, admin, "viewer", "viewer-password-123", RoleViewer)
	manager := loginAs(t, e, "manager", "manager-password-123")
	reader := loginAs(t, e, "reader", "reader-password-123")
	viewer := loginAs(t, e, "viewer", "viewer-password-123")
	c := createConn(t, admin, "sharing-ui", "dev", nil)
	base := "/api/connections/" + c.ID

	// Sharing accepts only an explicit boolean, and only from administration.
	for _, body := range []map[string]any{{}, {"shared": "yes"}, {"shared": nil}} {
		if rec := admin.do("PUT", base+"/sharing", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid visibility: %d %s", rec.Code, rec.Body.String())
		}
	}
	if rec := manager.do("PUT", base+"/sharing", map[string]bool{"shared": true}); rec.Code != http.StatusForbidden {
		t.Fatalf("member enabled sharing: %d", rec.Code)
	}
	before, err := e.store.DB.Query(`SELECT wrapped_dek, secret FROM connections WHERE id = ?`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var wrapped, sealed []byte
	if !before.Next() {
		t.Fatal("missing connection")
	}
	if err := before.Scan(&wrapped, &sealed); err != nil {
		t.Fatal(err)
	}
	before.Close()
	if rec := admin.do("PUT", base+"/sharing", map[string]bool{"shared": true}); rec.Code != http.StatusOK {
		t.Fatalf("enable sharing: %d %s", rec.Code, rec.Body.String())
	}
	var nextWrapped, nextSealed []byte
	if err := e.store.DB.QueryRow(`SELECT wrapped_dek, secret FROM connections WHERE id = ?`, c.ID).Scan(&nextWrapped, &nextSealed); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wrapped, nextWrapped) || !bytes.Equal(sealed, nextSealed) {
		t.Fatal("sharing changed credentials")
	}

	for name, level := range map[string]string{"manager": GrantManage, "reader": GrantUse, "viewer": GrantManage} {
		if rec := admin.do("POST", base+"/grants", map[string]any{"userId": userID(t, e, admin, name), "level": level}); rec.Code != http.StatusCreated {
			t.Fatalf("grant %s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	for _, tc := range []struct {
		c      *client
		status int
		access string
	}{
		{admin, http.StatusOK, "owner"}, {manager, http.StatusOK, GrantManage},
		{reader, http.StatusNotFound, GrantUse}, {viewer, http.StatusNotFound, GrantUse},
	} {
		if rec := tc.c.do("GET", base+"/grants/users", nil); rec.Code != tc.status {
			t.Fatalf("directory: got %d want %d", rec.Code, tc.status)
		}
		for _, path := range []string{base, "/api/connections"} {
			rec := tc.c.do("GET", path, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("access view: %d", rec.Code)
			}
			var v connView
			if path == base {
				err = json.Unmarshal(rec.Body.Bytes(), &v)
			} else {
				var views []connView
				err = json.Unmarshal(rec.Body.Bytes(), &views)
				for _, item := range views {
					if item.ID == c.ID {
						v = item
					}
				}
			}
			if err != nil || v.Access != tc.access {
				t.Fatalf("access got %q want %q: %v", v.Access, tc.access, err)
			}
		}
	}
	// A connection manager gets a minimal directory, never account management.
	directory := manager.do("GET", base+"/grants/users", nil)
	var people []map[string]any
	if err := json.Unmarshal(directory.Body.Bytes(), &people); err != nil {
		t.Fatal(err)
	}
	if len(people) != 3 {
		t.Fatalf("candidates: %s", directory.Body.String())
	}
	for _, person := range people {
		if len(person) != 3 {
			t.Fatalf("directory leaked account fields: %v", person)
		}
	}
	if rec := manager.do("GET", "/api/users", nil); rec.Code != http.StatusForbidden {
		t.Fatal("manager can administer accounts")
	}
	if rec := viewer.do("POST", base+"/grants", map[string]any{"userId": userID(t, e, admin, "reader"), "level": GrantManage}); rec.Code != http.StatusNotFound {
		t.Fatal("viewer can manage grants")
	}
	if rec := viewer.do("GET", base+"/grants", nil); rec.Code != http.StatusNotFound {
		t.Fatal("viewer can inspect grants")
	}
	if rec := admin.do("PUT", base+"/sharing", map[string]bool{"shared": false}); rec.Code != http.StatusOK {
		t.Fatal("could not stop sharing")
	}
	if rec := manager.do("GET", base, nil); rec.Code != http.StatusNotFound {
		t.Fatal("revoked access remains")
	}
	if rec := admin.do("GET", base+"/grants", nil); rec.Body.String() != "[]\n" {
		t.Fatalf("grants remain: %s", rec.Body.String())
	}
	if rec := admin.do("PUT", base+"/sharing", map[string]bool{"shared": true}); rec.Code != http.StatusOK {
		t.Fatal("could not reenable sharing")
	}
	if rec := manager.do("GET", base, nil); rec.Code != http.StatusNotFound {
		t.Fatal("reenabling resurrected grants")
	}
	if rec := admin.do("PUT", "/api/connections/missing/sharing", map[string]bool{"shared": true}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing connection: %d", rec.Code)
	}
}

// A manage grant lets someone edit a shared profile in place, not carry its
// stored credentials off in an export. Only the owner or an admin may export
// secrets.
func TestExportSecretsRequiresOwnerOrAdmin(t *testing.T) {
	e := newTestEnv(t)
	admin := e.client()
	e.setupAdmin(admin)
	createUser(t, admin, "manager", "manager-password-123", RoleMember)
	manager := loginAs(t, e, "manager", "manager-password-123")

	create := admin.do("POST", "/api/connections", map[string]any{
		"name": "team-db", "env": "dev", "host": "localhost", "username": "dbuser",
		"password": "SuperSecretPw!", "tlsMode": "disabled", "shared": true,
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Code, create.Body.String())
	}
	var conn connView
	_ = json.Unmarshal(create.Body.Bytes(), &conn)
	if rec := admin.do("POST", "/api/connections/"+conn.ID+"/grants",
		map[string]any{"userId": userID(t, e, admin, "manager"), "level": GrantManage}); rec.Code != http.StatusCreated {
		t.Fatalf("grant manage: %d %s", rec.Code, rec.Body.String())
	}

	exportBody := func(withSecrets bool) map[string]any {
		b := map[string]any{"ids": []string{conn.ID}, "includeSecrets": withSecrets}
		if withSecrets {
			b["passphrase"] = "a-strong-enough-passphrase"
		}
		return b
	}

	// The manage grantee may export the profile, but not its secrets.
	if rec := manager.do("POST", "/api/connections/transfer/export", exportBody(true)); rec.Code != http.StatusForbidden {
		t.Fatalf("manage grantee exported secrets: %d %s", rec.Code, rec.Body.String())
	}
	plain := manager.do("POST", "/api/connections/transfer/export", exportBody(false))
	if plain.Code != http.StatusOK {
		t.Fatalf("manage grantee could not export the profile: %d %s", plain.Code, plain.Body.String())
	}
	if bytes.Contains(plain.Body.Bytes(), []byte("SuperSecretPw!")) {
		t.Fatal("password leaked in a secret-free export")
	}

	// The owner/admin still can export secrets.
	if rec := admin.do("POST", "/api/connections/transfer/export", exportBody(true)); rec.Code != http.StatusOK {
		t.Fatalf("admin could not export secrets: %d %s", rec.Code, rec.Body.String())
	}
}
