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
  from child environments. Stop only owned processes. Use `live-test cleanup`
  when the run is finished. Pending typed feedback or a failed resume defers cleanup
  and preserves the run for reconciliation. Use `-ResumeRun` to continue an eligible
  retained run or `-CleanupRun` to abandon feedback and reconcile image effects.
- Image hosting requires explicit bounded permission. Never call providers from
  the runner. Prefer supported, configured Lostimg in isolated profiles; other
  compatible hosts are allowed and confirmed non-deletable uploads are retained.
  Pending or unknown effects remain incomplete, even after local success.
- Validate with the PowerShell parser, `validate.ps1` and separate browser `--list`
  discovery. These do not replace affected Go/frontend/E2E checks or independent
  review; actual live execution requires explicit authorization.
