# Tracker Release-Name Projection Plan

Status: proposed  
Date: 2026-07-23  
Scope: tracker-specific upload-name derivation, duplicate-search naming, tracker projection, retained upload preparation, CLI/API/WebUI presentation, tracker package layout, and contract tests.  
Related: `plans/release-workflow-centralization.md`

## Executive decision

Make tracker release-name resolution a required, pure, versioned registry capability. Resolve it centrally before preflight and duplicate checking, publish it in the tracker projection, and retain that exact value through payload preparation and submission.

The selected design combines:

- one mandatory naming contract at the `trackers.Definition` boundary;
- one explicit default policy per shared tracker family, avoiding 66 shallow implementations;
- tracker-local custom policies wired from `profile.go` or `definition.go`;
- custom algorithms in an obvious `name.go`, not in `upload.go`;
- a distinct, explicit duplicate-search name only when tracker search semantics truly differ;
- policy/version participation in existing projection fingerprints;
- central upload-time revalidation plus exact payload-preview comparison;
- no CLI-, WebUI-, or API-specific naming logic.

The central invariant is:

```text
canonical release
  -> effective tracker config/questionnaire/name instruction
  -> tracker naming policy
  -> reviewed tracker projection
       upload name
       duplicate-search name
  -> preflight
  -> duplicate check
  -> retained upload preparation
  -> exact payload/preview equality fence
  -> submission of retained request
```

For a ready tracker lane:

```text
displayed upload name == reviewed upload name == prepared payload name == submitted payload name
```

The duplicate check uses the reviewed upload name by default. A different search term must be declared explicitly and displayed as a different semantic value; it must never arise from an adapter silently ignoring `DuplicateCriteria.Name`.

## Problem statement

The current implementation reviews one name and prepares another.

- `internal/trackers/projection.go` creates a canonical projection without invoking tracker naming.
- `ProjectReleaseWithUploadName` changes `UploadReleaseName` only. It leaves `DuplicateCriteria.Name` canonical.
- BTN is the only custom `ReleaseProjector`; Unit3D, AZ-family, AR, MTV, and other standalone naming logic remains in late preparation.
- `applyProjectionInstruction` mutates `UploadReleaseName` after the projection and duplicate criteria have been constructed. It does not synchronize `DuplicateCriteria.Name` or its fingerprint.
- Duplicate services search with `DuplicateCriteria.Name` but decorate results with `UploadReleaseName`. Logs/UI can therefore report a name different from the actual remote query.
- `applyReviewedProjection` overwrites both `Meta.ReleaseName` and `Meta.ReleaseNameNoTag`, after which adapters may apply their naming transform again. Non-idempotent policies can double-transform.
- `validatePreparedProjection` catches a mismatch only during dry-run/upload preparation. At that point the WebUI, CLI, preflight, and dupe review have already used the wrong name.

The supplied trace demonstrates both variants:

- MTV: canonical spacing was reviewed; preparation converted separators.
- AR: canonical generated name was reviewed; preparation derived a different source/scene-based name.

The late equality check is a useful final fence, but it cannot substitute for resolving tracker semantics before duplicate checking.

## Required behavior

### Ordering

1. Build the canonical prepared `UploadSubject`.
2. Apply tracker-local config/site overrides and questionnaire answers.
3. Apply any requested upload-name instruction as input to the tracker naming policy.
4. Resolve and validate tracker-specific names.
5. Publish the immutable tracker projection.
6. Run preflight and duplicate checking from that projection.
7. Render the same projection in CLI, API, and WebUI.
8. Revalidate the policy input/version before retained upload preparation.
9. Build the payload from the reviewed name without recomputing it.
10. Reject any preview/payload name that differs from the projection.
11. Submit retained prepared bytes; do not rebuild naming or payload after approval.

### Naming invariants

- `CanonicalReleaseName` remains the application-level canonical name.
- `UploadReleaseName` is the exact principal name the tracker upload will use.
- `DuplicateCriteria.Name` defaults to `UploadReleaseName`.
- A tracker with intentionally different search semantics declares an explicit search name. No implicit fallback to canonical/title/filename is allowed inside the dupe adapter.
- Naming is deterministic and side-effect-free:
  - no network;
  - no authentication/session access;
  - no filesystem reads;
  - no clock or mutable global state;
  - no payload construction.
