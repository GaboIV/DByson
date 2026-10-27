package api

import (
	"net/http"
	"testing"
)

// Discovery reveals the server's own network interfaces, so the read-only
// viewer role must not reach it. The check runs before any scanning, so the
// test does not depend on the network.
func TestDiscoveryDeniedToViewers(t *testing.T) {
	e := newTestEnv(t)
	admin := e.client()
	e.setupAdmin(admin)
	createUser(t, admin, "viewer1", "viewer-password-123", RoleViewer)
	viewer := loginAs(t, e, "viewer1", "viewer-password-123")

	if rec := viewer.do("POST", "/api/discovery", map[string]any{}); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer reached discovery: %d %s", rec.Code, rec.Body.String())
	}
}
