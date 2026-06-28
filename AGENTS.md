# Project Guidelines

Always-loaded repo rules for AI coding agents. Keep this file short; nearest scoped `AGENTS.md` files carry area detail.

## Source Of Truth

- Tool config wins: `Makefile`, `lefthook.yml`, `.golangci.yml`, `gui/frontend/package.json`, `.github/workflows/*`.
- Contributor setup and command detail: `CONTRIBUTING.md`.
- Guidance disagrees with tools? Follow tools and update stale prose.

## Scoped References

- Backend, Go, path/log policy, trackers/config/domain rules, runtime architecture, lint/check policy: `internal/AGENTS.md`.
- CLI flags/prompts/unattended behavior: `cmd/upbrr/AGENTS.md`.
- Shared API/runtime contracts: `pkg/api/AGENTS.md`.
- Frontend, React, CSS, TypeScript, browser checks: `gui/frontend/AGENTS.md`.
- Playwright E2E harness, fake services, reports, manual workflow: `gui/frontend/e2e/AGENTS.md`.

Read the scoped file before editing that area. For simple grep/read-only questions, avoid loading extra instructions unless needed.

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

Start narrow; expand checks for shared behavior, release, GUI/web parity, or safety-sensitive changes. Do not substitute hook wrappers for the area-specific checks below.

## Area Checks

- Backend/Go: focused `go test -race -v -timeout 20m <packages>` for touched packages; `make test-go` when shared core behavior or broad regressions are plausible; `make lint`; `make logpolicy` for logging/internal changes; `make pathpolicy` for path-handling changes.
- CLI: `go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api`; add touched service/tracker packages; run `make backend` for command/build sanity.
- GUI/web/API parity: `go test -race -v -timeout 20m ./internal/guiapp ./internal/webserver ./internal/guishared ./pkg/api`; add frontend `typecheck`/unit checks when request/response shapes or runtime bridge code changes.
- Frontend: `pnpm --dir gui/frontend run lint`, `lint:dead`, `typecheck`, `test:unit`, `format:check`; add `lint:style` for CSS and `build` for bundle/runtime issues.
- E2E/browser: read `gui/frontend/e2e/AGENTS.md`; use embedded web checks, not Vite-only checks, for parity-sensitive UI/runtime changes.

Before commit, also run `git diff --check`, changed-package `make gofix-check-changed`, and the relevant hook commands if desired. If Go files, generated dirs, or scratch paths can affect package discovery, run `make lint` before commit.

## Repo Map

- CLI `cmd/upbrr`; core `internal/core`; services `internal/services`; trackers `internal/trackers`; config `internal/config`.
- Wails `gui`, `internal/guiapp`; embedded web/API `internal/webserver`; API contracts `pkg/api`; frontend `gui/frontend`.

## Non-Negotiables

- Keep changes narrow; fix root cause; do not revert user changes.
- Preserve CLI, Wails GUI, and embedded web parity.
- Preserve CLI `--unattended` / `--unattended_confirm` (`--uac`) safety: `--unattended` must not prompt; `--unattended_confirm` may ask required confirmation/manual inputs. No hidden prompts/confirms or ambiguous fallthrough.
- Never log credentials/tokens/API keys/cookies/secret payloads; use repo redaction/logging policy.
- Do not commit generated/local output: `dist/`, `gui/frontend/dist/`, `gui/build/bin/`, populated `internal/guiapp/assets`, Playwright reports/results, repo-local `tmp/`.
- `.github/workflows/*.yml` files are active; `.yml22` files are disabled templates.