- Source-path text may be used as an input where existing tracker policy requires it, but the policy must not inspect the filesystem. Public state retains only the resolved public name and fingerprints, never the source path.
- Output must be trimmed, non-empty, free of control characters, and valid for the tracker policy.
- A policy error or invalid output blocks only that tracker lane:
  - `Readiness=blocked`;
  - `DupeReady=false`;
  - `UploadReady=false`;
  - stable tracker-scoped failure code.
- No canonical fallback is allowed after a custom policy fails.
- Policy changes invalidate projection, preflight, dupe evidence, and downstream retained plans.
- Config/questionnaire/name-instruction changes invalidate the same downstream state.
- Missing reviewed projection, stale provenance, or name mismatch prevents submission.

### User-supplied upload-name instruction

`TrackerProjectionInstructions.UploadReleaseName` must stop being a post-projection patch.

Use these semantics:

- absent or reset: policy derives the automatic tracker name;
- present and empty: block with `name_instruction`;
- present and non-empty: pass the requested value into the tracker policy;
- policy may normalize or reject the requested value according to required tracker rules;
- policy output, not the raw instruction, becomes `UploadReleaseName`;
- the same instruction snapshot is retained for upload-time revalidation.

This guarantees that a manual value cannot bypass required separator, suffix, character, or naming rules.

## Selected module design

### Public internal contract

Exact type names may be adjusted during implementation, but preserve this shape:

```go
type ReleaseNameInput struct {
	Subject       api.UploadSubject
	TrackerConfig config.TrackerConfig
	RequestedName *string
}

type ResolvedReleaseNames struct {
	Upload     string
	Duplicate  string // empty means Upload
	Additional []api.TrackerReleaseName
}

type ReleaseNamePolicy interface {
	ResolveReleaseNames(ReleaseNameInput) (ResolvedReleaseNames, error)
}

type ReleaseNamePolicyBinding struct {
	ID       string // stable and versioned, e.g. "standalone/mtv/v1"
	Resolver ReleaseNamePolicy
}

type ReleaseNamePolicyProvider interface {
	ReleaseNamePolicy() ReleaseNamePolicyBinding
}
```

Make `ReleaseNamePolicyProvider` part of the required `Definition` contract, or embed its method directly into `Definition`. This makes every registered definition name-capable.

Family definitions provide the common implementation:

- `unit3d.Definition`: family default plus optional site callback;
- `azfamily.Definition`: AZ-family policy;
- `standalone.Definition`: canonical default plus optional profile callback.

The registry rejects:

- nil resolver;
- blank policy ID/version;
- invalid family binding;
- a custom callback without a custom policy version.

Do not require 66 identical canonical methods. The shared family definition is the explicit default owner. A source-policy test prevents upload files from adding hidden name derivation later.

### Deep naming module

Add `internal/trackers/release_name.go` to hide:

- canonical base-name selection;
- requested-name semantics;
- cloned input construction;
- output trimming/validation;
- duplicate-name defaulting;
- additional-name normalization/deduplication;
- policy/version fingerprinting;
- conversion into projection fields;
- stale/mismatch classification;
- upload-time re-resolution;
- safe tracker-scoped failures.

Keep the exported seam to one pure resolver method plus a versioned binding. Do not introduce an I/O port or mock adapter; all dependencies are in-process values.

Suggested checked payload accessor:

```go
func (input PreparationInput) ReviewedUploadName() (string, error)
```

It returns only a ready, projection-authorized name. Workflow dry-run/upload preparation must fail if the projection or name is absent. Payload builders consume this accessor; they do not call tracker-local name policies.

### Fingerprints and retained provenance

Avoid new public contract fields unless implementation proves them necessary.

Include the following in the existing `ProjectorFingerprint`:

- tracker ID;
- naming policy ID/version;
- projector/taxonomy version;
- normalized effective naming inputs;
- requested-name presence/value;
- resolved upload/search/additional names.

Include the exact `DuplicateCriteria.Name` in `CriteriaFingerprint`.

`TrackerReleaseProjectionSet.Instructions` already references the exact projection instruction snapshot. Retained upload preparation must load that snapshot and re-run the pure naming policy against:

- the same canonical release ref;
- effective tracker config;
- tracker-local questionnaire answers;
- requested-name instruction;
- current policy ID/version.

Any difference is `name_policy_stale` or `name_projection_mismatch`, not an automatic repair during upload.

### Projection ownership

