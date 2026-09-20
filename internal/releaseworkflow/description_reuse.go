// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// prepareReusableDescriptions attaches public reuse data to the same atomic
// repository save as its owning workflow snapshot and command receipt.
func (m *Module) prepareReusableDescriptions(
	ctx context.Context,
	ownerID string,
	state *State,
	snapshot *api.DescriptionSet,
	now time.Time,
) error {
	recorder, ok := m.descriptionBuilder.(ReusableDescriptionBuilder)
	if !ok || snapshot == nil || snapshot.Media == nil {
		return nil
	}
	targets, err := resolveDownstreamTrackerSet(state, nil, downstreamStageDescriptions, now)
	if err != nil {
		return err
	}
	privateMedia, err := m.private.Get(ownerID, state.Workflow.ID, mediaPrivateResourceID(snapshot.Media.ID), now)
	if err != nil {
		return fmt.Errorf("release workflow retain description media: %w", err)
	}
	privateInputs, err := m.private.Get(ownerID, state.Workflow.ID, descriptionPrivateResourceID(snapshot.ID), now)
	if err != nil {
		return fmt.Errorf("release workflow retain description inputs: %w", err)
	}
	instructions, ok := privateInputs.(api.DescriptionInstructions)
	if !ok {
		return ErrPrivateResourceUnavailable
	}
	record, err := recorder.PrepareReusableDescriptions(
		ctx, snapshot.ReleaseRef, targets.Projections(), state.Media[snapshot.Media.ID], privateMedia, instructions, *snapshot,
	)
	if err != nil {
		return fmt.Errorf("release workflow retain reusable descriptions: %w", err)
	}
	state.descriptionReuse = record
	return nil
}
