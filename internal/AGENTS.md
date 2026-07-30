# Backend Guidelines

Scoped rules for backend Go under `internal/`. Root repo rules still apply.

## Source Of Truth

- Go lint config: `.golangci.yml`
- Hook wiring: `lefthook.yml`
- Make targets: `Makefile`
- API/runtime contracts: `pkg/api`, `cmd/upbrr`, `internal/webserver`, `webui`

Tool output and config win over prose.

## Commands

```bash
make backend
make test-go
make lint
make logpolicy
make pathpolicy
make gofix-check-changed
go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api
go test -race -v -timeout 20m ./internal/webserver/... ./pkg/api
```

## Check Selection

- Touched Go package: `go test -race -v -timeout 20m <package>`.
- CLI behavior/flags/prompts: `go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api` plus touched services/trackers, then `make backend`.
- Core upload flow, tracker orchestration, config, DB, or API contracts: run focused package tests, then add `make test-go` when shared behavior can regress broadly.
- WebUI/API contracts: `go test -race -v -timeout 20m ./internal/webserver/... ./pkg/api`; add frontend `typecheck`/unit checks for request/response or browser-client changes.
- Tracker changes: test the tracker impl package and touched shared tracker packages; include config/defaults and catalog tests when definitions or auth material change.
- Logging/internal Go changes: `make logpolicy`.
- Path handling/local FS changes: `make pathpolicy`; use `make lint` before commit.
- Go modernization drift: `make gofix-check-changed`; use package-scoped `go fix -omitzero=false <packages>` only after review.

## Runtime Flow

1. Entrypoints build request/options from CLI args or WebUI route payloads.
2. `internal/preparedrelease` owns immutable source-scoped prepared generations and display projections, using `internal/sourcelayout` and `internal/externalidentity` for source resources and canonical identity.
3. `internal/core` consumes exact prepared generations and owns workflow orchestration, tracker eligibility, validation, screenshots/images, review, and upload.
4. `internal/clientdiscovery` owns normalized source-scoped torrent-client search.
5. `internal/webserver` owns browser transport, retained background jobs, and runtime activation through `RuntimeActivator`.
6. Generic tracker orchestration under `internal/trackers` consumes typed registry capabilities; auth, dupe, and data coordinators live in dedicated subpackages.
7. Tracker implementations are grouped by registry family: Unit3D sites under `internal/trackers/impl/unit3d/sites/<tracker>`, AvistaZ-family profiles under `internal/trackers/impl/azfamily`, and all other trackers under `internal/trackers/impl/standalone/<tracker>`. Tracker-local packages own endpoints, payloads, auth, lookup, rules, validation, descriptions, and policy.
8. DB/repository layers persist config, prepared generations, history, images, upload records, and status.

Preserve behavior across CLI and WebUI unless intentionally changing an entrypoint.

## Config / Runtime Ownership

- Runtime config can change after `SaveConfig` delegates activation to `RuntimeActivator`.
- Read WebUI config/core/logger via `currentConfig()`, `requireRuntime()`, or snapshots.
- Do not read `Backend.cfg` directly outside helpers.
- `Server.cfg` is startup-only.
- Config schema changes need `internal/config.Config`, embedded defaults, import/export, env overrides where relevant, settings UI/web parity, and secret redaction/encryption review.

## Canonical Release Ownership

- Canonical preparation contracts are single-source and preserve the caller's interaction mode.
- `PreparedRelease` contains typed, reusable source facts only. Do not retain workflow options, tracker choices, questionnaire answers, overrides, or outcomes in prepared state.
- Operations consume owner-local inputs plus an exact `ReleaseRef`; workflow interfaces must not accept a broad `PreparedRelease` when a narrower contract suffices.
- `PreparedReleaseDisplay` and `ProviderDisplay` construction belongs to `internal/preparedrelease`; tracker readiness belongs to workflow projection/preflight snapshots.
- Browser correlation IDs and event timestamps are transport concerns. Inject them under `internal/webserver`, not canonical preparation inputs or facts.

## Go Rules

