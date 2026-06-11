# Linting And Check Policy

Scoped reference for lint behavior, hook internals, generated artifacts, and checker failures.

## Source Of Truth

- Go lint config: `.golangci.yml`
- Hook wiring: `lefthook.yml`
- Make targets: `Makefile`
- Frontend scripts: `gui/frontend/package.json`

Tool output and config win over prose.

## Underlying Hook Checks

Pre-commit hook:

- Go format: `golangci-lint fmt {staged_files}`
- Log policy: `go run ./cmd/logpolicy`
- Path policy: `go run ./cmd/pathpolicy`
- Frontend format: `pnpm exec prettier --write --cache --ignore-unknown {staged_files}`
- Frontend lint: `pnpm exec eslint --cache --no-error-on-unmatched-pattern {staged_files}`

Pre-push hook:

- Go lint: `make lint`
- Frontend typecheck: `pnpm --dir gui/frontend run typecheck`

Do not rely on `make prepush` before a commit exists. Run the relevant underlying checks before commit when the change can affect them.

## Generated / Scratch Path Risk

`.gitignore` does not protect Go package discovery. `golangci-lint run ./...` and other Go tooling can still find ignored `.go` files under repo-local scratch paths.

Rules:

- Do not leave scratch `.go` files under repo paths like `tmp/`.
- If generated/scratch `.go` files are expected under an ignored directory, add a tool-level exclusion such as `.golangci.yml` `linters.exclusions.paths`.
- After creating generated dirs or broad Go tooling, run `make lint` before commit.
- Keep generated artifacts out of commits unless the task explicitly updates generated output.

Current expected local/generated ignores include:

- `dist/`
- `gui/frontend/dist/`
- `gui/build/bin/`
- `internal/guiapp/assets/*` except `.keep`
- `gui/frontend/playwright-report/`
- `gui/frontend/test-results/`
- `tmp/`

## Go Checks

- `make lint` runs path policy plus full `golangci-lint run --timeout=5m ./...`.
- `make logpolicy` checks logging rules.
- `make pathpolicy` checks path portability rules.
- `make gofix-check-changed` detects Go modernization drift for changed packages.

Fix checker failures in the smallest relevant scope. Do not weaken checks, remove tests, or add broad `nolint` to hide failures.

## Frontend Checks

- `pnpm --dir gui/frontend run typecheck` for TS/TSX changes.
- `pnpm --dir gui/frontend run test:unit` for runtime/API bridge or component behavior.
- `pnpm --dir gui/frontend run lint:style` for CSS changes.
- `pnpm --dir gui/frontend run format:check` before commit when touching formatted frontend/config docs.

ESLint may warn that Playwright E2E/config files are ignored if they are outside configured lint globs. Treat warnings as signal, but failures are the gate.
