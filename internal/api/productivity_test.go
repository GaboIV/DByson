package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gaboiv/dbyson/internal/auth"
)

func TestSavedQueriesEncryptedAndPrivate(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	created := c.do("POST", "/api/connections", map[string]any{"name": "library", "host": "127.0.0.1", "username": "demo", "tlsMode": "disabled"})
	var conn connView
	if err := json.Unmarshal(created.Body.Bytes(), &conn); err != nil || conn.ID == "" {
		t.Fatal(created.Body.String())
	}
	path := "/api/connections/" + conn.ID + "/saved-queries"
	res := c.do("POST", path, map[string]string{"name": "private", "database": "demo", "query": "SELECT 'sensitive-query-value'"})
	if res.Code != 201 {
		t.Fatal(res.Code, res.Body.String())
	}
	var saved savedQuery
	json.Unmarshal(res.Body.Bytes(), &saved)
	var raw, wrapped []byte
	if err := e.store.DB.QueryRow(`SELECT secret,wrapped_dek FROM saved_queries WHERE id=?`, saved.ID).Scan(&raw, &wrapped); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("sensitive-query-value")) || len(wrapped) == 0 {
		t.Fatal("SQL stored in plaintext")
	}
	res = c.do("GET", path, nil)
	if res.Code != 200 || !strings.Contains(res.Body.String(), "sensitive-query-value") {
		t.Fatal(res.Code, res.Body.String())
	}
	result, err := e.store.DB.Exec(`INSERT INTO users(username,password_hash,role,created_at) VALUES('other','unused','member',0)`)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := result.LastInsertId()
	token, csrf, err := auth.NewSessions(e.store.DB).Create(context.Background(), uid, false)
	if err != nil {
		t.Fatal(err)
	}
	foreign := e.client()
	foreign.cookie = &http.Cookie{Name: cookieName, Value: token}
	foreign.csrf = csrf
	for _, method := range []string{"GET", "POST", "DELETE"} {
		target := path
		var body any
		if method == "POST" {
			body = map[string]string{"name": "x", "query": "SELECT 1"}
		}
		if method == "DELETE" {
			target += "/" + saved.ID
		}
		if r := foreign.do(method, target, body); r.Code != 404 {
			t.Fatalf("cross-owner %s: %d %s", method, r.Code, r.Body.String())
		}
	}
	old := c.csrf
	c.csrf = ""
	if res = c.do("DELETE", path+"/"+saved.ID, nil); res.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	c.csrf = old
	if res = c.do("DELETE", path+"/"+saved.ID, nil); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	if res = c.do("GET", path, nil); res.Body.String() != "[]\n" {
		t.Fatal("query not removed", res.Body.String())
	}
}

func TestProductivityWritesRespectReadOnly(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	created := c.do("POST", "/api/connections", map[string]any{"name": "read-only", "host": "127.0.0.1", "username": "demo", "tlsMode": "disabled"})
	var conn connView
	json.Unmarshal(created.Body.Bytes(), &conn)
	for _, test := range []struct {
		path string
		body any
	}{
		{"/databases/demo/tables/t/import-csv", map[string]any{"columns": []string{"id"}, "rows": [][]string{{"1"}}}},
		{"/processes/kill", map[string]string{"thread": "123"}},
		{"/mysql-accounts", map[string]string{"action": "drop", "user": "demo", "host": "localhost"}},
	} {
		raw, _ := json.Marshal(test.body)
		r := httptest.NewRequest("POST", "/api/connections/"+conn.ID+test.path, bytes.NewReader(raw))
		r.AddCookie(c.cookie)
		r.Header.Set("X-CSRF-Token", c.csrf)
		r.Header.Set("X-DByson-Read-Only", "1")
		w := httptest.NewRecorder()
		e.handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("%s: %d %s", test.path, w.Code, w.Body.String())
		}
	}
	for _, thread := range []string{"1; DROP DATABASE demo", "-1", "", "123456789012345678901"} {
		res := c.do("POST", "/api/connections/"+conn.ID+"/processes/kill", map[string]string{"thread": thread})
		if res.Code != 400 {
			t.Fatalf("thread injection: %d", res.Code)
		}
	}
}
