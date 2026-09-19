// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

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
