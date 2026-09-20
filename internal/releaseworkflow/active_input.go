// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

// InputVerifier inspects the source layout and hashes bounded initial-file samples
// without collecting provider facts or proving full-file byte equality.
// The coordinator supplies the opaque input ID after verification succeeds.
type InputVerifier func(context.Context, api.PrepareInput) (api.InputRecord, error)

// WithActiveInputs installs the durable singleton slot and local verification boundary.
func WithActiveInputs(repository api.ActiveInputRepository, verifier InputVerifier) Option {
	return func(module *Module) error {
		if repository == nil || verifier == nil {
			return errors.New("release workflow: active input repository and verifier are required")
		}
		module.activeInputs, module.inputVerifier = repository, verifier
		return nil
	}
}

// OpenInputRequest reserves one exact slot revision before doing source I/O.
type OpenInputRequest struct {
	ExpectedRevision    uint64
	Input               api.PrepareInput
	IdempotencyKey      string
	TrackerDecisionMode TrackerDecisionMode
	Composite           *compositeUploadSession
	RequestFingerprint  api.WorkflowFingerprint
}

// ActiveInput returns private owner-scoped input state. Foreign owners receive no payload.
// Plain reads never hash source bytes, create workflows, or renew coordinator leases.
func (m *Module) ActiveInput(ctx context.Context, owner string) (api.ActiveInputRecord, error) {
	if m.activeInputs == nil || strings.TrimSpace(owner) == "" {
		return api.ActiveInputRecord{}, errors.New("release workflow: active input capability and owner are required")
	}
	slot, err := m.activeInputs.LoadActiveInput(ctx)
	if err != nil {
		return api.ActiveInputRecord{}, fmt.Errorf("release workflow read active input: %w", err)
	}
	if slot.State != api.ActiveInputEmpty && slot.OwnerID != owner {
		return api.ActiveInputRecord{}, api.ErrActiveInputBusy
	}
	return slot, nil
}

// OwnsActiveInput reports whether slot belongs to this module's process.
// Callers use it to avoid loading a foreign persisted input during startup.
func (m *Module) OwnsActiveInput(slot api.ActiveInputRecord) bool {
	return m != nil && slot.State != api.ActiveInputEmpty && slot.Fence != 0 && slot.CoordinatorID == m.processEpoch
}

// ResetIdleInputOnStartup clears a previous process's idle input unless a
// non-terminal composite workflow still requires an explicit continuation.
// It never verifies source bytes, resumes workflow work, or claims recovery.
func (m *Module) ResetIdleInputOnStartup(ctx context.Context) error {
	if m == nil || m.activeInputs == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("release workflow: startup reset context is required")
	}
	m.activeMu.Lock()
	defer m.activeMu.Unlock()

	slot, err := m.activeInputs.LoadActiveInput(ctx)
	if err != nil {
		return fmt.Errorf("release workflow load active input for startup reset: %w", err)
	}
	if slot.State == api.ActiveInputEmpty || m.OwnsActiveInput(slot) {
		return nil
	}
	if slot.State != api.ActiveInputActive {
		m.logger.Debugf("active input: startup reset decision=defer state=%s revision=%d", slot.State, slot.Revision)
		return nil
	}
	sourceState, err := m.repository.Load(ctx, slot.OwnerID, slot.WorkflowID)
	if err != nil {
		return fmt.Errorf("release workflow load active workflow for startup reset: %w", err)
	}
	if sourceState.Composite != nil {
		if sourceState.Workflow.Status != api.WorkflowStatusCompleted && sourceState.Workflow.Status != api.WorkflowStatusCanceled &&
			sourceState.Workflow.Status != api.WorkflowStatusFailed {
			m.logger.Debugf("active input: startup reset decision=retain_composite status=%s revision=%d", sourceState.Workflow.Status, slot.Revision)
			return nil
		}
	}
	if err := m.activeInputs.CloseIdleActiveInput(ctx, slot, m.clock.Now().UTC()); err != nil {
		if errors.Is(err, api.ErrActiveInputBusy) || errors.Is(err, api.ErrActiveInputChanged) {
			m.logger.Debugf("active input: startup reset decision=defer state=%s revision=%d", slot.State, slot.Revision)
			return nil
		}
		return fmt.Errorf("release workflow reset idle input on startup: %w", err)
	}
	m.logger.Debugf("active input: startup reset decision=closed state=%s revision=%d", slot.State, slot.Revision)
	return nil
}

