// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package releaseworkflow coordinates immutable release workflow stages and
// keeps repository and private-resource concerns behind one command boundary.
package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

const (
	legacyTrackerAuthActionKind        = api.RequiredActionKind("authenticate_tracker")
	legacyTrackerTwoFactorActionKind   = api.RequiredActionKind("provide_two_factor")
	legacyTrackerAuthFeedbackKind      = api.ReleaseWorkflowUploadFeedbackKind("trackerAuthentication")
	legacyTrackerTwoFactorFeedbackKind = api.ReleaseWorkflowUploadFeedbackKind("twoFactor")
)

// MediaArtifactContent is one owner-checked retained media stream.
type MediaArtifactContent struct {
	Body        io.ReadCloser
	ContentType string
}

// MediaPreviewContent retains one non-authoritative frame preview without
// exposing image bytes or filesystem identity in workflow snapshots.
type MediaPreviewContent struct {
	Bytes       []byte
	ContentType string
	Width       int
	Height      int
}

// StagedMediaContent is owner-scoped private upload content. Name and bytes
// never enter public workflow snapshots.
type StagedMediaContent struct {
	Name        string
	Bytes       []byte
	ContentType string
}

// StagedMediaAttachment binds private staged bytes to one requested artifact shape.
type StagedMediaAttachment struct {
	Attachment api.MediaAttachment
	Content    StagedMediaContent
}

// RetainedMediaResource keeps media bytes and filesystem identity private while
// allowing opaque workflow artifact reads and bounded deletion.
type RetainedMediaResource interface {
	OpenArtifact(context.Context, api.MediaArtifactSet, api.PublicResourceID) (MediaArtifactContent, error)
	DeleteArtifacts(context.Context, api.MediaArtifactSet, []api.PublicResourceID) (RetainedMediaResource, error)
}

// RetainedMediaCommitter finalizes staged external media mutations. Local
// artifact deletion commits before the workflow revision is saved so a failed
// commit keeps the artifact durable and retryable; hosted-image removal
// commits after the revision has committed. Commit must be idempotent so
// retries can converge after a partial filesystem or repository failure.
type RetainedMediaCommitter interface {
	Commit(context.Context) error
}

var (
	// ErrWorkflowNotFound hides both absent and foreign-owner workflows.
	ErrWorkflowNotFound = errors.New("release workflow not found")
	// ErrRevisionConflict reports optimistic concurrency failure.
	ErrRevisionConflict = errors.New("release workflow revision conflict")
	// ErrIdempotencyConflict reports reuse of a key with different command input.
	ErrIdempotencyConflict = errors.New("release workflow idempotency conflict")
	// ErrOperationConflict reports another active command or mismatched operation idempotency.
	ErrOperationConflict = errors.New("release workflow operation conflict")
	// ErrInvalidTransition reports a command whose dependencies are incomplete.
	ErrInvalidTransition = errors.New("release workflow invalid transition")
	// ErrPrivateResourceUnavailable reports missing, expired, or restart-invalidated authority.
	ErrPrivateResourceUnavailable = errors.New("release workflow private resource unavailable")
	// ErrPrivateResourceConsumed reports reuse of single-use private authority.
	ErrPrivateResourceConsumed = errors.New("release workflow private resource consumed")
	// ErrPrivateResourceIntegrity reports digest or metadata tampering in durable private authority.
	ErrPrivateResourceIntegrity = errors.New("release workflow private resource integrity check failed")
)

// DurablePrivateResource marshals one private value for owner-scoped restart recovery.
// Payloads may contain private paths or credentials and must only be written to the private vault.
type DurablePrivateResource interface {
	MarshalPrivateResource() (kind string, payload []byte, err error)
}

// PrivateResourceCodec rehydrates one private resource kind with current process services.
type PrivateResourceCodec struct {
	Kind   string
	Decode func([]byte) (any, error)
}

// Clock supplies deterministic workflow timestamps.
type Clock interface {
	Now() time.Time
}

// IDGenerator supplies opaque workflow and snapshot IDs.
type IDGenerator interface {
	NewID(prefix string) (string, error)
}

// ReleasePreparer owns canonical prepared-release generation.
type ReleasePreparer interface {
	Prepare(context.Context, api.PrepareInput) (api.PrepareResult, error)
	ResolveDisplay(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error)
	ResolveUploadSubject(context.Context, api.UploadSubjectInput) (api.UploadSubject, error)
	ResolveDuplicateSubject(context.Context, api.DuplicateCheckInput) (api.DuplicateSubject, error)
}

// ReleasePreparerFunc adapts a function to [ReleasePreparer].
type ReleasePreparerFunc struct {
	PrepareFunc   func(context.Context, api.PrepareInput) (api.PrepareResult, error)
	DisplayFunc   func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error)
	SubjectFunc   func(context.Context, api.UploadSubjectInput) (api.UploadSubject, error)
	DuplicateFunc func(context.Context, api.DuplicateCheckInput) (api.DuplicateSubject, error)
}

