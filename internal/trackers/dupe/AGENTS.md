# Duplicate Checking Guidelines

Scoped rules for the shared duplicate-search coordinator and evaluator. Root and `internal/AGENTS.md` rules still apply.

## Contract

- Keep protocol search, normalization, policy, evaluation, and presentation separate. Adapters return evidence; this package normalizes and evaluates it; tracker definitions own tracker policy.
- Compare candidates for the same work and overlapping content scope. Exact release-name or primary-video identity blocks before slot inference.
- Existing `in_client` evidence always hard-blocks that tracker. Never override, waive, reclassify, or bypass it, including in debug mode.
- Treat known differences as distinct release variants, not potential duplicates:
  - different content scope, date, season, or episode;
  - different resolution;
  - different normalized media type or class. Full disc, remux, encode, WEB, and broadcast are distinct. Remux versus non-remux is bidirectional. DVD and BDMV must not block each other when both types are authoritative;
  - different evidenced HDR slot, edition/cut, region, or 2D/3D presentation.
- A distinct candidate resolves to `coexists`: it must not block, count as actionable, or appear in the user-facing potential-duplicate list. Sanitized diagnostic/fingerprint evidence may retain the `coexists` evaluation.
- Missing or partial facts never prove equality or coexistence. Use `insufficient_evidence`; contradictory facts require `manual_review`. Unresolved overlapping candidates fall back to actionable `same_slot`.
- After authoritative same-work and same-season binding, a season pack always trumps individual episodes. An existing pack blocks a proposed episode; individual episodes are irrelevant to a proposed pack and must be discarded as potential duplicates.

## Evidence

- Prefer structured tracker/API or prepared-media facts. Title parsing is fallback evidence; preserve its partial provenance and detect contradictions.
- Search completeness requires both exhausted enumeration and authoritative provider-ID or tracker-group work binding. Title searches remain incomplete.
- Never infer quality, trumpability, capacity, or slot membership from release name or size alone.
- Keep raw tracker labels out of general comparisons. Normalize them into canonical facts; use tracker `type` or `media_kind` slot dimensions where their semantics are evidenced.

## HDR

- Do not collapse HDR to one boolean. Preserve `sdr`, `dolby_vision`, `hdr10`, `hdr10_plus`, `hlg`, `pq10`, `hdr_vivid`, and `wide_color_gamut` independently, including Dolby Vision profile and fallback formats.
- At 2160p, general policy gives SDR and DV-only distinct slots. Plain HDR and DV+HDR share one compatibility slot: DV+HDR trumps plain HDR in either direction.
- Apply general HDR coexistence or trumping only when both releases have complete, authoritative 2160p resolution and HDR evidence.
- At 1080p, general policy ignores HDR type; SDR, HDR, DV, and DV+HDR do not form distinct slots unless specific tracker rule/slot evidence allows it.
- General policy creates no HDR-based coexistence slots at other resolutions and does not split generic HDR formats or Dolby Vision profiles into additional slots.
- Finer distinctions such as HDR10 versus HDR10+, HLG, PQ10, HDR Vivid, WCG, Dolby Vision profiles, or directional compatibility beyond DV+HDR trumping plain HDR require specific tracker rule/slot evidence.
- Missing, partial, or contradictory HDR evidence must fail closed according to `HDRPartialMode`; never manufacture SDR from an absent field unless the adapter contract proves absence is authoritative.

## Tracker Overlays

- Put evidence-backed slot, precedence, size, and set-capacity rules in the tracker definition/profile or tracker-local `dupe_policy.go`. Do not add tracker-name branches to the shared evaluator.
- Read `docs/trackerdata/CONTEXT.md` and the relevant saved tracker snapshot before changing tracker-specific duplicate behavior.
- A tracker overlay may suppress a general coexistence axis only when its rules fully replace that axis. Missing overlay evidence must not erase a valid general finding.
- Subjective, staff-discretion, age/seed, screenshot, approval, and unstructured trump rules remain manual.

## Ownership

- `candidate.go`: adapter-entry normalization and candidate enrichment.
- `normalize.go`: canonical facts, provenance, media classification, and content scope.
- `findings.go`: general and tracker per-candidate rules and precedence.
- `set.go`: complete-collection capacity rules.
- `evaluator.go`: pure aggregation and sanitized candidate projections.
- `service.go`: bounded orchestration, result projection, and redacted logs.
- `assessment*.go`: immutable, fingerprint-bound decisions and authorization.

## Checks

Keep one focused regression for every rule boundary, both directions where directional. Run:

```powershell
go test -race -v -timeout 20m ./internal/trackers/dupe
```

Add the touched tracker package when changing an overlay or adapter. Use only synthetic release names and provider IDs in tests.