// OpenInput verifies each explicit new open request under a renewable reservation.
// A repeated idempotency key returns the already committed result without repeating work.
func (m *Module) OpenInput(ctx context.Context, owner string, request OpenInputRequest) (api.ActiveInputRecord, error) {
	if err := m.requireActiveConfig(ctx); err != nil {
		return api.ActiveInputRecord{}, err
	}
	if m.activeInputs == nil || m.inputVerifier == nil || strings.TrimSpace(owner) == "" ||
		strings.TrimSpace(request.IdempotencyKey) == "" || strings.TrimSpace(request.Input.SourcePath) == "" {
		return api.ActiveInputRecord{}, errors.New("release workflow: complete active input request is required")
	}
	m.activeMu.Lock()
	defer m.activeMu.Unlock()
	fingerprint, err := canonicalCommandFingerprint(struct {
		Input   api.PrepareInput
		Request api.WorkflowFingerprint
	}{request.Input, request.RequestFingerprint})
	if err != nil {
		return api.ActiveInputRecord{}, err
	}
	prior, err := m.activeInputs.LoadActiveInput(ctx)
	if err != nil {
		return api.ActiveInputRecord{}, fmt.Errorf("release workflow load input admission: %w", err)
	}
	m.logger.Debugf("active input: open admission decision=check state=%s revision=%d", prior.State, prior.Revision)
	if prior.State != api.ActiveInputEmpty && !prior.LeaseExpiresAt.After(m.clock.Now()) {
		foreignOwner := prior.OwnerID != owner
		prior, err = m.recoverActiveInput(ctx, prior, owner)
		if err != nil {
			// Foreign callers may release a safely recovered expired slot, but
			// must not learn whether the prior owner has unresolved effects or
			// other recovery failures.
			if foreignOwner {
				return api.ActiveInputRecord{}, api.ErrActiveInputBusy
			}
			return api.ActiveInputRecord{}, err
		}
	}
	if prior.State != api.ActiveInputEmpty && (prior.OwnerID != owner || prior.CoordinatorID != m.processEpoch) {
		return api.ActiveInputRecord{}, api.ErrActiveInputBusy
	}
	if prior.State == api.ActiveInputActive && prior.IdempotencyKey == request.IdempotencyKey {
		if prior.RequestFingerprint != fingerprint {
			return api.ActiveInputRecord{}, ErrIdempotencyConflict
		}
		return prior, nil
	}
	if prior.Revision != request.ExpectedRevision {
		return api.ActiveInputRecord{}, api.ErrActiveInputChanged
	}
	if prior.State != api.ActiveInputActive && prior.State != api.ActiveInputEmpty {
		return api.ActiveInputRecord{}, api.ErrActiveInputBusy
	}
	reservation, err := m.newID("input_open")
	if err != nil {
		return api.ActiveInputRecord{}, err
	}
	now := m.clock.Now()
	pending := prior
	pending.Revision++
	pending.State = api.ActiveInputSwitchPending
	if prior.State == api.ActiveInputEmpty {
		pending.State = api.ActiveInputOpening
		pending.Fence++
	}
	pending.OwnerID, pending.CoordinatorID = owner, m.processEpoch
	pending.ReservationID, pending.RequestedPath, pending.IdempotencyKey = reservation, request.Input.SourcePath, request.IdempotencyKey
	pending.RequestFingerprint = fingerprint
	pending.LeaseExpiresAt = now.Add(workflowWorkLeaseTTL)
	if err := m.activeInputs.CompareAndSwapActiveInput(ctx, prior, pending, now); err != nil {
		return api.ActiveInputRecord{}, fmt.Errorf("release workflow reserve input: %w", err)
	}
	ctx = api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: m.processEpoch, Fence: pending.Fence})
	m.startActiveInputHeartbeat(ctx, pending.Fence)
	committed := false
	defer func() {
		if committed {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		rollback := prior
		rollback.Revision, rollback.Fence = pending.Revision+1, pending.Fence
		rollback.OwnerID, rollback.CoordinatorID = owner, m.processEpoch
		rollback.LeaseExpiresAt = m.clock.Now().Add(workflowWorkLeaseTTL)
		if cleanupErr := m.activeInputs.CompareAndSwapActiveInput(cleanup, pending, rollback, m.clock.Now()); cleanupErr != nil {
			m.logger.Warnf("active input: rollback failed state=recovery_required")
		}
	}()
	if err := m.cancelPriorInputWork(ctx, owner, prior.WorkflowID); err != nil {
		return api.ActiveInputRecord{}, err
	}
	if err := m.reconcileReusableMedia(ctx, owner, prior.WorkflowID); err != nil {
		return api.ActiveInputRecord{}, err
	}
	record, err := m.inputVerifier(ctx, request.Input)
	if err != nil {
		return api.ActiveInputRecord{}, fmt.Errorf("release workflow verify input: %w", err)
	}
	record.ID, err = m.newID("input")
	if err != nil {
		return api.ActiveInputRecord{}, err
	}
	record.UpdatedAt = m.clock.Now()
	previousInput, lookupErr := m.activeInputs.LoadInputRecord(ctx, record.CanonicalPath)
	if lookupErr == nil {
		record.ID = previousInput.ID
	} else if !errors.Is(lookupErr, api.ErrInputRecordNotFound) {
		return api.ActiveInputRecord{}, fmt.Errorf("release workflow load verified input: %w", lookupErr)
	}
	workflow := prior.WorkflowID
	var updatedWorkflow *api.ReleaseWorkflowStateRecord
	if prior.InputID != record.ID || workflow == "" {
		created, createErr := m.Execute(ctx, owner, CreateWorkflowCommand{
			SourcePath:          strings.TrimSpace(record.CanonicalPath),
			Instructions:        request.Input.Instructions,
			IdempotencyKey:      reservation,
			TrackerDecisionMode: trackerDecisionModeFromContext(ctx, normalizeTrackerDecisionMode(request.TrackerDecisionMode)),
			Composite:           request.Composite,
			RequestFingerprint:  request.RequestFingerprint,
		})
		if createErr != nil {
			return api.ActiveInputRecord{}, createErr
		}
		workflow = created.Workflow.ID
	} else {
		state, loadErr := m.repository.Load(ctx, owner, workflow)
		if loadErr != nil {
			return api.ActiveInputRecord{}, fmt.Errorf("release workflow load refreshed workflow: %w", loadErr)
		}
		state.SourcePath = strings.TrimSpace(record.CanonicalPath)
		if request.Composite != nil && (state.Composite == nil || state.Composite.RequestFingerprint != request.Composite.RequestFingerprint) {
			state.Composite = request.Composite
		}
		state.Workflow.Revision++
		state.Workflow.UpdatedAt = m.clock.Now()
		state.Workflow.Status = api.WorkflowStatusDraft
		state.Workflow.SubmissionExclusions = nil
		state.Workflow.RequiredActions, state.Workflow.Failures = nil, nil
		invalidatePreparedAndDownstream(&state.Workflow)
		if state.Composite != nil {
			state.Composite.LastCommittedRevision = state.Workflow.Revision
		}
		encoded, encodeErr := workflowStateRecord(owner, state)
		if encodeErr != nil {
			return api.ActiveInputRecord{}, encodeErr
		}
		updatedWorkflow = &encoded
	}
	active := pending
	active.Revision++
	active.State, active.InputID, active.SourceVersion, active.WorkflowID = api.ActiveInputActive, record.ID, record.SourceVersion, workflow
	active.ReservationID, active.RequestedPath = "", ""
	active.LeaseExpiresAt = m.clock.Now().Add(workflowWorkLeaseTTL)
	if err := m.activeInputs.FinalizeActiveInput(ctx, pending, active, record, updatedWorkflow, m.clock.Now()); err != nil {
		return api.ActiveInputRecord{}, fmt.Errorf("release workflow commit verified input: %w", err)
	}
	committed = true
	return active, nil
}

