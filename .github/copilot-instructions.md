# upbrr - Repository Instructions

## Project Overview

upbrr is a Go-based upload preparation and tracker submission tool for private-tracker workflows. It ships three surfaces from a shared core:

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
- `.github/workflows/` - CI for tests (test.yml), linting (golangci-lint.yml), binary packaging (build-binaries.yml)
- `.golangci.yml` - Go linter configuration (source of truth for lint rules)

## Tech Stack & Versions

- **Go 1.26** - backend language
- **Node.js 20** - frontend tooling
- **pnpm 10** - frontend package manager
- **Wails v2.10.1** - desktop GUI framework
- **React 18** + Vite 5 + TypeScript 5 + Tailwind 4 - frontend
- **SQLite** (modernc.org/sqlite) - embedded database, no external DB server
- **golangci-lint** - Go linting

## Build & Validation Commands

### Go Backend

```bash
# Run CLI
go run ./cmd/upbrr [args...]

# Run GUI via CLI flag
go run ./cmd/upbrr --gui

# Run GUI directly
go run ./gui

# Run web server
go run ./cmd/upbrr serve

# Run tests (package-scoped)
go test -v ./internal/core/

# Run all tests
go test -v -timeout 20m ./...

# Lint
golangci-lint run --timeout=5m

# Build CLI binary
go build -o dist/upbrr ./cmd/upbrr
```

### Frontend (working directory: gui/frontend)

```bash
pnpm install --frozen-lockfile
pnpm run lint
pnpm run typecheck
pnpm run build
pnpm run dev          # Vite dev server on :5173
```

### Full Build (scripts)

```powershell
# Windows
.\scripts\build.ps1

# Unix/macOS
./scripts/build.sh
```

## Code Style

- Format Go with gofmt and goimports. Local import prefix: `github.com/autobrr/upbrr`
- `.golangci.yml` is the lint source of truth. Enabled linters: copyloopvar, errname, errorlint, exhaustive, fatcontext, gocritic, gosec, loggercheck, mirror, misspell, perfsprint, prealloc, rowserrcheck, spancheck, testifylint, unconvert, unused, whitespace
- Disabled linters (intentional policy): containedctx, noctx, revive
- Frontend: TypeScript strict, ESLint clean, no rule weakening

## Key Types

- `api.Request` - unified request struct (paths, mode, options, overrides)
- `api.Core` - 18-method orchestration interface (RunUpload, FetchMetadataPreview, CheckDupes, etc.)
- `api.Mode` - ModeCLI or ModeGUI string constant
- `api.ServiceSet` - 8 service interfaces (Metadata, Tracker, Torrent, Client, Filesystem, Dupe, Screenshot, ImageHosting)
- `api.Logger` - interface with Tracef/Debugf/Infof/Warnf/Errorf methods

## Key Invariants

- CLI, GUI, and web-serve mode share request construction and upload behavior via `pkg/api` types and `internal/core`
- Cross-platform: Windows, Linux, macOS. No OS-specific assumptions unless intentionally gated
- Unattended/unattended-confirm flows are safety-critical and must stay non-blocking
- site-check implies dry-run; debug implies safe non-upload behavior

## CI Workflows

- `test.yml` - Go tests + frontend lint/typecheck
- `golangci-lint.yml` - Go linting with golangci-lint
- `build-binaries.yml` - multi-platform binary builds (CLI + GUI) and Docker images
