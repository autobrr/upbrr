// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func appendWorkflowHDRAssets(exact *api.ExactMediaAssets, analysis api.HDRAnalysisResult, paths releaseworkflow.RetainedHDRAnalysisResource) error {
	if analysis.ID == "" || analysis.Status != api.StageStatusCompleted || paths == nil {
		return errors.New("complete HDR description resources are unavailable")
	}
	exact.HDRAnalysis = &api.HDRAnalysisRef{ID: analysis.ID, Revision: analysis.Revision}
	for _, target := range analysis.Targets {
		if target.Status != api.StageStatusCompleted || target.Artifact == nil {
			return errors.New("HDR description target is incomplete")
		}
		pathValue, err := paths.LocalArtifactPath(analysis, target.Artifact.ID)
		if err != nil {
			return fmt.Errorf("resolve HDR description plot: %w", err)
		}
		exact.HDRPlots = append(exact.HDRPlots, api.HDRDescriptionPlot{
			TargetID: target.TargetID,
			Label:    target.Label,
			Image: api.ScreenshotImage{
				Path:    pathValue,
				Purpose: api.ScreenshotPurposeHDRAnalysis,
				Width:   target.Artifact.Width,
				Height:  target.Artifact.Height,
			},
		})
	}
	return nil
}

func hdrDescriptionTrackers(subject api.UploadSubject, trackerNames []string) []string {
	var generated []string
	for _, tracker := range trackerNames {
		if source, final := subject.TrackerDescriptionOverride(tracker); !final || strings.TrimSpace(source) == "" {
			generated = append(generated, tracker)
		}
	}
	return generated
}

func resolveWorkflowDescriptionMedia(
	ctx context.Context,
	subject api.UploadSubject,
	privateMedia any,
	media api.MediaArtifactSet,
) (*api.ExactMediaAssets, error) {
	exact, err := resolveWorkflowExactMedia(privateMedia, media)
	if err != nil {
		return nil, err
	}
	resources, ok := privateMedia.(releaseworkflow.DescriptionResources)
	if !ok || resources.HDR == nil || len(hdrDescriptionTrackers(subject, subject.Trackers)) == 0 {
		return exact, nil
	}
	analysis, paths, err := resources.HDR(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve HDR description resources: %w", err)
	}
	if err := appendWorkflowHDRAssets(exact, analysis, paths); err != nil {
		return nil, err
	}
	if err := exact.Validate(); err != nil {
		return nil, fmt.Errorf("validate HDR description resources: %w", err)
	}
	return exact, nil
}

func (b workflowDescriptionBuilder) uploadHDRDescriptionImages(ctx context.Context, subject api.UploadSubject, trackerNames []string) error {
	trackerNames = hdrDescriptionTrackers(subject, trackerNames)
	if len(trackerNames) == 0 {
		return nil
	}
	if subject.ExactMedia == nil || len(subject.ExactMedia.HDRPlots) == 0 {
		return nil
	}
	if b.media == nil {
		return errors.New("HDR image hosting is unavailable")
	}
	images := make([]api.ScreenshotImage, len(subject.ExactMedia.HDRPlots))
	for index, plot := range subject.ExactMedia.HDRPlots {
		images[index] = plot.Image
	}
	targets, err := b.media.resolveImageUploadTargets(ctx, trackerNames, subject, "", nil)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return errors.New("no image host is available for HDR analysis")
	}
	result, err := b.media.uploadImagesToTargetsWithFallback(ctx, subject, "", nil, targets, images, nil, nil)
	if err != nil {
		return err
	}
	subject.ExactMedia.HDRUploads = result.Links
	hosts := make(map[string]string, len(trackerNames))
	for _, attempt := range result.Attempts {
		if attempt.Failure != nil || len(attempt.Links) == 0 {
			continue
		}
		for _, tracker := range attempt.Trackers {
			hosts[strings.ToUpper(strings.TrimSpace(tracker))] = strings.ToLower(strings.TrimSpace(attempt.Host))
		}
	}
	for _, tracker := range trackerNames {
		if hosts[strings.ToUpper(strings.TrimSpace(tracker))] == "" {
			return fmt.Errorf("HDR analysis image host is unavailable for %s", tracker)
		}
	}
	subject.ExactMedia.HDRUploadHosts = hosts
	if err := subject.ExactMedia.Validate(); err != nil {
		return fmt.Errorf("validate hosted HDR assets: %w", err)
	}
	return nil
}
