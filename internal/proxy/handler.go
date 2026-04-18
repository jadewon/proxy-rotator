// Package proxy implements the HTTP forward proxy that routes each request
// through a SOCKS5 entry selected from the central Pool.
package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jadewon/proxy-rotator/internal/auth"
	"github.com/jadewon/proxy-rotator/internal/guard"
	"github.com/jadewon/proxy-rotator/internal/metrics"
	"github.com/jadewon/proxy-rotator/internal/pool"
	"github.com/jadewon/proxy-rotator/internal/router"
	"golang.org/x/net/proxy"
)

type Config struct {
	MaxRetries      int
	PerProxyTimeout time.Duration
	TotalTimeout    time.Duration
}

type Handler struct {
	pool   *pool.Pool
	router *router.Router
	auth   *auth.Checker
	guard  guard.Config
	cfg    Config
	log    *slog.Logger
}

func New(p *pool.Pool, r *router.Router, a *auth.Checker, g guard.Config, cfg Config) *Handler {
	return &Handler{
		pool:   p,
		router: r,
		auth:   a,
		guard:  g,
		cfg:    cfg,
		log:    slog.With("component", "proxy"),
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.auth.Authorize(w, r) {
		return
	}

	target := targetHost(r)
	start := time.Now()
	switch h.router.Decide(target) {
	case router.ActionReject:
		http.Error(w, "rejected by router policy", http.StatusForbidden)
		metrics.RequestTotal.WithLabelValues("rejected").Inc()
		metrics.RequestDuration.WithLabelValues("rejected").Observe(time.Since(start).Seconds())
		return
	case router.ActionDirect:
		h.serveDirect(w, r, target)
		metrics.RequestTotal.WithLabelValues("direct").Inc()
		metrics.RequestDuration.WithLabelValues("direct").Observe(time.Since(start).Seconds())
		return
	}

	if err := h.guard.CheckHost(r.Context(), target); err != nil {
		h.log.Warn("guard blocked target", "host", target, "err", err)
		http.Error(w, "target blocked", http.StatusForbidden)
		metrics.RequestTotal.WithLabelValues("rejected").Inc()
		metrics.RequestDuration.WithLabelValues("rejected").Observe(time.Since(start).Seconds())
		return
	}

	if r.Method == http.MethodConnect {
		h.handleConnect(w, r, start)
	} else {
		h.handleHTTP(w, r, start)
	}
}

// targetHost returns the effective target host (with port when present).
// For CONNECT requests the URL is authority-form. For non-CONNECT forward
// proxy requests it is absolute-form so URL.Host is authoritative.
func targetHost(r *http.Request) string {
	if r.Method == http.MethodConnect {
		return r.URL.Host
	}
	if r.URL != nil && r.URL.Host != "" {
		return r.URL.Host
	}
	return r.Host
}

func (h *Handler) serveDirect(w http.ResponseWriter, r *http.Request, target string) {
	if r.Method == http.MethodConnect {
		upstream, err := net.DialTimeout("tcp", target, h.cfg.PerProxyTimeout)
		if err != nil {
			h.log.Debug("direct dial failed", "target", target, "err", err)
			http.Error(w, "direct dial failed", http.StatusBadGateway)
			return
		}
		hijackAndPipe(w, upstream, h.cfg.TotalTimeout)
		return
	}
	tr := &http.Transport{
		Proxy:               nil,
		TLSHandshakeTimeout: h.cfg.PerProxyTimeout,
	}
	defer tr.CloseIdleConnections()
	forward(w, r, tr, h.log)
}

func (h *Handler) handleConnect(w http.ResponseWriter, r *http.Request, overallStart time.Time) {
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.TotalTimeout)
	defer cancel()

	tried := map[string]bool{}
	var lastErr error
	for attempt := 0; attempt <= h.cfg.MaxRetries; attempt++ {
		entry, err := h.pool.Select(tried)
		if err != nil {
			lastErr = err
			break
		}
		released := false
		release := func() {
			if !released {
				released = true
				entry.DecInflight()
			}
		}

		dialStart := time.Now()
		upstream, dialErr := h.dialSOCKS5(ctx, entry, r.URL.Host)
		if dialErr != nil {
			h.pool.RecordFailure(entry.Addr)
			tried[entry.Addr] = true
			lastErr = dialErr
			metrics.UpstreamTotal.WithLabelValues("fail").Inc()
			h.log.Debug("connect dial failed", "proxy", entry.Addr, "target", r.URL.Host, "err", dialErr)
			release()
			continue
		}
		h.pool.RecordSuccess(entry.Addr, time.Since(dialStart))
		metrics.UpstreamTotal.WithLabelValues("success").Inc()

		// Tunnel holds the entry until the peer closes. Ensure Inflight is
		// decremented even if hijackAndPipe panics.
		defer release()
		hijackAndPipe(w, upstream, h.cfg.TotalTimeout)
		metrics.RequestTotal.WithLabelValues("success").Inc()
		metrics.RequestDuration.WithLabelValues("success").Observe(time.Since(overallStart).Seconds())
		return
	}

	metrics.RequestTotal.WithLabelValues("fail").Inc()
	metrics.RequestDuration.WithLabelValues("fail").Observe(time.Since(overallStart).Seconds())
	if errors.Is(lastErr, pool.ErrEmpty) {
		http.Error(w, "no proxies available", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, "bad gateway", http.StatusBadGateway)
}

func (h *Handler) handleHTTP(w http.ResponseWriter, r *http.Request, overallStart time.Time) {
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.TotalTimeout)
	defer cancel()

	// A request is retry-safe when it is idempotent AND has no body to replay.
	retrySafe := idempotent(r.Method) && (r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0)
	maxAttempts := 1
	if retrySafe {
		maxAttempts = h.cfg.MaxRetries + 1
	}

	tried := map[string]bool{}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		entry, err := h.pool.Select(tried)
		if err != nil {
			lastErr = err
			break
		}
		released := false
		release := func() {
			if !released {
				released = true
				entry.DecInflight()
			}
		}

		tr, trErr := h.transport(entry)
		if trErr != nil {
			h.pool.RecordFailure(entry.Addr)
			tried[entry.Addr] = true
			lastErr = trErr
			metrics.UpstreamTotal.WithLabelValues("fail").Inc()
			release()
			continue
		}

		outReq := r.Clone(ctx)
		outReq.RequestURI = ""
		removeHopHeaders(outReq.Header)

		rtStart := time.Now()
		resp, rtErr := tr.RoundTrip(outReq)
		if rtErr != nil {
			tr.CloseIdleConnections()
			h.pool.RecordFailure(entry.Addr)
			tried[entry.Addr] = true
			lastErr = rtErr
			metrics.UpstreamTotal.WithLabelValues("fail").Inc()
			h.log.Debug("http roundtrip failed", "proxy", entry.Addr, "target", outReq.URL.Host, "err", rtErr)
			release()
			continue
		}

		h.pool.RecordSuccess(entry.Addr, time.Since(rtStart))
		removeHopHeaders(resp.Header)
		copyHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		resp.Body.Close()
		tr.CloseIdleConnections()
		metrics.UpstreamTotal.WithLabelValues("success").Inc()
		metrics.RequestTotal.WithLabelValues("success").Inc()
		metrics.RequestDuration.WithLabelValues("success").Observe(time.Since(overallStart).Seconds())
		release()
		return
	}

	metrics.RequestTotal.WithLabelValues("fail").Inc()
	metrics.RequestDuration.WithLabelValues("fail").Observe(time.Since(overallStart).Seconds())
	if errors.Is(lastErr, pool.ErrEmpty) {
		http.Error(w, "no proxies available", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, "bad gateway", http.StatusBadGateway)
}

