# Architecture Notes

Scoped reference for cross-module data flow, service boundaries, API routing, runtime config, and entrypoint parity.

## Entry Points

- CLI: `cmd/upbrr`
- Core orchestration: `internal/core`
- Wails backend: `internal/guiapp`
- Embedded web server/API: `internal/webserver`
- Shared GUI/web API contracts: `pkg/api`
- Frontend bridge: `gui/frontend/src`

Preserve behavior across CLI, Wails GUI, and embedded web unless intentionally changing an entrypoint.

## Runtime Flow

Typical upload flow:

1. Entrypoint builds request/options from CLI args, Wails method input, or web route payload.
2. `internal/core` prepares metadata, config, services, repository access, validation, screenshots/images, tracker review, and upload.
3. Services under `internal/services` handle metadata, torrents, image hosts, dupe checks, screenshots, and tracker orchestration.
4. Tracker implementations under `internal/trackers/impl` produce tracker-specific payloads and rule handling.
5. DB/repository layers persist config, history, images, upload records, and status.

When changing request structs or upload behavior, check all builders/routes/bridges that construct or consume the same API shape.

## Config / Runtime Ownership

- Runtime config can change after `SaveConfig` / `applyConfig`.
- Read Wails/web config/core/logger via `currentConfig()`, `requireRuntime()`, or snapshots.
- Do not read `App.cfg` / `Backend.cfg` directly outside helpers.
- `Server.cfg` is startup-only.
- Config schema changes need `internal/config.Config`, defaults, import/export, env overrides where relevant, settings UI/web parity, and redaction/encryption review.

## API / Bridge Parity

Changes to these require entrypoint parity review:

- `pkg/api.Request`
- `UploadOptions`
- `PreparedMetadata`
- dry-run/upload review payloads
- questionnaire answers
- description groups
- tracker overrides and retry/skip flags
- upload status/history rows

Check CLI builders, Wails methods, web `/api/app/*` routes, frontend bridge request shapes, and TS types.

## Database

- SQLite migrations are additive, forward-only, and idempotent where practical.
- Use stable migration IDs.
- Do not destructively drop/rename/tighten existing schema.
- Keep dependencies narrow.
- Preserve `schema_migrations` and legacy `user_version` bridge.

## Generated / Embedded Assets

- `make frontend` / `make frontend-bundle` update `gui/frontend/dist` only.
- Embedded GUI/web checks need asset sync into `internal/guiapp/assets` plus CLI rebuild.
- Generated/built outputs are mostly ignored; do not commit `dist/`, `gui/frontend/dist`, `gui/build/bin`, generated Wails bindings, or populated `internal/guiapp/assets` unless deliberately updating generated artifacts.
