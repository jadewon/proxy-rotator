package sources

import (
	"context"
	"log/slog"
	"sync"

	"github.com/jadewon/proxy-rotator/internal/pool"
	"github.com/jadewon/proxy-rotator/internal/validator"
)

type Source interface {
	Name() string
	Run(ctx context.Context, p *pool.Pool, v validator.Validator) error
}

type Factory func() (Source, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

func Register(name string, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = f
}

func Build(name string) (Source, error) {
	registryMu.RLock()
	f, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return nil, &unknownError{name: name}
	}
	return f()
}

func Names() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

type unknownError struct{ name string }

func (e *unknownError) Error() string { return "sources: unknown source " + e.name }

func Logger(name string) *slog.Logger {
	return slog.With("source", name)
}
