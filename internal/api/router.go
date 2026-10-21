// Package api exposes the HTTP surface of AdminBase.
package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/adminbase/adminbase/internal/auth"
	"github.com/adminbase/adminbase/internal/policy"
	"github.com/adminbase/adminbase/internal/store"
	"github.com/adminbase/adminbase/internal/vault"
)

// Deps are the collaborators the HTTP layer needs.
type Deps struct {
	Store      *store.Store
	Vault      *vault.Vault
	Policy     *policy.HostPolicy
	SetupToken string // optional: required to create the first admin when set
}

type Server struct {
	Deps
	version  string
	sessions *auth.Sessions
	throttle *auth.Throttle // per ip+username (strict)
	// ipThrottle is per ip and loose, so a shared NAT/proxy address is not locked out by a few typos.
	ipThrottle *auth.Throttle
}

// NewRouter wires the API and the embedded single-page app.
func NewRouter(version string, assets fs.FS, deps Deps) http.Handler {
	s := &Server{
		Deps:       deps,
		version:    version,
		sessions:   auth.NewSessions(deps.Store.DB),
		throttle:   auth.NewThrottle(5, 15*time.Minute, 15*time.Minute),
		ipThrottle: auth.NewThrottle(30, 15*time.Minute, 15*time.Minute),
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": version})
	})

	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.Handle("POST /api/auth/logout", s.requireAuth(s.handleLogout))
	mux.Handle("GET /api/auth/me", s.requireAuth(s.handleMe))

	mux.Handle("GET /api/connections", s.requireAuth(s.listConnections))
	mux.Handle("POST /api/connections", s.requireAuth(s.createConnection))
	mux.Handle("POST /api/connections/test", s.requireAuth(s.testDraftConnection))
	mux.Handle("GET /api/connections/{id}", s.requireAuth(s.getConnection))
	mux.Handle("PUT /api/connections/{id}", s.requireAuth(s.updateConnection))
	mux.Handle("DELETE /api/connections/{id}", s.requireAuth(s.deleteConnection))
	mux.Handle("POST /api/connections/{id}/test", s.requireAuth(s.testSavedConnection))

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