// Prepare calls the adapted preparation function.
func (f ReleasePreparerFunc) Prepare(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
	return f.PrepareFunc(ctx, input)
}

// ResolveDisplay calls the adapted exact-generation display projection function.
func (f ReleasePreparerFunc) ResolveDisplay(ctx context.Context, ref api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
	return f.DisplayFunc(ctx, ref)
}

// ResolveUploadSubject calls the adapted exact-generation operation projection function.
func (f ReleasePreparerFunc) ResolveUploadSubject(ctx context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
	return f.SubjectFunc(ctx, input)
}

// ResolveDuplicateSubject calls the adapted exact-generation duplicate projection function.
func (f ReleasePreparerFunc) ResolveDuplicateSubject(ctx context.Context, input api.DuplicateCheckInput) (api.DuplicateSubject, error) {
	return f.DuplicateFunc(ctx, input)
}

// TrackerProjectionBuilder hides catalog/config resolution and tracker-local
// projection semantics. Rule authorization inputs are tracker-scoped,
// server-held fingerprints that implementations accept only for an identical
// freshly evaluated waivable-failure set.
type TrackerProjectionBuilder interface {
	Build(
		context.Context,
		api.ReleaseSnapshot,
		api.UploadSubject,
		[]api.TrackerID,
		map[api.TrackerID]api.TrackerProjectionInstructions,
		map[api.TrackerID]api.WorkflowFingerprint,
		api.WorkflowExecutionMode,
	) (
		api.TrackerCatalogSnapshot,
		api.TrackerRuntimeSnapshot,
		api.TrackerSelection,
		api.TrackerReleaseProjectionSet,
		error,
	)
}

// TrackerPreflightBuilder hides live tracker readiness checks behind one
// projection-bound application port. Returned values are unstamped templates;
// the workflow module owns identity, lineage, and publication.
type TrackerPreflightBuilder interface {
	Build(
		context.Context,
		api.UploadSubject,
		api.TrackerCatalogSnapshot,
		api.TrackerRuntimeSnapshot,
		api.TrackerReleaseProjectionSet,
		time.Time,
	) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error)
}

// DupeAssessmentBuilder executes projection-bound duplicate checks and returns
// a safe retained snapshot plus optional private evidence for later planning.
type DupeAssessmentBuilder interface {
	Build(
		context.Context,
		api.DuplicateSubject,
		api.TrackerReleaseProjectionSet,
		api.TrackerPreflightAssessment,
		time.Time,
		bool,
	) (api.DupeAssessment, any, error)
}

// MediaArtifactBuilder captures generation- and projection-bound media while
// keeping local paths and bytes in a private resource.
type MediaArtifactBuilder interface {
	Build(
		context.Context,
		api.ReleaseRef,
		api.TrackerReleaseProjectionSet,
		api.MediaCaptureInstructions,
		time.Time,
	) (api.MediaArtifactSet, any, error)
}

// IncrementalMediaArtifactBuilder preserves current media and skips already
// retained requested screenshot indexes during later capture commands.
type IncrementalMediaArtifactBuilder interface {
	BuildIncremental(
		context.Context,
		api.ReleaseRef,
		api.TrackerReleaseProjectionSet,
		api.MediaCaptureInstructions,
		*api.MediaArtifactSet,
		any,
		time.Time,
	) (api.MediaArtifactSet, RetainedMediaResource, error)
}

// MediaArtifactMutator owns private attachment and image-host mutations while
// the workflow module owns revision checks and snapshot publication.
type MediaArtifactMutator interface {
	Attach(
		context.Context,
		api.ReleaseRef,
		api.TrackerReleaseProjectionSet,
		*api.MediaArtifactSet,
		any,
		[]StagedMediaAttachment,
		time.Time,
	) (api.MediaArtifactSet, RetainedMediaResource, error)
	UploadImages(
		context.Context,
		api.ReleaseRef,
		api.TrackerReleaseProjectionSet,
		api.MediaArtifactSet,
		any,
		[]api.PublicResourceID,
		string,
		bool,
		time.Time,
	) (api.MediaArtifactSet, RetainedMediaResource, []api.HostedImageAttempt, error)
	RemoveHostedImages(
		context.Context,
		api.ReleaseRef,
		api.MediaArtifactSet,
		any,
		[]api.PublicResourceID,
		time.Time,
	) (api.MediaArtifactSet, RetainedMediaResource, error)
}

// MediaPlanner supplies safe screenshot timing and non-authoritative previews
// for one exact prepared release.
type MediaPlanner interface {
	Plan(context.Context, api.ReleaseRef, api.TrackerReleaseProjectionSet, time.Time) (api.MediaPlan, error)
	PreviewFrame(context.Context, api.ReleaseRef, float64) (MediaPreviewContent, error)
}

