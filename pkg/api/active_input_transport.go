// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"strings"
)

// ActiveInputSnapshot is one owner-safe read of the database's current input.
// Private paths, content digests, coordinator tokens and reservation data are excluded.
type ActiveInputSnapshot struct {
	State         ActiveInputState        `json:"state"`
	Revision      uint64                  `json:"revision"`
	InputID       string                  `json:"inputId,omitempty"`
	SourceVersion string                  `json:"sourceVersion,omitempty"`
	Current       *ReleaseWorkflowCurrent `json:"current,omitempty"`
	// RecoveryWorkflowIDs lists this owner's unresolved migrated workflows only
	// while no input is active. Selecting one starts reconciliation; it never
	// resumes preparation or submission.
	RecoveryWorkflowIDs []WorkflowID `json:"recoveryWorkflowIds,omitempty"`
}

// OpenActiveInputRequest explicitly opens or refreshes one source under slot CAS.
type OpenActiveInputRequest struct {
	ExpectedRevision uint64                         `json:"expectedRevision"`
	Request          ContinueReleaseWorkflowRequest `json:"request"`
}

// Validate requires source preparation without existing workflow authority and
// applies the continuation request's desired-state validation.
func (r OpenActiveInputRequest) Validate() error {
	if r.Request.Intent.Preparation == nil || r.Request.Authority != nil {
		return errors.New("open input requires preparation without workflow authority")
	}
	if strings.TrimSpace(r.Request.Intent.Preparation.SourcePath) == "" {
		return ErrPreparationSourceRequired
	}
	return r.Request.Validate()
}

// ReleaseActiveInputRequest closes the caller's input only at the observed slot revision.
// Closing an input does not delete its release history or reusable artifacts.
type ReleaseActiveInputRequest struct {
	ExpectedRevision uint64 `json:"expectedRevision"`
}

// RecoverLegacyActiveInputRequest selects one owner-scoped migrated workflow
// that has an unresolved external effect.
type RecoverLegacyActiveInputRequest struct {
	WorkflowID WorkflowID `json:"workflowId"`
}

// Validate requires the workflow selected for legacy recovery.
func (r RecoverLegacyActiveInputRequest) Validate() error {
	if strings.TrimSpace(string(r.WorkflowID)) == "" {
		return errors.New("legacy recovery workflow is required")
	}
	return nil
}

// ReconcileActiveInputRequest resolves one required reconciliation action in
// the currently claimed legacy recovery slot.
type ReconcileActiveInputRequest struct {
	Authority      WorkflowAuthority    `json:"authority"`
	Answer         RequiredActionAnswer `json:"answer"`
	IdempotencyKey string               `json:"idempotencyKey"`
}

// Validate requires an action answer for the exact workflow revision and an
// idempotency key. The workflow validates the answer against its required action.
func (r ReconcileActiveInputRequest) Validate() error {
	if strings.TrimSpace(string(r.Authority.WorkflowID)) == "" || r.Authority.ExpectedRevision == 0 || strings.TrimSpace(string(r.Answer.ActionID)) == "" ||
		r.Answer.WorkflowRevision != r.Authority.ExpectedRevision || strings.TrimSpace(r.IdempotencyKey) == "" {
		return errors.New("input reconciliation requires exact workflow authority, action answer, and idempotency key")
	}
	return nil
}

// AllSelectedTrackersAlreadyUploaded identifies a successful history-only terminal result.
func (w ReleaseWorkflow) AllSelectedTrackersAlreadyUploaded() bool {
	return w.Status == WorkflowStatusCompleted && w.TrackerProjections == nil && len(w.SubmissionExclusions) > 0
}
