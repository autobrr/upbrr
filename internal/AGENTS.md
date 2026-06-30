# Backend Guidelines

Scoped rules for backend Go under `internal/`. Root repo rules still apply.

## Source Of Truth

- Go lint config: `.golangci.yml`
- Hook wiring: `lefthook.yml`
- Make targets: `Makefile`
- API/runtime contracts: `pkg/api`, `cmd/upbrr`, `internal/guiapp`, `internal/webserver`, `gui/frontend`

Tool output and config win over prose.

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

## Check Selection

- Touched Go package: `go test -race -v -timeout 20m <package>`.
- CLI behavior/flags/prompts: `go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api` plus touched services/trackers, then `make backend`.
- Core upload flow, tracker orchestration, config, DB, or API contracts: run focused package tests, then add `make test-go` when shared behavior can regress broadly.
- Wails/web/embedded API parity: `go test -race -v -timeout 20m ./internal/guiapp ./internal/webserver ./internal/guishared ./pkg/api`; add frontend `typecheck`/unit checks for request/response or bridge changes.
- Tracker changes: test the tracker impl package and touched shared tracker packages; include config/defaults and catalog tests when definitions or auth material change.
- Logging/internal Go changes: `make logpolicy`.
- Path handling/local FS changes: `make pathpolicy`; use `make lint` before commit.
- Go modernization drift: `make gofix-check-changed`; use package-scoped `go fix -omitzero=false <packages>` only after review.

## Runtime Flow

1. Entrypoints build request/options from CLI args, Wails method input, or web route payload.
2. `internal/core` prepares metadata, config, services, repository access, validation, screenshots/images, tracker review, and upload.
3. Services under `internal/services` handle metadata, torrents, image hosts, dupe checks, screenshots, and tracker orchestration.
4. Tracker implementations under `internal/trackers/impl` produce tracker-specific payloads and rule handling.
5. DB/repository layers persist config, history, images, upload records, and status.

Preserve behavior across CLI, Wails GUI, and embedded web unless intentionally changing an entrypoint.

## Config / Runtime Ownership

- Runtime config can change after `SaveConfig` / `applyConfig`.
- Read Wails/web config/core/logger via `currentConfig()`, `requireRuntime()`, or snapshots.
- Do not read `App.cfg` / `Backend.cfg` directly outside helpers.
- `Server.cfg` is startup-only.
- Config schema changes need `internal/config.Config`, embedded defaults, import/export, env overrides where relevant, settings UI/web parity, and secret redaction/encryption review.

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
- Follow root log-level guidance with backend logger methods: `Infof`, `Warnf`, `Debugf`, and `Tracef`.
- Redact auth status text, remote response details, URLs, and raw errors before logging when they can contain secrets; use `internal/redaction.RedactValue` or tracker/common redaction helpers.
- No stdlib print/log under `internal/**`.
- Satisfy `cmd/logpolicy`.
- Redact via `internal/redaction/redaction.go`.
- Never log credentials, usernames, passwords, tokens, API keys, auth keys, passkeys, cookie values, 2FA codes, challenge IDs, refreshed API tokens, or secret payloads.

## Path Portability

- Local FS paths use `filepath`.
- Slash-data such as torrent paths, URLs, and API payload paths use `path` only with import-local `//nolint:depguard // <reason>`.
- Slash-data -> local FS: validate slash path, then `filepath.FromSlash`.
- Reject POSIX + Windows escapes on every OS: leading `/`, leading `\`, drive letters, UNC, `..`.
- Use `internal/pathutil.IsWithinRoot` / `SamePath`; no ad-hoc `filepath.Rel` + prefix guards.
- Tests: `t.TempDir`, `filepath.Join`, `filepath.ToSlash`; no hardcoded OS-rooted literals/raw slash assertions for local FS.
- `cmd/pathpolicy` flags wrong path APIs, string-built local paths, slash-data FS calls/assertions, and ad-hoc guards. Rare exceptions need `//pathpolicy:allow <reason>` same/previous line.

## Lint / Hook Policy

- Pre-commit hook: Go format, log policy, path policy, frontend Prettier/ESLint on staged files.
- Pre-push hook: `make lint` and frontend typecheck.
- Do not rely on `make prepush` before a commit exists. Run relevant underlying checks before commit when the change can affect them.
- `make lint` runs path policy plus full `golangci-lint run --timeout=5m ./...`.
- Fix checker failures in the smallest relevant scope. Do not weaken checks, remove tests, or add broad `nolint` to hide failures.

## Generated / Scratch Path Risk

`.gitignore` does not protect Go package discovery. `golangci-lint run ./...` and other Go tooling can still find ignored `.go` files under repo-local scratch paths.

- Do not leave scratch `.go` files under repo paths like `tmp/`.
- If generated/scratch `.go` files are expected under an ignored directory, add a tool-level exclusion such as `.golangci.yml` `linters.exclusions.paths`.
- After creating generated dirs or broad Go tooling, run `make lint` before commit.
- Keep generated artifacts out of commits unless the task explicitly updates generated output.

Current expected local/generated ignores: `dist/`, `gui/frontend/dist/`, `gui/build/bin/`, `internal/guiapp/assets/*` except `.keep`, `gui/frontend/playwright-report/`, `gui/frontend/test-results/`, `tmp/`.

## Domain Guardrails

- Tracker changes often touch `internal/trackers/impl/*`, `internal/trackers/impl/registry.go`, `internal/trackers/catalog.go`, `internal/trackers/unit3dmeta`, `internal/config/defaults/example.yaml`, and policy tests.
- DB schema changes use stable, additive, forward-only, idempotent SQLite migrations where practical; preserve `schema_migrations` and the legacy `user_version` bridge.
- Runtime bridge changes involving `globalThis.go.guiapp.App` need matching Wails `internal/guiapp` methods, web `/api/app/*` routes, browser bridge request shapes, and unit/embedded browser verification.
- Generated/built outputs are mostly ignored; do not commit populated `internal/guiapp/assets` unless deliberately updating generated artifacts.