`Registry.ProjectRelease` becomes the only naming coordinator:

1. derive canonical projection facts;
2. resolve tracker naming;
3. assign `UploadReleaseName`;
4. assign `DuplicateCriteria.Name` from explicit search name or upload name;
5. append typed additional names;
6. calculate criteria/projector fingerprints;
7. apply readiness/failure state.

Retire these split-ownership paths:

- `ProjectReleaseWithUploadName`;
- BTN’s custom `ProjectRelease`;
- post-projection name mutation in `applyProjectionInstruction`;
- canonical fallback that can bypass a failed custom policy.

The broad optional `ReleaseProjector` should not remain as a hidden naming escape hatch. If tracker-specific taxonomy projection is needed later, add a narrow taxonomy policy rather than allowing a second owner to rewrite names.

### Duplicate-search semantics

Default:

```text
DuplicateCriteria.Name = UploadReleaseName
```

Exception:

- policy returns an explicit `Duplicate` value;
- projection records it in `DuplicateCriteria.Name`;
- when it differs, also expose it as `AdditionalNames{Role: "search"}` for review;
- CLI/WebUI show the duplicate-search name when different;
- dupe adapter consumes the projected criteria and does not reconstruct a title/name from `UploadSubject`.

Audit each dupe adapter’s outbound query. Existing title/season/provider-ID searches may be valid, but their semantic input must be represented explicitly in `TrackerDuplicateCriteria`. A payload field named `name` or `title` is not automatically the principal upload release name.

### Final effect fences

Retain and strengthen `validatePreparedProjection`:

- compare preview principal upload name exactly;
- compare tracker-native taxonomy exactly;
- verify the name-bearing payload field where the adapter exposes it;
- reject a missing reviewed projection in workflow dry-run/upload paths;
- release prepared resources on mismatch;
- never create a submit-capable retained operation after mismatch.

Stop mutating canonical metadata in `applyReviewedProjection`.

Preferred payload flow:

```text
canonical Meta remains immutable
projection.UploadReleaseName is carried separately
adapter requests ReviewedUploadName()
adapter writes that exact value to preview and principal payload field
central validator compares preview/payload with projection
```

If a temporary compatibility shim must set `Meta.ReleaseName`, set it only in a payload-local clone after policy revalidation and remove it after all adapters use the checked accessor. Never set `ReleaseNameNoTag` to the tracker name.

## Tracker naming inventory

The built-in registry currently contains 37 Unit3D sites, 3 AZ-family sites, and 26 standalone trackers.

### Unit3D

Current family seam:

- `internal/trackers/impl/unit3d/profiles.go`: `SiteProfile.BuildName`;
- `internal/trackers/impl/unit3d/names.go`: `buildUnit3DName`;
- upload uses the builder;
- projection does not.

Custom naming sites:

| Sites | Current behavior | Migration |
|---|---|---|
| ACM, AITHER, DP, LDU, RHD | localized/language/audio/source/full reconstruction rules | Move complex algorithms to site-local `name.go`; wire versioned callbacks from `profile.go`. |
| CBR, LCD, SAM | localized formatted names | Keep one site-local naming function; move to `name.go` if currently embedded in `profile.go`. |
| OE, RF | no-group suffix rules | Move to `name.go`; test exact suffix and idempotence. |
| ULCX | removes `Hybrid` for applicable WebDV cases | Move to `name.go`; test applicable and no-op cases. |
| ZNTH | episode title/year reconstruction | Keep existing dedicated `name.go`; add versioned policy binding. |

Canonical/default Unit3D sites:

```text
A4K, BLU, EMUW, FRIKI, HHD, IHD, ITT, LST, LT, LUME, MNS, OTW, PT,
PTT, R4E, RAS, SHRI, SP, STC, TIK, TLZ, TOS, TTR, UTP, YUS
```

Migration rules:

- `unit3d.Definition.ReleaseNamePolicy` calls the family name builder.
- Custom `BuildName` callbacks require an explicit policy version.
- Default sites use the explicit Unit3D canonical policy version.
- `unit3d/upload.go` consumes `ReviewedUploadName()` and stops calling the builder.
- Registry tests cover all 37 profiles, not only custom sites.

### AZ family

Current seam:

- `internal/trackers/impl/azfamily/description.go`: `editName`;
- used during late payload/preview construction for AZ, CZ, and PHD;
- misplaced in description code and absent from projection.

Migration:

