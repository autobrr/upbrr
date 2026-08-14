# Project Guidelines

Always-loaded AI-agent repo rules. Keep short; nearest scoped `AGENTS.md` owns area detail.

## Source Of Truth

- Authority: `Makefile`, `lefthook.yml`, `.golangci.yml`, `webui/package.json`, `documentation/package.json`, and active `.github/workflows/*.yml` files. `.yml22` files are disabled templates.
- Setup/commands: `CONTRIBUTING.md`.
- Conflict: follow tools; update stale prose.

## Scoped References

- Backend/Go, path/log policy, trackers/config/domain, runtime architecture, lint/checks: `internal/AGENTS.md`.
- CLI flags/prompts/unattended behavior: `cmd/upbrr/AGENTS.md`.
- Shared API/runtime contracts: `pkg/api/AGENTS.md`.
- Frontend/React/CSS/TypeScript/browser checks: `webui/AGENTS.md`.
- Playwright E2E, fake services, reports, manual workflow: `webui/e2e/AGENTS.md`.
- Public Docusaurus content, synchronization, checks, and publishing: `documentation/AGENTS.md`.

Read scoped file before area edits. Simple grep/read-only work: load extra instructions only when needed.

## Quick Commands

```bash
make help                # supported targets
make backend             # fast CLI build sanity
make test-go             # full Go race tests
make test-frontend       # frontend lint/dead-code/type/unit/format
pnpm --dir documentation run check # public documentation format/type/build
make lint                # architecture/path/literal/workflow-contract checks + full Go lint
make precommit           # strong local validation before commit; no Go tests
make prepush             # Lefthook pre-push wrapper
git diff --check         # whitespace/conflict markers
```

Start narrow; expand for shared behavior, release, WebUI/API parity, or safety-sensitive changes. Area checks below cannot be replaced by hook wrappers.

## Area Checks

- Backend/Go: touched packages: focused `go test -race -v -timeout 20m <packages>`; shared core/broad regression risk: `make test-go`; always `make lint`; logging/internal: `make logpolicy`; paths: `make pathpolicy`.
- CLI: `go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api`; add touched service/tracker packages; build sanity: `make backend`.
- WebUI/API: `go test -race -v -timeout 20m ./internal/webserver/... ./pkg/api`; request/response or browser-client changes: add frontend `typecheck`/unit checks.
- Frontend: `pnpm --dir webui run lint`, `lint:dead`, `typecheck`, `test:unit`, `format:check`; CSS: `lint:style`; bundle/runtime: `build`.
- E2E/browser: read `webui/e2e/AGENTS.md`; runtime-sensitive UI requires embedded-web, not Vite-only, checks.
- Public documentation: read `documentation/AGENTS.md`; run `pnpm --dir documentation run check`.

Before commit: `git diff --check`, changed-package `make gofix-check-changed`, optionally relevant hooks. `make fmt-go` applies 160-column formatting and expands keyed composite literals containing at least three elements. If Go files, generated dirs, or scratch paths affect package discovery, run `make lint`.

## Repo Map

- CLI `cmd/upbrr`; workflow `internal/releaseworkflow`; domain adapters `internal/core`; config `internal/config`; other domain services `internal/services`.
- Prepared generations/display `internal/preparedrelease`; external identity `internal/externalidentity`; source resources `internal/sourcelayout`; client discovery `internal/clientdiscovery`.
- Tracker contracts/orchestration `internal/trackers`; families `internal/trackers/impl/{unit3d,azfamily,standalone}`; standalone trackers `internal/trackers/impl/standalone/<tracker>`; Unit3D sites `internal/trackers/impl/unit3d/sites/<tracker>`.
- Tracker semantic modules: `name.go`, `auth.go`, `taxonomy.go`, `validation.go`, `description.go`, `media.go`, `questionnaire.go` when applicable. Tracker `upload.go`: prepare/submit/preview orchestration and transport only.
- Tracker auth/dupe/data coordinators `internal/trackers/{auth,dupe,data}`; encrypted cookies `internal/cookies`; generic BBCode/description/image hosting `internal/{bbcode,description,imagehosting}`.
- Paths `internal/pathing`, with `layout` and checker-only `policy`; torrent clients `internal/torrentclient`; metainfo `internal/torrent/metainfo`; release policy `internal/releasepolicy`.
- WebUI server/API host `internal/webserver`; API contracts `pkg/api`; frontend workflow state/operation ownership `webui/src/releaseSession`.
- Public documentation site `documentation`; production publishing `.github/workflows/release.yml`.

## Logging Levels

- Purposeful levels across CLI, WebUI, tests, tooling.
- Log operator-visible progress/decisions, not only final errors: operation start, external/local check, decision, affected count.
- `INFO`: concise, relevant end-user progress/outcomes for uploads and top-level workflows.
- Warnings: failed/blocked outcomes requiring attention.
- `DEBUG`: richer developer troubleshooting and decision context.
- `TRACE`: near-complete operational flow.
- Prefer searchable, stable key/value message fields: `tracker=%s state=%s decision=%s count=%d`.

## Non-Negotiables

- Narrow changes; fix root cause; never revert user changes.
- Fill `.github/pull_request_template.md` into every PR body; `gh pr create --body` does not auto-fill it.
- Preserve shared CLI/WebUI workflow behavior.
- Debug mode validates end-to-end flow, not a non-mutating dry run. It suppresses tracker submission but may bypass policy gates such as banned-group blocking, keeping screenshots, descriptions, tracker preparation, and later stages testable. Client injection remains default; CLI `-ns` and WebUI Upload skip-client-injection are explicit opt-outs. Report defects only when behavior diverges from these semantics.
- Centrally resolve versioned tracker upload/search names before duplicate checks. Principal payload fields use `PreparationInput.ReviewedUploadName()`; custom naming algorithms belong in `name.go`.
- Preserve CLI `--unattended` / `--unattended_confirm` (`--uac`) safety: `--unattended` never prompts; `--unattended_confirm` may request required confirmation/manual input. No hidden prompts/confirms or ambiguous fallthrough.
- Never log credentials, tokens, API keys, cookies, or secret payloads; follow repo redaction/logging policy.
- Shareable examples, diagnostics, docs, fixtures: no real release names, movie/show titles, or provider IDs. Use synthetic `Example Release 2026`, `Example.Release.2026.1080p-GRP`, `tt1234567`. Prefer incidental group `GRP`; real groups only when behavior-relevant. Required production lists, including tracker banned groups, may retain real values.
- Never commit generated/local output: `dist/`, `webui/dist/`, `documentation/build/`, populated `internal/webserver/assets`, Playwright reports/results, repo-local `tmp/`.
- `.github/workflows/*.yml` files active; `.yml22` files disabled templates.
