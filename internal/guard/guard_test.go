package guard

import (
	"context"
	"errors"
	"testing"
)

func TestResolveBlocksLiteralIPs(t *testing.T) {
	g := Config{}
	blocked := []string{
		"127.0.0.1",
		"10.0.0.1",
		"192.168.1.1",
		"172.16.0.1",
		"169.254.169.254",
		"100.64.0.1",
		"fd00::1",
		"fe80::1",
		"::1",
		"127.0.0.1:8080",
		"[::1]:8080",
		"fd00:ec2::254",
	}
	for _, h := range blocked {
		if _, err := g.Resolve(context.Background(), h); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s should be blocked, got %v", h, err)
		}
	}
}

func TestResolveAllowsPublicLiteralIP(t *testing.T) {
	g := Config{}
	ip, err := g.Resolve(context.Background(), "1.1.1.1")
	if err != nil {
		t.Fatalf("public IP should be allowed, got %v", err)
	}
	if ip.String() != "1.1.1.1" {
		t.Fatalf("want 1.1.1.1, got %s", ip)
	}
}

func TestAllowPrivateOverrides(t *testing.T) {
	g := Config{AllowPrivate: true}
	ip, err := g.Resolve(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatalf("AllowPrivate should bypass, got %v", err)
	}
	if !ip.IsLoopback() {
		t.Fatalf("want loopback IP, got %s", ip)
	}
}

func TestCheckHostMatchesResolve(t *testing.T) {
	g := Config{}
	if err := g.CheckHost(context.Background(), "127.0.0.1"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("CheckHost should block loopback, got %v", err)
	}
	if err := g.CheckHost(context.Background(), "1.1.1.1"); err != nil {
		t.Fatalf("CheckHost should allow public, got %v", err)
	}
}
