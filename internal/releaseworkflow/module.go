// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

type randomIDGenerator struct{}

type operationExecutionContextKey struct{}

func (randomIDGenerator) NewID(prefix string) (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate workflow id: %w", err)
	}
	return strings.TrimSpace(prefix) + "_" + hex.EncodeToString(bytes[:]), nil
}

const (
	workflowPreviewTTL    = 15 * time.Minute
	workflowWorkLeaseTTL  = time.Minute
	workflowWorkHeartbeat = 20 * time.Second
	workflowCommandTTL    = 24 * time.Hour
)

// Option configures a workflow module.
type Option func(*Module) error

// WithClock installs a deterministic workflow clock.
func WithClock(clock Clock) Option {
	return func(module *Module) error {
		if clock == nil {
			return errors.New("release workflow: clock is required")
		}
		module.clock = clock
		return nil
	}
}

// WithIDGenerator installs an opaque ID generator.
func WithIDGenerator(generator IDGenerator) Option {
	return func(module *Module) error {
		if generator == nil {
			return errors.New("release workflow: id generator is required")
		}
		module.ids = generator
		return nil
	}
}

// WithProcessEpoch installs a process identity for deterministic restart
// tests. Production callers use a random epoch created by [New].
func WithProcessEpoch(epoch string) Option {
	return func(module *Module) error {
		epoch = strings.TrimSpace(epoch)
		if epoch == "" {
			return errors.New("release workflow: process epoch is required")
		}
		module.processEpoch = epoch
		return nil
	}
}

// WithTrackerProjectionBuilder installs tracker catalog/runtime/projection ownership.
func WithTrackerProjectionBuilder(builder TrackerProjectionBuilder) Option {
	return func(module *Module) error {
		if builder == nil {
			return errors.New("release workflow: tracker projection builder is required")
		}
		module.trackerProjector = builder
		return nil
	}
}

// WithTrackerPreflightBuilder installs projection-bound live readiness checks.
func WithTrackerPreflightBuilder(builder TrackerPreflightBuilder) Option {
	return func(module *Module) error {
		if builder == nil {
			return errors.New("release workflow: tracker preflight builder is required")
		}
		module.trackerPreflight = builder
		return nil
	}
}

// WithDupeAssessmentBuilder installs projection-bound duplicate execution.
func WithDupeAssessmentBuilder(builder DupeAssessmentBuilder) Option {
	return func(module *Module) error {
		if builder == nil {
			return errors.New("release workflow: duplicate assessment builder is required")
		}
		module.dupeBuilder = builder
		return nil
	}
}

// WithMediaArtifactBuilder installs generation-bound media capture.
func WithMediaArtifactBuilder(builder MediaArtifactBuilder) Option {
	return func(module *Module) error {
		if builder == nil {
			return errors.New("release workflow: media artifact builder is required")
		}
		module.mediaBuilder = builder
		return nil
	}
}

// WithDescriptionBuilder installs retained description generation.
func WithDescriptionBuilder(builder DescriptionBuilder) Option {
	return func(module *Module) error {
		if builder == nil {
			return errors.New("release workflow: description builder is required")
		}
		module.descriptionBuilder = builder
		return nil
	}
}

// WithUploadPlanBuilder installs exact reviewed-operation planning.
func WithUploadPlanBuilder(builder UploadPlanBuilder) Option {
	return func(module *Module) error {
		if builder == nil {
			return errors.New("release workflow: upload plan builder is required")
		}
		module.uploadPlanBuilder = builder
		return nil
	}
}

// WithOperationErrorClassifier installs the application boundary that maps
// asynchronous command errors to transport-safe operation failures.
func WithOperationErrorClassifier(classifier func(api.OperationKind, error) error) Option {
	return func(module *Module) error {
		if classifier == nil {
			return errors.New("release workflow: operation error classifier is required")
		}
		module.operationErrorClassifier = classifier
		return nil
	}
}

// WithLogger installs the application logger used for sanitized asynchronous
// operation diagnostics.
func WithLogger(logger api.Logger) Option {
	return func(module *Module) error {
		if logger == nil {
			return errors.New("release workflow: logger is required")
		}
		module.logger = logger
		return nil
	}
}

// Module owns workflow sequencing, invalidation, idempotency, and private retention.
type Module struct {
	repository               Repository
	operations               OperationRepository
	durability               DurabilityRepository
	private                  PrivateResourceStore
	preparer                 ReleasePreparer
	trackerProjector         TrackerProjectionBuilder
	trackerPreflight         TrackerPreflightBuilder
	dupeBuilder              DupeAssessmentBuilder
	mediaBuilder             MediaArtifactBuilder
	descriptionBuilder       DescriptionBuilder
	uploadPlanBuilder        UploadPlanBuilder
	operationErrorClassifier func(api.OperationKind, error) error
	logger                   api.Logger
	clock                    Clock
	ids                      IDGenerator
	processEpoch             string
	locksMu                  sync.Mutex
	locks                    map[string]*sync.Mutex
	operationLocksMu         sync.Mutex
	operationLocks           map[api.WorkflowOperationID]*sync.Mutex
	operationWorkersMu       sync.Mutex
	operationWorkers         map[api.WorkflowOperationID]operationWorker
	operationRecovery        sync.Once
	recoverError             error
}

type operationWorker struct {
	cancel context.CancelFunc
	done   <-chan struct{}
}

// New constructs the deep release-workflow application module.
func New(repository Repository, privateStore PrivateResourceStore, preparer ReleasePreparer, options ...Option) (*Module, error) {
	if repository == nil || privateStore == nil || preparer == nil {
		return nil, errors.New("release workflow: repository, private store, and preparer are required")
	}
	operations, ok := repository.(OperationRepository)
	if !ok || operations == nil {
		return nil, errors.New("release workflow: operation repository is required")
	}
	durability, ok := repository.(DurabilityRepository)
	if !ok || durability == nil {
		return nil, errors.New("release workflow: durability repository is required")
	}
	processEpoch, err := (randomIDGenerator{}).NewID("process")
	if err != nil {
		return nil, fmt.Errorf("release workflow: process epoch: %w", err)
	}
	module := &Module{
		repository:               repository,
		operations:               operations,
		durability:               durability,
		private:                  privateStore,
		preparer:                 preparer,
		operationErrorClassifier: func(_ api.OperationKind, err error) error { return err },
		logger:                   api.NopLogger{},
		clock:                    systemClock{},
		ids:                      randomIDGenerator{},
		processEpoch:             processEpoch,
		locks:                    make(map[string]*sync.Mutex),
		operationLocks:           make(map[api.WorkflowOperationID]*sync.Mutex),
		operationWorkers:         make(map[api.WorkflowOperationID]operationWorker),
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(module); err != nil {
			return nil, err
		}
	}
	return module, nil
}

// Execute applies one typed command with owner scoping, optimistic concurrency, and idempotency.
func (m *Module) Execute(ctx context.Context, ownerID string, command Command) (CommandResult, error) {
	return m.execute(ctx, ownerID, command)
}

func (m *Module) execute(ctx context.Context, ownerID string, command mutation) (CommandResult, error) {
	if ctx == nil {
		return CommandResult{}, errors.New("release workflow: context is required")
	}
	if err := ctx.Err(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow command: %w", err)
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || command == nil {
		return CommandResult{}, errors.New("release workflow: owner and command are required")
	}
	fingerprint, err := acceptedCommandFingerprint(command)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow %s fingerprint: %w", command.commandName(), err)
	}
	if create, ok := command.(CreateWorkflowCommand); ok {
		lock := m.commandLock(ownerID + "\x00create\x00" + strings.TrimSpace(create.IdempotencyKey))
		lock.Lock()
		defer lock.Unlock()
		return m.create(ctx, ownerID, create, fingerprint)
	}

	workflowID, expectedRevision, idempotencyKey, err := commandTarget(command)
	if err != nil {
		return CommandResult{}, err
	}
	if err := m.requireOperationOwnership(ctx, ownerID, workflowID); err != nil {
		return CommandResult{}, err
	}
	lock := m.commandLock(ownerID + "\x00" + string(workflowID))
	lock.Lock()
	defer lock.Unlock()
	if err := m.requireOperationOwnership(ctx, ownerID, workflowID); err != nil {
		return CommandResult{}, err
	}

	state, err := m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow load: %w", err)
	}
	if err := m.recoverAfterRestart(ctx, ownerID, &state); err != nil {
		return CommandResult{}, err
	}
	receiptKey := commandReceiptKey(command.commandName(), idempotencyKey, fingerprint)
	if receipt, ok := state.Receipts[receiptKey]; ok {
		if receipt.Fingerprint != fingerprint {
			return CommandResult{}, ErrIdempotencyConflict
		}
		if commandFinalizesMedia(command) {
			if err := m.finalizeRetainedMedia(ctx, ownerID, state.Workflow.ID, receipt.Result.Media, true); err != nil {
				return CommandResult{}, err
			}
		}
		return cloneCommandResult(receipt.Result)
	}
	if state.Workflow.Revision != expectedRevision {
		return CommandResult{}, ErrRevisionConflict
	}

	priorWorkflow := state.Workflow
	now := m.clock.Now().UTC()
	nextRevision := state.Workflow.Revision + 1
	result, err := m.apply(ctx, ownerID, &state, nextRevision, now, command)
	if err != nil {
		return CommandResult{}, err
	}
	state.Workflow.Revision = nextRevision
	state.Workflow.UpdatedAt = now
	result.Workflow = state.Workflow
	if operationID, _ := ctx.Value(operationExecutionContextKey{}).(api.WorkflowOperationID); operationID != "" {
		m.checkpointCompositeStage(&state, operationID, command, nextRevision)
	}
	if err := state.Workflow.Validate(); err != nil {
		m.cleanupUncommittedResult(ownerID, state.Workflow.ID, result)
		return CommandResult{}, fmt.Errorf("release workflow validate transition: %w", err)
	}
	if state.Receipts == nil {
		state.Receipts = make(map[string]commandReceipt)
	}
	state.Receipts[receiptKey] = commandReceipt{Fingerprint: fingerprint, Result: result}
	if err := ctx.Err(); err != nil {
		m.cleanupUncommittedResult(ownerID, state.Workflow.ID, result)
		return CommandResult{}, fmt.Errorf("release workflow command canceled before commit: %w", err)
	}
	if commandCommitsMediaBeforeSave(command) {
		if err := m.finalizeRetainedMedia(ctx, ownerID, state.Workflow.ID, result.Media, true); err != nil {
			m.cleanupUncommittedResult(ownerID, state.Workflow.ID, result)
			return CommandResult{}, err
		}
	}
	if err := m.repository.Save(ctx, ownerID, expectedRevision, state); err != nil {
		m.cleanupUncommittedResult(ownerID, state.Workflow.ID, result)
		return CommandResult{}, fmt.Errorf("release workflow save: %w", err)
	}
	if result.Media != nil {
		m.cleanupSupersededMediaResources(ownerID, state.Workflow.ID, priorWorkflow, state.Workflow)
	}
	if commandFinalizesMedia(command) && !commandCommitsMediaBeforeSave(command) {
		if err := m.finalizeRetainedMedia(ctx, ownerID, state.Workflow.ID, result.Media, true); err != nil {
			return CommandResult{}, err
		}
	}
	return cloneCommandResult(result)
}

func (m *Module) cleanupUncommittedResult(ownerID string, workflowID api.WorkflowID, result CommandResult) {
	if result.Media != nil {
		m.private.Delete(ownerID, workflowID, mediaPrivateResourceID(result.Media.ID))
	}
	if result.DryRun != nil {
		m.private.Delete(ownerID, workflowID, uploadPlanPrivateResourceID(result.DryRun.ID))
	}
	if result.UploadResult != nil {
		m.private.Delete(ownerID, workflowID, registeredArtifactAuthorityPrivateResourceID(result.UploadResult.ID))
	}
}

func commandFinalizesMedia(command mutation) bool {
	switch command.(type) {
	case DeleteMediaArtifactsCommand, RemoveHostedImagesCommand:
		return true
	default:
		return false
	}
}

// commandCommitsMediaBeforeSave selects commands whose staged local deletions
// commit before the snapshot save: a failed commit then fails the command while
// durable state still owns the artifact, so any retry is a fresh, valid delete.
// Hosted-image removal stays post-save because its cleanup belongs to the
// already-committed hosted-image revision.
func commandCommitsMediaBeforeSave(command mutation) bool {
	switch command.(type) {
	case DeleteMediaArtifactsCommand:
		return true
	default:
		return false
	}
}

func (m *Module) finalizeRetainedMedia(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	media *api.MediaArtifactSet,
	strict bool,
) error {
	if media == nil {
		return nil
	}
	value, err := m.private.Get(ownerID, workflowID, mediaPrivateResourceID(media.ID), m.clock.Now().UTC())
	if err != nil {
		if !strict && errors.Is(err, ErrPrivateResourceUnavailable) {
			return nil
		}
		return fmt.Errorf("release workflow finalize media mutation: %w", err)
	}
	committer, ok := value.(RetainedMediaCommitter)
	if !ok {
		return nil
	}
	if err := committer.Commit(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("release workflow finalize media mutation: %w", err)
	}
	return nil
}

func (m *Module) cleanupSupersededMediaResources(
	ownerID string,
	workflowID api.WorkflowID,
	prior api.ReleaseWorkflow,
	current api.ReleaseWorkflow,
) {
	if prior.Media != nil && (current.Media == nil || *prior.Media != *current.Media) {
		m.private.Delete(ownerID, workflowID, mediaPrivateResourceID(prior.Media.ID))
	}
	if prior.Descriptions != nil && (current.Descriptions == nil || *prior.Descriptions != *current.Descriptions) {
		m.private.Delete(ownerID, workflowID, descriptionPrivateResourceID(prior.Descriptions.ID))
	}
}

// Start durably accepts one long-running command and returns its queued
// operation before server-owned background work begins.
func (m *Module) Start(ctx context.Context, ownerID string, command Command) (api.WorkflowOperationStatus, error) {
	if ctx == nil {
		return api.WorkflowOperationStatus{}, errors.New("release workflow: context is required")
	}
	if err := ctx.Err(); err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow start operation: %w", err)
	}
	if err := m.ensureOperationRecovery(ctx); err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || command == nil {
		return api.WorkflowOperationStatus{}, errors.New("release workflow: owner and command are required")
	}
	operationKind, ok := operationKindForCommand(command)
	if !ok {
		return api.WorkflowOperationStatus{}, fmt.Errorf("%w: command %s is not long-running", ErrInvalidTransition, command.commandName())
	}
	workflowID, expectedRevision, idempotencyKey, err := commandTarget(command)
	if err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	fingerprint, err := acceptedCommandFingerprint(command)
	if err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow %s fingerprint: %w", command.commandName(), err)
	}
	legacyFingerprint, err := command.commandFingerprint()
	if err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow %s legacy fingerprint: %w", command.commandName(), err)
	}
	if prior, found, loadErr := m.loadActiveOperationReceipt(
		ctx,
		ownerID,
		workflowID,
		command.commandName(),
		idempotencyKey,
		fingerprint,
	); loadErr != nil {
		return api.WorkflowOperationStatus{}, loadErr
	} else if found {
		return prior, nil
	}
	lock := m.commandLock(ownerID + "\x00" + string(workflowID))
	lock.Lock()
	defer lock.Unlock()
	state, err := m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow start load: %w", err)
	}
	if err := m.recoverAfterRestart(ctx, ownerID, &state); err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	if prior, found, loadErr := m.loadOperationReceipt(
		ctx,
		ownerID,
		workflowID,
		command.commandName(),
		idempotencyKey,
		fingerprint,
		legacyFingerprint,
		state,
	); loadErr != nil {
		return api.WorkflowOperationStatus{}, loadErr
	} else if found {
		return prior, nil
	}
	if state.Workflow.Revision != expectedRevision {
		return api.WorkflowOperationStatus{}, ErrRevisionConflict
	}
	operationID, err := m.newID("operation")
	if err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	now := m.clock.Now().UTC()
	status := api.WorkflowOperationStatus{
		ID:         api.WorkflowOperationID(operationID),
		WorkflowID: workflowID,
		Revision:   expectedRevision,
		Sequence:   1,
		Command:    command.commandName(),
		Operation:  operationKind,
		Phase:      command.commandName(),
		Status:     api.StageStatusQueued,
		Message:    "Operation queued.",
		StartedAt:  now,
		UpdatedAt:  now,
	}
	if composite, ok := compositeUploadCommandTarget(command); ok {
		if state.Composite == nil || state.Composite.RequestFingerprint != composite.SessionFingerprint ||
			state.Composite.ActiveOperationID != "" {
			return api.WorkflowOperationStatus{}, ErrOperationConflict
		}
		state.Composite.ActiveOperationID = status.ID
		state.Composite.LastOperationID = status.ID
		state.Composite.TerminalReason = ""
		if err := m.saveCompositeMetadata(ctx, ownerID, &state); err != nil {
			return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow bind composite operation: %w", err)
		}
		status.Items = compositeUploadInitialItems()
		status.Total = len(compositeUploadStages) * 100
	}
	commandResourceID := operationCommandResourceID(status.ID)
	if err := m.private.Put(
		ownerID,
		workflowID,
		commandResourceID,
		durableOperationCommand{command: command},
		now.Add(workflowCommandTTL),
	); err != nil {
		if state.Composite != nil && state.Composite.ActiveOperationID == status.ID {
			state.Composite.ActiveOperationID = ""
			state.Composite.TerminalReason = "start_failed"
			_ = m.saveCompositeMetadata(ctx, ownerID, &state)
		}
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow retain operation command: %w", err)
	}
	record, idempotent, err := m.operations.CreateOperation(ctx, api.ReleaseWorkflowOperationRecord{
		OwnerID:            ownerID,
		WorkflowID:         workflowID,
		OperationID:        status.ID,
		ExpectedRevision:   expectedRevision,
		IdempotencyKey:     strings.TrimSpace(idempotencyKey),
		CommandFingerprint: fingerprint,
		ProcessEpoch:       m.processEpoch,
		Status:             status,
	})
	if err != nil {
		m.private.Delete(ownerID, workflowID, commandResourceID)
		if state.Composite != nil && state.Composite.ActiveOperationID == status.ID {
			state.Composite.ActiveOperationID = ""
			state.Composite.TerminalReason = "start_failed"
			_ = m.saveCompositeMetadata(ctx, ownerID, &state)
		}
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow create operation: %w", err)
	}
	if idempotent {
		m.private.Delete(ownerID, workflowID, commandResourceID)
		return cloneWorkflowOperationStatus(record.Status, "start idempotent operation")
	}
	if err := m.durability.ClaimWork(ctx, workflowWorkRecord(record, status, now, nil)); err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow claim operation work: %w", err)
	}
	events, err := m.durability.AppendEvents(ctx, ownerID, workflowID, projectOperationEvents(status))
	if err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow append queued operation event: %w", err)
	}
	status.Events = events
	m.dispatchOperationWorker(ctx, ownerID, command, record)
	return cloneWorkflowOperationStatus(status, "start operation")
}

func (m *Module) dispatchOperationWorker(
	ctx context.Context,
	ownerID string,
	command Command,
	record api.ReleaseWorkflowOperationRecord,
) {
	workerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	workerDone := make(chan struct{})
	m.operationWorkersMu.Lock()
	if _, exists := m.operationWorkers[record.OperationID]; exists {
		m.operationWorkersMu.Unlock()
		cancel()
		return
	}
	m.operationWorkers[record.OperationID] = operationWorker{cancel: cancel, done: workerDone}
	m.operationWorkersMu.Unlock()
	go func() {
		defer cancel()
		defer func() {
			close(workerDone)
			m.operationWorkersMu.Lock()
			delete(m.operationWorkers, record.OperationID)
			m.operationWorkersMu.Unlock()
		}()
		m.runOperation(workerCtx, ownerID, command, record)
	}()
}

func (m *Module) loadActiveOperationReceipt(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	commandName string,
	idempotencyKey string,
	fingerprint api.WorkflowFingerprint,
) (api.WorkflowOperationStatus, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return api.WorkflowOperationStatus{}, false, nil
	}
	prior, err := m.operations.LoadOperationByIdempotency(ctx, ownerID, workflowID, commandName, idempotencyKey)
	switch {
	case err == nil:
		if isTerminalProgressStatus(prior.Status.Status) {
			return api.WorkflowOperationStatus{}, false, nil
		}
		if prior.CommandFingerprint != fingerprint {
			return api.WorkflowOperationStatus{}, false, ErrOperationConflict
		}
		status, cloneErr := cloneWorkflowOperationStatus(prior.Status, "load active operation receipt")
		return status, true, cloneErr
	case errors.Is(err, ErrWorkflowNotFound):
		return api.WorkflowOperationStatus{}, false, nil
	default:
		return api.WorkflowOperationStatus{}, false, fmt.Errorf("release workflow load active operation receipt: %w", err)
	}
}

func (m *Module) loadOperationReceipt(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	commandName string,
	idempotencyKey string,
	fingerprint api.WorkflowFingerprint,
	legacyFingerprint api.WorkflowFingerprint,
	state State,
) (api.WorkflowOperationStatus, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return api.WorkflowOperationStatus{}, false, nil
	}
	prior, err := m.operations.LoadOperationByIdempotency(
		ctx,
		ownerID,
		workflowID,
		commandName,
		idempotencyKey,
	)
	switch {
	case err == nil:
		if prior.CommandFingerprint != fingerprint {
			if prior.CommandFingerprint == legacyFingerprint {
				stale, staleErr := m.markOperationResultStale(ctx, prior)
				return stale, true, staleErr
			}
			return api.WorkflowOperationStatus{}, false, ErrOperationConflict
		}
		if operationClaimsSuccessfulResult(prior.Status) && !m.operationResultIsCurrent(ownerID, prior.Status.Result, state) {
			stale, staleErr := m.markOperationResultStale(ctx, prior)
			return stale, true, staleErr
		}
		if isTerminalProgressStatus(prior.Status.Status) {
			if waitErr := m.waitForOperationCleanup(ctx, prior.OperationID); waitErr != nil {
				return api.WorkflowOperationStatus{}, false, waitErr
			}
		}
		status, cloneErr := cloneWorkflowOperationStatus(prior.Status, "load operation receipt")
		return status, true, cloneErr
	case errors.Is(err, ErrWorkflowNotFound):
		return api.WorkflowOperationStatus{}, false, nil
	default:
		return api.WorkflowOperationStatus{}, false, fmt.Errorf("release workflow load operation receipt: %w", err)
	}
}

func operationClaimsSuccessfulResult(status api.WorkflowOperationStatus) bool {
	return status.Status == api.StageStatusCompleted || status.Status == api.StageStatusPartial || status.Status == api.StageStatusExecuted
}

func (m *Module) operationResultIsCurrent(ownerID string, result *api.WorkflowOperationResult, state State) bool {
	if result == nil || result.WorkflowRevision == 0 || result.RefRevision == 0 || strings.TrimSpace(result.RefID) == "" {
		return false
	}
	switch result.Kind {
	case api.WorkflowOperationResultRelease:
		ref := state.Workflow.Release
		snapshot, ok := state.Releases[api.ReleaseSnapshotID(result.RefID)]
		return ref != nil && string(ref.ID) == result.RefID && ref.Revision == result.RefRevision && ok && snapshot.Revision == result.RefRevision
	case api.WorkflowOperationResultProjections:
		ref := state.Workflow.TrackerProjections
		snapshot, ok := state.Projections[api.TrackerReleaseProjectionSetID(result.RefID)]
		return ref != nil && string(ref.ID) == result.RefID && ref.Revision == result.RefRevision && ok && snapshot.Revision == result.RefRevision
	case api.WorkflowOperationResultPreflight:
		ref := state.Workflow.TrackerPreflight
		snapshot, ok := state.Preflights[api.TrackerPreflightAssessmentID(result.RefID)]
		return ref != nil && string(ref.ID) == result.RefID && ref.Revision == result.RefRevision && ok && snapshot.Revision == result.RefRevision
	case api.WorkflowOperationResultDupes:
		ref := state.Workflow.Dupes
		snapshot, ok := state.Dupes[api.DupeAssessmentID(result.RefID)]
		return ref != nil && string(ref.ID) == result.RefID && ref.Revision == result.RefRevision && ok && snapshot.Revision == result.RefRevision
	case api.WorkflowOperationResultMedia:
		ref := state.Workflow.Media
		snapshot, ok := state.Media[api.MediaArtifactSetID(result.RefID)]
		return ref != nil && string(ref.ID) == result.RefID && ref.Revision == result.RefRevision && ok && snapshot.Revision == result.RefRevision
	case api.WorkflowOperationResultDescriptions:
		ref := state.Workflow.Descriptions
		snapshot, ok := state.Descriptions[api.DescriptionSetID(result.RefID)]
		return ref != nil && string(ref.ID) == result.RefID && ref.Revision == result.RefRevision && ok && snapshot.Revision == result.RefRevision
	case api.WorkflowOperationResultDryRun:
		ref := state.Workflow.DryRun
		snapshot, ok := state.DryRuns[api.UploadDryRunResultID(result.RefID)]
		if ref == nil || string(ref.ID) != result.RefID || ref.Revision != result.RefRevision || !ok || snapshot.Revision != result.RefRevision {
			return false
		}
		_, err := m.private.Get(
			ownerID,
			state.Workflow.ID,
			uploadPlanPrivateResourceID(snapshot.ID),
			m.clock.Now().UTC(),
		)
		return err == nil
	case api.WorkflowOperationResultUpload:
		ref := state.Workflow.UploadResult
		snapshot, ok := state.UploadResults[api.UploadResultID(result.RefID)]
		return ref != nil && string(ref.ID) == result.RefID && ref.Revision == result.RefRevision && ok && snapshot.Revision == result.RefRevision
	}
	return false
}

func (m *Module) markOperationResultStale(
	ctx context.Context,
	record api.ReleaseWorkflowOperationRecord,
) (api.WorkflowOperationStatus, error) {
	status, err := m.mutateOperation(ctx, record.OwnerID, record.WorkflowID, record.OperationID, func(status *api.WorkflowOperationStatus) {
		now := m.clock.Now().UTC()
		status.Status = api.StageStatusStale
		status.Message = "The operation result is no longer current. Reprepare the workflow."
		status.ResultRevision = 0
		status.Result = nil
		status.CompletedAt = &now
		status.Failures = []api.WorkflowFailure{{
			Failure: api.OperationFailure{
				Code:      api.OperationFailureStaleResult,
				Operation: status.Operation,
				Message:   "The operation result is no longer current. Reprepare the workflow.",
				Recovery:  api.OperationRecoveryReprepare,
			},
		}}
	})
	if err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow mark stale operation result: %w", err)
	}
	return status, nil
}

func operationKindForCommand(command Command) (api.OperationKind, bool) {
	return CommandOperationKind(command)
}

func (m *Module) requireOperationOwnership(ctx context.Context, ownerID string, workflowID api.WorkflowID) error {
	records, err := m.operations.ListActiveOperations(ctx)
	if err != nil {
		return fmt.Errorf("release workflow list active operations: %w", err)
	}
	operationID, _ := ctx.Value(operationExecutionContextKey{}).(api.WorkflowOperationID)
	for _, record := range records {
		if record.OwnerID != ownerID || record.WorkflowID != workflowID {
			continue
		}
		if operationID == record.OperationID {
			return nil
		}
		return ErrOperationConflict
	}
	return nil
}

func (m *Module) runOperation(
	ctx context.Context,
	ownerID string,
	command Command,
	record api.ReleaseWorkflowOperationRecord,
) {
	workerLeaseCtx, stopLease := context.WithCancel(ctx)
	defer stopLease()
	go m.renewOperationWorkLease(workerLeaseCtx, record)
	_, runningErr := m.mutateOperation(ctx, record.OwnerID, record.WorkflowID, record.OperationID, func(status *api.WorkflowOperationStatus) {
		status.Status = api.StageStatusRunning
		status.Message = "Operation running."
	})
	if runningErr != nil {
		return
	}
	workerCtx := context.WithValue(ctx, operationExecutionContextKey{}, record.OperationID)
	closeProgress := func() {}
	if _, composite := compositeUploadCommandTarget(command); composite {
		workerCtx = api.WithWorkflowExternalEffectReporter(
			workerCtx,
			newDurableExternalEffectReporter(m, record.OwnerID, record.WorkflowID, record.OperationID),
		)
	} else {
		workerCtx, closeProgress = m.withOperationReporters(workerCtx, record.OwnerID, record.WorkflowID, record.OperationID)
	}
	var (
		result CommandResult
		err    error
	)
	if composite, ok := compositeUploadCommandTarget(command); ok {
		result, err = m.runCompositeUpload(workerCtx, ownerID, composite)
	} else {
		result, err = m.Execute(workerCtx, ownerID, command)
	}
	closeProgress()
	stopLease()
	terminalStatus := api.StageStatusFailed
	var operationResult *api.WorkflowOperationResult
	if err == nil {
		if _, composite := compositeUploadCommandTarget(command); composite {
			terminalStatus = compositeUploadTerminalStatus(result)
			operationResult = compositeUploadResult(result)
		} else {
			terminalStatus = terminalOperationStatus(command, result)
			operationResult, err = operationResultForCommand(command, result)
		}
		requiresResult := operationClaimsSuccessfulResult(api.WorkflowOperationStatus{Status: terminalStatus})
		if _, composite := compositeUploadCommandTarget(command); composite && terminalStatus == api.StageStatusPartial {
			requiresResult = false
		}
		if err == nil && requiresResult && operationResult == nil {
			err = fmt.Errorf("release workflow %s terminal result descriptor is missing", command.commandName())
		}
	}
	privateErr := err
	if err != nil {
		err = m.operationErrorClassifier(record.Status.Operation, err)
		m.logger.Errorf(
			"releaseworkflow: command=%s operation=%s stage=%s state=failed cause=%s",
			command.commandName(),
			record.Status.Operation,
			record.Status.Phase,
			logging.SanitizeMessage(privateErr.Error()),
		)
	}
	if _, terminalErr := m.completeOperation(ctx, record, func(status *api.WorkflowOperationStatus) {
		now := m.clock.Now().UTC()
		status.CompletedAt = &now
		switch {
		case errors.Is(err, context.Canceled):
			status.Status = api.StageStatusCanceled
			status.Message = "Operation canceled."
		case err != nil:
			status.Status = api.StageStatusFailed
			failure, ok := api.AsOperationFailure(err)
			if !ok {
				failure = api.OperationFailure{
					Code:      api.OperationFailureInternal,
					Operation: status.Operation,
					Message:   "The operation could not be completed.",
					Recovery:  api.OperationRecoveryRetry,
				}
			}
			status.Message = failure.Message
			status.Failures = []api.WorkflowFailure{{Failure: failure}}
		default:
			status.Status = terminalStatus
			status.ResultRevision = result.Workflow.Revision
			status.Result = operationResult
			if terminalStatus != api.StageStatusBlocked {
				status.Progress = 100
				status.Completed = max(status.Completed, status.Total)
			}
			switch status.Status {
			case api.StageStatusBlocked:
				status.Message = "Operation requires action."
			case api.StageStatusFailed:
				status.Message = "Operation completed with retained failures."
				status.Failures = append([]api.WorkflowFailure(nil), result.Workflow.Failures...)
			case api.StageStatusPartial:
				status.Message = "Operation completed with mixed outcomes."
			case api.StageStatusExecuted:
				status.Message = "Operation executed."
			case api.StageStatusPending,
				api.StageStatusQueued,
				api.StageStatusReady,
				api.StageStatusStale,
				api.StageStatusSkipped,
				api.StageStatusRunning,
				api.StageStatusCompleted,
				api.StageStatusInterrupted,
				api.StageStatusCanceled,
				api.StageStatusUnavailable:
				status.Message = "Operation complete."
			}
		}
	}); terminalErr != nil && !errors.Is(terminalErr, ErrOperationConflict) {
		m.logger.Warnf(
			"releaseworkflow: workflow=%s operation=%s stage=work_completion state=retry_pending cause=%s",
			record.WorkflowID,
			record.OperationID,
			logging.SanitizeMessage(terminalErr.Error()),
		)
	}
}

