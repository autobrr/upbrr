// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package submission

import "errors"

// Outcome describes what the adapter established about one remote
// attempt. HTTP status and diagnostic text alone do not establish an outcome.
type Outcome uint8

const (
	Unknown Outcome = iota
	NotSent
	Rejected
	Accepted
)

// Error carries adapter-owned protocol evidence without retaining
// response bodies or changing the cause's public diagnostic or error identity.
type Error struct {
	Outcome Outcome
	cause   error
}

func (e *Error) Error() string { return e.cause.Error() }

func (e *Error) Unwrap() error { return e.cause }

// NewError marks a proven outcome; unclassified failures must remain
// unknown. The cause must be non-nil and sanitized before exposing diagnostics.
func NewError(outcome Outcome, cause error) *Error {
	return &Error{Outcome: outcome, cause: cause}
}

// OutcomeOf treats a completed adapter call as accepted and
// leaves errors unknown unless the adapter supplied explicit outcome evidence.
func OutcomeOf(err error) Outcome {
	if err == nil {
		return Accepted
	}
	if failure, ok := errors.AsType[*Error](err); ok {
		return failure.Outcome
	}
	return Unknown
}
