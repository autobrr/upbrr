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
- Every registered tracker resolves one versioned `trackers.ValidationPolicyBinding`. Family or
  standalone defaults may supply an explicit no-extra-validation policy. Tracker-specific payload
  constructibility checks live in `validation.go`; release-eligibility extensions may stay with
  `rules.go`.
- Pre-duplicate validation is side-effect-free. It consumes detached canonical facts, projected
  questionnaire/config overrides, and prepared-resource readiness only—never network, filesystem
  reads, runtime secrets, mutable services, or payload submission.
- Upload and dry-run must share payload preparation. Dry-run must not submit the upload or repeat
  workflow-level discovery and duplicate checking.

## Responsibility layout

Tracker filenames are architecture boundaries:

| File                           | Owns                                                                                                       |
| ------------------------------ | ---------------------------------------------------------------------------------------------------------- |
| `profile.go` / `definition.go` | identity, endpoint, family, static capabilities, policy bindings, callback wiring                          |
| `name.go`                      | pure, versioned upload/search-name policy                                                                  |
| `auth.go` / `auth_*.go`        | tracker-local login/session validation, CSRF/auth-key extraction, cookie selection, typed failures         |
| `taxonomy.go`                  | pure category/type/source/codec/resolution/audio/language/tag/flag mapping                                 |
| `rules.go`                     | declarative release rules and release-eligibility policy                                                   |
| `validation.go`                | side-effect-free payload constructibility and prepared-resource checks over `api.TrackerValidationSubject` |
| `description.go`               | description preparation/composition and tracker-specific markup                                            |
| `bbcode.go`                    | tracker-local markup primitives used by `description.go`                                                   |
| `media.go`                     | MediaInfo/BDInfo/NFO selection, reads, parsing, normalized technical facts                                 |
| `questionnaire.go`             | schema, stable answer keys, defaults, normalization, validation                                            |
| `payload.go`                   | payload-only field/file encoding when a separate module is justified                                       |
| `upload.go`                    | prepare/submit/preview orchestration, transport/response handling, immutable prepared state                |

`upload.go` may call every semantic module, but it must not declare auth, naming, taxonomy,
validation/constructibility, description, technical-media, or questionnaire algorithms. Static
banned-group declarations belong in `banned_groups.go`. Do not create empty marker files:
family/default behavior, versioned no-extra-validation policies, and declarative central auth
require no shallow tracker-local file. `make architecturepolicy` and `make lint` enforce these
boundaries.

### Release-name contract

Every registered definition has an explicit family/default or custom
`trackers.ReleaseNamePolicyBinding`:

- custom principal-name algorithms live in `name.go`;
- Unit3D `SiteProfile.BuildName` callbacks also require `BuildNameVersion`;
- requested-name instructions are inputs to the policy and may be normalized/rejected;
- the registry resolves upload and duplicate-search names before preflight and duplicate checks;
- intentionally different search names are explicit projection values;
- principal preview/payload fields use `PreparationInput.ReviewedUploadName()` and never derive a
  replacement during upload preparation;
- naming is pure: no network, auth, filesystem, clock, payload construction, or mutable globals.

Questionnaire answers that affect naming must already participate in the projection input and
fingerprint. Use synthetic naming fixtures such as `Example.Release.2026.1080p-GRP`.

### Authentication contract

`api.TrackerAuthCapability` describes supported user actions. `trackers.AuthPolicy` resolves
secret-free effective requirements for the current config/mode. `internal/trackers/auth.Service`
owns status/import/validate/login/2FA/delete coordination across CLI, API, and WebUI.

- Use central capability/requirement constructors for exact API-key, passkey, cookie, login, or
  hybrid shapes.
- API-key/passkey-only trackers normally use declarative central auth and no local `auth.go`.
- Tracker `auth.go` retains protocol-specific endpoints/forms, login markers, CSRF/auth-key/token
  parsing, cookie filtering, and typed rejection semantics.
- `internal/trackers/auth` supplies reusable cookie-login lifecycle and clock-injected TOTP.
- `internal/cookies` remains the only encrypted cookie persistence owner.
- Requirement results contain facts only, never credentials, cookies, OTP secrets, auth keys,
  announce URLs, or clients.
