// Package api exposes the HTTP surface of DByson.
package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gaboiv/dbyson/internal/audit"
	"github.com/gaboiv/dbyson/internal/auth"
	"github.com/gaboiv/dbyson/internal/policy"
	"github.com/gaboiv/dbyson/internal/sso"
	"github.com/gaboiv/dbyson/internal/store"
	"github.com/gaboiv/dbyson/internal/vault"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Deps are the collaborators the HTTP layer needs.
type Deps struct {
	WebAuthn   *webauthn.WebAuthn
	Store      *store.Store
	Vault      *vault.Vault
	Policy     *policy.HostPolicy
	SetupToken string // optional: required to create the first admin when set
	Audit      *audit.Logger
	MFAPolicy  string // off | sensitive | all (see mfa.go); empty means sensitive
	OIDC       *sso.Provider
}

type Server struct {
	passkeyPending passkeyCeremonies
	queries        queryRegistry
	confirmations  sync.Map // short-lived, single-use challenges
	oidcStates     oidcStates
	mfaPending     mfaChallenges
	Deps
	version  string
	sessions *auth.Sessions
	throttle *auth.Throttle // per ip+username (strict)
	// ipThrottle is per ip and loose, so a shared NAT/proxy address is not locked out by a few typos.
	ipThrottle *auth.Throttle
}

// newServer builds the API server without routes, so tests can hold the same
// instance that serves their requests (shared short-lived challenge maps).
func newServer(version string, deps Deps) *Server {
	s := &Server{
		Deps:       deps,
		version:    version,
		sessions:   auth.NewSessions(deps.Store.DB),
		throttle:   auth.NewThrottle(5, 15*time.Minute, 15*time.Minute),
		ipThrottle: auth.NewThrottle(30, 15*time.Minute, 15*time.Minute),
		mfaPending: mfaChallenges{m: map[string]*mfaChallenge{}},
	}
	if s.MFAPolicy == "" {
		s.MFAPolicy = MFAPolicySensitive
	}
	if s.Audit == nil && deps.Vault != nil {
		s.Audit = audit.New(deps.Store.DB, deps.Vault.DeriveKey("audit"))
	}
	return s
}

// NewRouter wires the API and the embedded single-page app.
func NewRouter(version string, assets fs.FS, deps Deps) http.Handler {
	return newServer(version, deps).routes(assets)
}

