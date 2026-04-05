# Safety & Invariants

- CLI, GUI, and web-serve mode share request construction via `pkg/api` types and `internal/core`
- Cross-platform: Windows, Linux, macOS — no OS-specific assumptions unless intentionally gated
- Unattended/unattended-confirm flows are safety-critical and must stay non-blocking
- site-check implies dry-run; debug implies safe non-upload behavior
- Preserve safe skip/override behavior for dupes, rule failures, screenshot uploads, torrent injection, retry flows
- Do not introduce interactive prompts or hidden confirmations in unattended paths
- When a choice cannot be made safely in unattended mode, prefer dry-run, site-check, explicit skip, or clear failure

## Key Safety Types

- `api.UploadOptions`: DryRun, Debug, SiteCheck control execution safety
- `api.Mode`: ModeCLI vs ModeGUI — determines surface-specific behavior
- `api.ExecutionOptions`: Queue settings, SiteUploadTracker targeting

## Async Patterns

- Long operations (dupe checks, uploads) tracked by job ID in `internal/guiapp/`
- Frontend polls status via snapshot methods, not blocking calls
- Jobs emit events via `runtime.EventsEmit()` for real-time UI updates