- Tracker auth never prompts. It returns typed actions/errors. `--unattended` skips the affected
  lane without prompting; `--unattended_confirm` may let the CLI present required manual actions.
- Hybrid modes must be explicit. For example, API-upload and cookie-form upload can resolve to
  different requirement alternatives without tracker-name branches in generic code.

### Torrent-identity contract

For `TorrentIdentityPolicy`, use `TrackerURLPatterns` for announces and `CommentURLPatterns` plus a
capturing `DetailIDPattern` for concrete tracker detail IDs. A concrete comment-derived ID is
authoritative. `WorkingTrackerID` is only a stable synthetic fallback when a working announce is
sufficient; it must not replace an extracted detail ID. Test both the concrete and fallback paths
when declaring both.

## Fast file checklist

A standard Unit3D addition normally changes:

1. `internal/trackers/impl/unit3d/sites/<tracker>/profile.go`
2. `internal/trackers/impl/unit3d/sites/<tracker>/rules.go`, when site rules exist
3. `internal/trackers/impl/unit3d/sites/<tracker>/validation.go`, when site-specific payload
   constructibility or prepared-resource policy exists
4. `internal/trackers/impl/registry.go`
5. `internal/config/defaults/example.yaml`
6. `internal/trackers/rules_test.go`, when rules or validation exist
7. a site-local `profile_test.go`, when mappings or callbacks differ from Unit3D defaults

Static banned groups, custom naming/descriptions, image-host rules, and other policies add
site-local files only.

A standalone addition normally changes:

1. a new `internal/trackers/impl/standalone/<tracker>` package containing `profile.go`,
   orchestration-only `upload.go`, `dupe.go`, the semantic files required by actual behavior, and
   focused tests
2. `internal/trackers/impl/registry.go`
3. `internal/config/defaults/example.yaml`
4. `internal/trackers/rules_test.go`, when rules or validation exist
5. shared config/frontend field contracts only when no existing `config.TrackerConfig` field fits

## Choose the shared upload-content mode

`trackers.UploadContentMode` tells generic preparation which tracker-scoped content object the
adapter consumes. Choose by protocol behavior, not tracker identity:

| Mode          | Use when                                                                        | Adapter input                                                    | Failure scope                                                                  |
| ------------- | ------------------------------------------------------------------------------- | ---------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| `none`        | The adapter builds its payload without shared descriptions or selected images   | `PreparationInput.Assets` is `nil`                               | Shared content cannot block the tracker                                        |
| `screenshots` | The adapter consumes selected screenshots/menu images but no shared description | Ready screenshot assets, which may be empty                      | A failed screenshot object blocks only that tracker                            |
| `description` | The adapter consumes the full shared description plus its image assets          | Ready aggregate description assets, which may contain empty text | A failed description object or required image substep blocks only that tracker |

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

- bearer API-key capability and effective requirements
- `/api/torrents/upload` uploads
- `/api/torrents/filter` duplicate searches
- family-owned descriptions, taxonomy, technical media handling, and multipart payloads
- generic Unit3D metadata lookup
- standard category, type, and resolution IDs
- mandatory pre-duplicate constructibility for category, type, resolution, and canonical TV
  season/episode facts
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
  validation.go            # when the site has payload constructibility/resource checks
  banned_groups.go         # when the site has a static list
  name.go                  # only for custom naming
  taxonomy.go              # only for custom category/type/resolution/keywords
  description.go           # only for a replacement/finalizer
  payload.go               # only for additional payload fields
  profile_test.go          # when profile behavior differs from defaults
```

Omit optional files when the family default applies. `profile.go` wires callbacks; callback
algorithms belong in the matching semantic file. Site-local auth/media files are not needed for
standard Unit3D behavior.

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

Use site-local `SiteProfile` callbacks when the site differs. Keep wiring in `profile.go`:

```go
// profile.go
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
```

Put the algorithms in `taxonomy.go`:

```go
// taxonomy.go
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

An empty or `"0"` category, type, or resolution mapping is treated as unsupported by mandatory
Unit3D constructibility and blocks the tracker before duplicate search. Upload preparation retains
the same defensive invariant. Test every accepted mapping; do not let accidental empty IDs reach
the API.

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

