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

// Resolve resolves host to a single IP after rejecting blocked ranges.
// Callers should dial this IP directly (not the hostname) to avoid DNS
// rebinding between check and connect. When AllowPrivate is true the
// block list is bypassed entirely.
func (c Config) Resolve(ctx context.Context, host string) (net.IP, error) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		if !c.AllowPrivate && isBlocked(ip) {
			return nil, fmt.Errorf("%w: %s", ErrBlocked, ip)
		}
		return ip, nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%w: %s has no addresses", ErrBlocked, host)
	}
	if !c.AllowPrivate {
		for _, ip := range ips {
			if isBlocked(ip) {
				return nil, fmt.Errorf("%w: %s -> %s", ErrBlocked, host, ip)
			}
		}
	}
	return ips[0], nil
}

// CheckHost runs Resolve and discards the IP. Kept for callers that only
// need the policy decision (e.g. the SOCKS5 path where remote resolution
// happens at the upstream proxy).
func (c Config) CheckHost(ctx context.Context, host string) error {
	_, err := c.Resolve(ctx, host)
	return err
}

func isBlocked(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// Carrier-grade NAT (RFC 6598).
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
		// Cloud metadata endpoints explicitly (IPv4).
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
		return false
	}
	// Explicit AWS Nitro IPv6 metadata endpoint: fd00:ec2::254.
	// (IsPrivate already covers fd00::/8 ULA, but keep an explicit guard
	// in case a future Go release narrows IsPrivate semantics.)
	if ip16 := ip.To16(); ip16 != nil {
		if ip16[0] == 0xfd && ip16[1] == 0x00 && ip16[2] == 0xec && ip16[3] == 0x02 {
			return true
		}
	}
	return false
}
