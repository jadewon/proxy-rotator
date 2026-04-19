package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jadewon/proxy-rotator/internal/auth"
	"github.com/jadewon/proxy-rotator/internal/envutil"
	"github.com/jadewon/proxy-rotator/internal/guard"
	"github.com/jadewon/proxy-rotator/internal/metrics"
	"github.com/jadewon/proxy-rotator/internal/pool"
	proxysrv "github.com/jadewon/proxy-rotator/internal/proxy"
	"github.com/jadewon/proxy-rotator/internal/router"
	"github.com/jadewon/proxy-rotator/internal/sources"
	"github.com/jadewon/proxy-rotator/internal/validator"

	_ "github.com/jadewon/proxy-rotator/internal/sources/file"
	_ "github.com/jadewon/proxy-rotator/internal/sources/freeproxy"
)

const defaultTestURL = "https://example.com"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(envutil.String("LOG_LEVEL", "info")),
	})))

	listenAddr := envutil.String("LISTEN_ADDR", "127.0.0.1")
	listenPort := envutil.Int("LISTEN_PORT", 3128)
	adminAddr := envutil.String("ADMIN_ADDR", "0.0.0.0")
	adminPort := envutil.Int("ADMIN_PORT", 8080)

	authChecker := auth.New(
		envutil.String("PROXY_USERNAME", ""),
		envutil.String("PROXY_PASSWORD", ""),
	)
	if !authChecker.Enabled && !isLoopback(listenAddr) {
		slog.Error("refusing to bind non-loopback proxy port without PROXY_USERNAME/PROXY_PASSWORD",
			"listen_addr", listenAddr, "hint", "set credentials or LISTEN_ADDR=127.0.0.1")
		os.Exit(2)
	}

	guardCfg := guard.Config{AllowPrivate: envutil.Bool("ALLOW_PRIVATE_TARGETS", false)}

	poolCfg := pool.DefaultConfig()
	poolCfg.Max = envutil.Int("POOL_MAX", poolCfg.Max)
	poolCfg.EjectConsecFails = envutil.Int("EJECT_CONSEC_FAILS", poolCfg.EjectConsecFails)
	poolCfg.QuarantineDur = envutil.Duration("QUARANTINE_DURATION", poolCfg.QuarantineDur)

	p := pool.New(poolCfg)

	testURL := envutil.String("TEST_URL", defaultTestURL)
	if testURL == defaultTestURL {
		slog.Warn("TEST_URL is the default placeholder; set it to your real target URL for meaningful validation",
			"test_url", testURL)
	}
	testTimeout := envutil.Duration("TEST_TIMEOUT", 8*time.Second)
	matchBody := envutil.String("VERIFY_MATCH_BODY", "")
	v := validator.New(testURL, testTimeout, matchBody)

	rt := router.New(
		envutil.StringSlice("MATCH_HOSTS", ""),
		envutil.StringSlice("BYPASS_HOSTS", ".cluster.local,.svc,localhost"),
		router.ParseAction(envutil.String("DEFAULT_ACTION", "proxy")),
	)

	handlerCfg := proxysrv.Config{
		MaxRetries:      envutil.Int("MAX_RETRIES", 2),
		PerProxyTimeout: envutil.Duration("PER_PROXY_TIMEOUT", 8*time.Second),
		TotalTimeout:    envutil.Duration("TOTAL_TIMEOUT", 30*time.Second),
	}
	h := proxysrv.New(p, rt, authChecker, guardCfg, handlerCfg)

	maxRequestBody := int64(envutil.Int("MAX_REQUEST_BODY", 10*1024*1024))
	proxyHandler := withBodyLimit(h, maxRequestBody)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var wg sync.WaitGroup
	for _, name := range envutil.StringSlice("SOURCES", "file") {
		src, err := sources.Build(name)
		if err != nil {
			slog.Error("unknown source", "name", name, "err", err)
			continue
		}
		wg.Add(1)
		go func(s sources.Source) {
			defer wg.Done()
			slog.Info("source started", "name", s.Name())
			if err := s.Run(ctx, p, v); err != nil {
				slog.Error("source exited", "name", s.Name(), "err", err)
			}
		}(src)
	}

	go poolMetricsLoop(ctx, p)

	// Startup gate: becomes true once the pool first has an active entry OR
	// the grace period elapses (whichever comes first). The grace fallback
	// prevents the pod from restart-looping when upstream proxy sources are
	// transiently unavailable at boot.
	var startupDone atomic.Bool
	startupGrace := envutil.Duration("STARTUP_GRACE", 30*time.Second)
	go watchStartup(ctx, p, &startupDone, startupGrace)

	readHeaderTimeout := envutil.Duration("READ_HEADER_TIMEOUT", 5*time.Second)
	idleTimeout := envutil.Duration("IDLE_TIMEOUT", 120*time.Second)
	maxHeaderBytes := envutil.Int("MAX_HEADER_BYTES", 1<<20) // 1 MiB

	adminSrv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", adminAddr, adminPort),
		Handler:           adminMux(p, &startupDone),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
	proxySrv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", listenAddr, listenPort),
		Handler:           proxyHandler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}

	go func() {
		slog.Info("admin listening", "addr", adminSrv.Addr)
		if err := adminSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("admin server", "err", err)
		}
	}()
	go func() {
		slog.Info("proxy listening", "addr", proxySrv.Addr, "auth", authChecker.Enabled)
		if err := proxySrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("proxy server", "err", err)
		}
	}()

	<-ctx.Done()
	slog.Info("shutdown signal received")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	_ = proxySrv.Shutdown(shutCtx)
	_ = adminSrv.Shutdown(shutCtx)
	wg.Wait()
}

