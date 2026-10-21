// Package policy decides which network destinations AdminBase may connect to.
package policy

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// HostPolicy blocks dangerous destinations always and, when an allowlist is set,
// everything not on it. This keeps AdminBase from being used as an SSRF pivot.
type HostPolicy struct {
	names    map[string]bool
	prefixes []netip.Prefix
	Resolver interface {
		LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
	}
}

// ParseAllowlist parses a comma-separated list of hostnames, IPs and CIDRs. Empty means "no allowlist".
func ParseAllowlist(s string) (*HostPolicy, error) {
	p := &HostPolicy{names: map[string]bool{}, Resolver: net.DefaultResolver}
	for _, e := range strings.Split(s, ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if pf, err := netip.ParsePrefix(e); err == nil {
			p.prefixes = append(p.prefixes, pf.Masked())
		} else if ip, err := netip.ParseAddr(e); err == nil {
			p.prefixes = append(p.prefixes, netip.PrefixFrom(ip, ip.BitLen()))
		} else if strings.ContainsAny(e, " /\\") {
			return nil, fmt.Errorf("invalid allowlist entry %q", e)
		} else {
			p.names[e] = true
		}
	}
	return p, nil
}

func (p *HostPolicy) restricted() bool { return len(p.names) > 0 || len(p.prefixes) > 0 }

// Check returns the IPs of host that are allowed to be dialed.
func (p *HostPolicy) Check(ctx context.Context, host string) ([]netip.Addr, error) {
	host = strings.ToLower(host)
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip.Unmap()}
	} else {
		resolved, err := p.Resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", host, err)
		}
		for _, ip := range resolved {
			ips = append(ips, ip.Unmap())
		}
	}

	var ok []netip.Addr
	for _, ip := range ips {
		if alwaysBlocked(ip) {
			continue
		}
		if p.restricted() && !p.names[host] && !p.inPrefix(ip) {
			continue
		}
		ok = append(ok, ip)
	}
	if len(ok) == 0 {
		return nil, fmt.Errorf("destination %q is not allowed by policy", host)
	}
	return ok, nil
}

func (p *HostPolicy) inPrefix(ip netip.Addr) bool {
	for _, pf := range p.prefixes {
		if pf.Contains(ip) {
			return true
		}
	}
	return false
}

// alwaysBlocked covers addresses that are never a legitimate database target
// (notably the cloud metadata endpoint at 169.254.169.254).
func alwaysBlocked(ip netip.Addr) bool {
	return ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}
