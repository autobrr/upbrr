# CLI Guidelines

Scope: `cmd/upbrr`. Root + `internal/AGENTS.md` rules apply.

## Commands

```bash
go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api
make backend
make logpolicy
make pathpolicy
```

Focused `go test`: include touched service/tracker/internal packages.

## Unattended Modes

- Tracker auth resolution: dedicated Tracker Auth surface only. Upload preflight: retryable blocked lane + structured recovery; no auth/2FA action. CLI never logs in, submits 2FA, or mutates auth state.
- Questionnaire modules: never prompt or inspect CLI flags; return central typed questionnaire actions. CLI owns presentation + interaction policy.
- `--unattended` / `--ua`: no prompts. Tracker-scoped manual prerequisites: skip tracker, continue viable lanes. Auth-blocked lanes stay blocked in every interaction mode. Workflow-global missing data/unsafe ambiguity: clear error.
- `--unattended_confirm` / `--uac`: unattended defaults; required confirmation/manual prompts allowed.
- Post-dupe tracker approval: workflow-global. Show candidate duplicate evidence + reviewed upload names; require explicit non-empty tracker subset. Strict unattended: clear error, no prompting/auto-approval. `--uac` may prompt. Never ask obsolete final upload approval.
- Code/tests: `api.InteractionModeUnattended` for no-prompt behavior; `api.InteractionModeUnattendedConfirm` only when prompts expected.
- No-prompt error text: say `unattended`; mention `unattended_confirm`/`--uac` only for prompt opt-in.
- Preserve unattended safety: no hidden prompts/confirms or ambiguous fallthrough.
- One tracker blocked by auth/required questionnaire input must not stop other runnable tracker lanes.

## CLI Output / Logging

- Follow root log-level guidance for CLI logs + stdout/stderr.
- User-facing stdout/stderr may enter issues. Never print credentials, usernames, passwords, tokens, API keys, auth keys, passkeys, cookie values, 2FA codes, challenge IDs, refreshed API tokens, or secret payloads.
- CLI debug payload stdout is shareable diagnostics: redact endpoints + payload values via existing safe dry-run helpers before printing.
- Shared preflight owns auth-readiness logging. Use stable secret-free fields: `tracker`, `state`, `decision=blocked`. CLI renders retained workflow outcomes only.
- Stable searchable decisions: `ready`, `keep`, `skip`, `blocked`.
- Non-fatal filtering details—e.g., auth validation omitted because projection rules excluded tracker—log at debug level.
- Auth-not-ready blocked lanes: warning level; omit status messages, remote errors, secret-bearing detail.
- Redact every free-form status/error field before logging. Represent empty fields with stable readable value such as `none`.
- Run `make logpolicy` after CLI debug payload or auth-sensitive output/logging changes.

## Parity

- CLI request/options behavior shares WebUI workflow contracts.
- Request-shape changes usually require checking `pkg/api`, `internal/core`, `internal/webserver`, and `webui/src`.