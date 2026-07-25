# Release Workflow Centralization and Exact-Asset Plan

Status: implementation in progress  
Date: 2026-07-23  
Scope: CLI, `internal/releaseworkflow`, core workflow builders, tracker/image-host/client integrations, HTTP API, WebUI release session, generated workflow contracts, and embedded CLI/Web E2E.

Implementation snapshot (2026-07-23):

- Central `ContinueReleaseWorkflow` planning now owns CLI, WebUI, app HTTP, and public API orchestration.
- Exact media/description/upload lineage, durable intents/events/effect fences/work leases, private artifact codecs, and checkpoint-safe operation restart recovery are implemented.
- Stage-specific app and public API orchestration routes are retired; generated OpenAPI and TypeScript expose the continuation seam plus artifact/operation mutations.
- Remaining: replace tracker adapter `submit` closures with durable tracker-scoped exact request capsules and restart rehydration. Until then, reviewed upload execution authority intentionally fails closed after restart instead of rebuilding or blindly replaying a tracker request.

## Executive decision

Deepen `internal/releaseworkflow` into the only owner of workflow ordering, continuation policy, per-tracker isolation, exact asset lineage, operation outcome reduction, and recovery semantics.

Expose a compact continuation seam to CLI and HTTP/WebUI:

```go
type Application interface {
	Continue(context.Context, OwnerID, ContinueRequest) (Continuation, error)
	Current(context.Context, OwnerID, api.WorkflowID) (Continuation, error)
	Cancel(context.Context, OwnerID, OperationRef) (Continuation, error)
}
```

Media staging/streaming remains a separate private-artifact seam. Callers submit desired goals and answers; they do not choose or sequence internal stages.

Adopt this incrementally:

1. Lock current failures into shared characterization tests.
2. Fix unsafe CLI idempotency and operation completion contracts immediately.
3. Add canonical continuation/tracker-lane projections and central transition policy.
4. Move all required image hosting into one exact media-preparation step.
5. Route CLI, HTTP, and WebUI through the same `Continue` planner.
6. Preserve one exact private torrent/prepared operation per tracker through dry run and upload.
7. Add durable scoped events, effect fencing, and private artifact recovery after shared behavior is stable.

This gives near-term correctness without making the incident fix depend on a full event-sourced rewrite. The later durability work remains compatible with the chosen seam.

## Required product behavior

- CLI, API, and WebUI must reach identical workflow decisions for identical intent and retained state.
- Adapters may render, prompt for advertised actions, upload/stage user-provided artifacts, cancel, and poll. They must not encode stage order or decide whether a later stage is legal from raw statuses.
- One tracker failure at any tracker-local stage must not block unrelated runnable trackers.
- A required dry-run client-injection failure is a failure for that tracker. It must prevent that tracker from reporting dry-run success, but must not prevent other trackers from completing their dry runs.
- Every tracker must consume only its own exact prepared torrent artifact. Reusable generic torrent content may be copied into tracker-scoped artifacts where policy permits; tracker-unique torrents and sensitive announce data must never cross tracker scopes.
- Selected media is the only media eligible for hosting, description generation, dry run, or upload. Default capture selection means all captured artifacts selected unless explicit user selection says otherwise.
- Required image hosts must be derived from eligible tracker requirements. All independent targets start concurrently. Fallback is reconciled once and published before description generation.
- Description generation, dry-run planning, and upload-plan preparation must consume retained exact assets. They must not perform hidden image uploads.
- Operation lifecycle and business outcome are separate. A running root may contain a failed tracker item, but that item must be emitted/logged as failed at its own scope.
- `blocked` means required caller action with no runnable work at that scope. It must not mean merely “some tracker failed.”
- `--unattended` never prompts. `--unattended_confirm` may prompt only for typed actions allowed by policy.
- Public workflow state, logs, generated contracts, and review projections must not expose source-local private paths, torrent bytes, passkeys, cookies, API tokens, or secret tracker payloads.

## Incident findings

### 1. CLI “operation complete” followed by “no canonical release”

This is an idempotency receipt collision, not a failed preparation.

The committed CLI baseline generated process-global deterministic keys such as `cli-create-1` and `cli-prepare-2`. `CreateWorkflowCommand` fingerprints only fact instructions (`internal/releaseworkflow/contracts.go:395`), so unrelated runs with identical instructions can reuse the same workflow. `PrepareReleaseCommand` fingerprints only `PrepareInput`, omitting workflow authority/revision (`internal/releaseworkflow/contracts.go:427`).

On a later process:

1. Create reuses the old workflow.
2. Restart recovery sees process-local private authority is gone and clears the current release/downstream refs (`internal/releaseworkflow/module.go:1402`, especially `:1434-1468`).
3. `Start` looks up a durable operation receipt before loading or validating current workflow revision (`internal/releaseworkflow/module.go:414-445`).
4. The repeated prepare key and same input fingerprint return the old terminal operation without executing preparation.
5. CLI sees terminal completion, loads current state, then finds no release (`cmd/upbrr/release_workflow.go:100-131`, `:241-242`).

