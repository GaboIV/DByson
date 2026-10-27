package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gaboiv/dbyson/internal/api"
	"github.com/gaboiv/dbyson/internal/audit"
	"github.com/gaboiv/dbyson/internal/config"
	"github.com/gaboiv/dbyson/internal/policy"
	"github.com/gaboiv/dbyson/internal/sso"
	"github.com/gaboiv/dbyson/internal/store"
	"github.com/gaboiv/dbyson/internal/transport"
	"github.com/gaboiv/dbyson/internal/vault"
	"github.com/gaboiv/dbyson/web"
)

// Set at build time via -ldflags.
var version = "dev"

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) > 1 && os.Args[1] == "rotate-key" {
		if err := runRotateKey(log, os.Args[2:]); err != nil {
			log.Error("fatal", "err", err)
			os.Exit(1)
		}
		return
	}
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	// Secure default: loopback only. Exposing it requires an explicit opt-in.
	addr := config.GetOr("LISTEN", "127.0.0.1:8080")
	dataDir := config.GetOr("DATA_DIR", "./data")
	tlsConfig, err := transport.ServerTLS(config.Get("TLS_CERT_FILE"), config.Get("TLS_KEY_FILE"), config.Get("TLS_CLIENT_CA_FILE"))
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	st, err := store.Open(dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	key, keySource, keyPath, err := vault.LoadKey(dataDir)
	if err != nil {
		return err
	}
	v, err := vault.New(key)
	if err != nil {
		return err
	}
	if days, _ := strconv.Atoi(config.Get("AUDIT_RETENTION_DAYS")); days > 0 {
		cutoff := time.Now().AddDate(0, 0, -days).Unix()
		res, err := audit.New(st.DB, v.DeriveKey("audit")).PurgeOlderThan(context.Background(), cutoff)
		if err != nil {
			return fmt.Errorf("audit retention purge: %w", err)
		}
		if res.Deleted > 0 {
			log.Info("audit retention purge", "deleted", res.Deleted, "anchor", res.Anchor)
		}
	}
	mode := config.GetOr("MODE", modePersonal)
	if mode == modeTeam {
		// In team mode the database is reachable over the network, so a key
		// stored next to the data it encrypts defeats the vault: anyone with
		// the volume reads every credential. Refuse to start instead.
		if keySource == vault.SourceGenerated {
			return fmt.Errorf("DBYSON_MODE=team requires DBYSON_MASTER_KEY or DBYSON_MASTER_KEY_FILE outside %s (refusing to use the generated %s)", dataDir, keyPath)
		}
		if keySource == vault.SourceFile && pathInside(dataDir, keyPath) {
			return fmt.Errorf("DBYSON_MODE=team requires DBYSON_MASTER_KEY_FILE outside the data directory (got %s)", keyPath)
		}
		log.Info("master key", "source", string(keySource), "fingerprint", vault.Fingerprint(key))
	}
	mfaPolicy, err := resolveMFAPolicy(mode, config.Get("REQUIRE_MFA"))
	if err != nil {
		return err
	}
	if mode == modePersonal && !isLoopback(addr) {
		log.Warn("personal mode without required 2FA: publish the port on 127.0.0.1 only, or set DBYSON_MODE=team",
			"addr", addr)
	}
	log.Info("security policy", "mode", mode, "mfa", mfaPolicy)
	if legacy := config.LegacyPresent(); len(legacy) > 0 {
		log.Warn("ignoring legacy ADMINBASE_* variables (no longer supported): rename them to DBYSON_*",
			"variables", legacy)
	}
	pol, err := policy.ParseAllowlist(config.Get("ALLOWED_HOSTS"))
	if err != nil {
		return err
	}
	passkeys, err := api.NewPasskeys(config.Get("WEBAUTHN_RP_ID"), config.Get("WEBAUTHN_ORIGIN"))
	if err != nil {
		return err
	}
	oidcConfig, passwordLoginDisabled, err := loadAuthConfig()
	if err != nil {
		return err
	}
	oidc := sso.New(oidcConfig)
	if oidc.Config().Enabled() {
		log.Info("sso enabled", "provider", oidc.Config().Name(), "autoProvision", oidc.Config().AutoProvision,
			"allowedDomains", oidc.Config().AllowedDomains, "trustMFA", oidc.Config().TrustMFA)
		if oidc.Config().RedirectURL == "" {
			log.Warn("sso callback URL is not configured: set DBYSON_OIDC_REDIRECT_URL")
		}
	}

	srv := &http.Server{
		Addr:      addr,
		TLSConfig: tlsConfig,
		Handler: api.NewRouter(version, web.Assets(), api.Deps{
			WebAuthn:              passkeys,
			Store:                 st,
			Vault:                 v,
			Policy:                pol,
			SetupToken:            config.Get("SETUP_TOKEN"),
			MFAPolicy:             mfaPolicy,
			OIDC:                  oidc,
			PasswordLoginDisabled: passwordLoginDisabled,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		log.Info("dbyson listening", "addr", addr, "version", version, "data", dataDir)
		var serveErr error
		if tlsConfig != nil {
			serveErr = srv.ListenAndServeTLS("", "")
		} else {
			serveErr = srv.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errc <- serveErr
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	log.Info("dbyson stopped")
	return nil
}

// Deployment modes. Inside Docker the server always listens on 0.0.0.0 even when the port is
// only published on the host's loopback, so exposure cannot be detected and is declared instead.
const (
	modePersonal = "personal" // one person on their own machine: 2FA available, not required
	modeTeam     = "team"     // reachable by others over a network: 2FA required for prod and SSH
)

// resolveMFAPolicy applies an explicit DBYSON_REQUIRE_MFA, or the mode's default.
func resolveMFAPolicy(mode, explicit string) (string, error) {
	var def string
	switch mode {
	case modePersonal:
		def = api.MFAPolicyOff
	case modeTeam:
		def = api.MFAPolicySensitive
	default:
		return "", fmt.Errorf("DBYSON_MODE must be personal or team (got %q)", mode)
	}
	if explicit == "" {
		return def, nil
	}
	if !api.ValidMFAPolicy(explicit) {
		return "", fmt.Errorf("DBYSON_REQUIRE_MFA must be off, sensitive or all (got %q)", explicit)
	}
	return explicit, nil
}

// pathInside reports whether path resolves inside dir.
func pathInside(dir, path string) bool {
	absDir, err1 := filepath.Abs(dir)
	absPath, err2 := filepath.Abs(path)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(absDir, absPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// envBool reads a DBYSON_* flag: "true" or "1" enable it.
func envBool(name string) bool {
	v := config.Get(name)
	return v == "true" || v == "1"
}
