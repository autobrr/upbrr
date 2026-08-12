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

`make e2e` is preferred full local command. Installs frontend deps, builds frontend, syncs embedded assets, builds `dist/upbrr-e2e.exe` with `e2e` tag, runs all Playwright projects.

Missing Playwright browsers:

```bash
pnpm --dir webui exec playwright install chromium
```

Open HTML report from repo root:

```bash
pnpm --dir webui exec playwright show-report
```

## Projects

- `web-smoke`: embedded server at `http://localhost:7480`, `--dev-no-auth`; nav/settings/invalid-input smoke coverage.
- `web-base-path-smoke`: embedded server under configured base path; asset, navigation, API, event-stream routing coverage.
- `web-full-upload`: metadata, screenshot/image upload, tracker dry-run/upload, history through embedded web UI.
- `cli-full-upload`: full CLI upload path against local fakes + temp config/DB.
- `api-full-upload`: authenticated composite API uploads, authority/idempotency, continuation/restart, cancellation, client-effect recovery.

## Harness Rules

- Web UI E2E uses embedded app as source of truth, not Vite.
- Use isolated temp workspace per test: config YAML, SQLite DB, media/torrent/screenshot fixtures.
- Use only local fake tracker/image-host/torrent/metadata services.
- Fake-service scenarios cover auth-blocked preflight lanes, questionnaire schemas/answers, reviewed upload/search names, WebUI stage controls, CLI/composite post-dupe tracker approval, tracker-lane isolation when one lane is blocked.
- Exercise login + 2FA only through dedicated Tracker Auth surface; upload workflows must not issue or accept auth/2FA continuation feedback.
- Auth/questionnaire failure blocks only affected tracker; other runnable lanes continue. Across continuation/restart, assert downstream work uses exact approved or stage-controlled tracker subset. Never implement tracker semantics in fake frontend.
- No real tracker, image host, torrent client, TMDB, or credentials in E2E.
- Service seams test-only or config/test fixture driven; production defaults unchanged.
- Process manager cleans up `dist/upbrr-e2e.exe serve --config <temp>\config.yaml --dev-no-auth`.

## Generated Artifacts

Ignored outputs:

- `webui/playwright-report/`
- `webui/test-results/`

Never commit Playwright traces, videos, screenshots, reports, temp DBs, or `dist/upbrr-e2e.exe`.

## CI

Manual workflow only:

- `.github/workflows/e2e.yml`
- `workflow_dispatch`
- Builds frontend + embedded assets + CLI.
- Installs Playwright Chromium.
- Runs `make e2e`.
- Uploads report/traces on failure.