The current worktree contains a pending CLI run-randomization change. Keep it as the tactical fix, but do not treat it as sufficient. It lacks a persistent/restart reproduction and the module can still report a replayed terminal operation whose promised result is no longer present.

Required systemic corrections:

- Generate one cryptographically unique invocation ID per new CLI run; derive stable per-intent keys within that run.
- Centralize command receipt fingerprints over one canonical envelope: command kind, workflow ID, expected revision, goal/operation, and normalized payload. Individual command implementations must not be able to omit authority accidentally.
- Retain an explicit result contract on terminal operations: result revision plus the exact output ref/type expected from that operation.
- On receipt replay, validate that the original result still exists and has valid lineage. Return a typed stale-result/reprepare disposition when it does not; never return an unqualified “complete.”
- After polling a terminal operation, adapters consume the canonical `Continuation`; they do not separately infer success from a nullable stage pointer.

### 2. Required hosts upload late, repeat, and become invisible

Media capture already has multi-target concurrent upload/fallback logic in `internal/core/media.go:265-507`, and workflow media retains public/private artifacts in `internal/core/workflow_media.go`. Ownership is nevertheless split:

- Upload Images requires one explicit host in `UploadMediaImagesCommand` and in the WebUI page.
- CLI does not run that command before descriptions.
- Tracker preparation performs hidden rehosting during description generation, dry-run preparation, and retained upload-plan preparation.
- Description input freezes exact screenshots/uploads before tracker preflight (`internal/core/workflow_description.go:130-147`).
- `resolveExactMedia` creates a non-nil empty uploaded-image slice (`internal/core/workflow_description.go:286-351`).
- Tracker reload treats non-nil exact uploads as authoritative and skips fresh repository rows (`internal/trackers/description_assets.go:768-807`).

The supplied trace demonstrates the consequence: two hosts start concurrently; one times out; fallback uploads all selected images successfully; several trackers immediately fail to see those persisted variants; another tracker uploads the same fallback again; later trackers then succeed.

Root issue: one operation both mutates hosted-image state and consumes an earlier immutable-looking copy of that state. Nil/non-nil collection shape is also being used as an implicit discovery policy.

Required correction:

- Introduce one workflow-owned image-requirement/preparation step between media selection and descriptions.
- Compute requirements from exact eligible tracker projections.
- Upload all independent host/scope batches concurrently, with deterministic fallback grouping.
- Publish a new exact `MediaArtifactSet` revision containing every successful hosted variant and per-host/per-tracker failures.
- Generate descriptions only from that exact revision.
- Remove repository fallback and image-host mutation from tracker description/preparation paths.
- Remove the required host selector and tracker-intent controls from Upload Images. An optional explicit “add host copy” action may remain as a separate media mutation.

### 3. Aggregate status is being used as transition policy

`DescriptionSet` becomes `blocked` when any tracker fails, even when other descriptions succeeded (`internal/core/workflow_description.go:275-282`). Backend upload planning intentionally accepts terminal `completed`, `skipped`, `blocked`, and `failed` description sets (`internal/releaseworkflow/module.go:3695`, `:3746-3754`) and isolates unavailable trackers in `internal/core/workflow_upload_plan.go:164-205`.

Adapters drifted:

- CLI permits only `completed`/`skipped` and aborts (`cmd/upbrr/release_workflow.go:539-547`).
- WebUI added its own broader predicate (`webui/src/releaseSession/index.tsx:186`) and its own route-access graph (`:132`).
- Backend contains a separate copy of the description predicate.
- `ReleaseWorkflowCurrent` exposes snapshots and operation state but no canonical allowed intents or continuation (`pkg/api/workflow_contracts.go:586-604`).

This proves raw stage status is not a safe caller interface. Transition legality must be projected and enforced once by the backend.

### 4. Tracker-local isolation exists low in the stack but is lost above it

`workflowUploadPlanBuilder` and retained tracker preparation already preserve per-tracker slots/failures and continue eligible trackers (`internal/core/workflow_upload_plan.go`, `internal/trackers/service_upload_plan.go`). Aggregate workflow/status handling and adapter gates stop that behavior from reaching users.

Required correction:

- Make tracker lanes authoritative through projection, preflight, duplicate decision, asset readiness, description, preparation, dry run, submission, and client injection.
- Aggregate outcome is derived only for presentation/exit behavior. It never suppresses a runnable lane.
- A tracker retry targets only failed/recoverable lanes using exact prior refs.

### 5. Dry-run and exact torrent semantics

Recent centralized torrent work in `PrepareRetainedUploadPlan` is the correct foundation and must be preserved.

Required invariants:

