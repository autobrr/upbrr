# BTN Full Handling Implementation Plan

Status: implemented; manual specials, separate folder naming, and rules lacking typed bilateral evidence remain deferred
Prepared: 2026-08-08
Base branch: `refactor/evidence-driven-dupe-checking`
Base PR: [#327](https://github.com/autobrr/upbrr/pull/327)
Implementation branch: `feat/btn-full-handling`
Target PR: new stacked PR from `feat/btn-full-handling` into `refactor/evidence-driven-dupe-checking`
Underlying issue: [#325](https://github.com/autobrr/upbrr/issues/325)

## Objective

Bring BTN search, duplicate evaluation, upload validation, naming, and special-case preparation into line with the saved BTN rules. Fix the current SD-resolution false contradiction first, then add only behavior supported by objective tracker evidence and facts already available to upbrr.

This plan extends the evidence-driven duplicate work in PR #327 through a separate stacked branch and PR. It does not add BTN implementation commits to the base branch, create a second duplicate checker, or create a BTN-only evaluation path.

## Branch and PR Topology

Before implementation:

1. Confirm the working tree contains no unrelated tracked changes.
2. Record the current `refactor/evidence-driven-dupe-checking` HEAD SHA as the stacked base.
3. Create `feat/btn-full-handling` directly from that current HEAD with `git switch -c feat/btn-full-handling`.
4. Make every implementation, test, documentation, and review-fix commit on `feat/btn-full-handling`.
5. Open the BTN PR with base `refactor/evidence-driven-dupe-checking`, not `main` or `develop`, so its diff contains only BTN follow-up work.

The PR body must contain this dependency note near the top:

> **Stacked PR:** This branch was created from and targets `refactor/evidence-driven-dupe-checking`. It depends on #327 and contains only the BTN follow-up changes relative to that base. Review and merge #327 first; then retarget or rebase this PR as needed and verify its effective diff is unchanged.

Do not merge the stacked PR into the base branch merely to obtain a main-based review. After #327 merges, update the stacked PR's base/branch relationship through the normal non-force workflow unless the user explicitly authorizes otherwise.

## Fixed Decisions

- Keep BTN's existing TV-only upbrr scope. BTN may accept some TV-related movies, but movie classification, search filtering, and upload support are deferred.
- Do not count or reject related-movie rows as wrong-work results in this work. They remain outside the supported target scope.
- Preserve the dedicated daily-show search. Daily searches must stay narrowly filtered and one-shot; they must not paginate through thousands of broadly matching episodes. They may report public `complete=true` only when BTN group or TVDB identity binds the work and the one response returns every unique row in its stable reported total; title-fallback daily searches remain incomplete.
- Use BTN's reported `results` count to state whether an ordinary search returned every row. An incomplete search may still compare returned candidates, but it cannot prove absence or safely apply collection-capacity rules.
- Treat `SD` as a coarse resolution family, not as a synonym that permanently erases `480i`, `480p`, `576i`, or `576p`.
- Reuse shared dupe policy, set-capacity, validation-evidence, and questionnaire machinery. Add no BTN-specific framework.
- Automate only objective rules. Subjective quality judgments, staff approval, request state, account-age rules, and seeding-time rules remain manual or advisory.
- Keep tracker responses, auth data, download URLs, cookies, and tokens private. Tests and docs use synthetic release names and IDs.

## Evidence Inventory

The saved HTML is the authority for tracker rules. Hashes freeze the exact inputs reviewed by this plan.

| Evidence | SHA-256 | Rule areas |
| --- | --- | --- |
| `btn/Release Name __ BroadcasTheNet.htm` | `1E51ACEACF2DA47D4795DA388CCEA1787795FDC738C447FAB3691CCAADD9D02A` | allowed characters, scene-name preservation, dates, years, resolution tokens, audio tokens, groups, anime, pack folders |
| `btn/TV Movies and Specials Guideline __ BroadcasTheNet.htm` | `F1173BF2848C267A121D879E01147DFF488F9D37EF518535A539C21D9D9F4E5C` | permitted TV movies/specials, organization, manual metadata, autofill limitation |
| `btn/Upload Rules __ BroadcasTheNet.htm` | `CEFFED934DA279BF7BD3DC04AB86CBC13BBCCA9618AD2C415369E059DF603F6F` | package shape, season packs, format taxonomy, coexistence, trumping, capacities, language, reservations |

Secondary API evidence:

- Existing BTN adapter tests and observed response handling on PR #327.
- Jackett `BroadcasTheNet.cs` at commit `bb6bb1d75ee6481d4f96d51b605cba65fee18196`, used only as an implementation reference for `getTorrents`, offset pagination, a 100-row page size, and daily query shape.
- The operator's successful 150-row test proves BTN accepts at least 150 for that request. It does not by itself prove a stable maximum, ordering contract, or behavior for every filter.

Where sources conflict, a sanitized direct BTN response and saved BTN rules win. Jackett behavior is corroboration, not tracker authority.

## Pre-implementation Findings

This baseline describes behavior before this plan's implementation.

### Duplicate search

- `btn/dupe.go` searches by BTN group ID, then TVDB ID, then title fallback.
- Ordinary searches request 100 rows and paginate by the number of returned rows until BTN's `results` total is reached, a response becomes malformed/inconsistent, a request fails, or the configured page bound is reached.
- Daily searches use category `Episode` plus a date-shaped name and intentionally stop after one request.
- Search completeness is now exposed, but `docs/trackerdata/EVIDENCE.md` still says BTN is capped at 50 and never complete.
- Returned BTN rows currently retain only release name, ID, link, size, resolution, one source/type value, and HDR flags. BTN also supplies useful category, source, codec, container, origin, group, provider IDs, and time fields.
- BTN `Source` is currently stored only as `DupeEntry.Type`; `DupeEntry.Source` is empty. Policy work must first settle and test the field mapping.
- BTN has no tracker-specific `DupePolicy`, so all candidates use generic relations and no BTN capacity rules.

### Resolution root cause

BTN's API can report the structured resolution as `SD` while the release name contains the permitted concrete token `480p` or `576p`. Shared normalization currently compares structured and title values by exact string equality, marks the evidence contradictory, and returns manual/insufficient results before the useful resolution relation is evaluated.

There is a second edge: when BTN reports only coarse `SD` and the title carries no concrete token, comparing that row with a concrete SD target must not prove a different resolution. Coarse overlap is incomplete evidence, not coexistence.

### Upload and naming

- Upload taxonomy already maps supported BTN containers, codecs, sources, origins, countries, and resolutions; concrete 480/576 inputs map to BTN's `SD` form value.
- Validation currently proves only that canonical TV season/episode metadata can construct a BTN payload.
- Shared validation helpers already cover archive/extraneous files, media-only packages, single-file folders, multi-season packages, episode ranges, per-file uniformity, and media constraints.
- Naming code normalizes common release tokens but does not fully distinguish scene preservation from P2P normalization or enforce daily/year/SD/folder rules.
- The normal upload path relies on BTN autofill. Saved BTN guidance says autofill does not handle TV movies and specials, so those cases need an explicit manual preparation path before they can be supported.
- Season-pack reservation and claimed-show checks already exist, but their ordering, completeness, time windows, and evidence ownership need an isolated audit before reuse as blocking rules.

## Rule Classification

| Rule group | Planned handling | Reason |
| --- | --- | --- |
| SD structured/title compatibility | automatic shared fix | objective values with a clear coarse/fine relationship |
| Ordinary search pagination and total count | automatic | direct API contract; required for absence and capacity claims |
| Daily date/category search | automatic one-shot | narrow BTN path avoids unbounded broad pagination |
| Source, resolution, codec, container, category, group, origin mapping | automatic | direct response fields |
| Same-slot and numeric capacity rules | automatic only with complete search and required facts | objective collection rules |
| Scene/P2P, internal, foreign/English, codec, disc-standard coexistence | automatic when both operands are authoritative | objective tracker rules |
| Uniform pack beats mixed pack | manual/waivable | mixed packs can require staff approval; current evidence may be partial |
| Subjective source or encode superiority | manual | no deterministic threshold in saved rules |
| Defects, missing remux tracks, bad captures | manual unless BTN exposes an authoritative flag | requires content inspection or tracker judgment |
| Incomplete season exceptions, requests, age, seeding, staff approval | advisory/manual | account and tracker state are unavailable or mutable |
| TV-related movies and unrelated movies returned in TV searches | deferred | explicit scope decision |

## Implementation Constraints

1. Shared duplicate normalization remains the sole comparison engine.
2. BTN response decoding remains in `internal/trackers/impl/standalone/btn`; only protocol-independent facts cross into `pkg/api.DupeEntry`.
3. A new shared field or dimension is allowed only when a saved BTN rule consumes it and no existing typed fact represents it. BTN release origin is the expected case; untyped `Attributes` must not become a policy back door.
4. Fine resolution detail is retained when known. Global normalization must not collapse all SD values because PAL/NTSC and 480/576 rules need them.
5. Collection-capacity decisions require a complete, work-scoped candidate set. Title fallback and intentionally incomplete daily results cannot prove free capacity.
6. Proven rule violations may block. Missing evidence produces advisory, manual review, or insufficient evidence according to the existing rule contracts.
7. No live BTN call is added to unit tests. Sanitized synthetic fixtures reproduce the verified contract.

## Phase 0: Freeze BTN API and Rule Contracts

### Work

- Record a compact rule ledger from the three saved pages: rule ID, evidence file/hash, required input facts, disposition, and implementation phase.
- Verify direct BTN `getTorrents` behavior with operator-run, non-secret diagnostics:
  - accepted page sizes at 100 and 150;
  - whether any larger value is rejected, clamped, or honored;
  - zero-based offset behavior;
  - meaning and JSON type of `results`;
  - ordering when `Time` is used by reservation checks;
  - field presence and casing for `Category`, `Source`, `Codec`, `Container`, `Resolution`, `Origin`, `GroupName`, provider IDs, and `Time`;
  - exact daily query versus wildcard daily query;
  - behavior when no rows match.
- Save only sanitized shape/count evidence. Replace names, IDs, groups, URLs, hashes, and timestamps with synthetic values while preserving types and pagination structure.
- Extend existing inline `httptest` payloads unless a reusable multi-page payload is materially clearer in `testdata`.
- Define the expected mapping table before changing code. In particular, distinguish BTN category from release source instead of continuing the current ambiguous `Source -> Type` mapping by accident.

### Decisions produced by this phase

- `btnDupePageLimit`: use the largest repeatably verified, unclamped value. Keep 100 if 150 cannot be verified as a contract; pagination already supplies correctness.
- Daily name filter: use the narrowest verified form. Keep one-shot behavior even when the response reports more rows, mark it incomplete, and never silently claim absence.
- Reservation ordering: rely on one page only if BTN proves newest-first ordering for the exact filter; otherwise fetch enough evidence to find the latest matching timestamp.

### Exit criteria

- Every response field consumed later has a synthetic contract test.
- Page-size, total-count, offset, and daily behavior are explicit rather than inferred.
- No secret or real release data is committed.

Suggested commit: `test(btn): freeze search and rule contracts`

## Phase 1: Fix Shared Resolution Semantics

### Work

- Add one resolution-specific comparison helper with three outcomes: exact, overlapping, and disjoint.
- Treat structured `SD` plus a concrete title token of `480i`, `480p`, `576i`, or `576p` as compatible evidence. Prefer the concrete value for comparison while retaining its partial title provenance and the structured coarse evidence.
- Treat coarse `SD` versus a concrete SD value without finer evidence as overlapping, not different. This result cannot establish coexistence or an exact duplicate by resolution alone.
- Keep concrete values distinct from one another. `480p` and `576p` remain different when both are known.
- Keep `SD` versus `720p` or higher disjoint.
- Preserve contradiction handling for genuine conflicts such as structured `1080p` with a title-only `720p` marker.
- Use the helper in both target and candidate structured/title reconciliation and in general/policy resolution comparisons. Do not add a BTN name check to the shared evaluator.

### Tests

- BTN API `SD` plus title `480p` resolves to a usable `480p` fact without contradiction.
- BTN API `SD` plus title `576p` resolves to `576p`.
- Coarse `SD` against concrete `480p` yields insufficient overlap when no finer candidate evidence exists.
- Concrete `480p` against concrete `576p` remains different.
- `SD` against `720p` remains different.
- Structured `1080p` against title `720p` remains contradictory.
- Existing PTP/RTF SD, PAL/NTSC, resolution-slot, and generic evaluator tests remain unchanged and pass.

### Exit criteria

- The reported BTN resolution false-positive path is fixed at the shared root.
- No tracker loses concrete SD distinctions.

Suggested commit: `fix(dupe): reconcile coarse SD resolution evidence`

## Phase 2: Complete BTN Search Evidence Mapping

### Work

- Decode the verified BTN response fields into typed local values rather than repeatedly probing anonymous maps throughout evaluation code.
- Populate existing `DupeEntry` fields for category, source, codec, container, group, resolution, size, ID, link, and flags.
- Set `Internal` from the existing BTN internal-group list when group evidence is present.
- Preserve existing public/private separation: details links may be public; auth-bearing download authority remains private.
- Correct the `Type`/`Source` mapping using the Phase 0 contract. Keep compatibility only where it represents the same semantic fact.
- Add a typed protocol-independent `ReleaseOrigin` fact and `DupeDimensionReleaseOrigin` only if required to express the verified Scene/P2P/Mixed rules. Carry it through `DupeEntry`, candidate normalization, normalized facts, policy lookup, and sanitized evaluation output.
- Retain provider IDs for search/work-scope diagnostics but do not use them to implement related-movie filtering in this plan.
- Keep response-total validation, duplicate-ID handling, inconsistent-total detection, partial-page failure behavior, and page-bound warnings.

### Tests

- One response maps every supported field with exact casing and canonicalization expectations.
- Missing optional fields stay missing rather than being inferred from unrelated fields.
- Internal group detection is case-insensitive and does not mark ordinary groups internal.
- Multi-page totals, short final pages, duplicate IDs, malformed rows, inconsistent totals, and partial failures retain correct completeness.
- A daily response remains one request; a count mismatch reports incomplete evidence.

### Exit criteria

- BTN policy receives the facts BTN actually returned.
- No policy depends on raw response maps or secret attributes.

Suggested commit: `feat(btn): retain duplicate candidate evidence`

## Phase 3: Add an Evidence-Backed BTN Duplicate Policy

### Work

- Add a versioned policy such as `standalone/btn/duplicate/v1` with an evidence ID tied to the saved upload-rules hash.
- Define slot dimensions only from facts verified in Phase 2. Expected base dimensions are source, resolution, codec, container/media kind, release origin, group/internal state, pack scope, and date/episode scope where applicable.
- Express objective coexistence rules declaratively:
  - different concrete resolutions;
  - Scene versus P2P;
  - H.265 WEB versus H.264 WEB;
  - Blu-ray versus DVD;
  - PAL versus NTSC when both standards are known.
- Defer internal versus non-internal, foreign-language versus English, and pilot-versus-pack coexistence until shared typed evidence can prove both operands.
- Express objective directional precedence only where both sides have required facts, including H.264 over DivX/Xvid and any verified format rule with no subjective quality judgment.
- Add separate collection rules for independent capacities rather than merging unlike pools:
  - one Scene season pack per source/resolution family;
  - two P2P season packs per source/resolution family;
  - WEB capacity two at 720p/1080p and one at SD/2160p, subject to verified provider/source semantics.
- Require complete work-scoped search evidence for every capacity decision. If provider/service identity needed for a WEB rule is missing, return manual or insufficient evidence rather than treating all WEB releases as the same provider.
- Add manual-review rules for mixed packs, source superiority, encode quality, incomplete remuxes, and any staff-owned exception that can be identified but not decided.
- Preserve general exact-content and pack-containment logic. Multi-file matching remains set-aware; one episode file must not block a season pack.

### Tests

- Table tests cover both directions of every coexistence and precedence rule.
- Set-capacity tests cover below-capacity, at-capacity, incomplete search, order independence, Scene/P2P separation, and WEB resolution overrides.
- Missing origin/provider/language facts cannot accidentally prove coexistence or capacity.
- Daily episodes compare by exact date and do not collide with unrelated dates.
- Season packs are not blocked by a single matching episode; unrelated extra files do not turn two different releases into full duplicates.

### Exit criteria

- Every automatic BTN relation cites a saved evidence ID.
- Subjective rules cannot block without a manual decision.
- Policy registration and clone/validation tests pass.

Suggested commit: `feat(btn): add evidence-backed duplicate policy`

## Phase 4: Enforce Objective Upload Package and Media Rules

### Work

- Expand `validationPolicy()` by composing existing shared validators.
- Block proven archives, samples, extra NFO/subtitle/image files, and other non-media package entries where the saved rule forbids them.
- Block a proven single-file folder when the release should be uploaded as the media file itself.
- Block a proven multi-season package.
- Validate supported container, source, codec, disc, and resolution combinations from the saved taxonomy.
- Block objectively forbidden cases such as DVD remux and a verified Scene DVD image case.
- Evaluate episode-range completeness only when local episode facts are complete. Missing episode evidence is advisory, not a fabricated incomplete-season failure.
- Evaluate per-file source/resolution/origin/codec uniformity only when per-file facts exist. Because mixed packs may be staff-approved, proven mixtures produce a waivable/manual result, not an unconditional strict block.
- Represent incomplete-season, request, age, seeding, and staff-approval requirements as advisory/manual messages unless a future authenticated tracker contract supplies authoritative state.

### Tests

- One table covers each strict, waivable, advisory, and missing-evidence path.
- A clean single episode and a clean complete season pack pass.
- Synthetic archive, extra-file, single-file-folder, multi-season, unsupported-codec, DVD-remux, and mixed-pack cases return stable rule IDs and intended dispositions.
- Partial metadata never becomes a strict false positive.

### Exit criteria

- Machine-verifiable BTN upload rules fail before network preparation.
- Staff-only rules remain visible without being falsely automated.

Suggested commit: `feat(btn): validate objective upload rules`

## Phase 5: Align Release Names, Folder Names, and Specials Preparation

### Work

- Split naming behavior by evidence already present in the prepared subject:
  - preserve verified Scene/pre names and group capitalization;
  - normalize P2P names through the existing BTN naming path;
  - require the exact `YYYY.MM.DD` token for daily episodes;
  - omit year unless an objective exception applies;
  - allow only the saved SD concrete tokens and never emit literal `SD` in a release name;
  - preserve required resolution/audio/group suffix behavior;
  - use `BTN` for proven mixed-group packs and existing group/NOGRP rules otherwise.
- Validate allowed characters and extension-free release/folder names without renaming source media files.
- Keep anime's documented single-episode exception narrow and evidence-gated.
- Add explicit manual TV-movie/special preparation only after a UI/CLI-neutral questionnaire contract can supply the required BTN series/network organization fields.
- For the manual path, bypass autofill only when all required fields are supplied and validated. Never invent a BTN series, network, season, or staff approval.
- Keep general movie uploads unsupported; this phase covers only the saved BTN special-case organization model.
- Bump the naming-policy version only when output semantics change and update projection tests.

### Tests

- Synthetic Scene, P2P, daily, SD, mixed-group pack, anime single-episode, and invalid-character examples.
- CLI and WebUI consume the same required questionnaire fields and produce the same prepared payload.
- Missing manual special fields fail before submission with field-specific requirements.
- Ordinary TV upload behavior remains unchanged.

### Exit criteria

- Generated BTN names and folders follow saved objective rules.
- Specials do not enter a broken autofill path.
- Related general movies remain explicitly unsupported.

Suggested commits:

- `fix(btn): align release naming rules`
- `feat(btn): support manual special preparation`

The second commit is omitted if current shared questionnaire contracts cannot represent the required fields without a broad UI/API expansion; document the remaining limitation instead.

## Phase 6: Audit Reservation, Claims, and Shared API Use

### Work

- Verify the two-hour internal season-pack reservation against the saved rule and Phase 0 ordering evidence.
- Make the reservation decision from the newest complete matching evidence. Do not assume the first 50 rows contain the newest result unless BTN's ordering contract proves it.
- Audit the existing claimed-show forum cache separately from duplicate search. Preserve its current 48-hour/grace behavior unless direct BTN evidence supports a change.
- Ensure claim and reservation failures distinguish unavailable evidence from a proven block and use stable, non-secret logs.
- Consolidate request/response decoding only where search, exact tracker-ID lookup, and reservation checks truly share the same contract. Avoid refactoring upload auth or HTML submission merely for uniformity.

### Tests

- Reservation boundary tests immediately before, at, and after expiry.
- Multiple returned packs in unsorted fixture order still select the correct timestamp when the API does not guarantee order.
- Missing/malformed `Time`, partial search, request failure, and no-match cases have explicit outcomes.
- Claim-cache tests retain current expiry and failure behavior.

### Exit criteria

- Time-based blocks are supported by complete, correctly ordered evidence.
- Search helpers share only proven common behavior.

Suggested commit: `fix(btn): harden reservation evidence`

## Phase 7: Documentation, Diagnostics, and Full Validation

### Work

- Update `docs/trackerdata/EVIDENCE.md` so BTN describes the verified page size, pagination, daily exception, total-count completeness, and work scopes. Remove the stale 50-result statement.
- Add the BTN policy evidence ID and rule ledger to tracker docs.
- Update profile/responsibility tests for naming, validation, duplicate policy, auth, and preparation ownership.
- Add concise debug logs for search mode, pages, reported total, returned unique rows, completeness, and decision. Never log filters containing titles/IDs at info level and never log credentials or raw payloads.
- Run formatting before final checks so generated formatting changes are included in the intended commit.

### Required checks

```powershell
go test -race -v -timeout 20m ./internal/trackers/dupe ./internal/trackers/impl/standalone/btn ./internal/trackers/impl/standalone ./internal/trackers
make fmt-go
make gofix-check-changed
make logpolicy
make lint
make test-go
make backend
git diff --check
```

Run `make pathpolicy` only if path handling changes. Run relevant CLI/WebUI/API checks if the manual-special questionnaire changes shared request/response contracts.

### Exit criteria

- Focused and repository-wide checks pass.
- Docs match implemented behavior.
- No generated/local output is staged.

Suggested commit: `docs(btn): record verified tracker behavior`

## Phase 8: PR Delivery and CodeRabbit Review Loop

### Initial delivery

1. Keep each phase in the scoped commits listed above; do not mix review-only changes into implementation commits.
2. Create all planned local commits and run the complete validation set before pushing.
3. Push all completed implementation commits together from `feat/btn-full-handling`; do not push BTN commits to `refactor/evidence-driven-dupe-checking`.
4. Create an open, ready stacked PR whose head is `feat/btn-full-handling` and whose base is `refactor/evidence-driven-dupe-checking`.
5. Put the required stacked-dependency note near the top of the PR body. Also link this plan, issue #325, base PR #327, saved BTN evidence, the resolution-root fix, search-completeness behavior, explicit movie deferral, validation results, and remaining manual rules.
6. Re-read PR state with `gh` and confirm the head branch/SHA, base branch/SHA, effective diff, checks, and ready/open status.

### CodeRabbit handling

Because the stacked PR does not target `main`, do not assume CodeRabbit's automatic review has started.

1. After opening the PR, wait for CodeRabbit to post or update its PR comment with the explicit `Reviews paused` state. Do not post a review command before that state is visible.
2. Once `Reviews paused` is visible, add a new PR comment containing exactly `@coderabbitai review` to trigger one review.
3. Re-read the PR and confirm CodeRabbit acknowledged or began processing the current head SHA. A pause comment alone and a queued/in-progress check are not review completion.
4. Wait until CodeRabbit finishes the triggered review for that head SHA.
5. Fetch all review comments and unresolved threads with `gh`, including nitpicks, and read surrounding code before classifying each comment.
6. For every comment, choose one outcome:
   - actionable: fix the root cause in a narrowly scoped follow-up commit;
   - already fixed: reply with the exact commit link and short proof;
   - not applicable: reply with the precise contract, evidence, or scope reason;
   - deferred: reply with the explicit non-goal and tracking location.
7. Keep CodeRabbit-driven fixes grouped by concern, for example resolution semantics, BTN response mapping, validation, or docs. Do not create one commit per wording nit when several comments share one root cause.
8. Create and validate all follow-up commits locally, then push the complete follow-up batch once.
9. Reply directly to each specific CodeRabbit thread after its commit is on GitHub. Include a linked commit for code changes; include test evidence or source evidence when rejecting a finding.
10. Resolve a thread only after the reply and GitHub readback show the intended commit/comment. Do not resolve human-owned threads without an appropriate response.
11. After a follow-up push, inspect CodeRabbit state again. If it reports `Reviews paused` for the new head, comment exactly `@coderabbitai review` once, confirm processing, and wait for completion. Do not spam duplicate triggers while a review is running.
12. Repeat the review, response, scoped-fix, single-push, paused-state, manual-trigger, and rereview loop until no actionable CodeRabbit feedback remains and every required check passes.

### Base-PR preflight

Before creating the stacked PR, re-audit unresolved threads and checks on base PR #327. Earlier comments about the singular result row, public `WorkScope`/`WrongWorkCount`, bounded BTN completion, and contract-table coverage may be stale after later commits. Address base-specific comments on #327; address BTN child-branch comments on the stacked PR. Link between PRs when one commit supplies evidence for the other, but do not reply on the wrong PR merely to clear a thread.

### Exit criteria

- Base PR #327 remains unchanged by BTN commits and is open, ready, and green.
- The stacked BTN PR targets `refactor/evidence-driven-dupe-checking`, contains the dependency note, and exposes only the intended child diff.
- CodeRabbit's paused state was observed before each required `@coderabbitai review` trigger.
- Every CodeRabbit comment has a direct, evidence-based response.
- No actionable CodeRabbit thread remains unresolved.
- Review-driven changes are traceable to scoped commits.

## End-to-End Test Matrix

| Scenario | Expected result |
| --- | --- |
| BTN ID ordinary search, one page, total reached | complete authoritative work scope |
| TVDB ordinary search, multiple pages, total reached | complete provider-ID work scope |
| Title fallback, all rows fetched | complete transport but title-fallback work scope cannot prove wrong-work absence |
| Partial page failure or inconsistent total | returned rows evaluated; search incomplete |
| Daily exact date query | one request; returned rows evaluated; no broad pagination |
| Daily count exceeds returned rows | incomplete warning; no absence/capacity claim |
| Candidate structured `SD`, title `480p` | concrete compatible resolution; no contradiction |
| Candidate structured `SD`, no fine title, target `480p` | overlap/insufficient evidence, not automatic coexistence |
| Target `480p`, candidate `576p` | different concrete resolution according to BTN policy |
| Season pack versus one matching episode file | episode evidence does not block whole pack |
| Two different releases share one unrelated extra file | no full-release duplicate |
| Complete Scene/P2P capacity set | correct independent capacity decision |
| Incomplete capacity search | manual/insufficient; never claims free slot |
| Proven archive/extra file/multi-season package | stable blocking validation result |
| Proven mixed pack | waivable/manual staff result |
| Missing per-file metadata | advisory/insufficient; no strict false block |
| TV-related movie | unsupported/deferred with existing TV-only scope unchanged |

## Definition of Done

- BTN SD evidence no longer produces false contradictions or false resolution coexistence.
- Ordinary BTN search completion is based on verified totals and pagination; daily search remains narrow and one-shot and is public-complete only with BTN-group/TVDB work binding plus a count-matching response.
- BTN response fields needed by rules are retained as typed facts.
- Objective BTN duplicate, capacity, upload, and naming rules are implemented with evidence IDs and tests.
- Subjective or unavailable rules produce manual/advisory outcomes.
- `EVIDENCE.md`, policy/profile tests, focused tests, full Go tests, lint, log policy, build, and diff checks pass.
- Base PR #327 remains the explicit dependency, and the stacked BTN PR records that relationship in its body.
- The stacked BTN PR has completed manually triggered CodeRabbit review, direct thread replies, no actionable unresolved feedback, and green CI.

## Explicitly Deferred

- General BTN movie uploads and classification of TV-related movies.
- Wrong-work filtering of related-movie rows returned by BTN TV searches.
- Automated staff approval, request eligibility, account-age, seeding-time, subjective quality, source-superiority, or defect judgments.
- New shared UI/API machinery solely for BTN specials; add it only if the existing questionnaire contract cannot carry the required manual fields and the user approves that broader scope.
- Tracker API abstractions not required by at least two current BTN callers.
