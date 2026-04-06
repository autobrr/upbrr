# Project Guidelines

## Code Quality

- Match the repository's existing style and keep changes narrow. Prefer fixing root causes over adding special cases.
- Format Go code with gofmt and goimports. Keep the local import prefix set to github.com/autobrr/upbrr.
- Treat .golangci.yml as the Go standard of record. New code should satisfy all linters, including ones currently disabled in repository-wide enforcement.
- The disabled linters are intentional policy for existing code, not missed cleanup. Do not reshape existing code just to satisfy containedctx, noctx, revive, or other currently disabled linters unless there is a functional reason or the surrounding code already follows that pattern.
- Do not silently swallow errors. Handle them explicitly by returning, wrapping, logging with intentional context, or making the ignore path obvious and justified in code.
- Add logging to new functions where it helps explain meaningful state, decisions, failures, retries, or user-visible outcomes. When touching existing functions that lack appropriate logging, bring them up to the same standard when it is practical and relevant to the change.
- Always redact private, secret, or user-sensitive information from logs using the repository's redaction handling in `internal/redaction/redaction.go`. Never log credentials, tokens, API keys, passkeys, cookies, full payloads containing secrets, or other sensitive user data without applying that standard.
- Keep log levels purposeful. `INFO` should provide concise, relevant progress or outcome details for end users during uploads. `DEBUG` should include richer decision-making context useful for developer troubleshooting. `TRACE` should capture near-complete operational flow for high-fidelity execution reporting.
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

## Scope Notes

- Keep guidance consistent with .golangci.yml, gui/frontend/package.json, and .github/workflows/*.yml rather than inventing new required steps.
- If a change affects only one area, run the smallest set of relevant checks. If a change crosses backend, frontend, GUI, packaging, or unattended execution boundaries, expand validation accordingly.
