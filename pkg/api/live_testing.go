// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"strings"
	"sync/atomic"
)

// ErrLiveTestMutationDisabled identifies a write forbidden by the process policy.
var ErrLiveTestMutationDisabled = errors.New("live testing prohibits tracker submission and torrent-client writes")

// MaxLiveTestImageUploads is the process-enforced ceiling for one run's
// journaled remote image attempts.
const MaxLiveTestImageUploads = 500

// LiveTestEffectCounts distinguishes rejected requests from guarded writes for
// one forbidden effect class. Allowed lookup and image-host requests are excluded.
type LiveTestEffectCounts struct {
	RequestsDenied       uint64 `json:"requestsDenied"`
	MutationCallsDenied  uint64 `json:"mutationCallsDenied"`
	RemoteCallsStarted   uint64 `json:"remoteCallsStarted"`
	RemoteCallsSucceeded uint64 `json:"remoteCallsSucceeded"`
}

// TestRuntimeInfo is the secret-free live-testing capability and effect receipt.
type TestRuntimeInfo struct {
	ImageUploadLimit           int                  `json:"imageUploadLimit"`
	Mode                       string               `json:"mode"`
	RunID                      string               `json:"runId"`
	TrackerSubmissionAllowed   bool                 `json:"trackerSubmissionAllowed"`
	ClientMutationAllowed      bool                 `json:"clientMutationAllowed"`
	ImageUploadsRequireJournal bool                 `json:"imageUploadsRequireJournal"`
	TrackerSubmission          LiveTestEffectCounts `json:"trackerSubmission"`
	ClientMutation             LiveTestEffectCounts `json:"clientMutation"`
}

type liveTestEffectCounter struct {
	requests  atomic.Uint64
	mutations atomic.Uint64
}

// LiveTestPolicy is an immutable dependency shared by every runtime generation.
// Its private identity and journal path cannot be changed by workflow requests
// or configuration activation. A nil policy preserves ordinary execution.
type LiveTestPolicy struct {
	maxImages   int
	runID       string
	journalPath string
	tracker     liveTestEffectCounter
	client      liveTestEffectCounter
}

// NewLiveTestPolicy binds already-validated profile identity and private journal.
// The application entrypoint must validate the profile before constructing it.
func NewLiveTestPolicy(runID, journalPath string, maxImages int) (*LiveTestPolicy, error) {
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(journalPath) == "" || maxImages < 0 || maxImages > MaxLiveTestImageUploads {
		return nil, errors.New("live testing requires a run identity and image journal")
	}
	return &LiveTestPolicy{
		runID:       runID,
		journalPath: journalPath,
		maxImages:   maxImages,
	}, nil
}

// ImageUploadLimit is the maximum image count permitted across this run's attempts.
func (p *LiveTestPolicy) ImageUploadLimit() int {
	if p == nil {
		return 0
	}
	return p.maxImages
}

// RunID returns the opaque process-bound run identity.
func (p *LiveTestPolicy) RunID() string {
	if p == nil {
		return ""
	}
	return p.runID
}

// ImageJournalPath returns private dependency configuration, never a UI field.
func (p *LiveTestPolicy) ImageJournalPath() string {
	if p == nil {
		return ""
	}
	return p.journalPath
}

// RejectRequest records a prohibited request before any mutation is dispatched.
func (p *LiveTestPolicy) RejectRequest(operation OperationKind) error {
	if p == nil {
		return nil
	}
	p.counter(operation).requests.Add(1)
	return liveTestMutationError(operation)
}

// RejectMutation guards the actual mutation boundary without relying on context.
// Live-test dependencies never delegate forbidden writes, so their remote-call
// counts remain zero. Tests additionally assert the underlying fake is untouched.
func (p *LiveTestPolicy) RejectMutation(operation OperationKind) error {
	if p == nil {
		return nil
	}
	p.counter(operation).mutations.Add(1)
	return liveTestMutationError(operation)
}

func (p *LiveTestPolicy) counter(operation OperationKind) *liveTestEffectCounter {
	if operation == OperationKindClientInjection {
		return &p.client
	}
	return &p.tracker
}

func liveTestMutationError(operation OperationKind) error {
	return NewOperationError(OperationFailure{
		Code:      OperationFailureLiveTestMutationDisabled,
		Operation: operation,
		Message:   "Live testing prohibits tracker submission and torrent-client writes. Use dry-run preparation.",
		Recovery:  OperationRecoveryNone,
	}, ErrLiveTestMutationDisabled)
}

// Snapshot returns a path-free receipt; nil means ordinary execution.
func (p *LiveTestPolicy) Snapshot() *TestRuntimeInfo {
	if p == nil {
		return nil
	}
	return &TestRuntimeInfo{
		ImageUploadLimit:           p.maxImages,
		Mode:                       "live_test",
		RunID:                      p.runID,
		ImageUploadsRequireJournal: true,
		TrackerSubmission: LiveTestEffectCounts{
			RequestsDenied:      p.tracker.requests.Load(),
			MutationCallsDenied: p.tracker.mutations.Load(),
		},
		ClientMutation: LiveTestEffectCounts{
			RequestsDenied:      p.client.requests.Load(),
			MutationCallsDenied: p.client.mutations.Load(),
		},
	}
}