// DescriptionBuilder generates projection- and media-bound descriptions.
// Fingerprints must not invoke text generation or remote image work so the
// workflow can reuse a compatible retained revision without side effects.
type DescriptionBuilder interface {
	Fingerprints(
		context.Context,
		api.ReleaseRef,
		api.TrackerReleaseProjectionSet,
		api.MediaArtifactSet,
		any,
		api.DescriptionInstructions,
	) (api.WorkflowFingerprint, api.WorkflowFingerprint, error)
	Build(
		context.Context,
		api.ReleaseRef,
		api.TrackerReleaseProjectionSet,
		api.MediaArtifactSet,
		any,
		api.DescriptionInstructions,
		time.Time,
	) (api.DescriptionSet, error)
}

// RetainedUploadExecution is private single-use execution authority. Execute
// must submit already-prepared operations without rebuilding semantic payloads.
type RetainedUploadExecution interface {
	Execute(context.Context, []api.TrackerID) ([]api.UploadTrackerResult, error)
	RegisteredArtifactAuthority() RegisteredArtifactAuthority
	Release() error
}

// RegisteredArtifactAuthority retains private exact-torrent authority after a
// successful tracker submission. It must never enter public workflow snapshots.
type RegisteredArtifactAuthority struct {
	ClientSubject api.ClientSubject
	Torrents      map[api.TrackerID]api.TorrentResult
}

// UploadPlanBuildOptions controls side effects and execution policy for one
// retained upload preparation.
type UploadPlanBuildOptions struct {
	DryRun               bool
	NoSeed               bool
	TrackerIDs           []api.TrackerID
	TrackerApproval      *api.TrackerApprovalSnapshotRef
	AuthorityFingerprint api.WorkflowFingerprint
}

// UploadPlanBuilder creates the public review projection and the exact private
// prepared operations represented by it.
type UploadPlanBuilder interface {
	Fingerprint(
		context.Context,
		api.TrackerReleaseProjectionSet,
		api.DupeAssessment,
		api.MediaArtifactSet,
		api.DescriptionSet,
		UploadPlanBuildOptions,
	) (api.WorkflowFingerprint, error)
	Build(
		context.Context,
		api.TrackerReleaseProjectionSet,
		api.DupeAssessment,
		any,
		api.MediaArtifactSet,
		any,
		api.DescriptionSet,
		any,
		UploadPlanBuildOptions,
		time.Time,
	) (api.UploadPlan, RetainedUploadExecution, error)
	RetryClientInjections(
		context.Context,
		RegisteredArtifactAuthority,
		[]api.TrackerID,
	) ([]api.UploadTrackerResult, error)
}

// Repository atomically persists owner-scoped public workflow state.
type Repository interface {
	Create(context.Context, string, string, api.WorkflowFingerprint, State) (State, bool, error)
	Load(context.Context, string, api.WorkflowID) (State, error)
	Save(context.Context, string, api.WorkflowRevision, State) error
	Delete(context.Context, string, api.WorkflowID) error
}

// OperationRepository persists progress separately from aggregate state.
type OperationRepository interface {
	CreateOperation(context.Context, api.ReleaseWorkflowOperationRecord) (api.ReleaseWorkflowOperationRecord, bool, error)
	LoadOperation(context.Context, string, api.WorkflowID, api.WorkflowOperationID) (api.ReleaseWorkflowOperationRecord, error)
	LoadOperationByIdempotency(context.Context, string, api.WorkflowID, string, string) (api.ReleaseWorkflowOperationRecord, error)
	LoadLatestOperation(context.Context, string, api.WorkflowID) (api.ReleaseWorkflowOperationRecord, error)
	SaveOperation(context.Context, uint64, api.ReleaseWorkflowOperationRecord) error
	ListActiveOperations(context.Context) ([]api.ReleaseWorkflowOperationRecord, error)
}

// DurabilityRepository persists accepted intents, immutable events,
// materialized continuation views, and fenced external effects.
type DurabilityRepository interface {
	AcceptIntent(context.Context, api.ReleaseWorkflowIntentRecord) (api.ReleaseWorkflowIntentRecord, bool, error)
	SaveContinuation(context.Context, api.ReleaseWorkflowContinuationRecord) error
	AppendEvents(context.Context, string, api.WorkflowID, []api.WorkflowEvent) ([]api.WorkflowEvent, error)
	LoadEvents(context.Context, string, api.WorkflowID, uint64, int) ([]api.WorkflowEvent, error)
	BeginEffect(context.Context, api.ReleaseWorkflowEffectRecord) (api.ReleaseWorkflowEffectRecord, bool, error)
	CompleteEffect(context.Context, api.WorkflowEffectStatus, api.ReleaseWorkflowEffectRecord) error
	MarkOperationEffectsUnknown(context.Context, string, api.WorkflowID, api.WorkflowOperationID, time.Time) error
	ResolveEffectUnknown(context.Context, string, api.WorkflowID, api.WorkflowExternalEffectKind, string, time.Time) error
	LoadWork(context.Context, string, api.WorkflowID, api.WorkflowOperationID) (api.ReleaseWorkflowWorkRecord, error)
	ClaimWork(context.Context, api.ReleaseWorkflowWorkRecord) error
	RenewWork(context.Context, api.ReleaseWorkflowWorkRecord) error
	CheckpointWork(context.Context, api.ReleaseWorkflowWorkRecord) error
	CompleteWork(context.Context, api.ReleaseWorkflowWorkRecord) error
}

