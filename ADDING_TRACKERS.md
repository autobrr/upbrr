# Adding tracker support

This guide covers two supported extension paths:

- a Unit3D tracker under `internal/trackers/impl/unit3d/sites/<tracker>`
- a standalone, non-Unit3D tracker under `internal/trackers/impl/standalone/<tracker>`

AvistaZ-family (`azfamily`) trackers are intentionally out of scope.

## Ownership rules

- `internal/trackers/impl/registry.go` is the only complete supported-tracker manifest.
- A Unit3D or standalone profile owns the tracker's default endpoint and typed
  policies.
- Every tracker declares one semantic shared upload-content mode. Unit3D inherits
  `description` from its family definition; standalone profiles choose explicitly.
- `internal/config/defaults/example.yaml` owns the ordered config/settings surface. It must not
  contain tracker `url` fields.
- A tracker is configured when at least one activation credential in its config stanza is
  non-empty. Authentication readiness is a separate concern.
- Tracker-specific behavior stays in the tracker package. Do not add tracker-name dispatch to
  generic metadata, auth, image-hosting, torrent-client, config, or frontend code.
- The backend tracker catalog drives the frontend. Adding a tracker that uses existing config
  fields normally requires no tracker-specific frontend edit.
- Every registered tracker must expose a `dupe.Factory`, even when duplicate search can only
  return a typed not-run result.
- Upload and dry-run must share payload preparation. Dry-run must not submit the upload or repeat
  workflow-level discovery and duplicate checking.

## Fast file checklist

A standard Unit3D addition normally changes:

1. `internal/trackers/impl/unit3d/sites/<tracker>/profile.go`
2. `internal/trackers/impl/unit3d/sites/<tracker>/rules.go`, when site rules exist
3. `internal/trackers/impl/registry.go`
4. `internal/config/defaults/example.yaml`
5. `internal/trackers/rules_test.go`, when rules exist
6. a site-local `profile_test.go`, when mappings or callbacks differ from Unit3D defaults

Static banned groups, custom naming/descriptions, image-host rules, and other policies add
site-local files only.

A standalone addition normally changes:

1. a new `internal/trackers/impl/standalone/<tracker>` package containing `profile.go`, upload,
   duplicate-search, and focused test code
2. `internal/trackers/impl/registry.go`
3. `internal/config/defaults/example.yaml`
4. `internal/trackers/rules_test.go`, when rules exist
5. shared config/frontend field contracts only when no existing `config.TrackerConfig` field fits

## Choose the shared upload-content mode

`trackers.UploadContentMode` tells generic preparation which tracker-scoped content object the
adapter consumes. Choose by protocol behavior, not tracker identity:

| Mode | Use when | Adapter input | Failure scope |
| --- | --- | --- | --- |
| `none` | The adapter builds its payload without shared descriptions or selected images | `PreparationInput.Assets` is `nil` | Shared content cannot block the tracker |
| `screenshots` | The adapter consumes selected screenshots/menu images but no shared description | Ready screenshot assets, which may be empty | A failed screenshot object blocks only that tracker |
| `description` | The adapter consumes the full shared description plus its image assets | Ready aggregate description assets, which may contain empty text | A failed description object or required image substep blocks only that tracker |

Ready-empty content is valid and differs from failed content. Generic coordinators never infer a
failure from empty text or zero selected images.

All Unit3D and AvistaZ-family definitions currently declare `description` once at family level.
Standalone profiles must set `UploadContentMode` explicitly. Current protocol examples are BTN/NBL
for `none`, ANT/RTF for `screenshots`, and most other standalone trackers for `description`.
Changing a standalone tracker's workflow later should require only its profile and tracker-local
adapter implementation; do not add tracker-name branches to core or tracker orchestration.

## Add a Unit3D tracker

### 1. Confirm that shared Unit3D behavior fits

The shared implementation already supplies:

- bearer API-key auth
- `/api/torrents/upload` uploads
- `/api/torrents/filter` duplicate searches
- standard Unit3D descriptions and multipart payloads
- generic Unit3D metadata lookup
- standard category, type, and resolution IDs
- standard torrent identity matching from the profile base URL
- required MediaInfo encode-setting validation

