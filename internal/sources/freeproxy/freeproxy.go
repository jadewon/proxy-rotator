package freeproxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jadewon/proxy-rotator/internal/envutil"
	"github.com/jadewon/proxy-rotator/internal/metrics"
	"github.com/jadewon/proxy-rotator/internal/pool"
	"github.com/jadewon/proxy-rotator/internal/sources"
	"github.com/jadewon/proxy-rotator/internal/validator"
)

const Name = "freeproxy"

const defaultURL = "https://raw.githubusercontent.com/TheSpeedX/PROXY-List/master/socks5.txt"

func init() {
	sources.Register(Name, func() (sources.Source, error) {
		return &Source{
			URL:         envutil.String("SOURCE_FREEPROXY_URL", defaultURL),
			Interval:    envutil.Duration("SOURCE_FREEPROXY_INTERVAL", 10*time.Minute),
			IntervalLow: envutil.Duration("SOURCE_FREEPROXY_INTERVAL_LOW", 30*time.Second),
			PoolMin:     envutil.Int("POOL_MIN", 5),
			Concurrency: envutil.Int("SOURCE_FREEPROXY_CONCURRENCY", 20),
			HTTPTimeout: envutil.Duration("SOURCE_FREEPROXY_HTTP_TIMEOUT", 30*time.Second),
		}, nil
	})
}

type Source struct {
	URL         string
	Interval    time.Duration
	IntervalLow time.Duration
	PoolMin     int
	Concurrency int
	HTTPTimeout time.Duration
}

func (s *Source) Name() string { return Name }

func (s *Source) Run(ctx context.Context, p *pool.Pool, v validator.Validator) error {
	log := sources.Logger(Name)

	s.sync(ctx, log, p, v)
	for {
		wait := s.Interval
		if active, _ := p.Size(); active < s.PoolMin {
			wait = s.IntervalLow
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
		s.sync(ctx, log, p, v)
	}
}

func (s *Source) sync(ctx context.Context, log *slog.Logger, p *pool.Pool, v validator.Validator) {
	raws, err := s.fetch(ctx)
	if err != nil {
		log.Warn("fetch failed", "url", s.URL, "err", err)
		return
	}
	log.Info("fetched candidates", "count", len(raws))

	sem := make(chan struct{}, s.Concurrency)
	var wg sync.WaitGroup
	var added, validated int
	var mu sync.Mutex

	for _, raw := range raws {
		select {
		case <-ctx.Done():
			return
		default:
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(r validator.RawProxy) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := v.Validate(ctx, r); err != nil {
				metrics.VerifyTotal.WithLabelValues(Name, "fail").Inc()
				return
			}
			metrics.VerifyTotal.WithLabelValues(Name, "pass").Inc()
			mu.Lock()
			validated++
			mu.Unlock()
			ok := p.Add(pool.AddInput{
				Addr:     r.Addr,
				Source:   Name,
				Auth:     r.Auth,
				Metadata: r.Metadata,
			})
			if ok {
				mu.Lock()
				added++
				mu.Unlock()
			}
		}(raw)
	}
	wg.Wait()
	log.Info("sync done", "validated", validated, "added", added)
}

func (s *Source) fetch(ctx context.Context) ([]validator.RawProxy, error) {
	reqCtx, cancel := context.WithTimeout(ctx, s.HTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: s.HTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var out []validator.RawProxy
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "socks5://")
		if !strings.Contains(line, ":") {
			continue
		}
		out = append(out, validator.RawProxy{Addr: line})
	}
	return out, sc.Err()
}