// PrivateResourceStore retains owner-scoped resources that must never enter public snapshots.
type PrivateResourceStore interface {
	Put(ownerID string, workflowID api.WorkflowID, resourceID string, value any, expiresAt time.Time) error
	Get(ownerID string, workflowID api.WorkflowID, resourceID string, now time.Time) (any, error)
	Consume(ownerID string, workflowID api.WorkflowID, resourceID string, now time.Time) (any, error)
	Delete(ownerID string, workflowID api.WorkflowID, resourceID string)
	InvalidateWorkflow(ownerID string, workflowID api.WorkflowID)
	// InvalidateWorkflowExcept invalidates one workflow while retaining named resources.
	InvalidateWorkflowExcept(ownerID string, workflowID api.WorkflowID, preservedResourceIDs ...string)
	InvalidateAll()
}

// Application is the compact adapter-facing workflow command/query surface.
type Application interface {
	Continue(context.Context, string, api.ContinueReleaseWorkflowRequest) (CommandResult, error)
	StartUpload(context.Context, string, api.CreateReleaseWorkflowUploadRequest) (CommandResult, error)
	SubmitUploadFeedback(context.Context, string, api.WorkflowID, api.ReleaseWorkflowUploadFeedback) (CommandResult, error)
	Execute(context.Context, string, Command) (CommandResult, error)
	Start(context.Context, string, Command) (api.WorkflowOperationStatus, error)
	Workflow(context.Context, string, api.WorkflowID) (api.ReleaseWorkflow, error)
	Current(context.Context, string, api.WorkflowID) (CommandResult, error)
	Operation(context.Context, string, api.WorkflowID, api.WorkflowOperationID) (api.WorkflowOperationStatus, error)
	CancelOperation(context.Context, string, api.WorkflowID, api.WorkflowOperationID) (api.WorkflowOperationStatus, error)
	MediaPlan(context.Context, string, api.WorkflowID) (api.MediaPlan, error)
	PreviewFrame(context.Context, string, api.WorkflowID, api.WorkflowRevision, float64) (api.FramePreview, error)
	PreviewArtifact(context.Context, string, api.WorkflowID, api.PublicResourceID) (MediaArtifactContent, error)
	StageMediaResource(context.Context, string, api.WorkflowID, api.WorkflowRevision, StagedMediaContent) (api.WorkflowResourceRef, error)
	MediaArtifact(context.Context, string, api.WorkflowID, api.MediaArtifactSetRef, api.PublicResourceID) (MediaArtifactContent, error)
}

// State is the repository value owned exclusively by the workflow module.
// Snapshots are immutable; maps retain prior revisions for exact-reference reads.
type State struct {
	OwnerID                string
	ProcessEpoch           string
	TrackerDecisionMode    TrackerDecisionMode
	Workflow               api.ReleaseWorkflow
	FactInstructions       map[api.ReleaseFactInstructionSnapshotID]api.ReleaseFactInstructionSnapshot
	Releases               map[api.ReleaseSnapshotID]api.ReleaseSnapshot
	Catalogs               map[api.TrackerCatalogSnapshotID]api.TrackerCatalogSnapshot
	Runtimes               map[api.TrackerRuntimeSnapshotID]api.TrackerRuntimeSnapshot
	Selections             map[api.TrackerSelectionID]api.TrackerSelection
	ProjectionInstructions map[api.TrackerProjectionInstructionSnapshotID]api.TrackerProjectionInstructionSnapshot
	Projections            map[api.TrackerReleaseProjectionSetID]api.TrackerReleaseProjectionSet
	Preflights             map[api.TrackerPreflightAssessmentID]api.TrackerPreflightAssessment
	Dupes                  map[api.DupeAssessmentID]api.DupeAssessment
	TrackerApprovals       map[api.TrackerApprovalSnapshotID]api.TrackerApprovalSnapshot
	Media                  map[api.MediaArtifactSetID]api.MediaArtifactSet
	Descriptions           map[api.DescriptionSetID]api.DescriptionSet
	DryRuns                map[api.UploadDryRunResultID]api.UploadDryRunResult
	UploadResults          map[api.UploadResultID]api.UploadResult
	Operations             map[api.WorkflowOperationID]api.WorkflowOperationStatus
	Receipts               map[string]commandReceipt
	Composite              *compositeUploadSession
}

type commandReceipt struct {
	Fingerprint api.WorkflowFingerprint
	Result      CommandResult
}

// CommandResult is retained as an internal compatibility name while all
// adapters consume the pkg/api-owned serialized projection.
type CommandResult = api.ReleaseWorkflowCurrent

type mutation interface {
	commandName() string
	commandFingerprint() (api.WorkflowFingerprint, error)
}

// Command is a sealed adapter-facing user intent. Internal publication inputs
// deliberately do not implement it.
type Command interface {
	mutation
	userIntent()
	operationKind() api.OperationKind
}

