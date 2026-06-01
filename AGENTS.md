# Project Guidelines

## Source Of Truth

- Contributor setup/platform notes/Makefile targets/build commands/tests/hooks/commit format: `CONTRIBUTING.md`.
- Tool wiring: `Makefile`, `lefthook.yml`, `.golangci.yml`, `gui/frontend/package.json`, `.github/workflows/*`.
- Docs disagree? Tool config wins. Update prose; don't copy stale commands.

## Quick Commands

```bash
make help               # Show supported targets
make backend            # Fast build sanity check
make test-go            # Full Go tests with race detector
make test-frontend      # Frontend lint/dead-code/type/unit/format checks
make lint               # Path policy + full Go lint
make logpolicy          # Logging policy check
make pathpolicy         # Path portability policy check
make precommit          # Strong local validation before commit
make prepush            # Lefthook pre-push
make gofix-check-changed # Inspect Go fix drift on changed packages
git diff --check        # Whitespace/conflict-marker check
```

Use `CONTRIBUTING.md` for full command ref/platform details. Start narrow package/file checks; expand for shared behavior/release surfaces.
Before commit code changes, run targeted tests + `make precommit`; before push, run `make prepush`. `make precommit` runs staged Lefthook pre-commit hook, whitespace checks, changed-package Go fix drift, full Go lint/path policy, log policy, frontend lint/dead-code/type/unit/format checks. Does not run Go tests. For Go behavior changes, also run focused `go test` or `make test-go` as risk requires.
For Go changes, prefer running `make lint` or `make prepush` before committing when practical; pre-push runs full `golangci-lint ./...` and can catch wrapcheck/depguard issues not surfaced by the staged pre-commit hook. If pre-push fails after a commit, fix the exact checker output, rerun `make prepush`, then amend the commit.

## Repository Topology

- CLI entry/options/interactive flow: `cmd/upbrr`.
- Wails desktop entry: `gui`; React UI: `gui/frontend`; Wails backend: `internal/guiapp`.
- Embedded web backend + API routes: `internal/webserver`.
- Shared orchestration: `internal/core`; shared req/res contracts: `pkg/api`.
- Services under `internal/services`; tracker defs/shared tracker logic under `internal/trackers`; concrete tracker impls under `internal/trackers/impl`.

## Code Quality

- Match repo style. Narrow changes. Fix root cause, not symptoms.
- New Go code must satisfy `.golangci.yml` linters/formatters. Treat `.golangci.yml` as linter source of truth. Avoid broad `nolint`.
- Use context-aware APIs. Propagate context where meaningful; terminate deliberately across root/background work.
- Wrap external-package errors where lint requires. Handle errors by return/wrap/log useful context, or make intentional ignore obvious.
- Avoid unchecked type assertions. Use `testing` helpers in tests. Justify narrow `nolint` at source.
- Go tests creating files with `os.WriteFile` should use `0o600` or tighter perms unless broader mode bits are behavior under test.
- Frontend: keep TypeScript, ESLint, Stylelint, dead-code clean. Don't weaken rules or bypass type errors. CSS changes require `pnpm --dir gui/frontend run lint:style`; `make test-frontend` does not run Stylelint.
- Frontend styling: new/touched UI should prefer Tailwind utilities for local layout/spacing. Keep custom CSS for shared states, theme vars, cross-cutting selectors, or when utilities hurt JSX readability.
- React effects: use `useEffect` only to sync external systems: DOM, subscriptions, network, browser APIs. Avoid derived state in effects; calculate during render or use `useMemo` for costly work. Put user-driven logic in handlers. Prefer `key` or render-time adjustment for state resets. Fetch effects need cleanup/abort guards for stale responses.
- Embedded frontend visual checks: rebuild/sync embedded assets + CLI before browser automation. Use main embedded port `7480` with `dist/upbrr.exe serve --dev-no-auth`; avoid Vite-only `5173` for embedded parity. Stop server after inspection.
- Frontend build caveat: `make frontend` / `make frontend-bundle` builds `gui/frontend/dist` only. Does not sync `internal/guiapp/assets` or rebuild CLI. `make gui` uses current embedded assets and can hide stale frontend output.

## Path Portability

- Use `filepath` for local filesystem paths. Use `path` only for slash-delimited torrent paths, URLs, or API payloads explicitly defined to use `/`.
- Torrent/API -> local filesystem boundary: validate slash paths first, then convert deliberately with `filepath.FromSlash`.
- Security/path traversal checks reject POSIX + Windows absolute/escaping forms on every OS: leading `/`, leading `\`, drive-letter paths, UNC paths, `..` segments.
- Use `internal/pathutil.IsWithinRoot` / `SamePath` for local root containment/equality. Do not add ad-hoc `filepath.Rel` + string-prefix guards.
- Tests must not build local paths with hardcoded OS-rooted literals. Use `t.TempDir`, `filepath.Join`, `filepath.ToSlash` for cross-platform assertions.
- `cmd/pathpolicy` flags hardcoded OS-rooted literals in `filepath` calls, string-built local paths (`fmt.Sprintf`, `strings.Join`, `+`), `path` on local paths, `filepath` on URL/API slash paths, slash-data filesystem calls, slash assertions without `filepath.ToSlash`, and ad-hoc local path guards outside `internal/pathutil`.
- Legit stdlib `path` imports require import-local `//nolint:depguard // <slash-data reason>`. Rare `pathpolicy` cases require `//pathpolicy:allow <reason>` on same/previous line. Fix source first; don't weaken checkers.

