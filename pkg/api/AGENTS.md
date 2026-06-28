# API Contract Guidelines

Scoped rules for shared API contracts under `pkg/api`. Root and `internal/AGENTS.md` rules still apply.

## Entry Points

- CLI: `cmd/upbrr`
- Core orchestration: `internal/core`
- Wails backend: `internal/guiapp`
- Embedded web server/API: `internal/webserver`
- Frontend bridge: `gui/frontend/src`

Preserve CLI, Wails GUI, and embedded web parity unless intentionally changing an entrypoint.

## Contract Changes

Changes to these require entrypoint parity review:

- `Request`
- `UploadOptions`
- `PreparedMetadata`
- dry-run/upload review payloads
- questionnaire answers
- description groups
- tracker overrides and retry/skip flags
- upload status/history rows

Check CLI builders, Wails methods, web `/api/app/*` routes, frontend bridge request shapes, and TS types.

## Checks

- CLI/core contracts: `go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api`.
- Wails/web API contracts: `go test -race -v -timeout 20m ./internal/guiapp ./internal/webserver ./internal/guishared ./pkg/api`.
- Frontend bridge/types: `pnpm --dir gui/frontend run typecheck` and `pnpm --dir gui/frontend run test:unit`.
- Embedded runtime/UI behavior: build frontend, sync embedded assets, rebuild CLI, then inspect embedded web on `http://localhost:7480`.
- Full shared-regression sweep when behavior crosses several entrypoints: `make test-go` plus relevant frontend checks.