- Match repo style; keep changes narrow; fix root cause; keep tests for changed behavior.
- Satisfy `.golangci.yml`; avoid broad `nolint`.
- Wrap external/interface errors where lint requires.
- Avoid unchecked assertions.
- Use `testing` helpers.
- Test file writes use `0o600` unless mode bits are under test.
- No wholesale `go fix`. Prefer `make gofix-check-changed`, then package-scoped `go fix -omitzero=false <packages>`.
- Keep `omitzero` disabled unless JSON semantics were reviewed.

## Logging / Redaction

- Use context-aware APIs where meaningful.
- Follow root log-level guidance with backend logger methods: `Infof`, `Warnf`, `Debugf`, and `Tracef`.
- Redact auth status text, remote response details, URLs, and raw errors before logging when they can contain secrets; use `internal/redaction.RedactValue` or tracker/common redaction helpers.
- No stdlib print/log under `internal/**`.
- Satisfy `cmd/logpolicy`.
- Redact via `internal/redaction/redaction.go`.
- Never log credentials, usernames, passwords, tokens, API keys, auth keys, passkeys, cookie values, 2FA codes, challenge IDs, refreshed API tokens, or secret payloads.

## Path Portability

- Local FS paths use `filepath`.
- Slash-data such as torrent paths, URLs, and API payload paths use `path` only with import-local `//nolint:depguard // <reason>`.
- Slash-data -> local FS: validate slash path, then `filepath.FromSlash`.
- Reject POSIX + Windows escapes on every OS: leading `/`, leading `\`, drive letters, UNC, `..`.
- Use `internal/pathing.IsWithinRoot` / `SamePath`; no ad-hoc `filepath.Rel` + prefix guards.
- Tests: `t.TempDir`, `filepath.Join`, `filepath.ToSlash`; no hardcoded OS-rooted literals/raw slash assertions for local FS.
- `cmd/pathpolicy` flags wrong path APIs, string-built local paths, slash-data FS calls/assertions, and ad-hoc guards. Rare exceptions need `//pathpolicy:allow <reason>` same/previous line.

## Lint / Hook Policy

- Pre-commit hook: Go format, log policy, path policy, frontend Prettier/ESLint on staged files.
- Pre-push hook: `make lint` and frontend typecheck.
- Do not rely on `make prepush` before a commit exists. Run relevant underlying checks before commit when the change can affect them.
- `make lint` runs architecture, path, and literal policies plus full `golangci-lint run --timeout=5m ./...`.
- Fix checker failures in the smallest relevant scope. Do not weaken checks, remove tests, or add broad `nolint` to hide failures.

## Generated / Scratch Path Risk

`.gitignore` does not protect Go package discovery. `golangci-lint run ./...` and other Go tooling can still find ignored `.go` files under repo-local scratch paths.

- Do not leave scratch `.go` files under repo paths like `tmp/`.
- If generated/scratch `.go` files are expected under an ignored directory, add a tool-level exclusion such as `.golangci.yml` `linters.exclusions.paths`.
- After creating generated dirs or broad Go tooling, run `make lint` before commit.
- Keep generated artifacts out of commits unless the task explicitly updates generated output.

Current expected local/generated ignores: `dist/`, `webui/dist/`, `internal/webserver/assets/*` except `.keep`, `webui/playwright-report/`, `webui/test-results/`, `tmp/`.

## Domain Guardrails

