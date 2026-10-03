// Package netprobe implements ports.Prober with plain TCP dials and the system resolver.
// Dialing fixed IPs separates "no internet" from "DNS broken".
package netprobe

import (
	"context"
	"net"

	"github.com/sosalejandro/wifi-autopilot/internal/core/domain"
)

type Prober struct{}

func (Prober) Internet(ctx context.Context, c domain.Connectivity) bool {
	d := net.Dialer{Timeout: c.Timeout}
	for _, t := range c.TCPTargets {
		if conn, err := d.DialContext(ctx, "tcp", t); err == nil {
			conn.Close()
			return true
		}
	}
	return false
}

// DNS resolves through /etc/resolv.conf, the same path every other program uses.
func (Prober) DNS(ctx context.Context, c domain.Connectivity) bool {
	r := &net.Resolver{PreferGo: true}
	for _, name := range c.DNSNames {
		ctx, cancel := context.WithTimeout(ctx, c.Timeout)
		_, err := r.LookupHost(ctx, name)
		cancel()
		if err == nil {
			return true
		}
	}
	return false
}
