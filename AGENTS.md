# Project Guidelines

## Source Of Truth

- Contributor setup, supported platforms, hook install, commit format, and detailed command docs: `CONTRIBUTING.md`.
- Tool wiring: `Makefile`, `lefthook.yml`, `.golangci.yml`, `gui/frontend/package.json`, `.github/workflows/*`.
- Docs disagree? Tool config wins. Update stale prose; don't copy command detail.
- Token hygiene: read `CONTRIBUTING.md` by targeted section only when setup, hooks, commit format, or full command detail is needed.

## Quick Commands

```bash
make help               # supported targets
make backend            # fast CLI build sanity
make test-go            # full Go race tests
make test-frontend      # frontend lint/dead-code/type/unit/format
make lint               # path policy + full Go lint
make logpolicy          # logging policy
make pathpolicy         # path portability policy
make precommit          # strong local validation before commit; no Go tests
make prepush            # Lefthook pre-push
make gofix-check-changed # inspect Go fix drift
git diff --check        # whitespace/conflict markers
```

Start narrow; expand for shared behavior, release, GUI/web parity, or safety-sensitive changes. Before commit: targeted tests + `make precommit`; Go behavior also needs focused `go test` or `make test-go` as risk requires. Before push: `make prepush`.

## Repository Map

- CLI `cmd/upbrr`; Wails `gui`, `internal/guiapp`; frontend `gui/frontend`; embedded web/API `internal/webserver`; core `internal/core`; API contracts `pkg/api`; services `internal/services`; trackers `internal/trackers`, `internal/trackers/impl`.

## Code Rules

- Match repo style; narrow changes; fix root cause; keep tests for changed behavior.
- Go: satisfy `.golangci.yml`; avoid broad `nolint`; wrap external/interface errors where lint requires; avoid unchecked assertions; use `testing` helpers; test file writes use `0o600` unless mode bits are under test.
- Context/logging: use context-aware APIs where meaningful. Log meaningful state/decisions/failures/retries/outcomes. No stdlib print/log under `internal/**`; satisfy `cmd/logpolicy`; redact via `internal/redaction/redaction.go`; never log credentials/tokens/API keys/cookies/secret payloads. Levels: `INFO` user-facing outcomes, `DEBUG` troubleshooting, `TRACE` high-fidelity flow.
- Go fix: no wholesale `go fix`. Prefer `make gofix-check-changed`, then package-scoped `go fix -omitzero=false <packages>`. Keep `omitzero` disabled unless JSON semantics were reviewed.

## Frontend Rules

- Keep TypeScript, ESLint, Stylelint, dead-code clean. Don't weaken rules.
- Prefer Tailwind utilities for touched local layout/spacing; keep CSS for shared/theme/cross-cutting selectors or JSX readability.
- `useEffect` only for external sync. Avoid derived state in effects; render/`useMemo` instead; handlers own user actions; fetch effects need cleanup/abort guards.
- CSS changes require `pnpm --dir gui/frontend run lint:style`; `make test-frontend` omits Stylelint.
- Embedded visual checks: rebuild/sync embedded assets + CLI, serve `dist/upbrr.exe serve --dev-no-auth` at `http://localhost:7480`; avoid Vite `5173`; stop server after inspection.
- Build caveat: `make frontend`/`make frontend-bundle` update `gui/frontend/dist` only; no asset sync/CLI rebuild. `make gui` uses current embedded assets.

## Path Portability

- Local FS paths use `filepath`; slash-data (torrent paths, URLs, API payload paths) use `path` only with import-local `//nolint:depguard // <reason>`.
- Slash-data -> local FS: validate slash path, then `filepath.FromSlash`.
- Reject POSIX + Windows escapes on every OS: leading `/`, leading `\`, drive letters, UNC, `..`.
- Use `internal/pathutil.IsWithinRoot` / `SamePath`; no ad-hoc `filepath.Rel` + prefix guards.
- Tests: `t.TempDir`, `filepath.Join`, `filepath.ToSlash`; no hardcoded OS-rooted literals/raw slash assertions for local FS.
- `cmd/pathpolicy` flags wrong path APIs, string-built local paths, slash-data FS calls/assertions, and ad-hoc guards. Rare exceptions need `//pathpolicy:allow <reason>` same/previous line.

## Product Invariants

- Preserve CLI, Wails GUI, embedded web parity. App targets Windows/Linux/macOS; avoid OS assumptions unless platform-gated.
- Preserve `api.Mode`; align requests/options/upload behavior/tracker overrides/retries/execution flags across entrypoints.
- API/runtime parity: changes to `pkg/api.Request`, `UploadOptions`, `PreparedMetadata`, dry-run/upload review, questionnaire answers, description groups, or upload options must check CLI builders, Wails methods, web routes/jobs, frontend bridge, and TS types.
- SQLite migrations: additive, forward-only, idempotent where practical; stable IDs; no destructive drop/rename/tighten; narrow `dependsOn`; preserve `schema_migrations` + legacy `user_version` bridge.
- Runtime config mutable after `SaveConfig` / `applyConfig`: read Wails/web config/core/logger via `currentConfig()`, `requireRuntime()`, or snapshots; don't read `App.cfg`/`Backend.cfg` directly outside helpers. `Server.cfg` is startup-only.
- Unattended/unattended-confirm is safety-critical: no prompts/hidden confirms/ambiguous fallthrough. If unsafe, prefer dry-run, site-check, explicit skip, or clear failure. Preserve skip/override parity for dupes, rule failures, image-host uploads, torrent injection, and retries.

## Domain Guardrails

- Tracker changes often touch `internal/trackers/impl/*`, `internal/trackers/impl/registry.go`, `internal/trackers/catalog.go`, `internal/trackers/unit3dmeta`, `internal/config/defaults/example.yaml`, and policy tests.
- Config schema changes need `internal/config.Config`, embedded defaults, import/export, env overrides as relevant, settings UI/web parity, and secret redaction/encryption review.
- Runtime bridge changes involving `globalThis.go.guiapp.App` need matching Wails `internal/guiapp` methods, web `/api/app/*` routes, browser bridge req shapes, and unit/embedded browser verification.
- Generated/built output exists locally and mostly ignored. Do not commit `dist/`, `gui/frontend/dist`, `gui/build/bin`, generated Wails bindings, or populated `internal/guiapp/assets` unless deliberately updating generated artifacts.
- `.github/workflows/*.yml` files are active; `.yml22` files are disabled templates. Keep Makefile, scripts, `CONTRIBUTING.md`, workflow pins, Wails, and pnpm aligned.

## Validation Matrix

- CLI/shared behavior: `go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api`.
- GUI/web/API parity: `go test -race -v -timeout 20m ./internal/guiapp ./internal/webserver ./internal/guishared ./pkg/api`.
- Frontend runtime/API bridge: `pnpm --dir gui/frontend run test:unit` plus `pnpm --dir gui/frontend run typecheck`.
- CSS-only frontend changes: `pnpm --dir gui/frontend run lint:style`.
- Go lint/path/log changes: `make lint`, `make logpolicy`, `make pathpolicy`, plus `make gofix-check-changed` for changed Go packages.
- Cross-area or pre-commit confidence: `make precommit`; before push: `make prepush`.
