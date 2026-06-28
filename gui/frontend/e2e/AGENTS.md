# E2E Guidelines

Scoped rules for Playwright E2E under `gui/frontend/e2e`. Root and frontend rules still apply.

## Commands

```bash
make e2e
make e2e-web
make e2e-cli
make e2e-wails
pnpm --dir gui/frontend run test:e2e:web
pnpm --dir gui/frontend run test:e2e:full
```

`make e2e` is the preferred full local command. It installs frontend deps, builds the frontend, syncs embedded assets, builds `dist/upbrr-e2e.exe` with the `e2e` tag, and runs all Playwright projects.

If Playwright browsers are missing:

```bash
pnpm --dir gui/frontend exec playwright install chromium
```

To open the HTML report from repo root:

```bash
pnpm --dir gui/frontend exec playwright show-report
```

## Projects

- `web-smoke`: embedded server at `http://localhost:7480`, `--dev-no-auth`; nav/settings/invalid-input smoke coverage.
- `web-full-upload`: metadata, screenshot/image upload, tracker dry-run, tracker upload, and history through embedded web UI.
- `cli-full-upload`: full CLI upload path against local fakes and temp config/DB.
- `wails-basic`: Go parity tests for Wails/web/shared API packages; no native desktop UI automation.

## Harness Rules

- Web UI E2E uses the embedded app as source of truth, not Vite.
- Use isolated temp workspace per test: config YAML, SQLite DB, media/torrent/screenshot fixtures.
- Use local fake tracker/image-host/torrent/metadata services only.
- No real tracker, image host, torrent client, TMDB, or credentials in E2E.
- Service seams must be test-only or config/test fixture driven; production defaults stay unchanged.
- Process manager must clean up `dist/upbrr-e2e.exe serve --config <temp>\config.yaml --dev-no-auth`.

## Generated Artifacts

Ignored local outputs:

- `gui/frontend/playwright-report/`
- `gui/frontend/test-results/`

Do not commit Playwright traces, videos, screenshots, reports, temp DBs, or `dist/upbrr-e2e.exe`.

## CI

Manual workflow only:

- `.github/workflows/e2e.yml`
- `workflow_dispatch`
- Builds frontend + embedded assets + CLI.
- Installs Playwright Chromium.
- Runs `make e2e`.
- Uploads report/traces on failure.
