// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"errors"
	"time"
)

// ActiveInputState describes the durable singleton input transition.
type ActiveInputState string

const (
	ActiveInputEmpty         ActiveInputState = "empty"
	ActiveInputOpening       ActiveInputState = "opening"
	ActiveInputActive        ActiveInputState = "active"
	ActiveInputSwitchPending ActiveInputState = "switch_pending"
	ActiveInputRecovering    ActiveInputState = "recovering"
)

var (
	ErrInputRecordNotFound  = errors.New("input record: not found")
	ErrActiveInputBusy      = errors.New("active input: busy")
	ErrActiveInputChanged   = errors.New("active input: changed")
	ErrActiveInputLeaseLost = errors.New("active input: coordinator lease lost")
)

// InputRecord belongs to a canonical local source, independently of its prepared generations.
// It is private repository data, not a transport response.
type InputRecord struct {
	ID            string
	CanonicalPath string
	SourceVersion string
	Manifest      []byte
	UpdatedAt     time.Time
}

// ActiveInputRecord keeps the previously committed input while a replacement is inspected.
// Fence advances on coordinator acquisition; Revision advances on every state transition.
// Renewal changes only LeaseExpiresAt so observers need not resynchronize on heartbeats.
type ActiveInputRecord struct {
	State              ActiveInputState
	Revision           uint64
	Fence              uint64
	OwnerID            string
	CoordinatorID      string
	LeaseExpiresAt     time.Time
	InputID            string
	SourceVersion      string
	WorkflowID         WorkflowID
	ReservationID      string
	RequestedPath      string
	IdempotencyKey     string
	RequestFingerprint WorkflowFingerprint
}

// ActiveInputRepository serializes the configured database's sole input slot.
// Reads do not acquire or renew a lease. Callers enforce owner isolation before projection.
type ActiveInputRepository interface {
	LoadActiveInput(context.Context) (ActiveInputRecord, error)
	CompareAndSwapActiveInput(context.Context, ActiveInputRecord, ActiveInputRecord, time.Time) error
	CloseIdleActiveInput(context.Context, ActiveInputRecord, time.Time) error
	FinalizeActiveInput(context.Context, ActiveInputRecord, ActiveInputRecord, InputRecord, *ReleaseWorkflowStateRecord, time.Time) error
	RenewActiveInput(context.Context, string, uint64, time.Time, time.Time) error
	RelinquishActiveInput(context.Context, string, uint64, time.Time) error
	LoadInputRecord(context.Context, string) (InputRecord, error)
	LoadInputRecordByID(context.Context, string) (InputRecord, error)
	SaveInputRecord(context.Context, InputRecord) (InputRecord, error)
}

// ActiveInputAuthority binds a mutation to the coordinator that admitted it.
// It is installed by the backend, never decoded from a client request.
type ActiveInputAuthority struct {
	CoordinatorID string
	Fence         uint64
}

type activeInputAuthorityKey struct{}

// WithActiveInputAuthority attaches backend-issued coordinator authority for repository admission checks.
// Attaching a value does not validate its fence or renew the coordinator lease.
func WithActiveInputAuthority(ctx context.Context, authority ActiveInputAuthority) context.Context {
	return context.WithValue(ctx, activeInputAuthorityKey{}, authority)
}

// ActiveInputAuthorityFromContext returns the attached authority and whether it was present.
// Repository mutation checks remain responsible for validating it against the current slot.
func ActiveInputAuthorityFromContext(ctx context.Context) (ActiveInputAuthority, bool) {
	authority, ok := ctx.Value(activeInputAuthorityKey{}).(ActiveInputAuthority)
	return authority, ok
}
