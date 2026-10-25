package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gaboiv/dbyson/internal/audit"
)

// actionNames gives stable, readable names to audited routes (keyed by ServeMux pattern).
var actionNames = map[string]string{
	"POST /api/connections/{id}/mysql-accounts":                             "mysql.account.manage",
	"POST /api/connections/{id}/processes/kill":                             "mysql.process.kill",
	"POST /api/connections/{id}/saved-queries":                              "query.save",
	"DELETE /api/connections/{id}/saved-queries/{queryID}":                  "query.saved.delete",
	"POST /api/connections/{id}/databases/{db}/tables/{table}/import-csv":   "data.import.csv",
	"DELETE /api/auth/sessions/{sessionID}":                                 "auth.session.revoke",
	"POST /api/auth/sessions/revoke-others":                                 "auth.sessions.revoke_others",
	"POST /api/connections/{id}/queries/{queryID}/cancel":                   "query.cancel",
	"POST /api/auth/logout":                                                 "auth.logout",
	"POST /api/auth/mfa/setup":                                              "auth.mfa.setup",
	"POST /api/auth/mfa/enable":                                             "auth.mfa.enable",
	"POST /api/auth/mfa/disable":                                            "auth.mfa.disable",
	"POST /api/auth/mfa/recovery-codes":                                     "auth.mfa.recovery_codes",
	"GET /api/auth/oidc/login":                                              "auth.oidc.start",
	"GET /api/auth/oidc/callback":                                           "auth.oidc.login",
	"GET /api/audit/export":                                                 "audit.export",
	"POST /api/audit/purge":                                                 "audit.purge",
	"POST /api/connections":                                                 "connection.create",
	"PUT /api/connections/{id}":                                             "connection.update",
	"DELETE /api/connections/{id}":                                          "connection.delete",
	"GET /api/connections/{id}/grants":                                      "connection.grants.list",
	"POST /api/connections/{id}/grants":                                     "connection.grant.create",
	"DELETE /api/connections/{id}/grants/{userID}":                          "connection.grant.delete",
	"GET /api/users":                                                        "user.list",
	"POST /api/users":                                                       "user.create",
	"PUT /api/users/{id}":                                                   "user.update",
	"DELETE /api/users/{id}":                                                "user.delete",
	"POST /api/connections/test":                                            "connection.test",
	"POST /api/connections/{id}/test":                                       "connection.test",
	"POST /api/connections/{id}/test-draft":                                 "connection.test",
	"POST /api/connections/{id}/ssh/fingerprint":                            "ssh.fingerprint",
	"POST /api/ssh/fingerprint":                                             "ssh.fingerprint",
	"POST /api/discovery":                                                   "discovery.scan",
	"POST /api/connections/transfer/export":                                 "profiles.export",
	"POST /api/connections/transfer/import":                                 "profiles.import",
	"POST /api/connections/{id}/databases/create":                           "database.create",
	"POST /api/connections/{id}/databases/drop":                             "database.drop",
	"POST /api/connections/{id}/databases/{db}/tables/create":               "table.create",
	"POST /api/connections/{id}/databases/{db}/tables/{table}/row":          "row.read",
	"POST /api/connections/{id}/databases/{db}/tables/{table}/rows":         "row.insert",
	"PUT /api/connections/{id}/databases/{db}/tables/{table}/rows":          "row.update",
	"DELETE /api/connections/{id}/databases/{db}/tables/{table}/rows":       "row.delete",
	"POST /api/connections/{id}/databases/{db}/tables/{table}/batch-delete": "row.delete_batch",
	"POST /api/connections/{id}/databases/{db}/tables/{table}/truncate":     "table.truncate",
	"POST /api/connections/{id}/databases/{db}/tables/{table}/drop":         "table.drop",
	"POST /api/connections/{id}/databases/{db}/tables/{table}/optimize":     "table.optimize",
	"POST /api/connections/{id}/query":                                      "query.execute",
	"GET /api/connections/{id}/export":                                      "data.export",
}

// auditRecord travels in the request context so handlers can enrich the entry
// (SQL text, real outcome) that requireAuth writes once the handler returns.
type auditRecord struct {
	entry audit.Entry
	force bool // audit even though the method is GET (e.g. data exports)
	skip  bool // the handler audited the event itself
}

type auditKey struct{}

func auditFrom(r *http.Request) *auditRecord {
	rec, _ := r.Context().Value(auditKey{}).(*auditRecord)
	if rec == nil {
		return &auditRecord{} // detached: writes are harmless no-ops
	}
	return rec
}