If the site changes only rules, IDs, naming, description formatting, payload fields, or typed
policies, keep it as a Unit3D profile. If it replaces the protocol substantially, implement it as
a standalone tracker instead of filling shared Unit3D code with site-name branches.

### 2. Create the site package

Create:

```text
internal/trackers/impl/unit3d/sites/example/
  profile.go
  rules.go                 # when the site has release rules
  banned_groups.go         # when the site has a static list
  profile_test.go          # when profile behavior differs from defaults
```

Additional site-local files such as `name.go`, `description.go`, or `audio_policy.go` are useful
when `profile.go` would otherwise become a collection of unrelated behavior.

A minimal profile is:

```go
package example

import "github.com/autobrr/upbrr/internal/trackers/impl/unit3d"

// Profile returns EXAMPLE's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "EXAMPLE",
		BaseURL: "https://tracker.example.invalid",
		Rules:   Rules(),
	}
}
```

`Name` is the stable uppercase identifier. `BaseURL` is authoritative and must be a valid HTTPS
origin. Do not add an endpoint override to config.

### 3. Handle category, type, and resolution IDs

Shared Unit3D upload mapping is:

| Dimension  | Normalized value     | Default ID |
| ---------- | -------------------- | ---------- |
| Category   | `MOVIE`              | `1`        |
| Category   | `TV`                 | `2`        |
| Type       | `DISC`               | `1`        |
| Type       | `REMUX`              | `2`        |
| Type       | `ENCODE` or `DVDRIP` | `3`        |
| Type       | `WEBDL`              | `4`        |
| Type       | `WEBRIP`             | `5`        |
| Type       | `HDTV`               | `6`        |
| Resolution | `4320p`              | `1`        |
| Resolution | `2160p`              | `2`        |
| Resolution | `1080p` or `1440p`   | `3`        |
| Resolution | `1080i`              | `4`        |
| Resolution | `720p`               | `5`        |
| Resolution | `576p` / `576i`      | `6` / `7`  |
| Resolution | `480p` / `480i`      | `8` / `9`  |
| Resolution | unknown or `8640p`   | `10`       |

Use site-local `SiteProfile` callbacks when the site differs:

```go
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "EXAMPLE",
		BaseURL: "https://tracker.example.invalid",
		Rules:   Rules(),
		Site: unit3d.SiteProfile{
			ResolveCategoryID:   categoryID,
			ResolveTypeID:       typeID,
			ResolveResolutionID: resolutionID,
		},
	}
}

func categoryID(meta api.UploadSubject) string {
	switch {
	case strings.EqualFold(unit3d.Category(meta), "TV") && meta.TVPack:
		return "9"
	case strings.EqualFold(unit3d.Category(meta), "TV"):
		return "2"
	default:
		return unit3d.DefaultCategoryID(meta)
	}
}

func typeID(meta api.UploadSubject) string {
	return map[string]string{
		"DISC":   "10",
		"REMUX":  "20",
		"ENCODE": "30",
		"WEBDL":  "40",
	}[unit3d.InferType(meta)]
}

func resolutionID(meta api.UploadSubject) string {
	return map[string]string{
		"2160p": "1",
		"1080p": "2",
		"720p":  "3",
	}[unit3d.Resolution(meta)]
}
```

Available upload callbacks are:

| Callback                 | Use                                                                    |
| ------------------------ | ---------------------------------------------------------------------- |
| `BuildName`              | Replace shared release-name formatting                                 |
| `BuildDescription`       | Replace shared Unit3D description rendering                            |
| `ResolveKeywords`        | Filter or remap the `keywords` field                                   |
| `ResolveTypeID`          | Map prepared release facts to a site type ID                           |
| `ResolveResolutionID`    | Map prepared release facts to a site resolution ID                     |
| `ResolveCategoryID`      | Map canonical category and site facts to a site category ID            |
| `ApplyAdditionalPayload` | Add site-only upload fields after the common payload is built          |
| `FinalizeDescription`    | Transform the completed shared description without replacing its build |

