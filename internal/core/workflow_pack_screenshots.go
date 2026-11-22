// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

// selectedTrackerImageSources keeps reuse decisions in the same screenshot lane
// as live hosting. Menus and comparison sets keep their existing behavior.
func selectedTrackerImageSources(
	artifacts []api.MediaArtifact, selected map[api.PublicResourceID]struct{}, requirements api.TrackerArtifactRequirements,
) map[api.PublicResourceID]struct{} {
	scoped := make(map[api.PublicResourceID]struct{}, len(selected))
	for _, artifact := range artifacts {
		if _, ok := selected[artifact.ID]; !ok {
			continue
		}
		if artifact.Kind == api.MediaArtifactScreenshot && (!screenshotMatchesPackRequirement(artifact, requirements.PackScreenshots) ||
			artifact.AdditionalDisc && requirements.DiscScreenshots == nil) {
			continue
		}
		scoped[artifact.ID] = struct{}{}
	}
	return scoped
}

func screenshotMatchesPackRequirement(artifact api.MediaArtifact, pack *api.PackScreenshotRequirement) bool {
	if pack == nil {
		return artifact.PackMemberID == ""
	}
	return slices.Contains(pack.MemberIDs, artifact.PackMemberID)
}

func scopedImageSourcesCoverRequirement(
	artifacts []api.MediaArtifact, covered map[api.PublicResourceID]struct{}, requirements api.TrackerArtifactRequirements,
) bool {
	if requirements.PackScreenshots == nil && requirements.DiscScreenshots == nil {
		return true
	}
	sources := make([]api.MediaArtifact, 0, len(covered))
	for _, artifact := range artifacts {
		if _, ok := covered[artifact.ID]; ok {
			sources = append(sources, artifact)
		}
	}
	return releaseworkflow.LocalScreenshotSourceRequirementsMet(sources, []api.TrackerReleaseProjection{{Artifacts: requirements}})
}

// captureWorkflowPackScreenshots uses only member paths in the exact prepared
// subject, never user selections or the primary video's timing for another file.
func (b workflowMediaBuilder) captureWorkflowPackScreenshots(
	ctx context.Context, subject api.ScreenshotSubject, requirement api.PackScreenshotRequirement,
) ([]api.ScreenshotImage, error) {
	if b.screenshots == nil {
		return nil, errors.New("workflow pack screenshots: capture service is unavailable")
	}
	members := make(map[string]string, len(subject.PackMembers))
	for _, member := range subject.PackMembers {
		members[member.ID] = member.Path
	}
	var images []api.ScreenshotImage
	for _, id := range requirement.MemberIDs {
		source := members[id]
		if source == "" {
			return nil, errors.New("workflow pack screenshots: projected file is not in the prepared source")
		}
		member := subject
		member.PackMemberID, member.VideoPath = id, source
		member.PackMembers, member.ManualFrames, member.Discs = nil, nil, nil
		member.DefaultCount = requirement.CountPerMember
		if source != subject.VideoPath {
			member.MediaInfoJSONPath, member.HDR, member.HDRFacts = "", "", api.HDRFacts{}
		}
		plan, err := b.screenshots.Plan(ctx, member, requirement.CountPerMember)
		if err != nil {
			return nil, fmt.Errorf("workflow pack screenshots: plan member: %w", err)
		}
		selected := slices.Clone(plan.ExistingScreenshots)
		if len(plan.SuggestedSelections) != 0 {
			result, err := b.screenshots.Capture(ctx, member, plan.SuggestedSelections, api.ScreenshotPurposeFinal)
			if err != nil {
				return nil, fmt.Errorf("workflow pack screenshots: capture member: %w", err)
			}
			selected = append(selected, result.Images...)
		}
		slices.SortStableFunc(selected, func(a, b api.ScreenshotImage) int { return a.Index - b.Index })
		count := 0
		seen := make(map[int]bool)
		for _, image := range selected {
			if strings.TrimSpace(image.Path) == "" || seen[image.Index] {
				continue
			}
			image.PackMemberID = id
			images = append(images, image)
			seen[image.Index] = true
			count++
			if count == requirement.CountPerMember {
				break
			}
		}
		if count != requirement.CountPerMember {
			return nil, errors.New("workflow pack screenshots: an eligible file did not produce the required frames")
		}
	}
	return images, nil
}
