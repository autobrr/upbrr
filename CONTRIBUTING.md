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
- [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) — run through `make vuln` with `go run`; no separate install required
- [Lefthook](https://github.com/evilmartians/lefthook) — git hooks runner (see [Git hooks](#git-hooks-lefthook))
- [Wails CLI](https://wails.io/) `v2.10.1` for desktop builds
  - Install with `go install github.com/wailsapp/wails/v2/cmd/wails@v2.10.1`

On Linux, Wails builds also need GTK/WebKit development packages. The full list is in [`.github/workflows/build-binaries.yml`](./.github/workflows/build-binaries.yml) — key packages are `build-essential`, `libgtk-3-dev`, `libwebkit2gtk-4.0-dev` (or `4.1-dev`), `libglib2.0-dev`, and `pkg-config`.

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
- **Coding:** Keep changes narrow and match the surrounding style. For Go, follow the rules in [`AGENTS.md`](./AGENTS.md) and let `golangci-lint` drive. For the frontend, let the Lefthook Prettier + ESLint hooks do the work.
- **Commit messages:** We enforce [Conventional Commits](https://www.conventionalcommits.org/) via a repo-local validator. See [Commit message format](#commit-message-format) below.
  - No need to force-push or rebase — we squash on merge.
- **Pull requests:** Submit a PR with a clear description. Mark it _Draft_ if still in progress. Reference related issues.
- **Code review:** Be open to feedback during review.

## Git hooks (Lefthook)

All contributors should install the git hooks once. They run Prettier, ESLint, golangci-lint, the log-policy checker, and the repo's commit-message validator locally so issues surface before CI sees them.

```sh
# Go-only contributors (no Node required):
go install github.com/evilmartians/lefthook/v2@v2.1.0
lefthook install

# Frontend / full-stack contributors:
# `pnpm install` in gui/frontend/ brings in the Prettier + ESLint deps the hooks call.
cd gui/frontend && pnpm install && cd ../..
lefthook install
```

What runs when:

- `pre-commit` — on **staged files only**: `prettier --write` (gui/frontend), `eslint` (gui/frontend/src), `golangci-lint fmt` (Go), `go run ./cmd/logpolicy` (when `internal/**` Go files change). Formatters auto-re-stage their fixes.
- `pre-push` — full-project TypeScript typecheck and Go lint. CI also runs frontend lint, dead-code, and formatting checks.
- `commit-msg` — `go run ./cmd/commitmsgcheck` enforces [Conventional Commits](https://www.conventionalcommits.org/) without requiring Node.js or `pnpm install`.

Makefile shortcuts:

```sh
make precommit
make prepush
```

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
chore(ci): switch to pnpm action v5
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
make test-frontend     # ESLint, dead-code, typecheck, Prettier

# CSS changes:
pnpm --dir gui/frontend run lint:style
```

### Backend

```sh
# Run the CLI (defaults to ~/.upbrr/db.sqlite):
go run ./cmd/upbrr

# Run the embedded web server:
go run ./cmd/upbrr serve

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

Run the same checks CI runs:

```sh
make test-go            # Go tests with race detector
make test-frontend
make lint
make logpolicy
make vuln
```

Useful focused checks:

```sh
go test -race -v -timeout 20m <package>
make gofix-check-changed
make gofix-changed
pnpm --dir gui/frontend run lint:style
```

Alternatively, `make precommit` and `make prepush` run the configured Lefthook checks.

## Project conventions

- The frontend build output is embedded into the Go app from `internal/guiapp/assets`.
- The GUI, CLI, and embedded web server share the same core services and config model under `internal/`.
- Tracker-specific code lives primarily under `internal/trackers/impl`. Shared tracker behaviour goes in `internal/trackers`, not in the impls.
- `pkg/api` holds request/response types shared across surfaces.
- The repo currently includes generated and built assets in a few locations; review changes carefully and avoid committing build output by accident.

## AI agent instructions

This project uses [AGENTS.md](https://agents.md/) — an open standard for guiding AI coding agents. The root [`AGENTS.md`](./AGENTS.md) file contains build commands, code style rules, testing instructions, and project conventions. Most modern AI coding tools support `AGENTS.md` natively or via simple configuration.