// CreateWorkflowCommand creates fact instructions and a draft aggregate.
// Composite, when set, installs retained composite state in the same mutation.
type CreateWorkflowCommand struct {
	WorkflowID          api.WorkflowID
	Instructions        api.ReleaseFactInstructions
	IdempotencyKey      string
	RequestFingerprint  api.WorkflowFingerprint
	TrackerDecisionMode TrackerDecisionMode
	Composite           *compositeUploadSession
}

func (CreateWorkflowCommand) commandName() string              { return "create" }
func (CreateWorkflowCommand) userIntent()                      {}
func (CreateWorkflowCommand) operationKind() api.OperationKind { return api.OperationKindUnknown }
func (c CreateWorkflowCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		Instructions        api.ReleaseFactInstructions
		RequestFingerprint  api.WorkflowFingerprint
		TrackerDecisionMode TrackerDecisionMode
	}{
		Instructions:        c.Instructions,
		RequestFingerprint:  c.RequestFingerprint,
		TrackerDecisionMode: normalizeTrackerDecisionMode(c.TrackerDecisionMode),
	})
}

// ReplaceFactInstructionsCommand publishes a new instruction revision and invalidates all facts and downstream state.
type ReplaceFactInstructionsCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Instructions     api.ReleaseFactInstructions
	IdempotencyKey   string
}

func (ReplaceFactInstructionsCommand) commandName() string { return "replace_fact_instructions" }
func (ReplaceFactInstructionsCommand) userIntent()         {}
func (ReplaceFactInstructionsCommand) operationKind() api.OperationKind {
	return api.OperationKindUnknown
}
func (c ReplaceFactInstructionsCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(c.Instructions)
}

// PrepareReleaseCommand creates a canonical release snapshot from retained fact instructions.
type PrepareReleaseCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Input            api.PrepareInput
	IdempotencyKey   string
}

func (PrepareReleaseCommand) commandName() string              { return "prepare_release" }
func (PrepareReleaseCommand) userIntent()                      {}
func (PrepareReleaseCommand) operationKind() api.OperationKind { return api.OperationKindPreparation }
func (c PrepareReleaseCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(c.Input)
}

// ResetReleaseCommand replaces exact fact instructions and force-reprepares in one operation.
type ResetReleaseCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Input            api.PrepareInput
	IdempotencyKey   string
}

func (ResetReleaseCommand) commandName() string              { return "reset_release" }
func (ResetReleaseCommand) userIntent()                      {}
func (ResetReleaseCommand) operationKind() api.OperationKind { return api.OperationKindPreparation }
func (c ResetReleaseCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(c.Input)
}

// SelectBlurayCandidateCommand validates and selects one retained candidate, then force-reprepares.
type SelectBlurayCandidateCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	ReleaseID        string
	IdempotencyKey   string
}

func (SelectBlurayCandidateCommand) commandName() string { return "select_bluray_candidate" }
func (SelectBlurayCandidateCommand) userIntent()         {}
func (SelectBlurayCandidateCommand) operationKind() api.OperationKind {
	return api.OperationKindPreparation
}
func (c SelectBlurayCandidateCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		ReleaseID        string
	}{c.ExpectedRevision, c.ReleaseID})
}

// trackerContextPublication publishes exact catalog/runtime/selection snapshots.
type trackerContextPublication struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Catalog          api.TrackerCatalogSnapshot
	Runtime          api.TrackerRuntimeSnapshot
	Selection        api.TrackerSelection
	IdempotencyKey   string
}

// ProjectTrackersCommand resolves catalog/runtime/selection and tracker-local projections behind one transition.
type ProjectTrackersCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	TrackerIDs       []api.TrackerID
	Instructions     map[api.TrackerID]api.TrackerProjectionInstructions
	ExecutionMode    api.WorkflowExecutionMode
	IdempotencyKey   string
}

func (ProjectTrackersCommand) commandName() string { return "project_trackers" }
func (ProjectTrackersCommand) userIntent()         {}
func (ProjectTrackersCommand) operationKind() api.OperationKind {
	return api.OperationKindDuplicateCheck
}
func (c ProjectTrackersCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		TrackerIDs    []api.TrackerID
		Instructions  map[api.TrackerID]api.TrackerProjectionInstructions
		ExecutionMode api.WorkflowExecutionMode
	}{c.TrackerIDs, c.Instructions, api.NormalizeWorkflowExecutionMode(c.ExecutionMode)})
}

// projectionSetPublication publishes a projection revision and invalidates dependent state.
type projectionSetPublication struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Snapshot         api.TrackerReleaseProjectionSet
	IdempotencyKey   string
}

// PreflightTrackersCommand runs live checks and atomically publishes both the
// assessment and a new finalized projection revision.
type PreflightTrackersCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	InputFingerprint api.WorkflowFingerprint
	Interaction      api.InteractionMode
	ExecutionMode    api.WorkflowExecutionMode
	IdempotencyKey   string
}