- move `editName` and helpers to `internal/trackers/impl/azfamily/name.go`;
- bind the policy from `azfamily.Definition`;
- assign a family policy version, with site discriminator included in the fingerprint;
- make payload/preview use `ReviewedUploadName()`;
- test AZ, CZ, and PHD independently, including AKA/dubbed/dual-audio removal, PHD edition handling, no-group suffix, and TV-year behavior.

### Standalone trackers

#### Material custom transforms

| Tracker | Current location/behavior | Required migration |
|---|---|---|
| AR | `upload.go:resolveARName`; scene-first or source-derived dotted name plus group handling | Move to `name.go`; policy uses values only, no filesystem reads; preview/payload consume reviewed name. |
| ASC | `metadata.go:resolveUploadTitle`; localized/display title plus season/episode construction | Move principal-name logic to `name.go`; distinguish localized metadata title fields from principal upload name. |
| BHD | `upload.go:resolveUploadName`; DVD/audio/video and `DD+` normalization | Move to `name.go`; projection must have all required local facts or block. |
| BHDTV | `upload.go:resolveUploadName`; separator and audio normalization | Move to `name.go`; add exact separator regression. |
| BTN | `upload.go:resolveUploadName` plus later `applyBTNNameMapping` on `scenename` | Consolidate both into one policy. Derive mapping from local prepared facts before dupes; do not depend on remote autofill. Remove custom `ProjectRelease`. |
| DC | `upload.go:resolveUploadName`; scene marker, codec/HDR normalization, ASCII sanitation | Move to `name.go`; validate sanitation output centrally. |
| FF | `upload.go:resolveName`; scene/fallback and dot formatting | Move to `name.go`. |
| FL | `upload.go:resolveName`; questionnaire override plus DV/Remux/HDR/audio/separator rules | Move to `name.go`; requested/questionnaire changes invalidate projection and dupes. Keep torrent filename policy separate. |
| HDT | `upload.go:resolveName`; audio, DV, remux, and punctuation rules | Move to `name.go`. |
| IS | `upload.go:resolveSubject`; scene/fallback and alternate/dubbed/dual-audio cleanup | Move to `name.go`; distinguish upload subject from dupe query semantics. |
| MTV | `upload.go:resolveUploadName`/`cleanName`; whitespace-to-dot formatting | Move to `name.go`; wire in `profile.go`; payload uses reviewed name. |
| SPD | `upload.go:normalizeName`; ASCII/forbidden-character handling | Move to `name.go`; policy input must include selected base name. |
| THR | `upload.go:resolveName`; `DD+` and regex sanitation | Move to `name.go`. |
| TVC | `upload.go:resolveName`; tracker-specific movie/pack/episode reconstruction and country code | Move to `name.go`; cover each release shape. |

#### Name selection without material transformation

| Trackers | Current behavior | Migration |
|---|---|---|
| ANT, HDB, NBL, PTP | `ReleaseName` -> `ReleaseNameNoTag` -> filename -> source base | Use shared standalone canonical/fallback policy; remove upload-local resolver. |
| CZT, TL | scene-first, then release/title/filename | Use a shared explicit scene-first policy or small tracker-local `name.go`; payload consumes reviewed name. |

#### Inline canonical/fallback use

```text
BJS, BT, GPW, HDS, PTS, RTF
```

Audit each preview and payload field, then bind the explicit standalone default policy. Remove inline principal-name selection. Preserve unrelated metadata title/subtitle fields as separately named payload values.

### Secondary name-bearing fields

The migration must produce a tracker name-field ledger containing, for every built-in tracker:

- principal upload name;
- payload field carrying the principal name;
- preview field;
- duplicate-search term;
- scene/alternate/group/title fields;
- source function and file;
- policy ID/version;
- config/questionnaire dependencies.

Pay special attention to:

- BTN `scenename` after `applyBTNNameMapping`;
- GPW title/subname fields, which may be metadata titles rather than the principal release name;
- ASC localized titles;
- scene-name fields distinct from upload names;
- dupe adapters that currently rebuild queries from `Release.Title`.

Do not force semantically different title fields into `UploadReleaseName`. Instead, classify them and use existing typed `AdditionalNames` roles or explicit payload metadata.

## MTV package cleanup

Do not move MTV implementation detail into a large `definition.go`. Keep declaration/wiring in `profile.go` and split behavior by responsibility:

