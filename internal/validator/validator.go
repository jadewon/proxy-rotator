package validator

import (
	"context"
	"fmt"
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
	TestURL string
	Timeout time.Duration
}

func New(testURL string, timeout time.Duration) *HTTP {
	return &HTTP{TestURL: testURL, Timeout: timeout}
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
	return nil
}
