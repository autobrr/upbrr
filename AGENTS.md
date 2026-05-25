# Project Guidelines

## Code Quality

- Match repo style. Keep changes narrow. Fix root causes, not symptoms.
- Format Go with `gofmt` and `goimports`. Use local prefix `github.com/autobrr/upbrr`.
- Treat `.golangci.yml` as Go source of truth. New Go code must satisfy enabled linters and formatters. Avoid broad `nolint`.
- `containedctx` is only disabled Go linter. Treat it as existing-code exception, not license for new context fields.
- Active checks include `noctx`, `contextcheck`, `wrapcheck`, `revive`, `forcetypeassert`, `unparam`, `usetesting`, and `gosec`.
- Use context-aware APIs. Propagate context where meaningful; terminate it deliberately when crossing into root/background work.
- Wrap external-package errors where lint requires it. Handle errors by returning, wrapping, logging with useful context, or making intentional ignore paths obvious.
- Avoid unchecked type assertions. Use `testing` helpers in tests. Justify narrow `nolint` at source.
- Do not reshape existing code only for disabled lint policy unless functional need exists or nearby code already follows that pattern.
- Add logs where they explain meaningful state, decisions, failures, retries, or user-visible outcomes. Improve touched functions when practical and relevant.
- Treat `cmd/logpolicy` as logging contract. Fix flagged log messages or levels at source; do not weaken checker or move noise sideways.
- Redact secrets and user-sensitive data with `internal/redaction/redaction.go`. Never log credentials, tokens, API keys, passkeys, cookies, or secret-bearing payloads without that standard.
- Keep log levels purposeful: `INFO` for concise user-facing upload progress/outcomes, `DEBUG` for troubleshooting context, `TRACE` for high-fidelity operational flow.
- Respect current golangci-lint exclusions and formatter settings. Avoid churn in files covered by scoped exceptions.
- Frontend: keep TypeScript and ESLint clean. Do not weaken rules or bypass type errors.
- Frontend CSS: keep Stylelint clean. Avoid dead selectors, files, exports, and dependencies. Dead-code check uses `knip.ts` plus CSS compiler hook over `src/**/*.{ts,tsx,css}`.
- Commits must satisfy `cmd/commitmsgcheck`: `type(scope): subject`; optional scope; lower-case imperative subject; no trailing period; max 115 chars. Allowed types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`.
- Treat `lefthook.yml` as local preflight contract. Do not bypass hooks or validation unless user explicitly asks, or bypass is temporary WIP that gets validated before handoff.

## Validation

- Run relevant CI-aligned checks after changes.
- PR validation can include commit-message validation, full Go tests, golangci-lint, logpolicy, and frontend lint/stylelint/type/format/dead-code checks. Use `lefthook.yml`, `CONTRIBUTING.md`, `gui/frontend/package.json`, and `.github/workflows/*` as source of truth.
- Go: run narrow package tests first. If change touches shared behavior, multiple packages, or cross-surface flows, expand up to `go test -v -timeout 20m ./...`.
- Go lint: run `golangci-lint run --timeout=5m ./...`.
- Logging changes under `internal`: run `go run ./cmd/logpolicy`.
- Commit checks: run `go run ./cmd/commitmsgcheck <commit-msg-file>` or `go run ./cmd/commitmsgcheck --from <base> --to <head>`.
- If Lefthook is installed and task includes staged changes or push, run matching hook when practical: `lefthook run pre-commit` or `lefthook run pre-push`.
- Frontend: prefer smallest relevant validation for changed files, but keep affected surface lint/typecheck clean.
- Broad frontend/config changes: from `gui/frontend`, run `pnpm run lint`, `pnpm run lint:dead`, `pnpm run typecheck`, and `pnpm run format:check`.
- Frontend dependency or lockfile-sensitive changes: run `pnpm install --frozen-lockfile`.
- CSS changes: also run `pnpm run lint:style`.
- Frontend build logic, embedded assets, or Vite/TypeScript config: also run `pnpm run build`.
- Wails runtime/backend changes: validate with `go run ./gui` when practical.
- GUI packaging, embedded assets, Wails config, or desktop integration: run `pnpm run build` in `gui/frontend` plus nearest relevant Wails build validation.
- Packaging/release/Docker/build-script/cross-platform changes: review `.github/workflows/build-binaries.yml` and validate directly affected local path, such as `scripts/build.sh`, `scripts/build.ps1`, CLI build, GUI build, or Docker build.

## Product Invariants

- Shared behavior spans CLI, Wails GUI, and embedded web-serving mode. Preserve parity in request construction, options, and upload behavior where practical.
- App targets Windows, Linux, and macOS. Avoid OS-specific path, process, filesystem, archive, or build assumptions unless intentionally platform-gated.
- Preserve `api.Mode` usage. Keep CLI, Wails GUI, and embedded web flows aligned with request types under `pkg/api` and shared core behavior under `internal`.
- Upload options, tracker overrides, retries, and execution flags should be checked from CLI and GUI entrypoints when shared.
- SQLite databases may be shared across branches during development. Keep migrations compatible with permissive cross-branch use.
- Migrations must be additive, forward-only, and idempotent where practical. Prefer guarded table/index creation, additive columns, and safe backfills. Avoid destructive drop/rename/tighten changes older branches may still read.

## Unattended Safety

- Treat unattended and unattended-confirm flows as safety-critical. They must remain non-blocking and conservative.
- Do not add interactive prompts, hidden confirmations, or ambiguous fallthrough behavior in unattended paths.
- If unattended mode cannot choose safely, prefer dry-run, site-check, explicit skip, or clear failure over uncertain upload.
- Preserve invariants: site-check implies dry-run; debug implies safe non-upload behavior; unattended flows keep current questionnaire/default-selection behavior unless change updates those rules everywhere.
- Preserve safe skip and override behavior for dupes, rule failures, screenshot/image-host uploads, torrent injection, and retries. If one shared surface supports skip/override, keep parity in other shared surface.

## Scope Notes

- Keep guidance consistent with `.golangci.yml`, `gui/frontend/package.json`, and `.github/workflows/*.yml`. Do not invent new required steps.
- If change affects one area, run smallest relevant checks. If change crosses backend, frontend, GUI, packaging, or unattended execution, expand validation.