An empty or `"0"` custom type ID is treated as unsupported and blocks payload preparation. Test
every accepted type. Category and resolution callbacks should also return deliberate values for
all supported inputs; do not let accidental empty IDs reach the API.

#### Duplicate-search mappings can differ independently

`SiteProfile.ResolveCategoryID`, `ResolveTypeID`, and `ResolveResolutionID` currently affect upload
and dry-run payloads only. Unit3D duplicate search builds a separate filter query.

If the new tracker uses nonstandard duplicate-search IDs, omits filters, changes the filter path,
or searches pending torrents, treat that as a Unit3D family-contract extension:

1. Add a typed duplicate-search callback/policy to the Unit3D profile contract.
2. Pass the composed profile into the Unit3D duplicate adapter.
3. Apply the callback in `dupe_params.go` or the Unit3D data client without adding a new
   tracker-name conditional.
4. Define the mapping in the new site's `profile.go` or a site-local `dupe.go`.
5. Test the exact query parameters and response normalization.

Do not assume that custom upload IDs are also correct for duplicate search. Do not copy existing
legacy site-name exceptions as the pattern for new trackers.

### 4. Add tracker rules

Declare standard rules through `ruletypes.RuleSet`. Unit3D automatically enables
`RequireValidMISetting`, so the site does not need to repeat it.

An adult rule plus Aither-style language rule looks like:

```go
package example

import "github.com/autobrr/upbrr/internal/trackers/ruletypes"

// Rules returns EXAMPLE's release eligibility requirements.
func Rules() *ruletypes.RuleSet {
	return &ruletypes.RuleSet{
		BlockAdult:  true,
		AdultMessage: "Adult content is not allowed at EXAMPLE.",
		Language: &ruletypes.LanguageRule{
			Languages:      []string{"english", "en", "eng"},
			RequireAudio:   true,
			RequireSubs:    true,
			AllowOriginal:  true,
			ApplyIfNonDisc: true,
		},
	}
}
```

Other reusable `RuleSet` fields cover:

- category restrictions: movie-only, TV-only, or movie-unless-TV-pack
- disc-only, minimum resolution, HEVC, DVD rip, external subtitle, hardcoded subtitle, and scene
  NFO constraints
- blocked groups and group/type exceptions
- single-file folders
- structured language requirements

Use `ExtraCheck` for one additional pass/fail decision and `FailureCheck` when one evaluation can
produce multiple named failures. Site-local checks should consume `api.RuleSubject` and Unit3D
helpers such as `RuleType`, `RuleResolution`, `RuleGroup`, `RuleGenres`, `RuleKeywords`, `Anime`,
`Animation`, and `AdultContent`. Provider metadata must belong to the exact prepared source; do not
introduce fallbacks to stale parser or provider state.

Add behavior cases to the combined `internal/trackers/rules_test.go`. Use
`EvaluateRulesWithRegistry` so tests include Unit3D-wide requirements. Add focused site tests only
for site-local mapping or complex `ExtraCheck` behavior.

### 5. Add banned groups

For a static list, keep the data beside the site:

```go
package example

// BannedGroups returns EXAMPLE's static release-group blacklist.
func BannedGroups() []string {
	return []string{"GROUP-A", "GROUP-B"}
}
```

Attach it in `Profile()`:

```go
BannedGroups: BannedGroups(),
```

For a dynamic Unit3D blacklist, declare the endpoint in the profile:

```go
BannedPolicy: &trackers.BannedGroupPolicy{
	EndpointPath:  "/api/blacklists/releasegroups",
	RequireAPIKey: true,
},
```

Use `DefaultEndpoint` only for a tracker-owned absolute endpoint that cannot derive from
`BaseURL`. Use `TRaSHGuideURL` for an external TRaSH-compatible source. Set `RawAPIKeyFallback` only
when the remote API requires that known alternate authorization form. Parsing or auth behavior not
represented by `BannedGroupPolicy` needs a typed policy extension, not a tracker-name branch in
`internal/trackers/banned.go`.

### 6. Add other site-owned policies only when required

`unit3d.Profile` can also declare:

