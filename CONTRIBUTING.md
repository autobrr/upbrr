# Contributing to upbrr

Thanks for taking interest in contributing! We welcome anyone who wants to help move the project forward.

If you have an idea for a bigger feature or a change, it is usually a good idea to discuss it first to make sure it aligns with the project. Open an issue or post in the [autobrr Discord](https://discord.autobrr.com).

This document is a guide to help you through the process of contributing to upbrr.

## Become a contributor

- **Code** — new features, bug fixes, improvements
- **Report bugs** — with clear reproduction steps and environment details
- **Tracker implementations** — support for new private trackers lives under
  `internal/trackers/impl`; follow [`ADDING_TRACKERS.md`](./ADDING_TRACKERS.md) for the required
  module, auth, naming, registry, config, and validation contracts
- **Documentation** — public guides under `documentation/`, this file, the README, or inline Go/TS docs

## Reporting bugs and requesting features

Use the [bug report template](.github/ISSUE_TEMPLATE/bug_report.md) for unexpected behavior and the [feature request template](.github/ISSUE_TEMPLATE/feature_request.md) for new capabilities. Search open and closed issues first. For development or PR builds, check the relevant Markdown documentation in this repository at the build's commit or branch; the published site describes released versions.

### Automated bug-report completeness check

Newly opened, human-authored bug reports are checked once by the default-branch workflow. A report is explicitly identified by its `[Bug]` title prefix (case-insensitive) or the `<!-- upbrr-bug-report:v1 -->` marker. A `[Feature]` prefix or `<!-- upbrr-feature-request:v1 -->` marker exempts it, even if it also contains bug identification. Unclassified issues are left for manual triage. All issues authored by Audionut's numeric GitHub account ID `13182387` are exempt; the event sender and display name do not grant this exemption.

“Significantly different” means any of these specific structural failures:

- Missing a required heading: **What happened?**, **Version and environment**, **Steps to reproduce**, **Expected behavior**, or **Actual behavior**. Heading case, level, and closing Markdown hashes may vary.
- Empty **What happened?**, **Expected behavior**, or **Actual behavior** content after removing comments and Markdown scaffolding, or a placeholder-only answer (`N/A`, `NA`, `none`, `unknown`, `TODO`, `TBD`, or ellipses; case-insensitive). Answers must contain at least one letter or number; this is a completeness check, not semantic validation.
- Missing or unfilled **upbrr version and build**, **Installation method**, or **Operating system and architecture** bullets under **Version and environment**. Keep those field names and put each answer after its colon on the same line; the same placeholder rules apply.
- No numbered reproduction step with a non-placeholder answer on the same line.

Additional details, subsections, screenshots, logs, and reordered sections are allowed. Optional sections may be removed or left blank. Checklist wording and checkbox state are not automatic rejection criteria; reporters are still expected to complete the prerequisites, including checking repository documentation for development builds. Headings inside comments or fenced code do not satisfy the required structure.

An incomplete report receives the `invalid` label, a GitHub Actions bot comment listing missing information, and closure as not planned. The reporter's text is never edited or deleted. Correct the report and ask a maintainer to reopen it. There is no automatic reopening, validation on edits/reopening/label changes, historical sweep, or validation on manually rerun workflow attempts. A report changed while the workflow was queued is left for manual triage.

The repository must have an `invalid` label. Failure to apply it or publish the explanation stops the workflow before closure. The issue job uses only default-branch code with `contents: read` and `issues: write`; issue text is read as JSON data, never interpolated into a shell command. Pull-request runs only execute the offline test job with read-only permissions. No live issue is changed by the tests:

```sh
python3 -m unittest discover -s scripts/issue_reports -v
```

## Developer guide

### Dependencies

Install the following on your machine:

- [Git](https://git-scm.com/)
- [Go](https://golang.org/dl/) — see [go.mod](./go.mod) for the required version
- [Node.js](https://nodejs.org) (`>=24` for the frontend and public documentation; Node 26 is within this range, while CI stays on Node 24 for a consistent baseline)
- [pnpm](https://pnpm.io/installation) — use the version pinned in `webui/package.json` and `documentation/package.json` via `packageManager`
- [GNU Make](https://www.gnu.org/software/make/) — top-level shortcuts for builds, checks, formatting, and hooks
- [golangci-lint](https://golangci-lint.run/) — use the version pinned in the [CI workflow](./.github/workflows/golangci-lint.yml) for hooks and local checks
- [Lefthook](https://github.com/evilmartians/lefthook) — git hooks runner (see [Git hooks](#git-hooks-lefthook))

### Supported platforms

Every script, hook, and VS Code task runs on macOS, Linux, and Windows using native tooling:

- **macOS 13 Ventura or newer** — zsh or bash (required by Go 1.27).
- **Linux** — bash.
- **Windows** — PowerShell 7+ or Git Bash. WSL works but is not required.

Notes:

- CLI binaries are named `upbrr` on Unix and `upbrr.exe` on Windows. The Makefile picks the right suffix automatically for `make backend`.
- Prefer `make build` for full local builds. It dispatches to `scripts/build.sh` on macOS/Linux and `scripts/build.ps1` on Windows; use the scripts directly only when debugging script behavior.
- Line endings are normalised via `.gitattributes` and `.editorconfig`.

Pull-request Go checks use seven shards on Linux, Windows, and macOS: general
core, native HDR core, decoded core, workflow, media, metadata/config/database, and
the remaining packages. Core tests are partitioned by their top-level names; all
other packages enter exactly one shard. New packages default to the remaining
packages shard unless they match an existing owner; `scripts/ci/go-test-packages.ps1 -Shard <name>` lists
its packages. Linux shards use the race detector; Windows and macOS shards run native tests. `make test-go` still runs the full race suite locally.
Go ownership/policy checks and golangci-lint run on separate runners.

## How to contribute

- **Fork and clone:** [Fork the upbrr repository](https://github.com/autobrr/upbrr/fork) and clone it to start working on your changes.
- **Branching:** Create a descriptively named branch.
  - Example: `git checkout -b fix/bt-dupe-check` or `git checkout -b feat/playlist-selection`
- **Coding:** Keep changes narrow and match the surrounding style. For Go, follow the rules in [`AGENTS.md`](./AGENTS.md) and let `golangci-lint` drive. For frontend work, see the [Web UI development guide](./webui/README.md) and [`webui/AGENTS.md`](./webui/AGENTS.md), and let the Lefthook Prettier + ESLint hooks do the work.
- **Commit messages:** We enforce [Conventional Commits](https://www.conventionalcommits.org/) via a repo-local validator. See [Commit message format](#commit-message-format) below.
  - No need to force-push or rebase — we squash on merge.
- **Pull requests:** Submit a PR with a clear description. Mark it _Draft_ if still in progress. Reference related issues. Update public documentation for user-visible changes.
- **Code review:** Be open to feedback during review.

### Pull request Docker images

Docker CI validates every pull request on Linux amd64. Same-repository, non-Dependabot pull requests also publish `ghcr.io/autobrr/upbrr:pr<number>` after a successful build. Fork and Dependabot pull requests remain build-only. The build job has read-only repository access; a separate job publishes the exported image without checking out or running pull request code. See the [installation guide](documentation/docs/getting-started/installation.md#test-a-pull-request-image) for testing instructions.

### Public documentation

The Docusaurus site under `documentation/` is published at [upbrr.com](https://upbrr.com). Keep affected pages, navigation, and cross-links synchronized with user-visible CLI, configuration, Web UI, workflow, tracker, installation, upgrade, and API changes.

```powershell
pnpm --dir documentation install --frozen-lockfile
pnpm --dir documentation run check
```

Production documentation is built from the exact release tag and published only by the release workflow. Never commit `documentation/build/`.

### Temporary dependency exceptions

The scheduled vulnerability workflow retains Stylelint and all CSS checks. The former
WebUI-only exception for GHSA-vfj7-8cjw-p6xm was removed after the advisory was withdrawn
and a full WebUI scan without exceptions passed. The earlier
[temporary risk acceptance](https://github.com/autobrr/upbrr/issues/585) was not remediation;
[the cleanup](https://github.com/autobrr/upbrr/issues/770) does not establish that braces was patched.
All advisories remain scanned without that exception.

If a braces exception is present, the workflow checks OSV and npm for a confirmed published
stable fix and checks every resolved braces version in the WebUI lockfile. Lookup failures
fail monitoring explicitly, and a withdrawn advisory requires reassessment rather than being
treated as a fixed release. Expiry restores failure and is never extended automatically.
With no exception, the remediation monitor exits without upstream lookups or follow-up issues;
the scheduled vulnerability scan still runs. A bot-created follow-up is deduplicated across open and closed
issues, including later fixed-version or cleanup-only observations. Update every affected
copy, verify a scan without exceptions passes, and remove the exception. An already-clean
lockfile produces a cleanup-only follow-up after that unignored scan passes.

Run the monitor's offline regression tests with Python 3.12 and PyYAML 6.0.3:

```sh
python -m unittest discover -s scripts/vulnerability -v
```

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

- `pre-commit` — runs `prettier --write` (webui), `eslint` (webui/src), and `golangci-lint fmt` (Go) on staged files. Formatters auto-re-stage their fixes. Staged Go changes also trigger repository-wide composite-literal and path-policy checks; staged `internal/**` Go changes trigger the repository-wide log-policy check. These policy checks inspect the working tree, including unstaged changes.
- `pre-push` — TypeScript changes trigger the full-project typecheck; Go changes trigger `make lint`, which runs the architecture ownership, path-portability, composite-literal layout, workflow-contract drift, and Input correction-choice drift checkers before golangci-lint. CI mirrors these Go policy checks and frontend checks for pull requests.
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

```sh
make build
./dist/upbrr serve --dev-no-auth
```

On Windows, run `./dist/upbrr.exe serve --dev-no-auth` instead.

Use `http://localhost:7480` for Playwright or browser automation. Avoid `5173` for embedded parity checks; stale embedded assets can otherwise hide or misrepresent frontend changes.

Stop the embedded server after inspection so later runs do not reuse an old process.

For theme, page layout, and rendered-description changes, check the embedded UI at desktop and mobile widths in the supported light and dark modes. `make e2e` covers representative theme, MediaInfo, and description states; the separate visual suites sweep more routes and palette combinations. The [Web UI development guide](./webui/README.md) describes the styling and rendering owners, and [Playwright guidance](./webui/e2e/AGENTS.md) lists the visual commands.

Input correction suggestions are generated from the backend's metadata dictionaries. After changing supported service, region, resolution, or distributor values, run `make correction-choices` and include the updated `webui/src/pages/input/correctionChoices.json`. `make correction-choices-check` (also part of `make lint`) checks for drift. Category, Type, Source, and Resolution controls accept only the generated choices; Service, Region, and Distributor retain custom entry.

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

### End-to-end checks

Every pull request gets a lightweight change-selection check. The full Ubuntu 24.04 Playwright job runs only when the complete PR diff touches runtime, frontend, normal E2E, dependency, asset, or shared build inputs. Documentation-only changes, the separate visual suites, and unrelated tooling skip that job. [`scripts/ci/e2e-scope.mjs`](scripts/ci/e2e-scope.mjs) owns the exact paths; source-tree unit-test changes are included conservatively. Deleted paths and both sides of renames count. An unreadable diff runs the full suite, and manual workflow dispatch always runs it. The workflow keeps read-only permissions and does not need repository secrets for forks.

Before local E2E validation, fetch the PR target branch and inspect the branch diff from its merge base, staged changes, unstaged changes, and untracked files. For a target of `origin/main` (use `upstream/main` for a fork when appropriate):

```sh
git fetch origin main
git diff --name-only --no-renames "$(git merge-base HEAD origin/main)" HEAD
git diff --cached --name-only --no-renames
git diff --name-only --no-renames
git ls-files --others --exclude-standard
```

Skip local E2E for documentation-only or unrelated changes. Run the affected project or `--grep` for a narrow browser/test change; shared runtime, workflow/API, build, and dependency changes need all five projects. A unit-test-only edit needs its unit checks and E2E only when its behavior also warrants an integration check. Selector-only changes need `node --test scripts/ci/e2e-scope.test.mjs` and workflow validation. These are selection guidelines: an explicit `make e2e`, full validation request, or benchmark command still runs the complete requested suite, even with no diff.

`make e2e` builds the embedded application and runs all five projects against isolated local fake services. Install FFmpeg on `PATH` for the audio-analysis scenarios; no real service credentials are needed. Normal local runs default to eight fully parallel workers. CI distributes individual tests across four Ubuntu jobs using Playwright's `--shard=N/4`, each with two workers and its own embedded build. Developers on smaller or busy systems may need fewer workers: after `make e2e-build`, use `pnpm --dir webui exec playwright test --workers=2` or `--workers=1`. Rebuild when runtime, embedded assets, or build inputs change; test-only runs may reuse a matching build. The [E2E workflow](.github/workflows/e2e.yml) uploads JSON test timings from each completed shard, plus uniquely named reports and test results on failure. The separate visual suites retain their own configuration.

### Other checks

For opt-in checks against configured services and local media, see the [live testing runner](scripts/live-testing/README.md). It uses an isolated profile and production build, blocks tracker submission and client writes, and defaults to no image uploads. Private media, credentials, screenshots, and run evidence stay outside the repository. These checks are separate from ordinary tests and CI.

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

# Public documentation:
pnpm --dir documentation run check

# Broad Go regression, lint, and policy sweeps:
make test-go-fast
make test-go
make architecturepolicy
make lint
make logpolicy
make pathpolicy
```

Use `make test-go-fast` for rapid full-suite iteration. It omits the race
detector; run focused race tests while developing and `make test-go` before
handoff when broad Go regressions are plausible.

`make test-go` bounds each race-enabled test binary to four logical CPUs and
four concurrent `t.Parallel` tests. Package test binaries retain Go's default
`-p` concurrency. These per-binary limits prevent each concurrently running
package from independently using the host's full CPU count. Override
`GO_TEST_SCHEDULER_FLAGS` when benchmarking another per-binary limit, or set it
empty to use Go defaults:

```sh
make test-go GO_TEST_SCHEDULER_FLAGS="-cpu=2 -parallel=2"
make test-go GO_TEST_SCHEDULER_FLAGS=
```

For Go modernization, check changed-package drift with `make gofix-check-changed`; apply reviewed fixes with `make gofix-changed`. Both keep `omitzero` disabled until JSON semantics are reviewed.

Alternatively, `make precommit` and `make prepush` run the configured Lefthook checks.
`make precommit` also runs stronger local validation: whitespace/conflict-marker checks, changed-package Go fix drift, full Go lint plus architecture/path/literal policies, log policy, and frontend lint/dead-code/type/unit/format checks. Hook wrappers do not replace focused Go tests, CSS Stylelint, embedded parity checks, or E2E checks when those areas changed.

## Project conventions

- The CLI and embedded WebUI share core services/config under `internal/`; frontend output is embedded from `internal/webserver/assets`.
- `internal/preparedrelease` owns canonical prepared generations and display projections; `internal/externalidentity`, `internal/sourcelayout` and `internal/clientdiscovery` own external identity, source resources and torrent-client discovery.
- `internal/webserver` owns runtime activation and workflow HTTP adapters; `internal/releaseworkflow` owns durable coordination; `webui/src/releaseSession` owns frontend release state and operation coordination. `pkg/api` holds shared request/response contracts.
- Tracker families live under `internal/trackers/impl/{unit3d,azfamily,standalone}`. `internal/trackers/impl/registry.go` is the sole supported manifest; generic auth/dupe/data coordination lives under `internal/trackers/{auth,dupe,data}`, and encrypted cookies under `internal/cookies`.
- Before tracker changes, follow [`ADDING_TRACKERS.md`](ADDING_TRACKERS.md) for semantic file ownership, registration/config, auth, naming, questions, immutable upload preparation and validation contracts. Do not duplicate those algorithms in generic infrastructure or UI. In particular, [content-filename transformations are prohibited](ADDING_TRACKERS.md#content-filename-policy); upload/search-title resolution is separate.
- Metadata producers resolve parser/provider/media/user precedence before publishing a generation. Trackers consume [finalized facts](ADDING_TRACKERS.md#finalized-metadata-source-of-truth), preserving provider overrides and presentation-independent facts. Changes need producer-to-projection coverage, conflicting/empty parser cases and a prepared-contract version bump when cached semantics change.
- Generic release policy, paths, clients and metainfo live under `internal/releasepolicy`, `internal/pathing`, `internal/torrentclient` and `internal/torrent/metainfo`. Generic BBCode, descriptions and image hosting live under `internal/{bbcode,description,imagehosting}`; tracker policy stays tracker-owned.
- Managed release files share the `internal/pathing/layout.ReleaseTempDirFor` owner under application `tmp`; feature outputs may nest inside it. Source fingerprints and verified-content identity govern reuse independently of names. Artifact layout changes require coordinated producers, persistence/hydration, consumers, History cleanup and upgrade docs; never add automatic legacy cache discovery or migration.
- `make architecturepolicy` enforces release artifact roots, tracker semantic ownership and reviewed-name boundaries; `make pathpolicy` checks filesystem portability and containment conventions. Generated frontend bundles, populated embedded assets, Playwright output and local binaries remain uncommitted local artifacts.

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

- Redact credentials at the logging boundary. Before sharing logs or failure artifacts, inspect them and remove remaining credentials, cookies, tokens, private URLs and unrelated personal information; automatic redaction is not a guarantee.
- Test assertion output is treated as CI-visible log material. Do not print raw cookies, auth headers, API keys, passkeys, passwords, CSRF tokens, OTP secrets, tracker announce URLs, or unredacted request/response bodies in tests, application logs, CLI output, or checker failure text.
- Pay particular attention in API/HTTP/cookie type handling, ensuring redaction of headers and response bodies.
- Fix redaction gaps at the shared boundary (`internal/redaction/redaction.go`) and enforce logging policy with `internal/logpolicy/checker.go`.
- Config values should be encrypted where appropriate, and only ever displayed in a redacted state.
- When the protocol supports it, prefer credentials in request headers or bodies over URL query parameters; keep HTTPS and redaction in either case.

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

[`AGENTS.md`](./AGENTS.md) contains cross-cutting repository rules and links to area guidance. Keep commands and contracts near their owner; do not repeat an ancestor's rules in every scoped file. Read the applicable ancestor chain and any explicitly required cross-area references before editing.

- Go: [`internal/AGENTS.md`](internal/AGENTS.md), also referenced for `cmd/` and `pkg/` work.
- CLI: [`cmd/upbrr/AGENTS.md`](cmd/upbrr/AGENTS.md); shared contracts: [`pkg/api/AGENTS.md`](pkg/api/AGENTS.md).
- Tracker contracts: [`internal/trackers/AGENTS.md`](internal/trackers/AGENTS.md); duplicate evaluation: [`internal/trackers/dupe/AGENTS.md`](internal/trackers/dupe/AGENTS.md).
- Browser transport/runtime: [`internal/webserver/AGENTS.md`](internal/webserver/AGENTS.md).
- Frontend checks: [`webui/AGENTS.md`](webui/AGENTS.md); React, styling and workflow ownership: [`webui/src/AGENTS.md`](webui/src/AGENTS.md); browser tests: [`webui/e2e/AGENTS.md`](webui/e2e/AGENTS.md).
- Signal fidelity: [`internal/services/audioanalysis/AGENTS.md`](internal/services/audioanalysis/AGENTS.md).
- Public site: [`documentation/AGENTS.md`](documentation/AGENTS.md); opt-in live harness: [`scripts/live-testing/AGENTS.md`](scripts/live-testing/AGENTS.md).

Discovery depends on the agent. [Codex](https://developers.openai.com/codex/guides/agents-md) loads the root-to-working-directory chain at startup; cross-area references still need explicit reading. `GUIDANCE.md` is not a default Codex discovery name, and none exists here. Existing `CLAUDE.md` symlinks point to the corresponding guidance for Claude Code.

When maintaining these files, keep the root small and route to scoped instructions or existing contributor documentation. Retain non-obvious repository invariants and verified commands rather than generic advice or copies of configuration. This follows [OpenAI's progressive-disclosure guidance](https://openai.com/index/harness-engineering/); it is not a line or token quota.