func (m *Module) completeOperation(
	ctx context.Context,
	record api.ReleaseWorkflowOperationRecord,
	mutate func(*api.WorkflowOperationStatus),
) (api.WorkflowOperationStatus, error) {
	lock := m.operationLock(record.OperationID)
	lock.Lock()
	defer lock.Unlock()

	operationCtx := context.WithoutCancel(ctx)
	current, err := m.operations.LoadOperation(operationCtx, record.OwnerID, record.WorkflowID, record.OperationID)
	if err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow load operation for completion: %w", err)
	}
	if current.ProcessEpoch != m.processEpoch || !workflowOperationActive(current.Status.Status) {
		return api.WorkflowOperationStatus{}, ErrOperationConflict
	}
	expectedSequence := current.Status.Sequence
	previousStatus, err := cloneWorkflowOperationStatus(current.Status, "capture operation state before completion")
	if err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	current.Status.Events = nil
	mutate(&current.Status)
	current.Status.Sequence = expectedSequence + 1
	current.Status.UpdatedAt = m.clock.Now().UTC()
	current.ProcessEpoch = m.processEpoch
	sanitizeWorkflowOperationStatus(&current.Status)
	eventChanges := projectOperationEventChanges(previousStatus, current.Status)
	current.Status.Events = eventChanges

	completedAt := current.Status.UpdatedAt
	if err := m.durability.CompleteWork(
		operationCtx,
		workflowWorkRecord(current, current.Status, current.Status.UpdatedAt, &completedAt),
	); err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow complete operation work: %w", err)
	}
	if err := m.operations.SaveOperation(operationCtx, expectedSequence, current); err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow publish completed operation: %w", err)
	}
	m.private.Delete(current.OwnerID, current.WorkflowID, operationCommandResourceID(current.OperationID))
	events, err := m.durability.AppendEvents(
		operationCtx,
		current.OwnerID,
		current.WorkflowID,
		eventChanges,
	)
	if err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow append completed operation events: %w", err)
	}
	current.Status.Events = events
	return cloneWorkflowOperationStatus(current.Status, "complete operation")
}

func (m *Module) renewOperationWorkLease(ctx context.Context, record api.ReleaseWorkflowOperationRecord) {
	ticker := time.NewTicker(workflowWorkHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := m.clock.Now().UTC()
			renewal := api.ReleaseWorkflowWorkRecord{
				OwnerID:        record.OwnerID,
				WorkflowID:     record.WorkflowID,
				OperationID:    record.OperationID,
				LeaseOwner:     m.processEpoch,
				LeaseExpiresAt: now.Add(workflowWorkLeaseTTL),
				Checkpoint:     []byte(`{}`),
				UpdatedAt:      now,
			}
			if err := m.durability.RenewWork(context.WithoutCancel(ctx), renewal); err != nil {
				m.operationWorkersMu.Lock()
				worker := m.operationWorkers[record.OperationID]
				m.operationWorkersMu.Unlock()
				if worker.cancel != nil {
					worker.cancel()
				}
				return
			}
		}
	}
}

func operationResultForCommand(command Command, result CommandResult) (*api.WorkflowOperationResult, error) {
	var (
		kind     api.WorkflowOperationResultKind
		refID    string
		revision api.WorkflowRevision
	)
	switch command.(type) {
	case PrepareReleaseCommand, ResetReleaseCommand, SelectBlurayCandidateCommand:
		kind = api.WorkflowOperationResultRelease
		if result.Release != nil {
			refID, revision = string(result.Release.ID), result.Release.Revision
		}
	case ProjectTrackersCommand:
		kind = api.WorkflowOperationResultProjections
		if result.Projections != nil {
			refID, revision = string(result.Projections.ID), result.Projections.Revision
		}
	case PreflightTrackersCommand:
		kind = api.WorkflowOperationResultPreflight
		if result.Preflight != nil {
			refID, revision = string(result.Preflight.ID), result.Preflight.Revision
		}
	case CheckDuplicatesCommand:
		kind = api.WorkflowOperationResultDupes
		if result.Dupes != nil {
			refID, revision = string(result.Dupes.ID), result.Dupes.Revision
		}
	case CaptureMediaCommand, UploadMediaImagesCommand:
		kind = api.WorkflowOperationResultMedia
		if result.Media != nil {
			refID, revision = string(result.Media.ID), result.Media.Revision
		}
	case GenerateDescriptionsCommand:
		kind = api.WorkflowOperationResultDescriptions
		if result.Descriptions != nil {
			refID, revision = string(result.Descriptions.ID), result.Descriptions.Revision
		}
	case DryRunUploadsCommand:
		kind = api.WorkflowOperationResultDryRun
		if result.DryRun != nil {
			refID, revision = string(result.DryRun.ID), result.DryRun.Revision
		}
	case ExecuteUploadsCommand, RetryFailedUploadsCommand, RetryClientInjectionsCommand:
		kind = api.WorkflowOperationResultUpload
		if result.UploadResult != nil {
			refID, revision = string(result.UploadResult.ID), result.UploadResult.Revision
		}
	default:
		return nil, nil
	}
	if strings.TrimSpace(refID) == "" || revision == 0 {
		return nil, nil
	}
	if result.Workflow.Revision == 0 || revision > result.Workflow.Revision {
		return nil, fmt.Errorf("release workflow %s terminal result lineage is invalid", command.commandName())
	}
	return &api.WorkflowOperationResult{
		Kind:             kind,
		WorkflowRevision: result.Workflow.Revision,
		RefID:            refID,
		RefRevision:      revision,
	}, nil
}

func terminalOperationStatus(command Command, result CommandResult) api.StageStatus {
	stageStatus := api.StageStatusCompleted
	switch command.(type) {
	case PrepareReleaseCommand:
		switch result.Workflow.Status {
		case api.WorkflowStatusBlocked:
			stageStatus = api.StageStatusBlocked
		case api.WorkflowStatusFailed:
			stageStatus = api.StageStatusFailed
		case api.WorkflowStatusDraft, api.WorkflowStatusActive, api.WorkflowStatusCompleted, api.WorkflowStatusCanceled:
		}
	case ProjectTrackersCommand:
		if result.Projections != nil {
			stageStatus = result.Projections.Status
		}
	case PreflightTrackersCommand:
		if result.Preflight != nil {
			stageStatus = result.Preflight.Status
		}
	case CheckDuplicatesCommand:
		if result.Dupes != nil {
			stageStatus = result.Dupes.Status
		}
	case CaptureMediaCommand:
		if result.Media != nil {
			stageStatus = result.Media.Status
		}
	case UploadMediaImagesCommand:
		if result.Media != nil && len(result.Media.HostAttempts) > 0 {
			hasHostedResult := slices.ContainsFunc(latestHostedImageAttempts(result.Media.HostAttempts), func(attempt api.HostedImageAttempt) bool {
				return len(attempt.Results) > 0
			})
			hasUnresolvedFailure := slices.ContainsFunc(result.Media.Failures, func(failure api.WorkflowFailure) bool {
				return failure.Failure.Operation == api.OperationKindImageHosting
			})
			switch {
			case hasUnresolvedFailure && hasHostedResult:
				stageStatus = api.StageStatusPartial
			case hasUnresolvedFailure:
				stageStatus = api.StageStatusFailed
			}
		}
	case GenerateDescriptionsCommand:
		if result.Descriptions != nil {
			stageStatus = result.Descriptions.Status
		}
	case DryRunUploadsCommand:
		if result.DryRun != nil {
			stageStatus = result.DryRun.Status
		}
	case ExecuteUploadsCommand, RetryFailedUploadsCommand, RetryClientInjectionsCommand:
		stageStatus = api.StageStatusExecuted
	}
	switch stageStatus {
	case api.StageStatusReady, api.StageStatusSkipped:
		return api.StageStatusCompleted
	case api.StageStatusBlocked, api.StageStatusFailed, api.StageStatusPartial, api.StageStatusExecuted:
		return stageStatus
	case api.StageStatusPending,
		api.StageStatusQueued,
		api.StageStatusStale,
		api.StageStatusRunning,
		api.StageStatusCompleted,
		api.StageStatusInterrupted,
		api.StageStatusCanceled,
		api.StageStatusUnavailable:
		return api.StageStatusCompleted
	}
	return api.StageStatusCompleted
}

func latestHostedImageAttempts(attempts []api.HostedImageAttempt) []api.HostedImageAttempt {
	var latest time.Time
	for _, attempt := range attempts {
		if attempt.AttemptedAt.After(latest) {
			latest = attempt.AttemptedAt
		}
	}
	if latest.IsZero() {
		return attempts
	}
	return slices.DeleteFunc(append([]api.HostedImageAttempt(nil), attempts...), func(attempt api.HostedImageAttempt) bool {
		return !attempt.AttemptedAt.Equal(latest)
	})
}

func (m *Module) ensureOperationRecovery(ctx context.Context) error {
	m.operationRecovery.Do(func() {
		recoveryCtx := context.WithoutCancel(ctx)
		records, err := m.operations.ListActiveOperations(recoveryCtx)
		if err != nil {
			m.recoverError = fmt.Errorf("release workflow recover operations: %w", err)
			return
		}
		for _, record := range records {
			if record.ProcessEpoch == m.processEpoch {
				continue
			}
			if err := m.recoverOperationAfterLease(recoveryCtx, record); err != nil {
				m.recoverError = err
				return
			}
		}
	})
	return m.recoverError
}

func (m *Module) recoverOperationAfterLease(
	ctx context.Context,
	record api.ReleaseWorkflowOperationRecord,
) error {
	work, err := m.durability.LoadWork(ctx, record.OwnerID, record.WorkflowID, record.OperationID)
	if err != nil {
		if errors.Is(err, ErrWorkflowNotFound) {
			return m.interruptRecoveredOperation(ctx, record, "Operation command predates durable restart recovery.")
		}
		return fmt.Errorf("release workflow load interrupted work lease: %w", err)
	}
	if work.CompletedAt != nil {
		var checkpoint api.WorkflowOperationStatus
		if err := json.Unmarshal(work.Checkpoint, &checkpoint); err != nil ||
			checkpoint.ID != record.OperationID ||
			checkpoint.WorkflowID != record.WorkflowID ||
			checkpoint.Command != record.Status.Command ||
			checkpoint.Sequence != record.Status.Sequence+1 ||
			!isTerminalProgressStatus(checkpoint.Status) ||
			checkpoint.Validate() != nil {
			return m.interruptRecoveredOperation(ctx, record, "The completed operation checkpoint failed its integrity check.")
		}
		return m.publishCompletedOperationCheckpoint(ctx, record, checkpoint)
	}
	now := m.clock.Now().UTC()
	if work.LeaseExpiresAt.After(now) {
		delay := work.LeaseExpiresAt.Sub(now)
		recoveryCtx := context.WithoutCancel(ctx)
		go func() {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			<-timer.C
			if recoveryErr := m.recoverOperationAfterLease(recoveryCtx, record); recoveryErr != nil {
				m.logger.Errorf(
					"releaseworkflow: workflow=%s operation=%s stage=restart_recovery state=failed cause=%s",
					record.WorkflowID,
					record.OperationID,
					logging.SanitizeMessage(recoveryErr.Error()),
				)
			}
		}()
		return nil
	}

	current, err := m.operations.LoadOperation(ctx, record.OwnerID, record.WorkflowID, record.OperationID)
	if err != nil {
		return fmt.Errorf("release workflow reload interrupted operation: %w", err)
	}
	if !workflowOperationActive(current.Status.Status) || current.ProcessEpoch == m.processEpoch {
		return nil
	}
	current.ProcessEpoch = m.processEpoch
	if err := m.durability.ClaimWork(
		ctx,
		workflowWorkRecord(current, current.Status, now, nil),
	); err != nil {
		if errors.Is(err, ErrOperationConflict) {
			return nil
		}
		return fmt.Errorf("release workflow claim interrupted work: %w", err)
	}
	value, err := m.private.Get(
		current.OwnerID,
		current.WorkflowID,
		operationCommandResourceID(current.OperationID),
		now,
	)
	if err != nil {
		return m.interruptRecoveredOperation(ctx, current, "The retained operation command is unavailable.")
	}
	capsule, ok := value.(durableOperationCommand)
	if !ok || capsule.command == nil {
		return m.interruptRecoveredOperation(ctx, current, "The retained operation command is invalid.")
	}
	fingerprint, err := acceptedCommandFingerprint(capsule.command)
	if err != nil || fingerprint != current.CommandFingerprint {
		return m.interruptRecoveredOperation(ctx, current, "The retained operation command failed its integrity check.")
	}
	workflowID, expectedRevision, idempotencyKey, err := commandTarget(capsule.command)
	if err != nil || workflowID != current.WorkflowID || expectedRevision != current.ExpectedRevision {
		return m.interruptRecoveredOperation(ctx, current, "The retained operation command no longer matches its work receipt.")
	}
	state, err := m.repository.Load(ctx, current.OwnerID, current.WorkflowID)
	if err != nil {
		if errors.Is(err, ErrWorkflowNotFound) {
			return m.interruptRecoveredOperation(ctx, current, "The retained operation workflow is unavailable.")
		}
		return fmt.Errorf("release workflow load interrupted operation authority: %w", err)
	}
	receiptKey := commandReceiptKey(capsule.command.commandName(), idempotencyKey, fingerprint)
	_, commandCommitted := state.Receipts[receiptKey]
	composite, compositeCommand := compositeUploadCommandTarget(capsule.command)
	compositeValid := compositeCommand && compositeUploadRecoveryValid(composite, current, state)
	if state.Workflow.Revision != current.ExpectedRevision && !commandCommitted && !compositeValid {
		return m.interruptRecoveredOperation(ctx, current, "The retained operation authority is stale.")
	}
	if err := m.durability.MarkOperationEffectsUnknown(
		ctx,
		current.OwnerID,
		current.WorkflowID,
		current.OperationID,
		now,
	); err != nil {
		return fmt.Errorf("release workflow fence interrupted effects: %w", err)
	}
	m.dispatchOperationWorker(ctx, current.OwnerID, capsule.command, current)
	return nil
}

func (m *Module) publishCompletedOperationCheckpoint(
	ctx context.Context,
	record api.ReleaseWorkflowOperationRecord,
	checkpoint api.WorkflowOperationStatus,
) error {
	lock := m.operationLock(record.OperationID)
	lock.Lock()
	defer lock.Unlock()

	current, err := m.operations.LoadOperation(ctx, record.OwnerID, record.WorkflowID, record.OperationID)
	if err != nil {
		return fmt.Errorf("release workflow load completed operation checkpoint: %w", err)
	}
	if !workflowOperationActive(current.Status.Status) {
		m.private.Delete(current.OwnerID, current.WorkflowID, operationCommandResourceID(current.OperationID))
		return nil
	}
	if checkpoint.Sequence != current.Status.Sequence+1 {
		return fmt.Errorf(
			"release workflow completed operation checkpoint sequence=%d does not follow receipt sequence=%d",
			checkpoint.Sequence,
			current.Status.Sequence,
		)
	}
	expectedSequence := current.Status.Sequence
	previousStatus, err := cloneWorkflowOperationStatus(current.Status, "capture operation state before recovered completion")
	if err != nil {
		return err
	}
	checkpoint.Events = nil
	current.Status = checkpoint
	current.ProcessEpoch = m.processEpoch
	eventChanges := projectOperationEventChanges(previousStatus, current.Status)
	current.Status.Events = eventChanges
	if err := m.operations.SaveOperation(ctx, expectedSequence, current); err != nil {
		if errors.Is(err, ErrRevisionConflict) {
			reloaded, loadErr := m.operations.LoadOperation(ctx, current.OwnerID, current.WorkflowID, current.OperationID)
			if loadErr == nil && !workflowOperationActive(reloaded.Status.Status) {
				m.private.Delete(current.OwnerID, current.WorkflowID, operationCommandResourceID(current.OperationID))
				return nil
			}
		}
		return fmt.Errorf("release workflow publish completed operation checkpoint: %w", err)
	}
	m.private.Delete(current.OwnerID, current.WorkflowID, operationCommandResourceID(current.OperationID))
	if _, err := m.durability.AppendEvents(
		ctx,
		current.OwnerID,
		current.WorkflowID,
		eventChanges,
	); err != nil {
		return fmt.Errorf("release workflow append recovered completion events: %w", err)
	}
	return nil
}

func (m *Module) interruptRecoveredOperation(
	ctx context.Context,
	record api.ReleaseWorkflowOperationRecord,
	reason string,
) error {
	now := m.clock.Now().UTC()
	terminal, err := m.mutateOperation(ctx, record.OwnerID, record.WorkflowID, record.OperationID, func(status *api.WorkflowOperationStatus) {
		status.Status = api.StageStatusInterrupted
		status.Message = "Operation interrupted by process restart. Retry the stage."
		status.CompletedAt = &now
		status.Failures = []api.WorkflowFailure{{
			Failure: api.OperationFailure{
				Code:      api.OperationFailureStaleReview,
				Operation: status.Operation,
				Message:   "Operation interrupted by process restart. Retry the stage.",
				Recovery:  api.OperationRecoveryRetry,
			},
			Resource: logging.SanitizeMessage(reason),
		}}
	})
	if err != nil {
		return fmt.Errorf("release workflow interrupt operation: %w", err)
	}
	record.ProcessEpoch = m.processEpoch
	completedAt := now
	if err := m.durability.CompleteWork(
		ctx,
		workflowWorkRecord(record, terminal, now, &completedAt),
	); err != nil && !errors.Is(err, ErrOperationConflict) {
		return fmt.Errorf("release workflow complete interrupted work: %w", err)
	}
	m.private.Delete(record.OwnerID, record.WorkflowID, operationCommandResourceID(record.OperationID))
	return nil
}

func (m *Module) operationLock(operationID api.WorkflowOperationID) *sync.Mutex {
	m.operationLocksMu.Lock()
	defer m.operationLocksMu.Unlock()
	lock, ok := m.operationLocks[operationID]
	if !ok {
		lock = &sync.Mutex{}
		m.operationLocks[operationID] = lock
	}
	return lock
}

func (m *Module) mutateOperation(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
	mutate func(*api.WorkflowOperationStatus),
) (api.WorkflowOperationStatus, error) {
	lock := m.operationLock(operationID)
	lock.Lock()
	defer lock.Unlock()
	operationCtx := context.WithoutCancel(ctx)
	record, err := m.operations.LoadOperation(operationCtx, ownerID, workflowID, operationID)
	if err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow load operation for update: %w", err)
	}
	expectedSequence := record.Status.Sequence
	previousStatus, err := cloneWorkflowOperationStatus(record.Status, "capture operation state before update")
	if err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	record.Status.Events = nil
	mutate(&record.Status)
	record.Status.Sequence = expectedSequence + 1
	record.Status.UpdatedAt = m.clock.Now().UTC()
	ownsWorkLease := record.ProcessEpoch == m.processEpoch
	record.ProcessEpoch = m.processEpoch
	sanitizeWorkflowOperationStatus(&record.Status)
	eventChanges := projectOperationEventChanges(previousStatus, record.Status)
	record.Status.Events = eventChanges
	if err := m.operations.SaveOperation(operationCtx, expectedSequence, record); err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow save operation update: %w", err)
	}
	if err := m.durability.CheckpointWork(
		operationCtx,
		workflowWorkRecord(record, record.Status, record.Status.UpdatedAt, nil),
	); err != nil && ownsWorkLease {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow checkpoint operation: %w", err)
	}
	events, err := m.durability.AppendEvents(
		operationCtx,
		record.OwnerID,
		record.WorkflowID,
		eventChanges,
	)
	if err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow append operation events: %w", err)
	}
	record.Status.Events = events
	return cloneWorkflowOperationStatus(record.Status, "update operation")
}

func workflowWorkRecord(
	operation api.ReleaseWorkflowOperationRecord,
	status api.WorkflowOperationStatus,
	now time.Time,
	completedAt *time.Time,
) api.ReleaseWorkflowWorkRecord {
	checkpoint, err := json.Marshal(status)
	if err != nil {
		checkpoint = []byte(`{}`)
	}
	return api.ReleaseWorkflowWorkRecord{
		OwnerID:        operation.OwnerID,
		WorkflowID:     operation.WorkflowID,
		OperationID:    operation.OperationID,
		LeaseOwner:     operation.ProcessEpoch,
		LeaseExpiresAt: now.Add(workflowWorkLeaseTTL),
		Checkpoint:     checkpoint,
		UpdatedAt:      now,
		CompletedAt:    completedAt,
	}
}

func sanitizeWorkflowOperationStatus(status *api.WorkflowOperationStatus) {
	status.Message = logging.SanitizeMessage(status.Message)
	for index := range status.Items {
		status.Items[index].Label = logging.SanitizeMessage(status.Items[index].Label)
		status.Items[index].Message = logging.SanitizeMessage(status.Items[index].Message)
	}
	for index := range status.Failures {
		status.Failures[index].Failure.Message = logging.SanitizeMessage(status.Failures[index].Failure.Message)
		status.Failures[index].Resource = logging.SanitizeMessage(status.Failures[index].Resource)
	}
}

func cloneWorkflowOperationStatus(
	status api.WorkflowOperationStatus,
	action string,
) (api.WorkflowOperationStatus, error) {
	cloned, err := status.Clone()
	if err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow %s clone: %w", action, err)
	}
	if len(cloned.Events) == 0 {
		cloned.Events = projectOperationEvents(cloned)
	}
	return cloned, nil
}

func (m *Module) withOperationReporters(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) (context.Context, func()) {
	progress := newDurableOperationProgressReporter(ctx, m, ownerID, workflowID, operationID)
	report := progress.Report
	ctx = api.WithWorkflowProgressReporter(ctx, report)
	ctx = api.WithWorkflowExternalEffectReporter(
		ctx,
		newDurableExternalEffectReporter(m, ownerID, workflowID, operationID),
	)
	ctx = api.WithPreparationProgressReporter(ctx, func(update api.PreparationProgressUpdate) {
		itemStatus := api.StageStatusRunning
		switch update.Status {
		case api.PreparationProgressCompleted:
			itemStatus = api.StageStatusCompleted
		case api.PreparationProgressSkipped:
			itemStatus = api.StageStatusSkipped
		case api.PreparationProgressFailed:
			itemStatus = api.StageStatusFailed
		case api.PreparationProgressRunning:
		}
		completed := 0
		if itemStatus != api.StageStatusRunning {
			completed = min(update.Order, 1200)
		}
		report(api.WorkflowProgressUpdate{
			Phase:     string(update.Phase),
			ItemID:    string(update.Phase),
			Kind:      "preparation_phase",
			Label:     update.Label,
			Status:    itemStatus,
			Completed: completed,
			Total:     1200,
			Message:   update.Message,
		})
	})
	ctx = api.WithDupeProgressReporter(ctx, func(update api.DupeProgressUpdate) {
		report(api.WorkflowProgressUpdate{
			Phase:     "duplicate_check",
			ItemID:    strings.ToUpper(strings.TrimSpace(update.Tracker)),
			Kind:      "tracker",
			Label:     strings.ToUpper(strings.TrimSpace(update.Tracker)),
			Status:    operationItemStatus(update.Status),
			Completed: update.Completed,
			Total:     update.Total,
			Message:   update.Message,
		})
	})
	ctx = api.WithDVDMenuProgressReporter(ctx, func(update api.DVDMenuProgressUpdate) {
		total := max(update.DiscoveredMenus, update.CapturedCount)
		report(api.WorkflowProgressUpdate{
			Phase:     "dvd_menu_" + strings.TrimSpace(update.Phase),
			ItemID:    "dvd_menus",
			Kind:      "media",
			Label:     "DVD menus",
			Status:    api.StageStatusRunning,
			Completed: update.CapturedCount,
			Total:     total,
			Message:   update.Message,
		})
	})
	ctx = api.WithImageUploadProgressReporter(ctx, func(update api.ImageUploadProgressUpdate) {
		report(api.WorkflowProgressUpdate{
			Phase:     "image_hosting",
			ItemID:    strings.TrimSpace(update.AttemptID),
			Kind:      "image_host",
			Label:     strings.ToLower(strings.TrimSpace(update.Host)),
			Status:    operationItemStatus(string(update.Status)),
			Completed: update.Completed,
			Total:     update.Total,
			Message:   update.Message,
		})
	})
	ctx = api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
		itemID := strings.TrimSpace(update.Task)
		if tracker := strings.ToUpper(strings.TrimSpace(update.Tracker)); tracker != "" {
			itemID = tracker + ":" + itemID
		}
		report(api.WorkflowProgressUpdate{
			Phase:     strings.TrimSpace(update.Task),
			ItemID:    itemID,
			Kind:      "upload",
			Label:     strings.ToUpper(strings.TrimSpace(update.Tracker)),
			Status:    operationItemStatus(update.Status),
			Completed: update.CompletedPieces,
			Total:     update.TotalPieces,
			Message:   update.Message,
		})
	})
	return ctx, progress.Close
}

func applyWorkflowProgress(status *api.WorkflowOperationStatus, update api.WorkflowProgressUpdate) {
	itemID := strings.TrimSpace(update.ItemID)
	itemIndex := -1
	for index := range status.Items {
		if status.Items[index].ID == itemID {
			itemIndex = index
			break
		}
	}
	if itemIndex >= 0 && isTerminalProgressStatus(status.Items[itemIndex].Status) && !isTerminalProgressStatus(update.Status) {
		return
	}
	status.Phase = strings.TrimSpace(update.Phase)
	status.Message = strings.TrimSpace(update.Message)
	status.Completed = max(status.Completed, max(0, update.Completed))
	status.Total = max(status.Total, max(0, update.Total))
	if status.Total > 0 {
		progress := min(100, status.Completed*100/status.Total)
		status.Progress = max(status.Progress, progress)
	}
	if itemID == "" {
		return
	}
	item := api.WorkflowOperationItem{
		ID:        itemID,
		Kind:      strings.TrimSpace(update.Kind),
		Label:     strings.TrimSpace(update.Label),
		Phase:     strings.TrimSpace(update.Phase),
		Status:    update.Status,
		Completed: max(0, update.Completed),
		Total:     max(0, update.Total),
		Message:   strings.TrimSpace(update.Message),
	}
	if itemIndex >= 0 {
		item.Completed = max(status.Items[itemIndex].Completed, item.Completed)
		item.Total = max(status.Items[itemIndex].Total, item.Total)
		if progressStatusRank(status.Items[itemIndex].Status) > progressStatusRank(item.Status) {
			item.Status = status.Items[itemIndex].Status
			item.Message = status.Items[itemIndex].Message
			item.Phase = status.Items[itemIndex].Phase
		}
		status.Items[itemIndex] = item
	} else {
		status.Items = append(status.Items, item)
	}
	slices.SortStableFunc(status.Items, func(left, right api.WorkflowOperationItem) int {
		return strings.Compare(left.Kind+"\x00"+left.Label+"\x00"+left.ID, right.Kind+"\x00"+right.Label+"\x00"+right.ID)
	})
}

func progressStatusRank(status api.StageStatus) int {
	switch status {
	case api.StageStatusFailed, api.StageStatusPartial, api.StageStatusBlocked, api.StageStatusCanceled, api.StageStatusInterrupted:
		return 4
	case api.StageStatusCompleted, api.StageStatusExecuted, api.StageStatusSkipped:
		return 3
	case api.StageStatusRunning:
		return 2
	case api.StageStatusPending, api.StageStatusQueued, api.StageStatusReady, api.StageStatusStale, api.StageStatusUnavailable:
		return 1
	}
	return 0
}

func operationItemStatus(status string) api.StageStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "pending":
		return api.StageStatusQueued
	case "running", "searching":
		return api.StageStatusRunning
	case "completed", "ready", "success", "succeeded":
		return api.StageStatusCompleted
	case "bypassed":
		return api.StageStatusCompleted
	case "skipped":
		return api.StageStatusSkipped
	case "blocked":
		return api.StageStatusBlocked
	case "failed", "error":
		return api.StageStatusFailed
	case "canceled", "cancelled":
		return api.StageStatusCanceled
	case "interrupted":
		return api.StageStatusInterrupted
	default:
		return api.StageStatusRunning
	}
}

// Workflow returns one detached owner-scoped aggregate.
func (m *Module) Workflow(ctx context.Context, ownerID string, workflowID api.WorkflowID) (api.ReleaseWorkflow, error) {
	ownerID = strings.TrimSpace(ownerID)
	state, err := m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return api.ReleaseWorkflow{}, fmt.Errorf("release workflow query: %w", err)
	}
	if err := m.recoverAfterRestart(ctx, ownerID, &state); err != nil {
		return api.ReleaseWorkflow{}, err
	}
	workflow, err := state.Workflow.Clone()
	if err != nil {
		return api.ReleaseWorkflow{}, fmt.Errorf("release workflow query clone: %w", err)
	}
	return workflow, nil
}

