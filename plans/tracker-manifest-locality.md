# Tracker Definition Locality and Catalog Deepening Plan

Status: completed

Date: 2026-07-16

## Completion Receipt

Implemented all six phases and acceptance criteria:

- one family-grouped built-in implementation catalog and first-class family queries
- tracker-owned endpoints, audio/image-host/auth/torrent-identity/data policies, with generic name dispatch removed
- immutable profile endpoints plus saved/imported URL migration; custom tracker and `MANUAL` removal
- ordered `example.yaml`-derived config catalog, configured-state evaluation, and backend catalog transport
- catalog-driven tracker settings entries, selector/reset behavior, unsupported-entry handling, and app-wide tracker availability
- onboarding/docs/ADR updates plus locality, parity, migration, API-contract, frontend, and embedded E2E guardrails

Final validation passed on 2026-07-16:

- focused tracker/config/metadata/image-host/torrent-client/WebUI/API race suites
- `make fmt-go`, `make gofix-check-changed`, `make lint`, `make logpolicy`, `make test-go`
- `make test-frontend`, frontend production build, and `git diff --check`
- `make e2e-web`: 5 embedded WebUI tests passed, including catalog-driven tracker settings smoke

## Goal

Make the tracker registry a deep module: callers use one small, typed interface while tracker identity, family, endpoint, rules, and policies remain local to tracker implementations. Adding a standard Unit3D tracker must not require tracker-name edits in generic metadata, auth, image-hosting, torrent-client, WebUI, or compatibility catalogs.

Target manual scope for a standard Unit3D tracker with an adult rule and Aither-style language handling:

1. `internal/trackers/impl/unit3d/sites/<tracker>/profile.go`
2. `internal/trackers/impl/unit3d/sites/<tracker>/rules.go`
3. One entry in the Unit3D group inside `internal/trackers/impl/registry.go`
4. One config-surface stanza in `internal/config/defaults/example.yaml`, with no tracker `url`
5. Behavioral cases in the combined `internal/trackers/rules_test.go`

Optional site-owned files remain local: `banned_groups.go`, custom payload/name/description/auth files, and focused tests for custom implementation behavior.

No new-tracker edit should be needed in:

- `internal/metadata/media_details.go`
- `internal/trackers/unit3dmeta`
- `internal/trackers/catalog.go` compatibility data
- `internal/trackers/auth` tracker-name tables
- `internal/imagehosting/policy` tracker-name tables
- `internal/torrentclient` tracker-name tables
- `webui/src/hooks/useSettingsState.tsx`
- duplicated registry/profile parity lists

## Approved Decisions

### One central implementation catalog

Keep one file that explicitly composes every supported tracker: `internal/trackers/impl/registry.go`. Do not create separate per-family catalog files.

Within that file, segregate definitions by family:

```go
func unit3DDefinitions() []trackers.Definition
func azFamilyDefinitions() []trackers.Definition
func standaloneDefinitions() []trackers.Definition
func builtInDefinitions() []trackers.Definition
```

Exact types may be adjusted to carry registration metadata cleanly. The invariant is one explicit import/composition list, grouped by family, consumed by registry construction and contract tests. Avoid package `init` registration and global mutation.

### Family is first-class metadata

Replace the coarse `unit3d` versus `non-unit3d` classification with queryable family metadata:

- `unit3d`
- `azfamily`
- `standalone`

Expose deterministic family queries such as `NamesByFamily`. Family is valid for composition, protocol adapter selection, and shared family tests. Generic policy decisions must use typed capabilities rather than family/name switches.

### Source ownership

- Site `profile.go` owns the default endpoint and site policies.
- Site `rules.go` owns release rules and related typed policy constructors.
- Central `registry.go` owns supported-tracker composition, family grouping, and relational data such as curated tracker priority.
- `internal/config/defaults/example.yaml` owns the supported tracker config surface, field order, and config defaults/baseline.
- Frontend owns generic field presentation only: labels, controls, sensitivity, advanced flags, and dynamic option rendering.

