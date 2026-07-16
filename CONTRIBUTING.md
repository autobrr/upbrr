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
- [Node.js](https://nodejs.org) (`^20.19.0`, `^22.13.0`, or `>=24`)
- [pnpm](https://pnpm.io/installation) (10 or newer — version is pinned in `webui/package.json` via `packageManager`)
- [GNU Make](https://www.gnu.org/software/make/) — top-level shortcuts for builds, checks, formatting, and hooks
- [golangci-lint](https://golangci-lint.run/) — used by hooks and CI
- [Lefthook](https://github.com/evilmartians/lefthook) — git hooks runner (see [Git hooks](#git-hooks-lefthook))

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
- **Coding:** Keep changes narrow and match the surrounding style. For Go, follow the rules in [`AGENTS.md`](./AGENTS.md) and let `golangci-lint` drive. For frontend work, also see [`webui/AGENTS.md`](./webui/AGENTS.md) and let the Lefthook Prettier + ESLint hooks do the work.
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
# `pnpm install` in webui/ brings in the Prettier + ESLint deps the hooks call.
pnpm --dir webui install
lefthook install
```

What runs when Git invokes hooks:

- `pre-commit` — on **staged files only**: `prettier --write` (webui), `eslint` (webui/src), `golangci-lint fmt` (Go), the composite-literal layout policy, `go run ./cmd/logpolicy` (when `internal/**` Go files change), and `go run ./cmd/pathpolicy` (when Go files change). Formatters auto-re-stage their fixes.
- `pre-push` — full-project TypeScript typecheck and `make lint`, which runs the architecture ownership, path-portability, and composite-literal layout checkers before golangci-lint. CI mirrors these Go policy checks and frontend checks for pull requests.
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
- **Scope** is optional (e.g. `config`, `ci`, `webui`, `cli`, or a tracker acronym like `bt`, `asc`).
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
pnpm --dir webui install --frozen-lockfile
make dev-frontend      # Vite dev server
pnpm --dir webui run lint
pnpm --dir webui run lint:dead
pnpm --dir webui run typecheck
pnpm --dir webui run test:unit
pnpm --dir webui run format:check

# CSS changes:
pnpm --dir webui run lint:style

# Bundle/runtime changes:
pnpm --dir webui run build
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

```

### Build

Full build (CLI with embedded WebUI):

```sh
make build
```

`make build` runs the platform build script, installs frontend deps, builds the React frontend, syncs it into `internal/webserver/assets`, then builds `dist/upbrr[.exe]`.

Individual pieces:

```sh
make backend          # CLI only
make frontend         # Typecheck + frontend bundle
make frontend-bundle  # Vite bundle only
```

## Tests and checks

Run checks for the areas you touched:

```sh
# Backend/Go packages:
go test -race -v -timeout 20m <package>

# CLI changes:
go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api
make backend

# WebUI/API contracts:
go test -race -v -timeout 20m ./internal/webserver/... ./pkg/api

# Frontend TS/TSX:
pnpm --dir webui run lint
pnpm --dir webui run lint:dead
pnpm --dir webui run typecheck
pnpm --dir webui run test:unit
pnpm --dir webui run format:check

# CSS:
pnpm --dir webui run lint:style

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
pnpm --dir webui run lint:style
```

Alternatively, `make precommit` and `make prepush` run the configured Lefthook checks.
`make precommit` also runs stronger local validation: whitespace/conflict-marker checks, changed-package Go fix drift, full Go lint plus architecture/path/literal policies, log policy, and frontend lint/dead-code/type/unit/format checks. Hook wrappers do not replace focused Go tests, CSS Stylelint, embedded parity checks, or E2E checks when those areas changed.

## Project conventions

- The frontend build output is embedded into the Go app from `internal/webserver/assets`.
- The CLI and embedded WebUI share the same core services and config model under `internal/`.
- Canonical prepared generations and display projections live under `internal/preparedrelease`; external identity, source-resource resolution, and torrent-client discovery live under `internal/externalidentity`, `internal/sourcelayout`, and `internal/clientdiscovery`.
- WebUI runtime activation and retained background jobs live under `internal/webserver`; frontend release state and shared Job coordination live under `webui/src/releaseSession` and `webui/src/jobRegistry`.
- Tracker-specific code lives under `internal/trackers/impl/<tracker>`. Unit3D protocol behavior lives in `internal/trackers/impl/unit3d`, with site profiles and exceptions under `internal/trackers/impl/unit3d/sites/<tracker>`.
- Shared tracker contracts and registry-driven orchestration live under `internal/trackers`; generic auth, dupe, and tracker-data coordinators are in its `auth`, `dupe`, and `data` subpackages.
- Generic release/path/torrent-client infrastructure lives under `internal/releasepolicy`, `internal/pathing`, and `internal/torrentclient`. Torrent metainfo helpers live under `internal/torrent/metainfo`.
- Generic BBCode, description, and image-hosting infrastructure lives under `internal/bbcode`, `internal/description`, and `internal/imagehosting`; tracker-specific policy stays with its tracker implementation.
- `pkg/api` holds request/response types shared across surfaces.
- Generated frontend bundles, populated embedded assets, Playwright output, and local binaries are ignored local artifacts. Do not commit them.

### Shareable fixtures and examples

Examples, docs, diagnostics, test fixtures, and failure artifacts should be safe to copy into issues, CI logs, and AI tools. Do not use real release names, real movie/show titles, or real provider IDs in shareable material.

Use synthetic placeholders instead:

- `Example Release 2026`
- `Example.Release.2026.1080p-GRP`
- `C:\path\to\release`
- `tt1234567`

Production domain data may keep real values when behavior depends on them, such as tracker banned-group lists or official tracker constants.
Prefer generic group tags such as `GRP` when the group is incidental; real group names are fine when they are relevant to behavior.

### Logging levels

Keep log levels purposeful.
- `INFO` should provide concise, relevant progress or outcome details for end users during uploads and other top-level workflows.
- `DEBUG` should include richer decision-making context useful for developer troubleshooting.
- `TRACE` should capture near-complete operational flow for high-fidelity execution reporting.

### Sensitive information

- Logging should be completely safe for blanket copy/pasting, without exposing any user sensitive credentials. Err on the side of caution.
- Test assertion output is treated as CI-visible log material. Do not print raw cookies, auth headers, API keys, passkeys, passwords, CSRF tokens, OTP secrets, tracker announce URLs, or unredacted request/response bodies in tests, application logs, CLI output, or checker failure text.
- Pay particular attention in API/HTTP/cookie type handling, ensuring redaction of headers and response bodies.
- Gaps fixes in `internal/redaction/redaction.go` or `internal/logpolicy/checker.go`, are especially appreciated pull requests.
- Config values should be encrypted where appropriate, and only ever displayed in a redacted state.
- If an endpoint supports GET/query style and POST/bearer style handling, use the endpoint that puts sensitive credentials into a secure packet, rather than as a plain URL parameter.

### Path portability

upbrr targets Windows, Linux, and macOS. Do not assume POSIX path behavior in Go code or tests unless the value is explicitly a torrent-internal path, URL path, or remote API payload path.

- Use `filepath.Join`, `filepath.Clean`, `filepath.Rel`, `filepath.Separator`, and related `filepath` APIs for local filesystem paths.
- Use `path` only for slash-delimited data formats, such as torrent-internal file names, URLs, or API payloads defined to use `/`.
- At boundaries between torrent/API paths and local filesystem paths, normalize deliberately: validate slash paths first, then convert with `filepath.FromSlash`.
- Security/path traversal checks must reject both POSIX and Windows absolute or escaping forms on every OS: leading `/`, leading `\`, drive-letter paths, UNC paths, and `..` segments.
- Use `internal/pathing.IsWithinRoot` and `internal/pathing.SamePath` for local root containment and path equality. Do not add ad-hoc `filepath.Rel` plus string-prefix guards; `pathpolicy` rejects those helper names outside `internal/pathing`.
- Tests should not assert raw `"/foo/"` substrings against local filesystem paths. Use `filepath.ToSlash(path)` for cross-platform assertions, or build expected paths with `filepath.Join`.
- Tests should not pass hardcoded OS-rooted literals such as `C:\...`, `\\server\share`, or `/tmp/...` into `filepath` calls. Use `t.TempDir` or existing path variables.
- Do not build local filesystem paths with string concatenation, `fmt.Sprintf`, or `strings.Join(..., "/")`. Use `filepath.Join`.
- Use `path.Base`, `path.Ext`, and related `path` APIs for URL/API paths. Use `filepath.Base`, `filepath.Ext`, and related `filepath` APIs for local paths. Legit stdlib `path` imports need import-local `//nolint:depguard // <slash-data reason>`.

`make pathpolicy` runs the repo-local AST checker for hardcoded OS-rooted literals in `filepath` calls, string-built local paths, wrong `path`/`filepath` package use, slash-data filesystem calls, slash assertions without `filepath.ToSlash`, and ad-hoc local path guard helpers outside `internal/pathing`. Rare intentional checker exceptions need `//pathpolicy:allow <reason>` on the same or previous line. `make lint`, pre-commit, and pre-push run it automatically.

## AI agent instructions

This project uses [AGENTS.md](https://agents.md/) — an open standard for guiding AI coding agents. The root [`AGENTS.md`](./AGENTS.md) file contains always-loaded repo rules and routes agents to scoped references:

- [`webui/AGENTS.md`](./webui/AGENTS.md) for frontend, React, CSS, TypeScript, and browser checks.
- [`internal/AGENTS.md`](./internal/AGENTS.md) for Go, path/log policy, trackers/config/domain rules, runtime architecture, lint/check policy, and generated/scratch path risks.
- [`cmd/upbrr/AGENTS.md`](./cmd/upbrr/AGENTS.md) for CLI flags, prompts, and unattended behavior.
- [`pkg/api/AGENTS.md`](./pkg/api/AGENTS.md) for cross-entrypoint API/runtime contracts.
- [`webui/e2e/AGENTS.md`](./webui/e2e/AGENTS.md) for Playwright E2E harness rules and commands.

Most modern AI coding tools support `AGENTS.md` natively or via simple configuration. `CLAUDE.md` files are symlinks to the same guidance for Claude Code.
