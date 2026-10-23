package mysql

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gaboiv/dbyson/internal/policy"
	"github.com/gaboiv/dbyson/internal/tunnel"
)

func TestIntegrationSSHTunnel(t *testing.T) {
	host := os.Getenv("DBYSON_INTEGRATION_SSH_HOST")
	if host == "" {
		t.Skip("set DBYSON_INTEGRATION_SSH_HOST for disposable SSH and MySQL servers")
	}
	mysqlHost := os.Getenv("DBYSON_INTEGRATION_MYSQL_HOST")
	if mysqlHost == "" {
		mysqlHost = "dbyson-ssh-mysql"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	p, _ := policy.ParseAllowlist("")
	hop := tunnel.Hop{Host: host, Port: 2222, User: "tester", Auth: "password", Password: "dbyson-ssh-tests"}
	observation, err := tunnel.Observe(ctx, hop, p)
	if err != nil {
		t.Fatal(err)
	}
	hop.Fingerprint = observation.Fingerprint
	for _, format := range []string{"password", "openssh", "v2.ppk", "v3.ppk", "v2-encrypted.ppk", "v3-encrypted.ppk"} {
		t.Run(format, func(t *testing.T) {
			h := hop
			if format != "password" {
				data, e := os.ReadFile("../../tunnel/testdata/" + format)
				if e != nil {
					t.Fatal(e)
				}
				h.Auth = "key"
				h.PrivateKey = string(data)
				if strings.Contains(format, "encrypted") {
					h.Passphrase = "dbyson-fixture-passphrase"
				}
			}
			db, e := Open(Config{Host: mysqlHost, Port: 3306, User: "root", Password: "dbyson-tests", TLSMode: TLSDisabled, ReadOnly: true}, tunnel.Dialer(tunnel.Config{Enabled: true, Hop: h}, p))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			var version string
			if e = db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); e != nil {
				t.Fatal(e)
			}
			if version == "" {
				t.Fatal("no server version")
			}
			qctx, abort := context.WithTimeout(ctx, 100*time.Millisecond)
			defer abort()
			start := time.Now()
			_, e = db.ExecContext(qctx, "SELECT SLEEP(10)")
			if e == nil || time.Since(start) > 2*time.Second {
				t.Fatalf("tunneled query not canceled: %v", e)
			}
		})
	}
	jump := hop
	cfg := tunnel.Config{Enabled: true, Hop: hop, Jump: &jump}
	db, err := Open(Config{Host: mysqlHost, Port: 3306, User: "root", Password: "dbyson-tests", TLSMode: TLSDisabled}, tunnel.Dialer(cfg, p))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err = db.QueryRowContext(ctx, "SELECT 42").Scan(&n); err != nil || n != 42 {
		t.Fatal(fmt.Sprint("nested SSH query: ", err, n))
	}
}