`example.yaml` remains embedded global config data. Its empty tracker entries intentionally distinguish supported trackers from user-configured trackers and continue to support config schema filtering/backfill.

### Endpoint invariant

Tracker profile/definition endpoints are the only runtime tracker endpoints.

- Remove every tracker `url` key from `example.yaml`, not merely its value.
- Remove `TrackerConfig.URL`.
- Remove runtime endpoint override precedence.
- Strip legacy `URL` fields case-insensitively from saved and imported tracker entries, including unsupported entries.
- Report removed paths as deprecated/ignored migration changes.
- Never preserve tracker `URL` inside `TrackerConfig.Unknown`.

`announce_url` and `my_announce_url` are distinct tracker credentials/config values and remain where required.

### Supported, configured, and ready

Use the domain terms in `CONTEXT.md`:

- **Supported tracker**: present in the built-in implementation catalog and `example.yaml`.
- **Configured tracker**: at least one tracker-specific primary authentication/connection field differs from its empty default.
- **Ready tracker**: configured and complete/valid for use.
- **Unsupported tracker entry**: preserved inert config for a tracker that is not supported.

Configured state is deliberately weaker than readiness. For example, a non-empty username with an empty required password is configured but not ready. Optional presentation/behavior settings such as favicon, anonymity, image host, torrent client, or layout do not configure a tracker.

### No custom trackers

Remove configured custom Unit3D registration and inference. Only built-in catalog definitions are runnable.

Unknown saved tracker entries remain losslessly round-trippable and visible in a separate unsupported-entry warning surface, except that legacy tracker `URL` is always removed. Unsupported entries are never registered, authenticated, selected, or uploaded. Explicit deletion remains available.

### Remove `MANUAL`

Remove `MANUAL` tracker handling altogether:

- example config stanza
- compatibility catalog entry
- modified-release exemption and test
- frontend schema
- `TrackerConfig.Filebrowser` and secret handling

This does not remove unrelated manual screenshot, menu, metadata, or CLI interaction features.

### Tracker settings interaction

On the settings page:

- `Entries` shows configured trackers and newly selected unsaved drafts.
- The additional-entry selector shows every supported tracker not currently in `Entries`.
- Empty supported placeholders remain hidden.
- Removing an entry resets it to `example.yaml` defaults, removes it from `DefaultTrackers`, and returns it to the selector.
- Unsupported saved entries appear separately with a warning and delete action.
- Field order follows `example.yaml` order.

## Investigation Findings

### Duplicated identity and composition

- The branch has 37 Unit3D site profiles.
- The same Unit3D identity/base-URL data is duplicated in `internal/trackers/unit3dmeta/metadata.go`.
- Unit3D imports/profile lists are duplicated in `internal/trackers/impl/registry.go` and `internal/trackers/impl/unit3d_profiles_test.go`.
- `internal/trackers/catalog.go` duplicates known names, coarse kinds, priority construction, and special cases.

Tests currently protect agreement between duplicate lists instead of observable registry behavior.

### Embedded config is the intended config catalog

`example.yaml` currently has 67 tracker-shaped entries, including `MANUAL`, and 65 tracker `url` fields. It is used for:

- human example configuration
- runtime default/backfill placeholders
- tracker-specific allowed-field filtering
- frontend default comparison

Keep those roles. After removing `MANUAL`, built-in registry names and example tracker names must match exactly. Remove endpoint data only; do not split example data from runtime defaults or stop placeholder backfill.

Current schema extraction uses unordered maps. The frontend requirement makes ordered YAML mapping extraction necessary.

### Language policy is split

Aither demonstrates two distinct concepts:

- `RuleSet.Language`: release upload eligibility
- `AudioPolicy`: whether extra audio languages are allowed, warned, or blocked as audio bloat

