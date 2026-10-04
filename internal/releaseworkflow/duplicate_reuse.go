// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func (m *Module) pendingDuplicateReuse(
	ownerID string,
	state *State,
	projections api.TrackerReleaseProjectionSet,
	command CheckDuplicatesCommand,
	now time.Time,
) (*DuplicateAssessmentReuse, error) {
	pending := state.PendingDuplicateReuse
	if pending == nil {
		return nil, nil
	}
	prior, ok := state.Dupes[pending.Assessment.ID]
	if !ok || prior.Revision != pending.Assessment.Revision || !prior.ExpiresAt.After(now) ||
		prior.ReleaseRef != projections.ReleaseRef ||
		normalizedDuplicateCheckOrdinal(prior.CheckOrdinal) != normalizedDuplicateCheckOrdinal(command.CheckOrdinal) {
		return nil, nil
	}
	previous, ok := state.Projections[prior.ProjectionSet.ID]
	if !ok || previous.Revision != prior.ProjectionSet.Revision || previous.ExecutionMode != projections.ExecutionMode {
		return nil, nil
	}
	evidence, err := m.private.Get(ownerID, state.Workflow.ID, dupePrivateResourceID(prior.ID), now)
	if errors.Is(err, ErrPrivateResourceUnavailable) || errors.Is(err, ErrPrivateResourceConsumed) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("release workflow load reusable duplicate evidence: %w", err)
	}
	return &DuplicateAssessmentReuse{
		Assessment:          prior,
		Projections:         previous,
		PrivateEvidence:     evidence,
		InvalidatedTrackers: pending.InvalidatedTrackers,
	}, nil
}