func (PreflightTrackersCommand) commandName() string { return "preflight_trackers" }
func (PreflightTrackersCommand) userIntent()         {}
func (PreflightTrackersCommand) operationKind() api.OperationKind {
	return api.OperationKindDuplicateCheck
}
func (c PreflightTrackersCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		InputFingerprint api.WorkflowFingerprint
		Interaction      api.InteractionMode
		ExecutionMode    api.WorkflowExecutionMode
	}{
		ExpectedRevision: c.ExpectedRevision,
		InputFingerprint: c.InputFingerprint,
		Interaction:      continuationInteractionMode(api.WorkflowIntent{Interaction: c.Interaction}),
		ExecutionMode:    api.NormalizeWorkflowExecutionMode(c.ExecutionMode),
	})
}

// dupeAssessmentPublication publishes retained projection-bound duplicate results.
type dupeAssessmentPublication struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Snapshot         api.DupeAssessment
	IdempotencyKey   string
}

// CheckDuplicatesCommand executes one exact finalized projection revision.
type CheckDuplicatesCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	SkipRemote       bool
	CheckOrdinal     uint8
	IdempotencyKey   string
}

func (CheckDuplicatesCommand) commandName() string { return "check_duplicates" }
func (CheckDuplicatesCommand) userIntent()         {}
func (CheckDuplicatesCommand) operationKind() api.OperationKind {
	return api.OperationKindDuplicateCheck
}
func (c CheckDuplicatesCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		SkipRemote       bool
		CheckOrdinal     uint8
	}{
		ExpectedRevision: c.ExpectedRevision,
		SkipRemote:       c.SkipRemote,
		CheckOrdinal:     normalizedDuplicateCheckOrdinal(c.CheckOrdinal),
	})
}

// DecideDuplicatesCommand publishes owner decisions without repeating remote searches.
type DecideDuplicatesCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Decisions        map[api.TrackerID]api.DupeDecision
	IdempotencyKey   string
}

// ApproveTrackersCommand publishes exact post-dupe tracker authority.
type ApproveTrackersCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Approval         api.TrackerApproval
	IdempotencyKey   string
}

func (ApproveTrackersCommand) commandName() string              { return "approve_trackers" }
func (ApproveTrackersCommand) userIntent()                      {}
func (ApproveTrackersCommand) operationKind() api.OperationKind { return api.OperationKindUnknown }
func (c ApproveTrackersCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(c.Approval)
}

func (DecideDuplicatesCommand) commandName() string              { return "decide_duplicates" }
func (DecideDuplicatesCommand) userIntent()                      {}
func (DecideDuplicatesCommand) operationKind() api.OperationKind { return api.OperationKindUnknown }
func (c DecideDuplicatesCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		Decisions        map[api.TrackerID]api.DupeDecision
	}{c.ExpectedRevision, c.Decisions})
}

// mediaArtifactsPublication publishes generation-bound media and invalidates descriptions/upload plans.
type mediaArtifactsPublication struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Snapshot         api.MediaArtifactSet
	IdempotencyKey   string
}

// CaptureMediaCommand plans and captures all required screenshots/DVD menus.
type CaptureMediaCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Instructions     api.MediaCaptureInstructions
	IdempotencyKey   string
}

func (CaptureMediaCommand) commandName() string              { return "capture_media" }
func (CaptureMediaCommand) userIntent()                      {}
func (CaptureMediaCommand) operationKind() api.OperationKind { return api.OperationKindMedia }
func (c CaptureMediaCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		Instructions     api.MediaCaptureInstructions
	}{c.ExpectedRevision, c.Instructions})
}

// SetMediaSelectionCommand changes selection state for opaque retained media.
type SetMediaSelectionCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Media            api.MediaArtifactSetRef
	ArtifactIDs      []api.PublicResourceID
	Selected         bool
	IdempotencyKey   string
}

func (SetMediaSelectionCommand) commandName() string              { return "set_media_selection" }
func (SetMediaSelectionCommand) userIntent()                      {}
func (SetMediaSelectionCommand) operationKind() api.OperationKind { return api.OperationKindUnknown }
func (c SetMediaSelectionCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		Media            api.MediaArtifactSetRef
		ArtifactIDs      []api.PublicResourceID
		Selected         bool
	}{c.ExpectedRevision, c.Media, c.ArtifactIDs, c.Selected})
}

// DeleteMediaArtifactsCommand deletes owner-scoped retained media by opaque ID.
type DeleteMediaArtifactsCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Media            api.MediaArtifactSetRef
	ArtifactIDs      []api.PublicResourceID
	IdempotencyKey   string
}

func (DeleteMediaArtifactsCommand) commandName() string              { return "delete_media_artifacts" }
func (DeleteMediaArtifactsCommand) userIntent()                      {}
func (DeleteMediaArtifactsCommand) operationKind() api.OperationKind { return api.OperationKindUnknown }
func (c DeleteMediaArtifactsCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		Media            api.MediaArtifactSetRef
		ArtifactIDs      []api.PublicResourceID
	}{c.ExpectedRevision, c.Media, c.ArtifactIDs})
}

