# API Contract Guidelines

Scoped rules for shared API contracts under `pkg/api`. Root and `internal/AGENTS.md` rules still apply.

## Entry Points

- CLI: `cmd/upbrr`
- Core orchestration: `internal/core`
- WebUI server/API: `internal/webserver`
- Browser clients/types: `webui/src`

Preserve CLI and WebUI behavior unless intentionally changing an entrypoint.

## Contract Changes

Changes to these require entrypoint parity review:

- `Request`
- `UploadOptions`
- `PreparedRelease`, `PrepareResult`, and grouped canonical facts
- operation-specific inputs and exact `ReleaseRef` generations
- dry-run/upload review payloads
- questionnaire answers
- description groups
- tracker overrides and retry/skip flags
- upload status/history rows
- `PreparationProgressUpdate` and `ImageUploadProgressUpdate`
- `OperationFailure` codes and recovery metadata

Check CLI builders, WebUI `/api/app/*` routes, browser request shapes, and TS types.

## Canonical / Presentation Boundaries

- Canonical request and preparation contracts are single-source and operation inputs reference one exact `ReleaseRef` generation.
- Keep prepared facts typed and reusable; do not add presentation correlation or workflow state to canonical inputs or facts.
- WebUI transport injects progress correlation IDs and timestamps. Progress payloads must be frontend-safe; image-host updates are absolute snapshots for one host attempt.
- Recovery decisions use structured `OperationFailure` codes/metadata, never error-message substring matching.

## Checks

- CLI/core contracts: `go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api`.
- WebUI API contracts: `go test -race -v -timeout 20m ./internal/webserver/... ./pkg/api`.
- Browser clients/types: `pnpm --dir webui run typecheck` and `pnpm --dir webui run test:unit`.
- Embedded runtime/UI behavior: build frontend, sync embedded assets, rebuild CLI, then inspect embedded web on `http://localhost:7480`.
- Full shared-regression sweep when behavior crosses several entrypoints: `make test-go` plus relevant frontend checks.