// withBodyLimit wraps a handler to cap request body size. CONNECT tunnels
// bypass this since body semantics do not apply.
func withBodyLimit(next http.Handler, n int64) http.Handler {
	if n <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, n)
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(addr string) bool {
	addr = strings.TrimSpace(addr)
	return addr == "127.0.0.1" || addr == "::1" || addr == "localhost"
}

func adminMux(p *pool.Pool, startupDone *atomic.Bool) http.Handler {
	mux := http.NewServeMux()

	// livez: process is alive. Always 200 when the admin server responds.
	// Use as livenessProbe so a temporarily empty pool does not trigger a
	// restart loop.
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})

	// readyz: ready to serve proxied traffic (pool has at least one active
	// entry). Use as readinessProbe so Services remove the pod from
	// endpoints when the pool drains.
	readyHandler := func(w http.ResponseWriter, r *http.Request) {
		active, _ := p.Size()
		if active < 1 {
			http.Error(w, "pool empty", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintf(w, "ok active=%d\n", active)
	}
	mux.HandleFunc("/readyz", readyHandler)
	// healthz is kept as a backward-compatible alias for readyz.
	mux.HandleFunc("/healthz", readyHandler)

	// startupz: boot has completed. Returns 200 once either the pool has
	// served its first active entry or STARTUP_GRACE elapses. Use as
	// startupProbe to prevent slow source fetches from failing liveness.
	mux.HandleFunc("/startupz", func(w http.ResponseWriter, r *http.Request) {
		if startupDone.Load() {
			_, _ = w.Write([]byte("ok\n"))
			return
		}
		http.Error(w, "starting", http.StatusServiceUnavailable)
	})

	mux.HandleFunc("/pool", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p.Snapshot())
	})
	mux.Handle("/metrics", metrics.Handler())
	return mux
}

func watchStartup(ctx context.Context, p *pool.Pool, done *atomic.Bool, grace time.Duration) {
	deadline := time.After(grace)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			if done.CompareAndSwap(false, true) {
				slog.Info("startup grace elapsed", "grace", grace)
			}
			return
		case <-t.C:
			if active, _ := p.Size(); active >= 1 {
				if done.CompareAndSwap(false, true) {
					slog.Info("startup complete", "active", active)
				}
				return
			}
		}
	}
}

func poolMetricsLoop(ctx context.Context, p *pool.Pool) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		active, quarantine := p.Size()
		metrics.PoolSize.WithLabelValues("active").Set(float64(active))
		metrics.PoolSize.WithLabelValues("quarantine").Set(float64(quarantine))

		// Reset first so sources that disappeared fall to zero and then
		// drop out of the registry, instead of stuck at the last value.
		metrics.PoolBySource.Reset()
		for _, s := range p.Snapshot() {
			if s.State == "active" {
				metrics.PoolBySource.WithLabelValues(s.Source).Inc()
			}
		}
	}
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}
