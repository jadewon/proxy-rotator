package sources

import (
	"context"
	"log/slog"

	"github.com/jadewon/proxy-rotator/internal/pool"
	"github.com/jadewon/proxy-rotator/internal/validator"
)

type Source interface {
	Name() string
	Run(ctx context.Context, p *pool.Pool, v validator.Validator) error
}

type Factory func() (Source, error)

var registry = map[string]Factory{}

func Register(name string, f Factory) {
	registry[name] = f
}

func Build(name string) (Source, error) {
	f, ok := registry[name]
	if !ok {
		return nil, &unknownError{name: name}
	}
	return f()
}

func Names() []string {
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