| File | Ownership |
|---|---|
| `profile.go` | identity, capabilities, policy bindings, constructors |
| `name.go` | `resolveUploadName`, `cleanName`, naming version/binding |
| `taxonomy.go` | resolution/category/type/source/origin pure mappings |
| `auth.go` | auth-key extraction, cookies, login, 2FA/TOTP, session resolution |
| `payload.go` | upload fields, tags/group description, multipart request construction |
| `upload.go` | prepare/submit orchestration and response handling only |

Move corresponding tests beside each responsibility:

- `name_test.go`;
- `taxonomy_test.go`;
- `auth_test.go`;
- `payload_test.go`;
- orchestration tests remain in `upload_test.go`.

This is a locality cleanup, not a behavior rewrite. Perform it after characterization tests and before changing MTV payload callers, so moves and semantic changes remain reviewable.

Apply the same rule across trackers:

- policy declaration/wiring: `profile.go` or `definition.go`;
- name algorithm: `name.go`;
- HTTP/auth/submit: `upload.go` or dedicated `auth.go`;
- payload-only mapping: `payload.go`;
- description behavior: `description.go`;
- duplicate-search adapter: `dupe.go`.

## Implementation phases

### Phase 0 — Characterize and complete the name-field ledger

Files:

- `internal/trackers/impl/registry_test.go`;
- family and tracker tests;
- new testdata/ledger only if it stays synthetic and maintainable.

Tasks:

1. Add synthetic fixtures covering movie, episode, season pack, disc, scene, no-group, localized, and questionnaire-dependent inputs.
2. Record current principal preview/payload names for every built-in tracker.
3. Record the actual outbound dupe query name for every dupe adapter.
4. Classify every name-bearing form field as principal upload, search, scene, localized title, group title, or metadata title.
5. Add focused regressions for AR and MTV from the supplied failure shape.
6. Add BTN regression proving `applyBTNNameMapping` cannot create a second unprojected final name.
7. Confirm naming functions perform no network/filesystem/auth work. Refactor input acquisition before adopting the policy if they do.

Exit:

- every built-in tracker has a documented principal name and search semantic;
- tests fail when projection and preview/payload differ.

### Phase 1 — Add the central naming contract

Files:

- `internal/trackers/definition.go`;
- new `internal/trackers/release_name.go`;
- `internal/trackers/registry.go`;
- `internal/trackers/registry_test.go`;
- test definitions/fakes.

Tasks:

1. Add input/result/policy/binding types.
2. Add explicit canonical and scene-first policy helpers.
3. Make the definition boundary provide a policy binding.
4. Validate policy/version during registration.
5. Add normalized output validation and stable failures:
   - `name_policy`;
   - `name_required`;
   - `name_instruction`;
   - `name_policy_stale`;
   - `name_projection_mismatch`.
6. Add safe fingerprint construction without credentials or raw private paths.
7. Add the checked `ReviewedUploadName()` accessor.

Exit:

- no registered definition lacks central naming ownership;
- policy failures block only the affected lane.

### Phase 2 — Resolve names before preflight and dupes

Files:

- `internal/trackers/projection.go`;
- `internal/trackers/workflow_projector.go`;
- `internal/trackers/registry.go`;
- `pkg/api/workflow_contracts_validation.go`;
- relevant projection/presence/fingerprint tests.

Tasks:

1. Move requested-name handling before policy resolution.
2. Resolve name after effective config/site/questionnaire inputs are installed.
3. Build `UploadReleaseName`, `DuplicateCriteria.Name`, additional names, and fingerprints atomically.
4. Require ready projections to have valid principal/search names.
5. Require `DuplicateCriteria.Name == UploadReleaseName` unless the policy explicitly declared a search name.
6. Remove post-fingerprint name mutation.
7. Retire `ProjectReleaseWithUploadName` and BTN’s custom projector.
8. Bump projector/policy versions so stale persisted projections cannot survive.

Exit:

- AR/MTV/BTN and all family custom names are visible before preflight/dupes;
- criteria fingerprints cover the exact remote search name.

### Phase 3 — Make duplicate checks consume exact criteria

Files:

- `internal/core/workflow_dupes.go`;
- `internal/trackers/dupe/service.go`;
- every `dupe.go`/duplicate adapter identified by the ledger;
- CLI/WebUI duplicate review components when search differs.

Tasks:

