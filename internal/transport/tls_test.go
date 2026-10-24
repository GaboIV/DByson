package transport

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gaboiv/dbyson/internal/testpki"
)

func TestMutualTLSHandshake(t *testing.T) {
	dir := t.TempDir()
	if err := testpki.Generate(dir); err != nil {
		t.Fatal(err)
	}
	file := func(name string) string { return filepath.Join(dir, name) }
	cfg, err := ServerTLS(file("server.pem"), file("server-key.pem"), file("ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.VerifiedChains) == 0 {
			t.Error("unverified client")
		}
		w.WriteHeader(204)
	}))
	srv.TLS = cfg
	srv.StartTLS()
	defer srv.Close()
	pem, _ := os.ReadFile(file("ca.pem"))
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pem)
	for _, name := range []string{"client", "", "expired", "foreign", "wrong-usage"} {
		t.Run(name, func(t *testing.T) {
			clientTLS := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}
			if name != "" {
				pair, err := tls.LoadX509KeyPair(file(name+".pem"), file(name+"-key.pem"))
				if err != nil {
					t.Fatal(err)
				}
				// Force sending even untrusted/expired fixtures to test server verification.
				clientTLS.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &pair, nil }
			}
			transport := &http.Transport{TLSClientConfig: clientTLS}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			res, err := client.Get(srv.URL)
			if res != nil {
				res.Body.Close()
			}
			if name == "client" {
				if err != nil || res.StatusCode != 204 {
					t.Fatalf("valid client: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid client reached HTTP handler")
			}
		})
	}
	// An old TLS client cannot negotiate a fallback, and plaintext cannot reach the handler.
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MaxVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	if res, err := (&http.Client{Transport: transport, Timeout: time.Second}).Get(srv.URL); err == nil {
		res.Body.Close()
		t.Fatal("accepted TLS 1.2")
	}
}

func TestTLSConfigurationFailsClosed(t *testing.T) {
	if cfg, err := ServerTLS("", "", ""); cfg != nil || err != nil {
		t.Fatal("HTTP opt-in default changed")
	}
	for _, files := range [][3]string{{"", "", "ca"}, {"cert", "", ""}, {"", "key", ""}, {"missing", "missing", ""}} {
		if _, err := ServerTLS(files[0], files[1], files[2]); err == nil {
			t.Fatalf("accepted partial/invalid configuration: %v", files)
		}
	}
	dir := t.TempDir()
	if err := testpki.Generate(dir); err != nil {
		t.Fatal(err)
	}
	for _, invalidCA := range []string{"client.pem", "server-key.pem", "missing.pem"} {
		if _, err := ServerTLS(filepath.Join(dir, "server.pem"), filepath.Join(dir, "server-key.pem"), filepath.Join(dir, invalidCA)); err == nil {
			t.Fatalf("accepted invalid CA %q", invalidCA)
		}
	}
}
