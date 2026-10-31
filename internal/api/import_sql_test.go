package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImportSQLConfirmsTheExactFile(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	var cv connView
	_ = json.Unmarshal(c.do("POST", "/api/connections", map[string]any{"name": "imp", "host": "127.0.0.1", "username": "u"}).Body.Bytes(), &cv)

	script := []byte("CREATE TABLE t (id int);\nINSERT INTO t VALUES (1);\n")
	sum := sha256.Sum256(script)
	digest := hex.EncodeToString(sum[:])
	path := fmt.Sprintf("/api/connections/%s/databases/shop/import-sql?sha256=%s&name=dump.sql&size=%d", cv.ID, digest, len(script))

	// Step 1: no body, no token -> a challenge bound to this database and digest.
	rec := c.do("POST", path, nil)
	var challenge struct {
		NeedsConfirmation  bool
		ConfirmationToken  string
		ConfirmationTarget string
	}
	json.Unmarshal(rec.Body.Bytes(), &challenge)
	if rec.Code != http.StatusOK || !challenge.NeedsConfirmation || challenge.ConfirmationTarget != "shop" {
		t.Fatalf("expected a confirmation challenge: %d %s", rec.Code, rec.Body.String())
	}

	upload := func(body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, bytes.NewReader(body))
		req.AddCookie(c.cookie)
		req.Header.Set("X-CSRF-Token", c.csrf)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("X-DByson-Confirmation-Token", challenge.ConfirmationToken)
		req.Header.Set("X-DByson-Confirmation-Name", challenge.ConfirmationTarget)
		out := httptest.NewRecorder()
		e.handler.ServeHTTP(out, req)
		return out
	}
	// Step 2 with a different file of the same size is refused before anything runs.
	tampered := bytes.Replace(script, []byte("(1)"), []byte("(2)"), 1)
	if rec := upload(tampered); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no coincide") {
		t.Fatalf("a file other than the confirmed one must be rejected: %d %s", rec.Code, rec.Body.String())
	}

	// Read-only connections never import.
	var ro connView
	_ = json.Unmarshal(c.do("POST", "/api/connections", map[string]any{"name": "ro", "host": "127.0.0.1", "username": "u", "readOnly": true}).Body.Bytes(), &ro)
	if rec := c.do("POST", strings.Replace(path, cv.ID, ro.ID, 1), nil); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only connection must refuse imports, got %d", rec.Code)
	}
	if rec := c.do("POST", strings.Replace(path, "sha256="+digest, "sha256=nothex", 1), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid digest must be rejected, got %d", rec.Code)
	}
}