| Profile field      | Purpose                                                      |
| ------------------ | ------------------------------------------------------------ |
| `AudioPolicy`      | Multi-language/bloat policy beyond release eligibility rules |
| `DupePolicy`       | Candidate-comparison semantics after duplicate search        |
| `UploadArtifact`   | Torrent source/announce personalization                      |
| `ImageHost`        | Accepted, private, or conditional image hosts                |
| `TorrentIdentity`  | Extra announce/comment aliases and torrent reuse preferences |
| `ClaimPolicy`      | Generic claim orchestration requirements                     |
| `DescriptionGroup` | Site-specific saved description override group               |

Keep construction in `profile.go`; move substantial policy logic or data into a clearly named
site-local file. If a capability does not exist, extend the typed profile/definition contract once
and keep the site decision in the site package.

### 7. Register the profile

In `internal/trackers/impl/registry.go`:

1. Import the site package.
2. Add `example.Profile()` to `unit3DDefinitions()`.

Do not add package `init()` registration. Add the tracker to `SetPriorityOrder` only when it needs
an intentional curated priority; otherwise remaining Unit3D trackers are appended automatically.

### 8. Add the config/settings stanza

Add the tracker in the intended display order in `internal/config/defaults/example.yaml`:

```yaml
EXAMPLE:
  link_dir_name: ""
  api_key: ""
  anon: false
  modq: false
```

Rules:

- Include only fields the tracker consumes.
- Never add `url`.
- Standard Unit3D activation uses an empty `api_key` default.
- Activation credentials must default to an empty string.
- Recognized activation keys are `api_key`, `ApiUser`, `ApiKey`, `username`, `password`,
  `passkey`, `announce_url`, and `my_announce_url`.
- `link_dir_name`, `favicon_url`, `image_host`, and `torrent_client` are not activation
  credentials.
- Stanza field order becomes frontend field order.

When every field already exists in `config.TrackerConfig`, no tracker-specific frontend table is
needed. If the tracker requires a genuinely new config field, also update:

- `config.TrackerConfig` and its YAML/JSON tags in `internal/config/config.go`
- activation-key handling in `internal/config/tracker_catalog.go`, if the field configures the
  tracker
- secret encryption/decryption coverage in `internal/config/secrets.go`, if sensitive
- generic frontend presentation metadata in `webui/src/settings/trackerFields.ts`
- config persistence, catalog, and frontend settings tests

Prefer an existing semantically correct field over adding an alias for the same credential.

### 9. Unit3D tests

At minimum, cover:

- registry/config parity
- profile endpoint and family
- rule allow/deny cases in `internal/trackers/rules_test.go`
- every nonstandard category/type/resolution mapping
- additional payload fields and description/name transforms
- static/dynamic banned-group behavior when present
- duplicate-search query mapping when it differs from standard Unit3D
- dry-run/live payload parity for site-specific fields

Existing contract tests also enforce endpoint locality, absence of generic tracker dispatch, and a
duplicate factory for every registered tracker.

## Add a standalone non-Unit3D tracker

### 1. Create a tracker-owned package

Use a package such as:

```text
internal/trackers/impl/standalone/example/
  profile.go
  upload.go
  dupe.go
  rules.go                 # optional
  banned_groups.go         # optional
  auth.go                  # optional
  data.go                  # optional dynamic lookup
  claims.go                # optional dynamic claim checks
  profile_test.go
  upload_test.go
  dupe_test.go
```

Keep all endpoint, payload, auth, parser, rule, and policy behavior in this package. Shared protocol
helpers belong in a neutral package only when more than one implementation genuinely shares the
same contract.

### 2. Compose the tracker profile

Every standalone package owns one `profile.go`. It composes identity, the default endpoint,
preparation callbacks, duplicate-search factory, auth descriptor, and static typed policy:

```go
// Profile returns EXAMPLE's standalone tracker composition.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                 "EXAMPLE",
		BaseURL:              "https://tracker.example.invalid",
		UploadContentMode:    trackers.UploadContentModeDescription,
		DescriptionGroup:     "example",
		PrepareDescription:   prepareDescription,
		PrepareUpload:        prepareUpload,
		NewDuplicateAdapter:  newDuplicateAdapter,
		Rules:                rules(),
		BannedGroups:         bannedGroups(),
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{
			Source: "EXAMPLE",
		},
	}
}

// New returns a fresh EXAMPLE definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
```

