// Package guard provides SSRF protections for target addresses that the
// forward proxy is asked to reach, including SOCKS5 upstream addresses.
package guard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

var ErrBlocked = errors.New("target address blocked by guard policy")

// Config controls which target addresses are reachable via the forward proxy.
type Config struct {
	// AllowPrivate disables the private / metadata block list. Only flip this
	// for test environments where the proxy is trusted and internal.
	AllowPrivate bool
}

// CheckHost resolves host and rejects targets in private, loopback,
// link-local, or known-metadata ranges.
func (c Config) CheckHost(ctx context.Context, host string) error {
	if c.AllowPrivate {
		return nil
	}
	// host may be "example.com" or "example.com:443" — strip the port.
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		if isBlocked(ip) {
			return fmt.Errorf("%w: %s", ErrBlocked, ip)
		}
		return nil
	}
	resolver := net.DefaultResolver
	ips, err := resolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("%w: %s has no addresses", ErrBlocked, host)
	}
	for _, ip := range ips {
		if isBlocked(ip) {
			return fmt.Errorf("%w: %s -> %s", ErrBlocked, host, ip)
		}
	}
	return nil
}

func isBlocked(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate() {
		return true
	}
	// Carrier-grade NAT (RFC 6598).
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
		// Cloud metadata endpoints explicitly.
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
	}
	return false
}