`internal/metadata/media_details.go` currently hardcodes tracker-name collections for AITHER, SPD, MTV, ASC, BJS, BT, DC, FF, and TL despite registry `AudioPolicy` support already existing for ANT/BHD.

Keep the concepts typed and separate. For Unit3D sites, both constructors may live in the site `rules.go`; `profile.go` composes them. Generic metadata evaluates registry policy without tracker names.

### Frontend settings duplicate the tracker schema

`webui/src/hooks/useSettingsState.tsx` has a large tracker-name-to-fields map and a broad fallback. RHD is registered but absent from that map, proving real drift.

The same hook also treats optional fields such as image-host settings as activation fields. That conflicts with the approved configured-state definition.

### Other tracker-name dispatch remains outside implementations

The full audit found additional locality leaks:

| Current surface | Tracker-specific data | Target owner |
| --- | --- | --- |
| `internal/trackers/auth` | built-in auth specs, adapter switches, nil-registry fallbacks | definition/profile auth capabilities and resolvers; Unit3D family defaults |
| `internal/imagehosting/policy` | accepted hosts, conditional hosts, owned-host tracker map | tracker `ImageHostPolicy`; generic uploader/host registry stays in image hosting |
| `internal/torrentclient/search.go` | URL patterns, comment aliases, detail-ID regexes, MTV search preference | typed tracker identity/search policy in definitions/profiles |
| `internal/trackers/catalog.go` | known names, family fallback, priority, metadata locale special cases, `MANUAL` | composed registry; central composition priority; profile capabilities |
| `internal/metadata/tracker_data.go` and `internal/trackers/data` | nil-registry Unit3D inference and compatibility priority | mandatory registry/family queries |
| tracker implementations | `TrackerConfig.URL` fallback/override logic | local profile/definition endpoint only |

Curated priority is relational rather than site-owned, so it stays in the single central composition file. Site endpoint aliases, identity patterns, and policy are site-owned.

### URL overrides are also a test seam today

Many tests set `TrackerConfig.URL` to an `httptest` server. Removing production endpoint overrides must not make tracker modules untestable.

Use internal seams instead:

- injected `http.Client`/transport
- package-private constructor options
- test profiles/definitions with a test endpoint

Production constructors always use the declared profile endpoint. Do not expose a replacement runtime config override merely for tests.

## Target Module Design

### Registry seam

The registry is the deep module. Callers learn one interface and receive:

- normalized supported names
- family membership
- default endpoint
- release rules
- auth capability/resolver
- audio policy
- image-host policy
- banned/claim/dupe/artifact/description capabilities
- torrent identity/search policy

Keep consumer methods narrow (`LookupAudioPolicy`, `LookupImageHostPolicy`, `LookupTorrentIdentityPolicy`, etc.). Do not force generic callers to inspect a mega-manifest or switch on names/families.

Registry construction validates and defensively copies all declarative values:

- unique normalized name
- valid family
- non-empty canonical HTTPS endpoint
- capability tracker IDs matching the definition name
- valid/compiled identity patterns
- valid policy combinations

### Unit3D profile

Extend `unit3d.Profile` only for genuinely site-owned declarative behavior, including `AudioPolicy` and torrent identity aliases/overrides where required. Shared Unit3D defaults provide:

- family `unit3d`
- common API-key auth capability
- common detail-ID extraction
- common upload/data behavior

A standard site profile should declare only name, base URL, rules/policies, and deviations from family defaults.

Example composition:

```go
func Profile() unit3d.Profile {
    return unit3d.Profile{
        Name:        "EXAMPLE",
        BaseURL:     "https://example.invalid",
        Rules:       Rules(),
        AudioPolicy: AudioPolicy(),
    }
}
```

### Config schema catalog

Deepen config's existing example-derived schema into an immutable ordered catalog. It should expose, per supported tracker:

- normalized name
- ordered JSON/YAML field keys
- default values
- activation-field keys

