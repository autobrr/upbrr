// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// restoreReusableMedia attaches compatible saved images as soon as current
// duplicate decisions and tracker authority permit media use. Reopening an
// input must not require a new capture command to recover its saved selections.
func (m *Module) restoreReusableMedia(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	result *CommandResult,
) error {
	restorer, ok := m.mediaBuilder.(CompatibleMediaRestorer)
	if !ok || state.Workflow.Media != nil {
		return nil
	}
	targets, err := resolveDownstreamTrackerSet(state, nil, downstreamStageMedia, now)
	if errors.Is(err, ErrInvalidTransition) {
		// Pending duplicate decisions or tracker approval do not authorize reuse.
		return nil
	}
	if err != nil {
		return err
	}
	projections := targets.Projections()
	if len(projections.Projections) == 0 {
		return nil
	}
	snapshot, retained, err := restorer.RestoreCompatible(ctx, projections.ReleaseRef, projections, now)
	if err != nil {
		return fmt.Errorf("release workflow restore saved media: %w", err)
	}
	if len(snapshot.Artifacts) == 0 {
		return nil
	}
	restored, err := m.publishMediaMutation(ownerID, state, nextRevision, now, snapshot, retained)
	if err != nil {
		return err
	}
	result.Media = restored.Media
	m.logger.Debugf("release workflow: media reuse state=restored count=%d", len(snapshot.Artifacts))
	return nil
}

// recordReusableMedia records one committed snapshot unless its exact media
// receipt is already durable. Command receipt replay calls this after a
// previous post-save recording failure.
func (m *Module) recordReusableMedia(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	media *api.MediaArtifactSet,
	now time.Time,
) error {
	recorder, ok := m.mediaBuilder.(ReusableMediaRecorder)
	if !ok || media == nil {
		return nil
	}
	if checker, ok := recorder.(ReusableMediaCommitChecker); ok {
		committed, err := checker.HasReusableMediaCommit(ctx, *media)
		if err != nil {
			return fmt.Errorf("release workflow check reusable media receipt: %w", err)
		}
		if committed {
			return nil
		}
	}
	retained, err := m.private.Get(ownerID, workflowID, mediaPrivateResourceID(media.ID), now)
	if err != nil {
		return fmt.Errorf("release workflow retain media selection: %w", err)
	}
	if err := recorder.RecordReusableMedia(ctx, *media, retained); err != nil {
		return fmt.Errorf("release workflow retain media selection: %w", err)
	}
	return nil
}

// reconcileReusableMedia records the final committed media snapshot before its
// active input is invalidated. A missing retained resource blocks the switch:
// recording a stale snapshot could otherwise restore deleted selections later.
func (m *Module) reconcileReusableMedia(ctx context.Context, ownerID string, workflowID api.WorkflowID) error {
	recorder, ok := m.mediaBuilder.(ReusableMediaRecorder)
	if !ok || workflowID == "" {
		return nil
	}
	state, err := m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return fmt.Errorf("release workflow load media for reuse reconciliation: %w", err)
	}
	if state.Workflow.Media == nil {
		return nil
	}
	media, ok := state.Media[state.Workflow.Media.ID]
	if !ok || media.Revision != state.Workflow.Media.Revision {
		return fmt.Errorf("%w: committed media snapshot is unavailable", ErrInvalidTransition)
	}
	if checker, ok := recorder.(ReusableMediaCommitChecker); ok {
		committed, checkErr := checker.HasReusableMediaCommit(ctx, media)
		if checkErr != nil {
			return fmt.Errorf("release workflow check reusable media reconciliation: %w", checkErr)
		}
		if committed {
			return nil
		}
	}
	retained, err := m.private.Get(ownerID, workflowID, mediaPrivateResourceID(media.ID), m.clock.Now().UTC())
	if err != nil {
		return fmt.Errorf("release workflow load retained media for reuse reconciliation: %w", err)
	}
	if err := recorder.RecordReusableMedia(ctx, media, retained); err != nil {
		return fmt.Errorf("release workflow record reusable media reconciliation: %w", err)
	}
	return nil
}
