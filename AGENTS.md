# Project Guidelines

## Code Quality

- Match the repository's existing style and keep changes narrow. Prefer fixing root causes over adding special cases.
- Format Go code with gofmt and goimports. Keep the local import prefix set to github.com/autobrr/upbrr.
- Treat .golangci.yml as the Go standard of record. New code should satisfy these enabled linters: copyloopvar, errname, errorlint, exhaustive, fatcontext, gocritic, gosec, loggercheck, mirror, misspell, perfsprint, prealloc, rowserrcheck, spancheck, testifylint, unconvert, unused, and whitespace.
- The disabled linters are intentional policy, not missed cleanup. Do not reshape code just to satisfy containedctx, noctx, or revive unless there is a functional reason or the surrounding code already follows that pattern.
- Respect the current golangci-lint exclusions and formatter settings instead of reintroducing churn in files already covered by scoped exceptions.
- For frontend changes, keep TypeScript and ESLint clean without weakening existing rules or bypassing type errors.

## Validation

- Run the relevant CI-aligned checks after changes.
- For Go changes, run the narrowest relevant Go tests first, such as a package-scoped go test invocation for the affected area. When changes touch shared behavior, multiple packages, or cross-surface flows, expand to broader coverage up to: go test -v -timeout 20m ./...
- For Go changes, run: golangci-lint run --timeout=5m
- Prefer the smallest relevant frontend validation for the files you changed, but keep lint and typecheck clean for the affected frontend surface. When frontend changes are broad, shared, or configuration-related, run the full gui/frontend checks called out below.
- For gui/frontend changes, use gui/frontend as the working directory. Run pnpm install --frozen-lockfile whenever frontend dependencies or lockfiles may affect the change; otherwise run pnpm run lint and pnpm run typecheck
- For gui/frontend build logic, embedded assets, or Vite/TypeScript config changes, also run: pnpm run build
- For Wails runtime/backend changes, validate with go run ./gui when practical. For GUI packaging, embedded assets, Wails config, or desktop integration changes, run pnpm run build in gui/frontend and the nearest relevant wails build validation
- For packaging, release, Dockerfile, build-script, or cross-platform changes, review .github/workflows/build-binaries.yml and validate the directly affected local path you can exercise, such as scripts/build.sh, scripts/build.ps1, a CLI build, a GUI build, or a Docker build

## Product Invariants

- This repository ships shared behavior across CLI, Wails GUI, and embedded web-serving mode. Preserve parity in request construction, option handling, and upload behavior where practical instead of letting one surface drift.
- The application targets Windows, Linux, and macOS. Avoid OS-specific assumptions in paths, process handling, filesystem behavior, archives, and build logic unless the code is already intentionally platform-gated.
- Preserve api.Mode usage and keep CLI, Wails GUI, and embedded web-serving flows aligned with shared request types under pkg/api and shared core behavior under internal.
- Changes around upload options, tracker overrides, retries, or execution flags should be checked from both CLI and GUI entrypoints when the same behavior exists in both surfaces.

## Unattended Safety

- Treat unattended and unattended-confirm flows as safety-critical. They must stay non-blocking and conservative.
- Do not introduce new interactive prompts, hidden confirmations, or ambiguous fallthrough behavior in unattended paths.
- When a choice cannot be made safely in unattended mode, prefer dry-run, site-check, explicit skip behavior, or a clear failure over attempting an upload with uncertain state.
- Preserve existing invariants such as site-check implying dry-run, debug implying safe non-upload behavior, and unattended flows keeping their current questionnaire/default-selection behavior unless the change explicitly updates those rules everywhere.
- Preserve safe skip and override behavior for dupes, rule failures, screenshot/image-host uploads, torrent injection, and retry flows. If one surface supports a skip or override, keep parity in the other surface when that behavior is shared.

## Agent Workflow Rules

These rules are mandatory for all AI coding agents working on this repository.

### Research Before Acting

- **Never hallucinate** API usage, library signatures, CLI flags, or configuration formats. Always use context7 MCP, web fetch, or read the actual source to verify before writing code.
- Before modifying any file, read it first. Understand existing patterns, naming, and flow before proposing changes.
- When working with third-party libraries (Wails, golangci-lint, Tailwind, Vite, etc.), fetch current docs rather than relying on training data.
- Use interactive tools when available — askQuestion in VS Code, terminal prompts — to clarify ambiguous requirements instead of guessing.

### Testing Discipline

