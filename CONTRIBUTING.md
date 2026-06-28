# Contributing to upbrr

Thanks for taking interest in contributing! We welcome anyone who wants to help move the project forward.

If you have an idea for a bigger feature or a change, it is usually a good idea to discuss it first to make sure it aligns with the project. Open an issue or post in the [autobrr Discord](https://discord.autobrr.com).

This document is a guide to help you through the process of contributing to upbrr.

## Become a contributor

- **Code** — new features, bug fixes, improvements
- **Report bugs** — with clear reproduction steps and environment details
- **Tracker implementations** — support for new private trackers lives under `internal/trackers/impl`
- **Documentation** — improvements to this file, the README, or inline Go/TS docs

## Developer guide

### Dependencies

Install the following on your machine:

- [Git](https://git-scm.com/)
- [Go](https://golang.org/dl/) — see [go.mod](./go.mod) for the required version
- [Node.js](https://nodejs.org) (20 LTS or newer)
- [pnpm](https://pnpm.io/installation) (10 or newer — version is pinned in `gui/frontend/package.json` via `packageManager`)
- [GNU Make](https://www.gnu.org/software/make/) — top-level shortcuts for builds, checks, formatting, and hooks
- [golangci-lint](https://golangci-lint.run/) — used by hooks and CI
- [Lefthook](https://github.com/evilmartians/lefthook) — git hooks runner (see [Git hooks](#git-hooks-lefthook))
- [Wails CLI](https://wails.io/) `v2.12.0` for desktop builds
  - Build scripts invoke `go run github.com/wailsapp/wails/v2/cmd/wails@v2.12.0`

On Linux, Wails builds also need GTK/WebKit development packages. The full list is in [`.github/workflows/build-binaries.yml`](./.github/workflows/build-binaries.yml) — key packages are `build-essential`, `libgtk-3-dev`, `libwebkit2gtk-4.1-dev`, `libglib2.0-dev`, and `pkg-config`. Builds using WebKitGTK 4.1 must pass the Go build tag `webkit2_41`.

### Supported platforms

Every script, hook, and VS Code task runs on macOS, Linux, and Windows using native tooling:

- **macOS** — zsh or bash.
- **Linux** — bash.
- **Windows** — PowerShell 7+ or Git Bash. WSL works but is not required.

Notes:

- CLI binaries are named `upbrr` on Unix and `upbrr.exe` on Windows. The Makefile picks the right suffix automatically for `make backend`.
- Prefer `make build` for full local builds. It dispatches to `scripts/build.sh` on macOS/Linux and `scripts/build.ps1` on Windows; use the scripts directly only when debugging script behavior.
- Line endings are normalised via `.gitattributes` and `.editorconfig`.

## How to contribute

- **Fork and clone:** [Fork the upbrr repository](https://github.com/autobrr/upbrr/fork) and clone it to start working on your changes.
- **Branching:** Create a descriptively named branch.
  - Example: `git checkout -b fix/bt-dupe-check` or `git checkout -b feat/playlist-selection`
- **Coding:** Keep changes narrow and match the surrounding style. For Go, follow the rules in [`AGENTS.md`](./AGENTS.md) and let `golangci-lint` drive. For frontend work, also see [`gui/frontend/AGENTS.md`](./gui/frontend/AGENTS.md) and let the Lefthook Prettier + ESLint hooks do the work.
- **Commit messages:** We enforce [Conventional Commits](https://www.conventionalcommits.org/) via a repo-local validator. See [Commit message format](#commit-message-format) below.
  - No need to force-push or rebase — we squash on merge.
- **Pull requests:** Submit a PR with a clear description. Mark it _Draft_ if still in progress. Reference related issues.
- **Code review:** Be open to feedback during review.

## Git hooks (Lefthook)

All contributors should install the git hooks once. They run Prettier, ESLint, golangci-lint formatting, the log-policy checker, the path-portability checker, and the repo's commit-message validator locally so issues surface before CI sees them.

```sh
# Go-only contributors (no Node required):
go install github.com/evilmartians/lefthook/v2@v2.1.0
lefthook install

# Frontend / full-stack contributors:
# `pnpm install` in gui/frontend/ brings in the Prettier + ESLint deps the hooks call.
cd gui/frontend && pnpm install && cd ../..
lefthook install
```

What runs when Git invokes hooks:

- `pre-commit` — on **staged files only**: `prettier --write` (gui/frontend), `eslint` (gui/frontend/src), `golangci-lint fmt` (Go), `go run ./cmd/logpolicy` (when `internal/**` Go files change), and `go run ./cmd/pathpolicy` (when Go files change). Formatters auto-re-stage their fixes.
- `pre-push` — full-project TypeScript typecheck and `make lint`, which runs the path-portability checker before golangci-lint. CI mirrors the Go test/pathpolicy OS matrix and frontend checks for pull requests.
- `commit-msg` — `go run ./cmd/commitmsgcheck` enforces [Conventional Commits](https://www.conventionalcommits.org/) without requiring Node.js or `pnpm install`.

Makefile shortcuts:

```sh
make precommit  # staged hook + stronger local validation
make prepush    # Lefthook pre-push
```

`make precommit` is intentionally stronger than the Git `pre-commit` hook. It runs `lefthook run pre-commit`, then `git diff --check`, `make gofix-check-changed`, `make lint`, `make logpolicy`, and `make test-frontend`. Use it before committing code changes when you want unstaged and full-repo lint/type/dead-code/unit/format issues caught locally.

Bypass (use sparingly, e.g. for emergency fixes or WIP commits):

- Skip one commit: `git commit --no-verify` (shortcut `-n`).
- Skip all hooks in a shell session: `LEFTHOOK=0 git commit ...`.

## Commit message format

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <subject>

[optional body]
[optional footer(s)]
```

- **Allowed types:** `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`.
- **Scope** is optional (e.g. `config`, `ci`, `gui`, `cli`, or a tracker acronym like `bt`, `asc`).
- **Subject** is imperative, lower-case, no trailing period, **115 characters max** (derived from history + 30% headroom).
- **Breaking change:** append `!` after type/scope (`feat!: drop Go 1.19`) or add a `BREAKING CHANGE:` footer.

Examples:

```
feat(config): add YAML import
fix(bt): correct duplicate search payload
chore(ci): switch to pnpm action v6
```

VS Code users: `.vscode/settings.json` wires the Source Control input-box length warning and Copilot's _Generate commit message_ prompt to the same 115-character budget, so IDE output passes the hooks on first try.

## Development environment

Clone the project and change directory:

```sh
git clone https://github.com/<your-user>/upbrr && cd upbrr
```

### Frontend

```sh
pnpm --dir gui/frontend install --frozen-lockfile
make dev-frontend      # Vite dev server
pnpm --dir gui/frontend run lint
pnpm --dir gui/frontend run lint:dead
pnpm --dir gui/frontend run typecheck
pnpm --dir gui/frontend run test:unit
pnpm --dir gui/frontend run format:check

# CSS changes:
pnpm --dir gui/frontend run lint:style

# Bundle/runtime changes:
pnpm --dir gui/frontend run build
```

For embedded web visual checks, test the embedded build rather than the Vite-only server. Rebuild the frontend, sync it into embedded assets, rebuild the CLI, then run the auth-disabled embedded server on the main port:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/build.ps1
.\dist\upbrr.exe serve --dev-no-auth
```

Use `http://localhost:7480` for Playwright or browser automation. Avoid `5173` for embedded parity checks; stale embedded assets can otherwise hide or misrepresent frontend changes.

Stop the embedded server after inspection so later runs do not reuse an old process.

### Backend

```sh
# Run the CLI (defaults to ~/.upbrr/db.sqlite):
go run ./cmd/upbrr

# Run the embedded web server:
go run ./cmd/upbrr serve

# Run the embedded web server without web auth for local frontend development:
go run ./cmd/upbrr serve --dev-no-auth

# Launch the Wails desktop GUI:
go run ./cmd/upbrr --gui
# or the dedicated GUI entrypoint:
go run ./gui
```

### Build

Full build (CLI + Wails GUI + embedded frontend):

```sh
make build
```

`make build` runs the platform build script, installs frontend deps, builds the React frontend, syncs it into `internal/guiapp/assets`, then builds the CLI into `dist/` and the Wails GUI into `gui/build/bin/`.

Individual pieces:

```sh
make backend          # CLI only
make frontend         # Typecheck + frontend bundle
make frontend-bundle  # Vite bundle only
make gui              # Wails GUI with current embedded assets
```

## Tests and checks

Run checks for the areas you touched:

```sh
# Backend/Go packages:
go test -race -v -timeout 20m <package>

# CLI changes:
go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api
make backend

# Wails/web/API parity:
go test -race -v -timeout 20m ./internal/guiapp ./internal/webserver ./internal/guishared ./pkg/api

# Frontend TS/TSX:
pnpm --dir gui/frontend run lint
pnpm --dir gui/frontend run lint:dead
pnpm --dir gui/frontend run typecheck
pnpm --dir gui/frontend run test:unit
pnpm --dir gui/frontend run format:check

# CSS:
pnpm --dir gui/frontend run lint:style

# Broad Go regression, lint, and policy sweeps:
make test-go
make lint
make logpolicy
make pathpolicy
```

Useful focused checks:

```sh
go test -race -v -timeout 20m <package>
make gofix-check-changed
make gofix-changed
pnpm --dir gui/frontend run lint:style
```

Alternatively, `make precommit` and `make prepush` run the configured Lefthook checks.
`make precommit` also runs stronger local validation: whitespace/conflict-marker checks, changed-package Go fix drift, full Go lint/path policy, log policy, and frontend lint/dead-code/type/unit/format checks. Hook wrappers do not replace focused Go tests, CSS Stylelint, embedded parity checks, or E2E checks when those areas changed.

## Project conventions

- The frontend build output is embedded into the Go app from `internal/guiapp/assets`.
- The GUI, CLI, and embedded web server share the same core services and config model under `internal/`.
- Tracker-specific code lives primarily under `internal/trackers/impl`. Shared tracker behaviour goes in `internal/trackers`, not in the impls.
- `pkg/api` holds request/response types shared across surfaces.
- The repo currently includes generated and built assets in a few locations; review changes carefully and avoid committing build output by accident.

### Path portability

upbrr targets Windows, Linux, and macOS. Do not assume POSIX path behavior in Go code or tests unless the value is explicitly a torrent-internal path, URL path, or remote API payload path.

- Use `filepath.Join`, `filepath.Clean`, `filepath.Rel`, `filepath.Separator`, and related `filepath` APIs for local filesystem paths.
- Use `path` only for slash-delimited data formats, such as torrent-internal file names, URLs, or API payloads defined to use `/`.
- At boundaries between torrent/API paths and local filesystem paths, normalize deliberately: validate slash paths first, then convert with `filepath.FromSlash`.
- Security/path traversal checks must reject both POSIX and Windows absolute or escaping forms on every OS: leading `/`, leading `\`, drive-letter paths, UNC paths, and `..` segments.
- Use `internal/pathutil.IsWithinRoot` and `internal/pathutil.SamePath` for local root containment and path equality. Do not add ad-hoc `filepath.Rel` plus string-prefix guards; `pathpolicy` rejects those helper names outside `internal/pathutil`.
- Tests should not assert raw `"/foo/"` substrings against local filesystem paths. Use `filepath.ToSlash(path)` for cross-platform assertions, or build expected paths with `filepath.Join`.
- Tests should not pass hardcoded OS-rooted literals such as `C:\...`, `\\server\share`, or `/tmp/...` into `filepath` calls. Use `t.TempDir` or existing path variables.
- Do not build local filesystem paths with string concatenation, `fmt.Sprintf`, or `strings.Join(..., "/")`. Use `filepath.Join`.
- Use `path.Base`, `path.Ext`, and related `path` APIs for URL/API paths. Use `filepath.Base`, `filepath.Ext`, and related `filepath` APIs for local paths. Legit stdlib `path` imports need import-local `//nolint:depguard // <slash-data reason>`.

`make pathpolicy` runs the repo-local AST checker for hardcoded OS-rooted literals in `filepath` calls, string-built local paths, wrong `path`/`filepath` package use, slash-data filesystem calls, slash assertions without `filepath.ToSlash`, and ad-hoc local path guard helpers outside `internal/pathutil`. Rare intentional checker exceptions need `//pathpolicy:allow <reason>` on the same or previous line. `make lint`, pre-commit, and pre-push run it automatically.

## AI agent instructions

This project uses [AGENTS.md](https://agents.md/) — an open standard for guiding AI coding agents. The root [`AGENTS.md`](./AGENTS.md) file contains always-loaded repo rules and routes agents to scoped references:

- [`gui/frontend/AGENTS.md`](./gui/frontend/AGENTS.md) for frontend, React, CSS, TypeScript, and browser checks.
- [`internal/AGENTS.md`](./internal/AGENTS.md) for Go, path/log policy, trackers/config/domain rules, runtime architecture, lint/check policy, and generated/scratch path risks.
- [`cmd/upbrr/AGENTS.md`](./cmd/upbrr/AGENTS.md) for CLI flags, prompts, and unattended behavior.
- [`pkg/api/AGENTS.md`](./pkg/api/AGENTS.md) for cross-entrypoint API/runtime contracts.
- [`gui/frontend/e2e/AGENTS.md`](./gui/frontend/e2e/AGENTS.md) for Playwright E2E harness rules and commands.

Most modern AI coding tools support `AGENTS.md` natively or via simple configuration. `CLAUDE.md` files are symlinks to the same guidance for Claude Code.