// ReorderMediaArtifactsCommand establishes selected final-media order by
// opaque artifact ID without changing private resource identity.
type ReorderMediaArtifactsCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Media            api.MediaArtifactSetRef
	ArtifactIDs      []api.PublicResourceID
	IdempotencyKey   string
}

func (ReorderMediaArtifactsCommand) commandName() string { return "reorder_media_artifacts" }
func (ReorderMediaArtifactsCommand) userIntent()         {}
func (ReorderMediaArtifactsCommand) operationKind() api.OperationKind {
	return api.OperationKindUnknown
}
func (c ReorderMediaArtifactsCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		Media            api.MediaArtifactSetRef
		ArtifactIDs      []api.PublicResourceID
	}{c.ExpectedRevision, c.Media, c.ArtifactIDs})
}

// AttachMediaArtifactsCommand consumes owner-scoped staged resources and
// publishes them into exact workflow media lineage.
type AttachMediaArtifactsCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Media            *api.MediaArtifactSetRef
	Attachments      []api.MediaAttachment
	IdempotencyKey   string
}

func (AttachMediaArtifactsCommand) commandName() string              { return "attach_media_artifacts" }
func (AttachMediaArtifactsCommand) userIntent()                      {}
func (AttachMediaArtifactsCommand) operationKind() api.OperationKind { return api.OperationKindUnknown }
func (c AttachMediaArtifactsCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		Media            *api.MediaArtifactSetRef
		Attachments      []api.MediaAttachment
	}{c.ExpectedRevision, c.Media, c.Attachments})
}

// UploadMediaImagesCommand hosts selected local artifacts for exact media lineage.
type UploadMediaImagesCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Media            api.MediaArtifactSetRef
	ArtifactIDs      []api.PublicResourceID
	Host             string
	Retry            bool
	IdempotencyKey   string
}

func (UploadMediaImagesCommand) commandName() string {
	return "upload_media_images"
}
func (UploadMediaImagesCommand) userIntent() {}
func (UploadMediaImagesCommand) operationKind() api.OperationKind {
	return api.OperationKindImageHosting
}
func (c UploadMediaImagesCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		Media            api.MediaArtifactSetRef
		ArtifactIDs      []api.PublicResourceID
		Host             string
		Retry            bool
	}{c.ExpectedRevision, c.Media, c.ArtifactIDs, c.Host, c.Retry})
}

// RemoveHostedImagesCommand removes hosted outcomes by opaque artifact ID.
type RemoveHostedImagesCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Media            api.MediaArtifactSetRef
	ArtifactIDs      []api.PublicResourceID
	IdempotencyKey   string
}

func (RemoveHostedImagesCommand) commandName() string              { return "remove_hosted_images" }
func (RemoveHostedImagesCommand) userIntent()                      {}
func (RemoveHostedImagesCommand) operationKind() api.OperationKind { return api.OperationKindUnknown }
func (c RemoveHostedImagesCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		Media            api.MediaArtifactSetRef
		ArtifactIDs      []api.PublicResourceID
	}{c.ExpectedRevision, c.Media, c.ArtifactIDs})
}

// GenerateDescriptionsCommand generates or reuses exact compatible descriptions.
type GenerateDescriptionsCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Instructions     api.DescriptionInstructions
	IdempotencyKey   string
}

func (GenerateDescriptionsCommand) commandName() string { return "generate_descriptions" }
func (GenerateDescriptionsCommand) userIntent()         {}
func (GenerateDescriptionsCommand) operationKind() api.OperationKind {
	return api.OperationKindDescription
}
func (c GenerateDescriptionsCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		Instructions     api.DescriptionInstructions
	}{c.ExpectedRevision, c.Instructions})
}

// SaveDescriptionOverrideCommand changes one exact retained description group.
type SaveDescriptionOverrideCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Descriptions     api.DescriptionSetRef
	GroupKey         string
	Source           string
	IdempotencyKey   string
}

func (SaveDescriptionOverrideCommand) commandName() string { return "save_description_override" }
func (SaveDescriptionOverrideCommand) userIntent()         {}
func (SaveDescriptionOverrideCommand) operationKind() api.OperationKind {
	return api.OperationKindUnknown
}
func (c SaveDescriptionOverrideCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		Descriptions     api.DescriptionSetRef
		GroupKey         string
		Source           string
	}{c.ExpectedRevision, c.Descriptions, c.GroupKey, c.Source})
}

// ResetDescriptionOverrideCommand restores one exact retained description group.
type ResetDescriptionOverrideCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Descriptions     api.DescriptionSetRef
	GroupKey         string
	IdempotencyKey   string
}

func (ResetDescriptionOverrideCommand) commandName() string { return "reset_description_override" }
func (ResetDescriptionOverrideCommand) userIntent()         {}
func (ResetDescriptionOverrideCommand) operationKind() api.OperationKind {
	return api.OperationKindUnknown
}
func (c ResetDescriptionOverrideCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		Descriptions     api.DescriptionSetRef
		GroupKey         string
	}{c.ExpectedRevision, c.Descriptions, c.GroupKey})
}