// Current returns detached current stage snapshots without regenerating or
// refreshing any workflow dependency.
func (m *Module) Current(ctx context.Context, ownerID string, workflowID api.WorkflowID) (CommandResult, error) {
	if err := m.ensureOperationRecovery(ctx); err != nil {
		return CommandResult{}, err
	}
	ownerID = strings.TrimSpace(ownerID)
	state, err := m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow current query: %w", err)
	}
	if err := m.recoverAfterRestart(ctx, ownerID, &state); err != nil {
		return CommandResult{}, err
	}
	result := CommandResult{Workflow: state.Workflow}
	result.FactInstructions = currentSnapshot(state.FactInstructions, state.Workflow.FactInstructions.ID)
	if ref := state.Workflow.Release; ref != nil {
		result.Release = currentSnapshot(state.Releases, ref.ID)
	}
	if ref := state.Workflow.TrackerCatalog; ref != nil {
		result.Catalog = currentSnapshot(state.Catalogs, ref.ID)
	}
	if ref := state.Workflow.TrackerRuntime; ref != nil {
		result.Runtime = currentSnapshot(state.Runtimes, ref.ID)
	}
	if ref := state.Workflow.Selection; ref != nil {
		result.Selection = currentSnapshot(state.Selections, ref.ID)
	}
	if ref := state.Workflow.ProjectionInstructions; ref != nil {
		result.ProjectionInstructions = currentSnapshot(state.ProjectionInstructions, ref.ID)
	}
	if ref := state.Workflow.TrackerProjections; ref != nil {
		result.Projections = currentSnapshot(state.Projections, ref.ID)
	}
	if ref := state.Workflow.TrackerPreflight; ref != nil {
		result.Preflight = currentSnapshot(state.Preflights, ref.ID)
	}
	if ref := state.Workflow.Dupes; ref != nil {
		result.Dupes = currentSnapshot(state.Dupes, ref.ID)
	}
	if ref := state.Workflow.TrackerApproval; ref != nil {
		result.TrackerApproval = currentSnapshot(state.TrackerApprovals, ref.ID)
	}
	if ref := state.Workflow.Media; ref != nil {
		result.Media = currentSnapshot(state.Media, ref.ID)
		if err := m.finalizeRetainedMedia(ctx, ownerID, workflowID, result.Media, false); err != nil {
			m.logger.Warnf(
				"releaseworkflow: workflow=%s stage=media_cleanup state=retry_pending cause=%s",
				workflowID,
				logging.SanitizeMessage(err.Error()),
			)
		}
	}
	if ref := state.Workflow.Descriptions; ref != nil {
		result.Descriptions = currentSnapshot(state.Descriptions, ref.ID)
	}
	if ref := state.Workflow.DryRun; ref != nil {
		result.DryRun = currentSnapshot(state.DryRuns, ref.ID)
	}
	if ref := state.Workflow.UploadResult; ref != nil {
		result.UploadResult = currentSnapshot(state.UploadResults, ref.ID)
	}
	operation, operationErr := m.operations.LoadLatestOperation(ctx, ownerID, workflowID)
	if operationErr == nil {
		if isTerminalProgressStatus(operation.Status.Status) {
			if err := m.waitForOperationCleanup(ctx, operation.OperationID); err != nil {
				return CommandResult{}, err
			}
		}
		if len(operation.Status.Events) == 0 {
			operation.Status.Events = projectOperationEvents(operation.Status)
		}
		result.Operation = &operation.Status
	} else if !errors.Is(operationErr, ErrWorkflowNotFound) {
		return CommandResult{}, fmt.Errorf("release workflow current operation query: %w", operationErr)
	}
	result.Continuation = projectWorkflowContinuationForState(result, &state, m.clock.Now().UTC())
	result.Workflow.RequiredActions = append(
		[]api.RequiredAction(nil),
		result.Continuation.RequiredActions...,
	)
	events, eventsErr := m.durability.LoadEvents(ctx, ownerID, workflowID, 0, 1000)
	if eventsErr != nil {
		return CommandResult{}, fmt.Errorf("release workflow load retained events: %w", eventsErr)
	}
	result.Continuation.Events = events
	continuationPayload, marshalErr := json.Marshal(result.Continuation)
	if marshalErr != nil {
		return CommandResult{}, fmt.Errorf("release workflow encode materialized continuation: %w", marshalErr)
	}
	if err := m.durability.SaveContinuation(ctx, api.ReleaseWorkflowContinuationRecord{
		OwnerID:    ownerID,
		WorkflowID: workflowID,
		Revision:   result.Workflow.Revision,
		Payload:    continuationPayload,
		UpdatedAt:  result.Workflow.UpdatedAt,
	}); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow save materialized continuation: %w", err)
	}
	cloned, err := cloneCommandResult(result)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow current query clone: %w", err)
	}
	return cloned, nil
}

// MediaPlan returns a safe page-facing plan bound to the current exact release
// and tracker-projection revision.
func (m *Module) MediaPlan(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
) (api.MediaPlan, error) {
	if err := ctx.Err(); err != nil {
		return api.MediaPlan{}, fmt.Errorf("release workflow media plan: %w", err)
	}
	planner, ok := m.mediaBuilder.(MediaPlanner)
	if !ok {
		return api.MediaPlan{}, fmt.Errorf("%w: media planner is unavailable", ErrInvalidTransition)
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || workflowID == "" {
		return api.MediaPlan{}, ErrWorkflowNotFound
	}
	lock := m.commandLock(ownerID + "\x00" + string(workflowID))
	lock.Lock()
	defer lock.Unlock()
	state, err := m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return api.MediaPlan{}, fmt.Errorf("release workflow media plan load: %w", err)
	}
	if state.Workflow.Release == nil || state.Workflow.TrackerProjections == nil {
		return api.MediaPlan{}, fmt.Errorf("%w: media plan dependencies are incomplete", ErrInvalidTransition)
	}
	release, releaseOK := state.Releases[state.Workflow.Release.ID]
	projections, projectionsOK := state.Projections[state.Workflow.TrackerProjections.ID]
	if !releaseOK || !projectionsOK || release.Revision != state.Workflow.Release.Revision ||
		projections.Revision != state.Workflow.TrackerProjections.Revision {
		return api.MediaPlan{}, fmt.Errorf("%w: media plan dependencies are stale", ErrInvalidTransition)
	}
	now := m.clock.Now().UTC()
	targets, err := resolveDownstreamTrackerSet(&state, nil, downstreamStageMedia, now)
	if err != nil {
		return api.MediaPlan{}, err
	}
	plan, err := planner.Plan(ctx, api.ReleaseRef{
		SourcePath: release.Release.Source.SourcePath,
		Generation: release.Release.Generation,
	}, targets.Projections(), now)
	if err != nil {
		return api.MediaPlan{}, fmt.Errorf("release workflow build media plan: %w", err)
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		WorkflowID api.WorkflowID
		Revision   api.WorkflowRevision
		Release    api.ReleaseSnapshotRef
		Projection api.TrackerReleaseProjectionSetRef
		Selections []api.ScreenshotSelection
	}{workflowID, state.Workflow.Revision, *state.Workflow.Release, *state.Workflow.TrackerProjections, plan.SuggestedSelections})
	if err != nil {
		return api.MediaPlan{}, fmt.Errorf("release workflow media plan fingerprint: %w", err)
	}
	plan.ID = api.MediaPlanID("plan_" + string(fingerprint)[:24])
	plan.WorkflowID = workflowID
	plan.Revision = state.Workflow.Revision
	plan.Release = *state.Workflow.Release
	plan.ProjectionSet = *state.Workflow.TrackerProjections
	plan.CreatedAt = now
	if state.Workflow.Media != nil {
		if media, exists := state.Media[state.Workflow.Media.ID]; exists && media.Revision == state.Workflow.Media.Revision {
			plan.ExistingArtifacts = append([]api.MediaArtifact(nil), media.Artifacts...)
		}
	}
	return plan, nil
}

// PreviewFrame creates non-authoritative owner-scoped preview content without
// publishing media or invalidating downstream workflow state.
func (m *Module) PreviewFrame(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	expectedRevision api.WorkflowRevision,
	timestampSeconds float64,
) (api.FramePreview, error) {
	if err := ctx.Err(); err != nil {
		return api.FramePreview{}, fmt.Errorf("release workflow preview frame: %w", err)
	}
	if timestampSeconds < 0 {
		return api.FramePreview{}, fmt.Errorf("%w: frame preview timestamp cannot be negative", ErrInvalidTransition)
	}
	planner, ok := m.mediaBuilder.(MediaPlanner)
	if !ok {
		return api.FramePreview{}, fmt.Errorf("%w: frame preview service is unavailable", ErrInvalidTransition)
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || workflowID == "" || expectedRevision == 0 {
		return api.FramePreview{}, ErrWorkflowNotFound
	}
	lock := m.commandLock(ownerID + "\x00" + string(workflowID))
	lock.Lock()
	defer lock.Unlock()
	state, err := m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return api.FramePreview{}, fmt.Errorf("release workflow preview frame load: %w", err)
	}
	if state.Workflow.Revision != expectedRevision {
		return api.FramePreview{}, ErrRevisionConflict
	}
	if state.Workflow.Release == nil {
		return api.FramePreview{}, fmt.Errorf("%w: prepared release is unavailable", ErrInvalidTransition)
	}
	release, exists := state.Releases[state.Workflow.Release.ID]
	if !exists || release.Revision != state.Workflow.Release.Revision {
		return api.FramePreview{}, fmt.Errorf("%w: prepared release is stale", ErrInvalidTransition)
	}
	content, err := planner.PreviewFrame(ctx, api.ReleaseRef{
		SourcePath: release.Release.Source.SourcePath,
		Generation: release.Release.Generation,
	}, timestampSeconds)
	if err != nil {
		return api.FramePreview{}, fmt.Errorf("release workflow preview frame: %w", err)
	}
	id, err := m.newID("preview")
	if err != nil {
		return api.FramePreview{}, err
	}
	previewID := api.PublicResourceID(id)
	expiresAt := m.clock.Now().UTC().Add(workflowPreviewTTL)
	if err := m.private.Put(ownerID, workflowID, previewPrivateResourceID(previewID), content, expiresAt); err != nil {
		return api.FramePreview{}, fmt.Errorf("release workflow retain frame preview: %w", err)
	}
	return api.FramePreview{
		ID:               previewID,
		WorkflowID:       workflowID,
		WorkflowRevision: expectedRevision,
		Release:          *state.Workflow.Release,
		TimestampSeconds: timestampSeconds,
		ExpiresAt:        expiresAt,
	}, nil
}

// PreviewArtifact returns owner-scoped transient preview bytes by opaque ID.
func (m *Module) PreviewArtifact(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	previewID api.PublicResourceID,
) (MediaArtifactContent, error) {
	if err := ctx.Err(); err != nil {
		return MediaArtifactContent{}, fmt.Errorf("release workflow preview artifact: %w", err)
	}
	value, err := m.private.Get(strings.TrimSpace(ownerID), workflowID, previewPrivateResourceID(previewID), m.clock.Now().UTC())
	if err != nil {
		return MediaArtifactContent{}, fmt.Errorf("release workflow preview artifact resource: %w", err)
	}
	content, ok := value.(MediaPreviewContent)
	if !ok {
		return MediaArtifactContent{}, ErrPrivateResourceUnavailable
	}
	return MediaArtifactContent{
		Body:        io.NopCloser(bytes.NewReader(content.Bytes)),
		ContentType: content.ContentType,
	}, nil
}

func previewPrivateResourceID(id api.PublicResourceID) string { return "preview:" + string(id) }

// StageMediaResource retains owner-scoped image bytes for a later exact
// AttachMediaArtifactsCommand without exposing a host filesystem path.
func (m *Module) StageMediaResource(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	expectedRevision api.WorkflowRevision,
	content StagedMediaContent,
) (api.WorkflowResourceRef, error) {
	if err := ctx.Err(); err != nil {
		return api.WorkflowResourceRef{}, fmt.Errorf("release workflow stage media: %w", err)
	}
	ownerID = strings.TrimSpace(ownerID)
	content.ContentType = strings.ToLower(strings.TrimSpace(strings.SplitN(content.ContentType, ";", 2)[0]))
	if ownerID == "" || workflowID == "" || expectedRevision == 0 {
		return api.WorkflowResourceRef{}, ErrWorkflowNotFound
	}
	if len(content.Bytes) == 0 || !strings.HasPrefix(content.ContentType, "image/") {
		return api.WorkflowResourceRef{}, fmt.Errorf("%w: staged media must be a non-empty image", ErrInvalidTransition)
	}
	lock := m.commandLock(ownerID + "\x00" + string(workflowID))
	lock.Lock()
	defer lock.Unlock()
	state, err := m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return api.WorkflowResourceRef{}, fmt.Errorf("release workflow stage media load: %w", err)
	}
	if state.Workflow.Revision != expectedRevision {
		return api.WorkflowResourceRef{}, ErrRevisionConflict
	}
	id, err := m.newID("resource")
	if err != nil {
		return api.WorkflowResourceRef{}, err
	}
	resourceID := api.WorkflowResourceID(id)
	content.Bytes = append([]byte(nil), content.Bytes...)
	if err := m.private.Put(
		ownerID,
		workflowID,
		stagedMediaPrivateResourceID(resourceID),
		content,
		m.clock.Now().UTC().Add(time.Hour),
	); err != nil {
		return api.WorkflowResourceRef{}, fmt.Errorf("release workflow retain staged media: %w", err)
	}
	return api.WorkflowResourceRef{
		ID:          resourceID,
		ContentType: content.ContentType,
		SizeBytes:   int64(len(content.Bytes)),
	}, nil
}

func stagedMediaPrivateResourceID(id api.WorkflowResourceID) string {
	return "staged-media:" + string(id)
}

// MediaArtifact returns one retained owner-scoped media stream addressed only
// by its exact workflow/media revision and opaque artifact ID.
func (m *Module) MediaArtifact(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	mediaRef api.MediaArtifactSetRef,
	artifactID api.PublicResourceID,
) (MediaArtifactContent, error) {
	if err := ctx.Err(); err != nil {
		return MediaArtifactContent{}, fmt.Errorf("release workflow media artifact: %w", err)
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || workflowID == "" || mediaRef.ID == "" || mediaRef.Revision == 0 || artifactID == "" {
		return MediaArtifactContent{}, ErrWorkflowNotFound
	}
	lock := m.commandLock(ownerID + "\x00" + string(workflowID))
	lock.Lock()
	defer lock.Unlock()
	state, err := m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return MediaArtifactContent{}, fmt.Errorf("release workflow media artifact load: %w", err)
	}
	if state.Workflow.Media == nil || *state.Workflow.Media != mediaRef {
		return MediaArtifactContent{}, ErrRevisionConflict
	}
	snapshot, ok := state.Media[mediaRef.ID]
	if !ok || snapshot.Revision != mediaRef.Revision {
		return MediaArtifactContent{}, ErrRevisionConflict
	}
	value, err := m.private.Get(ownerID, workflowID, mediaPrivateResourceID(mediaRef.ID), m.clock.Now().UTC())
	if err != nil {
		return MediaArtifactContent{}, fmt.Errorf("release workflow media artifact resource: %w", err)
	}
	resource, ok := value.(RetainedMediaResource)
	if !ok {
		return MediaArtifactContent{}, ErrPrivateResourceUnavailable
	}
	content, err := resource.OpenArtifact(ctx, snapshot, artifactID)
	if err != nil {
		return MediaArtifactContent{}, fmt.Errorf("release workflow open media artifact: %w", err)
	}
	return content, nil
}

func currentSnapshot[ID comparable, Snapshot any](values map[ID]Snapshot, id ID) *Snapshot {
	value, ok := values[id]
	if !ok {
		return nil
	}
	return &value
}

// Operation returns one detached owner-scoped pollable operation status.
func (m *Module) Operation(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) (api.WorkflowOperationStatus, error) {
	if err := m.ensureOperationRecovery(ctx); err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	ownerID = strings.TrimSpace(ownerID)
	record, err := m.operations.LoadOperation(ctx, ownerID, workflowID, operationID)
	if err != nil {
		if !errors.Is(err, ErrWorkflowNotFound) {
			return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow operation query: %w", err)
		}
		lock := m.commandLock(ownerID + "\x00" + string(workflowID))
		lock.Lock()
		defer lock.Unlock()
		state, loadErr := m.repository.Load(ctx, ownerID, workflowID)
		if loadErr != nil {
			return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow operation legacy query: %w", loadErr)
		}
		if recoveryErr := m.recoverAfterRestart(ctx, ownerID, &state); recoveryErr != nil {
			return api.WorkflowOperationStatus{}, recoveryErr
		}
		operation, ok := state.Operations[operationID]
		if !ok {
			return api.WorkflowOperationStatus{}, ErrWorkflowNotFound
		}
		return cloneWorkflowOperationStatus(operation, "query legacy operation")
	}
	if isTerminalProgressStatus(record.Status.Status) {
		if err := m.waitForOperationCleanup(ctx, operationID); err != nil {
			return api.WorkflowOperationStatus{}, err
		}
	}
	return cloneWorkflowOperationStatus(record.Status, "query operation")
}

// OperationEvents returns retained events for one owner-scoped operation after
// a workflow-global event cursor.
func (m *Module) OperationEvents(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
	after uint64,
	limit int,
) ([]api.WorkflowEvent, error) {
	if err := m.ensureOperationRecovery(ctx); err != nil {
		return nil, err
	}
	ownerID = strings.TrimSpace(ownerID)
	if _, err := m.operations.LoadOperation(ctx, ownerID, workflowID, operationID); err != nil {
		return nil, fmt.Errorf("release workflow operation events query: %w", err)
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	const pageSize = 1000
	cursor := after
	result := make([]api.WorkflowEvent, 0, limit)
	for len(result) < limit {
		events, err := m.durability.LoadEvents(ctx, ownerID, workflowID, cursor, pageSize)
		if err != nil {
			return nil, fmt.Errorf("release workflow load operation events: %w", err)
		}
		if len(events) == 0 {
			break
		}
		for _, event := range events {
			cursor = event.Sequence
			if event.OperationID != operationID {
				continue
			}
			result = append(result, event)
			if len(result) == limit {
				return result, nil
			}
		}
		if len(events) < pageSize {
			break
		}
	}
	return result, nil
}

func (m *Module) waitForOperationCleanup(ctx context.Context, operationID api.WorkflowOperationID) error {
	m.operationWorkersMu.Lock()
	worker := m.operationWorkers[operationID]
	m.operationWorkersMu.Unlock()
	if worker.done == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("release workflow wait for operation cleanup: %w", ctx.Err())
	case <-worker.done:
		return nil
	}
}

// CancelOperation requests cancellation of one active owner-scoped operation.
func (m *Module) CancelOperation(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) (api.WorkflowOperationStatus, error) {
	if err := m.ensureOperationRecovery(ctx); err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	record, err := m.operations.LoadOperation(ctx, strings.TrimSpace(ownerID), workflowID, operationID)
	if err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("release workflow cancel operation: %w", err)
	}
	if !workflowOperationActive(record.Status.Status) {
		return cloneWorkflowOperationStatus(record.Status, "cancel terminal operation")
	}
	m.operationWorkersMu.Lock()
	worker := m.operationWorkers[operationID]
	m.operationWorkersMu.Unlock()
	if worker.cancel != nil {
		worker.cancel()
	}
	return m.mutateOperation(ctx, record.OwnerID, workflowID, operationID, func(status *api.WorkflowOperationStatus) {
		status.Message = "Cancellation requested."
	})
}

func (m *Module) newID(prefix string) (string, error) {
	id, err := m.ids.NewID(prefix)
	if err != nil {
		return "", fmt.Errorf("release workflow generate %s id: %w", prefix, err)
	}
	return id, nil
}

func (m *Module) commandLock(key string) *sync.Mutex {
	m.locksMu.Lock()
	defer m.locksMu.Unlock()
	lock, ok := m.locks[key]
	if !ok {
		lock = &sync.Mutex{}
		m.locks[key] = lock
	}
	return lock
}

func (m *Module) recoverAfterRestart(ctx context.Context, ownerID string, state *State) error {
	if state.ProcessEpoch == m.processEpoch {
		return nil
	}
	authRetryRequired, obsoletePreflightActions := currentPreflightAuthRetry(*state)
	if !workflowNeedsRestartRecovery(*state) && !authRetryRequired {
		state.ProcessEpoch = m.processEpoch
		return nil
	}

	now := m.clock.Now().UTC()
	expected := state.Workflow.Revision
	nextRevision := expected + 1
	interrupted := false
	for id, operation := range state.Operations {
		if operation.Status != api.StageStatusRunning {
			continue
		}
		operation.Revision = nextRevision
		operation.Status = api.StageStatusInterrupted
		operation.Message = "Operation interrupted by process restart. Prepare and review the workflow again."
		operation.Failures = []api.WorkflowFailure{{
			Failure: api.OperationFailure{
				Code:      api.OperationFailureStaleReview,
				Operation: operation.Operation,
				Message:   "Operation interrupted by process restart.",
				Recovery:  api.OperationRecoveryReviewAgain,
			},
		}}
		completedAt := now
		operation.CompletedAt = &completedAt
		state.Operations[id] = operation
		interrupted = true
	}

	workflow := &state.Workflow
	legacyAuthAction := slices.ContainsFunc(workflow.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == legacyTrackerAuthActionKind || action.Kind == legacyTrackerTwoFactorActionKind
	})
	if state.Composite != nil &&
		state.Composite.Version < compositeUploadSessionVersion &&
		len(state.Composite.RemoveTrackers) > 0 &&
		(authRetryRequired || legacyAuthAction) {
		workflow.Revision = nextRevision
		workflow.UpdatedAt = now
		workflow.RequiredActions = nil
		workflow.Status = api.WorkflowStatusFailed
		workflow.Failures = []api.WorkflowFailure{{
			Failure: api.OperationFailure{
				Code:      api.OperationFailureStaleReview,
				Operation: api.OperationKindUploadExecute,
				Message:   "Legacy tracker exclusions cannot be safely attributed. Start a fresh upload workflow.",
				Recovery:  api.OperationRecoveryNone,
			},
		}}
		state.Composite.ActiveOperationID = ""
		state.Composite.TerminalReason = "legacy_tracker_exclusions_require_fresh_workflow"
		state.ProcessEpoch = m.processEpoch
		if err := workflow.Validate(); err != nil {
			return fmt.Errorf("release workflow legacy auth restart recovery validate: %w", err)
		}
		if err := m.repository.Save(ctx, ownerID, expected, *state); err != nil {
			return fmt.Errorf("release workflow legacy auth restart recovery save: %w", err)
		}
		return nil
	}
	reconciliationPending := slices.ContainsFunc(workflow.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionReconcileSubmission && action.Status == api.RequiredActionStatusPending
	})
	if !reconciliationPending {
		if err := m.invalidateUnavailablePrivateAuthority(ownerID, workflow, now); err != nil {
			return err
		}
	}
	if authRetryRequired {
		workflow.TrackerProjections = nil
		workflow.TrackerPreflight = nil
		invalidateDupeAndDownstream(workflow)
	}
	workflow.Revision = nextRevision
	workflow.UpdatedAt = now
	workflow.RequiredActions = slices.DeleteFunc(workflow.RequiredActions, func(action api.RequiredAction) bool {
		if _, obsolete := obsoletePreflightActions[action.ID]; obsolete {
			return true
		}
		switch action.Kind {
		case api.RequiredActionReprepare:
			return true
		case api.RequiredActionReviewDuplicates:
			return workflow.Dupes == nil
		case api.RequiredActionApproveTrackers,
			api.RequiredActionApproveUpload: //nolint:staticcheck // Remove retained v1 action during restart.
			return true
		case api.RequiredActionReconcileSubmission:
			return workflow.UploadResult == nil && workflow.Media == nil
		case api.RequiredActionSelectPlaylist,
			api.RequiredActionSelectMetadata,
			api.RequiredActionConfirmRescan,
			api.RequiredActionProvideTrackerInput,
			api.RequiredActionAnswerQuestionnaire,
			api.RequiredActionAuthorizeRules:
			return false
		case legacyTrackerAuthActionKind,
			legacyTrackerTwoFactorActionKind:
			return true
		}
		return false
	})
	for index := range workflow.RequiredActions {
		workflow.RequiredActions[index].WorkflowRevision = nextRevision
	}
	if len(workflow.RequiredActions) > 0 {
		workflow.Status = api.WorkflowStatusBlocked
	} else {
		workflow.Status = api.WorkflowStatusActive
	}
	workflow.Failures = slices.DeleteFunc(workflow.Failures, func(failure api.WorkflowFailure) bool {
		if authRetryRequired &&
			(failure.Failure.Code == api.OperationFailureTrackerAuthRequired ||
				failure.Failure.Code == api.OperationFailureNoEligibleTrackers) {
			return true
		}
		if failure.Failure.Code == api.OperationFailureStaleReview {
			return true
		}
		if failure.Failure.Code == api.OperationFailureUnknownOutcome {
			return !reconciliationPending
		}
		return interrupted
	})
	state.ProcessEpoch = m.processEpoch
	if err := workflow.Validate(); err != nil {
		return fmt.Errorf("release workflow restart recovery validate: %w", err)
	}
	if err := m.repository.Save(ctx, ownerID, expected, *state); err != nil {
		return fmt.Errorf("release workflow restart recovery save: %w", err)
	}
	return nil
}

func currentPreflightAuthRetry(state State) (bool, map[api.RequiredActionID]struct{}) {
	actionIDs := make(map[api.RequiredActionID]struct{})
	ref := state.Workflow.TrackerPreflight
	if ref == nil {
		return false, actionIDs
	}
	assessment, ok := state.Preflights[ref.ID]
	if !ok || assessment.Revision != ref.Revision {
		return false, actionIDs
	}
	authBlocked := false
	for _, result := range assessment.Results {
		for _, action := range result.RequiredActions {
			if action.ID != "" {
				actionIDs[action.ID] = struct{}{}
			}
			if action.Kind == legacyTrackerAuthActionKind || action.Kind == legacyTrackerTwoFactorActionKind {
				authBlocked = true
			}
		}
		if slices.ContainsFunc(result.Failures, func(failure api.WorkflowFailure) bool {
			return failure.Failure.Code == api.OperationFailureTrackerAuthRequired
		}) {
			authBlocked = true
		}
	}
	return authBlocked, actionIDs
}

func (m *Module) invalidateUnavailablePrivateAuthority(
	ownerID string,
	workflow *api.ReleaseWorkflow,
	now time.Time,
) error {
	if workflow.Dupes != nil {
		available, err := m.privateResourceAvailable(
			ownerID,
			workflow.ID,
			dupePrivateResourceID(workflow.Dupes.ID),
			now,
		)
		if err != nil {
			return err
		}
		if !available {
			invalidateDupeAndDownstream(workflow)
		}
	}
	if workflow.Dupes != nil && workflow.Media != nil {
		available, err := m.privateResourceAvailable(
			ownerID,
			workflow.ID,
			mediaPrivateResourceID(workflow.Media.ID),
			now,
		)
		if err != nil {
			return err
		}
		if !available {
			workflow.Media = nil
			workflow.Descriptions = nil
			invalidateUploadPlan(workflow)
		}
	}
	if workflow.Media != nil && workflow.Descriptions != nil {
		available, err := m.privateResourceAvailable(
			ownerID,
			workflow.ID,
			descriptionPrivateResourceID(workflow.Descriptions.ID),
			now,
		)
		if err != nil {
			return err
		}
		if !available {
			workflow.Descriptions = nil
			invalidateUploadPlan(workflow)
		}
	}
	if workflow.UploadResult == nil && workflow.DryRun != nil {
		available, err := m.privateResourceAvailable(
			ownerID,
			workflow.ID,
			uploadPlanPrivateResourceID(workflow.DryRun.ID),
			now,
		)
		if err != nil {
			return err
		}
		if !available {
			workflow.DryRun = nil
		}
	}
	return nil
}

func (m *Module) privateResourceAvailable(
	ownerID string,
	workflowID api.WorkflowID,
	resourceID string,
	now time.Time,
) (bool, error) {
	_, err := m.private.Get(ownerID, workflowID, resourceID, now)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrPrivateResourceUnavailable), errors.Is(err, ErrPrivateResourceConsumed):
		return false, nil
	default:
		return false, fmt.Errorf("release workflow recover private resource: %w", err)
	}
}

func workflowNeedsRestartRecovery(state State) bool {
	switch state.Workflow.Status {
	case api.WorkflowStatusCompleted, api.WorkflowStatusCanceled, api.WorkflowStatusFailed:
		return false
	case api.WorkflowStatusDraft, api.WorkflowStatusActive, api.WorkflowStatusBlocked:
	}
	if state.Workflow.Release != nil {
		return true
	}
	for _, operation := range state.Operations {
		if operation.Status == api.StageStatusRunning {
			return true
		}
	}
	return state.Workflow.Status == api.WorkflowStatusActive || state.Workflow.Status == api.WorkflowStatusBlocked
}

func (m *Module) create(
	ctx context.Context,
	ownerID string,
	command CreateWorkflowCommand,
	fingerprint api.WorkflowFingerprint,
) (CommandResult, error) {
	now := m.clock.Now().UTC()
	workflowID := command.WorkflowID
	if workflowID == "" {
		value, err := m.newID("workflow")
		if err != nil {
			return CommandResult{}, err
		}
		workflowID = api.WorkflowID(value)
	}
	factID, err := m.newID("facts")
	if err != nil {
		return CommandResult{}, err
	}
	facts := api.ReleaseFactInstructionSnapshot{
		ID:           api.ReleaseFactInstructionSnapshotID(factID),
		WorkflowID:   workflowID,
		Revision:     1,
		Instructions: command.Instructions,
		CreatedAt:    now,
	}
	facts, err = facts.WithFingerprint()
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow create facts: %w", err)
	}
	if err := facts.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow create facts: %w", err)
	}
	workflow := api.ReleaseWorkflow{
		ID:               workflowID,
		Revision:         1,
		FactInstructions: api.ReleaseFactInstructionSnapshotRef{ID: facts.ID, Revision: facts.Revision},
		Status:           api.WorkflowStatusDraft,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := workflow.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow create: %w", err)
	}
	state := newState(ownerID, workflow)
	state.ProcessEpoch = m.processEpoch
	state.TrackerDecisionMode = normalizeTrackerDecisionMode(command.TrackerDecisionMode)
	state.FactInstructions[facts.ID] = facts
	state.Composite = command.Composite
	if state.Composite != nil {
		state.Composite.LastCommittedRevision = workflow.Revision
	}
	state, idempotent, err := m.repository.Create(ctx, ownerID, command.IdempotencyKey, fingerprint, state)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow create: %w", err)
	}
	if idempotent {
		if err := m.recoverAfterRestart(ctx, ownerID, &state); err != nil {
			return CommandResult{}, err
		}
		facts = state.FactInstructions[state.Workflow.FactInstructions.ID]
		workflow = state.Workflow
	}
	result := CommandResult{Workflow: workflow, FactInstructions: &facts}
	return cloneCommandResult(result)
}