- Each tracker lane owns a tracker-scoped prepared operation and torrent artifact ref.
- Dry-run review, dry-run client injection, actual tracker submission, and post-upload client injection use the same semantic prepared operation/torrent fingerprint.
- No lookup by filename, tracker field name, or “latest torrent” may select an artifact.
- A generic torrent may be materialized as separate tracker-scoped copies only when tracker policy declares it semantically reusable.
- A tracker requiring unique/sensitive metainfo always receives a unique private artifact.
- `NoSeed=false`: missing prepared torrent, missing client, or injection error makes that tracker dry run fail. Other lanes continue. Aggregate is `partial` when at least one lane succeeds, `failed` when none do.
- `NoSeed=true`: injection is explicitly `skipped`, never silently treated as attempted success.
- Dry run never submits to a tracker.

### 6. Progress and logging have no canonical scope/severity

Workflow progress updates mutate root phase/message/counts while child item status is retained separately (`internal/releaseworkflow/module.go:875-920`). CLI logging chooses level from the root operation status. A failed image-host item under a running root can therefore log “Host batch upload failed” at INFO, while lower layers log the same recoverable incident at ERROR and WARN.

Required correction:

- Emit canonical structured events with `scope`, `scopeId`, `phase`, `state`, `outcome`, `severity`, stable failure code, recovery, and redacted message.
- Adapters render event severity; they do not derive it from root status or message text.
- Recoverable primary-host failure followed by fallback success: failed host attempt is WARN; reconciled host requirement is successful/INFO; affected trackers are not failed.
- Exhausted host requirement for one tracker: tracker-scoped ERROR/WARN according to recovery policy; unrelated trackers continue.
- Root ERROR is reserved for workflow-scoped failure/no viable lane/invariant failure.

## Selected module design

### External seam

```go
type ContinueRequest struct {
	Authority      *WorkflowAuthority // nil only when beginning
	IdempotencyKey string
	Goal           WorkflowGoal
	Intent         WorkflowIntent
	Answers        []ActionResolution
	Approval       *UploadApproval
}

type WorkflowAuthority struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
}

type WorkflowGoal string

const (
	GoalPrepared          WorkflowGoal = "prepared"
	GoalTrackersAssessed  WorkflowGoal = "trackers_assessed"
	GoalDuplicatesDecided WorkflowGoal = "duplicates_decided"
	GoalMediaReady        WorkflowGoal = "media_ready"
	GoalDescriptionsReady WorkflowGoal = "descriptions_ready"
	GoalUploadReviewed    WorkflowGoal = "upload_reviewed"
	GoalDryRun            WorkflowGoal = "dry_run"
	GoalUploaded          WorkflowGoal = "uploaded"
)

type Continuation struct {
	Current         api.ReleaseWorkflowCurrent
	Lifecycle       OperationLifecycle // queued/running/waiting/terminal
	Disposition     WorkflowDisposition // none/succeeded/partial/failed/canceled
	TrackerOutcomes []TrackerLaneOutcome
	RequiredActions []api.RequiredAction
	AvailableGoals  []GoalAvailability
	Events          []WorkflowEvent
}
```

`WorkflowIntent` is a typed desired-state contract, not a low-level “run next stage” command. It contains preparation input, tracker selection/projection answers, duplicate policy/decisions, media selection/capture policy, description instructions/overrides, interaction policy, upload options, and exact approval when applicable.

Existing stage commands remain private compatibility implementation during migration. Do not expose a generic `json.RawMessage` command envelope initially; retain generated typed Go/TypeScript contracts.

### Core invariants

1. Module alone owns stage ordering and continuation.
2. One accepted operation per workflow at a time.
3. Acceptance, idempotency receipt, exact request fingerprint, and queued operation commit atomically.
4. Same idempotency key plus same canonical envelope returns the original receipt. Same key plus different envelope is a conflict.
5. Every product references exact upstream refs/fingerprints; no worker resolves ambient “current/latest” data after dispatch.
6. Concurrent work returns scoped result patches. It does not replace a whole aggregate from a stale copy.
7. Tracker lanes continue independently until lane failure, required action, explicit exclusion, cancellation, or requested goal.
8. Root lifecycle describes execution; disposition describes business result.
9. `partial` is terminal presentation state, never an execution gate.
10. Required actions carry exact revision/dependency tokens. Stale answers are rejected.
11. Module never prompts. Interaction mode controls which typed actions an adapter may answer.
12. External side effects are invoked only after durable attempt intent exists once durability phase lands.
13. Remote unknown submission outcome is never blindly retried.
14. Public/private separation is owner/workflow/generation/tracker scoped.

### Internal ordering

1. Validate owner, authority, idempotency envelope, intent, interaction policy, and requested goal.
2. Persist acceptance and schedule hidden work.
3. Prepare exact release generation.
4. Resolve required release candidate/playlist action.
5. Project and preflight tracker lanes.
6. Check duplicates and apply exact decisions.
7. Capture media and publish explicit selection.
8. Derive tracker image requirements; upload/reconcile host variants; publish exact media revision.
9. Generate descriptions from the exact media revision.
10. Prepare one sealed tracker operation and torrent artifact per eligible lane.
11. For dry run, perform required client injection per ready lane.
12. For upload, require approval bound to exact upload-plan fingerprint, submit ready lanes, then inject each successful lane’s exact torrent.
13. Reduce tracker outcomes to continuation disposition and emit terminal events.

