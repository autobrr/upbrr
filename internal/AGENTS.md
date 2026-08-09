# Backend Guidelines

Scope: backend Go under `internal/`; root repo rules apply.

## Source Of Truth

- Go lint: `.golangci.yml`
- Hooks: `lefthook.yml`
- Make targets: `Makefile`
- API/runtime: `pkg/api`, `cmd/upbrr`, `internal/webserver`, `webui`

Tool output/config override prose.

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
- CLI behavior/flags/prompts: `go test -race -v -timeout 20m ./cmd/upbrr ./internal/core ./pkg/api` + touched services/trackers; then `make backend`.
- Core upload, tracker orchestration, config, DB, API contracts: focused package tests; add `make test-go` when shared behavior may regress broadly.
- WebUI/API contracts: `go test -race -v -timeout 20m ./internal/webserver/... ./pkg/api`; add frontend `typecheck`/unit checks for request/response or browser-client changes.
- Tracker changes: test implementation + touched shared tracker packages; include config/default/catalog tests when definitions or auth material change.
- Logging/internal Go: `make logpolicy`.
- Path/local FS: `make pathpolicy`; pre-commit `make lint`.
- Go modernization: `make gofix-check-changed`; reviewed packages only: `go fix -omitzero=false <packages>`.

## Runtime Flow

1. Entrypoints build request/options from CLI args or WebUI payloads.
2. `internal/preparedrelease` owns immutable source-scoped prepared generations/display projections; uses `internal/sourcelayout` and `internal/externalidentity` for source resources/canonical identity.
3. `internal/core` consumes exact prepared generations; owns workflow orchestration, tracker eligibility, validation, screenshots/images, review, upload.
4. `internal/clientdiscovery` owns normalized source-scoped torrent-client search.
5. `internal/webserver` owns browser transport, retained background jobs, runtime activation via `RuntimeActivator`.
6. Generic `internal/trackers` orchestration consumes typed registry capabilities; dedicated subpackages own auth, dupe, data coordination.
7. Implementations by registry family: Unit3D under `internal/trackers/impl/unit3d/sites/<tracker>`, AvistaZ profiles under `internal/trackers/impl/azfamily`, others under `internal/trackers/impl/standalone/<tracker>`. Tracker-local packages own endpoints, payloads, auth, lookup, rules, validation, descriptions, policy.
8. DB/repositories persist config, prepared generations, history, images, upload records, status.

Preserve CLI/WebUI behavior unless intentionally changing one entrypoint.

## Config / Runtime Ownership

- Runtime config may change after `SaveConfig` delegates activation to `RuntimeActivator`.
- Read WebUI config/core/logger through `currentConfig()`, `requireRuntime()`, or snapshots.
- Never read `Backend.cfg` directly outside helpers.
- `Server.cfg` is startup-only.
- Config schema changes require `internal/config.Config`, embedded defaults, import/export, relevant env overrides, settings UI/web parity, secret redaction/encryption review.

## Canonical Release Ownership

- Canonical preparation contracts: single-source; preserve caller interaction mode.
- `PreparedRelease`: typed reusable source facts only; no workflow options, tracker choices, questionnaire answers, overrides, outcomes.
- Operations consume owner-local inputs + exact `ReleaseRef`; workflow interfaces accept only the narrowest sufficient contract, not broad `PreparedRelease`.
- `PreparedReleaseDisplay`/`ProviderDisplay` construction belongs to `internal/preparedrelease`; tracker readiness belongs to workflow projection/preflight snapshots.
- Browser correlation IDs/event timestamps are transport concerns; inject under `internal/webserver`, never canonical preparation inputs/facts.

## Go Rules

- Match repo style; narrow changes; root-cause fixes; tests for changed behavior.
- Satisfy `.golangci.yml`; avoid broad `nolint`.
- Wrap external/interface errors when lint requires.
- No unchecked assertions.
- Use `testing` helpers.
- Test writes: `0o600` unless testing mode bits.
- No wholesale `go fix`; prefer `make gofix-check-changed`, then package-scoped `go fix -omitzero=false <packages>`.
- Keep `omitzero` disabled unless JSON semantics reviewed.