func newState(ownerID string, workflow api.ReleaseWorkflow) State {
	return State{
		OwnerID:                ownerID,
		Workflow:               workflow,
		FactInstructions:       make(map[api.ReleaseFactInstructionSnapshotID]api.ReleaseFactInstructionSnapshot),
		Releases:               make(map[api.ReleaseSnapshotID]api.ReleaseSnapshot),
		Catalogs:               make(map[api.TrackerCatalogSnapshotID]api.TrackerCatalogSnapshot),
		Runtimes:               make(map[api.TrackerRuntimeSnapshotID]api.TrackerRuntimeSnapshot),
		Selections:             make(map[api.TrackerSelectionID]api.TrackerSelection),
		ProjectionInstructions: make(map[api.TrackerProjectionInstructionSnapshotID]api.TrackerProjectionInstructionSnapshot),
		Projections:            make(map[api.TrackerReleaseProjectionSetID]api.TrackerReleaseProjectionSet),
		Preflights:             make(map[api.TrackerPreflightAssessmentID]api.TrackerPreflightAssessment),
		Dupes:                  make(map[api.DupeAssessmentID]api.DupeAssessment),
		TrackerApprovals:       make(map[api.TrackerApprovalSnapshotID]api.TrackerApprovalSnapshot),
		Media:                  make(map[api.MediaArtifactSetID]api.MediaArtifactSet),
		Descriptions:           make(map[api.DescriptionSetID]api.DescriptionSet),
		DryRuns:                make(map[api.UploadDryRunResultID]api.UploadDryRunResult),
		UploadResults:          make(map[api.UploadResultID]api.UploadResult),
		Operations:             make(map[api.WorkflowOperationID]api.WorkflowOperationStatus),
		Receipts:               make(map[string]commandReceipt),
	}
}

func commandReceiptKey(name string, idempotencyKey string, fingerprint api.WorkflowFingerprint) string {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = string(fingerprint)
	}
	return name + "\x00" + idempotencyKey
}

// acceptedCommandFingerprint binds every receipt to the same authority-aware
// envelope. Individual command payload fingerprints cannot omit workflow or
// revision authority accidentally.
func acceptedCommandFingerprint(command mutation) (api.WorkflowFingerprint, error) {
	payload, err := command.commandFingerprint()
	if err != nil {
		return "", err
	}
	var (
		workflowID       api.WorkflowID
		expectedRevision api.WorkflowRevision
		operation        api.OperationKind
	)
	if create, ok := command.(CreateWorkflowCommand); ok {
		workflowID = create.WorkflowID
	} else {
		workflowID, expectedRevision, _, err = commandTarget(command)
		if err != nil {
			return "", err
		}
	}
	if typed, ok := command.(Command); ok {
		operation = typed.operationKind()
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Command          string
		WorkflowID       api.WorkflowID
		ExpectedRevision api.WorkflowRevision
		Operation        api.OperationKind
		Payload          api.WorkflowFingerprint
	}{command.commandName(), workflowID, expectedRevision, operation, payload})
	if err != nil {
		return "", fmt.Errorf("canonical accepted command fingerprint: %w", err)
	}
	return fingerprint, nil
}

func commandTarget(command mutation) (api.WorkflowID, api.WorkflowRevision, string, error) {
	switch typed := command.(type) {
	case ReplaceFactInstructionsCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case PrepareReleaseCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case ResetReleaseCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case SelectBlurayCandidateCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case ProjectTrackersCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case PreflightTrackersCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case CheckDuplicatesCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case DecideDuplicatesCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case ApproveTrackersCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case CaptureMediaCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case SetMediaSelectionCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case DeleteMediaArtifactsCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case ReorderMediaArtifactsCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case AttachMediaArtifactsCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case UploadMediaImagesCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case RemoveHostedImagesCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case GenerateDescriptionsCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case SaveDescriptionOverrideCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case ResetDescriptionOverrideCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case DryRunUploadsCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case ExecuteUploadsCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case RetryFailedUploadsCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case RetryClientInjectionsCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case CancelWorkflowCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case InvalidateTrackersCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case ResolveActionCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case CompositeUploadCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case applyCompositeUploadFeedbackCommand:
		return typed.WorkflowID, typed.ExpectedRevision, typed.IdempotencyKey, nil
	case CreateWorkflowCommand:
		return "", 0, "", errors.New("release workflow create command has no existing target")
	}
	return "", 0, "", errors.New("release workflow unsupported command")
}

func (m *Module) apply(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command mutation,
) (CommandResult, error) {
	switch typed := command.(type) {
	case ReplaceFactInstructionsCommand:
		return m.replaceFactInstructions(ctx, ownerID, state, nextRevision, now, typed)
	case PrepareReleaseCommand:
		return m.prepareRelease(ctx, ownerID, state, nextRevision, now, typed)
	case ResetReleaseCommand:
		return m.resetRelease(ctx, ownerID, state, nextRevision, now, typed)
	case SelectBlurayCandidateCommand:
		return m.selectBlurayCandidate(ctx, ownerID, state, nextRevision, now, typed)
	case ProjectTrackersCommand:
		return m.projectTrackers(ctx, ownerID, state, nextRevision, now, typed)
	case PreflightTrackersCommand:
		return m.preflightTrackers(ctx, ownerID, state, nextRevision, now, typed)
	case CheckDuplicatesCommand:
		return m.checkDuplicates(ctx, ownerID, state, nextRevision, now, typed)
	case DecideDuplicatesCommand:
		return m.decideDuplicates(ownerID, state, nextRevision, now, typed)
	case ApproveTrackersCommand:
		return m.approveTrackers(state, nextRevision, now, typed)
	case CaptureMediaCommand:
		return m.captureMedia(ctx, ownerID, state, nextRevision, now, typed)
	case SetMediaSelectionCommand:
		return m.setMediaSelection(ctx, ownerID, state, nextRevision, now, typed)
	case DeleteMediaArtifactsCommand:
		return m.deleteMediaArtifacts(ctx, ownerID, state, nextRevision, now, typed)
	case ReorderMediaArtifactsCommand:
		return m.reorderMediaArtifacts(ctx, ownerID, state, nextRevision, now, typed)
	case AttachMediaArtifactsCommand:
		return m.attachMediaArtifacts(ctx, ownerID, state, nextRevision, now, typed)
	case UploadMediaImagesCommand:
		return m.uploadMediaImages(ctx, ownerID, state, nextRevision, now, typed)
	case RemoveHostedImagesCommand:
		return m.removeHostedImages(ctx, ownerID, state, nextRevision, now, typed)
	case GenerateDescriptionsCommand:
		return m.generateDescriptions(ctx, ownerID, state, nextRevision, now, typed)
	case SaveDescriptionOverrideCommand:
		return m.mutateDescriptionOverride(ctx, ownerID, state, nextRevision, now, typed.Descriptions, typed.GroupKey, &typed.Source)
	case ResetDescriptionOverrideCommand:
		return m.mutateDescriptionOverride(ctx, ownerID, state, nextRevision, now, typed.Descriptions, typed.GroupKey, nil)
	case DryRunUploadsCommand:
		return m.dryRunUploads(ctx, ownerID, state, nextRevision, now, typed)
	case ExecuteUploadsCommand:
		return m.executeUploads(ctx, ownerID, state, nextRevision, now, typed)
	case RetryFailedUploadsCommand:
		return m.retryFailedUploads(ctx, ownerID, state, nextRevision, now, typed)
	case RetryClientInjectionsCommand:
		return m.retryClientInjections(ctx, ownerID, state, nextRevision, now, typed)
	case CancelWorkflowCommand:
		return m.cancelWorkflow(ctx, ownerID, state)
	case InvalidateTrackersCommand:
		return m.invalidateTrackers(ctx, ownerID, state, nextRevision, now, typed)
	case ResolveActionCommand:
		return m.resolveAction(ctx, ownerID, state, nextRevision, now, typed)
	case applyCompositeUploadFeedbackCommand:
		return m.applyCompositeUploadFeedback(ctx, ownerID, state, nextRevision, now, typed)
	case CompositeUploadCommand:
		return CommandResult{}, errors.New("release workflow composite command cannot update one workflow stage")
	case CreateWorkflowCommand:
		return CommandResult{}, errors.New("release workflow create command cannot update existing state")
	}
	return CommandResult{}, errors.New("release workflow unsupported command")
}

// invalidateWorkflowPrivateResources clears stale stage authority while
// retaining the active operation's durable command capsule.
func (m *Module) invalidateWorkflowPrivateResources(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
) {
	operationID, _ := ctx.Value(operationExecutionContextKey{}).(api.WorkflowOperationID)
	if operationID == "" {
		m.private.InvalidateWorkflow(ownerID, workflowID)
		return
	}
	m.private.InvalidateWorkflowExcept(ownerID, workflowID, operationCommandResourceID(operationID))
}

func (m *Module) cancelWorkflow(ctx context.Context, ownerID string, state *State) (CommandResult, error) {
	switch state.Workflow.Status {
	case api.WorkflowStatusCompleted, api.WorkflowStatusCanceled:
		return CommandResult{}, fmt.Errorf("%w: terminal workflow cannot be canceled", ErrInvalidTransition)
	case api.WorkflowStatusDraft, api.WorkflowStatusActive, api.WorkflowStatusBlocked, api.WorkflowStatusFailed:
	}
	m.invalidateWorkflowPrivateResources(ctx, ownerID, state.Workflow.ID)
	state.Workflow.Release = nil
	state.Workflow.TrackerCatalog = nil
	state.Workflow.TrackerRuntime = nil
	state.Workflow.Selection = nil
	state.Workflow.ProjectionInstructions = nil
	state.Workflow.TrackerProjections = nil
	state.Workflow.TrackerPreflight = nil
	state.Workflow.Dupes = nil
	state.Workflow.TrackerApproval = nil
	state.Workflow.Media = nil
	state.Workflow.Descriptions = nil
	state.Workflow.DryRun = nil
	state.Workflow.UploadResult = nil
	state.Workflow.RequiredActions = nil
	state.Workflow.Failures = nil
	state.Workflow.Status = api.WorkflowStatusCanceled
	return CommandResult{}, nil
}

func (m *Module) replaceFactInstructions(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command ReplaceFactInstructionsCommand,
) (CommandResult, error) {
	id, err := m.newID("facts")
	if err != nil {
		return CommandResult{}, err
	}
	snapshot := api.ReleaseFactInstructionSnapshot{
		ID:           api.ReleaseFactInstructionSnapshotID(id),
		WorkflowID:   state.Workflow.ID,
		Revision:     nextRevision,
		Instructions: command.Instructions,
		CreatedAt:    now,
	}
	snapshot, err = snapshot.WithFingerprint()
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow replace facts: %w", err)
	}
	if err := snapshot.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow replace facts: %w", err)
	}
	state.FactInstructions[snapshot.ID] = snapshot
	state.Workflow.FactInstructions = api.ReleaseFactInstructionSnapshotRef{ID: snapshot.ID, Revision: snapshot.Revision}
	invalidatePreparedAndDownstream(&state.Workflow)
	state.Workflow.Status = api.WorkflowStatusDraft
	state.Workflow.RequiredActions = nil
	state.Workflow.Failures = nil
	m.invalidateWorkflowPrivateResources(ctx, ownerID, state.Workflow.ID)
	return CommandResult{FactInstructions: &snapshot}, nil
}

func (m *Module) prepareRelease(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command PrepareReleaseCommand,
) (CommandResult, error) {
	facts, ok := state.FactInstructions[state.Workflow.FactInstructions.ID]
	if !ok || facts.Revision != state.Workflow.FactInstructions.Revision {
		return CommandResult{}, fmt.Errorf("%w: fact instructions unavailable", ErrInvalidTransition)
	}
	command.Input.Instructions = facts.Instructions
	prepared, err := m.preparer.Prepare(ctx, command.Input)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow prepare canonical release: %w", err)
	}
	ref := api.ReleaseRef{SourcePath: prepared.Release.Source.SourcePath, Generation: prepared.Release.Generation}
	display, err := m.preparer.ResolveDisplay(ctx, ref)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow project release display: %w", err)
	}
	id, err := m.newID("release")
	if err != nil {
		return CommandResult{}, err
	}
	snapshot := api.ReleaseSnapshot{
		ID:               api.ReleaseSnapshotID(id),
		WorkflowID:       state.Workflow.ID,
		Revision:         nextRevision,
		FactInstructions: state.Workflow.FactInstructions,
		Release:          prepared.Release,
		Display:          display,
		Diagnostics:      prepared.Diagnostics,
		CreatedAt:        now,
	}
	snapshot.PreparationFingerprint, err = api.CanonicalWorkflowFingerprint(command.Input)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow fingerprint preparation input: %w", err)
	}
	snapshot.Fingerprint, err = snapshot.ComputeFingerprint()
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow fingerprint release: %w", err)
	}
	if err := snapshot.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow publish release: %w", err)
	}
	state.Releases[snapshot.ID] = snapshot
	prior := state.Workflow.Release
	state.Workflow.Release = &api.ReleaseSnapshotRef{ID: snapshot.ID, Revision: snapshot.Revision}
	if prior == nil || prior.ID != snapshot.ID || prior.Revision != snapshot.Revision {
		invalidateTrackerAndDownstream(&state.Workflow)
		m.invalidateWorkflowPrivateResources(ctx, ownerID, state.Workflow.ID)
	}
	state.Workflow.Status = api.WorkflowStatusActive
	state.Workflow.RequiredActions = nil
	state.Workflow.Failures = nil
	if strings.EqualFold(strings.TrimSpace(prepared.Release.Source.Classification.DiscType), "BDMV") &&
		!facts.Instructions.Playlist.Set {
		options := make([]api.RequiredActionOption, 0)
		for _, entry := range prepared.Release.Source.Entries {
			playlist := strings.TrimSpace(entry.Playlist)
			if entry.Type != api.SourceEntryTypePlaylist || playlist == "" {
				continue
			}
			options = append(options, api.RequiredActionOption{Value: playlist, Label: playlist})
		}
		if len(options) > 0 {
			actionID, actionErr := m.newID("action")
			if actionErr != nil {
				return CommandResult{}, actionErr
			}
			state.Workflow.Status = api.WorkflowStatusBlocked
			state.Workflow.RequiredActions = []api.RequiredAction{{
				ID:               api.RequiredActionID(actionID),
				Kind:             api.RequiredActionSelectPlaylist,
				Status:           api.RequiredActionStatusPending,
				WorkflowRevision: nextRevision,
				Prompt:           "Select one or more Blu-ray playlists to analyze.",
				Options:          options,
				CreatedAt:        now,
			}}
		}
	}
	return CommandResult{Release: &snapshot}, nil
}

func (m *Module) resetRelease(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command ResetReleaseCommand,
) (CommandResult, error) {
	current, err := currentReleaseSnapshot(state)
	if err != nil {
		return CommandResult{}, err
	}
	sourcePath := strings.TrimSpace(command.Input.SourcePath)
	if sourcePath == "" || sourcePath != strings.TrimSpace(current.Release.Source.SourcePath) {
		return CommandResult{}, fmt.Errorf("%w: reset source does not match the current release", ErrInvalidTransition)
	}
	command.Input.SourcePath = sourcePath
	command.Input.Force = true
	return m.replaceFactsAndPrepare(ctx, ownerID, state, nextRevision, now, command.Input)
}

func (m *Module) selectBlurayCandidate(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command SelectBlurayCandidateCommand,
) (CommandResult, error) {
	current, err := currentReleaseSnapshot(state)
	if err != nil {
		return CommandResult{}, err
	}
	releaseID := strings.TrimSpace(command.ReleaseID)
	bluray := current.Release.ProviderMetadata.Bluray
	if bluray == nil {
		return CommandResult{}, fmt.Errorf("%w: retained Blu-ray candidates are unavailable", ErrInvalidTransition)
	}
	found := false
	for _, candidate := range bluray.Candidates {
		if strings.TrimSpace(candidate.ReleaseID) == releaseID {
			found = true
			break
		}
	}
	if !found {
		return CommandResult{}, fmt.Errorf("%w: Blu-ray candidate is not retained by this workflow", ErrInvalidTransition)
	}
	facts, ok := state.FactInstructions[state.Workflow.FactInstructions.ID]
	if !ok || facts.Revision != state.Workflow.FactInstructions.Revision {
		return CommandResult{}, fmt.Errorf("%w: fact instructions unavailable", ErrInvalidTransition)
	}
	instructions := facts.Instructions
	instructions.BlurayReleaseID = releaseID
	return m.replaceFactsAndPrepare(ctx, ownerID, state, nextRevision, now, api.PrepareInput{
		SourcePath:   current.Release.Source.SourcePath,
		Intent:       api.PreparationIntentPreview,
		Instructions: instructions,
		Controls: api.PreparationControls{
			Interaction: api.InteractionModeInteractive,
		},
		Force: true,
	})
}

func (m *Module) replaceFactsAndPrepare(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	input api.PrepareInput,
) (CommandResult, error) {
	factsResult, err := m.replaceFactInstructions(ctx, ownerID, state, nextRevision, now, ReplaceFactInstructionsCommand{
		Instructions: input.Instructions,
	})
	if err != nil {
		return CommandResult{}, err
	}
	preparedResult, err := m.prepareRelease(ctx, ownerID, state, nextRevision, now, PrepareReleaseCommand{Input: input})
	if err != nil {
		return CommandResult{}, err
	}
	preparedResult.FactInstructions = factsResult.FactInstructions
	return preparedResult, nil
}

func currentReleaseSnapshot(state *State) (api.ReleaseSnapshot, error) {
	if state.Workflow.Release == nil {
		return api.ReleaseSnapshot{}, fmt.Errorf("%w: current release is required", ErrInvalidTransition)
	}
	current, ok := state.Releases[state.Workflow.Release.ID]
	if !ok || current.Revision != state.Workflow.Release.Revision {
		return api.ReleaseSnapshot{}, fmt.Errorf("%w: current release is unavailable", ErrInvalidTransition)
	}
	return current, nil
}

func (m *Module) setTrackerContext(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command trackerContextPublication,
) (CommandResult, error) {
	if state.Workflow.Release == nil {
		return CommandResult{}, fmt.Errorf("%w: release is required before tracker selection", ErrInvalidTransition)
	}
	catalogID, err := m.newID("catalog")
	if err != nil {
		return CommandResult{}, err
	}
	command.Catalog.ID = api.TrackerCatalogSnapshotID(catalogID)
	command.Catalog.Revision = nextRevision
	command.Catalog.CreatedAt = now
	catalog, err := command.Catalog.WithFingerprint()
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow tracker catalog: %w", err)
	}
	if err := catalog.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow tracker catalog: %w", err)
	}

	runtimeID, err := m.newID("runtime")
	if err != nil {
		return CommandResult{}, err
	}
	command.Runtime.ID = api.TrackerRuntimeSnapshotID(runtimeID)
	command.Runtime.Revision = nextRevision
	command.Runtime.Catalog = api.TrackerCatalogSnapshotRef{ID: catalog.ID, Revision: catalog.Revision}
	command.Runtime.CreatedAt = now
	runtime, err := command.Runtime.WithFingerprint()
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow tracker runtime: %w", err)
	}
	if err := runtime.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow tracker runtime: %w", err)
	}

	selectionID, err := m.newID("selection")
	if err != nil {
		return CommandResult{}, err
	}
	command.Selection.ID = api.TrackerSelectionID(selectionID)
	command.Selection.WorkflowID = state.Workflow.ID
	command.Selection.Revision = nextRevision
	command.Selection.Catalog = runtime.Catalog
	command.Selection.Runtime = api.TrackerRuntimeSnapshotRef{ID: runtime.ID, Revision: runtime.Revision}
	command.Selection.CreatedAt = now
	selection, err := command.Selection.WithFingerprint()
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow tracker selection: %w", err)
	}
	if err := selection.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow tracker selection: %w", err)
	}

	state.Catalogs[catalog.ID] = catalog
	state.Runtimes[runtime.ID] = runtime
	state.Selections[selection.ID] = selection
	state.Workflow.TrackerCatalog = &api.TrackerCatalogSnapshotRef{ID: catalog.ID, Revision: catalog.Revision}
	state.Workflow.TrackerRuntime = &api.TrackerRuntimeSnapshotRef{ID: runtime.ID, Revision: runtime.Revision}
	state.Workflow.Selection = &api.TrackerSelectionRef{ID: selection.ID, Revision: selection.Revision}
	invalidateProjectionAndDownstream(&state.Workflow)
	m.invalidateWorkflowPrivateResources(ctx, ownerID, state.Workflow.ID)
	state.Workflow.Status = api.WorkflowStatusActive
	return CommandResult{
		Catalog:   &catalog,
		Runtime:   &runtime,
		Selection: &selection,
	}, nil
}

func (m *Module) projectTrackers(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command ProjectTrackersCommand,
) (CommandResult, error) {
	if m.trackerProjector == nil {
		return CommandResult{}, fmt.Errorf("%w: tracker projection builder is unavailable", ErrInvalidTransition)
	}
	if state.Workflow.Release == nil {
		return CommandResult{}, fmt.Errorf("%w: release is required before tracker projection", ErrInvalidTransition)
	}
	release, ok := state.Releases[state.Workflow.Release.ID]
	if !ok || release.Revision != state.Workflow.Release.Revision {
		return CommandResult{}, fmt.Errorf("%w: release snapshot is unavailable", ErrInvalidTransition)
	}
	trackerNames := make([]string, len(command.TrackerIDs))
	for index, trackerID := range command.TrackerIDs {
		trackerNames[index] = string(trackerID)
	}
	subject, err := m.preparer.ResolveUploadSubject(ctx, api.UploadSubjectInput{
		Release: api.ReleaseRef{
			SourcePath: release.Release.Source.SourcePath,
			Generation: release.Release.Generation,
		},
		Trackers: trackerNames,
	})
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow resolve tracker projection subject: %w", err)
	}
	catalog, runtime, selection, projections, err := m.trackerProjector.Build(
		ctx,
		release,
		subject,
		command.TrackerIDs,
		command.Instructions,
		command.ExecutionMode,
	)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow build tracker projections: %w", err)
	}
	contextResult, err := m.setTrackerContext(ctx, ownerID, state, nextRevision, now, trackerContextPublication{
		Catalog:   catalog,
		Runtime:   runtime,
		Selection: selection,
	})
	if err != nil {
		return CommandResult{}, err
	}
	instructionID, err := m.newID("projection_instructions")
	if err != nil {
		return CommandResult{}, err
	}
	instructionSnapshot := api.TrackerProjectionInstructionSnapshot{
		ID:           api.TrackerProjectionInstructionSnapshotID(instructionID),
		WorkflowID:   state.Workflow.ID,
		Revision:     nextRevision,
		Instructions: command.Instructions,
		CreatedAt:    now,
	}
	instructionSnapshot, err = instructionSnapshot.WithFingerprint()
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow projection instructions: %w", err)
	}
	if err := instructionSnapshot.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow projection instructions: %w", err)
	}
	state.ProjectionInstructions[instructionSnapshot.ID] = instructionSnapshot
	state.Workflow.ProjectionInstructions = &api.TrackerProjectionInstructionSnapshotRef{
		ID:       instructionSnapshot.ID,
		Revision: instructionSnapshot.Revision,
	}
	projections.Instructions = state.Workflow.ProjectionInstructions
	projectionResult, err := m.publishProjections(ctx, ownerID, state, nextRevision, now, projectionSetPublication{Snapshot: projections})
	if err != nil {
		return CommandResult{}, err
	}
	return CommandResult{
		Catalog:                contextResult.Catalog,
		Runtime:                contextResult.Runtime,
		Selection:              contextResult.Selection,
		ProjectionInstructions: &instructionSnapshot,
		Projections:            projectionResult.Projections,
	}, nil
}

func (m *Module) publishProjections(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command projectionSetPublication,
) (CommandResult, error) {
	workflow := state.Workflow
	if workflow.Release == nil || workflow.TrackerCatalog == nil || workflow.TrackerRuntime == nil || workflow.Selection == nil {
		return CommandResult{}, fmt.Errorf("%w: tracker projection dependencies are incomplete", ErrInvalidTransition)
	}
	id, err := m.newID("projections")
	if err != nil {
		return CommandResult{}, err
	}
	snapshot := command.Snapshot
	snapshot.ID = api.TrackerReleaseProjectionSetID(id)
	snapshot.WorkflowID = workflow.ID
	snapshot.Revision = nextRevision
	snapshot.Release = *workflow.Release
	release := state.Releases[workflow.Release.ID]
	snapshot.ReleaseRef = api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation}
	snapshot.Catalog = *workflow.TrackerCatalog
	snapshot.Runtime = *workflow.TrackerRuntime
	snapshot.Selection = *workflow.Selection
	snapshot.CreatedAt = now
	if err := snapshot.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow publish projections: %w", err)
	}
	state.Projections[snapshot.ID] = snapshot
	state.Workflow.TrackerProjections = &api.TrackerReleaseProjectionSetRef{ID: snapshot.ID, Revision: snapshot.Revision}
	invalidatePreflightAndDownstream(&state.Workflow)
	m.invalidateWorkflowPrivateResources(ctx, ownerID, state.Workflow.ID)
	setWorkflowStageStatus(&state.Workflow, snapshot.Status, snapshot.RequiredActions, snapshot.Failures)
	return CommandResult{Projections: &snapshot}, nil
}

func (m *Module) preflightTrackers(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command PreflightTrackersCommand,
) (CommandResult, error) {
	if m.trackerPreflight == nil {
		return CommandResult{}, fmt.Errorf("%w: tracker preflight builder is unavailable", ErrInvalidTransition)
	}
	workflow := state.Workflow
	if workflow.TrackerCatalog == nil || workflow.TrackerRuntime == nil || workflow.TrackerProjections == nil {
		return CommandResult{}, fmt.Errorf("%w: tracker projection dependencies are incomplete", ErrInvalidTransition)
	}
	catalog, catalogOK := state.Catalogs[workflow.TrackerCatalog.ID]
	runtime, runtimeOK := state.Runtimes[workflow.TrackerRuntime.ID]
	initial, projectionsOK := state.Projections[workflow.TrackerProjections.ID]
	if !catalogOK || !runtimeOK || !projectionsOK || catalog.Revision != workflow.TrackerCatalog.Revision ||
		runtime.Revision != workflow.TrackerRuntime.Revision || initial.Revision != workflow.TrackerProjections.Revision {
		return CommandResult{}, fmt.Errorf("%w: tracker preflight dependencies are unavailable", ErrInvalidTransition)
	}
	trackerIDs := make([]string, len(initial.Projections))
	for index, projection := range initial.Projections {
		trackerIDs[index] = string(projection.TrackerID)
	}
	subject, err := m.preparer.ResolveUploadSubject(ctx, api.UploadSubjectInput{
		Release:  initial.ReleaseRef,
		Trackers: trackerIDs,
	})
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow resolve tracker preflight subject: %w", err)
	}
	if api.NormalizeWorkflowExecutionMode(command.ExecutionMode) != api.NormalizeWorkflowExecutionMode(initial.ExecutionMode) {
		return CommandResult{}, fmt.Errorf("%w: tracker preflight execution mode does not match projections", ErrInvalidTransition)
	}
	assessment, finalized, err := m.trackerPreflight.Build(ctx, subject, catalog, runtime, initial, now)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow build tracker preflight: %w", err)
	}
	if err := validatePreflightBuild(initial, assessment, finalized); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow build tracker preflight: %w", err)
	}
	applyPreflightInteractionPolicy(command.Interaction, &assessment, finalized)
	if command.InputFingerprint != "" {
		assessment.InputFingerprint = command.InputFingerprint
	}
	preflightID, err := m.newID("preflight")
	if err != nil {
		return CommandResult{}, err
	}
	assessment.ID = api.TrackerPreflightAssessmentID(preflightID)
	assessment.WorkflowID = workflow.ID
	assessment.Revision = nextRevision
	assessment.ProjectionSet = *workflow.TrackerProjections
	assessment.Runtime = *workflow.TrackerRuntime
	assessment.ExecutionMode = api.NormalizeWorkflowExecutionMode(command.ExecutionMode)
	assessment.CreatedAt = now
	if err := m.stampPreflightActions(&assessment, nextRevision, now); err != nil {
		return CommandResult{}, err
	}
	assessment.Status = preflightStageStatus(assessment.Results)
	if err := assessment.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow finalize tracker preflight: %w", err)
	}

	projectionID, err := m.newID("projections")
	if err != nil {
		return CommandResult{}, err
	}
	finalSet := initial
	finalSet.ID = api.TrackerReleaseProjectionSetID(projectionID)
	finalSet.Revision = nextRevision
	finalSet.Preflight = &api.TrackerPreflightAssessmentRef{ID: assessment.ID, Revision: assessment.Revision}
	finalSet.Projections = finalized
	finalSet.RequiredActions = collectPreflightActions(assessment.Results)
	finalSet.Failures = collectPreflightFailures(assessment.Results)
	finalSet.Status = finalizedProjectionStatus(finalized, finalSet.RequiredActions, finalSet.Failures)
	finalSet.CreatedAt = now
	if err := finalSet.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow finalize tracker projections: %w", err)
	}
	state.Preflights[assessment.ID] = assessment
	state.Projections[finalSet.ID] = finalSet
	state.Workflow.TrackerPreflight = finalSet.Preflight
	state.Workflow.TrackerProjections = &api.TrackerReleaseProjectionSetRef{ID: finalSet.ID, Revision: finalSet.Revision}
	invalidateDupeAndDownstream(&state.Workflow)
	m.invalidateWorkflowPrivateResources(ctx, ownerID, state.Workflow.ID)
	setWorkflowStageStatus(&state.Workflow, finalSet.Status, finalSet.RequiredActions, finalSet.Failures)
	return CommandResult{Preflight: &assessment, Projections: &finalSet}, nil
}

func applyPreflightInteractionPolicy(
	interaction api.InteractionMode,
	assessment *api.TrackerPreflightAssessment,
	finalized []api.TrackerReleaseProjection,
) {
	if interaction != api.InteractionModeUnattended || assessment == nil {
		return
	}
	projectionIndexes := make(map[api.TrackerID]int, len(finalized))
	for index := range finalized {
		projectionIndexes[finalized[index].TrackerID] = index
	}
	for index := range assessment.Results {
		result := &assessment.Results[index]
		if len(result.RequiredActions) == 0 {
			continue
		}
		result.RequiredActions = nil
		result.State = api.TrackerPreflightStateFailed
		if len(result.Failures) == 0 {
			result.Failures = []api.WorkflowFailure{{
				Failure: api.OperationFailure{
					Code:      api.OperationFailureMissingPrerequisite,
					Operation: api.OperationKindDuplicateCheck,
					Message:   "Tracker requires manual input and was skipped in unattended mode.",
					Recovery:  api.OperationRecoveryCompletePrerequisite,
				},
				TrackerID: result.TrackerID,
			}}
		}
		projectionIndex, ok := projectionIndexes[result.TrackerID]
		if !ok {
			continue
		}
		projection := &finalized[projectionIndex]
		projection.RequiredActions = nil
		if len(projection.Failures) == 0 {
			projection.Failures = append([]api.WorkflowFailure(nil), result.Failures...)
		}
		appendPreflightFailureDecision(projection, result.Failures)
		projection.Readiness = api.ReadinessStatusIneligible
		projection.DupeReady = false
		projection.UploadReady = false
	}
}