// statusWriter captures the response status for the audit outcome.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func outcomeFor(status int) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return audit.OutcomeDenied
	case status >= 400:
		return audit.OutcomeError
	}
	return audit.OutcomeOK
}

func auditTarget(r *http.Request) string {
	var parts []string
	for _, k := range []string{"db", "table"} {
		if v := r.PathValue(k); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, ".")
}

// record writes an audit entry. A failure is logged but does not fail the request:
// by the time we know the outcome the operation has already happened.
func (s *Server) record(ctx context.Context, e audit.Entry) {
	if s.Audit == nil {
		return
	}
	if err := s.Audit.Record(context.WithoutCancel(ctx), e); err != nil {
		slog.Error("audit write failed", "action", e.Action, "err", err)
	}
}

// recordFor writes an explicit entry for the request and suppresses the generic one
// requireAuth would otherwise add when the handler returns.
func (s *Server) recordFor(r *http.Request, action, target, detail, outcome string) {
	auditFrom(r).skip = true
	e := audit.Entry{IP: clientIP(r), Action: action, Target: target, Detail: detail, Outcome: outcome}
	if sess := sessionFrom(r); sess != nil {
		e.UserID, e.Username = sess.UserID, sess.Username
	}
	s.record(r.Context(), e)
}

func filterFromQuery(q interface {
	Get(string) string
}) audit.Filter {
	limit, _ := strconv.Atoi(q.Get("limit"))
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	until, _ := strconv.ParseInt(q.Get("until"), 10, 64)
	return audit.Filter{
		Limit: limit, BeforeID: before, Action: q.Get("action"),
		Username: q.Get("username"), Outcome: q.Get("outcome"), Since: since, Until: until,
	}
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	f := filterFromQuery(r.URL.Query())
	if sess.Role != "admin" {
		f.UserID = sess.UserID // non-admins only see their own trail
		f.Username = ""
	}
	entries, err := s.Audit.List(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// exportAudit streams the trail as JSONL or CSV for ingestion by an external
// system (SIEM, archive). Non-admins export only their own trail.
func (s *Server) exportAudit(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	q := r.URL.Query()
	format := q.Get("format")
	if format == "" {
		format = audit.ExportJSONL
	}
	if format != audit.ExportJSONL && format != audit.ExportCSV {
		writeError(w, http.StatusBadRequest, "format must be jsonl or csv")
		return
	}
	f := filterFromQuery(q)
	if n, err := strconv.Atoi(q.Get("maxRows")); err == nil && n > 0 {
		f.Limit = n
	}
	if sess.Role != "admin" {
		f.UserID = sess.UserID
		f.Username = ""
	}
	rec := auditFrom(r)
	rec.skip = true // audited below with the real row count
	ct, ext := "application/x-ndjson", "ndjson"
	if format == audit.ExportCSV {
		ct, ext = "text/csv", "csv"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", `attachment; filename="dbyson-audit.`+ext+`"`)
	n, err := s.Audit.Export(r.Context(), w, format, f)
	outcome, detail := audit.OutcomeOK, "format="+format
	if err != nil {
		// Headers are already sent; the outcome is recorded, not the error.
		outcome, detail = audit.OutcomeError, "format="+format+" rows="+strconv.Itoa(n)
	}
	s.record(r.Context(), audit.Entry{UserID: sess.UserID, Username: sess.Username,
		IP: clientIP(r), Action: "audit.export", Detail: detail + " rows=" + strconv.Itoa(n), Outcome: outcome})
}

// purgeAudit deletes entries older than the given retention window. Only
// admins, and only explicit: there is no silent background deletion.
func (s *Server) purgeAudit(w http.ResponseWriter, r *http.Request) {
	if sessionFrom(r).Role != "admin" {
		writeError(w, http.StatusForbidden, "admin role required")
		return
	}
	var in struct {
		OlderThanDays int `json:"olderThanDays"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.OlderThanDays < 1 || in.OlderThanDays > 3650 {
		writeError(w, http.StatusBadRequest, "olderThanDays must be between 1 and 3650")
		return
	}
	cutoff := time.Now().AddDate(0, 0, -in.OlderThanDays).Unix()
	res, err := s.Audit.PurgeOlderThan(r.Context(), cutoff)
	if err != nil {
		s.fail(w, err)
		return
	}
	auditFrom(r).entry.Detail = "deleted=" + strconv.FormatInt(res.Deleted, 10) + " anchor=" + res.Anchor
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) verifyAudit(w http.ResponseWriter, r *http.Request) {
	if sessionFrom(r).Role != "admin" {
		writeError(w, http.StatusForbidden, "admin role required")
		return
	}
	v, err := s.Audit.Verify(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, v)
}
