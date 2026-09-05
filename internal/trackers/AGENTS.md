# Tracker Guidelines

Scope: `internal/trackers/` and changes elsewhere to tracker definitions, registration, auth, naming, validation, or duplicate contracts. Root and `internal/AGENTS.md` rules apply.

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
- Standard Unit3D addition: site profile, optional rules/validation, one registry entry, one example-config stanza without `url`, combined rule/validation cases. Never infer configured custom trackers; unsupported saved entries remain inert and preserve unknown non-URL fields.

## Checks

Follow the root and backend check selection. Test the implementation and touched shared tracker packages; include config/default/catalog checks when definitions or auth material change. Duplicate behavior also follows `internal/trackers/dupe/AGENTS.md`.