func appendPreflightFailureDecision(projection *api.TrackerReleaseProjection, failures []api.WorkflowFailure) {
	if projection == nil {
		return
	}
	for _, failure := range failures {
		code := strings.TrimSpace(string(failure.Failure.Code))
		reason := strings.TrimSpace(failure.Failure.Message)
		if code == "" || reason == "" {
			continue
		}
		if slices.ContainsFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
			return strings.EqualFold(strings.TrimSpace(decision.Code), code)
		}) {
			return
		}
		projection.PolicyDecisions = append(projection.PolicyDecisions, api.TrackerPolicyDecision{
			Code:     code,
			Decision: "ineligible",
			Blocking: true,
			Message:  reason,
		})
		return
	}
}

func validatePreflightBuild(
	initial api.TrackerReleaseProjectionSet,
	assessment api.TrackerPreflightAssessment,
	finalized []api.TrackerReleaseProjection,
) error {
	if api.NormalizeWorkflowExecutionMode(assessment.ExecutionMode) != api.NormalizeWorkflowExecutionMode(initial.ExecutionMode) {
		return errors.New("preflight execution mode does not match projections")
	}
	if len(initial.Projections) == 0 || len(assessment.Results) != len(initial.Projections) || len(finalized) != len(initial.Projections) {
		return errors.New("preflight must return one result and finalized projection per selected tracker")
	}
	results := make(map[api.TrackerID]api.TrackerPreflightResult, len(assessment.Results))
	finalByTracker := make(map[api.TrackerID]api.TrackerReleaseProjection, len(finalized))
	for _, result := range assessment.Results {
		results[result.TrackerID] = result
	}
	for _, projection := range finalized {
		finalByTracker[projection.TrackerID] = projection
	}
	for _, projection := range initial.Projections {
		result, resultOK := results[projection.TrackerID]
		finalProjection, finalOK := finalByTracker[projection.TrackerID]
		if !resultOK || !finalOK {
			return fmt.Errorf("preflight omitted tracker %s", projection.TrackerID)
		}
		fingerprint, err := api.CanonicalWorkflowFingerprint(projection)
		if err != nil {
			return fmt.Errorf("fingerprint initial projection %s: %w", projection.TrackerID, err)
		}
		if result.ConfigFingerprint != projection.ConfigFingerprint || result.ProjectionFingerprint != fingerprint {
			return fmt.Errorf("preflight fingerprint mismatch for tracker %s", projection.TrackerID)
		}
		if finalProjection.InputFingerprint != projection.InputFingerprint ||
			finalProjection.CatalogFingerprint != projection.CatalogFingerprint ||
			finalProjection.ConfigFingerprint != projection.ConfigFingerprint {
			return fmt.Errorf("finalized projection dependency mismatch for tracker %s", projection.TrackerID)
		}
	}
	return nil
}

func (m *Module) stampPreflightActions(
	assessment *api.TrackerPreflightAssessment,
	revision api.WorkflowRevision,
	now time.Time,
) error {
	for resultIndex := range assessment.Results {
		result := &assessment.Results[resultIndex]
		for actionIndex := range result.RequiredActions {
			action := &result.RequiredActions[actionIndex]
			if action.ID == "" {
				id, err := m.newID("action")
				if err != nil {
					return err
				}
				action.ID = api.RequiredActionID(id)
			}
			action.Status = api.RequiredActionStatusPending
			action.WorkflowRevision = revision
			action.TrackerID = result.TrackerID
			action.CreatedAt = now
			if action.ExpiresAt == nil {
				expiresAt := result.FreshUntil
				action.ExpiresAt = &expiresAt
			}
		}
	}
	return nil
}

func preflightStageStatus(results []api.TrackerPreflightResult) api.StageStatus {
	readyCount := 0
	hasRecoverable := false
	for _, result := range results {
		switch result.State {
		case api.TrackerPreflightStateReady:
			readyCount++
		case api.TrackerPreflightStateFailed:
		case api.TrackerPreflightStateActionRequired, api.TrackerPreflightStateRetryable, api.TrackerPreflightStateExpired:
			hasRecoverable = true
		default:
			return api.StageStatusFailed
		}
	}
	if readyCount > 0 {
		return api.StageStatusReady
	}
	if hasRecoverable {
		return api.StageStatusBlocked
	}
	return api.StageStatusFailed
}

func collectPreflightActions(results []api.TrackerPreflightResult) []api.RequiredAction {
	var actions []api.RequiredAction
	for _, result := range results {
		actions = append(actions, result.RequiredActions...)
	}
	return actions
}

func collectPreflightFailures(results []api.TrackerPreflightResult) []api.WorkflowFailure {
	var failures []api.WorkflowFailure
	for _, result := range results {
		failures = append(failures, result.Failures...)
	}
	return failures
}

func finalizedProjectionStatus(
	projections []api.TrackerReleaseProjection,
	actions []api.RequiredAction,
	failures []api.WorkflowFailure,
) api.StageStatus {
	readyCount := 0
	for _, projection := range projections {
		if projection.DupeReady && projection.Readiness == api.ReadinessStatusReady {
			readyCount++
		}
	}
	if readyCount > 0 {
		return api.StageStatusReady
	}
	if len(actions) > 0 {
		return api.StageStatusBlocked
	}
	_ = failures
	return api.StageStatusFailed
}

func (m *Module) checkDuplicates(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command CheckDuplicatesCommand,
) (CommandResult, error) {
	if m.dupeBuilder == nil {
		return CommandResult{}, fmt.Errorf("%w: duplicate assessment builder is unavailable", ErrInvalidTransition)
	}
	workflow := state.Workflow
	if workflow.TrackerProjections == nil || workflow.TrackerPreflight == nil {
		return CommandResult{}, fmt.Errorf("%w: finalized tracker projections and preflight are required", ErrInvalidTransition)
	}
	projections, projectionsOK := state.Projections[workflow.TrackerProjections.ID]
	preflight, preflightOK := state.Preflights[workflow.TrackerPreflight.ID]
	if !projectionsOK || !preflightOK || projections.Preflight == nil || *projections.Preflight != *workflow.TrackerPreflight ||
		projections.Status != api.StageStatusReady || preflight.Status != api.StageStatusReady || !preflight.ExpiresAt.After(now) {
		return CommandResult{}, fmt.Errorf("%w: duplicate assessment dependencies are stale or not ready", ErrInvalidTransition)
	}
	trackerIDs := make([]string, len(projections.Projections))
	for index, projection := range projections.Projections {
		trackerIDs[index] = string(projection.TrackerID)
	}
	subject, err := m.preparer.ResolveDuplicateSubject(ctx, api.DuplicateCheckInput{
		Release:  projections.ReleaseRef,
		Trackers: trackerIDs,
		Skip:     command.SkipRemote,
	})
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow resolve duplicate subject: %w", err)
	}
	snapshot, privateEvidence, err := m.dupeBuilder.Build(ctx, subject, projections, preflight, now, command.SkipRemote)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow build duplicate assessment: %w", err)
	}
	if err := validateDupeBuild(projections, snapshot); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow build duplicate assessment: %w", err)
	}
	snapshot.CheckOrdinal = normalizedDuplicateCheckOrdinal(command.CheckOrdinal)
	snapshot.InputFingerprint, err = api.CanonicalWorkflowFingerprint(struct {
		BuilderFingerprint api.WorkflowFingerprint
		CheckOrdinal       uint8
	}{
		BuilderFingerprint: snapshot.InputFingerprint,
		CheckOrdinal:       snapshot.CheckOrdinal,
	})
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow fingerprint duplicate assessment ordinal: %w", err)
	}
	if err := m.stampDupeActions(&snapshot, nextRevision, now); err != nil {
		return CommandResult{}, err
	}
	snapshot.Status = dupeStageStatus(snapshot.Results)
	result, err := m.publishDupes(ownerID, state, nextRevision, now, dupeAssessmentPublication{Snapshot: snapshot})
	if err != nil {
		return CommandResult{}, err
	}
	if privateEvidence != nil && result.Dupes != nil {
		if err := m.private.Put(
			ownerID,
			workflow.ID,
			dupePrivateResourceID(result.Dupes.ID),
			privateEvidence,
			result.Dupes.ExpiresAt,
		); err != nil {
			return CommandResult{}, fmt.Errorf("release workflow retain duplicate evidence: %w", err)
		}
	}
	return result, nil
}

func (m *Module) decideDuplicates(
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command DecideDuplicatesCommand,
) (CommandResult, error) {
	if state.Workflow.Dupes == nil {
		return CommandResult{}, fmt.Errorf("%w: duplicate assessment is required", ErrInvalidTransition)
	}
	current, ok := state.Dupes[state.Workflow.Dupes.ID]
	if !ok || current.Revision != state.Workflow.Dupes.Revision || !current.ExpiresAt.After(now) {
		return CommandResult{}, fmt.Errorf("%w: duplicate assessment is stale or unavailable", ErrInvalidTransition)
	}
	privateEvidence, privateErr := m.private.Get(
		ownerID,
		state.Workflow.ID,
		dupePrivateResourceID(current.ID),
		now,
	)
	if privateErr != nil && !errors.Is(privateErr, ErrPrivateResourceUnavailable) {
		return CommandResult{}, fmt.Errorf("release workflow load duplicate evidence: %w", privateErr)
	}
	snapshot, err := current.Clone()
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow clone duplicate assessment: %w", err)
	}
	seen := make(map[api.TrackerID]struct{}, len(snapshot.Results))
	for index := range snapshot.Results {
		result := &snapshot.Results[index]
		seen[result.TrackerID] = struct{}{}
		decision, requested := command.Decisions[result.TrackerID]
		if !requested {
			if result.Decision == api.DupeDecisionPending {
				for actionIndex := range result.RequiredActions {
					result.RequiredActions[actionIndex].ID = ""
				}
			}
			continue
		}
		if (result.Decision != api.DupeDecisionPending && result.Decision != api.DupeDecisionAccepted &&
			result.Decision != api.DupeDecisionIgnored) ||
			(decision != api.DupeDecisionAccepted && decision != api.DupeDecisionIgnored) {
			return CommandResult{}, fmt.Errorf("%w: invalid duplicate decision for tracker %s", ErrInvalidTransition, result.TrackerID)
		}
		if decision == api.DupeDecisionIgnored && strictDupeResult(*result) {
			return CommandResult{}, fmt.Errorf("%w: tracker %s is already in client and cannot be overridden", ErrInvalidTransition, result.TrackerID)
		}
		result.Decision = decision
		result.Status = api.StageStatusCompleted
		result.RequiredActions = nil
	}
	for trackerID := range command.Decisions {
		if _, ok := seen[trackerID]; !ok {
			return CommandResult{}, fmt.Errorf("%w: duplicate decision tracker %s is absent", ErrInvalidTransition, trackerID)
		}
	}
	if err := m.stampDupeActions(&snapshot, nextRevision, now); err != nil {
		return CommandResult{}, err
	}
	snapshot.Status = dupeStageStatus(snapshot.Results)
	result, err := m.publishDupes(ownerID, state, nextRevision, now, dupeAssessmentPublication{Snapshot: snapshot})
	if err != nil {
		return CommandResult{}, err
	}
	if privateErr == nil && result.Dupes != nil {
		if err := m.private.Put(
			ownerID,
			state.Workflow.ID,
			dupePrivateResourceID(result.Dupes.ID),
			privateEvidence,
			result.Dupes.ExpiresAt,
		); err != nil {
			return CommandResult{}, fmt.Errorf("release workflow retain decided duplicate evidence: %w", err)
		}
	}
	return result, nil
}

func (m *Module) approveTrackers(
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command ApproveTrackersCommand,
) (CommandResult, error) {
	if normalizeTrackerDecisionMode(state.TrackerDecisionMode) != TrackerDecisionModePostDupeGate {
		return CommandResult{}, fmt.Errorf("%w: tracker approval is unavailable for this workflow", ErrInvalidTransition)
	}
	action, actionFingerprint, err := projectedTrackerApprovalAction(state, now)
	if err != nil {
		return CommandResult{}, err
	}
	eligible, dupes, candidateIDs, err := trackerApprovalCandidates(state, now)
	if err != nil {
		return CommandResult{}, err
	}
	if action == nil || command.Approval.ActionID != action.ID ||
		state.Workflow.Dupes == nil || command.Approval.Dupes != *state.Workflow.Dupes ||
		command.Approval.InputFingerprint != dupes.InputFingerprint {
		return CommandResult{}, fmt.Errorf("%w: tracker approval action is stale", ErrRevisionConflict)
	}
	requested := make(map[api.TrackerID]struct{}, len(command.Approval.TrackerIDs))
	for _, trackerID := range command.Approval.TrackerIDs {
		trackerID = normalizeDownstreamTrackerID(trackerID)
		if trackerID == "" {
			return CommandResult{}, fmt.Errorf("%w: tracker approval ID is required", ErrInvalidTransition)
		}
		if _, duplicate := requested[trackerID]; duplicate {
			return CommandResult{}, fmt.Errorf("%w: tracker approval contains duplicate tracker %s", ErrInvalidTransition, trackerID)
		}
		if !slices.Contains(candidateIDs, trackerID) {
			return CommandResult{}, fmt.Errorf("%w: tracker %s is not an approval candidate", ErrInvalidTransition, trackerID)
		}
		requested[trackerID] = struct{}{}
	}
	if len(requested) == 0 {
		return CommandResult{}, fmt.Errorf("%w: tracker approval requires at least one tracker", ErrInvalidTransition)
	}
	approvedIDs := make([]api.TrackerID, 0, len(requested))
	for _, projection := range eligible.Projections {
		if _, approved := requested[projection.TrackerID]; approved {
			approvedIDs = append(approvedIDs, projection.TrackerID)
		}
	}
	id, err := m.newID("tracker_approval")
	if err != nil {
		return CommandResult{}, err
	}
	approvalFingerprint, err := trackerApprovalFingerprint(actionFingerprint, candidateIDs, approvedIDs)
	if err != nil {
		return CommandResult{}, err
	}
	snapshot := api.TrackerApprovalSnapshot{
		ID:                  api.TrackerApprovalSnapshotID(id),
		WorkflowID:          state.Workflow.ID,
		Revision:            nextRevision,
		Release:             *state.Workflow.Release,
		Selection:           *state.Workflow.Selection,
		ProjectionSet:       *state.Workflow.TrackerProjections,
		Preflight:           *state.Workflow.TrackerPreflight,
		Dupes:               api.DupeAssessmentRef{ID: dupes.ID, Revision: dupes.Revision},
		CandidateTrackerIDs: append([]api.TrackerID(nil), candidateIDs...),
		ApprovedTrackerIDs:  approvedIDs,
		InputFingerprint:    approvalFingerprint,
		CreatedAt:           now,
	}
	if err := snapshot.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow approve trackers: %w", err)
	}
	state.TrackerApprovals[snapshot.ID] = snapshot
	state.Workflow.TrackerApproval = &api.TrackerApprovalSnapshotRef{ID: snapshot.ID, Revision: snapshot.Revision}
	state.Workflow.Media = nil
	state.Workflow.Descriptions = nil
	invalidateUploadPlan(&state.Workflow)
	state.Workflow.RequiredActions = nil
	state.Workflow.Failures = nil
	state.Workflow.Status = api.WorkflowStatusActive
	return CommandResult{TrackerApproval: &snapshot}, nil
}

func strictDupeResult(result api.TrackerDupeAssessment) bool {
	return slices.ContainsFunc(result.Matches, func(match api.DupeMatchProjection) bool {
		return strings.EqualFold(strings.TrimSpace(match.Reason), "in_client")
	})
}

func validateDupeBuild(projections api.TrackerReleaseProjectionSet, snapshot api.DupeAssessment) error {
	if len(snapshot.Results) != len(projections.Projections) {
		return errors.New("duplicate assessment must return one result per projection")
	}
	results := make(map[api.TrackerID]api.TrackerDupeAssessment, len(snapshot.Results))
	for _, result := range snapshot.Results {
		results[result.TrackerID] = result
	}
	for _, projection := range projections.Projections {
		result, ok := results[projection.TrackerID]
		if !ok {
			return fmt.Errorf("duplicate assessment omitted tracker %s", projection.TrackerID)
		}
		fingerprint, err := api.CanonicalWorkflowFingerprint(projection)
		if err != nil {
			return fmt.Errorf("fingerprint duplicate projection %s: %w", projection.TrackerID, err)
		}
		criteriaFingerprint, err := api.CanonicalWorkflowFingerprint(result.Criteria)
		if err != nil {
			return fmt.Errorf("fingerprint duplicate criteria %s: %w", projection.TrackerID, err)
		}
		if result.UploadReleaseName != projection.UploadReleaseName || criteriaFingerprint != projection.CriteriaFingerprint ||
			result.ProjectionFingerprint != fingerprint || result.CriteriaFingerprint != projection.CriteriaFingerprint {
			return fmt.Errorf("duplicate assessment lineage mismatch for tracker %s", projection.TrackerID)
		}
	}
	return nil
}

func (m *Module) stampDupeActions(snapshot *api.DupeAssessment, revision api.WorkflowRevision, now time.Time) error {
	for resultIndex := range snapshot.Results {
		result := &snapshot.Results[resultIndex]
		for actionIndex := range result.RequiredActions {
			action := &result.RequiredActions[actionIndex]
			if action.ID == "" {
				id, err := m.newID("action")
				if err != nil {
					return err
				}
				action.ID = api.RequiredActionID(id)
			}
			action.Status = api.RequiredActionStatusPending
			action.WorkflowRevision = revision
			action.TrackerID = result.TrackerID
			action.CreatedAt = now
			if action.ExpiresAt == nil {
				expiresAt := result.FreshUntil
				action.ExpiresAt = &expiresAt
			}
		}
	}
	return nil
}

func dupeStageStatus(results []api.TrackerDupeAssessment) api.StageStatus {
	completedCount := 0
	hasFailure := false
	for _, result := range results {
		if result.Status == api.StageStatusFailed {
			hasFailure = true
		}
		if result.Decision == api.DupeDecisionPending || result.Status == api.StageStatusBlocked {
			return api.StageStatusBlocked
		}
		if result.Status == api.StageStatusCompleted || result.Status == api.StageStatusSkipped {
			completedCount++
		}
	}
	if completedCount > 0 {
		return api.StageStatusCompleted
	}
	if hasFailure {
		return api.StageStatusFailed
	}
	return api.StageStatusCompleted
}

func collectDupeActions(results []api.TrackerDupeAssessment) []api.RequiredAction {
	var actions []api.RequiredAction
	for _, result := range results {
		actions = append(actions, result.RequiredActions...)
	}
	return actions
}

func collectDupeFailures(results []api.TrackerDupeAssessment) []api.WorkflowFailure {
	var failures []api.WorkflowFailure
	for _, result := range results {
		failures = append(failures, result.Failures...)
	}
	return failures
}

func dupePrivateResourceID(id api.DupeAssessmentID) string { return "dupe:" + string(id) }

func (m *Module) publishDupes(
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command dupeAssessmentPublication,
) (CommandResult, error) {
	workflow := state.Workflow
	priorDupes := workflow.Dupes
	priorMedia := workflow.Media
	priorDescriptions := workflow.Descriptions
	if workflow.Release == nil || workflow.Selection == nil || workflow.TrackerProjections == nil || workflow.TrackerPreflight == nil {
		return CommandResult{}, fmt.Errorf("%w: duplicate assessment dependencies are incomplete", ErrInvalidTransition)
	}
	preflight, ok := state.Preflights[workflow.TrackerPreflight.ID]
	if !ok || preflight.Revision != workflow.TrackerPreflight.Revision || !preflight.ExpiresAt.After(now) || preflight.Status != api.StageStatusReady {
		return CommandResult{}, fmt.Errorf("%w: tracker preflight is unavailable, expired, or not ready", ErrInvalidTransition)
	}
	projections, ok := state.Projections[workflow.TrackerProjections.ID]
	if !ok || (projections.Preflight != nil && *projections.Preflight != *workflow.TrackerPreflight) {
		return CommandResult{}, fmt.Errorf("%w: finalized tracker projections do not match preflight", ErrInvalidTransition)
	}
	id, err := m.newID("dupes")
	if err != nil {
		return CommandResult{}, err
	}
	snapshot := command.Snapshot
	snapshot.ID = api.DupeAssessmentID(id)
	snapshot.WorkflowID = workflow.ID
	snapshot.Revision = nextRevision
	snapshot.Release = *workflow.Release
	release := state.Releases[workflow.Release.ID]
	snapshot.ReleaseRef = api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation}
	snapshot.Selection = *workflow.Selection
	snapshot.ProjectionSet = *workflow.TrackerProjections
	snapshot.Preflight = workflow.TrackerPreflight
	snapshot.CreatedAt = now
	if err := snapshot.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow publish dupes: %w", err)
	}
	state.Dupes[snapshot.ID] = snapshot
	state.Workflow.Dupes = &api.DupeAssessmentRef{ID: snapshot.ID, Revision: snapshot.Revision}
	state.Workflow.TrackerApproval = nil
	state.Workflow.Media = nil
	state.Workflow.Descriptions = nil
	invalidateUploadPlan(&state.Workflow)
	if priorDupes != nil {
		m.private.Delete(ownerID, state.Workflow.ID, dupePrivateResourceID(priorDupes.ID))
	}
	if priorMedia != nil {
		m.private.Delete(ownerID, state.Workflow.ID, mediaPrivateResourceID(priorMedia.ID))
	}
	if priorDescriptions != nil {
		m.private.Delete(ownerID, state.Workflow.ID, descriptionPrivateResourceID(priorDescriptions.ID))
	}
	setWorkflowStageStatus(&state.Workflow, snapshot.Status, collectDupeActions(snapshot.Results), collectDupeFailures(snapshot.Results))
	return CommandResult{Dupes: &snapshot}, nil
}

func (m *Module) captureMedia(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command CaptureMediaCommand,
) (CommandResult, error) {
	if m.mediaBuilder == nil {
		return CommandResult{}, fmt.Errorf("%w: media artifact builder is unavailable", ErrInvalidTransition)
	}
	if command.Instructions.ScreenshotCount < 0 || command.Instructions.MaxDVDMenuItems < 0 {
		return CommandResult{}, fmt.Errorf("%w: media capture counts cannot be negative", ErrInvalidTransition)
	}
	switch command.Instructions.Purpose {
	case "", api.ScreenshotPurposeFinal, api.ScreenshotPurposeMenu:
	case api.ScreenshotPurposePreview:
		return CommandResult{}, fmt.Errorf("%w: preview images cannot satisfy retained media requirements", ErrInvalidTransition)
	default:
		return CommandResult{}, fmt.Errorf("%w: invalid media capture purpose", ErrInvalidTransition)
	}
	workflow := state.Workflow
	if workflow.Release == nil || workflow.TrackerProjections == nil || workflow.Dupes == nil {
		return CommandResult{}, fmt.Errorf("%w: media dependencies are incomplete", ErrInvalidTransition)
	}
	projections, projectionsOK := state.Projections[workflow.TrackerProjections.ID]
	dupes, dupesOK := state.Dupes[workflow.Dupes.ID]
	if !projectionsOK || !dupesOK || projections.Revision != workflow.TrackerProjections.Revision ||
		dupes.Revision != workflow.Dupes.Revision || !dupes.ExpiresAt.After(now) || !dupesAllowContinuation(&projections, &dupes) {
		return CommandResult{}, fmt.Errorf("%w: media dependencies are stale or unresolved", ErrInvalidTransition)
	}
	targets, err := resolveDownstreamTrackerSet(state, nil, downstreamStageMedia, now)
	if err != nil {
		return CommandResult{}, err
	}
	eligibleProjections := targets.Projections()
	var priorMedia *api.MediaArtifactSet
	var priorPrivate any
	if workflow.Media != nil {
		media, ok := state.Media[workflow.Media.ID]
		if !ok || media.Revision != workflow.Media.Revision {
			return CommandResult{}, fmt.Errorf("%w: retained media is stale", ErrInvalidTransition)
		}
		priorMedia = &media
		priorPrivate, err = m.private.Get(ownerID, workflow.ID, mediaPrivateResourceID(media.ID), now)
		if err != nil {
			return CommandResult{}, fmt.Errorf("release workflow load retained media for capture: %w", err)
		}
	}
	var snapshot api.MediaArtifactSet
	var privateArtifacts any
	if incremental, ok := m.mediaBuilder.(IncrementalMediaArtifactBuilder); ok {
		var retained RetainedMediaResource
		snapshot, retained, err = incremental.BuildIncremental(
			ctx,
			projections.ReleaseRef,
			eligibleProjections,
			command.Instructions,
			priorMedia,
			priorPrivate,
			now,
		)
		privateArtifacts = retained
	} else {
		snapshot, privateArtifacts, err = m.mediaBuilder.Build(ctx, projections.ReleaseRef, eligibleProjections, command.Instructions, now)
	}
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow build media artifacts: %w", err)
	}
	requirementsFingerprint, err := mediaRequirementsFingerprint(eligibleProjections.Projections)
	if err != nil {
		return CommandResult{}, err
	}
	if snapshot.RequirementsFingerprint != requirementsFingerprint {
		return CommandResult{}, errors.New("release workflow build media artifacts: requirements fingerprint mismatch")
	}
	if len(snapshot.Failures) == 0 {
		refreshMutatedMediaStatus(&snapshot, eligibleProjections.Projections)
	}
	snapshot.TrackerApproval = targets.TrackerApproval()
	result, err := m.publishMedia(ownerID, state, nextRevision, now, mediaArtifactsPublication{Snapshot: snapshot})
	if err != nil {
		return CommandResult{}, err
	}
	if privateArtifacts != nil && result.Media != nil {
		if err := m.private.Put(
			ownerID,
			workflow.ID,
			mediaPrivateResourceID(result.Media.ID),
			privateArtifacts,
			now.Add(24*time.Hour),
		); err != nil {
			return CommandResult{}, fmt.Errorf("release workflow retain media artifacts: %w", err)
		}
	}
	return result, nil
}

func mediaRequirementsFingerprint(projections []api.TrackerReleaseProjection) (api.WorkflowFingerprint, error) {
	type trackerRequirements struct {
		TrackerID api.TrackerID
		Artifacts api.TrackerArtifactRequirements
	}
	requirements := make([]trackerRequirements, len(projections))
	for index, projection := range projections {
		requirements[index] = trackerRequirements{TrackerID: projection.TrackerID, Artifacts: projection.Artifacts}
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(requirements)
	if err != nil {
		return "", fmt.Errorf("release workflow media requirements fingerprint: %w", err)
	}
	return fingerprint, nil
}

func mediaPrivateResourceID(id api.MediaArtifactSetID) string { return "media:" + string(id) }

func descriptionPrivateResourceID(id api.DescriptionSetID) string { return "description:" + string(id) }

func uploadPlanPrivateResourceID(id api.UploadDryRunResultID) string {
	return "upload-plan:" + string(id)
}

func (m *Module) setMediaSelection(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command SetMediaSelectionCommand,
) (CommandResult, error) {
	snapshot, resource, err := m.currentMutableMedia(ctx, ownerID, state, command.Media, command.ArtifactIDs, now)
	if err != nil {
		return CommandResult{}, err
	}
	selected := make(map[api.PublicResourceID]struct{}, len(command.ArtifactIDs))
	for _, artifactID := range command.ArtifactIDs {
		selected[artifactID] = struct{}{}
	}
	for index := range snapshot.Artifacts {
		if _, ok := selected[snapshot.Artifacts[index].ID]; ok {
			snapshot.Artifacts[index].Selected = command.Selected
		}
	}
	snapshot.ImageRequirementsPrepared = false
	return m.publishMediaMutation(ownerID, state, nextRevision, now, snapshot, resource)
}

func (m *Module) reorderMediaArtifacts(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command ReorderMediaArtifactsCommand,
) (CommandResult, error) {
	snapshot, resource, err := m.currentMutableMedia(ctx, ownerID, state, command.Media, command.ArtifactIDs, now)
	if err != nil {
		return CommandResult{}, err
	}
	order := make(map[api.PublicResourceID]int, len(command.ArtifactIDs))
	for index, artifactID := range command.ArtifactIDs {
		order[artifactID] = index
	}
	nextOrder := len(order)
	for index := range snapshot.Artifacts {
		if artifactOrder, ok := order[snapshot.Artifacts[index].ID]; ok {
			snapshot.Artifacts[index].Order = artifactOrder
			snapshot.Artifacts[index].Selected = true
			continue
		}
		snapshot.Artifacts[index].Order = nextOrder
		nextOrder++
	}
	snapshot.ImageRequirementsPrepared = false
	return m.publishMediaMutation(ownerID, state, nextRevision, now, snapshot, resource)
}

func (m *Module) attachMediaArtifacts(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command AttachMediaArtifactsCommand,
) (CommandResult, error) {
	mutator, ok := m.mediaBuilder.(MediaArtifactMutator)
	if !ok {
		return CommandResult{}, fmt.Errorf("%w: media attachment service is unavailable", ErrInvalidTransition)
	}
	if len(command.Attachments) == 0 {
		return CommandResult{}, fmt.Errorf("%w: at least one media attachment is required", ErrInvalidTransition)
	}
	release, projections, eligible, snapshot, privateMedia, err := m.mediaExtensionContext(
		ctx,
		ownerID,
		state,
		command.Media,
		now,
	)
	if err != nil {
		return CommandResult{}, err
	}
	attachments := make([]StagedMediaAttachment, 0, len(command.Attachments))
	seen := make(map[api.WorkflowResourceID]struct{}, len(command.Attachments))
	for _, attachment := range command.Attachments {
		if attachment.Resource.ID == "" {
			return CommandResult{}, fmt.Errorf("%w: staged media resource is required", ErrInvalidTransition)
		}
		if _, duplicate := seen[attachment.Resource.ID]; duplicate {
			return CommandResult{}, fmt.Errorf("%w: duplicate staged media resource", ErrInvalidTransition)
		}
		seen[attachment.Resource.ID] = struct{}{}
		value, loadErr := m.private.Get(
			ownerID,
			state.Workflow.ID,
			stagedMediaPrivateResourceID(attachment.Resource.ID),
			now,
		)
		if loadErr != nil {
			return CommandResult{}, fmt.Errorf("release workflow load staged media: %w", loadErr)
		}
		content, contentOK := value.(StagedMediaContent)
		if !contentOK {
			return CommandResult{}, ErrPrivateResourceUnavailable
		}
		attachments = append(attachments, StagedMediaAttachment{Attachment: attachment, Content: content})
	}
	updated, retained, err := mutator.Attach(ctx, release, eligible, snapshot, privateMedia, attachments, now)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow attach media artifacts: %w", err)
	}
	if err := validateMediaRequirementsFingerprint(updated, projections); err != nil {
		return CommandResult{}, err
	}
	refreshMutatedMediaStatus(&updated, eligible.Projections)
	updated.ImageRequirementsPrepared = false
	return m.publishMediaReplacement(ownerID, state, nextRevision, now, updated, retained)
}

