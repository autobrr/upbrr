---
name: verify-upbrr
description: Drive upbrr the way a user does and capture proof. Use for /verify-upbrr after changes to the CLI upload pipeline, the embedded Web UI, the release workflow, or tracker rules such as SAM.
---

# Verify upbrr

upbrr ships one Go binary with two primary user surfaces: the interactive CLI upload pipeline
(`dist/upbrr`) and the embedded React Web UI server (`dist/upbrr serve`). The Docusaurus site under
`documentation/` is public documentation, not runtime behavior.

## Launch

Build once per code change:

```bash
make backend    # dist/upbrr (CLI)
make e2e-build  # dist/upbrr-e2e plus synced embedded webui assets (Playwright projects)
```

`make e2e-build` installs frontend dependencies, builds the bundle, syncs embedded assets, and builds
the `e2e`-tagged binary. Browser projects and a fresh `serve` both need it; `make backend` alone only
covers CLI runs. Without embedded assets, `serve` exits with `web assets not found`. To build just the
assets for a manual `serve`:

```bash
pnpm --dir webui run build:bundle   # or: make frontend-bundle
sh scripts/sync-webui-assets.sh     # copies webui/dist into internal/webserver/assets
```

Both outputs are git-ignored (`webui/dist/`, `internal/webserver/assets/*`).

Short-lived CLI drives (`-input-only`, `-site-check`, an upload) need no server. Give every drive its
own config and database directory so runs cannot share state. For a manual Web UI drive (assets built
first):

```bash
mkdir -p tmp/verify-upbrr/run1
printf 'main_settings:\n  db_path: "%s/upbrr.db"\nlogging:\n  level: "debug"\n' \
  "$PWD/tmp/verify-upbrr/run1" > tmp/verify-upbrr/run1/config.yaml
dist/upbrr serve --config tmp/verify-upbrr/run1/config.yaml --dev-no-auth --addr 127.0.0.1:7491
```

`--dev-no-auth` skips the Web UI password. Ready when `curl -fsS http://127.0.0.1:7491/ >/dev/null`
exits 0. Record the PID and stop that PID when done; never kill by process name.

## Doctor

Read-only checks before trusting a drive:

```bash
go version                      # must satisfy go.mod
go build ./...                  # compile check
dist/upbrr --version            # binary exists and runs
ss -ltn | grep 7491             # nothing else owns the port you intend to use
```

If the binary was built before the change under test, rebuild; otherwise the drive proves the old
code.

## Drive

Pick the mapped feature closest to the change (see `features/`). Fast, real harnesses in this repo:

```bash
# Tracker policy truth (SAM naming, structure, language, duplicate slots)
go test -race -count=1 -timeout 20m ./internal/trackers/impl/unit3d/sites/sam/ \
  ./internal/trackers/dupe/ ./internal/trackers/data/ ./internal/mediafacts/

# Full CLI upload through local fake services + isolated temp config/DB
make e2e-build
pnpm --dir webui exec playwright test --project=cli-full-upload --workers=1 --reporter=list

# Embedded Web UI workflow through local fakes
pnpm --dir webui exec playwright test --project=web-full-upload --workers=1 --reporter=list
```

Manual CLI smoke without tracker writes:

```bash
dist/upbrr --version
dist/upbrr -input-only --config tmp/verify-upbrr/run1/config.yaml <source-path>
```

`-input-only` prepares local metadata and policy evaluation but performs no remote tracker work. It
is not a dry run of the full upload path: it stops before duplicate search and submission.

## Evidence

Write proof to `tmp/verify-upbrr/<run-id>/` (repository `tmp/` is ignored). Never commit evidence.
Capture, per drive:

- the exact command and its exit code (`... 2>&1 | tee tmp/verify-upbrr/<run-id>/<step>.log`);
- the observed state that proves the user-visible outcome, not just the final screen: prepared name,
  rule failure codes (`sam_language`, `sam_tv_structure`, `sam_season_pack_status`), duplicate
  relation, files written, fake-service counters;
- for Playwright, the HTML report under `webui/playwright-report/` plus the step log.

Proof standard: exercise the user path, not internal setters or test-only endpoints. Mock only at
boundaries that production already isolates (tracker HTTP, torrent client, image host, metadata
provider) and only with local fakes. A policy test proves policy truth; a harness run proves the
wiring. Say which one you ran.

## Cleanup

Remove instances and scratch state you created, keep the evidence:

```bash
kill "$SERVE_PID" 2>/dev/null || true
rm -rf tmp/verify-upbrr/run1
find webui/test-results -mindepth 1 -maxdepth 1 -type d -exec rm -rf {} + 2>/dev/null || true
```

Evidence survives in `tmp/verify-upbrr/<run-id>/` and `webui/playwright-report/`. Do not delete the
whole `tmp/verify-upbrr/` tree until the proof has been read.

## Helpers

- `make backend` - fast native CLI build.
- `make e2e-build` - embedded assets plus `dist/upbrr-e2e`.
- `make test-go` - full Go race suite.
- `make test-frontend` - frontend lint/dead-code/type/unit/format checks.
- `make lint` - architecture/path/literal/workflow/correction checks plus full Go lint.
- `python3 -m unittest discover -s scripts/issue_reports -v` - offline issue-report checks.

## Feature map

Start at `features/README.md`. Each file names the observable end state that proves the feature works.