### 4. Add tracker rules and validation

Declare standard rules through `trackers.RuleSet`. Unit3D automatically enables
`RequireValidMISetting`, so the site does not need to repeat it.

An adult rule plus Aither-style language rule looks like:

```go
package example

import "github.com/autobrr/upbrr/internal/trackers"

// Rules returns EXAMPLE's release eligibility requirements.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		BlockAdult:  true,
		AdultMessage: "Adult content is not allowed at EXAMPLE.",
		Language: &trackers.LanguageRule{
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

`RuleSet` is declarative only. When site-local policy cannot be expressed by its common fields,
implement a versioned validation binding. Keep release-eligibility extensions in `rules.go`; keep
payload constructibility and prepared-resource checks in `validation.go`. For example:

```go
func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID: "unit3d-example-payload-v1",
		Check: func(
			ctx context.Context,
			subject api.TrackerValidationSubject,
			_ api.Logger,
		) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("validation canceled: %w", err)
			}
			if !subject.MediaInfoTextReady && !subject.BDInfoReady {
				return []api.RuleFailure{trackers.NewRuleFailure(
					"prepared_media_missing",
					"EXAMPLE requires prepared MediaInfo or BDInfo text.",
					api.RuleDispositionStrict,
				)}, nil
			}
			return nil, nil
		},
	}
}
```

Wire the binding in the site profile:

```go
ValidationPolicy: validationPolicy(),
```

The policy `ID` is stable and versioned because it participates in tracker policy fingerprints.
Change its version when validation semantics change. Unit3D composes the site binding with mandatory
family constructibility; a site without custom validation omits the field and still receives the
family policy.

Choose dispositions deliberately:

- `advisory` records a decision but never blocks;
- `waivable` requires exact user authorization, then permits normal live execution; debug mode bypasses it automatically;
- `strict` blocks in every execution mode.

The workflow emits one tracker-scoped authorization action for the exact current set of waivable
failures. A changed failure set invalidates that authority and requires a new confirmation. Strict
failures never emit an authorization action and cannot be overridden.

Every constructibility predicate whose result depends on a required mapping or resource, including
a missing resolution, category, type, questionnaire answer, or prepared media fact, must be
`strict`. Keep rule keys stable and unique within the tracker. Cancellation or invalid evaluation
state returns `error`; ordinary unsupported release facts return keyed `api.RuleFailure` values.
Return operator diagnostics through those stable rule codes/reasons. Validation checks must not
emit their own terminal progress messages; the workflow coordinator owns the single user-visible
outcome. Reserve the supplied logger for non-terminal `DEBUG`/`TRACE` decision context.

Validation checks consume `api.TrackerValidationSubject`, which contains detached canonical facts,
tracker-scoped answers/overrides, and readiness booleans such as `MediaInfoTextReady`,
`DVDVOBMediaInfoReady`, `BDInfoReady`, and `SceneNFOReady`. They must not perform network calls,
read paths, resolve credentials, inspect mutable services, or build/submit payloads. Reuse pure
taxonomy helpers through a narrow projection helper when needed. Provider metadata must belong to
the exact prepared source; do not introduce fallbacks to stale parser or provider state.

Validation runs during tracker projection before authentication and duplicate search. Preflight
re-evaluates an eligible projection when local availability clears a projected resource-readiness
flag, and upload preparation retains a defensive constructibility check for direct callers. These
evaluations must produce the same blocking result for the same policy ID, subject, and execution
mode.

Add behavior cases to the combined `internal/trackers/rules_test.go`. Use
`EvaluateTrackerValidationWithRegistry` so tests include declarative rules, metadata requirements,
mandatory Unit3D constructibility, and the site validation binding. Cover stable policy IDs,
rule codes/reasons, normal/debug disposition behavior, missing required resources, and every
supported/unsupported custom mapping. Add focused site tests only for local mapping or complex
validation behavior.

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

`make architecturepolicy` requires tracker-local static banned-group declarations to live in
`banned_groups.go`; do not hide the list or its constructor in `profile.go`, `rules.go`, or
`upload.go`.

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
| `ValidationPolicy` | Versioned site constructibility/custom policy binding        |
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
- rule and validation allow/deny cases in `internal/trackers/rules_test.go`
- stable validation policy ID, failure codes/reasons, and normal/debug behavior
- mandatory category/type/resolution constructibility for accepted and unsupported inputs
- every nonstandard category/type/resolution mapping
- additional payload fields and description/name transforms
- static/dynamic banned-group behavior when present
- duplicate-search query mapping when it differs from standard Unit3D
- dry-run/live payload parity for site-specific fields

Existing contract tests also enforce endpoint locality, validation/banned-group ownership, absence
of generic tracker dispatch, a versioned validation policy, and a duplicate factory for every
registered tracker.

## Add a standalone non-Unit3D tracker

### 1. Create a tracker-owned package

Use a package such as:

```text
internal/trackers/impl/standalone/example/
  profile.go
  upload.go
  dupe.go
  name.go                  # custom naming only
  auth.go                  # protocol-specific auth only
  taxonomy.go              # classification mappings
  description.go           # description mode/composition
  media.go                 # technical files/parsing, when present
  questionnaire.go         # schema/answers, when present
  payload.go               # optional payload-only split
  rules.go                 # optional
  validation.go            # payload constructibility/prepared-resource checks
  banned_groups.go         # optional
  data.go                  # optional dynamic lookup
  claims.go                # optional dynamic claim checks
  profile_test.go
  upload_test.go
  dupe_test.go
