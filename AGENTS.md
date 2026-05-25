# Project Guidelines

## Source Of Truth

- Contributor setup, platform notes, Makefile targets, build commands, tests, hooks, and commit format live in `CONTRIBUTING.md`.
- Tool wiring lives in `Makefile`, `lefthook.yml`, `.golangci.yml`, `gui/frontend/package.json`, and `.github/workflows/*.yml`.
- When docs disagree, prefer tool config over prose. Update prose instead of copying stale commands.

## Quick Commands

```bash
make help               # Show supported targets
make backend            # Fast build sanity check
make test-go            # Full Go tests
make test-frontend      # Frontend lint/dead-code/type/format checks
make lint               # Full Go lint
make logpolicy          # Logging policy check
make precommit          # Lefthook pre-commit
make prepush            # Lefthook pre-push
make gofix-check-changed # Inspect Go fix drift on changed packages
git diff --check        # Whitespace/conflict-marker check
```

Use `CONTRIBUTING.md` for full command reference and platform details. Use narrow package/file checks first, then expand when touching shared behavior or release surfaces.

## Code Quality

- Match repo style. Keep changes narrow. Fix root causes, not symptoms.
- New Go code must satisfy enabled `.golangci.yml` linters and formatters. Avoid broad `nolint`.
- `containedctx` is the only disabled Go linter. Do not add new context fields just because that linter is disabled.
- Active checks include `noctx`, `contextcheck`, `wrapcheck`, `revive`, `forcetypeassert`, `unparam`, `usetesting`, and `gosec`.
- Use context-aware APIs. Propagate context where meaningful; terminate it deliberately when crossing into root/background work.
- Wrap external-package errors where lint requires it. Handle errors by returning, wrapping, logging with useful context, or making intentional ignore paths obvious.
- Avoid unchecked type assertions. Use `testing` helpers in tests. Justify narrow `nolint` at source.
- Do not reshape existing code only for disabled lint policy unless functional need exists or nearby code already follows that pattern.
- Frontend changes must keep TypeScript, ESLint, Stylelint, and dead-code checks clean. Do not weaken rules or bypass type errors.

## Logging

- Add logs where they explain meaningful state, decisions, failures, retries, or user-visible outcomes. Improve touched functions when practical and relevant.
- Treat `cmd/logpolicy` as logging contract. Fix flagged log messages or levels at source; do not weaken checker or move noise sideways.
- Redact secrets and user-sensitive data with `internal/redaction/redaction.go`.
- Never log credentials, tokens, API keys, passkeys, cookies, or secret-bearing payloads without repository redaction standard.
- Keep levels purposeful: `INFO` for concise user-facing upload progress/outcomes, `DEBUG` for troubleshooting context, `TRACE` for high-fidelity operational flow.

## Go Fix

- Do not apply `go fix` wholesale without review.
- Prefer `make gofix-check-changed` and package-scoped `go fix -omitzero=false <packages>`.
- Keep `omitzero` disabled unless a change explicitly reviews JSON output semantics.

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

- Do not duplicate detailed contributor workflow from `CONTRIBUTING.md` here. Link or summarize only agent-critical deltas.
- If change affects one area, run smallest relevant checks. If change crosses backend, frontend, GUI, packaging, or unattended execution, expand validation.
