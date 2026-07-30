// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// EffectiveSubmissionStatus returns the explicit tracker-submission outcome,
// with compatibility inference for results retained before split outcomes.
func (r UploadTrackerResult) EffectiveSubmissionStatus() StageStatus {
	if r.SubmissionStatus != "" {
		return r.SubmissionStatus
	}
	if r.Status == StageStatusFailed && r.hasClientInjectionFailure() {
		return StageStatusCompleted
	}
	return r.Status
}

// EffectiveClientInjectionStatus returns the explicit client-effect outcome,
// with compatibility inference for results retained before split outcomes.
func (r UploadTrackerResult) EffectiveClientInjectionStatus() StageStatus {
	if r.ClientInjectionStatus != "" {
		return r.ClientInjectionStatus
	}
	switch {
	case r.ClientInjected || r.CrossSeeded:
		return StageStatusCompleted
	case r.hasClientInjectionFailure():
		return StageStatusFailed
	case r.Status == StageStatusSkipped:
		return StageStatusSkipped
	case r.EffectiveSubmissionStatus() == StageStatusCompleted:
		return StageStatusSkipped
	default:
		return StageStatusPending
	}
}

// DerivedStatus projects split submission and client outcomes onto the legacy
// aggregate status field.
func (r UploadTrackerResult) DerivedStatus() StageStatus {
	submission := r.EffectiveSubmissionStatus()
	client := r.EffectiveClientInjectionStatus()
	switch submission {
	case StageStatusCompleted:
		if client == StageStatusFailed || client == StageStatusUnavailable {
			return StageStatusPartial
		}
		return StageStatusCompleted
	case StageStatusSkipped:
		switch client {
		case StageStatusCompleted:
			return StageStatusCompleted
		case StageStatusFailed, StageStatusUnavailable:
			return StageStatusFailed
		case StageStatusPending, StageStatusQueued, StageStatusReady, StageStatusBlocked, StageStatusStale, StageStatusPartial,
			StageStatusSkipped, StageStatusRunning, StageStatusExecuted, StageStatusInterrupted, StageStatusCanceled, "":
			return StageStatusSkipped
		}
	case StageStatusFailed, StageStatusUnavailable, StageStatusInterrupted, StageStatusCanceled:
		return StageStatusFailed
	case StageStatusPending, StageStatusQueued, StageStatusReady, StageStatusBlocked, StageStatusStale, StageStatusPartial,
		StageStatusRunning, StageStatusExecuted, "":
		return submission
	}
	return submission
}

// SubmissionRetryable reports whether a fresh tracker submission is safe to
// offer for this retained result.
func (r UploadTrackerResult) SubmissionRetryable() bool {
	return r.EffectiveSubmissionStatus() == StageStatusFailed && !r.hasFailureCode(OperationFailureUnknownOutcome)
}

// ClientInjectionRetryable reports whether retained registered-artifact
// authority can safely drive a client-only retry.
func (r UploadTrackerResult) ClientInjectionRetryable() bool {
	return r.EffectiveSubmissionStatus() == StageStatusCompleted &&
		r.EffectiveClientInjectionStatus() == StageStatusFailed &&
		r.ClientFailureCode == OperationFailureClientInjection
}

func (r UploadTrackerResult) hasClientInjectionFailure() bool {
	for _, failure := range r.Failures {
		if failure.Failure.Operation == OperationKindClientInjection {
			return true
		}
	}
	return false
}

func (r UploadTrackerResult) hasFailureCode(code OperationFailureCode) bool {
	if r.ClientFailureCode == code {
		return true
	}
	for _, failure := range r.Failures {
		if failure.Failure.Code == code {
			return true
		}
	}
	return false
}