`standalone.Definition` normalizes and defensively copies the profile, reports
`trackers.FamilyStandalone`, and exposes declared capabilities. Required fields are name, base URL,
upload-content mode, upload preparer, and duplicate factory. `PrepareDescription` is required only
for `description`; omit it for `none` and `screenshots`.

Keep rules and banned groups in tracker-local files and pass their package functions into the
profile. Fold one-method static policy files into `profile.go`. Keep complex auth/session, parser,
description, data, and claim behavior in focused tracker-local files.

### 3. Prepare one immutable upload operation

For `description`, implement two preparation callbacks:

- `prepareDescription`: build or pass through the description and return its group.
- `prepareUpload`: build canonical payload/artifact state once and return a
  `trackers.PreparedOperation` containing its sanitized preview and a submit closure over the
  captured state.

For `none` or `screenshots`, implement only `prepareUpload`. A screenshot-mode adapter reads
`req.Assets.Screenshots`, `req.Assets.MenuImages`, and `req.Assets.Slots`; it must tolerate a ready
empty object when its protocol allows zero images.

```go
func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "EXAMPLE",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "example",
		Description:      state.description,
		Endpoint:         state.endpoint,
		Payload:          state.previewFields(), // secrets redacted
		Files:            state.previewFiles(),
	})
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, state)
	}, state.release), nil
}
```

Description-preview calls only the description preparer. Dry-run and upload-review prepare a
preview but cannot submit. Upload submits the captured state once. A submit closure may acquire a
short-lived CSRF token, but must not rebuild canonical fields, rerun image uploads, or reread
mutable prepared-release inputs. `Release` owns temporary resources and is exact-once.

### 4. Keep category/type handling local

Map the standalone protocol's categories, types, resolutions, flags, and payload fields inside the
tracker package. Consume finalized prepared contracts:

- `api.UploadSubject` for upload/dry-run
- `api.DuplicateSubject` for duplicate search
- `api.RuleSubject` for rules

Do not add the mapping to `internal/metadata/media_details.go` or another generic package. Upload
and duplicate-search mappings are separate API contracts; test both when they differ.

### 5. Implement duplicate search

Every tracker must implement `dupe.Factory`:

```go
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	return &duplicateSearcher{
		cfg:    deps.TrackerConfig(),
		http:   deps.HTTPClient(),
		logger: deps.Logger(),
	}
}
```

The adapter's `Search` must return one structural result:

- `dupe.Resolved` for a completed search, including zero matches
- `dupe.NotRun` with a stable code and safe message for missing credentials/metadata,
  unsupported content, manual checking, or unavailable implementation
- `dupe.Failed` with a stable failure code, safe message, and diagnostic cause for attempted work
  that failed

Use only the dependency snapshot supplied to the factory. Do not read unrelated global config.
Bound response bodies, sanitize remote diagnostics, and normalize entries into `api.DupeEntry`.

### 6. Add rules and banned groups

Standalone profiles use the same typed capabilities as Unit3D:

```go
func rules() *ruletypes.RuleSet { ... }
func bannedGroups() []string { ... }

// profile.go
Rules:             rules(),
BannedGroups:      bannedGroups(),
BannedGroupPolicy: &trackers.BannedGroupPolicy{...},
```

Keep implementations in `rules.go` and `banned_groups.go`. Add combined rule behavior to
`internal/trackers/rules_test.go`; add tracker-package tests for protocol-specific parsing or
mapping.

### 7. Add optional capabilities

Declare static capabilities directly in `standalone.Profile`:

