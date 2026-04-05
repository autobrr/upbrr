@AGENTS.md

# upbrr

Upload preparation and tracker submission tool for private-tracker workflows, written in Go.

## Surfaces

- **CLI** (`cmd/upbrr`) - interactive and unattended command-line workflow
- **Desktop GUI** (`gui`) - Wails v2 desktop app with React/Vite frontend
- **Web mode** (`upbrr serve`) - embedded web server serving the same frontend

Module: `github.com/autobrr/upbrr`

## Repository Layout

- `cmd/upbrr/` - CLI entrypoint (main.go, cli_options.go, interactive.go)
- `gui/` - Wails GUI entrypoint and config (wails.json)
- `gui/frontend/` - React + Vite + Tailwind + TypeScript frontend (pnpm)
- `internal/` - core business logic, services, tracker implementations, GUI backend bindings, web server
- `internal/core/` - upload orchestration, metadata, descriptions, screenshots
- `internal/config/` - YAML import/export, SQLite persistence
- `internal/dupechecking/` - 30+ tracker-specific dupe check implementations
- `internal/guiapp/` - Wails GUI backend bindings and embedded assets
- `internal/webserver/` - embedded web server
- `pkg/api/` - shared request/response types and interfaces across all surfaces
- `scripts/` - build helpers (build.ps1, build.sh)
- `.github/workflows/` - CI for tests, linting, binary packaging
- `.golangci.yml` - Go linter configuration (source of truth for lint rules)

## Tech Stack

- Go 1.26, Node.js 20, pnpm 10, Wails v2.10.1
- React 18 + Vite 5 + TypeScript 5 + Tailwind 4
- SQLite (modernc.org/sqlite) — embedded, no external DB server
- golangci-lint for Go linting

## Build & Validate

### Go Backend

```bash
go run ./cmd/upbrr [args...]          # Run CLI
go run ./cmd/upbrr --gui              # Run GUI via CLI
go run ./gui                          # Run GUI directly
go run ./cmd/upbrr serve              # Run web server
go test -v ./internal/core/           # Package-scoped tests
go test -v -timeout 20m ./...         # All tests
golangci-lint run --timeout=5m        # Lint
go build -o dist/upbrr ./cmd/upbrr    # Build CLI binary
```

### Frontend (cwd: gui/frontend)

```bash
pnpm install --frozen-lockfile
pnpm run lint
pnpm run typecheck
pnpm run build
pnpm run dev                          # Vite dev server on :5173
```

### Full Build

```powershell
.\scripts\build.ps1                   # Windows
./scripts/build.sh                    # Unix/macOS
```

## Key Types

- `api.Request` - unified request struct (paths, mode, options, overrides)
- `api.Core` - 18-method orchestration interface (RunUpload, FetchMetadataPreview, CheckDupes, etc.)
- `api.Mode` - ModeCLI or ModeGUI string constant
- `api.ServiceSet` - 8 service interfaces (Metadata, Tracker, Torrent, Client, Filesystem, Dupe, Screenshot, ImageHosting)
- `api.Logger` - interface with Tracef/Debugf/Infof/Warnf/Errorf methods
- `api.PreparedMetadata` - cached metadata struct keyed by (path, signature) in dupe cache

## CI

- `test.yml` — Go tests + frontend lint/typecheck
- `golangci-lint.yml` — Go linting
- `build-binaries.yml` — multi-platform binary builds (CLI + GUI) and Docker images
