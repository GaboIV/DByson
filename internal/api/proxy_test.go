package api

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIPAndHTTPSHonorTrustedProxies(t *testing.T) {
	proxied := &Server{Deps: Deps{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}}
	direct := &Server{}

	mk := func(remote, xff, xfproto string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		if xfproto != "" {
			r.Header.Set("X-Forwarded-Proto", xfproto)
		}
		return r
	}

	// Without trusted proxies the socket peer is the client; a forwarded header is ignored.
	if got := direct.clientIP(mk("203.0.113.9:4000", "1.2.3.4", "")); got != "203.0.113.9" {
		t.Fatalf("direct clientIP = %q, want the socket peer", got)
	}
	// A trusted proxy peer: the rightmost non-proxy forwarded address wins.
	if got := proxied.clientIP(mk("10.0.0.5:5000", "198.51.100.7, 10.0.0.9", "")); got != "198.51.100.7" {
		t.Fatalf("proxied clientIP = %q, want the real client", got)
	}
	// An untrusted peer claiming a forwarded address is not believed.
	if got := proxied.clientIP(mk("203.0.113.9:4000", "10.0.0.1", "")); got != "203.0.113.9" {
		t.Fatalf("spoofed clientIP = %q, want the socket peer", got)
	}
	// A dual-stack listener reports the IPv4 proxy as ::ffff:10.0.0.5; it must still be trusted.
	if got := proxied.clientIP(mk("[::ffff:10.0.0.5]:5000", "198.51.100.7", "")); got != "198.51.100.7" {
		t.Fatalf("IPv4-mapped proxy clientIP = %q, want the real client", got)
	}
	if !proxied.isHTTPS(mk("[::ffff:10.0.0.5]:5000", "", "https")) {
		t.Fatal("IPv4-mapped trusted proxy X-Forwarded-Proto was not believed")
	}

	// isHTTPS: an untrusted peer's X-Forwarded-Proto is ignored once a proxy is configured.
	if proxied.isHTTPS(mk("203.0.113.9:4000", "", "https")) {
		t.Fatal("untrusted X-Forwarded-Proto was believed")
	}
	// A trusted proxy's X-Forwarded-Proto is believed.
	if !proxied.isHTTPS(mk("10.0.0.5:5000", "", "https")) {
		t.Fatal("trusted X-Forwarded-Proto was not believed")
	}
	// With no proxy configured the header is still believed, so TLS-terminating setups do not regress.
	if !direct.isHTTPS(mk("203.0.113.9:4000", "", "https")) {
		t.Fatal("back-compat X-Forwarded-Proto was dropped")
	}
}
