package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestViewOnlyRequestBlocksWritesWithoutChangingProfile(t *testing.T) {
	env := newTestEnv(t)
	c := env.client()
	env.setupAdmin(c)
	created := c.do("POST", "/api/connections", map[string]any{"name": "editable", "host": "127.0.0.1", "username": "demo", "tlsMode": "disabled"})
	var connection connView
	json.Unmarshal(created.Body.Bytes(), &connection)
	base := "/api/connections/" + connection.ID
	for _, test := range []struct {
		method, path string
		body         any
	}{
		{"POST", base + "/query", map[string]any{"query": "DELETE FROM orders", "database": "demo"}},
		{"POST", base + "/databases/create", map[string]any{"name": "demo"}},
		{"POST", base + "/databases/demo/tables/orders/rows", map[string]any{"values": map[string]any{"id": 1}}},
		{"POST", base + "/databases/demo/tables/orders/truncate", map[string]any{}},
	} {
		data, _ := json.Marshal(test.body)
		req := httptest.NewRequest(test.method, test.path, bytes.NewReader(data))
		req.AddCookie(c.cookie)
		req.Header.Set("X-CSRF-Token", c.csrf)
		req.Header.Set("X-DByson-Read-Only", "1")
		res := httptest.NewRecorder()
		env.handler.ServeHTTP(res, req)
		if res.Code != 403 {
			t.Fatalf("%s: expected read-only block, got %d %s", test.path, res.Code, res.Body.String())
		}
	}
	var readOnly int
	if err := env.store.DB.QueryRow(`SELECT read_only FROM connections WHERE id=?`, connection.ID).Scan(&readOnly); err != nil || readOnly != 0 {
		t.Fatal("preference changed persisted profile", err)
	}
}