## Logging / Redaction

- Use context-aware APIs when meaningful.
- Follow root levels via `Infof`, `Warnf`, `Debugf`, `Tracef`.
- Before logging, redact auth status, remote response details, URLs, raw errors when secret-bearing; use `internal/redaction.RedactValue` or tracker/common helpers.
- No stdlib print/log under `internal/**`.
- Satisfy `cmd/logpolicy`.
- Redaction source: `internal/redaction/redaction.go`.
- Never log credentials, usernames, passwords, tokens, API keys, auth keys, passkeys, cookies, 2FA codes, challenge IDs, refreshed API tokens, secret payloads.

## Path Portability

- Local FS paths: `filepath`.
- Slash-data—torrent paths, URLs, API payload paths: `path` only with import-local `//nolint:depguard // <reason>`.
- Slash-data to local FS: validate, then `filepath.FromSlash`.
- Reject POSIX/Windows escapes on every OS: leading `/`, leading `\`, drives, UNC, `..`.
- Use `internal/pathing.IsWithinRoot` / `SamePath`; no custom `filepath.Rel` + prefix guards.
- Tests: `t.TempDir`, `filepath.Join`, `filepath.ToSlash`; no hardcoded OS-rooted literals/raw slash assertions for local FS.
- `cmd/pathpolicy` detects wrong APIs, string-built local paths, slash-data FS calls/assertions, custom guards. Rare exception: same/previous-line `//pathpolicy:allow <reason>`.

## Lint / Hook Policy

- Pre-commit: Go format, log/path policies, frontend Prettier/ESLint on staged files.
- Pre-push: `make lint` + frontend typecheck.
- Do not rely on `make prepush` before a commit exists; run relevant checks pre-commit.
- `make lint`: architecture, path, literal, workflow-contract checks + full `golangci-lint run --timeout=5m ./...`.
- Fix failures in smallest relevant scope; never weaken checks, remove tests, or add broad `nolint` to hide failures.

## Generated / Scratch Path Risk

`.gitignore` does not prevent Go package discovery; `golangci-lint run ./...` and other Go tools can find ignored repo-local `.go` files.

- No scratch `.go` files under repo paths such as `tmp/`.
- Expected generated/scratch `.go` files under ignored dirs require tool exclusion, e.g. `.golangci.yml` `linters.exclusions.paths`.
- After generated dirs or broad Go tooling, run `make lint` pre-commit.
- Exclude generated artifacts from commits unless explicitly updating them.

Expected local/generated ignores: `dist/`, `webui/dist/`, `internal/webserver/assets/*` except `.keep`, `webui/playwright-report/`, `webui/test-results/`, `tmp/`.

## Domain Guardrails

