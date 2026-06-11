# Project Guidelines

Always-loaded repo rules for AI coding agents. Keep this file short; load scoped docs only when relevant.

## Source Of Truth

- Tool config wins: `Makefile`, `lefthook.yml`, `.golangci.yml`, `gui/frontend/package.json`, `.github/workflows/*`.
- Contributor setup and command detail: `CONTRIBUTING.md`.
- Docs disagree with tools? Follow tools and update stale prose.

## Scoped References

- Backend, Go, path/log policy, trackers/config/domain rules: `docs/backend.md`.
- Frontend, React, CSS, TypeScript, browser checks: `gui/frontend/AGENTS.md`.
- Cross-entrypoint architecture, API/runtime flow, DB/config ownership: `docs/architecture.md`.
- Lint/check policy, generated/scratch path risks, hook internals: `docs/linting.md`.
- Playwright E2E harness, fake services, reports, manual workflow: `docs/e2e.md`.

Read the scoped file before editing that area. For simple grep/read-only questions, avoid loading extra docs unless needed.

## Quick Commands

```bash
make help                # supported targets
make backend             # fast CLI build sanity
make test-go             # full Go race tests
make test-frontend       # frontend lint/dead-code/type/unit/format
make lint                # path policy + full Go lint
make precommit           # strong local validation before commit; no Go tests
make prepush             # Lefthook pre-push wrapper
git diff --check         # whitespace/conflict markers
```

Start narrow; expand checks for shared behavior, release, GUI/web parity, or safety-sensitive changes. Before commit: targeted tests + `make precommit`; Go behavior also needs focused `go test` or `make test-go` as risk requires. If Go files, generated dirs, or scratch paths can affect package discovery, run `make lint` before commit. If TS/TSX changed, run `pnpm --dir gui/frontend run typecheck` before commit.

## Repo Map

- CLI `cmd/upbrr`; core `internal/core`; services `internal/services`; trackers `internal/trackers`; config `internal/config`.
- Wails `gui`, `internal/guiapp`; embedded web/API `internal/webserver`; API contracts `pkg/api`; frontend `gui/frontend`.

## Non-Negotiables

- Keep changes narrow; fix root cause; do not revert user changes.
- Preserve CLI, Wails GUI, and embedded web parity.
- Preserve unattended/unattended-confirm safety: no hidden prompts/confirms or ambiguous fallthrough.
- Never log credentials/tokens/API keys/cookies/secret payloads; use repo redaction/logging policy.
- Do not commit generated/local output: `dist/`, `gui/frontend/dist/`, `gui/build/bin/`, populated `internal/guiapp/assets`, Playwright reports/results, repo-local `tmp/`.
- `.github/workflows/*.yml` files are active; `.yml22` files are disabled templates.