Activation fields are the primary auth/connection fields present in that tracker's schema, such as API-key variants, API-user, username/password, passkey, and announce-URL variants. Every supported tracker schema must contain at least one activation field, and every activation default must be empty.

Config remains independent of tracker implementations. Cross-source parity belongs in an integration/implementation contract test to avoid an import cycle.

### WebUI tracker catalog

Replace `ListKnownTrackers` with `ListTrackerCatalog`. The backend composes registry identity with the config schema catalog and current redacted config state.

Conceptual response entry:

```text
name
family
baseURL
fields[]          # ordered, with default value and activation marker
configured
```

Do not include effective secret values. Current values continue through the existing redacted config transport. Auth readiness remains in the tracker-auth status workflow rather than being conflated with local configured state.

Move generic field presentation metadata out of `useSettingsState.tsx` into a focused frontend tracker-field module. Remove all tracker-name dispatch from React. Dynamic image-host/torrent-client options remain generic renderer behavior.

## Implementation Phases

### Phase 1: Establish one catalog and family model

1. Add `trackers.Family` with `unit3d`, `azfamily`, and `standalone` values.
2. Replace `KindProvider`/`NamesByKind` usages with family equivalents; use a temporary compatibility alias only within this phase.
3. Refactor `internal/trackers/impl/registry.go` into `unit3DDefinitions`, `azFamilyDefinitions`, `standaloneDefinitions`, and `builtInDefinitions` while retaining the single file/import list.
4. Keep curated priority in this file and apply it once during registry construction.
5. Refactor registry/profile tests to consume the central composition and assert unique names, valid families/endpoints, deterministic ordering, and complete registration.
6. Remove the second Unit3D import/profile list from `unit3d_profiles_test.go`.

Exit condition: every built-in tracker is defined once for composition and can be queried by family.

### Phase 2: Move site policy into definitions/profiles

1. Add `AudioPolicy` to Unit3D profile/definition capability projection.
2. Move metadata audio maps into site/definition policy:
   - AITHER: English allowed extra language
   - SPD: Romanian allowed extra language
   - MTV: hard block for English-original foreign audio
   - ASC/BJS/BT/DC/FF/TL: allow audio bloat
   - preserve ANT/BHD behavior through the same evaluator
3. Keep release language/adult rules in site `rules.go`; allow `AudioPolicy()` beside `Rules()` when related.
4. Rewrite metadata audio evaluation as tracker-neutral typed policy evaluation.
5. Move tracker accepted/conditional image hosts into `ImageHostPolicy` providers. Keep uploader availability, host normalization, and HTTP upload implementations in `internal/imagehosting`.
6. Move remaining metadata locale/name special cases to existing profile capabilities.
7. Preserve combined RuleSet coverage in `internal/trackers/rules_test.go`; keep site-local tests for implementation-only behavior.

Exit condition: generic metadata and image-host policy code contain no supported tracker-name policy maps.

### Phase 3: Move identity/auth behavior and require the registry

1. Add typed torrent identity/search capability support for:
   - announce/detail URL match patterns
   - client comment aliases
   - torrent/detail ID extraction
   - tracker-specific search preference/stop behavior
2. Derive common Unit3D patterns from profile base URLs and family defaults; keep site aliases such as RF beside that site profile.
3. Move non-Unit3D patterns and special search behavior to their definitions.
4. Finish tracker-owned auth capability/resolver coverage, including a shared Unit3D auth default.
5. Delete auth `builtInSpecs`, legacy adapter switches, and registry-null fallbacks once all production definitions expose required capabilities.
6. Make the composed registry mandatory in production metadata, tracker-data, description, torrent-client, auth, and upload composition roots.
7. Replace nil-registry tests with small explicit registries.
8. Delete `internal/trackers/unit3dmeta` and compatibility name/kind/priority fallbacks from `internal/trackers/catalog.go`; retain generic family types in an appropriately named file.