func (h *Handler) dialSOCKS5(ctx context.Context, e *pool.Entry, target string) (net.Conn, error) {
	var auth *proxy.Auth
	if e.Auth != nil {
		auth = &proxy.Auth{User: e.Auth.User, Password: e.Auth.Pass}
	}
	d, err := proxy.SOCKS5("tcp", e.Addr, auth, &net.Dialer{Timeout: h.cfg.PerProxyTimeout})
	if err != nil {
		return nil, err
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("dialer not ContextDialer")
	}
	dialCtx, cancel := context.WithTimeout(ctx, h.cfg.PerProxyTimeout)
	defer cancel()
	return cd.DialContext(dialCtx, "tcp", target)
}

func (h *Handler) transport(e *pool.Entry) (*http.Transport, error) {
	var auth *proxy.Auth
	if e.Auth != nil {
		auth = &proxy.Auth{User: e.Auth.User, Password: e.Auth.Pass}
	}
	d, err := proxy.SOCKS5("tcp", e.Addr, auth, &net.Dialer{Timeout: h.cfg.PerProxyTimeout})
	if err != nil {
		return nil, err
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("dialer not ContextDialer")
	}
	return &http.Transport{
		DialContext:           cd.DialContext,
		TLSHandshakeTimeout:   h.cfg.PerProxyTimeout,
		ResponseHeaderTimeout: h.cfg.PerProxyTimeout,
		DisableKeepAlives:     true,
	}, nil
}

func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// hijackAndPipe bridges a CONNECT tunnel between client and upstream.
// idleTimeout applies SetDeadline so zombie tunnels do not linger forever.
func hijackAndPipe(w http.ResponseWriter, upstream net.Conn, idleTimeout time.Duration) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	defer client.Close()
	defer upstream.Close()

	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	if idleTimeout > 0 {
		_ = client.SetDeadline(time.Now().Add(idleTimeout))
		_ = upstream.SetDeadline(time.Now().Add(idleTimeout))
	}

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
}

func forward(w http.ResponseWriter, r *http.Request, tr *http.Transport, log *slog.Logger) {
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	removeHopHeaders(outReq.Header)
	resp, err := tr.RoundTrip(outReq)
	if err != nil {
		log.Debug("direct forward failed", "target", outReq.URL.Host, "err", err)
		http.Error(w, "direct forward failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	removeHopHeaders(resp.Header)
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

var hopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func removeHopHeaders(h http.Header) {
	for _, k := range hopHeaders {
		h.Del(k)
	}
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