- Standalone tracker behavior belongs in `internal/trackers/impl/standalone/<tracker>`; Unit3D site exceptions belong in `internal/trackers/impl/unit3d/sites/<tracker>`. Each standalone package composes identity and static capabilities in `profile.go`; dynamic data/claim factories may use a small local wrapper around `standalone.Definition`. Register definitions explicitly in `internal/trackers/impl/registry.go`; generic packages must not import individual implementations.
- `internal/trackers/impl/registry.go` is the only complete supported-tracker composition list and groups definitions by family. Tracker profiles/definitions own endpoints and typed policy; `internal/config/defaults/example.yaml` owns ordered config surfaces/defaults. Generic metadata, auth, image-hosting, torrent-client, and frontend code must consume registry/catalog capabilities without tracker-name dispatch.
- Tracker semantic file ownership is strict:
  - `profile.go` / `definition.go`: identity, endpoint, family, static capabilities, policy bindings, callback wiring; no substantial algorithms.
  - `name.go`: pure versioned upload/search-name policy.
  - `auth.go` / `auth_*.go`: tracker-local login/session validation, CSRF/auth-key extraction, cookie selection, and typed auth failures.
  - `taxonomy.go`: pure tracker vocabulary mappings for category/type/source/codec/resolution/audio/language/tags/flags; no I/O.
  - `validation.go`: side-effect-free pre-duplicate payload constructibility and prepared-resource checks over `api.TrackerValidationSubject`; no network, filesystem reads, runtime secrets, or mutable services. Release-eligibility policy that extends `rules.go` may expose its validation binding there.
  - `description.go` plus optional `bbcode.go`: description preparation/composition and tracker markup.
  - `media.go`: MediaInfo/BDInfo/NFO path selection, reads, parsing, and normalized technical facts.
  - `questionnaire.go`: schema, stable answer keys, defaults, normalization, and validation; no prompting/UI.
  - `payload.go`: payload-only field/file encoding when separated.
  - `upload.go`: prepare/submit/preview orchestration, transport, response parsing, and immutable prepared state capture. It may call semantic modules but must not declare their algorithms, including validation or constructibility checks.
- Do not create empty marker files. Unit3D/AZ family defaults and central declarative auth count as owned behavior. Unit3D shares family auth/taxonomy/description/media and permits site-local callback implementations only in the matching semantic file. AZ-family behavior remains family-owned. Unrelated standalone trackers do not share taxonomy or description dispatch.
- `internal/trackers/auth` owns cross-surface status/import/validate/login/2FA/delete coordination, secret-free effective requirements, reusable cookie-login lifecycle, and TOTP. `internal/cookies` owns encrypted persistence. Tracker packages retain protocol forms/endpoints, validation markers, cookie filters, and challenge interpretation; tracker auth code never prompts.
- Every built-in tracker needs explicit effective auth requirements, a versioned release-name policy, and a resolved versioned validation policy. Family/default validation counts as explicit; tracker-specific payload constructibility checks live in `validation.go`, while release-eligibility policy may stay with `rules.go`. Principal payload fields consume `PreparationInput.ReviewedUploadName()` after central projection; no late name derivation in upload or payload code.
- `internal/architecturepolicy` keeps validation algorithms out of tracker `upload.go`, enforces static banned-group locality, Unit3D callback file bindings, name locality, reviewed-name payload fences, and forbidden imports. Add accepted/rejected fixture coverage when extending these rules.
- Upload preparation returns one immutable `trackers.PreparedOperation`: preview and submission must use the same captured canonical state. Submission may defer short-lived remote tokens, but must not rebuild payloads, reread mutable prepared inputs, or rerun image uploads. Dry-run and upload-review never receive a submittable plan.
- Downstream media preparation, dry-run, upload, artifact retention, and client injection use the exact tracker authority selected by the workflow decision mode: a durable post-dupe approval for gated workflows or existing stage controls for WebUI workflows. Never widen the set to unapproved or disabled trackers, and never reintroduce the obsolete final upload approval.
- A successful tracker submission returns registration authority through `api.UploadSummary.UploadedTorrents`. Preserve the direct tracker page in `TorrentURL` and retain the exact tracker-returned torrent in `TorrentPath` when available; reconstruct only when the tracker protocol makes that deterministic after confirmed success. Client injection uses this registered authority, never the pre-upload prepared torrent. A local artifact failure must not reclassify a successful remote upload.
- Standard Unit3D additions require the site profile, optional site rules/validation, one Unit3D registry entry, one example-config stanza without `url`, and combined rule/validation cases. Do not infer configured custom trackers; unsupported saved entries stay inert and preserve non-URL unknown fields.
- DB schema changes use stable, additive, forward-only, idempotent SQLite migrations where practical; preserve `schema_migrations` and the legacy `user_version` bridge.
- WebUI client changes need matching `/api/app/*` routes, typed request shapes, and unit/embedded browser verification.
- Generated/built outputs are mostly ignored; do not commit populated `internal/webserver/assets` unless deliberately updating generated artifacts.