func (s *Server) routes(assets fs.FS) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": s.version})
	})

	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("GET /api/auth/passkeys/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"enabled": s.WebAuthn != nil})
	})
	mux.HandleFunc("POST /api/auth/passkeys/login/begin", s.beginPasskeyLogin)
	mux.HandleFunc("POST /api/auth/passkeys/login/finish", s.finishPasskeyLogin)
	mux.Handle("GET /api/auth/passkeys", s.requireAuth(s.listPasskeys))
	mux.Handle("POST /api/auth/passkeys/register/begin", s.requireAuth(s.beginPasskeyRegistration))
	mux.Handle("POST /api/auth/passkeys/register/finish", s.requireAuth(s.finishPasskeyRegistration))
	mux.Handle("POST /api/auth/passkeys/delete", s.requireAuth(s.deletePasskey))
	mux.Handle("POST /api/auth/logout", s.requireAuth(s.handleLogout))
	mux.Handle("GET /api/auth/me", s.requireAuth(s.handleMe))
	mux.Handle("GET /api/auth/sessions", s.requireAuth(s.listSessions))
	mux.Handle("DELETE /api/auth/sessions/{sessionID}", s.requireAuth(s.revokeSession))
	mux.Handle("POST /api/auth/sessions/revoke-others", s.requireAuth(s.revokeOtherSessions))
	mux.HandleFunc("GET /api/auth/oidc/status", s.handleOIDCStatus)
	mux.HandleFunc("GET /api/auth/oidc/login", s.handleOIDCLogin)
	mux.HandleFunc("GET /api/auth/oidc/callback", s.handleOIDCCallback)
	mux.HandleFunc("POST /api/auth/mfa/verify", s.handleMFAVerify)
	mux.Handle("GET /api/auth/mfa", s.requireAuth(s.handleMFAStatus))
	mux.Handle("POST /api/auth/mfa/setup", s.requireAuth(s.handleMFASetup))
	mux.Handle("POST /api/auth/mfa/enable", s.requireAuth(s.handleMFAEnable))
	mux.Handle("POST /api/auth/mfa/disable", s.requireAuth(s.handleMFADisable))
	mux.Handle("POST /api/auth/mfa/recovery-codes", s.requireAuth(s.handleMFARecoveryCodes))
	mux.Handle("GET /api/audit", s.requireAuth(s.listAudit))
	mux.Handle("GET /api/audit/verify", s.requireAuth(s.verifyAudit))
	mux.Handle("GET /api/audit/export", s.requireAuth(s.exportAudit))
	mux.Handle("POST /api/audit/purge", s.requireAuth(s.purgeAudit))

	mux.Handle("GET /api/users", s.requireAuth(s.listUsers))
	mux.Handle("POST /api/users", s.requireAuth(s.createUser))
	mux.Handle("PUT /api/users/{id}", s.requireAuth(s.updateUser))
	mux.Handle("DELETE /api/users/{id}", s.requireAuth(s.deleteUser))

	mux.Handle("GET /api/connections", s.requireAuth(s.listConnections))
	mux.Handle("POST /api/connections", s.requireAuth(s.createConnection))
	mux.Handle("POST /api/connections/test", s.requireAuth(s.testDraftConnection))
	mux.Handle("GET /api/connections/{id}", s.requireAuth(s.getConnection))
	mux.Handle("PUT /api/connections/{id}", s.requireAuth(s.updateConnection))
	mux.Handle("DELETE /api/connections/{id}", s.requireAuth(s.deleteConnection))
	mux.Handle("POST /api/connections/{id}/test", s.requireAuth(s.testSavedConnection))

	mux.Handle("GET /api/connections/{id}/grants", s.requireAuth(s.listGrants))
	mux.Handle("POST /api/connections/{id}/grants", s.requireAuth(s.createGrant))
	mux.Handle("DELETE /api/connections/{id}/grants/{userID}", s.requireAuth(s.deleteGrant))

	mux.Handle("POST /api/connections/{id}/test-draft", s.requireAuth(s.testDraftConnection))
	mux.Handle("POST /api/connections/{id}/ssh/fingerprint", s.requireAuth(s.sshFingerprint))
	mux.Handle("POST /api/ssh/fingerprint", s.requireAuth(s.sshFingerprint))
	mux.Handle("POST /api/discovery", s.requireAuth(s.discover))
	mux.Handle("POST /api/connections/transfer/export", s.requireAuth(s.exportProfiles))
	mux.Handle("POST /api/connections/transfer/import", s.requireAuth(s.importProfiles))
	// Explorer & Database interaction routes
	mux.Handle("GET /api/connections/{id}/databases", s.requireAuth(s.handleListDatabases))
	mux.Handle("POST /api/connections/{id}/databases/create", s.requireAuth(s.handleCreateDatabase))
	mux.Handle("POST /api/connections/{id}/databases/drop", s.requireAuth(s.handleDropDatabase))
	mux.Handle("GET /api/connections/{id}/databases/{db}/tables", s.requireAuth(s.handleListTables))
	mux.Handle("POST /api/connections/{id}/databases/{db}/tables/create", s.requireAuth(s.handleCreateTable))
	mux.Handle("GET /api/connections/{id}/databases/{db}/tables/{table}/schema", s.requireAuth(s.handleTableSchema))
	mux.Handle("GET /api/connections/{id}/databases/{db}/tables/{table}/data", s.requireAuth(s.handleTableData))
	mux.Handle("GET /api/connections/{id}/databases/{db}/tables/{table}/count", s.requireAuth(s.handleTableCount))
	mux.Handle("POST /api/connections/{id}/databases/{db}/tables/{table}/row", s.requireAuth(s.handleRowDetail))
	mux.Handle("POST /api/connections/{id}/databases/{db}/tables/{table}/rows", s.requireAuth(s.handleInsertRow))
	mux.Handle("PUT /api/connections/{id}/databases/{db}/tables/{table}/rows", s.requireAuth(s.handleUpdateRow))
	mux.Handle("DELETE /api/connections/{id}/databases/{db}/tables/{table}/rows", s.requireAuth(s.handleDeleteRow))
	mux.Handle("POST /api/connections/{id}/databases/{db}/tables/{table}/batch-delete", s.requireAuth(s.handleBatchDeleteRows))
	mux.Handle("POST /api/connections/{id}/databases/{db}/tables/{table}/truncate", s.requireAuth(s.handleTruncateTable))
	mux.Handle("POST /api/connections/{id}/databases/{db}/tables/{table}/drop", s.requireAuth(s.handleDropTable))
	mux.Handle("POST /api/connections/{id}/databases/{db}/tables/{table}/optimize", s.requireAuth(s.handleOptimizeTable))
	mux.Handle("POST /api/connections/{id}/query", s.requireAuth(s.handleQuery))
	mux.Handle("GET /api/connections/{id}/saved-queries", s.requireAuth(s.savedQueries))
	mux.Handle("POST /api/connections/{id}/saved-queries", s.requireAuth(s.savedQueries))
	mux.Handle("DELETE /api/connections/{id}/saved-queries/{queryID}", s.requireAuth(s.deleteSavedQuery))
	mux.Handle("GET /api/connections/{id}/databases/{db}/relations", s.requireAuth(s.relations))
	mux.Handle("POST /api/connections/{id}/databases/{db}/tables/{table}/import-csv", s.requireAuth(s.importCSV))
	mux.Handle("POST /api/connections/{id}/processes/kill", s.requireAuth(s.killProcess))
	mux.Handle("POST /api/connections/{id}/mysql-accounts", s.requireAuth(s.mysqlAccounts))
	mux.Handle("POST /api/connections/{id}/queries/{queryID}/cancel", s.requireAuth(s.cancelQuery))
	mux.Handle("GET /api/connections/{id}/export", s.requireAuth(s.handleExport))

	// Unknown API routes must 404 as JSON, never fall through to the SPA.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	mux.Handle("/", spaHandler(assets))

	return securityHeaders(sameOriginOnly(mux))
}

// spaHandler serves static files and falls back to index.html for client-side routes.
func spaHandler(assets fs.FS) http.Handler {
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(assets, name); err != nil {
			if _, err := fs.Stat(assets, "index.html"); err != nil {
				http.Error(w, "frontend not built", http.StatusServiceUnavailable)
				return
			}
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
}