func (m *Module) cancelPriorInputWork(ctx context.Context, owner string, workflow api.WorkflowID) error {
	operations, err := m.operations.ListActiveOperations(ctx)
	if err != nil {
		return fmt.Errorf("release workflow list input operations: %w", err)
	}
	for _, operation := range operations {
		if operation.OwnerID != owner || operation.WorkflowID != workflow {
			return api.ErrActiveInputBusy
		}
		m.operationWorkersMu.Lock()
		worker := m.operationWorkers[operation.OperationID]
		m.operationWorkersMu.Unlock()
		if worker.cancel == nil {
			return api.ErrActiveInputBusy
		}
		worker.cancel()
		select {
		case <-ctx.Done():
			return fmt.Errorf("release workflow wait for input cleanup: %w", ctx.Err())
		case <-worker.done:
		}
	}
	return nil
}

func (m *Module) recoverActiveInput(ctx context.Context, prior api.ActiveInputRecord, requestedOwner string) (api.ActiveInputRecord, error) {
	now := m.clock.Now()
	recovering := prior
	recovering.State, recovering.Revision, recovering.Fence = api.ActiveInputRecovering, prior.Revision+1, prior.Fence+1
	recovering.CoordinatorID, recovering.LeaseExpiresAt = m.processEpoch, now.Add(workflowWorkLeaseTTL)
	if err := m.activeInputs.CompareAndSwapActiveInput(ctx, prior, recovering, now); err != nil {
		return api.ActiveInputRecord{}, fmt.Errorf("release workflow claim input recovery: %w", err)
	}
	m.startActiveInputHeartbeat(ctx, recovering.Fence)
	ctx = api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: m.processEpoch, Fence: recovering.Fence})
	// Restore only the committed input under the new fence before recovery can
	// claim work. Existing unknown effects still block switching and submission.
	restored := recovering
	restored.Revision++
	restored.State = api.ActiveInputActive
	if restored.InputID == "" {
		restored.State = api.ActiveInputEmpty
	}
	restored.ReservationID, restored.RequestedPath = "", ""
	if prior.ReservationID != "" {
		// An abandoned request never committed its idempotency receipt.
		restored.IdempotencyKey, restored.RequestFingerprint = "", ""
	}
	if err := m.activeInputs.CompareAndSwapActiveInput(ctx, recovering, restored, m.clock.Now()); err != nil {
		return api.ActiveInputRecord{}, fmt.Errorf("release workflow restore committed input: %w", err)
	}
	if err := m.ensureOperationRecovery(ctx); err != nil {
		return api.ActiveInputRecord{}, err
	}
	if restored.OwnerID != requestedOwner && restored.State == api.ActiveInputActive {
		empty := api.ActiveInputRecord{
			State:          api.ActiveInputEmpty,
			Revision:       restored.Revision + 1,
			Fence:          restored.Fence,
			OwnerID:        restored.OwnerID,
			CoordinatorID:  m.processEpoch,
			LeaseExpiresAt: restored.LeaseExpiresAt,
		}
		if err := m.activeInputs.CompareAndSwapActiveInput(ctx, restored, empty, m.clock.Now()); err != nil {
			return api.ActiveInputRecord{}, fmt.Errorf("release workflow release recovered input: %w", err)
		}
		return empty, nil
	}
	return restored, nil
}

