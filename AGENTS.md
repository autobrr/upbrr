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

Use `CONTRIBUTING.md` for full command reference/platform details. Start narrow package/file checks; expand for shared behavior or release surfaces.
Before committing code changes, run targeted tests plus `make precommit`. It runs the staged Lefthook pre-commit hook, whitespace checks, changed-package Go fix drift, full Go lint/path policy, log policy, and frontend lint/dead-code/type/unit/format checks. For Go behavior changes, also run focused `go test` or `make test-go` as risk requires.

## Code Quality

- Match repo style. Narrow changes. Fix root cause, not symptoms.
- New Go code must satisfy `.golangci.yml` linters/formatters. Avoid broad `nolint`.
- Active checks: `depguard`, `noctx`, `contextcheck`, `containedctx`, `wrapcheck`, `revive`, `forcetypeassert`, `unparam`, `usetesting`, `gosec`, `logpolicy`, `pathpolicy`.
- Use context-aware APIs. Propagate context where meaningful; terminate deliberately crossing root/background work.
- Wrap external-package errors where lint requires. Handle errors by return/wrap/log useful context, or make intentional ignore obvious.
- Avoid unchecked type assertions. Use `testing` helpers in tests. Justify narrow `nolint` at source.
- Frontend: keep TypeScript, ESLint, Stylelint, dead-code clean. Don't weaken rules or bypass type errors.
- Embedded frontend visual checks: rebuild/sync embedded assets + CLI before browser automation. Use main embedded port `7480` with `dist/upbrr.exe serve --dev-no-auth`; avoid Vite-only `5173` for embedded parity. Stop server after inspection.

## Path Portability

- Use `filepath` for local filesystem paths. Use `path` only for slash-delimited torrent paths, URLs, or API payloads explicitly defined to use `/`.
- Torrent/API -> local filesystem boundary: validate slash paths first, then convert deliberately with `filepath.FromSlash`.
- Security/path traversal checks reject POSIX + Windows absolute/escaping forms on every OS: leading `/`, leading `\`, drive-letter paths, UNC paths, `..` segments.
- Use `internal/pathutil.IsWithinRoot` / `SamePath` for local root containment/equality. Do not add ad-hoc `filepath.Rel` + string-prefix guards.
- Tests must not build local paths with hardcoded OS-rooted literals. Use `t.TempDir`, `filepath.Join`, `filepath.ToSlash` for cross-platform assertions.
- `cmd/pathpolicy` flags hardcoded OS-rooted literals in `filepath` calls, string-built local paths, `path` on local paths, `filepath` on URL/API slash paths, slash-data filesystem calls, slash assertions without `filepath.ToSlash`, and ad-hoc local path guards outside `internal/pathutil`.
- Legit stdlib `path` imports require import-local `//nolint:depguard // <slash-data reason>`. Rare `pathpolicy` cases require `//pathpolicy:allow <reason>` on same/previous line. Fix source first; don't weaken checkers.

## Logging

- Add logs for meaningful state, decisions, failures, retries, user-visible outcomes. Improve touched funcs when relevant.
- Treat `cmd/logpolicy` as logging contract. Fix flagged logs/levels at source; don't weaken checker or move noise sideways.
- Redact secrets/user-sensitive data with `internal/redaction/redaction.go`.
- Never log credentials, tokens, API keys, passkeys, cookies, or secret-bearing payloads without repo redaction standard.
- Levels: `INFO` concise user-facing upload progress/outcomes; `DEBUG` troubleshooting context; `TRACE` high-fidelity operational flow.

## Go Fix

- Don't apply `go fix` wholesale without review.
- Prefer `make gofix-check-changed` and package-scoped `go fix -omitzero=false <packages>`.
- Keep `omitzero` disabled unless change explicitly reviews JSON output semantics.

## Product Invariants

- Shared behavior spans CLI, Wails GUI, embedded web-serving mode. Preserve parity in request construction, options, upload behavior where practical.
- App targets Windows, Linux, macOS. Avoid OS-specific path/process/filesystem/archive/build assumptions unless intentionally platform-gated.
- Preserve `api.Mode` usage. Align CLI, Wails GUI, embedded web flows with request types under `pkg/api` and shared core behavior under `internal`.
- Upload options, tracker overrides, retries, execution flags: check CLI + GUI entrypoints when shared.
- SQLite DBs may be shared across branches during dev. Keep migrations compatible with permissive cross-branch use.
- Migrations additive, forward-only, idempotent where practical. Prefer guarded table/index creation, additive columns, safe backfills. Avoid destructive drop/rename/tighten changes older branches may still read.

## Unattended Safety

- Unattended/unattended-confirm flows safety-critical. Keep non-blocking + conservative.
- No interactive prompts, hidden confirmations, ambiguous fallthrough in unattended paths.
- If unattended cannot choose safely, prefer dry-run, site-check, explicit skip, or clear failure over uncertain upload.
- Preserve invariants: site-check implies dry-run; debug implies safe non-upload; unattended flows keep current questionnaire/default-selection behavior unless change updates rules everywhere.
- Preserve safe skip/override for dupes, rule failures, screenshot/image-host uploads, torrent injection, retries. If one shared surface supports skip/override, keep parity in other shared surface.

## Scope Notes

- Don't duplicate detailed contributor workflow from `CONTRIBUTING.md`; link/summarize agent-critical deltas only.
- If change affects one area, run smallest relevant checks. If change crosses backend/frontend/GUI/packaging/unattended execution, expand validation.