```

Keep all endpoint, payload, protocol auth, parser, rule, validation, and policy behavior in this
package.
Create only files backed by real behavior. Shared helpers belong in a neutral package only when at
least two implementations genuinely share the exact contract; unrelated standalone taxonomy and
description behavior stays local.

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
		ValidationPolicy:     validationPolicy(),
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
for `description`; omit it for `none` and `screenshots`. Omit `ValidationPolicy` only when the
tracker has no extra constructibility or custom policy; `standalone.Definition` then supplies an
explicit versioned no-extra-validation binding.

Keep rules, validation, and banned groups in their tracker-local responsibility files and pass
their package functions into the profile. Fold unrelated one-method static policy files into
`profile.go`, but keep payload constructibility in `validation.go`, release-eligibility policy with
`rules.go`, and static banned-group declarations in `banned_groups.go`. Keep complex auth/session,
parser, description, data, and claim behavior in focused tracker-local files.

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

#### Preserve registered torrent authority

After confirmed remote success, return one `api.UploadedTorrent` per successful tracker upload in
`api.UploadSummary.UploadedTorrents`. Populate:

- `Tracker` and the concrete `TorrentID` when available.
- `TorrentURL` with the direct tracker detail page, not the download endpoint.
- `TorrentPath` with the retained registered torrent. Prefer downloading and validating the exact
  tracker-returned metainfo through `trackers.DownloadRegisteredTorrent`.
- `DownloadURL` when the authenticated registered-torrent endpoint is available. Treat it as
  private authority: never expose it in previews, public history, logs, or diagnostics.

Use `trackers.PersistReconstructedRegisteredTorrent` only when the protocol makes reconstruction
deterministic after confirmed success. A registered-artifact download or persistence failure must
use `trackers.LogRegisteredTorrentUnavailable` and leave the successful remote upload successful.
Client injection consumes this registered authority; never substitute the pre-upload prepared
torrent as if it were tracker-registered.

### 4. Keep category/type handling local

Map the standalone protocol's categories, types, resolutions, sources, codecs, audio/languages,
tags, and flags in `taxonomy.go`. Keep taxonomy pure—no network, filesystem, auth, payload
encoding, or description rendering. Consume finalized prepared contracts:

- `api.UploadSubject` for upload/dry-run
- `api.DuplicateSubject` for duplicate search
- `api.RuleSubject` for declarative rules
- `api.TrackerValidationSubject` for pre-duplicate constructibility/custom validation

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

### 6. Add rules, validation, and banned groups

Standalone profiles use the same typed capabilities as Unit3D:

```go
func rules() *trackers.RuleSet { ... }
func validationPolicy() trackers.ValidationPolicyBinding { ... }
func bannedGroups() []string { ... }