- Duplicate-search, evaluation, adapter, or policy work anywhere under `internal/trackers` must also read and follow `internal/trackers/dupe/AGENTS.md`, including tracker-local `dupe.go` and `dupe_policy.go` files.
- Standalone behavior belongs in `internal/trackers/impl/standalone/<tracker>`; Unit3D exceptions in `internal/trackers/impl/unit3d/sites/<tracker>`. Each standalone package composes identity/static capabilities in `profile.go`; dynamic data/claim factories may wrap `standalone.Definition` locally. Explicitly register definitions in `internal/trackers/impl/registry.go`; generic packages never import implementations.
- `internal/trackers/impl/registry.go` is the sole complete supported-tracker composition list, grouped by family. Profiles/definitions own endpoints/typed policy; `internal/config/defaults/example.yaml` owns ordered config/defaults. Generic metadata, auth, image-hosting, torrent-client, frontend code consume registry/catalog capabilities without tracker-name dispatch.
- Strict tracker semantic ownership:
  - `profile.go` / `definition.go`: identity, endpoint, family, static capabilities, policy bindings, callback wiring; no substantial algorithms.
  - `name.go`: pure versioned upload/search-name policy.
  - `auth.go` / `auth_*.go`: local login/session validation, CSRF/auth-key extraction, cookie selection, typed auth failures.
  - `taxonomy.go`: pure category/type/source/codec/resolution/audio/language/tag/flag mappings; no I/O.
  - `validation.go`: side-effect-free pre-dupe payload constructibility/prepared-resource checks over `api.TrackerValidationSubject`; no network, FS reads, runtime secrets, mutable services. Release-eligibility extensions to `rules.go` may expose validation binding there.
  - `description.go` + optional `bbcode.go`: description preparation/composition and tracker markup.
  - `media.go`: MediaInfo/BDInfo/NFO selection, reads, parsing, normalized technical facts.
  - `questionnaire.go`: schema, stable answer keys, defaults, normalization, validation; no prompting/UI.
  - `payload.go`: payload-only field/file encoding when separated.
  - `upload.go`: prepare/submit/preview orchestration, transport, response parsing, immutable prepared-state capture. May call semantic modules; never define their algorithms, validation, or constructibility checks.
- No empty marker files. Unit3D/AZ defaults and central declarative auth count as owned behavior. Unit3D shares family auth/taxonomy/description/media; site callbacks only in matching semantic files. AZ behavior stays family-owned. Unrelated standalone trackers never share taxonomy/description dispatch.
- `internal/trackers/auth` owns cross-surface status/import/validate/login/2FA/delete coordination, secret-free effective requirements, reusable cookie-login lifecycle, TOTP. `internal/cookies` owns encrypted persistence. Tracker packages retain protocol forms/endpoints, validation markers, cookie filters, challenge interpretation; tracker auth never prompts.
- Every built-in tracker requires explicit effective auth requirements, versioned release-name policy, resolved versioned validation policy. Family/default validation is explicit. Tracker payload-constructibility checks belong in `validation.go`; release eligibility may remain in `rules.go`. Principal payload fields use `PreparationInput.ReviewedUploadName()` after central projection; no late name derivation in upload/payload code.
- `internal/architecturepolicy` keeps validation algorithms out of tracker `upload.go`; enforces static banned-group locality, Unit3D callback-file bindings, name locality, reviewed-name payload fences, forbidden imports. Extend rules with accepted/rejected fixtures.
- Upload preparation returns one immutable `trackers.PreparedOperation`; preview/submission use identical captured canonical state. Submission may defer short-lived remote tokens, but never rebuild payloads, reread mutable prepared inputs, or rerun image uploads. Dry-run/upload-review never receive a submittable plan.
- Downstream media preparation, dry-run, upload, artifact retention, client injection use exact tracker authority selected by workflow mode: durable post-dupe approval for gated workflows or existing WebUI stage controls. Never widen to unapproved/disabled trackers or restore obsolete final upload approval.
- Successful submission returns registration authority through `api.UploadSummary.UploadedTorrents`. Preserve direct tracker page in `TorrentURL`; retain exact tracker-returned torrent in `TorrentPath` when available. Reconstruct only when protocol makes it deterministic after confirmed success. Client injection uses registered authority, never pre-upload prepared torrent. Local artifact failure never reclassifies successful remote upload.
- Standard Unit3D addition: site profile, optional rules/validation, one registry entry, one example-config stanza without `url`, combined rule/validation cases. Never infer configured custom trackers; unsupported saved entries remain inert and preserve unknown non-URL fields.
- DB schema changes: stable, additive, forward-only, idempotent SQLite migrations where practical; preserve `schema_migrations` and legacy `user_version` bridge.
- WebUI client changes require matching `/api/app/*` routes, typed requests, unit/embedded-browser verification.
- Generated/built outputs are mostly ignored; never commit populated `internal/webserver/assets` unless intentionally updating generated artifacts.
