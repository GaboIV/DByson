package main

import (
	"context"
	"errors"
	"log/slog"
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

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
