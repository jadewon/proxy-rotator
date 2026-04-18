package router

import "strings"

type Action int

const (
	ActionProxy Action = iota
	ActionDirect
	ActionReject
)

func (a Action) String() string {
	switch a {
	case ActionProxy:
		return "proxy"
	case ActionDirect:
		return "direct"
	case ActionReject:
		return "reject"
	}
	return "unknown"
}

func ParseAction(s string) Action {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "direct":
		return ActionDirect
	case "reject":
		return ActionReject
	default:
		return ActionProxy
	}
}

type Router struct {
	match   []string
	bypass  []string
	fallback Action
}

func New(match, bypass []string, fallback Action) *Router {
	return &Router{match: normalize(match), bypass: normalize(bypass), fallback: fallback}
}

func normalize(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (r *Router) Decide(host string) Action {
	h := hostOnly(strings.ToLower(host))
	if matches(h, r.bypass) {
		return ActionDirect
	}
	if len(r.match) > 0 {
		if matches(h, r.match) {
			return ActionProxy
		}
		return r.fallback
	}
	return r.fallback
}

func hostOnly(host string) string {
	if i := strings.LastIndex(host, ":"); i >= 0 {
		// IPv6 literal "[::1]:80"
		if strings.Contains(host[:i], "]") {
			return host[:i]
		}
		// simple "host:port"
		if !strings.Contains(host[:i], ":") {
			return host[:i]
		}
	}
	return host
}

func matches(host string, patterns []string) bool {
	for _, p := range patterns {
		if strings.HasPrefix(p, ".") {
			if strings.HasSuffix(host, p) || host == p[1:] {
				return true
			}
			continue
		}
		if strings.HasPrefix(p, "*.") {
			suffix := p[1:]
			if strings.HasSuffix(host, suffix) {
				return true
			}
			continue
		}
		if host == p {
			return true
		}
	}
	return false
}