## Logging

- Add logs for meaningful state, decisions, failures, retries, user-visible outcomes. Improve touched funcs when relevant.
- Treat `cmd/logpolicy` as logging contract. Checks non-test Go under `internal/**`, rejects stdlib print/log calls + low-context log formats, enforces level hygiene. Fix flagged logs/levels at source; don't weaken checker or move noise sideways.
- Redact secrets/user-sensitive data with `internal/redaction/redaction.go`.
- Never log credentials, tokens, API keys, passkeys, cookies, or secret-bearing payloads without repo redaction standard.
- Levels: `INFO` concise user-facing upload progress/outcomes; `DEBUG` troubleshooting context; `TRACE` high-fidelity operational flow.

## Go Fix

- Don't apply `go fix` wholesale without review.
- Prefer `make gofix-check-changed` and package-scoped `go fix -omitzero=false <packages>`.
- Keep `omitzero` disabled unless change explicitly reviews JSON output semantics.

## Product Invariants

- Shared behavior spans CLI, Wails GUI, embedded web-serving mode. Preserve parity in req construction, options, upload behavior where practical.
- App targets Windows, Linux, macOS. Avoid OS-specific path/process/filesystem/archive/build assumptions unless intentionally platform-gated.
- Preserve `api.Mode` usage. Align CLI, Wails GUI, embedded web flows with req types under `pkg/api` and shared core behavior under `internal`.
- Upload options, tracker overrides, retries, execution flags: check CLI + GUI entrypoints when shared.
- API/runtime parity: changes to `pkg/api.Request`, `UploadOptions`, `PreparedMetadata`, dry-run/upload review, questionnaire answers, description groups, or upload options must check CLI req builders, Wails methods, web routes/backend/jobs, frontend runtime bridge, and TS types.
- SQLite DBs may be shared across branches during dev. Keep migrations compatible with permissive cross-branch use.
- Migrations additive, forward-only, idempotent where practical. Prefer guarded table/index creation, additive columns, safe backfills. Avoid destructive drop/rename/tighten changes older branches may still read.
- Migration IDs stable. Do not rename/reuse shipped IDs. Use narrow `dependsOn` instead of assuming contiguous global versions. Keep `schema_migrations` branch-friendly and preserve legacy `user_version` bridge compatibility when needed.
- Runtime config mutable after `SaveConfig` / `applyConfig`. Wails `App` and web `Backend` config/core/logger protected by `runtimeMu`; read via `currentConfig()`, `requireRuntime()`, or runtime snapshot once per request/job; reuse snapshot. Do not read `App.cfg` / `Backend.cfg` directly outside runtime helpers. Treat `Server.cfg` as startup-only state; request-time values like tracker favicon URL, DB/log/tmp paths, upload options, logger/core handles should come from backend runtime snapshot/current config.

## Domain Guardrails

- Tracker changes often need sync across `internal/trackers/impl/*`, `internal/trackers/impl/registry.go`, `internal/trackers/catalog.go`, `internal/trackers/unit3dmeta`, `internal/config/defaults/example.yaml`, and dupe/source-lookup/image-host policy tests.
- Config schema changes need `internal/config.Config`, embedded defaults, import/export paths, env overrides when relevant, settings UI/web parity, and secret redaction/encryption review.
- Runtime bridge changes involving `globalThis.go.guiapp.App` need matching Wails `internal/guiapp` methods, web `/api/app/*` routes, browser bridge req shapes, and unit/embedded browser verification.
- Generated/built output exists locally and mostly ignored. Do not commit `dist/`, `gui/frontend/dist`, `gui/build/bin`, generated Wails bindings, or populated `internal/guiapp/assets` unless deliberately updating generated artifacts.
- GitHub workflow note: `.github/workflows/*.yml` files active; `.yml22` files disabled templates. Keep Makefile, scripts, CONTRIBUTING, workflow version pins aligned, esp Wails and pnpm.

## Focused Validation

- CLI/shared behavior: `go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api`.
- GUI/web/API parity: `go test -race -v -timeout 20m ./internal/guiapp ./internal/webserver ./internal/guishared ./pkg/api`.
- Frontend runtime/API bridge: `pnpm --dir gui/frontend run test:unit` plus `pnpm --dir gui/frontend run typecheck`.
- CSS-only frontend changes: add `pnpm --dir gui/frontend run lint:style`.

## Unattended Safety

- Unattended/unattended-confirm flows safety-critical. Keep non-blocking + conservative.
- No interactive prompts, hidden confirmations, ambiguous fallthrough in unattended paths.
- If unattended cannot choose safely, prefer dry-run, site-check, explicit skip, or clear failure over uncertain upload.
- Preserve invariants: site-check implies dry-run; debug implies safe non-upload; unattended flows keep current questionnaire/default-selection behavior unless change updates rules everywhere.
- Preserve safe skip/override for dupes, rule failures, screenshot/image-host uploads, torrent injection, retries. If one shared surface supports skip/override, keep parity in other shared surface.

## Scope Notes

- Don't duplicate detailed contributor workflow from `CONTRIBUTING.md`; link/summarize agent-critical deltas only.
- If change affects one area, run smallest relevant checks. If change crosses backend/frontend/GUI/packaging/unattended execution, expand validation.
