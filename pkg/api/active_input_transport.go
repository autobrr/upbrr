// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

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

// ReconcileActiveInputRequest resolves one required reconciliation action in
// the currently claimed legacy recovery slot.
type ReconcileActiveInputRequest struct {
	Authority      WorkflowAuthority    `json:"authority"`
	Answer         RequiredActionAnswer `json:"answer"`
	IdempotencyKey string               `json:"idempotencyKey"`
}

// AllSelectedTrackersAlreadyUploaded identifies a successful history-only terminal result.
func (w ReleaseWorkflow) AllSelectedTrackersAlreadyUploaded() bool {
	return w.Status == WorkflowStatusCompleted && w.TrackerProjections == nil && len(w.SubmissionExclusions) > 0
}
