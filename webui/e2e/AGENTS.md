# E2E Guidelines

Playwright E2E rules scoped to `webui/e2e`. Root + frontend rules apply.

## Commands

```bash
make e2e
make e2e-web
make e2e-cli
pnpm --dir webui run test:e2e:web
pnpm --dir webui run test:e2e:full
```

`make e2e` is preferred full local command. Installs frontend deps, builds frontend, syncs embedded assets, builds `dist/upbrr-e2e` (`.exe` on Windows) with `e2e` tag, runs all Playwright projects.

CI runs tests fully in parallel with two workers; the local default is eight. Developers on smaller or busy systems should reduce workers with `--workers=2` or `--workers=1`. The audio-analysis scenarios require FFmpeg on `PATH` (`ffmpeg -version`); CI installs the Ubuntu distribution package. To compare test-level parallel execution locally, build once with `make e2e-build`, then run these commands separately:

```bash
pnpm --dir webui exec playwright test --workers=1
pnpm --dir webui exec playwright test --workers=2 --fully-parallel
```

Each test owns its app process, local fakes, ports, and temp workspace. Do not run builds or separate Playwright invocations concurrently: they share the binary and report directories. Compare full browser runs on the target OS when changing worker counts; API/CLI-only timings do not establish browser stability. Local Windows runs remain supported. The crash-recovery scenario intentionally waits for the production 60-second lease to expire.

## Selecting local checks

Follow `CONTRIBUTING.md` before automatically running E2E: inspect the diff from the fetched PR-target merge base, plus staged, unstaged, and untracked changes. Skip unrelated/docs-only work. Narrow UI/spec edits use relevant projects or `--grep`; shared runtime, API/workflow, dependency, asset, and build changes use all five projects. Rebuild embedded assets/binary when those inputs change. Explicit full-suite requests and benchmark commands remain full even when the diff is empty. Separate visual-only changes use their visual config.

Missing Playwright browsers:

```bash
pnpm --dir webui exec playwright install chromium
```

Use Node 24 to reproduce the CI runtime; the package supports Node `>=24`, including Node 26. After updating Playwright and its lockfile, install the matching browser binaries with the command above before validating. Do not reuse an older browser revision for a new Playwright version.

Open HTML report from repo root:

```bash
pnpm --dir webui exec playwright show-report
```

## Projects

- `web-smoke`: embedded server at `http://localhost:7480`; nav/settings/invalid-input and authenticated appearance coverage, including palette contrast and mobile overflow.
- `web-base-path-smoke`: embedded server under configured base path; asset, navigation, API, event-stream routing coverage.
- `web-full-upload`: metadata, screenshot/image upload, tracker dry-run/upload, history, source MediaInfo, and mixed BBCode/HTML description preview and recovery through embedded Web UI.
- `cli-full-upload`: full CLI upload path against local fakes + temp config/DB.
- `api-full-upload`: authenticated composite API uploads, authority/idempotency, continuation/restart, cancellation, client-effect recovery.

## Visual checks

The route/theme/description capture and mobile touch specs use separate Playwright configs; `make e2e` does not include them. Build the embedded E2E binary first, then run the relevant suite from the repo root:

```powershell
make e2e-build
pnpm --dir webui exec playwright test --config visual-quality.config.ts
pnpm --dir webui exec playwright test --config visual-quality-touch.config.ts
```

Use `--grep` to run a focused visual scenario while iterating. The capture suite writes local evidence under ignored `docs/plans/visual-quality-evidence/`; inspect relevant screenshots and audit records, and never commit them.

## Harness Rules

- Web UI E2E uses embedded app as source of truth, not Vite.
- Use isolated temp workspace per test: config YAML, SQLite DB, media/torrent/screenshot fixtures.
- Use only local fake tracker/image-host/torrent/metadata services.
- Browser specs use the harness's `test` fixture to serve synthetic tracker icons without external favicon requests.
- Fake-service scenarios cover auth-blocked preflight lanes, questionnaire schemas/answers, reviewed upload/search names, WebUI stage controls, CLI/composite post-dupe tracker approval, tracker-lane isolation when one lane is blocked.
- Exercise login + 2FA only through dedicated Tracker Auth surface; upload workflows must not issue or accept auth/2FA continuation feedback.
- Auth/questionnaire failure blocks only affected tracker; other runnable lanes continue. Across continuation/restart, assert downstream work uses exact approved or stage-controlled tracker subset. Never implement tracker semantics in fake frontend.
- No real tracker, image host, torrent client, TMDB, or credentials in E2E.
- Service seams test-only or config/test fixture driven; production defaults unchanged.
- Process manager cleans up `dist/upbrr-e2e serve --config <temp>/config.yaml --dev-no-auth` (using the native `.exe` and path separators on Windows).

## Generated Artifacts

Ignored outputs:

- `webui/playwright-report/`
- `webui/test-results/`

Never commit Playwright traces, videos, screenshots, reports, temp DBs, or `dist/upbrr-e2e` binaries.

## CI

Pull request and manual workflow:

- `.github/workflows/e2e.yml`
- `pull_request` (opened, reopened, and updated) and `workflow_dispatch`.
- An always-present Linux gate tests `scripts/ci/e2e-scope.mjs` and reads the full base-to-head merge-base diff. Relevant changes run Ubuntu 24.04 E2E; unrelated/docs-only changes skip it without leaving an absent required check. Unreadable diffs or gate failures run the full suite; cancellation stays canceled. Manual dispatch always runs all projects.
- Builds frontend + embedded assets + CLI.
- Installs Playwright Chromium.
- Installs and verifies FFmpeg for the real audio-analysis decoder.
- Runs `make e2e`.
- Uploads report/traces on failure.
- Uses read-only repository permissions and local fakes; fork PRs need no repository secrets.
- Cancels obsolete runs for the same PR and retains the 45-minute job timeout.