### Interface error boundary

Returned Go/transport errors mean no trustworthy accepted result:

- malformed/unsupported intent;
- owner/not-found;
- revision/idempotency/active-operation conflict;
- stale action/approval;
- durable repository failure before acceptance;
- invariant breach preventing a safe retained outcome.

After acceptance, expected external/business failures are retained tracker/workflow outcomes and events, not transport errors.

## Module and dependency boundaries

| Concern | Classification | Placement |
|---|---|---|
| Ordering, transition policy, invalidation, lineage, reducers | In-process | Hidden in `internal/releaseworkflow`; no port |
| Prepared release/core computation | Local-substitutable | Internal seam; tested with exact-generation implementation |
| SQLite aggregate/operation store | Local-substitutable | Real temp SQLite in seam tests; production SQLite adapter |
| Private media/prepared operations/torrents | Local-substitutable | Owner/workflow/tracker artifact vault; temp filesystem in tests |
| Image hosts | True external | Narrow `ImageHostPort`; production and scripted adapters |
| Tracker auth/dupe/prepare/submit/reconcile | True external | Narrow `TrackerPort`; registry production and scripted adapters |
| Torrent clients | True external | Narrow `ClientPort`; production and scripted adapters |
| CLI | Inbound adapter | Flags/input/action rendering only |
| HTTP | Inbound adapter | Auth, typed request mapping, result/status mapping only |
| WebUI | HTTP consumer | View rendering, local form state, typed action submission only |

Keep current builder interfaces private during migration. Do not create ports for in-process workflow computations merely to mock them. Prefer seam tests through `Application` using real temp SQLite/filesystem and mocks only for true external services.

## Design alternatives considered

Four independent designs were compared.

| Design | Strength | Cost/risk | Decision |
|---|---|---|---|
| Minimal `Continue/Current/Cancel` | Small caller surface; typed goals; can wrap current module incrementally | Planner becomes internally complex | Selected |
| Caller-simple `Begin/Answer/View/Cancel` | Almost no adapter knowledge; strong decision model | Requires broader immediate API/UI rewrite | Fold `Answer` into `Continue`; preserve as target behavior |
| Flexible `Submit/Observe` + versioned generic intents | Extensible operation registry; strong future compatibility | Weakens transport static typing; large journal/schema investment | Defer generic envelope; keep typed contracts |
| Port/durable scheduler design | Best crash recovery/effect fencing; clean external adapters | Largest migration; delays current correctness fixes | Adopt ports now where external; defer journal/vault to durability phase |

The selected hybrid preserves the deep-module benefits: deleting `internal/releaseworkflow` would force ordering, tracker isolation, exact lineage, recovery decisions, and outcome reduction back into every adapter.

## Phased implementation

### Phase 0 — Characterization and safety rails

Objective: freeze the reported behavior before changing contracts.

Implementation:

- Add a persistent SQLite/restart reproduction for deterministic CLI creation/prepare keys. Cover same source and different source.
- Add module reproduction where a terminal operation receipt exists but restart recovery removed its promised release ref.
- Add exact-media reproduction: non-nil empty exact uploads, primary host failure, successful fallback persistence, immediate tracker resolution.
- Add concurrent host reproduction proving all tracker-required hosts start before any completes.
- Add selected-media reproduction proving default capture selects every artifact and explicit deselection excludes it everywhere.
- Add mixed tracker reproduction: Tracker A description failure, Tracker B success; Tracker B reaches dry run and upload planning.
- Add dry-run reproduction: Tracker A injection failure, Tracker B success; no tracker submit calls; aggregate partial.
- Freeze exact per-tracker torrent tests, including semantically generic copies and unique sensitive variants.
- Add root/item logging reproduction for failed item under running root.
- Add CLI/Web HTTP parity fixture using synthetic release/tracker names.

Exit criteria:

- Every named incident fails for the expected reason before implementation and passes only with the intended change.
- Tests use synthetic data and local fake external services.
- No source behavior changed in this phase.

Rollback: tests only; no runtime migration.

### Phase 1 — Immediate idempotency and truthful operation completion

Objective: eliminate the current CLI no-canonical-release failure without waiting for broader orchestration work.

Implementation:

- Keep/groom the pending unique CLI invocation ID and per-intent sequence.
- Replace command-specific fingerprint omissions with one canonical accepted-command envelope helper.
- Extend operation receipt/status with a safe result descriptor: output kind, result revision, and exact public result ref where applicable.
- Validate result descriptor at terminal commit.
- On receipt replay, return typed `stale_result`/`reprepare` continuation if the result ref is no longer current/available. Do not return “Operation complete.”
- Make CLI polling consume that disposition and report the structured recovery reason.
- Make progress logging item-aware as a tactical improvement, while retaining the full event model for Phase 6.

