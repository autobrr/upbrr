# CLI Guidelines

Scoped rules for `cmd/upbrr`. Root and `internal/AGENTS.md` rules still apply.

## Commands

```bash
go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api
make backend
make logpolicy
make pathpolicy
```

Add touched service/tracker/internal packages to focused `go test` runs.

## Unattended Modes

- `--unattended` / `--ua`: no prompts. Missing required data, BDMV playlist ambiguity, tracker-auth 2FA, or other manual work must return a clear error.
- `--unattended_confirm` / `--uac`: unattended defaults plus required confirmation/manual prompts are allowed.
- Code/tests should use `api.InteractionModeUnattended` for no-prompt behavior and `api.InteractionModeUnattendedConfirm` only when prompts are expected.
- Error text for no-prompt failures should say `unattended`; mention `unattended_confirm`/`--uac` only when telling users how to opt into prompts.
- Preserve unattended safety: no hidden prompts/confirms or ambiguous fallthrough.

## CLI Output / Logging

- Follow root log-level guidance for CLI logs and stdout/stderr.
- User-facing stdout/stderr may be copied into issues. Do not print credentials, usernames, passwords, tokens, API keys, auth keys, passkeys, cookie values, 2FA codes, challenge IDs, refreshed API tokens, or secret payloads.
- CLI dry-run stdout is shareable debug material: redact endpoints and payload values with the existing safe dry-run helpers before printing.
- Tracker-auth CLI logs should make the pre-dupe auth flow reconstructable without secrets: capability load count, auth-check start with tracker count, validation start per managed tracker with `tracker` and `auth_kind`, status result with `state`, cookie count, encrypted-storage availability and `needs_2fa`, per-tracker decision, and final ready/skipped counts.
- Decision values should be stable and searchable: `ready`, `keep`, `skip`, `blocked`, `prompt_2fa`, or `prompt_2fa_again`.
- Log unattended blocks as warnings with `unattended=true` and a stable reason such as `2fa_required`.
- Log non-fatal filtering details, such as auth skipped because preview rule failures already excluded a tracker, at debug level.
- Log prompt failures, blank 2FA submissions, failed 2FA submissions, and auth-not-ready skips at warning level.
- Redact every free-form status/error field before logging. Keep empty fields readable with a stable value such as `none`.
- Run `make logpolicy` when changing CLI dry-run output or auth-sensitive output/logging.

## Parity

- CLI request/options behavior shares contracts with Wails and embedded web.
- Request-shape changes usually require checking `pkg/api`, `internal/core`, `internal/guiapp`, `internal/webserver`, and `gui/frontend/src`.