func (m *Module) uploadMediaImages(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command UploadMediaImagesCommand,
) (CommandResult, error) {
	mutator, ok := m.mediaBuilder.(MediaArtifactMutator)
	if !ok {
		return CommandResult{}, fmt.Errorf("%w: workflow image hosting is unavailable", ErrInvalidTransition)
	}
	artifactIDs := append([]api.PublicResourceID(nil), command.ArtifactIDs...)
	if len(artifactIDs) == 0 {
		selectedArtifactIDs, selectionErr := selectedLocalMediaArtifactIDs(state, command.Media)
		if selectionErr != nil {
			return CommandResult{}, selectionErr
		}
		artifactIDs = selectedArtifactIDs
	}
	snapshot, retained, err := m.currentMutableMedia(ctx, ownerID, state, command.Media, artifactIDs, now)
	if err != nil {
		return CommandResult{}, err
	}
	if strings.TrimSpace(command.Host) == "" && len(command.ArtifactIDs) > 0 {
		selected := make(map[api.PublicResourceID]struct{}, len(artifactIDs))
		for _, artifactID := range artifactIDs {
			selected[artifactID] = struct{}{}
		}
		for index := range snapshot.Artifacts {
			if snapshot.Artifacts[index].Kind != api.MediaArtifactScreenshot &&
				snapshot.Artifacts[index].Kind != api.MediaArtifactDVDMenu {
				continue
			}
			_, snapshot.Artifacts[index].Selected = selected[snapshot.Artifacts[index].ID]
		}
	}
	release, _, eligible, _, _, err := m.mediaExtensionContext(ctx, ownerID, state, &command.Media, now)
	if err != nil {
		return CommandResult{}, err
	}
	updated, nextRetained, attempts, err := mutator.UploadImages(
		ctx,
		release,
		eligible,
		snapshot,
		retained,
		artifactIDs,
		command.Host,
		command.Retry,
		now,
	)
	if err != nil {
		failure, ok := api.AsOperationFailure(err)
		if ok && failure.Code == api.OperationFailureUnknownOutcome {
			effectScopeID := workflowImageHostingEffectScope(command.Host, eligible)
			action, idErr := m.newReconcileAction(
				nextRevision,
				now,
				"",
				api.WorkflowExternalEffectImageHosting,
				effectScopeID,
				"Verify that the image-host upload did not complete before retrying.",
			)
			if idErr != nil {
				return CommandResult{}, idErr
			}
			snapshot.Status = api.StageStatusBlocked
			snapshot.ImageRequirementsPrepared = false
			snapshot.RequiredActions = append(snapshot.RequiredActions, action)
			snapshot.Failures = append(snapshot.Failures, api.WorkflowFailure{
				Failure:  failure,
				Resource: effectScopeID,
			})
			return m.publishMediaMutation(ownerID, state, nextRevision, now, snapshot, retained)
		}
		return CommandResult{}, fmt.Errorf("release workflow upload media images: %w", err)
	}
	updated.HostAttempts = append(updated.HostAttempts, attempts...)
	if strings.TrimSpace(command.Host) == "" {
		updated.ImageRequirementsPrepared = true
	}
	return m.publishMediaMutation(ownerID, state, nextRevision, now, updated, nextRetained)
}

func workflowImageHostingEffectScope(host string, projections api.TrackerReleaseProjectionSet) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host != "" {
		return host
	}
	trackerIDs := make([]string, 0, len(projections.Projections))
	for _, projection := range projections.Projections {
		trackerIDs = append(trackerIDs, string(projection.TrackerID))
	}
	slices.Sort(trackerIDs)
	return "required:" + strings.Join(trackerIDs, ",")
}

func selectedLocalMediaArtifactIDs(state *State, mediaRef api.MediaArtifactSetRef) ([]api.PublicResourceID, error) {
	if state == nil || state.Workflow.Media == nil || *state.Workflow.Media != mediaRef {
		return nil, fmt.Errorf("%w: media revision is stale", ErrInvalidTransition)
	}
	snapshot, ok := state.Media[mediaRef.ID]
	if !ok || snapshot.Revision != mediaRef.Revision {
		return nil, fmt.Errorf("%w: media revision is unavailable", ErrInvalidTransition)
	}
	artifactIDs := make([]api.PublicResourceID, 0, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		if !artifact.Selected || (artifact.Kind != api.MediaArtifactScreenshot && artifact.Kind != api.MediaArtifactDVDMenu) {
			continue
		}
		artifactIDs = append(artifactIDs, artifact.ID)
	}
	if len(artifactIDs) == 0 {
		return nil, fmt.Errorf("%w: select at least one local media artifact", ErrInvalidTransition)
	}
	return artifactIDs, nil
}

func (m *Module) removeHostedImages(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command RemoveHostedImagesCommand,
) (CommandResult, error) {
	mutator, ok := m.mediaBuilder.(MediaArtifactMutator)
	if !ok {
		return CommandResult{}, fmt.Errorf("%w: workflow hosted-image removal is unavailable", ErrInvalidTransition)
	}
	snapshot, retained, err := m.currentMutableMedia(ctx, ownerID, state, command.Media, command.ArtifactIDs, now)
	if err != nil {
		return CommandResult{}, err
	}
	release, _, _, _, _, err := m.mediaExtensionContext(ctx, ownerID, state, &command.Media, now)
	if err != nil {
		return CommandResult{}, err
	}
	updated, nextRetained, err := mutator.RemoveHostedImages(ctx, release, snapshot, retained, command.ArtifactIDs, now)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow remove hosted images: %w", err)
	}
	updated.ImageRequirementsPrepared = false
	return m.publishMediaMutation(ownerID, state, nextRevision, now, updated, nextRetained)
}

func (m *Module) mediaExtensionContext(
	ctx context.Context,
	ownerID string,
	state *State,
	mediaRef *api.MediaArtifactSetRef,
	now time.Time,
) (
	api.ReleaseRef,
	api.TrackerReleaseProjectionSet,
	api.TrackerReleaseProjectionSet,
	*api.MediaArtifactSet,
	any,
	error,
) {
	if err := ctx.Err(); err != nil {
		return api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, api.TrackerReleaseProjectionSet{}, nil, nil,
			fmt.Errorf("release workflow media extension: %w", err)
	}
	workflow := state.Workflow
	if workflow.Release == nil || workflow.TrackerProjections == nil || workflow.Dupes == nil {
		return api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, api.TrackerReleaseProjectionSet{}, nil, nil,
			fmt.Errorf("%w: media dependencies are incomplete", ErrInvalidTransition)
	}
	releaseSnapshot, releaseOK := state.Releases[workflow.Release.ID]
	projections, projectionsOK := state.Projections[workflow.TrackerProjections.ID]
	dupes, dupesOK := state.Dupes[workflow.Dupes.ID]
	if !releaseOK || !projectionsOK || !dupesOK || releaseSnapshot.Revision != workflow.Release.Revision ||
		projections.Revision != workflow.TrackerProjections.Revision || dupes.Revision != workflow.Dupes.Revision ||
		dupes.ProjectionSet != *workflow.TrackerProjections || !dupes.ExpiresAt.After(now) || !dupesAllowContinuation(&projections, &dupes) {
		return api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, api.TrackerReleaseProjectionSet{}, nil, nil,
			fmt.Errorf("%w: media dependencies are stale or unresolved", ErrInvalidTransition)
	}
	var snapshot *api.MediaArtifactSet
	var retained any
	switch {
	case workflow.Media == nil && mediaRef != nil:
		return api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, api.TrackerReleaseProjectionSet{}, nil, nil,
			fmt.Errorf("%w: media revision is stale", ErrInvalidTransition)
	case workflow.Media != nil:
		if mediaRef == nil || *workflow.Media != *mediaRef {
			return api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, api.TrackerReleaseProjectionSet{}, nil, nil,
				fmt.Errorf("%w: media revision is stale", ErrInvalidTransition)
		}
		media, mediaOK := state.Media[workflow.Media.ID]
		if !mediaOK || media.Revision != workflow.Media.Revision {
			return api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, api.TrackerReleaseProjectionSet{}, nil, nil,
				fmt.Errorf("%w: media revision is unavailable", ErrInvalidTransition)
		}
		value, loadErr := m.private.Get(ownerID, workflow.ID, mediaPrivateResourceID(media.ID), now)
		if loadErr != nil {
			return api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, api.TrackerReleaseProjectionSet{}, nil, nil,
				fmt.Errorf("release workflow load media extension resource: %w", loadErr)
		}
		snapshot = &media
		retained = value
	}
	release := api.ReleaseRef{
		SourcePath: releaseSnapshot.Release.Source.SourcePath,
		Generation: releaseSnapshot.Release.Generation,
	}
	targets, err := resolveDownstreamTrackerSet(state, nil, downstreamStageMedia, now)
	if err != nil {
		return api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, api.TrackerReleaseProjectionSet{}, nil, nil, err
	}
	return release, projections, targets.Projections(), snapshot, retained, nil
}

func validateMediaRequirementsFingerprint(snapshot api.MediaArtifactSet, projections api.TrackerReleaseProjectionSet) error {
	expected, err := mediaRequirementsFingerprint(projections.Projections)
	if err != nil {
		return err
	}
	if snapshot.RequirementsFingerprint != expected {
		return errors.New("release workflow media mutation: requirements fingerprint mismatch")
	}
	return nil
}

func (m *Module) deleteMediaArtifacts(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command DeleteMediaArtifactsCommand,
) (CommandResult, error) {
	snapshot, resource, err := m.currentMutableMedia(ctx, ownerID, state, command.Media, command.ArtifactIDs, now)
	if err != nil {
		return CommandResult{}, err
	}
	resource, err = resource.DeleteArtifacts(ctx, snapshot, command.ArtifactIDs)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow delete media artifacts: %w", err)
	}
	deleted := make(map[api.PublicResourceID]struct{}, len(command.ArtifactIDs))
	for _, artifactID := range command.ArtifactIDs {
		deleted[artifactID] = struct{}{}
	}
	artifacts := make([]api.MediaArtifact, 0, len(snapshot.Artifacts)-len(deleted))
	for _, artifact := range snapshot.Artifacts {
		if _, ok := deleted[artifact.ID]; !ok {
			artifacts = append(artifacts, artifact)
		}
	}
	snapshot.Artifacts = artifacts
	snapshot.ImageRequirementsPrepared = false
	return m.publishMediaMutation(ownerID, state, nextRevision, now, snapshot, resource)
}

func (m *Module) currentMutableMedia(
	ctx context.Context,
	ownerID string,
	state *State,
	mediaRef api.MediaArtifactSetRef,
	artifactIDs []api.PublicResourceID,
	now time.Time,
) (api.MediaArtifactSet, RetainedMediaResource, error) {
	if err := ctx.Err(); err != nil {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("release workflow mutate media: %w", err)
	}
	if state.Workflow.Media == nil || *state.Workflow.Media != mediaRef {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("%w: media revision is stale", ErrInvalidTransition)
	}
	snapshot, ok := state.Media[mediaRef.ID]
	if !ok || snapshot.Revision != mediaRef.Revision {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("%w: media revision is unavailable", ErrInvalidTransition)
	}
	if len(artifactIDs) == 0 {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("%w: at least one media artifact is required", ErrInvalidTransition)
	}
	known := make(map[api.PublicResourceID]struct{}, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		known[artifact.ID] = struct{}{}
	}
	requested := make(map[api.PublicResourceID]struct{}, len(artifactIDs))
	for _, artifactID := range artifactIDs {
		if artifactID == "" {
			return api.MediaArtifactSet{}, nil, fmt.Errorf("%w: media artifact id is required", ErrInvalidTransition)
		}
		if _, duplicate := requested[artifactID]; duplicate {
			return api.MediaArtifactSet{}, nil, fmt.Errorf("%w: duplicate media artifact id", ErrInvalidTransition)
		}
		if _, exists := known[artifactID]; !exists {
			return api.MediaArtifactSet{}, nil, fmt.Errorf("%w: media artifact is unavailable", ErrInvalidTransition)
		}
		requested[artifactID] = struct{}{}
	}
	value, err := m.private.Get(ownerID, state.Workflow.ID, mediaPrivateResourceID(mediaRef.ID), now)
	if err != nil {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("release workflow load media mutation resource: %w", err)
	}
	resource, ok := value.(RetainedMediaResource)
	if !ok {
		return api.MediaArtifactSet{}, nil, ErrPrivateResourceUnavailable
	}
	return snapshot, resource, nil
}

func (m *Module) publishMediaMutation(
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	snapshot api.MediaArtifactSet,
	resource RetainedMediaResource,
) (CommandResult, error) {
	if state.Workflow.TrackerProjections == nil || state.Workflow.Dupes == nil {
		return CommandResult{}, fmt.Errorf("%w: media dependencies are incomplete", ErrInvalidTransition)
	}
	projections, projectionsOK := state.Projections[state.Workflow.TrackerProjections.ID]
	dupes, dupesOK := state.Dupes[state.Workflow.Dupes.ID]
	if !projectionsOK || !dupesOK || projections.Revision != state.Workflow.TrackerProjections.Revision ||
		dupes.Revision != state.Workflow.Dupes.Revision {
		return CommandResult{}, fmt.Errorf("%w: media dependencies are stale", ErrInvalidTransition)
	}
	targets, err := resolveDownstreamTrackerSet(state, nil, downstreamStageMedia, now)
	if err != nil {
		return CommandResult{}, err
	}
	eligible := targets.Projections()
	refreshMutatedMediaStatus(&snapshot, eligible.Projections)
	snapshot.TrackerApproval = targets.TrackerApproval()
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Prior                     api.WorkflowFingerprint
		Artifacts                 []api.MediaArtifact
		HostAttempts              []api.HostedImageAttempt
		FailedHosts               []string
		ImageRequirementsPrepared bool
	}{
		snapshot.CaptureFingerprint,
		snapshot.Artifacts,
		snapshot.HostAttempts,
		snapshot.FailedHosts,
		snapshot.ImageRequirementsPrepared,
	})
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow fingerprint media mutation: %w", err)
	}
	snapshot.CaptureFingerprint = fingerprint
	result, err := m.publishMedia(ownerID, state, nextRevision, now, mediaArtifactsPublication{Snapshot: snapshot})
	if err != nil {
		return CommandResult{}, err
	}
	if result.Media == nil {
		return CommandResult{}, errors.New("release workflow publish media mutation returned no media")
	}
	if err := m.private.Put(
		ownerID,
		state.Workflow.ID,
		mediaPrivateResourceID(result.Media.ID),
		resource,
		now.Add(24*time.Hour),
	); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow retain media mutation: %w", err)
	}
	return result, nil
}

func (m *Module) publishMediaReplacement(
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	snapshot api.MediaArtifactSet,
	resource RetainedMediaResource,
) (CommandResult, error) {
	targets, err := resolveDownstreamTrackerSet(state, nil, downstreamStageMedia, now)
	if err != nil {
		return CommandResult{}, err
	}
	snapshot.TrackerApproval = targets.TrackerApproval()
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Prior                     api.WorkflowFingerprint
		Artifacts                 []api.MediaArtifact
		HostAttempts              []api.HostedImageAttempt
		FailedHosts               []string
		ImageRequirementsPrepared bool
	}{
		snapshot.CaptureFingerprint,
		snapshot.Artifacts,
		snapshot.HostAttempts,
		snapshot.FailedHosts,
		snapshot.ImageRequirementsPrepared,
	})
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow fingerprint media replacement: %w", err)
	}
	snapshot.CaptureFingerprint = fingerprint
	result, err := m.publishMedia(ownerID, state, nextRevision, now, mediaArtifactsPublication{Snapshot: snapshot})
	if err != nil {
		return CommandResult{}, err
	}
	if result.Media == nil {
		return CommandResult{}, errors.New("release workflow publish media replacement returned no media")
	}
	if err := m.private.Put(
		ownerID,
		state.Workflow.ID,
		mediaPrivateResourceID(result.Media.ID),
		resource,
		now.Add(24*time.Hour),
	); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow retain media replacement: %w", err)
	}
	return result, nil
}

func refreshMutatedMediaStatus(snapshot *api.MediaArtifactSet, projections []api.TrackerReleaseProjection) {
	hostFailures := make([]api.WorkflowFailure, 0, len(snapshot.Failures))
	for _, failure := range snapshot.Failures {
		if failure.Failure.Operation == api.OperationKindImageHosting {
			hostFailures = append(hostFailures, failure)
		}
	}
	reconcileActions := slices.DeleteFunc(append([]api.RequiredAction(nil), snapshot.RequiredActions...), func(action api.RequiredAction) bool {
		return action.Kind != api.RequiredActionReconcileSubmission
	})
	requiredScreenshots, requiredMenus := 0, 0
	for _, projection := range projections {
		requiredScreenshots = max(requiredScreenshots, projection.Artifacts.ScreenshotCount)
		requiredMenus = max(requiredMenus, projection.Artifacts.DVDMenuCount)
	}
	selectedScreenshots, selectedMenus := 0, 0
	for _, artifact := range snapshot.Artifacts {
		if !artifact.Selected {
			continue
		}
		switch artifact.Kind {
		case api.MediaArtifactScreenshot:
			selectedScreenshots++
		case api.MediaArtifactDVDMenu:
			selectedMenus++
		case api.MediaArtifactHostedImage:
		}
	}
	snapshot.Failures = hostFailures
	snapshot.RequiredActions = reconcileActions
	if selectedScreenshots < requiredScreenshots || selectedMenus < requiredMenus {
		snapshot.Status = api.StageStatusBlocked
		snapshot.RequiredActions = append(snapshot.RequiredActions, api.RequiredAction{
			Kind:   api.RequiredActionProvideTrackerInput,
			Prompt: "Capture or select the required release images before continuing.",
		})
		return
	}
	if len(reconcileActions) > 0 {
		snapshot.Status = api.StageStatusBlocked
		return
	}
	if requiredScreenshots == 0 && requiredMenus == 0 && len(snapshot.Artifacts) == 0 {
		snapshot.Status = api.StageStatusSkipped
		return
	}
	snapshot.Status = api.StageStatusCompleted
}

func (m *Module) publishMedia(
	_ string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command mediaArtifactsPublication,
) (CommandResult, error) {
	workflow := state.Workflow
	if workflow.Release == nil || workflow.TrackerProjections == nil {
		return CommandResult{}, fmt.Errorf("%w: media dependencies are incomplete", ErrInvalidTransition)
	}
	id, err := m.newID("media")
	if err != nil {
		return CommandResult{}, err
	}
	snapshot := command.Snapshot
	snapshot.ID = api.MediaArtifactSetID(id)
	snapshot.WorkflowID = workflow.ID
	snapshot.Revision = nextRevision
	snapshot.Release = *workflow.Release
	release := state.Releases[workflow.Release.ID]
	snapshot.ReleaseRef = api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation}
	snapshot.ProjectionSet = *workflow.TrackerProjections
	snapshot.CreatedAt = now
	if err := m.stampMediaActions(&snapshot, nextRevision, now); err != nil {
		return CommandResult{}, err
	}
	if err := snapshot.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow publish media: %w", err)
	}
	state.Media[snapshot.ID] = snapshot
	state.Workflow.Media = &api.MediaArtifactSetRef{ID: snapshot.ID, Revision: snapshot.Revision}
	state.Workflow.Descriptions = nil
	invalidateUploadPlan(&state.Workflow)
	setWorkflowStageStatus(&state.Workflow, snapshot.Status, snapshot.RequiredActions, snapshot.Failures)
	return CommandResult{Media: &snapshot}, nil
}

func (m *Module) stampMediaActions(snapshot *api.MediaArtifactSet, revision api.WorkflowRevision, now time.Time) error {
	for index := range snapshot.RequiredActions {
		action := &snapshot.RequiredActions[index]
		if action.ID == "" {
			id, err := m.newID("action")
			if err != nil {
				return err
			}
			action.ID = api.RequiredActionID(id)
		}
		action.Status = api.RequiredActionStatusPending
		action.WorkflowRevision = revision
		action.CreatedAt = now
	}
	return nil
}

func (m *Module) generateDescriptions(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command GenerateDescriptionsCommand,
) (CommandResult, error) {
	if m.descriptionBuilder == nil {
		return CommandResult{}, fmt.Errorf("%w: description builder is unavailable", ErrInvalidTransition)
	}
	if err := command.Instructions.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("%w: %w", ErrInvalidTransition, err)
	}
	workflow := state.Workflow
	if workflow.Release == nil || workflow.TrackerProjections == nil || workflow.Dupes == nil || workflow.Media == nil {
		return CommandResult{}, fmt.Errorf("%w: description dependencies are incomplete", ErrInvalidTransition)
	}
	projections, projectionsOK := state.Projections[workflow.TrackerProjections.ID]
	dupes, dupesOK := state.Dupes[workflow.Dupes.ID]
	media, mediaOK := state.Media[workflow.Media.ID]
	if !projectionsOK || !dupesOK || !mediaOK || projections.Revision != workflow.TrackerProjections.Revision ||
		dupes.Revision != workflow.Dupes.Revision || dupes.ProjectionSet != *workflow.TrackerProjections ||
		media.Revision != workflow.Media.Revision || media.ProjectionSet != *workflow.TrackerProjections ||
		(media.Status != api.StageStatusCompleted && media.Status != api.StageStatusSkipped) {
		return CommandResult{}, fmt.Errorf("%w: description dependencies are stale or not ready", ErrInvalidTransition)
	}
	targets, err := resolveDownstreamTrackerSet(state, nil, downstreamStageDescriptions, now)
	if err != nil {
		return CommandResult{}, err
	}
	descriptionProjections := targets.Projections()
	privateMedia, err := m.private.Get(ownerID, workflow.ID, mediaPrivateResourceID(media.ID), now)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow load media artifacts for descriptions: %w", err)
	}
	inputFingerprint, templateFingerprint, err := m.descriptionBuilder.Fingerprints(
		ctx,
		projections.ReleaseRef,
		descriptionProjections,
		media,
		privateMedia,
		command.Instructions,
	)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow fingerprint descriptions: %w", err)
	}
	if workflow.Descriptions != nil {
		current, ok := state.Descriptions[workflow.Descriptions.ID]
		if ok && current.Revision == workflow.Descriptions.Revision && current.Release == *workflow.Release &&
			current.ProjectionSet == *workflow.TrackerProjections && current.Media != nil && *current.Media == *workflow.Media &&
			current.InputFingerprint == inputFingerprint && current.TemplateFingerprint == templateFingerprint {
			return CommandResult{Descriptions: &current}, nil
		}
	}
	snapshot, err := m.descriptionBuilder.Build(
		ctx,
		projections.ReleaseRef,
		descriptionProjections,
		media,
		privateMedia,
		command.Instructions,
		now,
	)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow build descriptions: %w", err)
	}
	if snapshot.InputFingerprint != inputFingerprint || snapshot.TemplateFingerprint != templateFingerprint {
		return CommandResult{}, errors.New("release workflow build descriptions: dependency fingerprint mismatch")
	}
	snapshot.TrackerApproval = targets.TrackerApproval()
	if err := validateDescriptionBuild(descriptionProjections, snapshot); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow build descriptions: %w", err)
	}
	result, err := m.publishDescriptions(ownerID, state, nextRevision, now, descriptionsPublication{Snapshot: snapshot})
	if err != nil {
		return CommandResult{}, err
	}
	if result.Descriptions != nil {
		if err := m.private.Put(
			ownerID,
			workflow.ID,
			descriptionPrivateResourceID(result.Descriptions.ID),
			cloneDescriptionInstructions(command.Instructions),
			now.Add(24*time.Hour),
		); err != nil {
			return CommandResult{}, fmt.Errorf("release workflow retain description inputs: %w", err)
		}
	}
	return result, nil
}

func cloneDescriptionInstructions(input api.DescriptionInstructions) api.DescriptionInstructions {
	cloned := input
	cloned.Overrides = append([]api.DescriptionOverrideInput(nil), input.Overrides...)
	cloned.QuestionnaireAnswers = make(map[api.TrackerID]map[string]string, len(input.QuestionnaireAnswers))
	for trackerID, answers := range input.QuestionnaireAnswers {
		clonedAnswers := make(map[string]string, len(answers))
		maps.Copy(clonedAnswers, answers)
		cloned.QuestionnaireAnswers[trackerID] = clonedAnswers
	}
	return cloned
}

func (m *Module) mutateDescriptionOverride(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	descriptionsRef api.DescriptionSetRef,
	groupKey string,
	source *string,
) (CommandResult, error) {
	if m.descriptionBuilder == nil {
		return CommandResult{}, fmt.Errorf("%w: description builder is unavailable", ErrInvalidTransition)
	}
	groupKey = strings.TrimSpace(groupKey)
	if groupKey == "" {
		return CommandResult{}, fmt.Errorf("%w: description group key is required", ErrInvalidTransition)
	}
	workflow := state.Workflow
	if workflow.Release == nil || workflow.TrackerProjections == nil || workflow.Dupes == nil || workflow.Media == nil ||
		workflow.Descriptions == nil || *workflow.Descriptions != descriptionsRef {
		return CommandResult{}, fmt.Errorf("%w: description revision is stale", ErrInvalidTransition)
	}
	current, currentOK := state.Descriptions[descriptionsRef.ID]
	projections, projectionsOK := state.Projections[workflow.TrackerProjections.ID]
	dupes, dupesOK := state.Dupes[workflow.Dupes.ID]
	media, mediaOK := state.Media[workflow.Media.ID]
	if !currentOK || !projectionsOK || !dupesOK || !mediaOK || current.Revision != descriptionsRef.Revision ||
		projections.Revision != workflow.TrackerProjections.Revision || dupes.Revision != workflow.Dupes.Revision ||
		media.Revision != workflow.Media.Revision || current.Media == nil || *current.Media != *workflow.Media {
		return CommandResult{}, fmt.Errorf("%w: description dependencies are stale", ErrInvalidTransition)
	}
	targetIndex := -1
	for index := range current.Descriptions {
		if strings.EqualFold(strings.TrimSpace(current.Descriptions[index].GroupKey), groupKey) {
			targetIndex = index
			groupKey = current.Descriptions[index].GroupKey
			break
		}
	}
	if targetIndex < 0 {
		return CommandResult{}, fmt.Errorf("%w: description group is unavailable", ErrInvalidTransition)
	}
	privateInputs, err := m.private.Get(ownerID, workflow.ID, descriptionPrivateResourceID(current.ID), now)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow load description inputs: %w", err)
	}
	instructions, ok := privateInputs.(api.DescriptionInstructions)
	if !ok {
		return CommandResult{}, ErrPrivateResourceUnavailable
	}
	instructions = cloneDescriptionInstructions(instructions)
	matchedOverride := false
	overrides := make([]api.DescriptionOverrideInput, 0, len(instructions.Overrides)+1)
	for _, override := range instructions.Overrides {
		if strings.EqualFold(strings.TrimSpace(override.GroupKey), groupKey) {
			matchedOverride = true
			if source != nil {
				overrides = append(overrides, api.DescriptionOverrideInput{GroupKey: groupKey, Source: *source})
			}
			continue
		}
		overrides = append(overrides, override)
	}
	if source != nil && !matchedOverride {
		overrides = append(overrides, api.DescriptionOverrideInput{GroupKey: groupKey, Source: *source})
	}
	instructions.Overrides = overrides
	if err := instructions.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow validate description override: %w", err)
	}

	privateMedia, err := m.private.Get(ownerID, workflow.ID, mediaPrivateResourceID(media.ID), now)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow load media artifacts for description override: %w", err)
	}
	targets, err := resolveDownstreamTrackerSet(state, nil, downstreamStageDescriptions, now)
	if err != nil {
		return CommandResult{}, err
	}
	descriptionProjections := targets.Projections()
	rebuilt, err := m.descriptionBuilder.Build(
		ctx,
		projections.ReleaseRef,
		descriptionProjections,
		media,
		privateMedia,
		instructions,
		now,
	)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow rebuild description group: %w", err)
	}
	var rebuiltTarget *api.RenderedDescription
	for index := range rebuilt.Descriptions {
		if strings.EqualFold(strings.TrimSpace(rebuilt.Descriptions[index].GroupKey), groupKey) {
			rebuiltTarget = &rebuilt.Descriptions[index]
			break
		}
	}
	if rebuiltTarget == nil {
		return CommandResult{}, fmt.Errorf("release workflow rebuild description group: %w", ErrPrivateResourceUnavailable)
	}

	updated := current
	updated.Descriptions = append([]api.RenderedDescription(nil), current.Descriptions...)
	updated.Descriptions[targetIndex] = *rebuiltTarget
	updated.InputFingerprint = rebuilt.InputFingerprint
	updated.TemplateFingerprint = rebuilt.TemplateFingerprint
	updated.TrackerApproval = targets.TrackerApproval()
	targetTrackers := make(map[api.TrackerID]struct{}, len(rebuiltTarget.TrackerIDs))
	for _, trackerID := range rebuiltTarget.TrackerIDs {
		targetTrackers[trackerID] = struct{}{}
	}
	updated.TrackerResults = make([]api.DescriptionTrackerResult, 0, len(current.TrackerResults))
	for _, result := range current.TrackerResults {
		if _, target := targetTrackers[result.TrackerID]; !target {
			updated.TrackerResults = append(updated.TrackerResults, result)
		}
	}
	for _, result := range rebuilt.TrackerResults {
		if _, target := targetTrackers[result.TrackerID]; target {
			updated.TrackerResults = append(updated.TrackerResults, result)
		}
	}
	updated.Failures = make([]api.WorkflowFailure, 0, len(current.Failures)+len(rebuilt.Failures))
	for _, failure := range current.Failures {
		if _, target := targetTrackers[failure.TrackerID]; !target {
			updated.Failures = append(updated.Failures, failure)
		}
	}
	for _, failure := range rebuilt.Failures {
		if _, target := targetTrackers[failure.TrackerID]; target {
			updated.Failures = append(updated.Failures, failure)
		}
	}
	updated.RequiredActions = nil
	switch {
	case len(updated.Descriptions) == 0 && len(updated.Failures) > 0:
		updated.Status = api.StageStatusFailed
	case len(updated.Descriptions) == 0:
		updated.Status = api.StageStatusSkipped
	case len(updated.Failures) > 0:
		updated.Status = api.StageStatusBlocked
	default:
		updated.Status = api.StageStatusCompleted
	}
	if err := validateDescriptionBuild(descriptionProjections, updated); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow validate description group mutation: %w", err)
	}
	result, err := m.publishDescriptions(ownerID, state, nextRevision, now, descriptionsPublication{Snapshot: updated})
	if err != nil {
		return CommandResult{}, err
	}
	if result.Descriptions != nil {
		if err := m.private.Put(
			ownerID,
			workflow.ID,
			descriptionPrivateResourceID(result.Descriptions.ID),
			instructions,
			now.Add(24*time.Hour),
		); err != nil {
			return CommandResult{}, fmt.Errorf("release workflow retain mutated description inputs: %w", err)
		}
	}
	return result, nil
}

