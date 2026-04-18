package file

import (
	"bufio"
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jadewon/proxy-rotator/internal/envutil"
	"github.com/jadewon/proxy-rotator/internal/metrics"
	"github.com/jadewon/proxy-rotator/internal/pool"
	"github.com/jadewon/proxy-rotator/internal/sources"
	"github.com/jadewon/proxy-rotator/internal/validator"
)

const Name = "file"

func init() {
	sources.Register(Name, func() (sources.Source, error) {
		return &Source{
			Path:        envutil.String("SOURCE_FILE_PATH", "/etc/proxy-rotator/proxies.txt"),
			Interval:    envutil.Duration("SOURCE_FILE_INTERVAL", 60*time.Second),
			Concurrency: envutil.Int("SOURCE_FILE_CONCURRENCY", 10),
		}, nil
	})
}

type Source struct {
	Path        string
	Interval    time.Duration
	Concurrency int
}

func (s *Source) Name() string { return Name }

func (s *Source) Run(ctx context.Context, p *pool.Pool, v validator.Validator) error {
	log := sources.Logger(Name)
	tick := time.NewTicker(s.Interval)
	defer tick.Stop()

	s.sync(ctx, log, p, v)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			s.sync(ctx, log, p, v)
		}
	}
}

func (s *Source) sync(ctx context.Context, log *slog.Logger, p *pool.Pool, v validator.Validator) {
	raws, err := s.load()
	if err != nil {
		log.Warn("load failed", "path", s.Path, "err", err)
		return
	}
	log.Info("loaded candidates", "count", len(raws))

	sem := make(chan struct{}, s.Concurrency)
	var wg sync.WaitGroup
	var added int
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
				log.Debug("validate failed", "addr", r.Addr, "err", err)
				return
			}
			metrics.VerifyTotal.WithLabelValues(Name, "pass").Inc()
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
	log.Info("sync done", "added", added)
}

func (s *Source) load() ([]validator.RawProxy, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []validator.RawProxy
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "socks5://")
		out = append(out, validator.RawProxy{Addr: line})
	}
	return out, sc.Err()
}
