package guard

import (
	"context"
	"errors"
	"testing"
)

func TestCheckHostBlocksLiteralIPs(t *testing.T) {
	g := Config{}
	blocked := []string{
		"127.0.0.1",
		"10.0.0.1",
		"192.168.1.1",
		"172.16.0.1",
		"169.254.169.254",      // AWS metadata
		"100.64.0.1",           // CGN
		"fd00::1",              // ULA
		"fe80::1",              // link-local
		"::1",                  // loopback
		"127.0.0.1:8080",       // with port
		"[::1]:8080",           // ipv6 with port
	}
	for _, h := range blocked {
		if err := g.CheckHost(context.Background(), h); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s should be blocked, got %v", h, err)
		}
	}
}

func TestCheckHostAllowsPublicLiteralIP(t *testing.T) {
	g := Config{}
	if err := g.CheckHost(context.Background(), "1.1.1.1"); err != nil {
		t.Errorf("public IP should be allowed, got %v", err)
	}
}

func TestAllowPrivateOverrides(t *testing.T) {
	g := Config{AllowPrivate: true}
	if err := g.CheckHost(context.Background(), "127.0.0.1"); err != nil {
		t.Errorf("AllowPrivate should bypass, got %v", err)
	}
}