| Profile field             | Typical supporting file | Purpose                                      |
| ------------------------- | ----------------------- | -------------------------------------------- |
| `AuthCapability`          | `auth.go`               | Declares API-key, passkey, cookie, login/2FA |
| `AuthResolver`            | `auth.go`               | Validates or refreshes remote auth           |
| `AuthPolicy`              | `profile.go`            | Auth coordinator semantics                   |
| `AuthStateManager`        | `auth.go`               | Cleans tracker-owned persisted auth state    |
| `Rules`                   | `rules.go`              | Release eligibility                          |
| `MetadataPolicy`          | `profile.go`            | Required canonical/provider metadata         |
| `ArtifactPolicy`          | `profile.go`            | Torrent size and piece-size limits           |
| `UploadArtifactPolicy`    | `profile.go`            | Source/announce personalization              |
| `BannedGroups` / policy   | `banned_groups.go`      | Static or dynamic blacklists                 |
| `DupePolicy`              | `dupe.go`               | Candidate comparison semantics               |
| `AudioPolicy`             | `profile.go`            | Multi-language/bloat constraints             |
| `ImageHostPolicy`         | `profile.go`            | Allowed/private image hosts                  |
| `TorrentIdentityPolicy`   | `profile.go`            | Announce/comment identity and reuse behavior |
| `LocalizedMetadataLocale` | `profile.go`            | Locale-specific tracker rendering            |
| `DescriptionGroup`        | `profile.go`            | Saved description override group             |
| `DataPolicy`              | `profile.go`            | Lookup cooldown/defer behavior                |
| `ClaimPolicy`             | `profile.go`            | Active-claim orchestration                    |

Implement only capabilities the tracker needs. If new behavior cannot be expressed by an existing
typed capability, extend the shared profile/registry contract; do not
teach generic coordinators the tracker name.

Rare dynamic interfaces stay on a small local wrapper embedding `*standalone.Definition`. Use this
only for `NewDataLookup`, `DataLookupConfigured`, or `NewClaimChecker`; do not create an empty local
definition type for static capabilities.

For auth:

- API-key/passkey-only trackers should expose an `AuthCapability` with the corresponding
  `Requires...` flag.
- Cookie/login trackers should expose `AuthCapability` and `AuthSessionResolver`.
- Use `AuthPolicy` only for coordinator semantics that cannot be inferred from the public
  capability.
- Store and clean any tracker-specific auth material through an `AuthStateManager`.

### 8. Register and configure the tracker

In `internal/trackers/impl/registry.go`:

1. Import `internal/trackers/impl/standalone/example`.
2. Add `example.New()` to `standaloneDefinitions()`.

Add an ordered stanza to `internal/config/defaults/example.yaml`. It must contain at least one
empty activation credential such as `api_key`, `announce_url`, `username`, `password`, or
`passkey`, depending on the tracker's real setup requirements. Do not add `url`.

The registry/config parity test fails when either side is missing. Existing config fields render
through the generic frontend catalog. Follow the additional config-field steps from the Unit3D
section when a new field is unavoidable.

### 9. Standalone tests

At minimum, cover:

- definition name, endpoint, family, and registered capabilities
- config/catalog parity
- upload payload construction and response parsing
- dry-run/live payload parity and absence of dry-run network submission
- duplicate request construction, result normalization, not-run states, and failures
- category/type/resolution mapping
- rule and banned-group behavior
- auth status/login/session behavior when implemented
- bounded response handling and sanitized diagnostics
- torrent artifact and image-host behavior when declared

## Validation checklist

Start narrow, then run the shared tracker checks because registry and config changes affect all
trackers:

```powershell
go test -race -v -timeout 20m ./internal/trackers/impl/unit3d/... ./internal/trackers/impl ./internal/trackers ./internal/config
```

For a standalone tracker, replace the Unit3D package with its package:

```powershell
go test -race -v -timeout 20m ./internal/trackers/impl/standalone/example ./internal/trackers/impl ./internal/trackers ./internal/config
```

Then run the repo-required checks appropriate to the change:

```powershell
make fmt-go
make gofix-check-changed
make lint
make logpolicy
make backend
git diff --check
```

Run `make test-go` when shared Unit3D behavior, tracker orchestration, auth, config persistence, or
other broad contracts changed. When a new config field changes the WebUI surface, also run:

```powershell
pnpm --dir webui run lint
pnpm --dir webui run lint:dead
pnpm --dir webui run typecheck
pnpm --dir webui run test:unit
pnpm --dir webui run format:check
```

Do not commit generated frontend assets, local binaries, Playwright output, or populated
`internal/webserver/assets`.