func validateDescriptionBuild(projections api.TrackerReleaseProjectionSet, snapshot api.DescriptionSet) error {
	projected := make(map[api.TrackerID]api.TrackerReleaseProjection, len(projections.Projections))
	covered := make(map[api.TrackerID]struct{}, len(projections.Projections))
	failed := make(map[api.TrackerID]struct{}, len(snapshot.Failures))
	for _, projection := range projections.Projections {
		projected[projection.TrackerID] = projection
	}
	for _, failure := range snapshot.Failures {
		if failure.TrackerID != "" {
			failed[failure.TrackerID] = struct{}{}
		}
	}
	for _, rendered := range snapshot.Descriptions {
		if len(rendered.TrackerIDs) == 0 {
			return fmt.Errorf("description group %s has no projected trackers", rendered.GroupKey)
		}
		baseGroup := strings.TrimSpace(strings.SplitN(rendered.GroupKey, "|", 2)[0])
		for _, trackerID := range rendered.TrackerIDs {
			projection, ok := projected[trackerID]
			if !ok {
				return fmt.Errorf("description group %s references unprojected tracker %s", rendered.GroupKey, trackerID)
			}
			if !projection.Artifacts.Description {
				return fmt.Errorf("description group %s references tracker %s that does not consume descriptions", rendered.GroupKey, trackerID)
			}
			if projection.DescriptionGroup != "" && !strings.EqualFold(strings.TrimSpace(projection.DescriptionGroup), baseGroup) {
				return fmt.Errorf(
					"description group %s mismatches projected group %s for tracker %s",
					rendered.GroupKey,
					projection.DescriptionGroup,
					trackerID,
				)
			}
			if _, duplicate := covered[trackerID]; duplicate {
				return fmt.Errorf("tracker %s appears in more than one description group", trackerID)
			}
			covered[trackerID] = struct{}{}
		}
	}
	if len(snapshot.TrackerResults) > 0 {
		results := make(map[api.TrackerID]api.DescriptionTrackerResult, len(snapshot.TrackerResults))
		for _, result := range snapshot.TrackerResults {
			projection, ok := projected[result.TrackerID]
			if !ok || !projection.Artifacts.Description {
				return fmt.Errorf("description result references unprojected tracker %s", result.TrackerID)
			}
			if _, duplicate := results[result.TrackerID]; duplicate {
				return fmt.Errorf("tracker %s has more than one description result", result.TrackerID)
			}
			results[result.TrackerID] = result
		}
		for _, projection := range projections.Projections {
			if !projection.Artifacts.Description {
				continue
			}
			result, ok := results[projection.TrackerID]
			if !ok {
				return fmt.Errorf("descriptions omit tracker result %s", projection.TrackerID)
			}
			_, hasDescription := covered[projection.TrackerID]
			_, hasFailure := failed[projection.TrackerID]
			switch result.Status {
			case api.StageStatusCompleted:
				if !hasDescription || hasFailure {
					return fmt.Errorf("completed description result is inconsistent for tracker %s", projection.TrackerID)
				}
			case api.StageStatusSkipped:
				if hasDescription || hasFailure {
					return fmt.Errorf("skipped description result is inconsistent for tracker %s", projection.TrackerID)
				}
			case api.StageStatusFailed:
				if hasDescription || !hasFailure {
					return fmt.Errorf("failed description result is inconsistent for tracker %s", projection.TrackerID)
				}
			case api.StageStatusPending, api.StageStatusQueued, api.StageStatusReady, api.StageStatusBlocked, api.StageStatusStale,
				api.StageStatusPartial, api.StageStatusRunning, api.StageStatusExecuted, api.StageStatusInterrupted, api.StageStatusCanceled,
				api.StageStatusUnavailable, "":
				return fmt.Errorf("tracker %s has invalid description result status %q", projection.TrackerID, result.Status)
			default:
				return fmt.Errorf("tracker %s has invalid description result status %q", projection.TrackerID, result.Status)
			}
		}
		return nil
	}
	if snapshot.Status == api.StageStatusCompleted {
		for _, projection := range projections.Projections {
			if !projection.Artifacts.Description {
				continue
			}
			if _, ok := covered[projection.TrackerID]; !ok {
				if _, failedOK := failed[projection.TrackerID]; !failedOK {
					return fmt.Errorf("completed descriptions omit tracker %s", projection.TrackerID)
				}
			}
		}
	}
	return nil
}

func (m *Module) publishDescriptions(
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command descriptionsPublication,
) (CommandResult, error) {
	workflow := state.Workflow
	priorDescriptions := workflow.Descriptions
	if workflow.Release == nil || workflow.TrackerProjections == nil {
		return CommandResult{}, fmt.Errorf("%w: description dependencies are incomplete", ErrInvalidTransition)
	}
	id, err := m.newID("descriptions")
	if err != nil {
		return CommandResult{}, err
	}
	snapshot := command.Snapshot
	snapshot.ID = api.DescriptionSetID(id)
	snapshot.WorkflowID = workflow.ID
	snapshot.Revision = nextRevision
	snapshot.Release = *workflow.Release
	release := state.Releases[workflow.Release.ID]
	snapshot.ReleaseRef = api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation}
	snapshot.ProjectionSet = *workflow.TrackerProjections
	snapshot.Media = workflow.Media
	snapshot.CreatedAt = now
	if err := snapshot.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow publish descriptions: %w", err)
	}
	state.Descriptions[snapshot.ID] = snapshot
	state.Workflow.Descriptions = &api.DescriptionSetRef{ID: snapshot.ID, Revision: snapshot.Revision}
	invalidateUploadPlan(&state.Workflow)
	if priorDescriptions != nil {
		m.private.Delete(ownerID, state.Workflow.ID, descriptionPrivateResourceID(priorDescriptions.ID))
	}
	setWorkflowStageStatus(&state.Workflow, snapshot.Status, snapshot.RequiredActions, snapshot.Failures)
	return CommandResult{Descriptions: &snapshot}, nil
}

type preparedUploads struct {
	projections  api.TrackerReleaseProjectionSet
	dupes        api.DupeAssessment
	media        api.MediaArtifactSet
	descriptions api.DescriptionSet
	trackerIDs   []api.TrackerID
	plan         api.UploadPlan
	execution    RetainedUploadExecution
	dryRun       bool
}

func (p *preparedUploads) Release() error {
	if p == nil || p.execution == nil {
		return nil
	}
	err := p.execution.Release()
	p.execution = nil
	if err != nil {
		return fmt.Errorf("release retained upload execution: %w", err)
	}
	return nil
}

func (m *Module) prepareUploads(
	ctx context.Context,
	ownerID string,
	state *State,
	options UploadPlanBuildOptions,
	now time.Time,
) (preparedUploads, error) {
	if m.uploadPlanBuilder == nil {
		return preparedUploads{}, fmt.Errorf("%w: upload execution builder is unavailable", ErrInvalidTransition)
	}
	workflow := state.Workflow
	if workflow.TrackerProjections == nil || workflow.Dupes == nil || workflow.Media == nil || workflow.Descriptions == nil {
		return preparedUploads{}, fmt.Errorf("%w: upload dependencies are incomplete", ErrInvalidTransition)
	}
	projections, projectionsOK := state.Projections[workflow.TrackerProjections.ID]
	dupes, dupesOK := state.Dupes[workflow.Dupes.ID]
	media, mediaOK := state.Media[workflow.Media.ID]
	descriptions, descriptionsOK := state.Descriptions[workflow.Descriptions.ID]
	if !projectionsOK || !dupesOK || !mediaOK || !descriptionsOK ||
		projections.Revision != workflow.TrackerProjections.Revision || dupes.Revision != workflow.Dupes.Revision ||
		media.Revision != workflow.Media.Revision || descriptions.Revision != workflow.Descriptions.Revision ||
		dupes.ProjectionSet != *workflow.TrackerProjections || media.ProjectionSet != *workflow.TrackerProjections ||
		descriptions.ProjectionSet != *workflow.TrackerProjections || descriptions.Media == nil || *descriptions.Media != *workflow.Media ||
		projections.Status != api.StageStatusReady || !dupesAllowContinuation(&projections, &dupes) || !dupes.ExpiresAt.After(now) ||
		!stageSucceeded(media.Status) ||
		!descriptionsHaveViableTracker(&descriptions) {
		return preparedUploads{}, fmt.Errorf("%w: upload dependencies are stale or not ready", ErrInvalidTransition)
	}
	targets, err := resolveDownstreamTrackerSet(state, options.TrackerIDs, downstreamStageUpload, now)
	if err != nil {
		return preparedUploads{}, err
	}
	if !trackerApprovalRefsEqual(media.TrackerApproval, targets.TrackerApproval()) ||
		!trackerApprovalRefsEqual(descriptions.TrackerApproval, targets.TrackerApproval()) {
		return preparedUploads{}, fmt.Errorf("%w: upload tracker authority lineage is stale", ErrInvalidTransition)
	}
	projections = targets.Projections()
	options.TrackerIDs = targets.TrackerIDs()
	options.TrackerApproval = targets.TrackerApproval()
	options.AuthorityFingerprint = targets.authority
	inputFingerprint, err := m.uploadPlanBuilder.Fingerprint(ctx, projections, dupes, media, descriptions, options)
	if err != nil {
		return preparedUploads{}, fmt.Errorf("release workflow fingerprint upload execution: %w", err)
	}
	privateDupes, err := m.private.Get(ownerID, workflow.ID, dupePrivateResourceID(dupes.ID), now)
	if err != nil {
		return preparedUploads{}, fmt.Errorf("release workflow load duplicate evidence for upload: %w", err)
	}
	privateMedia, err := m.private.Get(ownerID, workflow.ID, mediaPrivateResourceID(media.ID), now)
	if err != nil {
		return preparedUploads{}, fmt.Errorf("release workflow load media for upload: %w", err)
	}
	privateDescriptions, err := m.private.Get(ownerID, workflow.ID, descriptionPrivateResourceID(descriptions.ID), now)
	if err != nil {
		return preparedUploads{}, fmt.Errorf("release workflow load description inputs for upload: %w", err)
	}
	plan, execution, err := m.uploadPlanBuilder.Build(
		ctx,
		projections,
		dupes,
		privateDupes,
		media,
		privateMedia,
		descriptions,
		privateDescriptions,
		options,
		now,
	)
	if err != nil {
		return preparedUploads{}, fmt.Errorf("release workflow prepare uploads: %w", err)
	}
	if execution == nil {
		return preparedUploads{}, errors.New("release workflow prepare uploads: retained execution is required")
	}
	plan.TrackerApproval = options.TrackerApproval
	if err := validateUploadPlanBuild(projections, inputFingerprint, plan); err != nil {
		_ = execution.Release()
		return preparedUploads{}, fmt.Errorf("release workflow prepare uploads: %w", err)
	}
	return preparedUploads{
		projections:  projections,
		dupes:        dupes,
		media:        media,
		descriptions: descriptions,
		trackerIDs:   targets.TrackerIDs(),
		plan:         plan,
		execution:    execution,
		dryRun:       options.DryRun,
	}, nil
}

func trackerApprovalRefsEqual(left, right *api.TrackerApprovalSnapshotRef) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func validateUploadPlanBuild(
	projections api.TrackerReleaseProjectionSet,
	inputFingerprint api.WorkflowFingerprint,
	plan api.UploadPlan,
) error {
	if plan.InputFingerprint != inputFingerprint {
		return errors.New("input fingerprint mismatch")
	}
	if len(plan.Trackers) != len(projections.Projections) {
		return errors.New("upload plan must contain one tracker result per downstream projection")
	}
	projectionByTracker := make(map[api.TrackerID]api.TrackerReleaseProjection, len(projections.Projections))
	for _, projection := range projections.Projections {
		projectionByTracker[projection.TrackerID] = projection
	}
	seen := make(map[api.TrackerID]struct{}, len(plan.Trackers))
	for index, tracker := range plan.Trackers {
		projection, ok := projectionByTracker[tracker.TrackerID]
		if !ok {
			return fmt.Errorf("upload plan includes unselected tracker %s", tracker.TrackerID)
		}
		if _, ok := seen[tracker.TrackerID]; ok {
			return fmt.Errorf("upload plan contains duplicate tracker %s", tracker.TrackerID)
		}
		if tracker.TrackerID != projections.Projections[index].TrackerID {
			return errors.New("upload plan tracker order does not match downstream authority")
		}
		seen[tracker.TrackerID] = struct{}{}
		if tracker.UploadReleaseName != projection.UploadReleaseName || tracker.Taxonomy != projection.Taxonomy ||
			tracker.DescriptionGroup != projection.DescriptionGroup {
			return fmt.Errorf("upload plan semantic projection mismatch for tracker %s", tracker.TrackerID)
		}
	}
	return nil
}

func (m *Module) dryRunUploads(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command DryRunUploadsCommand,
) (CommandResult, error) {
	prepared, err := m.prepareUploads(
		ctx,
		ownerID,
		state,
		UploadPlanBuildOptions{
			DryRun:     true,
			NoSeed:     command.NoSeed,
			TrackerIDs: command.TrackerIDs,
		},
		now,
	)
	if err != nil {
		return CommandResult{}, err
	}
	retained := false
	defer func() {
		if !retained {
			_ = prepared.Release()
		}
	}()
	reports := uploadDryRunReports(prepared.plan.Trackers)
	succeededCount, failedCount, skippedCount, status := reduceUploadDryRunReports(reports)
	id, err := m.newID("upload_dry_run")
	if err != nil {
		return CommandResult{}, err
	}
	snapshot := api.UploadDryRunResult{
		ID:               api.UploadDryRunResultID(id),
		WorkflowID:       state.Workflow.ID,
		Revision:         nextRevision,
		ProjectionSet:    api.TrackerReleaseProjectionSetRef{ID: prepared.projections.ID, Revision: prepared.projections.Revision},
		Dupes:            api.DupeAssessmentRef{ID: prepared.dupes.ID, Revision: prepared.dupes.Revision},
		TrackerApproval:  prepared.plan.TrackerApproval,
		Media:            api.MediaArtifactSetRef{ID: prepared.media.ID, Revision: prepared.media.Revision},
		Descriptions:     api.DescriptionSetRef{ID: prepared.descriptions.ID, Revision: prepared.descriptions.Revision},
		InputFingerprint: prepared.plan.InputFingerprint,
		NoSeed:           command.NoSeed,
		TrackerIDs:       append([]api.TrackerID(nil), prepared.trackerIDs...),
		Reports:          reports,
		SucceededCount:   succeededCount,
		FailedCount:      failedCount,
		SkippedCount:     skippedCount,
		Status:           status,
		CreatedAt:        now,
	}
	if err := snapshot.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow publish upload dry run: %w", err)
	}
	reconcileActions, reconcileFailures, err := m.reconcileActionsForDryRun(snapshot, nextRevision, now)
	if err != nil {
		return CommandResult{}, err
	}
	if err := m.private.Put(
		ownerID,
		state.Workflow.ID,
		uploadPlanPrivateResourceID(snapshot.ID),
		&prepared,
		prepared.plan.ExpiresAt,
	); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow retain exact upload plan: %w", err)
	}
	if prior := state.Workflow.DryRun; prior != nil && prior.ID != snapshot.ID {
		m.private.Delete(ownerID, state.Workflow.ID, uploadPlanPrivateResourceID(prior.ID))
	}
	retained = true
	state.DryRuns[snapshot.ID] = snapshot
	state.Workflow.DryRun = &api.UploadDryRunResultRef{ID: snapshot.ID, Revision: snapshot.Revision}
	if state.Workflow.UploadResult == nil {
		setWorkflowStageStatus(&state.Workflow, snapshot.Status, reconcileActions, reconcileFailures)
		if len(reconcileActions) > 0 {
			state.Workflow.Status = api.WorkflowStatusBlocked
		}
	}
	return CommandResult{DryRun: &snapshot}, nil
}

func (m *Module) reconcileActionsForDryRun(
	snapshot api.UploadDryRunResult,
	revision api.WorkflowRevision,
	now time.Time,
) ([]api.RequiredAction, []api.WorkflowFailure, error) {
	var actions []api.RequiredAction
	var failures []api.WorkflowFailure
	for _, report := range snapshot.Reports {
		for _, failure := range report.Failures {
			if failure.Failure.Code != api.OperationFailureUnknownOutcome {
				continue
			}
			action, err := m.newReconcileAction(
				revision,
				now,
				report.TrackerID,
				api.WorkflowExternalEffectClientInjection,
				failure.Resource,
				"Verify that the client injection did not complete before preparing and reviewing a replacement exact upload plan.",
			)
			if err != nil {
				return nil, nil, err
			}
			actions = append(actions, action)
			failures = append(failures, failure)
		}
	}
	return actions, failures, nil
}

func uploadDryRunReports(trackers []api.UploadPlanTracker) []api.TrackerDryRunReport {
	reports := make([]api.TrackerDryRunReport, 0, len(trackers))
	for _, tracker := range trackers {
		status := api.StageStatusCompleted
		failures := append([]api.WorkflowFailure(nil), tracker.Failures...)
		if tracker.Status == api.StageStatusSkipped {
			status = api.StageStatusSkipped
		} else if tracker.Status != api.StageStatusReady || tracker.ClientInjectionStatus == api.StageStatusFailed {
			status = api.StageStatusFailed
			if len(failures) == 0 {
				message := uploadDryRunFailureMessage(tracker)
				code := tracker.ClientFailureCode
				if code == "" {
					code = api.OperationFailureMissingPreparedTracker
				}
				operation := api.OperationKindUploadDryRun
				resource := ""
				if code == api.OperationFailureUnknownOutcome {
					operation = api.OperationKindClientInjection
					resource = "dry-run:" + string(tracker.TrackerID)
				}
				failures = append(failures, api.WorkflowFailure{
					Failure: api.OperationFailure{
						Code:      code,
						Operation: operation,
						Message:   message,
						Recovery:  api.OperationRecoveryRetry,
					},
					TrackerID: tracker.TrackerID,
					Resource:  resource,
				})
			}
		}
		reports = append(reports, api.TrackerDryRunReport{
			TrackerID:           tracker.TrackerID,
			DisplayName:         tracker.DisplayName,
			UploadReleaseName:   tracker.UploadReleaseName,
			Status:              status,
			Endpoint:            tracker.Endpoint,
			Fields:              append([]api.UploadPlanField(nil), tracker.Fields...),
			Files:               append([]api.UploadPlanFile(nil), tracker.Files...),
			PreparedOperationID: tracker.PreparedOperationID,
			TorrentArtifactID:   tracker.TorrentArtifactID,
			TorrentFingerprint:  tracker.TorrentFingerprint,
			SemanticFingerprint: tracker.SemanticFingerprint,
			ClientInjection: api.ClientInjectionOutcome{
				Status:  tracker.ClientInjectionStatus,
				Message: tracker.ClientInjectionMessage,
			},
			Warnings: append([]string(nil), tracker.Warnings...),
			Failures: failures,
		})
	}
	return reports
}

func reduceUploadDryRunReports(reports []api.TrackerDryRunReport) (int, int, int, api.StageStatus) {
	var succeeded, failed, skipped int
	for _, report := range reports {
		switch report.Status {
		case api.StageStatusCompleted:
			succeeded++
		case api.StageStatusFailed:
			failed++
		case api.StageStatusSkipped:
			skipped++
		case api.StageStatusPending, api.StageStatusQueued, api.StageStatusReady, api.StageStatusBlocked, api.StageStatusStale,
			api.StageStatusPartial, api.StageStatusRunning, api.StageStatusExecuted, api.StageStatusInterrupted, api.StageStatusCanceled,
			api.StageStatusUnavailable, "":
		}
	}
	switch {
	case succeeded > 0 && failed > 0:
		return succeeded, failed, skipped, api.StageStatusPartial
	case failed > 0:
		return succeeded, failed, skipped, api.StageStatusFailed
	case succeeded > 0:
		return succeeded, failed, skipped, api.StageStatusCompleted
	default:
		return succeeded, failed, skipped, api.StageStatusSkipped
	}
}

func uploadDryRunFailureMessage(tracker api.UploadPlanTracker) string {
	if tracker.Status != api.StageStatusReady {
		for _, warning := range tracker.Warnings {
			if message := strings.TrimSpace(logging.SanitizeMessage(warning)); message != "" {
				return message
			}
		}
	}
	if tracker.ClientInjectionStatus == api.StageStatusFailed {
		if message := strings.TrimSpace(logging.SanitizeMessage(tracker.ClientInjectionMessage)); message != "" {
			return message
		}
	}
	return "Tracker dry run did not complete. Review the retained report and retry."
}

func (m *Module) executeUploads(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command ExecuteUploadsCommand,
) (CommandResult, error) {
	if state.Workflow.UploadResult != nil {
		return CommandResult{}, fmt.Errorf("%w: upload already has a retained result; retry failed trackers or change workflow inputs", ErrInvalidTransition)
	}
	prepared, err := m.preparedUploadsForExecution(ctx, ownerID, state, command.NoSeed, command.TrackerIDs, now)
	if err != nil {
		return CommandResult{}, err
	}
	defer func() { _ = prepared.Release() }()
	requestedTrackerIDs := command.TrackerIDs
	if len(requestedTrackerIDs) == 0 {
		requestedTrackerIDs = prepared.trackerIDs
	}
	trackerIDs, err := validateUploadExecutionTrackerIDs(prepared.plan.Trackers, requestedTrackerIDs)
	if err != nil {
		return CommandResult{}, err
	}
	results, executionErr := prepared.execution.Execute(ctx, trackerIDs)
	results = completeUploadExecutionResults(prepared.plan.Trackers, results, executionErr, trackerIDs)
	authority := prepared.execution.RegisteredArtifactAuthority()
	return m.publishUploadResult(ownerID, state, nextRevision, now, prepared, results, authority)
}

func (m *Module) preparedUploadsForExecution(
	ctx context.Context,
	ownerID string,
	state *State,
	noSeed bool,
	trackerIDs []api.TrackerID,
	now time.Time,
) (preparedUploads, error) {
	if state.Workflow.DryRun == nil {
		return m.prepareUploads(ctx, ownerID, state, UploadPlanBuildOptions{NoSeed: noSeed, TrackerIDs: trackerIDs}, now)
	}
	value, err := m.private.Consume(
		ownerID,
		state.Workflow.ID,
		uploadPlanPrivateResourceID(state.Workflow.DryRun.ID),
		now,
	)
	if err != nil {
		return preparedUploads{}, api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureStaleReview,
			Operation: api.OperationKindUploadExecute,
			Message:   "The exact reviewed upload plan is no longer available. Prepare and review uploads again.",
			Recovery:  api.OperationRecoveryReviewAgain,
		}, err)
	}
	prepared, ok := value.(*preparedUploads)
	if !ok || prepared == nil {
		releasePrivateResource(value)
		return preparedUploads{}, errors.New("release workflow exact upload plan has an invalid private resource type")
	}
	if !prepared.dryRun || !prepared.plan.ExpiresAt.After(now) ||
		state.Workflow.TrackerProjections == nil || prepared.plan.ProjectionSet != *state.Workflow.TrackerProjections ||
		state.Workflow.Dupes == nil || prepared.plan.Dupes != *state.Workflow.Dupes ||
		!trackerApprovalRefsEqual(prepared.plan.TrackerApproval, state.Workflow.TrackerApproval) ||
		state.Workflow.Media == nil || prepared.plan.Media == nil || *prepared.plan.Media != *state.Workflow.Media ||
		state.Workflow.Descriptions == nil || prepared.plan.Descriptions == nil || *prepared.plan.Descriptions != *state.Workflow.Descriptions {
		_ = prepared.Release()
		return preparedUploads{}, api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureStaleReview,
			Operation: api.OperationKindUploadExecute,
			Message:   "Workflow inputs no longer match the exact reviewed upload plan. Prepare and review uploads again.",
			Recovery:  api.OperationRecoveryReviewAgain,
		}, ErrInvalidTransition)
	}
	result := *prepared
	return result, nil
}

func (m *Module) retryFailedUploads(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command RetryFailedUploadsCommand,
) (CommandResult, error) {
	prior, ok := state.UploadResults[command.Retry.Result.ID]
	if !ok || prior.Revision != command.Retry.Result.Revision || !uploadResultMatchesCurrent(prior, state.Workflow) {
		return CommandResult{}, fmt.Errorf("%w: prior upload result is stale", ErrInvalidTransition)
	}
	failed := make(map[api.TrackerID]struct{})
	for _, result := range prior.Results {
		if result.SubmissionRetryable() {
			failed[api.TrackerID(strings.ToUpper(strings.TrimSpace(string(result.TrackerID))))] = struct{}{}
		}
	}
	targets := make([]api.TrackerID, 0, len(command.Retry.TrackerIDs))
	for _, trackerID := range command.Retry.TrackerIDs {
		trackerID = api.TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
		if _, ok := failed[trackerID]; !ok {
			return CommandResult{}, fmt.Errorf("%w: tracker %s did not fail in the referenced upload result", ErrInvalidTransition, trackerID)
		}
		if !slices.Contains(targets, trackerID) {
			targets = append(targets, trackerID)
		}
	}
	if len(targets) == 0 {
		return CommandResult{}, fmt.Errorf("%w: failed upload retry targets are required", ErrInvalidTransition)
	}
	prepared, err := m.prepareUploads(ctx, ownerID, state, UploadPlanBuildOptions{NoSeed: command.NoSeed, TrackerIDs: targets}, now)
	if err != nil {
		return CommandResult{}, err
	}
	defer func() { _ = prepared.execution.Release() }()
	retried, executionErr := prepared.execution.Execute(ctx, prepared.trackerIDs)
	retried = completeUploadExecutionResults(prepared.plan.Trackers, retried, executionErr, prepared.trackerIDs)
	byTracker := make(map[api.TrackerID]api.UploadTrackerResult, len(retried))
	for _, result := range retried {
		byTracker[result.TrackerID] = result
	}
	merged := append([]api.UploadTrackerResult(nil), prior.Results...)
	for index, result := range merged {
		if replacement, exists := byTracker[result.TrackerID]; exists {
			merged[index] = replacement
			delete(byTracker, result.TrackerID)
		}
	}
	for _, trackerID := range targets {
		if result, exists := byTracker[trackerID]; exists {
			merged = append(merged, result)
		}
	}
	authority := prepared.execution.RegisteredArtifactAuthority()
	if priorAuthority, ok := m.registeredArtifactAuthority(ownerID, state.Workflow.ID, prior.ID, now); ok {
		authority = mergeRegisteredArtifactAuthorities(priorAuthority, authority)
	}
	return m.publishUploadResult(ownerID, state, nextRevision, now, prepared, merged, authority)
}

func (m *Module) retryClientInjections(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command RetryClientInjectionsCommand,
) (CommandResult, error) {
	prior, ok := state.UploadResults[command.Retry.Result.ID]
	if !ok || prior.Revision != command.Retry.Result.Revision || !uploadResultMatchesCurrent(prior, state.Workflow) {
		return CommandResult{}, fmt.Errorf("%w: prior upload result is stale", ErrInvalidTransition)
	}
	retryable := make(map[api.TrackerID]struct{})
	for _, result := range prior.Results {
		if result.ClientInjectionRetryable() {
			retryable[api.TrackerID(strings.ToUpper(strings.TrimSpace(string(result.TrackerID))))] = struct{}{}
		}
	}
	targets := make([]api.TrackerID, 0, len(command.Retry.TrackerIDs))
	for _, trackerID := range command.Retry.TrackerIDs {
		trackerID = api.TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
		if _, ok := retryable[trackerID]; !ok {
			return CommandResult{}, fmt.Errorf(
				"%w: tracker %s has no retryable client injection in the referenced upload result",
				ErrInvalidTransition,
				trackerID,
			)
		}
		if !slices.Contains(targets, trackerID) {
			targets = append(targets, trackerID)
		}
	}
	if len(targets) == 0 {
		return CommandResult{}, fmt.Errorf("%w: client injection retry targets are required", ErrInvalidTransition)
	}
	authority, ok := m.registeredArtifactAuthority(ownerID, state.Workflow.ID, prior.ID, now)
	if !ok {
		return CommandResult{}, api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureStaleResult,
			Operation: api.OperationKindClientInjection,
			Message:   "Registered tracker artifact authority is unavailable. The tracker upload will not be resubmitted.",
			Recovery:  api.OperationRecoveryNone,
		}, ErrPrivateResourceUnavailable)
	}
	retried, err := m.uploadPlanBuilder.RetryClientInjections(ctx, authority, targets)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow retry client injections: %w", err)
	}
	byTracker := make(map[api.TrackerID]api.UploadTrackerResult, len(retried))
	for _, result := range retried {
		byTracker[result.TrackerID] = result
	}
	merged := append([]api.UploadTrackerResult(nil), prior.Results...)
	for index, result := range merged {
		retryResult, exists := byTracker[result.TrackerID]
		if !exists {
			continue
		}
		result.ClientInjectionStatus = retryResult.ClientInjectionStatus
		result.ClientInjectionMessage = retryResult.ClientInjectionMessage
		result.ClientFailureCode = retryResult.ClientFailureCode
		result.ClientInjected = retryResult.ClientInjected
		result.Failures = slices.DeleteFunc(result.Failures, func(failure api.WorkflowFailure) bool {
			return failure.Failure.Operation == api.OperationKindClientInjection
		})
		result.Failures = append(result.Failures, retryResult.Failures...)
		result.Status = result.DerivedStatus()
		merged[index] = result
		delete(byTracker, result.TrackerID)
	}
	if len(byTracker) > 0 {
		return CommandResult{}, errors.New("release workflow client injection retry returned an unrelated tracker")
	}
	prepared, err := preparedUploadsForRetriedResult(state, prior)
	if err != nil {
		return CommandResult{}, err
	}
	return m.publishUploadResult(ownerID, state, nextRevision, now, prepared, merged, authority)
}

