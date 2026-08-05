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

- Tracker authentication is resolved only through the dedicated Tracker Auth surface. Upload preflight emits a retryable blocked lane with structured recovery and no auth/2FA action; CLI must never log in, submit 2FA, or mutate auth state.
- Questionnaire modules never prompt or inspect CLI flags. They return central typed questionnaire actions; CLI owns presentation and interaction policy.
- `--unattended` / `--ua`: no prompts. Tracker-scoped manual prerequisites must skip that tracker and continue viable lanes. Auth-blocked lanes remain blocked for every interaction mode. Workflow-global missing data or ambiguity that prevents safe continuation must return a clear error.
- `--unattended_confirm` / `--uac`: unattended defaults plus required confirmation/manual prompts are allowed.
- Post-dupe tracker approval is workflow-global. Show candidate duplicate evidence and reviewed upload names, then require an explicit non-empty tracker subset. Strict unattended mode must stop with a clear error instead of prompting or auto-approving; `--uac` may prompt. Do not ask the obsolete final upload approval.
- Code/tests should use `api.InteractionModeUnattended` for no-prompt behavior and `api.InteractionModeUnattendedConfirm` only when prompts are expected.
- Error text for no-prompt failures should say `unattended`; mention `unattended_confirm`/`--uac` only when telling users how to opt into prompts.
- Preserve unattended safety: no hidden prompts/confirms or ambiguous fallthrough.
- One tracker blocked on auth or required questionnaire input must not stop other runnable tracker lanes.

## CLI Output / Logging

- Follow root log-level guidance for CLI logs and stdout/stderr.
- User-facing stdout/stderr may be copied into issues. Do not print credentials, usernames, passwords, tokens, API keys, auth keys, passkeys, cookie values, 2FA codes, challenge IDs, refreshed API tokens, or secret payloads.
- CLI debug payload stdout is shareable diagnostic material: redact endpoints and payload values with the existing safe dry-run helpers before printing.
- Shared preflight owns auth-readiness logging. Use stable secret-free fields such as `tracker`, `state`, and `decision=blocked`; CLI renders retained workflow outcomes only.
- Decision values should be stable and searchable: `ready`, `keep`, `skip`, or `blocked`.
- Log non-fatal filtering details, such as auth validation omitted because projection rules already excluded a tracker, at debug level.
- Log auth-not-ready blocked lanes at warning level without status messages, remote errors, or secret-bearing detail.
- Redact every free-form status/error field before logging. Keep empty fields readable with a stable value such as `none`.
- Run `make logpolicy` when changing CLI debug payload output or auth-sensitive output/logging.

## Parity

- CLI request/options behavior shares contracts with WebUI workflows.
- Request-shape changes usually require checking `pkg/api`, `internal/core`, `internal/webserver`, and `webui/src`.
