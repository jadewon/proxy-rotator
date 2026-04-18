// Package validator probes SOCKS5 candidates by requesting a known URL
// through each proxy and optionally checking the response body.
package validator

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/jadewon/proxy-rotator/internal/pool"
	"golang.org/x/net/proxy"
)

type RawProxy struct {
	Addr     string
	Auth     *pool.Auth
	Metadata map[string]string
}

type Validator interface {
	Validate(ctx context.Context, raw RawProxy) error
}

type HTTP struct {
	TestURL   string
	Timeout   time.Duration
	MatchBody string // if non-empty, response body must contain this string
}

func New(testURL string, timeout time.Duration, matchBody string) *HTTP {
	return &HTTP{TestURL: testURL, Timeout: timeout, MatchBody: matchBody}
}

func (v *HTTP) Validate(ctx context.Context, raw RawProxy) error {
	var auth *proxy.Auth
	if raw.Auth != nil {
		auth = &proxy.Auth{User: raw.Auth.User, Password: raw.Auth.Pass}
	}
	dialer, err := proxy.SOCKS5("tcp", raw.Addr, auth, &net.Dialer{Timeout: v.Timeout})
	if err != nil {
		return fmt.Errorf("socks5 init: %w", err)
	}
	ctxDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return fmt.Errorf("dialer does not implement ContextDialer")
	}

	tr := &http.Transport{
		DialContext:         ctxDialer.DialContext,
		TLSHandshakeTimeout: v.Timeout,
		DisableKeepAlives:   true,
	}
	defer tr.CloseIdleConnections()

	client := &http.Client{Transport: tr, Timeout: v.Timeout}

	reqCtx, cancel := context.WithTimeout(ctx, v.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, v.TestURL, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	if v.MatchBody == "" {
		return nil
	}
	// Cap body read to guard against hostile upstreams feeding infinite bodies.
	const bodyCap = 256 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyCap))
	if err != nil {
		return fmt.Errorf("body read: %w", err)
	}
	if !containsString(body, v.MatchBody) {
		return fmt.Errorf("body match failed")
	}
	return nil
}

func containsString(haystack []byte, needle string) bool {
	if needle == "" {
		return true
	}
	if len(haystack) < len(needle) {
		return false
	}
	// net/http guarantees body is raw bytes; simple substring check.
	// Avoid importing "strings" on a hot-ish path by doing it manually.
	n := []byte(needle)
	for i := 0; i+len(n) <= len(haystack); i++ {
		if haystack[i] == n[0] && bytesEq(haystack[i:i+len(n)], n) {
			return true
		}
	}
	return false
}

func bytesEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
