# Contributing

Thanks for your interest in improving proxy-rotator.

## Development

```bash
go build ./...
go test ./...
go vet ./...
```

Build a local binary:

```bash
go build -o /tmp/proxy-rotator ./cmd/proxy-rotator
```

Build the Docker image:

```bash
docker build -f deploy/Dockerfile -t proxy-rotator:dev .
```

## Pull Requests

1. Fork the repo and create a topic branch off `main`.
2. Keep changes focused — one logical change per PR.
3. Ensure `go build ./...`, `go test ./...`, and `go vet ./...` all pass. CI will run these.
4. Update `README.md` / `PRD.md` when you change behavior, env vars, or architecture.
5. Add an entry under `Unreleased` in `CHANGELOG.md`.

## Adding a Proxy Source Plugin

1. Create `internal/sources/<name>/<name>.go`.
2. Implement the `sources.Source` interface (see `internal/sources/source.go`).
3. Register the factory inside `init()` with `sources.Register(Name, factory)`.
4. Add a blank import to `cmd/proxy-rotator/main.go`: `_ "github.com/jadewon/proxy-rotator/internal/sources/<name>"`.
5. Document new environment variables in `README.md`.

See `internal/sources/file` and `internal/sources/freeproxy` for reference implementations.

## Coding Style

- Follow standard Go conventions (`gofmt`, effective Go).
- Prefer composition over inheritance / large interfaces.
- Keep the central `Pool` as the single source of truth — sources add, feedback loop removes.
- Avoid adding dependencies unless necessary. The core binary is designed to stay small and static.

## Reporting Issues

Please include:
- proxy-rotator version / image tag
- Go version (if building from source)
- Relevant environment variables (redact secrets)
- Minimal reproduction steps
- Logs at `LOG_LEVEL=debug` when possible
