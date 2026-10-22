package api

import (
	"context"
	"encoding/json"
	"github.com/adminbase/adminbase/internal/auth"
	"net/http/httptest"
	"testing"
	"time"
)

func TestConfirmationIsBoundExpiringAndSingleUse(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("POST", "/api/connections/id/query", nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, &auth.Session{CSRF: "session-one"}))
	issue := func() confirmationInput {
		w := httptest.NewRecorder()
		if s.requireConfirmation(w, r, "DROP\nDROP TABLE t", "db.t", "drops table", "DROP TABLE t", confirmationInput{}) {
			t.Fatal("executed before confirmation")
		}
		var challenge struct {
			Token string `json:"confirmationToken"`
		}
		json.Unmarshal(w.Body.Bytes(), &challenge)
		if challenge.Token == "" {
			t.Fatal(w.Body.String())
		}
		return confirmationInput{challenge.Token, "db.t"}
	}
	in := issue()
	if s.requireConfirmation(httptest.NewRecorder(), r, "DROP\nDROP TABLE changed", "db.t", "", "", in) {
		t.Fatal("changed SQL accepted")
	}
	in = issue()
	other := r.WithContext(context.WithValue(r.Context(), ctxKey{}, &auth.Session{CSRF: "session-two"}))
	if s.requireConfirmation(httptest.NewRecorder(), other, "DROP\nDROP TABLE t", "db.t", "", "", in) {
		t.Fatal("other session accepted")
	}
	in = issue()
	wrong := in
	wrong.ConfirmationName = "different"
	if s.requireConfirmation(httptest.NewRecorder(), r, "DROP\nDROP TABLE t", "db.t", "", "", wrong) {
		t.Fatal("wrong name accepted")
	}
	if !s.requireConfirmation(httptest.NewRecorder(), r, "DROP\nDROP TABLE t", "db.t", "", "", in) {
		t.Fatal("valid confirmation rejected")
	}
	if s.requireConfirmation(httptest.NewRecorder(), r, "DROP\nDROP TABLE t", "db.t", "", "", in) {
		t.Fatal("replay accepted")
	}
	in = issue()
	value, _ := s.confirmations.Load(in.ConfirmationToken)
	expired := value.(confirmationChallenge)
	expired.expires = time.Now().Add(-time.Second)
	s.confirmations.Store(in.ConfirmationToken, expired)
	if s.requireConfirmation(httptest.NewRecorder(), r, "DROP\nDROP TABLE t", "db.t", "", "", in) {
		t.Fatal("expired confirmation accepted")
	}
}

func TestDestructiveRoutesCannotSkipConfirmation(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	response := c.do("POST", "/api/connections", map[string]any{"name": "test", "host": "127.0.0.1", "username": "u"})
	var connection connView
	json.Unmarshal(response.Body.Bytes(), &connection)
	base := "/api/connections/" + connection.ID
	for _, test := range []struct {
		method, path string
		body         map[string]any
	}{
		{"POST", base + "/query", map[string]any{"query": "DROP TABLE t", "confirmDestructive": true}},
		{"POST", base + "/databases/drop", map[string]any{"name": "db"}},
		{"POST", base + "/databases/db/tables/t/drop", map[string]any{}},
		{"POST", base + "/databases/db/tables/t/truncate", map[string]any{}},
		{"DELETE", base + "/databases/db/tables/t/rows", map[string]any{"pk": map[string]any{"id": 1}}},
		{"POST", base + "/databases/db/tables/t/batch-delete", map[string]any{"pks": []map[string]any{{"id": 1}}}},
	} {
		response := c.do(test.method, test.path, test.body)
		var result struct {
			Needs bool `json:"needsConfirmation"`
		}
		json.Unmarshal(response.Body.Bytes(), &result)
		if response.Code != 200 || !result.Needs {
			t.Errorf("%s: %d %s", test.path, response.Code, response.Body.String())
		}
	}
}

func TestReadOnlyConsoleDeniesUnsafeStatementsWithoutDialing(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.setupAdmin(c)
	response := c.do("POST", "/api/connections", map[string]any{"name": "ro", "host": "127.0.0.1", "username": "u", "readOnly": true})
	var connection connView
	json.Unmarshal(response.Body.Bytes(), &connection)
	for _, query := range []string{"CALL p()", "SET SESSION TRANSACTION READ WRITE", "WITH c AS (SELECT 1) DELETE FROM t", "SELECT 1 INTO OUTFILE '/tmp/dump'"} {
		response := c.do("POST", "/api/connections/"+connection.ID+"/query", map[string]any{"query": query, "confirmDestructive": true})
		if response.Code != 403 {
			t.Errorf("%s: %d %s", query, response.Code, response.Body.String())
		}
	}
}
