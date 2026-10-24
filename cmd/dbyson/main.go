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
	"syscall"
	"time"

	"github.com/gaboiv/dbyson/internal/api"
	"github.com/gaboiv/dbyson/internal/config"
	"github.com/gaboiv/dbyson/internal/policy"
	"github.com/gaboiv/dbyson/internal/store"
	"github.com/gaboiv/dbyson/internal/transport"
	"github.com/gaboiv/dbyson/internal/vault"
	"github.com/gaboiv/dbyson/web"
)

// Set at build time via -ldflags.
var version = "dev"

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
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

	key, err := vault.LoadKey(dataDir)
	if err != nil {
		return err
	}
	v, err := vault.New(key)
	if err != nil {
		return err
	}
	mode := config.GetOr("MODE", modePersonal)
	mfaPolicy, err := resolveMFAPolicy(mode, config.Get("REQUIRE_MFA"))
	if err != nil {
		return err
	}
	if mode == modePersonal && !isLoopback(addr) {
		log.Warn("personal mode without required 2FA: publish the port on 127.0.0.1 only, or set DBYSON_MODE=team",
			"addr", addr)
	}
	log.Info("security policy", "mode", mode, "mfa", mfaPolicy)
	if old := config.Deprecated(); len(old) > 0 {
		log.Warn("deprecated environment variables: rename the ADMINBASE_ prefix to DBYSON_", "variables", old)
	}
	pol, err := policy.ParseAllowlist(config.Get("ALLOWED_HOSTS"))
	if err != nil {
		return err
	}
	passkeys, err := api.NewPasskeys(config.Get("WEBAUTHN_RP_ID"), config.Get("WEBAUTHN_ORIGIN"))
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:      addr,
		TLSConfig: tlsConfig,
		Handler: api.NewRouter(version, web.Assets(), api.Deps{
			WebAuthn:   passkeys,
			Store:      st,
			Vault:      v,
			Policy:     pol,
			SetupToken: config.Get("SETUP_TOKEN"),
			MFAPolicy:  mfaPolicy,
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
