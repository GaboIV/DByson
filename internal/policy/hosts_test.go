package policy

import (
	"context"
	"net/netip"
	"testing"
)

type fakeResolver map[string][]string

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	var out []netip.Addr
	for _, s := range f[host] {
		out = append(out, netip.MustParseAddr(s))
	}
	return out, nil
}

func check(t *testing.T, allow, host string, dns fakeResolver) error {
	t.Helper()
	p, err := ParseAllowlist(allow)
	if err != nil {
		t.Fatal(err)
	}
	p.Resolver = dns
	_, err = p.Check(context.Background(), host)
	return err
}

func TestNoAllowlistAllowsNormalHosts(t *testing.T) {
	if err := check(t, "", "10.0.0.5", nil); err != nil {
		t.Fatal(err)
	}
	if err := check(t, "", "db.internal", fakeResolver{"db.internal": {"10.1.1.1"}}); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataAlwaysBlocked(t *testing.T) {
	if err := check(t, "", "169.254.169.254", nil); err == nil {
		t.Fatal("metadata IP must be blocked")
	}
	if err := check(t, "169.254.0.0/16", "169.254.169.254", nil); err == nil {
		t.Fatal("allowlisting must not unblock link-local")
	}
	if err := check(t, "", "evil.example", fakeResolver{"evil.example": {"169.254.169.254"}}); err == nil {
		t.Fatal("hostname resolving to metadata IP must be blocked")
	}
}

func TestAllowlist(t *testing.T) {
	dns := fakeResolver{"db.example.com": {"203.0.113.9"}, "other.com": {"198.51.100.1"}}
	if err := check(t, "db.example.com,10.0.0.0/8", "db.example.com", dns); err != nil {
		t.Fatal(err)
	}
	if err := check(t, "db.example.com,10.0.0.0/8", "10.2.3.4", dns); err != nil {
		t.Fatal(err)
	}
	if err := check(t, "db.example.com,10.0.0.0/8", "other.com", dns); err == nil {
		t.Fatal("host outside allowlist must be rejected")
	}
	if err := check(t, "10.0.0.0/8", "192.168.1.1", dns); err == nil {
		t.Fatal("IP outside allowlist must be rejected")
	}
}

func TestBadEntry(t *testing.T) {
	if _, err := ParseAllowlist("good.com,bad entry"); err == nil {
		t.Fatal("expected error")
	}
}
