# API Contract Guidelines

Shared API contract rules under `pkg/api`. Root + `internal/AGENTS.md` rules still apply.

## Entry Points

- CLI: `cmd/upbrr`
- Core: `internal/core`
- WebUI server/API: `internal/webserver`
- Browser clients/types: `webui/src`

Preserve CLI/WebUI behavior unless intentionally changing entrypoint.

## Contract Changes

Changes require entrypoint parity review:

- `Request`
- `UploadOptions`
- `PreparedRelease`, `PrepareResult`, grouped canonical facts
- operation inputs + exact `ReleaseRef` generations
- dry-run/upload review payloads
- questionnaire answers
- description groups
- tracker overrides + retry/skip flags
- tracker decision modes, approval actions, snapshots, refs, exact candidate/approved tracker sets
- `UploadSummary` / `UploadedTorrent` registration artifacts + direct tracker-page links
- upload status/history rows
- `PreparationProgressUpdate` + `ImageUploadProgressUpdate`
- `OperationFailure` codes + recovery metadata

Check CLI builders, WebUI `/api/app/*` routes, browser request shapes, TS types.

## Canonical / Presentation Boundaries

- Canonical request/preparation contracts single-source; operation inputs reference one exact `ReleaseRef` generation.
- Keep prepared facts typed/reusable; no presentation correlation or workflow state in canonical inputs/facts.
- Backend-owned shared contracts: tracker auth capabilities/status/preflight failures, questionnaire schemas/answers, reviewed upload/search-name projections. Upload workflows neither resolve auth nor accept auth/2FA feedback; retained v1 auth action/feedback shapes support decoding compatibility only. Transport DTOs must not derive tracker-specific auth, taxonomy, description, media, questionnaire, or naming semantics.
- Post-dupe tracker approval binds one explicit ordered subset to exact candidate, duplicate-evidence, and input-fingerprint authority. Downstream contracts carry exact snapshot ref unchanged; WebUI workflows use persisted stage controls. Never infer/widen either tracker set in transport code.
- `UploadedTorrent.TorrentURL` = direct tracker-page link. `TorrentPath` + `DownloadURL` = private registered-artifact authority for retention/client injection; never expose secret-bearing download URLs in public projections/diagnostics.
- Auth requirement resolution stays internal + secret-free. Never add cookie values, API keys, passkeys, OTP secrets, auth keys, or HTTP clients to shared status/projection contracts.
- WebUI transport injects progress correlation IDs/timestamps. Progress payloads must be frontend-safe; image-host updates are absolute snapshots for one host attempt.
- Recovery decisions use structured `OperationFailure` codes/metadata, never error-message substring matching.

## Checks

- CLI/core: `go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api`.
- WebUI API: `go test -race -v -timeout 20m ./internal/webserver/... ./pkg/api`.
- Browser clients/types: `pnpm --dir webui run typecheck` + `pnpm --dir webui run test:unit`.
- Embedded runtime/UI: build frontend, sync embedded assets, rebuild CLI, inspect embedded web on `http://localhost:7480`.
- Cross-entrypoint behavior: `make test-go` + relevant frontend checks.