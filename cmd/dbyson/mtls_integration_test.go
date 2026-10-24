package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Runs only against the disposable, certificate-protected application started
// by compose.security-test.yml. This proves the real listener and router wiring.
func TestIntegrationApplicationMTLS(t *testing.T) {
	endpoint := os.Getenv("DBYSON_MTLS_TEST_URL")
	if endpoint == "" {
		t.Skip("use compose.security-test.yml for the mTLS application fixture")
	}
	dir := os.Getenv("DBYSON_TEST_PKI_DIR")
	ca, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	client := func(name string) *http.Client {
		cfg := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}
		if name != "" {
			pair, err := tls.LoadX509KeyPair(filepath.Join(dir, name+".pem"), filepath.Join(dir, name+"-key.pem"))
			if err != nil {
				t.Fatal(err)
			}
			cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &pair, nil }
		}
		transport := &http.Transport{TLSClientConfig: cfg}
		t.Cleanup(transport.CloseIdleConnections)
		jar, _ := cookiejar.New(nil)
		return &http.Client{Transport: transport, Jar: jar, Timeout: 3 * time.Second}
	}
	valid := client("client")
	deadline := time.Now().Add(15 * time.Second)
	for {
		res, err := valid.Get(endpoint + "/api/healthz")
		if err == nil {
			res.Body.Close()
			if res.StatusCode != 200 {
				t.Fatal("healthcheck rejected valid client")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, name := range []string{"", "foreign", "expired", "wrong-usage"} {
		c := client(name)
		req, _ := http.NewRequest("GET", endpoint+"/api/status", nil)
		req.Header.Set("X-SSL-Client-Verify", "SUCCESS")
		if res, err := c.Do(req); err == nil {
			res.Body.Close()
			t.Fatalf("client %q bypassed the actual mTLS listener", name)
		}
	}
	res, err := valid.Get(endpoint + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		SetupRequired bool `json:"setupRequired"`
	}
	json.NewDecoder(res.Body).Decode(&status)
	res.Body.Close()
	path := "/api/auth/login"
	if status.SetupRequired {
		path = "/api/setup"
	}
	res, err = valid.Post(endpoint+path, "application/json", bytes.NewBufferString(`{"username":"mtls-admin","password":"mtls-Administrator-2026!"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("certificate login: %d", res.StatusCode)
	}
	for _, cookie := range res.Cookies() {
		if cookie.Name == "dbyson_session" && !cookie.Secure {
			t.Fatal("mTLS session cookie is not Secure")
		}
	}
	me, err := valid.Get(endpoint + "/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	me.Body.Close()
	if me.StatusCode != 200 {
		t.Fatal("original certificate cannot use its session")
	}
	other := client("client-other")
	other.Jar.SetCookies(res.Request.URL, valid.Jar.Cookies(res.Request.URL))
	replayed, err := other.Get(endpoint + "/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	replayed.Body.Close()
	if replayed.StatusCode != 401 {
		t.Fatal("another trusted certificate replayed the cookie")
	}
}