// ReleaseInput closes only a safe, idle, exact owned slot; it retains reusable history.
func (m *Module) ReleaseInput(ctx context.Context, owner string, revision uint64) error {
	if err := m.requireActiveConfig(ctx); err != nil {
		return err
	}
	m.activeMu.Lock()
	defer m.activeMu.Unlock()
	slot, err := m.ActiveInput(ctx, owner)
	if err != nil {
		return err
	}
	if slot.Revision != revision {
		return api.ErrActiveInputChanged
	}
	if slot.State == api.ActiveInputEmpty {
		return nil
	}
	if slot.State == api.ActiveInputActive {
		if err := m.activeInputs.CloseIdleActiveInput(ctx, slot, m.clock.Now().UTC()); err != nil {
			return fmt.Errorf("release workflow close input: %w", err)
		}
		if m.activeCancel != nil {
			m.activeCancel()
		}
		return nil
	}
	if slot.CoordinatorID != m.processEpoch {
		return api.ErrActiveInputBusy
	}
	empty := api.ActiveInputRecord{
		State:          api.ActiveInputEmpty,
		Revision:       slot.Revision + 1,
		Fence:          slot.Fence,
		OwnerID:        owner,
		CoordinatorID:  m.processEpoch,
		LeaseExpiresAt: slot.LeaseExpiresAt,
	}
	if err := m.activeInputs.CompareAndSwapActiveInput(ctx, slot, empty, m.clock.Now()); err != nil {
		return fmt.Errorf("release workflow close input: %w", err)
	}
	if m.activeCancel != nil {
		m.activeCancel()
	}
	return nil
}