// profile.go
Rules:             rules(),
ValidationPolicy:  validationPolicy(),
BannedGroups:      bannedGroups(),
BannedGroupPolicy: &trackers.BannedGroupPolicy{...},
```

Keep implementations in `rules.go`, `validation.go`, and `banned_groups.go`. Validation uses the
same `ValidationPolicyBinding` contract and execution-mode semantics described in the Unit3D
section. Use strict failures for payload constructibility such as unsupported taxonomy mappings,
missing required questionnaire answers, or unavailable prepared media. Add combined rule and
validation behavior to `internal/trackers/rules_test.go`; add tracker-package tests for
protocol-specific pure mapping or complex validation.

### 7. Add optional capabilities

Declare static capabilities directly in `standalone.Profile`:

| Profile field             | Typical supporting file      | Purpose                                           |
| ------------------------- | ---------------------------- | ------------------------------------------------- |
| `AuthCapability`          | `auth.go`                    | Declares API-key, passkey, cookie, login/2FA      |
| `AuthResolver`            | `auth.go`                    | Validates or refreshes remote auth                |
| `AuthPolicy`              | `profile.go`                 | Auth coordinator semantics                        |
| `AuthStateManager`        | `auth.go`                    | Cleans tracker-owned persisted auth state         |
| `Rules`                   | `rules.go`                   | Release eligibility                               |
| `ValidationPolicy`        | `rules.go` / `validation.go` | Versioned eligibility/constructibility validation |
| `MetadataPolicy`          | `profile.go`                 | Required canonical/provider metadata              |
| `ArtifactPolicy`          | `profile.go`                 | Torrent size and piece-size limits                |
| `UploadArtifactPolicy`    | `profile.go`                 | Source/announce personalization                   |
| `BannedGroups` / policy   | `banned_groups.go`           | Static or dynamic blacklists                      |
| `DupePolicy`              | `dupe.go`                    | Candidate comparison semantics                    |
| `AudioPolicy`             | `profile.go`                 | Multi-language/bloat constraints                  |
| `ImageHostPolicy`         | `profile.go`                 | Allowed/private image hosts                       |
| `TorrentIdentityPolicy`   | `profile.go`                 | Announce/comment identity and reuse behavior      |
| `LocalizedMetadataLocale` | `profile.go`                 | Locale-specific tracker rendering                 |
| `DescriptionGroup`        | `profile.go`                 | Saved description override group                  |
| `DataPolicy`              | `profile.go`                 | Lookup cooldown/defer behavior                    |
| `ClaimPolicy`             | `profile.go`                 | Active-claim orchestration                        |

Implement only capabilities the tracker needs. If new behavior cannot be expressed by an existing
typed capability, extend the shared profile/registry contract; do not
teach generic coordinators the tracker name.

Rare dynamic interfaces stay on a small local wrapper embedding `*standalone.Definition`. Use this
only for `NewDataLookup`, `DataLookupConfigured`, or `NewClaimChecker`; do not create an empty local
definition type for static capabilities.

For auth:

- Every tracker must bind explicit effective requirements through `AuthPolicy`; the standard
  standalone constructor supplies exact declarative bindings from known capabilities.
- API-key/passkey-only trackers should use central constructors with the corresponding
  `Requires...` flag and normally need no `auth.go`.
- Cookie/login trackers expose `AuthCapability`, effective requirements, and an
  `AuthSessionResolver` when remote validation/login is supported.
- Hybrid trackers test every mode and requirement alternative, including whether API credentials
  also require an upload session.
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
- exact taxonomy mapping, including unknown/unsupported inputs and no I/O
- exact description output and technical-media selection/parsing
- questionnaire field order, keys, defaults, normalization, and invalid/missing answers
- rule and banned-group behavior
- validation policy ID, stable failure codes/reasons, constructibility, and normal/debug behavior
- auth capability/effective requirements plus status/login/session/2FA behavior when implemented
- release-name projection/version and principal payload use of the reviewed name
- bounded response handling and sanitized diagnostics
- torrent artifact and image-host behavior when declared
- successful `UploadedTorrents` identity, direct tracker-page URL, registered artifact
  download/reconstruction, and post-submit artifact-failure semantics

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
make architecturepolicy
make lint
make logpolicy
make pathpolicy
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