Tests:

- Two independent CLI sessions never share creation or operation keys.
- Same run retry with same key/fingerprint returns one receipt and one side effect.
- Same key/different envelope conflicts.
- Restart-invalidated result cannot report completed success.
- Terminal prepare always has a resolvable release result ref.
- Existing optimistic revision and cancellation tests remain green.

Exit criteria:

- Repeated CLI invocation cannot produce terminal prepare success with nil canonical release.
- Persistent/restart tests, not only a fake CLI core, prove it.

Rollback: additive receipt fields remain optional during one compatibility window; old records map to `result_unknown` and require refresh rather than claiming success.

### Phase 2 — Canonical continuation, lifecycle, disposition, and transition policy

Objective: remove status interpretation from adapters before more workflow fixes land.

Implementation:

- Add `OperationLifecycle`, `WorkflowDisposition`, `TrackerLaneOutcome`, `GoalAvailability`, and stable reason/failure codes to `pkg/api`.
- Add `Continuation` to the backend application contract and generated OpenAPI/TypeScript output.
- Implement one pure backend reducer/policy:
  - derives runnable/blocked/failed tracker lanes;
  - derives lifecycle/disposition;
  - derives available goals and exact required actions;
  - validates requested transitions using the same result.
- Stop mapping every stage `blocked`/`failed` directly to whole-workflow state in `setWorkflowStageStatus`.
- Define aggregate rules:
  - runnable work exists -> lifecycle running/ready regardless of failed siblings;
  - no runnable work + required action -> waiting/needs action;
  - successes plus terminal failures -> terminal partial;
  - all terminal failures -> terminal failed;
  - cancellation -> terminal canceled with retained completed lane outcomes.
- Keep existing command handlers behind a compatibility adapter initially.
- Replace backend/frontend duplicate `descriptionStatusAllowsUploadPlanning` and frontend `routeAccess` business rules with `AvailableGoals`/reason codes.

Tests:

- Table/property tests over tracker-lane combinations.
- Command validation and advertised availability cannot disagree.
- Frontend routes render backend availability and do not inspect raw stage status for legality.
- CLI exits/prompt behavior is derived from continuation disposition/actions.
- Unknown newer goal/reason values degrade safely in older UI clients.

Generated outputs/checks:

- `make workflow-contracts`
- `make workflow-contracts-check`
- `pnpm --dir webui run typecheck`
- API and generated-client tests.

Exit criteria:

- No adapter contains a list such as “completed/skipped/blocked/failed means next stage allowed.”
- One backend policy both advertises and enforces every continuation.

Rollback: old snapshot/status fields remain during compatibility; new consumers prefer continuation fields.

### Phase 3 — Exact media selection and centralized image-host preparation

Objective: make Upload Images/asset preparation complete before descriptions in every adapter.

Implementation:

- Replace nil/empty selection semantics with an explicit selection policy or normalized selected artifact IDs:
  - default capture -> all artifacts selected;
  - explicit empty selection -> typed action/block, never accidental default;
  - only selected IDs enter downstream fingerprints.
- Add internal image-requirement planning from exact eligible tracker projections.
- Represent a host job by stable key: release generation + media revision/fingerprint + selected artifact fingerprints + host + usage scope + tracker scope where private.
- Run independent host jobs concurrently with bounded per-host concurrency.
- Reconcile fallback by uncovered tracker requirements, not by whichever tracker happens to execute next.
- Apply successful variants as scoped patches, then publish one new exact `MediaArtifactSet` revision.
- Retain host attempts, warnings, failed requirements, and covered tracker IDs.
- Description builder accepts exact media revision only.
- Remove `ExactUploadedImages` nil-sentinel discovery and path-wide `ListUploadedImagesByPath` fallback from exact rendering.
- Remove image-host mutation/reload from `BuildPreparation`, `BuildUploadDryRun`, and `PrepareRetainedUploadPlan`.
- Change Upload Images UI:
  - no tracker-intent selection;
  - all selected artifacts shown selected by default;
  - no required single host choice;
  - display derived required hosts, concurrent attempts, fallbacks, and tracker coverage;
  - optional explicit additional host upload is a separate mutation.
- CLI reaches `GoalMediaReady` through the same planner; no adapter-specific upload-images omission.

Tests:

- All selected images are default-selected and hosted.
- Deselected images never appear in uploads/descriptions/plans.
- Two required hosts start concurrently.
- Primary failure + fallback success is one fallback upload and no tracker description failure.
- Fresh variants are visible immediately to every dependent tracker.
- Shared compatible host/scope work is deduplicated; private tracker scope is not shared.
- Retry runs only uncovered/failed host jobs.
- Description/dry-run/upload preparation makes zero image-host calls.
- Exact media fingerprint changes when hosted variants/selection change and invalidates downstream descriptions/plans.

