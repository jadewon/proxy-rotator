package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jadewon/proxy-rotator/internal/envutil"
	"github.com/jadewon/proxy-rotator/internal/metrics"
	"github.com/jadewon/proxy-rotator/internal/pool"
	proxysrv "github.com/jadewon/proxy-rotator/internal/proxy"
	"github.com/jadewon/proxy-rotator/internal/router"
	"github.com/jadewon/proxy-rotator/internal/sources"
	"github.com/jadewon/proxy-rotator/internal/validator"

	_ "github.com/jadewon/proxy-rotator/internal/sources/file"
	_ "github.com/jadewon/proxy-rotator/internal/sources/freeproxy"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(envutil.String("LOG_LEVEL", "info")),
	})))

	listenPort := envutil.Int("LISTEN_PORT", 3128)
	adminPort := envutil.Int("ADMIN_PORT", 8080)

	poolCfg := pool.DefaultConfig()
	poolCfg.Max = envutil.Int("POOL_MAX", poolCfg.Max)
	poolCfg.EjectConsecFails = envutil.Int("EJECT_CONSEC_FAILS", poolCfg.EjectConsecFails)
	poolCfg.QuarantineDur = envutil.Duration("QUARANTINE_DURATION", poolCfg.QuarantineDur)

	p := pool.New(poolCfg)

	testURL := envutil.String("TEST_URL", "https://example.com")
	testTimeout := envutil.Duration("TEST_TIMEOUT", 8*time.Second)
	v := validator.New(testURL, testTimeout)

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
	h := proxysrv.New(p, rt, handlerCfg)

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

	adminSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", adminPort),
		Handler: adminMux(p),
	}
	proxySrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", listenPort),
		Handler: h,
	}

	go func() {
		slog.Info("admin listening", "port", adminPort)
		if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("admin server", "err", err)
		}
	}()
	go func() {
		slog.Info("proxy listening", "port", listenPort)
		if err := proxySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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

func adminMux(p *pool.Pool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		active, _ := p.Size()
		if active < 1 {
			http.Error(w, "pool empty", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintf(w, "ok active=%d\n", active)
	})
	mux.HandleFunc("/pool", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p.Snapshot())
	})
	mux.Handle("/metrics", metrics.Handler())
	return mux
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

		bySource := map[string]float64{}
		for _, s := range p.Snapshot() {
			if s.State == "active" {
				bySource[s.Source]++
			} else {
				if _, ok := bySource[s.Source]; !ok {
					bySource[s.Source] = 0
				}
			}
		}
		for src, n := range bySource {
			metrics.PoolBySource.WithLabelValues(src).Set(n)
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