Exit condition: supported identity and capabilities come only from the composed registry; no generic package can infer support from a static name table.

### Phase 4: Make profile endpoints absolute and migrate config

1. Remove all tracker `url` keys from `example.yaml`.
2. Remove `TrackerConfig.URL` and URL-specific secret treatment.
3. Add raw-config migration before unknown-field preservation/unmarshal:
   - identify direct `Trackers.Trackers.<name>.URL` ASCII-case variants
   - remove them for supported and unsupported entries
   - report deprecated/ignored paths and mark `Trackers` changed
   - apply to DB load/repair, native import, legacy import, export normalization, and WebUI save paths
4. Change every upload, dry-run, dupe, data, auth, claims, and banned-group endpoint resolver to use its definition/profile endpoint.
5. Replace tests that injected `TrackerConfig.URL` with internal HTTP/test endpoint adapters.
6. Remove `NewRegistryWithConfig` custom Unit3D registration and all auth-shape/custom Unit3D inference in `impl/unit3d`, `trackers/data`, and callers.
7. Remove `MANUAL`, `Filebrowser`, its rule exemption, and related frontend/config tests.
8. Preserve unknown tracker entry payloads inertly, except for mandatory URL removal. Do not expose them through registry/catalog selection.
9. Add exact catalog↔`example.yaml` normalized-name parity tests and assert:
   - no `MANUAL`
   - no tracker `url`
   - every profile endpoint is valid HTTPS
   - every example tracker has an empty-default activation field

Exit condition: profile/definition endpoint is the only runtime endpoint, and only catalog trackers can run.

### Phase 5: Serve the config catalog and simplify tracker settings

1. Parse `example.yaml` through `yaml.Node` or an equivalent order-preserving path and expose immutable tracker schemas/defaults/activation fields.
2. Add shared configured-state evaluation in Go using schema activation fields; keep readiness in auth validation.
3. Replace `ListKnownTrackers` with `ListTrackerCatalog` in Go routes, shared API types, TypeScript types, and frontend client code.
4. Compose each response from registry name/family/base URL plus config schema and configured state.
5. Refactor settings UI:
   - remove `trackerSchemas`, broad fallback, and tracker-name activation logic
   - render fields in backend-provided YAML order
   - use backend activation markers for unsaved draft state
   - use catalog base URL for tracker display/favicon fallback
   - keep generic field metadata in a focused frontend module
   - keep dynamic image-host and torrent-client options
6. Implement the approved `Entries`/additional-selector/reset behavior.
7. Render preserved unknown tracker configs in a separate unsupported-entry warning section with deletion.
8. Fail with a stable settings error if backend schema advertises a field unknown to generic frontend presentation metadata; do not silently use a broad tracker schema.
9. Add frontend regressions for RHD, a synthetic standard Unit3D catalog entry, field order, configured-versus-ready/incomplete credentials, entry reset, encrypted value preservation, and unsupported entries.

Exit condition: adding a tracker or changing its config field selection requires no frontend tracker-name edit.

### Phase 6: Documentation and guardrails

1. Update `internal/trackers/impl/unit3d/doc.go` with the final onboarding flow.
2. Update `internal/AGENTS.md` to remove compatibility guidance and state ownership rules.
3. Update `CONTRIBUTING.md` only if its tracker-location guidance is no longer accurate.
4. Record the hard-to-reverse source-ownership decision in an ADR:
   - central family-grouped composition
   - profile-owned endpoints/policies
   - example-owned config surfaces/order/defaults
   - registry/config composition for frontend
5. Add contract tests/targeted checks that reject reintroduction of:
   - config tracker `URL`
   - custom tracker registration
   - duplicate supported tracker lists
   - tracker-name maps in the audited generic policy files
6. Prefer behavior/interface tests over source-text checks where feasible. Keep any source check narrowly scoped so legitimate tracker IDs in fixtures and the central catalog remain allowed.