// Shutdown stops local workflow work, then relinquishes only this
// coordinator's active-input lease. It is for process shutdown, never runtime
// replacement, and retains the committed input for recovery.
func (m *Module) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("release workflow: shutdown context is required")
	}
	if err := m.Coordinator.Shutdown(ctx); err != nil {
		return err
	}
	if m.activeInputs == nil {
		return nil
	}
	m.activeMu.Lock()
	defer m.activeMu.Unlock()
	slot, err := m.activeInputs.LoadActiveInput(ctx)
	if err != nil {
		return fmt.Errorf("release workflow load active input for shutdown: %w", err)
	}
	now := m.clock.Now().UTC()
	if slot.State == api.ActiveInputEmpty || slot.CoordinatorID != m.processEpoch || slot.Fence == 0 || !slot.LeaseExpiresAt.After(now) {
		return nil
	}
	if err := m.activeInputs.RelinquishActiveInput(ctx, m.processEpoch, slot.Fence, now); err != nil {
		return fmt.Errorf("release workflow relinquish active input: %w", err)
	}
	return nil
}

func (m *Module) startActiveInputHeartbeat(ctx context.Context, fence uint64) {
	if m.activeCancel != nil {
		m.activeCancel()
	}
	// The coordinator retains the slot after the opening request completes.
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	m.activeCancel, m.activeDone = cancel, done
	go func() {
		defer close(done)
		ticker := time.NewTicker(workflowWorkHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := m.clock.Now()
				if err := m.activeInputs.RenewActiveInput(ctx, m.processEpoch, fence, now, now.Add(workflowWorkLeaseTTL)); err != nil {
					return
				}
			}
		}
	}()
}

func (m *Module) activeMutationContext(ctx context.Context, owner string, workflow api.WorkflowID) (context.Context, error) {
	if m.activeInputs == nil {
		return ctx, nil
	}
	_, suppliedAuthority := api.ActiveInputAuthorityFromContext(ctx)
	slot, err := m.activeInputs.LoadActiveInput(ctx)
	if err != nil {
		return ctx, fmt.Errorf("release workflow load mutation input: %w", err)
	}
	if slot.OwnerID != owner {
		return ctx, api.ErrActiveInputBusy
	}
	if slot.State != api.ActiveInputEmpty && !slot.LeaseExpiresAt.After(m.clock.Now()) {
		if suppliedAuthority {
			return ctx, api.ErrActiveInputLeaseLost
		}
		m.activeMu.Lock()
		defer m.activeMu.Unlock()
		slot, err = m.activeInputs.LoadActiveInput(ctx)
		if err != nil {
			return ctx, fmt.Errorf("release workflow reload mutation input: %w", err)
		}
		if slot.OwnerID != owner {
			return ctx, api.ErrActiveInputBusy
		}
		if slot.State != api.ActiveInputEmpty && !slot.LeaseExpiresAt.After(m.clock.Now()) {
			slot, err = m.recoverActiveInput(ctx, slot, owner)
			if err != nil {
				return ctx, err
			}
		}
	}
	if slot.State != api.ActiveInputActive || slot.WorkflowID != workflow {
		return ctx, api.ErrActiveInputChanged
	}
	if slot.CoordinatorID != m.processEpoch || !slot.LeaseExpiresAt.After(m.clock.Now()) {
		return ctx, api.ErrActiveInputLeaseLost
	}
	if authority, ok := api.ActiveInputAuthorityFromContext(ctx); ok {
		if authority.CoordinatorID != slot.CoordinatorID || authority.Fence != slot.Fence {
			return ctx, api.ErrActiveInputLeaseLost
		}
		return ctx, nil
	}
	return api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: slot.CoordinatorID, Fence: slot.Fence}), nil
}

func (m *Module) attachVerifiedInput(ctx context.Context, owner string, workflow api.WorkflowID, input *api.PrepareInput) error {
	if m.activeInputs == nil {
		return nil
	}
	slot, err := m.ActiveInput(ctx, owner)
	if err != nil {
		return err
	}
	if slot.State != api.ActiveInputActive || slot.WorkflowID != workflow {
		return api.ErrActiveInputChanged
	}
	record, err := m.activeInputs.LoadInputRecordByID(ctx, slot.InputID)
	if err != nil {
		return fmt.Errorf("release workflow load input verification: %w", err)
	}
	if record.SourceVersion != slot.SourceVersion || !pathing.SamePath(record.CanonicalPath, input.SourcePath) {
		return api.ErrActiveInputChanged
	}
	var verified api.VerifiedInputSource
	if err := json.Unmarshal(record.Manifest, &verified); err != nil {
		return fmt.Errorf("release workflow decode input verification: %w", err)
	}
	if verified.Identity.Digest != slot.SourceVersion {
		return api.ErrActiveInputChanged
	}
	input.VerifiedSource = &verified
	return nil
}