// descriptionsPublication publishes retained descriptions and invalidates upload plans.
type descriptionsPublication struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Snapshot         api.DescriptionSet
	IdempotencyKey   string
}

// DryRunUploadsCommand prepares sanitized reports without tracker submission.
type DryRunUploadsCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	NoSeed           bool
	TrackerIDs       []api.TrackerID
	IdempotencyKey   string
}

func (DryRunUploadsCommand) commandName() string              { return "dry_run_uploads" }
func (DryRunUploadsCommand) userIntent()                      {}
func (DryRunUploadsCommand) operationKind() api.OperationKind { return api.OperationKindUploadDryRun }
func (c DryRunUploadsCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		NoSeed           bool
		TrackerIDs       []api.TrackerID
	}{c.ExpectedRevision, c.NoSeed, c.TrackerIDs})
}

// ExecuteUploadsCommand directly prepares and executes eligible trackers.
type ExecuteUploadsCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	NoSeed           bool
	TrackerIDs       []api.TrackerID
	IdempotencyKey   string
}

func (ExecuteUploadsCommand) commandName() string { return "execute_uploads" }
func (ExecuteUploadsCommand) userIntent()         {}
func (ExecuteUploadsCommand) operationKind() api.OperationKind {
	return api.OperationKindUploadExecute
}
func (c ExecuteUploadsCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		NoSeed           bool
		TrackerIDs       []api.TrackerID
	}{c.ExpectedRevision, c.NoSeed, c.TrackerIDs})
}

// RetryFailedUploadsCommand retries only failed trackers from one exact prior result.
type RetryFailedUploadsCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Retry            api.FailedTrackerRetryRef
	NoSeed           bool
	IdempotencyKey   string
}

func (RetryFailedUploadsCommand) commandName() string { return "retry_failed_uploads" }
func (RetryFailedUploadsCommand) userIntent()         {}
func (RetryFailedUploadsCommand) operationKind() api.OperationKind {
	return api.OperationKindUploadExecute
}
func (c RetryFailedUploadsCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		Retry            api.FailedTrackerRetryRef
		NoSeed           bool
	}{c.ExpectedRevision, c.Retry, c.NoSeed})
}

// RetryClientInjectionsCommand retries retained client effects without
// rebuilding or resubmitting a tracker upload.
type RetryClientInjectionsCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Retry            api.ClientInjectionRetryRef
	IdempotencyKey   string
}

func (RetryClientInjectionsCommand) commandName() string { return "retry_client_injections" }
func (RetryClientInjectionsCommand) userIntent()         {}
func (RetryClientInjectionsCommand) operationKind() api.OperationKind {
	return api.OperationKindClientInjection
}
func (c RetryClientInjectionsCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		Retry            api.ClientInjectionRetryRef
	}{c.ExpectedRevision, c.Retry})
}

// CancelWorkflowCommand terminates a workflow and releases all private authority.
type CancelWorkflowCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Reason           string
	IdempotencyKey   string
}

func (CancelWorkflowCommand) commandName() string              { return "cancel_workflow" }
func (CancelWorkflowCommand) userIntent()                      {}
func (CancelWorkflowCommand) operationKind() api.OperationKind { return api.OperationKindUnknown }
func (c CancelWorkflowCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		Reason string
	}{c.Reason})
}

// InvalidateTrackersCommand selectively invalidates tracker projections and results.
type InvalidateTrackersCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	TrackerIDs       []api.TrackerID
	Reason           string
	IdempotencyKey   string
}

func (InvalidateTrackersCommand) commandName() string              { return "invalidate_trackers" }
func (InvalidateTrackersCommand) userIntent()                      {}
func (InvalidateTrackersCommand) operationKind() api.OperationKind { return api.OperationKindUnknown }
func (c InvalidateTrackersCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		TrackerIDs []api.TrackerID
		Reason     string
	}{c.TrackerIDs, c.Reason})
}

// ResolveActionCommand resolves one pending action with stale-revision protection.
type ResolveActionCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Answer           api.RequiredActionAnswer
	IdempotencyKey   string
}

func (ResolveActionCommand) commandName() string              { return "resolve_action" }
func (ResolveActionCommand) userIntent()                      {}
func (ResolveActionCommand) operationKind() api.OperationKind { return api.OperationKindUnknown }

// CommandOperationKind classifies one user intent without transport-owned type switches.
func CommandOperationKind(command Command) (api.OperationKind, bool) {
	if command == nil {
		return api.OperationKindUnknown, false
	}
	kind := command.operationKind()
	return kind, kind != api.OperationKindUnknown
}
func (c ResolveActionCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(c.Answer)
}

func canonicalCommandFingerprint(value any) (api.WorkflowFingerprint, error) {
	fingerprint, err := api.CanonicalWorkflowFingerprint(value)
	if err != nil {
		return "", fmt.Errorf("canonical command fingerprint: %w", err)
	}
	return fingerprint, nil
}
