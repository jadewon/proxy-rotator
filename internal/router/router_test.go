package router

import "testing"

func TestDecide(t *testing.T) {
	cases := []struct {
		name     string
		match    []string
		bypass   []string
		fallback Action
		host     string
		want     Action
	}{
		{"exact match", []string{"example.com"}, nil, ActionReject, "example.com", ActionProxy},
		{"exact match with port", []string{"example.com"}, nil, ActionReject, "example.com:443", ActionProxy},
		{"dot prefix suffix", []string{".example.com"}, nil, ActionReject, "a.example.com", ActionProxy},
		{"dot prefix root", []string{".example.com"}, nil, ActionReject, "example.com", ActionProxy},
		{"star suffix", []string{"*.example.com"}, nil, ActionReject, "x.example.com", ActionProxy},
		{"no match falls back", []string{"example.com"}, nil, ActionReject, "other.com", ActionReject},
		{"bypass wins", nil, []string{"localhost"}, ActionProxy, "localhost", ActionDirect},
		{"bypass suffix wins", nil, []string{".cluster.local"}, ActionProxy, "svc.cluster.local", ActionDirect},
		{"no rules -> fallback", nil, nil, ActionDirect, "example.com", ActionDirect},
		{"ipv6 host strips port", []string{"example.com"}, nil, ActionReject, "[::1]:443", ActionReject},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := New(c.match, c.bypass, c.fallback)
			if got := r.Decide(c.host); got != c.want {
				t.Fatalf("host=%q want=%s got=%s", c.host, c.want, got)
			}
		})
	}
}

func TestParseAction(t *testing.T) {
	cases := map[string]Action{
		"proxy":   ActionProxy,
		"direct":  ActionDirect,
		"reject":  ActionReject,
		"":        ActionProxy,
		"garbage": ActionProxy,
	}
	for in, want := range cases {
		if got := ParseAction(in); got != want {
			t.Errorf("%q: want %s got %s", in, want, got)
		}
	}
}
