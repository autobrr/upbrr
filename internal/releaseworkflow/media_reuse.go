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
	priorMedia *api.MediaArtifactSetRef,
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
	var existing *api.MediaArtifactSet
	var privateExisting any
	if priorMedia != nil {
		current, found := state.Media[priorMedia.ID]
		if found && current.Revision == priorMedia.Revision && current.WorkflowID == state.Workflow.ID &&
			state.Workflow.Release != nil && current.Release == *state.Workflow.Release && current.ReleaseRef == projections.ReleaseRef &&
			state.Workflow.TrackerProjections != nil && current.ProjectionSet == *state.Workflow.TrackerProjections {
			privateExisting, err = m.private.Get(ownerID, state.Workflow.ID, mediaPrivateResourceID(current.ID), now)
			if err != nil && !errors.Is(err, ErrPrivateResourceUnavailable) {
				return fmt.Errorf("release workflow load current reusable media: %w", err)
			}
			if err == nil {
				existing = &current
			}
		}
	}
	if existing != nil {
		requirements, fingerprintErr := mediaRequirementsFingerprint(projections.Projections)
		if fingerprintErr != nil {
			return fingerprintErr
		}
		approval := targets.TrackerApproval()
		sameApproval := existing.TrackerApproval == nil && approval == nil ||
			existing.TrackerApproval != nil && approval != nil && *existing.TrackerApproval == *approval
		if existing.RequirementsFingerprint == requirements && sameApproval {
			state.Workflow.Media = &api.MediaArtifactSetRef{ID: existing.ID, Revision: existing.Revision}
			setWorkflowStageStatus(&state.Workflow, existing.Status, existing.RequiredActions, existing.Failures)
			result.Media = existing
			m.logMediaInventory("retained", existing.Artifacts)
			return nil
		}
	}
	snapshot, retained, err := restorer.RestoreCompatible(ctx, projections.ReleaseRef, projections, existing, privateExisting, now)
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
	stage := "restored"
	if existing != nil {
		stage = "rebound"
	}
	m.logMediaInventory(stage, snapshot.Artifacts)
	return nil
}

func (m *Module) logMediaInventory(stage string, artifacts []api.MediaArtifact) {
	var localImages, selectedLocalImages, hostedLinks, selectedHostedLinks int
	for _, artifact := range artifacts {
		switch artifact.Kind {
		case api.MediaArtifactScreenshot, api.MediaArtifactDVDMenu:
			localImages++
			if artifact.Selected {
				selectedLocalImages++
			}
		case api.MediaArtifactHostedImage:
			hostedLinks++
			if artifact.Selected {
				selectedHostedLinks++
			}
		}
	}
	m.logger.Debugf(
		"release workflow: media inventory stage=%s artifacts=%d local_images=%d selected_local_images=%d hosted_links=%d selected_hosted_links=%d",
		stage, len(artifacts), localImages, selectedLocalImages, hostedLinks, selectedHostedLinks,
	)
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
	committed, err := recorder.HasReusableMediaCommit(ctx, *media)
	if err != nil {
		return fmt.Errorf("release workflow check reusable media receipt: %w", err)
	}
	if committed {
		return nil
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
	committed, checkErr := recorder.HasReusableMediaCommit(ctx, media)
	if checkErr != nil {
		return fmt.Errorf("release workflow check reusable media reconciliation: %w", checkErr)
	}
	if committed {
		return nil
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
