// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func (m *Module) descriptionResources(_ context.Context, ownerID string, state *State, media any, now time.Time) (any, error) {
	value, err := m.audioDescriptionResources(ownerID, state, media, now)
	if err != nil {
		return nil, err
	}
	if !state.Workflow.HDRAnalysisEnabled {
		return value, nil
	}
	resources, wrapped := value.(DescriptionResources)
	if !wrapped {
		resources.Media = value
	}
	resources.HDR = func(ctx context.Context) (api.HDRAnalysisResult, RetainedHDRAnalysisResource, error) {
		return m.resolveHDRDescriptionResources(ctx, ownerID, state, now)
	}
	return resources, nil
}

func (m *Module) resolveHDRDescriptionResources(
	ctx context.Context,
	ownerID string,
	state *State,
	now time.Time,
) (api.HDRAnalysisResult, RetainedHDRAnalysisResource, error) {
	ref := state.Workflow.HDRAnalysis
	if ref == nil || state.Workflow.Release == nil {
		return api.HDRAnalysisResult{}, nil, fmt.Errorf("%w: HDR analysis must be generated", ErrInvalidTransition)
	}
	analysis, ok := state.HDRAnalyses[ref.ID]
	release, err := currentReleaseSnapshot(state)
	if err != nil {
		return api.HDRAnalysisResult{}, nil, err
	}
	if !ok || analysis.Revision != ref.Revision || analysis.Status != api.StageStatusCompleted ||
		analysis.Release != (api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation}) {
		return api.HDRAnalysisResult{}, nil, fmt.Errorf("%w: every requested HDR target must complete", ErrInvalidTransition)
	}
	if validator, ok := m.hdrAnalysisBuilder.(HDRAnalysisAuthorityValidator); ok {
		if err := validator.ValidateAuthority(ctx, analysis); err != nil {
			return api.HDRAnalysisResult{}, nil, fmt.Errorf("validate HDR description authority: %w", err)
		}
	}
	retained, err := m.private.Get(ownerID, state.Workflow.ID, hdrAnalysisPrivateResourceID(analysis.AttemptID), now)
	if err != nil {
		return api.HDRAnalysisResult{}, nil, fmt.Errorf("load HDR description authority: %w", err)
	}
	resource, ok := retained.(RetainedHDRAnalysisResource)
	if !ok {
		return api.HDRAnalysisResult{}, nil, ErrPrivateResourceUnavailable
	}
	return analysis, resource, nil
}