Exit condition: documentation and automated contracts describe the same onboarding path and ownership model.

## Validation Plan

Run focused checks for each phase, then the full cross-surface set after compatibility removal:

```powershell
go test -race -v -timeout 20m ./internal/trackers/... ./internal/metadata ./internal/config ./internal/torrentclient ./internal/imagehosting/...
go test -race -v -timeout 20m ./internal/webserver/... ./pkg/api
pnpm --dir webui run lint
pnpm --dir webui run lint:dead
pnpm --dir webui run typecheck
pnpm --dir webui run test:unit
pnpm --dir webui run format:check
pnpm --dir webui run build
make fmt-go
make gofix-check-changed
make lint
make logpolicy
make test-go
git diff --check
```

Migration-specific tests must cover:

- saved supported tracker URL removal
- saved unsupported tracker URL removal while retaining other unknown fields
- mixed-case legacy `URL` removal
- native/legacy import behavior and migration reporting
- no URL reappearance through `Unknown`, export, or save
- old custom Unit3D entries remaining inert/unsupported
- `MANUAL` and `Filebrowser` removal
- exact registry/example parity

Perform one embedded WebUI settings smoke test on `http://localhost:7480`:

1. Confirm `Entries` initially contains only configured trackers.
2. Add an unconfigured supported tracker from the selector.
3. Enter one activation credential and confirm configured state.
4. Save/reload and confirm field order/value masking.
5. Remove the entry and confirm reset plus return to selector.
6. Confirm unsupported saved entries are warned and cannot be selected/run.

## Acceptance Criteria

- `internal/trackers/impl/registry.go` is the only complete supported implementation list and groups definitions by family.
- Registry family metadata supports Unit3D, AZ-family, and standalone queries.
- `example.yaml` contains exactly one config surface for every supported tracker, preserves field order/defaults, contains no `MANUAL`, and contains no tracker `url`.
- A standard Unit3D tracker addition is limited to its site files, one central Unit3D catalog entry, one example config stanza, and combined behavior tests.
- Unit3D base URLs exist only in site profiles; non-Unit3D endpoints exist only in their implementation definitions.
- Saved/imported tracker URLs are removed with explicit migration reporting and cannot survive as unknown data.
- Custom Unit3D trackers are not inferred or registered.
- Unknown tracker configs remain inert and recoverable, except for removed legacy URLs.
- Configured state depends only on non-empty activation credentials; readiness remains separate validation/auth state.
- Tracker settings `Entries`, selector, reset, field order, and unsupported-entry behavior match the approved interaction.
- Generic metadata/auth/image-host/torrent-client/frontend modules do not dispatch on supported tracker names.
- Combined `internal/trackers/rules_test.go` remains the cross-tracker RuleSet behavior surface.
- Production endpoint immutability does not reduce testability; tracker tests use internal injected adapters/endpoints.

## Explicit Non-Goals

- Dynamic runtime plugin loading.
- Custom user-defined tracker implementations.
- Runtime tracker endpoint overrides.
- Package `init` self-registration.
- Separate per-family catalog files.
- Moving generic Unit3D protocol implementation into every site folder.
- Moving generic frontend labels/control rendering into Go profiles.
- Splitting combined RuleSet behavior tests into one package per tracker.

## Recommended Delivery Slices

Use separate reviewable commits/PRs unless atomic delivery is explicitly required:

1. Central catalog grouping + first-class family model.
2. Audio/image-host/metadata policy locality.
3. Torrent identity/auth capabilities + mandatory registry + compatibility deletion.
4. Endpoint single-source migration + custom/MANUAL removal.
5. Ordered config catalog + backend transport + WebUI settings migration.
6. Final docs, ADR, guardrails, and full validation.

Each slice must preserve behavior except for the explicitly approved removals: custom trackers, `MANUAL`, and tracker URL overrides.