Exit criteria:

- Moving from Upload Images to Description Builder performs no surprise required-host upload.
- Description retry does not reupload already satisfied image requirements.
- CLI and WebUI produce the same exact media revision for equivalent intent.

Rollback: retain legacy explicit-host endpoint as compatibility mapping to “add host copy”; do not let it satisfy hidden stage orchestration.

### Phase 4 — Per-tracker lanes and non-blocking partial outcomes

Objective: carry lower-level tracker isolation through the whole workflow/read model.

Implementation:

- Define one canonical `TrackerLaneOutcome` per selected tracker with stage, lifecycle, disposition, exact refs, required actions, failures, and retryability.
- Make projection, preflight, duplicates, asset readiness, description, preparation, dry run, submission, and injection update only their lane.
- Convert concurrent stage workers to return patches keyed by tracker/artifact/host. Apply with lineage/CAS checks; never publish a whole stale snapshot.
- Description aggregate succeeds/partials when at least one eligible lane produced a valid description. A tracker-local description failure does not set global required-action state.
- Upload plan consumes only lane outcomes and exact refs. Remove aggregate DescriptionSet status as an eligibility gate.
- Retry APIs accept exact prior result plus explicit failed tracker IDs validated against retryable lanes.
- Preserve successful lane receipts across sibling retries/cancellation.

Tests:

- One lane fails at each stage while another reaches requested goal.
- One lane requires action while other runnable lanes continue; workflow waits only after runnable work drains.
- One success/one failure -> partial; all fail -> failed; all waiting -> needs action.
- Retry touches only requested failed lanes.
- Stale concurrent lane patch cannot overwrite newer media/description/plan revision.
- Cancellation retains committed lane successes.

Exit criteria:

- No stage-wide early return is triggered solely by one tracker-local failure.
- CLI, HTTP response, and WebUI show the same tracker outcomes and disposition.

Rollback: retain aggregate fields as projections derived from lanes; never maintain two independent sources of truth.

### Phase 5 — `Continue` planner and adapter cutover

Objective: remove stage sequencing from CLI and WebUI/API consumers.

Implementation:

- Add `Continue/Current/Cancel` facade over current internal command handlers.
- Planner reconciles complete typed intent and retained fingerprints toward a requested goal, reusing valid stages and invalidating only affected downstream refs.
- Planner stops only for workflow-scoped failure, a necessary typed action, cancellation, no viable lane, or goal satisfaction.
- Migrate CLI first:
  - flags/request -> typed intent and goal;
  - poll current continuation/events;
  - render typed actions according to interaction policy;
  - no project/preflight/dupe/media/description/upload chain.
- Migrate HTTP routes to the same facade. Existing command routes become thin compatibility mappings; they must not call old handlers independently.
- Reduce WebUI release-session port to continue/current/cancel plus artifact methods.
- WebUI pages render facets of `Continuation`; page navigation uses `AvailableGoals`.
- Require upload approval bound to exact upload-plan fingerprint/revision. Interaction mode never grants approval implicitly.
- Add architecture policy preventing adapter-owned stage order/status gating.

Tests:

- Same synthetic intent through direct CLI adapter and HTTP adapter produces normalized identical continuation/event/tracker transcripts.
- Default no-action run needs begin/continue plus current polling only.
- `--unattended` invokes zero prompt functions.
- `--unattended_confirm` answers only allowed typed actions.
- Stale action and stale upload approval are rejected.
- Adding a synthetic internal stage does not require CLI/WebUI changes.

Exit criteria:

- `cmd/upbrr` and `webui/src/releaseSession` do not encode internal stage order.
- HTTP and in-process callers invoke the same application seam.

Rollback: compatibility routes remain temporarily, but they call `Continue`; no parallel orchestration implementation is permitted.

### Phase 6 — Exact tracker preparation, dry run, and upload execution

Objective: seal exact tracker-specific payload/torrent identity across review and execution.

Implementation:

- Define private `PreparedTrackerSubmission`:
  - tracker ID and exact projection/description/media refs;
  - safe review projection;
  - opaque private request capsule;
  - exact tracker-scoped torrent artifact ref;
  - semantic fingerprint;
  - config/credential fingerprints without secret values.
- Keep/rework `PrepareRetainedUploadPlan` as the single preparation barrier.
- Create every tracker’s exact torrent in this barrier. Explicitly record whether content was generated uniquely or copied from an allowed generic semantic source.
- Dry run consumes sealed preparation:
  - never submits tracker requests;
  - performs required client injection unless `NoSeed`;
  - records missing preparation/torrent/client/injection as tracker-local required failure;
  - continues all other lanes.
- Actual upload consumes the same sealed semantic fingerprint. Refresh only short-lived auth transport where allowed; do not rebuild payload/torrent.
- Client injection always takes the lane’s exact torrent ref; no filename/tracker-field discovery.
- Validate public projections contain only sanitized presence/fingerprint metadata.

