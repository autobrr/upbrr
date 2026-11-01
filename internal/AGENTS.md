# Backend Guidelines

Scope: backend Go under `internal/`; root repo rules apply.

## Checks

- Touched packages: `go test -race -v -timeout 20m <packages>`; shared core, config, DB or API risk: add `make test-go` (full race suite).
- Run `make lint` (architecture, path, literal, workflow-contract and correction-choice checks plus golangci-lint); `make logpolicy` for internal/logging changes; `make pathpolicy` for filesystem changes.
- Before committing Go changes, run `make gofix-check-changed`. `make fmt-go` applies 160-column formatting and expands keyed composite literals with at least three elements.
- CLI, API, tracker and browser-specific check sets live in their scoped guidance; shared changes require the affected callers' checks too. See `CONTRIBUTING.md` for hooks and setup.

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

- Runtime activation and WebUI snapshots: read `internal/webserver/AGENTS.md` before changing those contracts, including callers.
- Production metadata service views must retain the required `MetadataEvidenceRepository` from validated `RepositoryCapabilities`; verify default Core construction for both owned CLI and borrowed WebUI repositories when changing these seams.
- Config schema changes require `internal/config.Config`, embedded defaults, import/export, relevant env overrides, settings UI/web parity and secret redaction/encryption review.

## Canonical Release Ownership

- Resolve managed artifact roots with `pathing/layout.ReleaseTempDirFor`; derive feature/generation/attempt subdirectories only beneath that owner. `ReleaseTempPathFor` is the non-creating cleanup lookup. Independent feature roots under application `tmp` are prohibited by `make architecturepolicy`.
- Keep source identity/fingerprints (including verified content) separate from visible directory names. Layout changes must align producers, persisted/hydrated references, consumers, cancellation/private writes, active/shared cleanup protection, and upgrade documentation. Do not discover or migrate abandoned legacy caches automatically.
- Canonical preparation contracts: single-source; preserve caller interaction mode.
- `PreparedRelease`: typed reusable source facts only; no workflow options, tracker choices, questionnaire answers, overrides, outcomes.
- Operations consume owner-local inputs + exact `ReleaseRef`; workflow interfaces accept only the narrowest sufficient contract, not broad `PreparedRelease`.
- `PreparedReleaseDisplay`/`ProviderDisplay` construction belongs to `internal/preparedrelease`; tracker readiness belongs to workflow projection/preflight snapshots.
- Browser correlation IDs/event timestamps are transport concerns; inject under `internal/webserver`, never canonical preparation inputs/facts.

## Go Rules

- Satisfy `.golangci.yml`; do not weaken checks or add broad `nolint` to hide failures. Test writes use `0o600` unless testing mode bits.
- No wholesale `go fix`: use `make gofix-check-changed`, then reviewed-package `go fix -omitzero=false <packages>`. Keep `omitzero` disabled unless JSON semantics are reviewed.
- Synchronize test goroutines with channels; use `testing/synctest` for timer behavior, not real `time.Sleep` as proof that work ran or completed. Preserve real process/filesystem boundaries where integration tests require them.

## Logging / Redaction

- Use context-aware APIs when meaningful.
- Follow root levels via `Infof`, `Warnf`, `Debugf`, `Tracef`. Log exact finalized prepared-generation facts at DEBUG after collection or reuse, not raw parser/intermediate values. Never fetch or derive facts only for diagnostics; hydration/projection must not repeat full summaries. Cache decisions and per-candidate evidence belong at TRACE.
- Before logging, redact auth status, remote response details, URLs, raw errors when secret-bearing; use `internal/redaction.RedactValue` or tracker/common helpers.
- No stdlib print/log under `internal/**`.
- Satisfy `cmd/logpolicy`.
- Redaction source: `internal/redaction/redaction.go`.
- Torrent-client connection diagnostics must omit private endpoints, including credential-free hosts and URL prefixes. Use the client boundary error sanitizer; preserve causal error identity and safe operation context.
- Never log credentials, usernames, passwords, tokens, API keys, auth keys, passkeys, cookies, 2FA codes, challenge IDs, refreshed API tokens, secret payloads.

## Path Portability

- Local FS paths: `filepath`.
- Slash-data—torrent paths, URLs, API payload paths: `path` only with import-local `//nolint:depguard // <reason>`.
- Slash-data to local FS: validate, then `filepath.FromSlash`.
- Reject POSIX/Windows escapes on every OS: leading `/`, leading `\`, drives, UNC, `..`.
- Use `internal/pathing.IsWithinRoot` / `SamePath`; no custom `filepath.Rel` + prefix guards.
- Tests: `t.TempDir`, `filepath.Join`, `filepath.ToSlash`; no hardcoded OS-rooted literals/raw slash assertions for local FS.
- `cmd/pathpolicy` detects wrong APIs, string-built local paths, slash-data FS calls/assertions, custom guards. Rare exception: same/previous-line `//pathpolicy:allow <reason>`.

## Generated / Scratch Path Risk

`.gitignore` does not prevent Go package discovery; `golangci-lint run ./...` and other Go tools can find ignored repo-local `.go` files.

- No scratch `.go` files under repo paths such as `tmp/`.
- Expected generated/scratch `.go` files under ignored dirs require tool exclusion, e.g. `.golangci.yml` `linters.exclusions.paths`.
- After generated dirs or broad Go tooling, run `make lint` pre-commit.
- Exclude generated artifacts from commits unless explicitly updating them.

## Domain Guardrails

Tracker definitions, registration, auth, naming, validation, and duplicate behavior: read `internal/trackers/AGENTS.md` before changing those contracts, including callers outside that subtree. Shared upload authority and persistence rules below still apply.

- Upload preparation returns one immutable `trackers.PreparedOperation`; preview/submission use identical captured canonical state. Submission may defer short-lived remote tokens, but never rebuild payloads, reread mutable prepared inputs, or rerun image uploads. Dry-run/upload-review never receive a submittable plan.
- Downstream media preparation, dry-run, upload, artifact retention, client injection use exact tracker authority selected by workflow mode: durable post-dupe approval for gated workflows or existing WebUI stage controls. Never widen to unapproved/disabled trackers or restore obsolete final upload approval.
- Successful submission returns registration authority through `api.UploadSummary.UploadedTorrents`. Preserve direct tracker page in `TorrentURL`; retain exact tracker-returned torrent in `TorrentPath` when available. Reconstruct only when protocol makes it deterministic after confirmed success. Client injection uses registered authority, never pre-upload prepared torrent. Local artifact failure never reclassifies successful remote upload.
- DB schema changes: stable, additive, forward-only, idempotent SQLite migrations where practical; preserve `schema_migrations` and legacy `user_version` bridge.
- WebUI client changes require matching `/api/app/*` routes, typed requests, unit/embedded-browser verification.
