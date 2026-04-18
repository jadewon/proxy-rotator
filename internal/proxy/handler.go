package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

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
	cfg    Config
	log    *slog.Logger
}

func New(p *pool.Pool, r *router.Router, cfg Config) *Handler {
	return &Handler{pool: p, router: r, cfg: cfg, log: slog.With("component", "proxy")}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if r.Method == http.MethodConnect {
		host = r.URL.Host
	}
	start := time.Now()
	switch h.router.Decide(host) {
	case router.ActionReject:
		http.Error(w, "rejected by router policy", http.StatusForbidden)
		metrics.RequestTotal.WithLabelValues("rejected").Inc()
		metrics.RequestDuration.WithLabelValues("rejected").Observe(time.Since(start).Seconds())
		return
	case router.ActionDirect:
		h.serveDirect(w, r)
		metrics.RequestTotal.WithLabelValues("direct").Inc()
		metrics.RequestDuration.WithLabelValues("direct").Observe(time.Since(start).Seconds())
		return
	}
	if r.Method == http.MethodConnect {
		h.handleConnect(w, r, start)
	} else {
		h.handleHTTP(w, r, start)
	}
}

func (h *Handler) serveDirect(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		upstream, err := net.DialTimeout("tcp", r.URL.Host, h.cfg.PerProxyTimeout)
		if err != nil {
			http.Error(w, "direct dial failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		hijackAndPipe(w, upstream)
		return
	}
	tr := &http.Transport{
		Proxy:               nil,
		TLSHandshakeTimeout: h.cfg.PerProxyTimeout,
	}
	defer tr.CloseIdleConnections()
	forward(w, r, tr)
}

func (h *Handler) handleConnect(w http.ResponseWriter, r *http.Request, overallStart time.Time) {
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.TotalTimeout)
	defer cancel()

	tried := map[string]bool{}
	var lastErr error
	for attempt := 0; attempt <= h.cfg.MaxRetries; attempt++ {
		entry, err := h.pool.SelectExcept(tried)
		if err != nil {
			lastErr = err
			break
		}

		upstream, dialErr := h.dialSOCKS5(ctx, entry, r.URL.Host)
		if dialErr != nil {
			h.pool.RecordFailure(entry.Addr)
			entry.DecInflight()
			tried[entry.Addr] = true
			lastErr = dialErr
			metrics.UpstreamTotal.WithLabelValues("fail").Inc()
			h.log.Debug("connect dial failed", "proxy", entry.Addr, "target", r.URL.Host, "err", dialErr)
			continue
		}

		start := time.Now()
		hijackAndPipe(w, upstream)
		h.pool.RecordSuccess(entry.Addr, time.Since(start))
		entry.DecInflight()
		metrics.UpstreamTotal.WithLabelValues("success").Inc()
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
	msg := "bad gateway"
	if lastErr != nil {
		msg = msg + ": " + lastErr.Error()
	}
	http.Error(w, msg, http.StatusBadGateway)
}

func (h *Handler) handleHTTP(w http.ResponseWriter, r *http.Request, overallStart time.Time) {
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.TotalTimeout)
	defer cancel()

	maxAttempts := 1
	if idempotent(r.Method) {
		maxAttempts = h.cfg.MaxRetries + 1
	}

	tried := map[string]bool{}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		entry, err := h.pool.SelectExcept(tried)
		if err != nil {
			lastErr = err
			break
		}

		tr, trErr := h.transport(entry)
		if trErr != nil {
			h.pool.RecordFailure(entry.Addr)
			entry.DecInflight()
			tried[entry.Addr] = true
			lastErr = trErr
			metrics.UpstreamTotal.WithLabelValues("fail").Inc()
			continue
		}

		outReq := r.Clone(ctx)
		outReq.RequestURI = ""
		removeHopHeaders(outReq.Header)

		start := time.Now()
		resp, rtErr := tr.RoundTrip(outReq)
		if rtErr != nil {
			tr.CloseIdleConnections()
			h.pool.RecordFailure(entry.Addr)
			entry.DecInflight()
			tried[entry.Addr] = true
			lastErr = rtErr
			metrics.UpstreamTotal.WithLabelValues("fail").Inc()
			h.log.Debug("http roundtrip failed", "proxy", entry.Addr, "target", r.Host, "err", rtErr)
			continue
		}

		h.pool.RecordSuccess(entry.Addr, time.Since(start))
		removeHopHeaders(resp.Header)
		copyHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		resp.Body.Close()
		entry.DecInflight()
		tr.CloseIdleConnections()
		metrics.UpstreamTotal.WithLabelValues("success").Inc()
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
	msg := "bad gateway"
	if lastErr != nil {
		msg = msg + ": " + lastErr.Error()
	}
	http.Error(w, msg, http.StatusBadGateway)
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

func hijackAndPipe(w http.ResponseWriter, upstream net.Conn) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		upstream.Close()
		http.Error(w, "hijack failed", http.StatusInternalServerError)
		return
	}
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		client.Close()
		upstream.Close()
		return
	}
	go func() {
		_, _ = io.Copy(upstream, client)
		upstream.Close()
	}()
	_, _ = io.Copy(client, upstream)
	client.Close()
}

func forward(w http.ResponseWriter, r *http.Request, tr *http.Transport) {
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	removeHopHeaders(outReq.Header)
	resp, err := tr.RoundTrip(outReq)
	if err != nil {
		http.Error(w, "direct forward failed: "+err.Error(), http.StatusBadGateway)
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
