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

	"github.com/adminbase/adminbase/internal/api"
	"github.com/adminbase/adminbase/internal/policy"
	"github.com/adminbase/adminbase/internal/store"
	"github.com/adminbase/adminbase/internal/vault"
	"github.com/adminbase/adminbase/web"
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
	addr := env("ADMINBASE_LISTEN", "127.0.0.1:8080")
	dataDir := env("ADMINBASE_DATA_DIR", "./data")

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
	mode := env("ADMINBASE_MODE", modePersonal)
	mfaPolicy, err := resolveMFAPolicy(mode, os.Getenv("ADMINBASE_REQUIRE_MFA"))
	if err != nil {
		return err
	}
	if mode == modePersonal && !isLoopback(addr) {
		log.Warn("personal mode without required 2FA: publish the port on 127.0.0.1 only, or set ADMINBASE_MODE=team",
			"addr", addr)
	}
	log.Info("security policy", "mode", mode, "mfa", mfaPolicy)
	pol, err := policy.ParseAllowlist(os.Getenv("ADMINBASE_ALLOWED_HOSTS"))
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr: addr,
		Handler: api.NewRouter(version, web.Assets(), api.Deps{
			Store:      st,
			Vault:      v,
			Policy:     pol,
			SetupToken: os.Getenv("ADMINBASE_SETUP_TOKEN"),
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
		log.Info("adminbase listening", "addr", addr, "version", version, "data", dataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
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
	log.Info("adminbase stopped")
	return nil
}

// Deployment modes. Inside Docker the server always listens on 0.0.0.0 even when the port is
// only published on the host's loopback, so exposure cannot be detected and is declared instead.
const (
	modePersonal = "personal" // one person on their own machine: 2FA available, not required
	modeTeam     = "team"     // reachable by others over a network: 2FA required for prod and SSH
)

// resolveMFAPolicy applies an explicit ADMINBASE_REQUIRE_MFA, or the mode's default.
func resolveMFAPolicy(mode, explicit string) (string, error) {
	var def string
	switch mode {
	case modePersonal:
		def = api.MFAPolicyOff
	case modeTeam:
		def = api.MFAPolicySensitive
	default:
		return "", fmt.Errorf("ADMINBASE_MODE must be personal or team (got %q)", mode)
	}
	if explicit == "" {
		return def, nil
	}
	if !api.ValidMFAPolicy(explicit) {
		return "", fmt.Errorf("ADMINBASE_REQUIRE_MFA must be off, sensitive or all (got %q)", explicit)
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

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