func (m *Module) registeredArtifactAuthority(
	ownerID string,
	workflowID api.WorkflowID,
	resultID api.UploadResultID,
	now time.Time,
) (RegisteredArtifactAuthority, bool) {
	value, err := m.private.Get(
		ownerID,
		workflowID,
		registeredArtifactAuthorityPrivateResourceID(resultID),
		now,
	)
	if err != nil {
		return RegisteredArtifactAuthority{}, false
	}
	authority, ok := value.(RegisteredArtifactAuthority)
	if !ok {
		return RegisteredArtifactAuthority{}, false
	}
	return cloneRegisteredArtifactAuthority(authority), true
}

func preparedUploadsForRetriedResult(state *State, result api.UploadResult) (preparedUploads, error) {
	projections, projectionsOK := state.Projections[result.ProjectionSet.ID]
	dupes, dupesOK := state.Dupes[result.Dupes.ID]
	media, mediaOK := state.Media[result.Media.ID]
	descriptions, descriptionsOK := state.Descriptions[result.Descriptions.ID]
	if !projectionsOK || !dupesOK || !mediaOK || !descriptionsOK {
		return preparedUploads{}, fmt.Errorf("%w: upload result lineage is unavailable", ErrInvalidTransition)
	}
	return preparedUploads{
		projections:  projections,
		dupes:        dupes,
		media:        media,
		descriptions: descriptions,
		plan: api.UploadPlan{
			TrackerApproval:  result.TrackerApproval,
			InputFingerprint: result.InputFingerprint,
		},
	}, nil
}

func uploadResultMatchesCurrent(result api.UploadResult, workflow api.ReleaseWorkflow) bool {
	return workflow.TrackerProjections != nil && result.ProjectionSet == *workflow.TrackerProjections &&
		workflow.Dupes != nil && result.Dupes == *workflow.Dupes &&
		trackerApprovalRefsEqual(result.TrackerApproval, workflow.TrackerApproval) &&
		workflow.Media != nil && result.Media == *workflow.Media &&
		workflow.Descriptions != nil && result.Descriptions == *workflow.Descriptions
}

func completeUploadExecutionResults(
	trackers []api.UploadPlanTracker,
	results []api.UploadTrackerResult,
	executionErr error,
	selectedTrackerIDs []api.TrackerID,
) []api.UploadTrackerResult {
	byTracker := make(map[api.TrackerID]api.UploadTrackerResult, len(results))
	for _, result := range results {
		byTracker[result.TrackerID] = result
	}
	selected := make(map[api.TrackerID]struct{}, len(selectedTrackerIDs))
	for _, trackerID := range selectedTrackerIDs {
		selected[trackerID] = struct{}{}
	}
	completed := make([]api.UploadTrackerResult, 0, len(trackers))
	for _, tracker := range trackers {
		if result, ok := byTracker[tracker.TrackerID]; ok {
			completed = append(completed, result)
			continue
		}
		if len(selected) > 0 && tracker.Eligible {
			if _, approved := selected[tracker.TrackerID]; !approved {
				completed = append(completed, api.UploadTrackerResult{
					TrackerID:             tracker.TrackerID,
					Status:                api.StageStatusSkipped,
					SubmissionStatus:      api.StageStatusSkipped,
					ClientInjectionStatus: api.StageStatusSkipped,
				})
				continue
			}
		}
		if !tracker.Eligible {
			switch tracker.Status {
			case api.StageStatusSkipped:
				completed = append(completed, api.UploadTrackerResult{
					TrackerID:             tracker.TrackerID,
					Status:                api.StageStatusSkipped,
					SubmissionStatus:      api.StageStatusSkipped,
					ClientInjectionStatus: api.StageStatusSkipped,
				})
			case api.StageStatusBlocked:
				completed = append(completed, api.UploadTrackerResult{
					TrackerID:             tracker.TrackerID,
					Status:                api.StageStatusFailed,
					SubmissionStatus:      api.StageStatusFailed,
					ClientInjectionStatus: api.StageStatusPending,
					Failures: []api.WorkflowFailure{{
						Failure: api.OperationFailure{
							Code:      api.OperationFailureInternal,
							Operation: api.OperationKindUploadExecute,
							Message:   "Tracker upload was not attempted because its operation could not be prepared.",
							Recovery:  api.OperationRecoveryRetry,
						},
						TrackerID: tracker.TrackerID,
					}},
				})
			case api.StageStatusPending, api.StageStatusQueued, api.StageStatusReady, api.StageStatusStale, api.StageStatusFailed,
				api.StageStatusPartial, api.StageStatusRunning, api.StageStatusCompleted, api.StageStatusExecuted, api.StageStatusInterrupted,
				api.StageStatusCanceled, api.StageStatusUnavailable, "":
			}
			continue
		}
		message := "Tracker upload did not return an outcome. Review the tracker result and retry."
		if executionErr != nil {
			message = "Tracker upload failed. Review the tracker result and retry."
		}
		completed = append(completed, api.UploadTrackerResult{
			TrackerID:             tracker.TrackerID,
			Status:                api.StageStatusFailed,
			SubmissionStatus:      api.StageStatusFailed,
			ClientInjectionStatus: api.StageStatusPending,
			Failures: []api.WorkflowFailure{{
				Failure: api.OperationFailure{
					Code:      api.OperationFailureInternal,
					Operation: api.OperationKindUploadExecute,
					Message:   message,
					Recovery:  api.OperationRecoveryRetry,
				},
				TrackerID: tracker.TrackerID,
			}},
		})
	}
	return completed
}

func validateUploadExecutionTrackerIDs(
	trackers []api.UploadPlanTracker,
	requested []api.TrackerID,
) ([]api.TrackerID, error) {
	if len(requested) == 0 {
		return nil, nil
	}
	eligible := make(map[api.TrackerID]struct{}, len(trackers))
	for _, tracker := range trackers {
		if tracker.Eligible && tracker.Status == api.StageStatusReady {
			eligible[tracker.TrackerID] = struct{}{}
		}
	}
	selected := make(map[api.TrackerID]struct{}, len(requested))
	for _, trackerID := range requested {
		trackerID = api.TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
		if _, exists := eligible[trackerID]; !exists {
			return nil, fmt.Errorf("%w: tracker %s is not eligible in the retained upload plan", ErrInvalidTransition, trackerID)
		}
		if _, duplicate := selected[trackerID]; duplicate {
			return nil, fmt.Errorf("%w: tracker %s appears more than once in the upload selection", ErrInvalidTransition, trackerID)
		}
		selected[trackerID] = struct{}{}
	}
	normalized := make([]api.TrackerID, 0, len(selected))
	for _, tracker := range trackers {
		if _, exists := selected[tracker.TrackerID]; exists {
			normalized = append(normalized, tracker.TrackerID)
		}
	}
	return normalized, nil
}

func (m *Module) publishUploadResult(
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	prepared preparedUploads,
	results []api.UploadTrackerResult,
	authority RegisteredArtifactAuthority,
) (CommandResult, error) {
	id, err := m.newID("upload_result")
	if err != nil {
		return CommandResult{}, err
	}
	status := derivedUploadResultStatus(results)
	snapshot := api.UploadResult{
		ID:               api.UploadResultID(id),
		WorkflowID:       state.Workflow.ID,
		Revision:         nextRevision,
		ProjectionSet:    api.TrackerReleaseProjectionSetRef{ID: prepared.projections.ID, Revision: prepared.projections.Revision},
		Dupes:            api.DupeAssessmentRef{ID: prepared.dupes.ID, Revision: prepared.dupes.Revision},
		TrackerApproval:  prepared.plan.TrackerApproval,
		Media:            api.MediaArtifactSetRef{ID: prepared.media.ID, Revision: prepared.media.Revision},
		Descriptions:     api.DescriptionSetRef{ID: prepared.descriptions.ID, Revision: prepared.descriptions.Revision},
		InputFingerprint: prepared.plan.InputFingerprint,
		Results:          results,
		Status:           status,
		CreatedAt:        now,
	}
	if err := snapshot.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow record upload result: %w", err)
	}
	if len(authority.Torrents) > 0 && slices.ContainsFunc(results, func(result api.UploadTrackerResult) bool {
		return result.EffectiveClientInjectionStatus() == api.StageStatusFailed
	}) {
		if err := m.private.Put(
			ownerID,
			state.Workflow.ID,
			registeredArtifactAuthorityPrivateResourceID(snapshot.ID),
			cloneRegisteredArtifactAuthority(authority),
			now.Add(workflowCommandTTL),
		); err != nil {
			m.logger.Warnf(
				"releaseworkflow: registered artifact authority retention failed workflow=%s decision=unavailable",
				state.Workflow.ID,
			)
		}
	}
	state.UploadResults[snapshot.ID] = snapshot
	state.Workflow.UploadResult = &api.UploadResultRef{ID: snapshot.ID, Revision: snapshot.Revision}
	state.Workflow.Status = api.WorkflowStatusCompleted
	state.Workflow.RequiredActions = nil
	state.Workflow.Failures = nil
	for _, result := range results {
		for _, failure := range result.Failures {
			if failure.Failure.Code != api.OperationFailureUnknownOutcome {
				continue
			}
			effectKind := api.WorkflowExternalEffectTrackerSubmission
			effectScopeID := string(result.TrackerID)
			prompt := "Verify that the tracker submission did not complete before preparing and reviewing a replacement exact upload plan."
			if failure.Failure.Operation == api.OperationKindClientInjection {
				effectKind = api.WorkflowExternalEffectClientInjection
				effectScopeID = failure.Resource
				prompt = "Verify that the client injection did not complete before retrying the retained registered tracker artifact."
			}
			action, actionErr := m.newReconcileAction(
				nextRevision,
				now,
				result.TrackerID,
				effectKind,
				effectScopeID,
				prompt,
			)
			if actionErr != nil {
				return CommandResult{}, actionErr
			}
			state.Workflow.RequiredActions = append(state.Workflow.RequiredActions, action)
			state.Workflow.Failures = append(state.Workflow.Failures, failure)
		}
	}
	if len(state.Workflow.RequiredActions) > 0 {
		state.Workflow.Status = api.WorkflowStatusBlocked
	}
	return CommandResult{UploadResult: &snapshot}, nil
}

func derivedUploadResultStatus(results []api.UploadTrackerResult) api.StageStatus {
	if slices.ContainsFunc(results, func(result api.UploadTrackerResult) bool {
		submission := result.EffectiveSubmissionStatus()
		return submission == api.StageStatusFailed || submission == api.StageStatusUnavailable
	}) {
		return api.StageStatusFailed
	}
	if slices.ContainsFunc(results, func(result api.UploadTrackerResult) bool {
		return result.EffectiveClientInjectionStatus() == api.StageStatusFailed
	}) {
		return api.StageStatusPartial
	}
	return api.StageStatusCompleted
}

func (m *Module) newReconcileAction(
	revision api.WorkflowRevision,
	now time.Time,
	trackerID api.TrackerID,
	effectKind api.WorkflowExternalEffectKind,
	effectScopeID string,
	prompt string,
) (api.RequiredAction, error) {
	effectScopeID = strings.TrimSpace(effectScopeID)
	if effectKind == "" || effectScopeID == "" {
		return api.RequiredAction{}, errors.New("release workflow reconciliation effect identity is unavailable")
	}
	actionID, err := m.newID("action")
	if err != nil {
		return api.RequiredAction{}, err
	}
	return api.RequiredAction{
		ID:               api.RequiredActionID(actionID),
		Kind:             api.RequiredActionReconcileSubmission,
		Status:           api.RequiredActionStatusPending,
		WorkflowRevision: revision,
		TrackerID:        trackerID,
		EffectKind:       effectKind,
		EffectScopeID:    effectScopeID,
		Prompt:           prompt,
		Options: []api.RequiredActionOption{{
			Value: api.RequiredActionReconcileNotCompleted,
			Label: "Confirmed not completed; allow a fresh exact attempt",
		}},
		CreatedAt: now,
	}, nil
}

func (m *Module) invalidateTrackers(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command InvalidateTrackersCommand,
) (CommandResult, error) {
	if state.Workflow.TrackerProjections == nil {
		return CommandResult{}, fmt.Errorf("%w: tracker projections are unavailable", ErrInvalidTransition)
	}
	invalid := make([]api.TrackerID, 0, len(command.TrackerIDs))
	for _, trackerID := range command.TrackerIDs {
		trackerID = api.TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
		if trackerID != "" && !slices.Contains(invalid, trackerID) {
			invalid = append(invalid, trackerID)
		}
	}
	if len(invalid) == 0 {
		return CommandResult{}, errors.New("release workflow tracker invalidation requires tracker ids")
	}
	reason := strings.TrimSpace(command.Reason)
	if reason == "" {
		reason = "tracker projection invalidated"
	}
	actionID, err := m.newID("action")
	if err != nil {
		return CommandResult{}, err
	}
	action := api.RequiredAction{
		ID:               api.RequiredActionID(actionID),
		Kind:             api.RequiredActionProvideTrackerInput,
		Status:           api.RequiredActionStatusPending,
		WorkflowRevision: nextRevision,
		Prompt:           reason,
		CreatedAt:        now,
	}
	projectionRef := *state.Workflow.TrackerProjections
	projections := state.Projections[projectionRef.ID]
	projections.Projections = slices.DeleteFunc(projections.Projections, func(projection api.TrackerReleaseProjection) bool {
		return slices.Contains(invalid, api.TrackerID(strings.ToUpper(strings.TrimSpace(string(projection.TrackerID)))))
	})
	var result CommandResult
	if len(projections.Projections) == 0 {
		state.Workflow.TrackerProjections = nil
		state.Workflow.TrackerPreflight = nil
		state.Workflow.Dupes = nil
	} else {
		projectionID, idErr := m.newID("projections")
		if idErr != nil {
			return CommandResult{}, idErr
		}
		projections.ID = api.TrackerReleaseProjectionSetID(projectionID)
		projections.Revision = nextRevision
		projections.Status = api.StageStatusStale
		projections.RequiredActions = append(projections.RequiredActions, action)
		projections.CreatedAt = now
		projections.Preflight = nil
		state.Projections[projections.ID] = projections
		state.Workflow.TrackerProjections = &api.TrackerReleaseProjectionSetRef{ID: projections.ID, Revision: projections.Revision}
		result.Projections = &projections

		if err := m.filterPreflight(state, invalid, nextRevision, now, state.Workflow.TrackerProjections); err != nil {
			return CommandResult{}, err
		}
		if err := m.filterDupes(state, invalid, nextRevision, now, state.Workflow.TrackerProjections, state.Workflow.TrackerPreflight); err != nil {
			return CommandResult{}, err
		}
	}
	state.Workflow.TrackerApproval = nil
	state.Workflow.Media = nil
	state.Workflow.Descriptions = nil
	invalidateUploadPlan(&state.Workflow)
	state.Workflow.Status = api.WorkflowStatusBlocked
	state.Workflow.RequiredActions = append(state.Workflow.RequiredActions, action)
	m.invalidateWorkflowPrivateResources(ctx, ownerID, state.Workflow.ID)
	return result, nil
}

func (m *Module) filterPreflight(
	state *State,
	invalid []api.TrackerID,
	nextRevision api.WorkflowRevision,
	now time.Time,
	projectionRef *api.TrackerReleaseProjectionSetRef,
) error {
	if state.Workflow.TrackerPreflight == nil {
		return nil
	}
	preflight := state.Preflights[state.Workflow.TrackerPreflight.ID]
	preflight.Results = slices.DeleteFunc(preflight.Results, func(result api.TrackerPreflightResult) bool {
		return slices.Contains(invalid, api.TrackerID(strings.ToUpper(strings.TrimSpace(string(result.TrackerID)))))
	})
	if len(preflight.Results) == 0 {
		state.Workflow.TrackerPreflight = nil
		return nil
	}
	id, err := m.newID("preflight")
	if err != nil {
		return fmt.Errorf("release workflow invalidate preflight: %w", err)
	}
	preflight.ID = api.TrackerPreflightAssessmentID(id)
	preflight.Revision = nextRevision
	preflight.ProjectionSet = *projectionRef
	preflight.Status = api.StageStatusStale
	preflight.CreatedAt = now
	preflight.ExpiresAt = now.Add(time.Nanosecond)
	state.Preflights[preflight.ID] = preflight
	state.Workflow.TrackerPreflight = &api.TrackerPreflightAssessmentRef{ID: preflight.ID, Revision: preflight.Revision}
	return nil
}

func (m *Module) filterDupes(
	state *State,
	invalid []api.TrackerID,
	nextRevision api.WorkflowRevision,
	now time.Time,
	projectionRef *api.TrackerReleaseProjectionSetRef,
	preflightRef *api.TrackerPreflightAssessmentRef,
) error {
	if state.Workflow.Dupes == nil {
		return nil
	}
	dupes := state.Dupes[state.Workflow.Dupes.ID]
	dupes.Results = slices.DeleteFunc(dupes.Results, func(result api.TrackerDupeAssessment) bool {
		return slices.Contains(invalid, api.TrackerID(strings.ToUpper(strings.TrimSpace(string(result.TrackerID)))))
	})
	if len(dupes.Results) == 0 {
		state.Workflow.Dupes = nil
		return nil
	}
	id, err := m.newID("dupes")
	if err != nil {
		return fmt.Errorf("release workflow invalidate dupes: %w", err)
	}
	dupes.ID = api.DupeAssessmentID(id)
	dupes.Revision = nextRevision
	dupes.ProjectionSet = *projectionRef
	dupes.Preflight = preflightRef
	dupes.Status = api.StageStatusStale
	dupes.CreatedAt = now
	dupes.ExpiresAt = now.Add(time.Nanosecond)
	state.Dupes[dupes.ID] = dupes
	state.Workflow.Dupes = &api.DupeAssessmentRef{ID: dupes.ID, Revision: dupes.Revision}
	return nil
}

func (m *Module) resolveAction(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command ResolveActionCommand,
) (CommandResult, error) {
	var resolvedUploadResult *api.UploadResult
	if command.Answer.WorkflowRevision != command.ExpectedRevision {
		return CommandResult{}, ErrRevisionConflict
	}
	index := slices.IndexFunc(state.Workflow.RequiredActions, func(action api.RequiredAction) bool {
		return action.ID == command.Answer.ActionID && action.Status == api.RequiredActionStatusPending
	})
	if index < 0 {
		return CommandResult{}, fmt.Errorf("%w: required action is stale or unknown", ErrInvalidTransition)
	}
	action := state.Workflow.RequiredActions[index]
	if action.Kind == api.RequiredActionReprepare {
		return CommandResult{}, fmt.Errorf("%w: reprepare action must be completed by preparing the release", ErrInvalidTransition)
	}
	if action.Kind == api.RequiredActionReconcileSubmission {
		if len(command.Answer.SelectedValues) != 1 ||
			command.Answer.SelectedValues[0] != api.RequiredActionReconcileNotCompleted ||
			command.Answer.TextValue != nil || command.Answer.Confirmed != nil {
			return CommandResult{}, fmt.Errorf(
				"%w: reconciliation requires exact confirmation that the external effect did not complete",
				ErrInvalidTransition,
			)
		}
		if action.EffectKind == "" || strings.TrimSpace(action.EffectScopeID) == "" {
			return CommandResult{}, fmt.Errorf("%w: reconciliation effect identity is unavailable", ErrInvalidTransition)
		}
		if err := m.durability.ResolveEffectUnknown(
			ctx,
			ownerID,
			state.Workflow.ID,
			action.EffectKind,
			action.EffectScopeID,
			now,
		); err != nil {
			return CommandResult{}, fmt.Errorf("release workflow reconcile external effect: %w", err)
		}
		if action.EffectKind == api.WorkflowExternalEffectImageHosting {
			return m.resolveImageHostingAction(ctx, ownerID, state, nextRevision, now, action)
		}
		if action.EffectKind == api.WorkflowExternalEffectClientInjection &&
			strings.HasPrefix(action.EffectScopeID, "upload:") {
			reconciled, err := m.reconcileClientInjectionResult(ownerID, state, nextRevision, now, action)
			if err != nil {
				return CommandResult{}, err
			}
			resolvedUploadResult = &reconciled
		} else {
			if state.Workflow.DryRun != nil {
				m.private.Delete(ownerID, state.Workflow.ID, uploadPlanPrivateResourceID(state.Workflow.DryRun.ID))
			}
			invalidateUploadPlan(&state.Workflow)
		}
		if err := m.invalidateUnavailablePrivateAuthority(ownerID, &state.Workflow, now); err != nil {
			return CommandResult{}, fmt.Errorf("release workflow reconcile private authority: %w", err)
		}
		state.Workflow.Failures = slices.DeleteFunc(state.Workflow.Failures, func(failure api.WorkflowFailure) bool {
			if failure.Failure.Code != api.OperationFailureUnknownOutcome {
				return false
			}
			if action.EffectKind == api.WorkflowExternalEffectClientInjection {
				return failure.Resource == action.EffectScopeID
			}
			return failure.TrackerID == action.TrackerID && failure.Failure.Operation == api.OperationKindUploadExecute
		})
	}
	state.Workflow.RequiredActions = slices.Delete(state.Workflow.RequiredActions, index, index+1)
	if len(state.Workflow.RequiredActions) == 0 && state.Workflow.Status == api.WorkflowStatusBlocked {
		if state.Workflow.UploadResult != nil {
			state.Workflow.Status = api.WorkflowStatusCompleted
		} else {
			state.Workflow.Status = api.WorkflowStatusActive
		}
	}
	return CommandResult{UploadResult: resolvedUploadResult}, nil
}

func (m *Module) reconcileClientInjectionResult(
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	action api.RequiredAction,
) (api.UploadResult, error) {
	if state.Workflow.UploadResult == nil {
		return api.UploadResult{}, fmt.Errorf("%w: reconciled upload result is unavailable", ErrInvalidTransition)
	}
	priorRef := *state.Workflow.UploadResult
	prior, ok := state.UploadResults[priorRef.ID]
	if !ok || prior.Revision != priorRef.Revision {
		return api.UploadResult{}, fmt.Errorf("%w: reconciled upload result is stale", ErrInvalidTransition)
	}
	id, err := m.newID("upload_result")
	if err != nil {
		return api.UploadResult{}, err
	}
	authority, authorityAvailable := m.registeredArtifactAuthority(ownerID, state.Workflow.ID, prior.ID, now)
	snapshot := prior
	snapshot.ID = api.UploadResultID(id)
	snapshot.Revision = nextRevision
	snapshot.CreatedAt = now
	snapshot.Results = append([]api.UploadTrackerResult(nil), prior.Results...)
	updated := false
	for index, result := range snapshot.Results {
		if result.TrackerID != action.TrackerID {
			continue
		}
		result.Failures = append([]api.WorkflowFailure(nil), result.Failures...)
		failureUpdated := false
		for failureIndex, failure := range result.Failures {
			if failure.Failure.Code != api.OperationFailureUnknownOutcome ||
				failure.Failure.Operation != api.OperationKindClientInjection ||
				failure.Resource != action.EffectScopeID {
				continue
			}
			failure.Failure.Code = api.OperationFailureClientInjection
			failure.Failure.Message = "Client injection was confirmed incomplete and can be retried without resubmitting."
			failure.Failure.Recovery = api.OperationRecoveryRetry
			if !authorityAvailable {
				failure.Failure.Code = api.OperationFailureMissingExactTorrent
				failure.Failure.Message = "Registered tracker artifact authority is unavailable; tracker submission remains completed."
				failure.Failure.Recovery = api.OperationRecoveryNone
			}
			result.Failures[failureIndex] = failure
			failureUpdated = true
		}
		if !failureUpdated {
			continue
		}
		result.ClientFailureCode = api.OperationFailureClientInjection
		result.ClientInjectionMessage = "Client injection was confirmed incomplete and can be retried without resubmitting."
		if !authorityAvailable {
			result.ClientFailureCode = api.OperationFailureMissingExactTorrent
			result.ClientInjectionMessage = "Registered tracker artifact authority is unavailable; tracker submission remains completed."
		}
		result.ClientInjectionStatus = api.StageStatusFailed
		result.Status = result.DerivedStatus()
		snapshot.Results[index] = result
		updated = true
	}
	if !updated {
		return api.UploadResult{}, fmt.Errorf("%w: reconciled client injection outcome is stale", ErrInvalidTransition)
	}
	snapshot.Status = derivedUploadResultStatus(snapshot.Results)
	if err := snapshot.Validate(); err != nil {
		return api.UploadResult{}, fmt.Errorf("release workflow reconcile client injection result: %w", err)
	}
	if authorityAvailable {
		if err := m.private.Put(
			ownerID,
			state.Workflow.ID,
			registeredArtifactAuthorityPrivateResourceID(snapshot.ID),
			authority,
			now.Add(workflowCommandTTL),
		); err != nil {
			return api.UploadResult{}, fmt.Errorf("release workflow retain reconciled client artifact authority: %w", err)
		}
	}
	state.UploadResults[snapshot.ID] = snapshot
	state.Workflow.UploadResult = &api.UploadResultRef{ID: snapshot.ID, Revision: snapshot.Revision}
	return snapshot, nil
}

func (m *Module) resolveImageHostingAction(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	action api.RequiredAction,
) (CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow reconcile image hosting: %w", err)
	}
	if state.Workflow.Media == nil {
		finishUnavailableImageHostingReconciliation(&state.Workflow, action)
		return CommandResult{}, nil
	}
	mediaRef := *state.Workflow.Media
	snapshot, ok := state.Media[mediaRef.ID]
	if !ok || snapshot.Revision != mediaRef.Revision {
		finishUnavailableImageHostingReconciliation(&state.Workflow, action)
		return CommandResult{}, nil
	}
	actionIndex := slices.IndexFunc(snapshot.RequiredActions, func(candidate api.RequiredAction) bool {
		return candidate.ID == action.ID && candidate.Kind == api.RequiredActionReconcileSubmission
	})
	if actionIndex < 0 {
		return CommandResult{}, fmt.Errorf("%w: reconciled media action is stale", ErrInvalidTransition)
	}
	value, err := m.private.Get(ownerID, state.Workflow.ID, mediaPrivateResourceID(mediaRef.ID), now)
	if err != nil {
		if errors.Is(err, ErrPrivateResourceUnavailable) || errors.Is(err, ErrPrivateResourceConsumed) {
			finishUnavailableImageHostingReconciliation(&state.Workflow, action)
			return CommandResult{}, nil
		}
		return CommandResult{}, fmt.Errorf("release workflow load reconciled media resource: %w", err)
	}
	resource, ok := value.(RetainedMediaResource)
	if !ok {
		finishUnavailableImageHostingReconciliation(&state.Workflow, action)
		return CommandResult{}, nil
	}
	snapshot.RequiredActions = slices.Delete(snapshot.RequiredActions, actionIndex, actionIndex+1)
	snapshot.Failures = slices.DeleteFunc(snapshot.Failures, func(failure api.WorkflowFailure) bool {
		return failure.Failure.Code == api.OperationFailureUnknownOutcome &&
			failure.Failure.Operation == api.OperationKindImageHosting &&
			failure.Resource == action.EffectScopeID
	})
	snapshot.ImageRequirementsPrepared = false
	return m.publishMediaMutation(ownerID, state, nextRevision, now, snapshot, resource)
}

func finishUnavailableImageHostingReconciliation(workflow *api.ReleaseWorkflow, action api.RequiredAction) {
	workflow.Media = nil
	workflow.Descriptions = nil
	invalidateUploadPlan(workflow)
	workflow.RequiredActions = slices.DeleteFunc(workflow.RequiredActions, func(candidate api.RequiredAction) bool {
		return candidate.ID == action.ID
	})
	workflow.Failures = slices.DeleteFunc(workflow.Failures, func(failure api.WorkflowFailure) bool {
		return failure.Failure.Code == api.OperationFailureUnknownOutcome &&
			failure.Failure.Operation == api.OperationKindImageHosting &&
			failure.Resource == action.EffectScopeID
	})
	if len(workflow.RequiredActions) == 0 && workflow.Status == api.WorkflowStatusBlocked {
		workflow.Status = api.WorkflowStatusActive
	}
}

func invalidatePreparedAndDownstream(workflow *api.ReleaseWorkflow) {
	workflow.Release = nil
	workflow.TrackerCatalog = nil
	workflow.TrackerRuntime = nil
	workflow.Selection = nil
	invalidateProjectionAndDownstream(workflow)
}

func invalidateTrackerAndDownstream(workflow *api.ReleaseWorkflow) {
	workflow.TrackerCatalog = nil
	workflow.TrackerRuntime = nil
	workflow.Selection = nil
	invalidateProjectionAndDownstream(workflow)
}

func invalidateProjectionAndDownstream(workflow *api.ReleaseWorkflow) {
	workflow.ProjectionInstructions = nil
	workflow.TrackerProjections = nil
	invalidatePreflightAndDownstream(workflow)
}

func invalidatePreflightAndDownstream(workflow *api.ReleaseWorkflow) {
	workflow.TrackerPreflight = nil
	invalidateDupeAndDownstream(workflow)
}

func invalidateDupeAndDownstream(workflow *api.ReleaseWorkflow) {
	workflow.Dupes = nil
	workflow.TrackerApproval = nil
	workflow.Media = nil
	workflow.Descriptions = nil
	invalidateUploadPlan(workflow)
}

func invalidateUploadPlan(workflow *api.ReleaseWorkflow) {
	workflow.DryRun = nil
	workflow.UploadResult = nil
}

func setWorkflowStageStatus(
	workflow *api.ReleaseWorkflow,
	status api.StageStatus,
	actions []api.RequiredAction,
	failures []api.WorkflowFailure,
) {
	workflow.RequiredActions = append([]api.RequiredAction(nil), actions...)
	workflow.Failures = append([]api.WorkflowFailure(nil), failures...)
	if len(actions) > 0 {
		workflow.Status = api.WorkflowStatusBlocked
		return
	}
	switch status {
	case api.StageStatusPending,
		api.StageStatusQueued,
		api.StageStatusReady,
		api.StageStatusBlocked,
		api.StageStatusStale,
		api.StageStatusFailed,
		api.StageStatusPartial,
		api.StageStatusSkipped,
		api.StageStatusRunning,
		api.StageStatusCompleted,
		api.StageStatusExecuted,
		api.StageStatusInterrupted,
		api.StageStatusCanceled,
		api.StageStatusUnavailable:
		workflow.Status = api.WorkflowStatusActive
	}
}

func cloneCommandResult(result CommandResult) (CommandResult, error) {
	state := State{Receipts: map[string]commandReceipt{"result": {Result: result}}}
	cloned, err := cloneState(state)
	if err != nil {
		return CommandResult{}, fmt.Errorf("clone workflow command result: %w", err)
	}
	return cloned.Receipts["result"].Result, nil
}