1. Remove fallback reconstruction of query names from canonical metadata.
2. Pass exact `TrackerDuplicateCriteria` into adapters.
3. Make adapters use the declared search name/provider IDs/taxonomy without silently substituting title or filename.
4. Add a defensive central invariant before any remote query.
5. Show `upload_name` and, only when different, `search_name` in CLI/WebUI/API review.
6. Keep remote request behavior tracker-specific; keep orchestration and contract validation central.

Exit:

- tests assert the actual adapter request query, not only decorated result output;
- displayed search semantics match the remote request.

### Phase 4 — Retain and enforce the reviewed name during upload

Files:

- `internal/trackers/plan.go`;
- `internal/trackers/service_upload_plan.go`;
- `internal/core/workflow_upload_plan.go`;
- `internal/releaseworkflow/module.go` where instruction snapshots are loaded;
- `internal/trackers/projection.go`.

Tasks:

1. Load the exact projection instruction snapshot referenced by the projection set.
2. Re-run the pure policy against the canonical subject and effective inputs.
3. Compare policy ID/version, resolved names, and fingerprints.
4. Stop rewriting canonical `ReleaseName`/`ReleaseNameNoTag`.
5. Require payload builders to get the exact name through `ReviewedUploadName()`.
6. Strengthen final preview/payload equality validation.
7. Route legacy direct `Service.Upload`/dry-run entry points through the same projection path or make them private test helpers. No face may bypass name projection.
8. Ensure mismatch cleanup releases prepared resources and creates no submit closure/retained capsule.

Exit:

- tracker naming runs before dupes and only as central revalidation later;
- payload construction never derives a second principal name.

### Phase 5 — Migrate family policies

Order:

1. Unit3D;
2. AZ family;
3. standalone default and scene-first policies.

Tasks:

- wire family definition policies;
- migrate site callbacks and versions;
- make upload payloads consume the checked reviewed name;
- move misplaced naming code to `name.go`;
- delete duplicate late helpers after all callers move;
- preserve canonical and tracker-specific values as separate fields.

Exit:

- all family trackers pass registry-wide projection-to-payload parity tests.

### Phase 6 — Migrate standalone custom policies

Suggested batches:

1. AR, MTV, BTN — supplied regressions and hidden double transform;
2. FL, TVC, ASC — questionnaire/localized/reconstructed names;
3. BHD, BHDTV, DC, HDT, IS — broader normalization;
4. FF, SPD, THR — smaller transformations;
5. ANT, HDB, NBL, PTP, CZT, TL and inline default trackers.

For each tracker:

1. move algorithm to `name.go`;
2. add policy version and profile binding;
3. define requested-name behavior;
4. define search-name behavior;
5. replace preview/payload calls with `ReviewedUploadName()`;
6. add positive, no-op, requested-name, invalid-output, and mismatch tests;
7. delete old helper/call sites.

Exit:

- no principal release-name transformation remains in standalone `upload.go`.

### Phase 7 — Enforce source layout and cross-surface parity

Add a source-policy/architecture check that fails when:

- tracker `upload.go` defines or calls principal functions matching the naming ledger outside approved `name.go`;
- payload code calls `buildUnit3DName`, `editName`, `resolveUploadName`, or equivalent legacy helpers;
- a custom naming callback lacks a version;
- a tracker bypasses `ReviewedUploadName()` for its principal payload field;
- a dupe adapter ignores declared criteria name without an explicit non-name search contract.

Add cross-surface tests:

- API projection carries exact custom upload and search names;
- CLI projection and dupe output show the same values;
- WebUI projection/dupe/upload review shows the same values;
- one tracker naming failure does not stop eligible trackers;
- no face can continue a blocked/mismatched lane.

## Test matrix

### Central contract tests

- canonical default policy;
- custom policy;
- custom explicit search name;
- blank output;
- invalid/control-character output;
- policy error;
- explicit empty instruction;
- instruction normalized by policy;
- instruction rejected by policy;
- config-dependent name;
- questionnaire-dependent name;
- changed policy version;
- changed config/questionnaire/instruction;
- criteria/projector fingerprint changes;
- projection-to-preview exact equality;
- deliberate payload mismatch fails before submit;
- no double transformation;
- no canonical metadata mutation;
- lane isolation.

### Registry-wide tests

For every built-in tracker:

- policy binding exists and has a stable version;
- policy resolves a non-empty name for applicable synthetic fixtures;
- projected upload name equals prepared preview principal name;
- projected search name equals actual dupe adapter query;
- policy is deterministic;
- canonical subject is not mutated;
- projection performs no auth/network/filesystem work;
- payload uses the retained projected name;
- ready projection validates under `pkg/api`.

Not every tracker supports every fixture. The table must state applicable release shapes and expected blocked reasons rather than weakening the contract.

### Targeted tracker tests

- AR source-derived and scene names;
- MTV separator formatting;
- BTN episode stripping plus codec/source mapping;
- all 12 custom Unit3D sites;
- AZ/CZ/PHD;
- FL questionnaire name;
- ASC localized name;
- TVC movie/pack/episode variants;
- sanitation/normalization trackers;
- canonical/default standalone trackers.

Use only synthetic names and IDs, for example:

```text
Example Release 2026
Example.Release.2026.1080p-GRP
tt1234567
```

## Validation

Start narrow while implementing each batch:

```powershell
go test -race -v -timeout 20m ./internal/trackers/...
go test -race -v -timeout 20m ./internal/core ./internal/releaseworkflow ./pkg/api
go test -race -v -timeout 20m ./cmd/upbrr ./internal/webserver/...
```

When frontend rendering/tests change:

```powershell
pnpm --dir webui run lint
pnpm --dir webui run lint:dead
pnpm --dir webui run typecheck
pnpm --dir webui run test:unit
pnpm --dir webui run format:check
```

Before declaring implementation complete:

```powershell
make backend
make test-go
make lint
make pathpolicy
make gofix-check-changed
make test-frontend
git diff --check
```

Run `make workflow-contracts` only if public API/OpenAPI/TypeScript shapes change. The preferred first implementation reuses existing projection/name/fingerprint fields and should not require a schema addition.

## Risks and controls

| Risk | Control |
|---|---|
| Manual override bypasses tracker normalization | Feed instruction into the policy before projection; reject post-policy mutation. |
| Name transform runs twice | Keep canonical meta immutable; payload consumes reviewed name directly. |
| Policy changes while retained state exists | Version in projector fingerprint; re-resolve from exact instruction/config snapshot. |
| Different dupe search term is legitimate | Model it explicitly; display it; assert actual outbound query. |
| New adapter hides naming in `upload.go` | Architecture check plus registry-wide projection/payload parity test. |
| Policy accidentally performs I/O | Pure interface without context/services; side-effect tests; move acquisition to earlier workflow stage. |
| Source-derived name exposes private path | Resolve public base name only; fingerprint private input; never serialize raw path. |
| Large migration obscures behavior changes | Characterize first; migrate by family/batch; separate MTV file moves from semantic changes. |
| Public schema churn delays root fix | Reuse `UploadReleaseName`, `DuplicateCriteria`, `AdditionalNames`, and existing fingerprints. |
| Tracker-local failure becomes global | Preserve lane-scoped readiness/failure and continue eligible trackers. |

## Non-goals

- Redesigning tracker taxonomy in this change.
- Moving all tracker payload logic into `definition.go`.
- Adding a generic naming DSL. Existing rules are too varied; typed callbacks provide better locality.
- Making CLI/WebUI independently derive or validate names.
- Rebuilding tracker payload after user approval.
- Treating every payload `name`/`title` field as the principal release name.

The BHD trace also showed a later unsupported-source failure after preflight. That is an adjacent taxonomy/readiness projection defect. Use the same architectural principle—a pure, versioned, tracker-local policy resolved before preflight—but track it separately so the release-name fix remains reviewable.

## Acceptance criteria

- AR and MTV project their actual tracker upload names before duplicate checking.
- WebUI, CLI, and API display the exact same projected upload name.
- Remote duplicate checks use the displayed search name.
- Upload payload uses the reviewed upload name without re-derivation.
- A missing, stale, invalid, or mismatched tracker name blocks that tracker before submission.
- Other tracker lanes continue.
- Config/questionnaire/name changes invalidate dupes and retained upload preparation.
- All built-in trackers have an explicit family or custom naming policy and version.
- All custom tracker naming algorithms live in `name.go`.
- MTV `upload.go` is reduced to upload orchestration; auth, naming, taxonomy, and payload mapping have dedicated files.
- No CLI-, WebUI-, or API-specific workaround exists.
- Registry-wide parity tests prove projection, dupe query, preview, payload, and submission naming semantics.