Tests:

- Every lane has distinct artifact ID and physical private path.
- Allowed generic copies have equal semantic content but distinct tracker-scoped refs.
- Unique trackers have distinct content/fingerprint and never receive another lane’s artifact.
- Dry-run and submit semantic fingerprints match.
- Required injection failure fails its lane and prevents aggregate full success.
- `NoSeed` records explicit skip.
- One lane lacking prepared torrent does not block another.
- Public JSON/log scan contains no private torrent path, announce URL/passkey, token, cookie, or request payload.

Exit criteria:

- No actual upload performs torrent creation or image hosting that should have occurred during review preparation.
- Exact lane artifact identity is auditable without exposing secrets.

Rollback: preserve existing retained-plan interface until sealed data representation passes parity; never fall back to a global/latest torrent lookup.

### Phase 7 — Canonical events, logging, and operator diagnostics

Objective: make all surfaces report the same scoped truth at correct level.

Implementation:

- Add durable/monotonic workflow event sequence and cursor.
- Event fields: workflow/operation ID, command/goal, phase, scope kind/ID, lifecycle, state/outcome, severity, completed/total, stable failure code, recovery, sanitized message, timestamp.
- Operation root lifecycle remains queued/running/waiting/terminal. Item events keep their own terminal state.
- Central reducer assigns severity and recovery. CLI logger and WebUI progress component render; they do not reinterpret.
- Emit searchable stable fields and retain current redaction policy.
- Add summarized terminal event: requested count, succeeded, failed, skipped, waiting, retried, fallback-used.
- Add explicit codes/messages for:
  - missing prepared tracker operation;
  - missing exact tracker torrent;
  - dry-run client injection failure;
  - host requirement exhausted;
  - stale operation result;
  - stale action/approval;
  - unknown external submission outcome.

Tests:

- Failed child item is WARN/ERROR while root may remain running.
- Fallback-recovered host attempt does not generate a false tracker failure.
- CLI log, API event, and WebUI presentation share code/scope/state.
- Messages are descriptive without private details.
- Event cursor is monotonic; terminal item events are not overwritten by later running updates.
- `make logpolicy` passes.

Exit criteria:

- No adapter selects log severity by parsing message text or root status.
- Supplied failure classes are diagnosable from retained safe events alone.

Rollback: project events from existing operation items first; make durable event tables additive in Phase 8.

### Phase 8 — Durable effects/private artifacts and old-path removal

Objective: make accepted work/restart behavior safe and retire compatibility orchestration.

Implementation:

- Add additive SQLite tables for accepted intents/receipts, work leases/checkpoints, immutable events, external attempts, and materialized continuation view.
- Add owner/workflow/generation/tracker-scoped private artifact vault for media, prepared request capsules, and torrents. Use opaque public refs, content digests, ACL-restricted files, TTL/refcount cleanup, and optional encryption based on threat review.
- Persist `attempt_started` before external side effects and terminal receipt afterward.
- Reconcile tracker/client/image-host attempts where provider APIs permit.
- Crash after possible tracker success without reconciliation -> `unknown_outcome` required action; never blind automatic resubmit.
- Resume only checkpoint-safe/idempotent stages after restart.
- Remove stage-specific public routes, broad frontend ports, duplicate predicates, hidden image-host preparation, process-local retained upload closures, and superseded shallow tests after parity window.
- Keep focused production-adapter contract tests; replace orchestration mocks with seam-level tests through `Application`.

Tests:

- Crash injection before dispatch, before effect call, after effect call, after receipt, and before aggregate commit.
- Safe stages resume once; completed external calls are not repeated.
- Unsupported reconciliation becomes unknown outcome/action.
- Owner/tracker artifact access isolation.
- Digest mismatch, expiry, cleanup, and restart recovery.
- SQLite migrations forward-only/idempotent and mixed-record compatible.
- Full direct/HTTP/CLI/Web embedded parity scenario.

Exit criteria:

- Restart no longer requires blanket invalidation solely because private authority was process-local.
- Old public stage orchestration and caller-side policy are deleted.
- Deleting `internal/releaseworkflow` would necessarily force all workflow policy back into callers: the deep-module deletion test passes.

Rollback: dual-read/compare new materialized views before cutover. Do not dual-execute external side effects.

## Cross-phase test matrix

| Scenario | Module seam | CLI | HTTP/API | WebUI embedded | Privacy |
|---|---:|---:|---:|---:|---:|
| Repeated run/restart idempotency | Required | Required | Required | N/A | N/A |
| Default all-media selection | Required | Required | Required | Required | N/A |
| Concurrent required hosts + fallback | Required | Event parity | Event parity | Required | Required |
| One tracker description failure | Required | Required | Required | Required | N/A |
| Required dry-run injection failure | Required | Required | Required | Required | Required |
| Exact per-tracker torrents | Required | Result parity | Review parity | Review parity | Required |
| Unattended required action | Required | Required | Request parity | Presentation | Required |
| Stale action/approval | Required | Required | Required | Required | N/A |
| Crash/unknown tracker outcome | Required | Required | Required | Required | Required |
| Scoped event severity | Required | Required | Required | Required | Required |

