// Package auth handles Proxy-Authorization for the forward proxy.
package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

// Checker validates Proxy-Authorization headers. When Enabled is false,
// all requests are permitted (use only when bound to loopback).
type Checker struct {
	Enabled  bool
	expected string
}

func New(user, pass string) *Checker {
	if user == "" && pass == "" {
		return &Checker{Enabled: false}
	}
	raw := user + ":" + pass
	return &Checker{
		Enabled:  true,
		expected: "Basic " + base64.StdEncoding.EncodeToString([]byte(raw)),
	}
}

// Authorize returns true if the request is allowed. When auth is enabled and
// the header is missing or invalid, it writes a 407 response and returns false.
func (c *Checker) Authorize(w http.ResponseWriter, r *http.Request) bool {
	if !c.Enabled {
		return true
	}
	got := r.Header.Get("Proxy-Authorization")
	if got != "" && constEqual(got, c.expected) {
		// Strip the header so it does not leak to the upstream.
		r.Header.Del("Proxy-Authorization")
		return true
	}
	w.Header().Set("Proxy-Authenticate", `Basic realm="proxy-rotator"`)
	http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
	return false
}

func constEqual(a, b string) bool {
	if len(a) != len(b) {
		return subtle.ConstantTimeCompare([]byte(a), []byte(strings.Repeat("\x00", len(a)))) == -1
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
