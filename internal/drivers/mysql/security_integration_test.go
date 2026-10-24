package mysql

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func integrationConfig(t *testing.T) Config {
	t.Helper()
	address := os.Getenv("DBYSON_INTEGRATION_ADDR")
	if address == "" {
		t.Skip("set DBYSON_INTEGRATION_ADDR to a disposable database")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return Config{Host: host, Port: number, User: "root", Password: "dbyson-tests", TLSMode: TLSDisabled}
}

func TestIntegrationClientCertificates(t *testing.T) {
	cfg := integrationConfig(t)
	dir := os.Getenv("DBYSON_TEST_PKI_DIR")
	if dir == "" {
		t.Skip("use compose.security-test.yml for the MySQL TLS fixture")
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.ExecContext(ctx, "CREATE USER 'dbyson_mtls_test'@'%' IDENTIFIED BY 'dbyson-tests' REQUIRE X509"); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(context.Background(), "DROP USER 'dbyson_mtls_test'@'%'")
	cfg.User = "dbyson_mtls_test"
	cfg.TLSMode = TLSVerifyIdentity
	cfg.CACert = read("ca.pem")
	for _, name := range []string{"client", "", "expired", "foreign", "wrong-usage", "mismatched-key", "missing-key"} {
		t.Run(name, func(t *testing.T) {
			in := cfg
			if name != "" {
				fixture := name
				if name == "mismatched-key" || name == "missing-key" {
					fixture = "client"
				}
				in.ClientCert, in.ClientKey = read(fixture+".pem"), read(fixture+"-key.pem")
				if name == "mismatched-key" {
					in.ClientKey = read("server-key.pem")
				}
				if name == "missing-key" {
					in.ClientKey = ""
				}
			}
			result, err := Test(ctx, in, nil)
			if name == "client" {
				if err != nil || result == nil || !result.TLS {
					t.Fatalf("valid X509: %+v %v", result, err)
				}
			} else if err == nil {
				t.Fatal("invalid client certificate connected to REQUIRE X509 account")
			}
		})
	}
	// Client authentication must not weaken server identity verification.
	cfg.ClientCert, cfg.ClientKey = read("client.pem"), read("client-key.pem")
	cfg.CACert = read("foreign.pem")
	if _, err := Test(ctx, cfg, nil); err == nil {
		t.Fatal("accepted an untrusted MySQL server CA")
	}
}

func TestIntegrationKillOwnQuery(t *testing.T) {
	cfg := integrationConfig(t)
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	admin, err := Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.ExecContext(ctx, "CREATE USER 'dbyson_kill_test'@'%' IDENTIFIED BY 'dbyson-tests'"); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(context.Background(), "DROP USER 'dbyson_kill_test'@'%'")
	cfg.User = "dbyson_kill_test" // USAGE only: no PROCESS, SUPER or CONNECTION_ADMIN.
	db, err := Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, mode := range []string{"explicit", "context", "before-start", "read-only"} {
		t.Run(mode, func(t *testing.T) {
			parent, cancel := context.WithCancel(ctx)
			defer cancel()
			target := db
			if mode == "read-only" {
				readonly := cfg
				readonly.ReadOnly = true
				target, err = Open(readonly, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer target.Close()
			}
			e, err := NewExecution(parent, target)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			if mode == "before-start" {
				if err := e.Cancel(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := e.Run("", "SELECT SLEEP(20)", 1); !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel before start: %v", err)
				}
				return
			}
			done := make(chan error, 1)
			go func() { _, err := e.Run("", "SELECT SLEEP(20)", 1); done <- err }()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var n int
				if err := admin.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE ID = ? AND INFO LIKE '%SLEEP(20)%'", e.thread).Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("query did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if mode != "context" {
				if err := e.Cancel(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("wanted interrupted query: %v", err)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("query kept running after cancellation")
			}
			// A late cancellation cannot kill a subsequent use of the pool.
			if err := e.Cancel(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := ExecuteSQL(ctx, db, "", "SELECT 42", 1); err != nil {
				t.Fatal(err)
			}
		})
	}
}
