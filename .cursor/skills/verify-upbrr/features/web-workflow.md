# Embedded Web UI release workflow

`upbrr serve` hosts the React app from embedded assets and exposes the workflow API. The UI mirrors
the CLI stages: Input, Dupe Check, Tracker Upload, History, Settings.

## Sub-features

- Input: add a source, watch preparation progress, correct metadata and questionnaire answers.
- Dupe Check: per-tracker duplicate results, approve or reject coexistence.
- Tracker Upload: stage-controlled lane execution, per-tracker continuation after failures.
- History: completed runs and their evidence.
- Settings: config, tracker auth, image hosts, client.

## How to get to it (user POV)

Start `dist/upbrr serve --config <isolated-config> --dev-no-auth --addr 127.0.0.1:7491` and open
`http://127.0.0.1:7491/`. Add a source on Input and follow the stages.

## Driving it with the harness

```bash
make e2e-build
pnpm --dir webui exec playwright test --project=web-full-upload --workers=1 --reporter=list
pnpm --dir webui exec playwright test --project=web-smoke --workers=1 --reporter=list
```

The harness starts the embedded server per test with a temp config/DB and local fakes. Use the
embedded binary, not the Vite dev server, whenever runtime or API behavior matters.

## Gotchas

- Each test owns its port, config, DB, and fakes; do not run Playwright invocations concurrently with
  `make e2e-build`, because they share the binary and report directories.
- `webui/playwright-report/` and `webui/test-results/` are ignored artifacts; read them, do not commit
  them.
- Check the API state and fake-service counters, not only the rendered screen.
