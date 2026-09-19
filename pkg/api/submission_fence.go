// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SubmissionFenceAuthority identifies one globally fenced tracker submission.
// CoordinatorID and Fence bind admission and completion to the active input
// lease that prepared the exact submitted content.
type SubmissionFenceAuthority struct {
	ContentIdentity SubmissionContentIdentity
	TrackerSite     string
	CoordinatorID   string
	Fence           uint64
}

// SubmissionFenceRecord is the minimal durable authority retained after
// display history is purged. It is not a browser-facing history record.
type SubmissionFenceRecord struct {
	ContentIdentity SubmissionContentIdentity
	TrackerSite     string
	OwnerID         string
	WorkflowID      WorkflowID
	OperationID     WorkflowOperationID
	EffectID        string
	Status          WorkflowEffectStatus
	StartedAt       time.Time
	UpdatedAt       time.Time
	ConfirmedAt     *time.Time
}

// SubmissionExclusion is safe confirmed-upload evidence for tracker selection.
// It deliberately omits the private content identity and source path.
type SubmissionExclusion struct {
	TrackerID   TrackerID `json:"trackerId"`
	Reason      string    `json:"reason"`
	ConfirmedAt time.Time `json:"confirmedAt"`
}

// SubmissionFenceRepository provides safe global exclusion lookups. Callers
// only use terminal success to exclude a tracker; started and unknown outcomes
// remain reconciliation fences.
type SubmissionFenceRepository interface {
	LoadSubmissionFence(context.Context, SubmissionContentIdentity, string) (SubmissionFenceRecord, error)
}

// Validate verifies that the fence has canonical content identity and a
// secret-free, normalized tracker-site key supplied by tracker registration.
func (a SubmissionFenceAuthority) Validate() error {
	if err := a.ValidateSubmission(); err != nil {
		return err
	}
	if strings.TrimSpace(a.CoordinatorID) == "" || a.Fence == 0 {
		return errors.New("submission fence authority is incomplete")
	}
	return nil
}

// ValidateSubmission verifies the content/site portion that tracker planning
// supplies before the workflow reporter binds its active-input lease token.
func (a SubmissionFenceAuthority) ValidateSubmission() error {
	if strings.TrimSpace(a.TrackerSite) == "" {
		return errors.New("submission fence authority is incomplete")
	}
	if strings.ContainsAny(a.TrackerSite, "\r\n\x00") {
		return errors.New("submission fence tracker site is invalid")
	}
	canonical, err := NewSubmissionContentIdentity(a.ContentIdentity.Scope, a.ContentIdentity.Files)
	if err != nil {
		return fmt.Errorf("submission fence content identity: %w", err)
	}
	if a.ContentIdentity.Version != canonical.Version || a.ContentIdentity.Digest != canonical.Digest {
		return errors.New("submission fence content identity is not canonical")
	}
	return nil
}