Test strategy:

- Test deep behavior through the selected `Application` seam.
- Use real temp SQLite and filesystem for local-substitutable dependencies.
- Mock/script only true external image-host, tracker, and torrent-client ports.
- Replace shallow builder tests when seam tests cover the same contract; do not retain duplicate test layers indefinitely.
- E2E must use embedded app/CLI and local fake services per `webui/e2e/AGENTS.md`.

## Validation by change area

Start narrow, then expand because this work crosses shared core behavior and all adapters.

Backend/shared contracts:

```powershell
go test -race -v -timeout 20m ./internal/releaseworkflow ./internal/core ./internal/trackers/... ./pkg/api
go test -race -v -timeout 20m ./cmd/upbrr ./internal/webserver/...
make workflow-contracts-check
make logpolicy
make architecturepolicy
make lint
```

Frontend:

```powershell
pnpm --dir webui run lint
pnpm --dir webui run lint:dead
pnpm --dir webui run typecheck
pnpm --dir webui run test:unit
pnpm --dir webui run format:check
pnpm --dir webui run build
```

Shared release behavior:

```powershell
make test-go
make e2e-cli
make e2e-web
make e2e
git diff --check
```

Run `make pathpolicy` for private artifact/path changes and changed-package `make gofix-check-changed` before commit readiness.

## File ownership map

- Public typed contracts/reducers: `pkg/api/workflow_*`.
- Application seam, planner, transition policy, continuation projection, receipts/events: `internal/releaseworkflow`.
- Exact media requirement/upload publication: `internal/core/workflow_media.go`, `internal/core/media.go`; narrow image-host adapter under `internal/imagehosting`.
- Pure exact description consumption: `internal/core/workflow_description.go`, tracker description asset preparation under `internal/trackers`.
- Per-tracker preparation/torrent sealing: `internal/core/workflow_upload_plan.go`, `internal/trackers/service_upload_plan.go`, `internal/torrent/metainfo`.
- In-process CLI adapter: `cmd/upbrr`.
- HTTP adapter/routes/OpenAPI: `internal/webserver`, `cmd/workflowcontractgen`.
- Web client adapter/session/pages: `webui/src/api`, `webui/src/releaseSession`, workflow pages.
- Embedded parity tests/fakes: `internal/core/e2e_enabled.go`, `webui/e2e`.

Keep phase ownership narrow. Do not mix adapter cosmetic changes with reducer, media-lineage, or private-torrent changes in one implementation chunk.

## Risks and mitigations

- **Large migration creates two workflow engines.** Compatibility routes must call `Continue`; never maintain parallel ordering.
- **Partial status changes surprise callers.** Add disposition/tracker outcomes additively; compare old/new projections before deleting old fields.
- **Concurrent patches lose variants.** Use exact lineage/CAS and scoped patch merge; no whole-snapshot last-writer-wins.
- **Image upload dedupe leaks private scope.** Include usage/tracker scope in stable job key; share only explicitly compatible work.
- **Exactly-once tracker upload is impossible on some providers.** Fence local attempts and expose unknown outcome/manual reconciliation instead of retrying blindly.
- **Durable private data expands attack surface.** Opaque refs, strict ownership, restricted ACLs, redaction scans, bounded retention, encryption review.
- **Generated contract drift.** `workflow-contracts-check` and TypeScript compile remain mandatory.
- **Frontend regains policy.** Architecture check rejects stage-order chains and raw-status transition gates under `webui/src` and `cmd/upbrr`.
- **Tactical fixes are mistaken for completion.** Phase 1 closes current failure only; exit criteria for Phases 2-8 remain tracked separately.

## Final acceptance criteria

- Repeating the same CLI command in new processes cannot replay a stale completed operation as current success.
- Upload Images derives every needed host from selected tracker lanes, starts independent hosts concurrently, selects all captured images by default, and publishes exact variants before descriptions.
- Description Builder, dry run, and actual upload perform no unexpected image rehosting.
- One tracker failure never blocks another eligible tracker.
- Required dry-run injection failure is visible and failing for that tracker; other trackers continue.
- Every tracker dry-run/upload/client action is bound to its own exact private torrent artifact.
- CLI, HTTP/API, and WebUI expose identical continuation, tracker outcomes, actions, and event severity for the same retained workflow.
- Adapters contain no internal stage sequence or raw-status transition policy.
- Restart behavior is safe: no blind replay of uncertain external submission and no secret/private artifact leakage.
- Full focused checks, generated-contract checks, frontend checks, embedded CLI/Web E2E, lint/policies, and privacy scans pass.
