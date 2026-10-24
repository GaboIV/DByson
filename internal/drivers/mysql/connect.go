// Package mysql opens connections to MySQL/MariaDB with explicit TLS modes and a policy-checked dialer.
package mysql

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
)

const (
	TLSDisabled       = "disabled"        // no encryption (only sensible for localhost)
	TLSRequired       = "required"        // encrypted, certificate NOT verified
	TLSVerifyCA       = "verify_ca"       // certificate chain verified, hostname not checked
	TLSVerifyIdentity = "verify_identity" // chain and hostname verified
)

func ValidTLSMode(m string) bool {
	switch m {
	case TLSDisabled, TLSRequired, TLSVerifyCA, TLSVerifyIdentity:
		return true
	}
	return false
}

// Dialer opens the raw network connection. The direct dialer is policy-checked; an SSH tunnel
// dialer can be plugged in later without touching this package.
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

type Config struct {
	Host       string
	Port       int
	User       string
	Password   string
	Database   string
	TLSMode    string
	CACert     string // PEM
	ClientCert string // PEM
	ClientKey  string // PEM
	Timeout    time.Duration
	ReadOnly   bool
}

// BuildTLS returns the *tls.Config for cfg, or nil when TLS is disabled.
func BuildTLS(cfg Config) (*tls.Config, error) {
	if cfg.TLSMode == TLSDisabled {
		return nil, nil
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.Host}

	var roots *x509.CertPool
	if cfg.CACert != "" {
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(cfg.CACert)) {
			return nil, errors.New("CA certificate is not valid PEM")
		}
	}
	if cfg.ClientCert != "" || cfg.ClientKey != "" {
		pair, err := tls.X509KeyPair([]byte(cfg.ClientCert), []byte(cfg.ClientKey))
		if err != nil {
			return nil, fmt.Errorf("client certificate/key: %w", err)
		}
		tc.Certificates = []tls.Certificate{pair}
	}

	switch cfg.TLSMode {
	case TLSRequired:
		tc.InsecureSkipVerify = true //nolint:gosec // explicit user choice: encrypt without authenticating the server
	case TLSVerifyCA:
		// Verify the chain ourselves, skipping only the hostname check.
		tc.InsecureSkipVerify = true //nolint:gosec // chain is verified in VerifyPeerCertificate
		tc.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("server presented no certificate")
			}
			certs := make([]*x509.Certificate, len(raw))
			for i, r := range raw {
				c, err := x509.ParseCertificate(r)
				if err != nil {
					return err
				}
				certs[i] = c
			}
			inter := x509.NewCertPool()
			for _, c := range certs[1:] {
				inter.AddCert(c)
			}
			_, err := certs[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter})
			return err
		}
	case TLSVerifyIdentity:
		tc.RootCAs = roots // nil means the system trust store
	default:
		return nil, fmt.Errorf("unknown TLS mode %q", cfg.TLSMode)
	}
	return tc, nil
}

// Open returns a handle to the server. The caller must Close it. Nothing is dialed until first use.
func Open(cfg Config, dial Dialer) (*sql.DB, error) {
	tc, err := BuildTLS(cfg)
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	mc := gomysql.NewConfig()
	mc.User = cfg.User
	mc.Passwd = cfg.Password
	mc.DBName = cfg.Database
	mc.Net = "tcp"
	mc.Addr = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	mc.DialFunc = dial
	mc.TLS = tc
	mc.Timeout = timeout
	mc.ReadTimeout = 0
	mc.ParseTime = true
	mc.Collation = "utf8mb4_general_ci" // also fixes the connection charset to utf8mb4
	// CREATE USER cannot be prepared on MySQL. Delegate parameter interpolation
	// to the driver, which escapes with the active SQL mode and fixed utf8mb4 charset.
	// MultiStatements stays disabled; application values never become SQL fragments.
	mc.InterpolateParams = true

	// Hardening: never allow the server to request local files (LOAD DATA LOCAL INFILE),
	// never send the password in cleartext, never silently fall back to plaintext.
	mc.AllowAllFiles = false
	mc.AllowCleartextPasswords = false
	mc.AllowFallbackToPlaintext = false

	connector, err := gomysql.NewConnector(mc)
	if err != nil {
		return nil, err
	}
	var configured driver.Connector = connector
	if cfg.ReadOnly {
		configured = readOnlyConnector{connector}
	}
	db := sql.OpenDB(configured)
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(10 * time.Minute)
	return db, nil
}

// Configure every physical connection, not just one arbitrary pool checkout.
// Fail closed if the server cannot enforce the requested session access mode.
type readOnlyConnector struct{ driver.Connector }

func (c readOnlyConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	exec, ok := conn.(driver.ExecerContext)
	if !ok {
		conn.Close()
		return nil, errors.New("driver cannot configure read-only sessions")
	}
	if _, err := exec.ExecContext(ctx, "SET SESSION TRANSACTION READ ONLY", nil); err != nil {
		conn.Close()
		return nil, fmt.Errorf("enforce read-only session: %w", err)
	}
	return conn, nil
}

type TestResult struct {
	ServerVersion string `json:"serverVersion"`
	TLS           bool   `json:"tls"`
	TLSCipher     string `json:"tlsCipher,omitempty"`
	TLSVersion    string `json:"tlsVersion,omitempty"`
	LatencyMs     int64  `json:"latencyMs"`
}

// Test connects, and reports the server version and whether the session is really encrypted.
func Test(ctx context.Context, cfg Config, dial Dialer) (*TestResult, error) {
	db, err := Open(cfg, dial)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	start := time.Now()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	res := &TestResult{LatencyMs: time.Since(start).Milliseconds()}
	if err := conn.QueryRowContext(ctx, "SELECT VERSION()").Scan(&res.ServerVersion); err != nil {
		return nil, err
	}
	// Ask the server what it negotiated rather than trusting our own config.
	var name, cipher, version string
	if err := conn.QueryRowContext(ctx, "SHOW SESSION STATUS LIKE 'Ssl_cipher'").Scan(&name, &cipher); err == nil && cipher != "" {
		res.TLS, res.TLSCipher = true, cipher
	}
	if err := conn.QueryRowContext(ctx, "SHOW SESSION STATUS LIKE 'Ssl_version'").Scan(&name, &version); err == nil {
		res.TLSVersion = version
	}
	return res, nil
}
