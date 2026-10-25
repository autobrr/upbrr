// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// automaticScreenshotSelection records intent, not the presence or format of images.
// An automatic incremental capture cannot replace an existing explicit selection.
func automaticScreenshotSelection(instructions api.MediaCaptureInstructions, prior *api.MediaArtifactSet) bool {
	if instructions.Purpose == api.ScreenshotPurposeMenu {
		return prior != nil && prior.AutomaticScreenshotSelection
	}
	if instructions.ScreenshotCount > 0 || len(instructions.Selections) > 0 || len(instructions.ManualFrames) > 0 || instructions.SavedImagePlanID != "" {
		return false
	}
	return prior == nil || prior.AutomaticScreenshotSelection
}

func markExplicitScreenshotSelection(snapshot *api.MediaArtifactSet, ids []api.PublicResourceID) {
	for _, artifact := range snapshot.Artifacts {
		if !slices.Contains(ids, artifact.ID) {
			continue
		}
		if artifact.Kind == api.MediaArtifactScreenshot || artifact.Kind == api.MediaArtifactHostedImage &&
			(artifact.Purpose == "" || artifact.Purpose == api.ScreenshotPurposeFinal) {
			snapshot.AutomaticScreenshotSelection = false
			return
		}
	}
}

// screenshotReferenceSuppression derives operation-local policy from exact retained
// media and count intent. Legacy media has no known automatic-selection authority.
func screenshotReferenceSuppression(state *State, targets []api.TrackerID, instructions map[api.TrackerID]api.TrackerProjectionInstructions) map[string]bool {
	explicitMedia := false
	if ref := state.Workflow.Media; ref != nil {
		media, ok := state.Media[ref.ID]
		explicitMedia = !ok || media.Revision != ref.Revision || state.Workflow.Release == nil || media.Release != *state.Workflow.Release ||
			!media.AutomaticScreenshotSelection
	}
	var suppressed map[string]bool
	for _, target := range targets {
		instruction := instructions[target]
		if explicitMedia || instruction.ScreenshotCount != nil && !instruction.ScreenshotCountIsDefault {
			if suppressed == nil {
				suppressed = make(map[string]bool)
			}
			suppressed[strings.ToUpper(strings.TrimSpace(string(target)))] = true
		}
	}
	return suppressed
}

func currentScreenshotReferenceSuppression(state *State) map[string]bool {
	if state.Workflow.TrackerProjections == nil {
		return nil
	}
	ref := state.Workflow.TrackerProjections
	projections := state.Projections[ref.ID]
	targets := make([]api.TrackerID, 0, len(projections.Projections))
	for _, projection := range projections.Projections {
		targets = append(targets, projection.TrackerID)
	}
	stale := projections.Revision != ref.Revision || state.Workflow.Release == nil || projections.Release != *state.Workflow.Release
	var instructions map[api.TrackerID]api.TrackerProjectionInstructions
	if instructionRef := state.Workflow.ProjectionInstructions; instructionRef != nil {
		snapshot, ok := state.ProjectionInstructions[instructionRef.ID]
		stale = stale || !ok || snapshot.Revision != instructionRef.Revision || snapshot.WorkflowID != state.Workflow.ID ||
			projections.Instructions == nil || *projections.Instructions != *instructionRef
		instructions = snapshot.Instructions
	} else if projections.Instructions != nil {
		stale = true
	}
	if stale {
		suppressed := make(map[string]bool, len(targets))
		for _, target := range targets {
			suppressed[strings.ToUpper(strings.TrimSpace(string(target)))] = true
		}
		return suppressed
	}
	return screenshotReferenceSuppression(state, targets, instructions)
}

// screenshotDescriptionResources carries policy without persisting a second copy.
// The same wrapper reaches generation, reuse, override rebuild and final validation.
func screenshotDescriptionResources(state *State, media any) any {
	suppressed := currentScreenshotReferenceSuppression(state)
	resources, wrapped := media.(DescriptionResources)
	if !wrapped {
		if len(suppressed) == 0 {
			return media
		}
		resources.Media = media
	}
	resources.SuppressImportedScreenshotReferences = suppressed
	return resources
}
