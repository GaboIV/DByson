package policy

import (
	"context"
	"errors"
	"net"
	"time"
)

// DirectDialer returns a dial function that resolves the target once, checks every resulting IP
// against the policy, and dials the IP itself, so a DNS answer cannot change between check and use.
func (p *HostPolicy) DirectDialer(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := p.Check(ctx, host)
		if err != nil {
			return nil, err
		}
		d := net.Dialer{Timeout: timeout}
		var lastErr error
		for _, ip := range ips {
			conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = errors.New("no addresses to dial")
		}
		return nil, lastErr
	}
}
