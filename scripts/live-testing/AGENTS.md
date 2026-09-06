# Live testing runner

This directory is an opt-in production-build harness. It must remain separate from
`webui/e2e`, CI, and ordinary hooks. Use PowerShell 7 and the existing Playwright
installation; do not add dependencies.

- Store corpus paths, source identities, screenshots, session cookies, snapshots,
  logs, and feedback only under `%LOCALAPPDATA%/upbrr-live-testing`.
- Never submit trackers, mutate clients, waive duplicate blocks, choose unknown
  playlists, or invent pack completeness. Preserve current configured defaults
  unless the operator explicitly selects one tracker.
- Start only an identified production live-test binary. Remove E2E substitutions
  from child environments. Stop only owned processes, then use `live-test cleanup`.
- Image hosting requires explicit bounded permission. Never call providers from
  the runner. Prefer supported, configured Lostimg in isolated profiles; other
  compatible hosts are allowed and confirmed non-deletable uploads are retained.
  Pending or unknown effects remain incomplete, even after local success.
- Worker checks: PS parser, `validate.ps1`, and separate browser `--list` discovery.
  The parent owns Go/frontend/E2E checks, actual live execution, and independent review.
