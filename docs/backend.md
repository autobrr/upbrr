# Backend Guidelines

Scoped reference for Go, path/log policy, domain rules, and backend validation.

## Commands

```bash
make backend
make test-go
make lint
make logpolicy
make pathpolicy
make gofix-check-changed
go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api
go test -race -v -timeout 20m ./internal/guiapp ./internal/webserver ./internal/guishared ./pkg/api
```

## Go Rules

- Match repo style; keep changes narrow; fix root cause; keep tests for changed behavior.
- Satisfy `.golangci.yml`; avoid broad `nolint`.
- Wrap external/interface errors where lint requires.
- Avoid unchecked assertions.
- Use `testing` helpers.
- Test file writes use `0o600` unless mode bits are under test.
- No wholesale `go fix`. Prefer `make gofix-check-changed`, then package-scoped `go fix -omitzero=false <packages>`.
- Keep `omitzero` disabled unless JSON semantics were reviewed.

## Logging / Redaction

- Use context-aware APIs where meaningful.
- Log meaningful decisions, failures, retries, and outcomes.
- No stdlib print/log under `internal/**`.
- Satisfy `cmd/logpolicy`.
- Redact via `internal/redaction/redaction.go`.
- Never log credentials, tokens, API keys, cookies, or secret payloads.

## Path Portability

- Local FS paths use `filepath`.
- Slash-data such as torrent paths, URLs, and API payload paths use `path` only with import-local `//nolint:depguard // <reason>`.
- Slash-data -> local FS: validate slash path, then `filepath.FromSlash`.
- Reject POSIX + Windows escapes on every OS: leading `/`, leading `\`, drive letters, UNC, `..`.
- Use `internal/pathutil.IsWithinRoot` / `SamePath`; no ad-hoc `filepath.Rel` + prefix guards.
- Tests: `t.TempDir`, `filepath.Join`, `filepath.ToSlash`; no hardcoded OS-rooted literals/raw slash assertions for local FS.
- `cmd/pathpolicy` flags wrong path APIs, string-built local paths, slash-data FS calls/assertions, and ad-hoc guards. Rare exceptions need `//pathpolicy:allow <reason>` same/previous line.

## Domain Guardrails

- Tracker changes often touch `internal/trackers/impl/*`, `internal/trackers/impl/registry.go`, `internal/trackers/catalog.go`, `internal/trackers/unit3dmeta`, `internal/config/defaults/example.yaml`, and policy tests.
- Config schema changes need `internal/config.Config`, embedded defaults, import/export, env overrides as relevant, settings UI/web parity, and secret redaction/encryption review.
- Runtime bridge changes involving `globalThis.go.guiapp.App` need matching Wails `internal/guiapp` methods, web `/api/app/*` routes, browser bridge request shapes, and unit/embedded browser verification.
