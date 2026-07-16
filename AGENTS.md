# Project Guidelines

Always-loaded repo rules for AI coding agents. Keep this file short; nearest scoped `AGENTS.md` files carry area detail.

## Source Of Truth

- Tool config wins: `Makefile`, `lefthook.yml`, `.golangci.yml`, `webui/package.json`, `.github/workflows/*`.
- Contributor setup and command detail: `CONTRIBUTING.md`.
- Guidance disagrees with tools? Follow tools and update stale prose.

## Scoped References

- Backend, Go, path/log policy, trackers/config/domain rules, runtime architecture, lint/check policy: `internal/AGENTS.md`.
- CLI flags/prompts/unattended behavior: `cmd/upbrr/AGENTS.md`.
- Shared API/runtime contracts: `pkg/api/AGENTS.md`.
- Frontend, React, CSS, TypeScript, browser checks: `webui/AGENTS.md`.
- Playwright E2E harness, fake services, reports, manual workflow: `webui/e2e/AGENTS.md`.

Read the scoped file before editing that area. For simple grep/read-only questions, avoid loading extra instructions unless needed.

## Quick Commands

```bash
make help                # supported targets
make backend             # fast CLI build sanity
make test-go             # full Go race tests
make test-frontend       # frontend lint/dead-code/type/unit/format
make lint                # architecture/path/literal policies + full Go lint
make precommit           # strong local validation before commit; no Go tests
make prepush             # Lefthook pre-push wrapper
git diff --check         # whitespace/conflict markers
```

Start narrow; expand checks for shared behavior, release, WebUI/API parity, or safety-sensitive changes. Do not substitute hook wrappers for the area-specific checks below.

## Area Checks

- Backend/Go: focused `go test -race -v -timeout 20m <packages>` for touched packages; `make test-go` when shared core behavior or broad regressions are plausible; `make lint`; `make logpolicy` for logging/internal changes; `make pathpolicy` for path-handling changes.
- CLI: `go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api`; add touched service/tracker packages; run `make backend` for command/build sanity.
- WebUI/API: `go test -race -v -timeout 20m ./internal/webserver/... ./pkg/api`; add frontend `typecheck`/unit checks when request/response shapes or browser client code changes.
- Frontend: `pnpm --dir webui run lint`, `lint:dead`, `typecheck`, `test:unit`, `format:check`; add `lint:style` for CSS and `build` for bundle/runtime issues.
- E2E/browser: read `webui/e2e/AGENTS.md`; use embedded web checks, not Vite-only checks, for runtime-sensitive UI changes.

Before commit, also run `git diff --check`, changed-package `make gofix-check-changed`, and the relevant hook commands if desired. `make fmt-go` applies the 160-column formatter and expands keyed composite literals with three or more elements. If Go files, generated dirs, or scratch paths can affect package discovery, run `make lint` before commit.

## Repo Map

- CLI `cmd/upbrr`; workflow orchestration `internal/core`; config `internal/config`; remaining domain services `internal/services`.
- Canonical prepared generations/display `internal/preparedrelease`; external identity `internal/externalidentity`; source resources `internal/sourcelayout`; torrent-client discovery `internal/clientdiscovery`.
- Tracker contracts/orchestration `internal/trackers`; implementations `internal/trackers/impl`; Unit3D sites `internal/trackers/impl/unit3d/sites`.
- Tracker auth/dupe/data coordinators `internal/trackers/{auth,dupe,data}`; generic BBCode/description/image hosting `internal/{bbcode,description,imagehosting}`.
- Paths `internal/pathing` with `layout` and checker-only `policy`; torrent clients `internal/torrentclient`; metainfo `internal/torrent/metainfo`; release policy `internal/releasepolicy`.
- WebUI server/API and retained jobs `internal/webserver`; API contracts `pkg/api`; frontend release ownership `webui/src/releaseSession`; shared job coordination `webui/src/jobRegistry`.

## Logging Levels

- Keep log levels purposeful across CLI, WebUI, tests, and tooling.
- Add logs for operator-visible progress and decision points, not just final errors. Good logs answer what operation started, what external/local check ran, what decision was made, and how many items were affected.
- `INFO` should provide concise, relevant progress or outcome details for end users during uploads and other top-level workflows.
- Warnings should cover failed or blocked outcomes that require attention.
- `DEBUG` should include richer decision-making context useful for developer troubleshooting.
- `TRACE` should capture near-complete operational flow for high-fidelity execution reporting.
- Prefer stable key/value-style fields in message strings (`tracker=%s state=%s decision=%s count=%d`) so logs are searchable.

## Non-Negotiables

- Keep changes narrow; fix root cause; do not revert user changes.
- Preserve CLI and WebUI behavior where they share workflows.
- Preserve CLI `--unattended` / `--unattended_confirm` (`--uac`) safety: `--unattended` must not prompt; `--unattended_confirm` may ask required confirmation/manual inputs. No hidden prompts/confirms or ambiguous fallthrough.
- Never log credentials/tokens/API keys/cookies/secret payloads; use repo redaction/logging policy.
- Shareable examples, diagnostics, docs, and test fixtures must not use real release names, real movie/show titles, or real provider IDs. Use synthetic placeholders such as `Example Release 2026`, `Example.Release.2026.1080p-GRP`, and `tt1234567`. Prefer generic group tags such as `GRP` when the group is incidental; real group names are allowed when relevant to behavior. Production domain lists such as tracker banned groups may keep real values when those values are required behavior.
- Do not commit generated/local output: `dist/`, `webui/dist/`, populated `internal/webserver/assets`, Playwright reports/results, repo-local `tmp/`.
- `.github/workflows/*.yml` files are active; `.yml22` files are disabled templates.