- **New functionality must include tests.** No feature or behavior change ships without corresponding test coverage.
- Write tests that bring actual value: test real behavior, edge cases, error paths, and cross-surface consistency — not just happy paths.
- **If a test fails after code changes, assume your code is wrong, not the test.** Tests encode the project's intended behavior.
- Never blindly simplify, weaken, or rewrite a test to make it pass. Always do full research: read the test, understand what it asserts, trace the code path, and fix the root cause.
- Follow existing test patterns: `t.Parallel()`, stub service interfaces, table-driven tests, `ptr[T]()` helper for pointer values.
- When fixing a bug, first write a test that reproduces it, then fix the code to make the test pass.

### Impact Analysis

- Before modifying shared code (`pkg/api/`, `internal/core/`, `internal/errors/`), check all call sites across CLI, GUI, and web-serve surfaces.
- Before modifying or creating common/generic/helper functions, search for existing utilities that already do what you need — do not duplicate.
- When changing `pkg/api` types or interfaces, verify impact on: `cmd/upbrr/` (CLI), `internal/guiapp/` (GUI bindings), `internal/webserver/` (web), and `gui/frontend/wailsjs/` (generated TypeScript).
- When changing config struct fields, check YAML tag consistency (`snake_case`), defaults in `internal/config/defaults.go`, and any frontend settings UI that reads those fields.
- When adding a new tracker, verify that dupe checking (`internal/dupechecking/`), tracker config, description builder, and any tracker-specific overrides are all addressed.
- When adding a new Wails binding method in `internal/guiapp/`, the frontend TypeScript types in `wailsjs/` will need regeneration.

### Documentation & Maintenance

- **Update README.md** when adding features, changing setup steps, modifying CLI flags, or altering build/run instructions.
- **Update AGENTS.md** when adding new conventions, project structure changes, key type additions, or workflow rules.
- After updating any AI instruction file (AGENTS.md, .claude/rules/, .cursor/rules/, .github/instructions/), **run the sync script** (`scripts/sync-ai-instructions.ps1` on Windows, `scripts/sync-ai-instructions.sh` on Unix) — do not manually duplicate content across instruction files.
- Do not duplicate information that already exists in AGENTS.md into other instruction files. The sync script handles both scoped rule files (Go, frontend) and repo-wide files (`copilot-instructions.md`, `project.mdc`, `CLAUDE.md`). Claude's `CLAUDE.md` uses an `@AGENTS.md` import that the sync script manages automatically.

### Code Change Discipline

- Keep changes narrow and focused. A bug fix does not need surrounding refactoring. A feature does not need speculative configurability.
- Do not add dependencies without clear justification. Check if existing stdlib or project utilities cover the need first.
- Do not create new abstractions, helpers, or utilities for one-time operations.
- Check existing patterns before introducing new approaches — consistency matters more than novelty.
- Do not leave dead code, commented-out blocks, or TODO comments without associated issue references.
- After every change: run linters, run affected tests, verify the build compiles. Never submit changes that break CI.

### Cross-Surface Awareness

- This project ships three surfaces (CLI, GUI, web-serve) from shared core logic. When changing shared behavior, verify all three surfaces still work correctly.
- CLI-only changes (`cmd/upbrr/`) do not need GUI validation. GUI-only changes (`internal/guiapp/`, `gui/frontend/`) do not need CLI validation. But changes to `internal/core/`, `pkg/api/`, or shared services need both.
- When adding a new option, flag, or override: add it to `api.Request`/`api.UploadOptions`/`api.ExecutionOptions` first, then wire it through every surface that needs it.
- Frontend changes must work in both Wails native and browser (web-serve) runtimes. Test the code path for `isWebUIRuntime()` when touching API calls or event handling.

### Error Handling & Safety

- Wrap errors with context: `fmt.Errorf("what failed: %w", err)`. Never swallow errors silently.
- Preserve safe defaults: DryRun, SiteCheck, Debug flags must remain respected everywhere they are checked.
- Never introduce panics in production code paths. Return errors and let callers decide.
- Check context cancellation in long-running operations: `select { case <-ctx.Done(): return ctx.Err() }`.

## Scope Notes

- Keep guidance consistent with .golangci.yml, gui/frontend/package.json, and .github/workflows/*.yml rather than inventing new required steps.
- If a change affects only one area, run the smallest set of relevant checks. If a change crosses backend, frontend, GUI, packaging, or unattended execution boundaries, expand validation accordingly.
