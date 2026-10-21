// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func appendWorkflowHDRAssets(
	ctx context.Context,
	exact *api.ExactMediaAssets,
	analysis api.HDRAnalysisResult,
	paths releaseworkflow.RetainedHDRAnalysisResource,
) error {
	if analysis.ID == "" || !analysis.CanIncludeImages() || paths == nil {
		return errors.New("complete HDR description resources are unavailable")
	}
	exact.HDRAnalysis = &api.HDRAnalysisRef{ID: analysis.ID, Revision: analysis.Revision}
	for _, target := range analysis.Targets {
		if !api.IsNativeHDRProfile(analysis.ProfileVersion) && (target.Status != api.StageStatusCompleted || target.Artifact == nil) {
			return errors.New("HDR description target is incomplete")
		}
		for _, artifact := range target.AudioStatisticsEntries() {
			label, err := hdrAudioDescriptionLabel(target, artifact)
			if err != nil {
				return err
			}
			content, err := paths.OpenArtifact(ctx, analysis, artifact.ID)
			if err != nil {
				return fmt.Errorf("open HDR audio statistics: %w", err)
			}
			data, readErr := io.ReadAll(io.LimitReader(content.Body, api.AudioAnalysisStatsMaxBytes+1))
			if err := errors.Join(readErr, content.Body.Close()); err != nil {
				return fmt.Errorf("read HDR audio statistics: %w", err)
			}
			if content.ContentType != "text/plain" || len(data) > api.AudioAnalysisStatsMaxBytes {
				return releaseworkflow.ErrPrivateResourceIntegrity
			}
			exact.HDRStats = append(exact.HDRStats, api.HDRDescriptionStats{
				ArtifactID:             artifact.ID,
				TargetID:               target.TargetID,
				Label:                  target.Label + " — " + label + " amplitude statistics",
				Text:                   string(data),
				AudioTrackID:           artifact.AudioTrackID,
				MeasurementFingerprint: artifact.MeasurementFingerprint,
				RenderFingerprint:      artifact.RenderFingerprint,
			})
		}
		// The workflow projection has already applied exact image selection.
		for _, artifact := range target.SelectableImages() {
			label := target.Label
			if api.IsNativeHDRProfile(analysis.ProfileVersion) {
				mode := artifact.Kind.DecodedMode()
				switch {
				case mode == api.HDRDecodedAudio:
					audioLabel, err := hdrAudioDescriptionLabel(target, artifact)
					if err != nil {
						return err
					}
					label += " — " + audioLabel + " " + strings.ReplaceAll(string(artifact.Kind), "_", " ")
				case mode != "":
					label += " — HDR " + string(mode) + " evidence"
				case artifact.Format == api.HDRNativeDolbyVision:
					label += " — Dolby Vision L1 metadata"
				default:
					label += " — HDR10+ metadata"
				}
			}
			pathValue, err := paths.LocalArtifactPath(analysis, artifact.ID)
			if err != nil {
				return fmt.Errorf("resolve HDR description plot: %w", err)
			}
			exact.HDRPlots = append(exact.HDRPlots, api.HDRDescriptionPlot{
				GenerationComplete:     target.ArtifactGenerationComplete(artifact),
				ArtifactID:             artifact.ID,
				Format:                 artifact.Format,
				Kind:                   artifact.Kind,
				TargetID:               target.TargetID,
				Label:                  label,
				AudioTrackID:           artifact.AudioTrackID,
				MeasurementFingerprint: artifact.MeasurementFingerprint,
				RenderFingerprint:      artifact.RenderFingerprint,
				Image: api.ScreenshotImage{
					Path:    pathValue,
					Purpose: api.ScreenshotPurposeHDRAnalysis,
					Width:   artifact.Width,
					Height:  artifact.Height,
				},
			})
		}
	}
	return nil
}

func hdrAudioDescriptionLabel(target api.HDRAnalysisTargetResult, artifact api.HDRTypedArtifact) (string, error) {
	if artifact.Profile != api.HDRAnalysisProfileV3 {
		return "Primary audio", nil
	}
	for _, result := range target.Decoded {
		for _, track := range result.AudioTracks {
			if result.Mode == api.HDRDecodedAudio && track.TrackID == artifact.AudioTrackID && track.PCMMeasured &&
				slices.Contains(track.Artifacts, artifact.ID) {
				parts := []string{fmt.Sprintf("Audio track %d", track.Ordinal)}
				for _, value := range []string{track.Language, track.Codec, track.ChannelLayout} {
					if value != "" {
						parts = append(parts, value)
					}
				}
				return strings.Join(parts, " · "), nil
			}
		}
	}
	return "", releaseworkflow.ErrPrivateResourceIntegrity
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
	if err := appendWorkflowHDRAssets(ctx, exact, analysis, paths); err != nil {
		return nil, err
	}
	if err := exact.Validate(); err != nil {
		return nil, fmt.Errorf("validate HDR description resources: %w", err)
	}
	return exact, nil
}

// uploadHDRDescriptionImages fills exact HDR uploads and per-tracker host choices
// for generated descriptions. Explicit skip permits missing hosts and reuses
// compatible cached links without uploading; final description overrides are excluded.
func (b workflowDescriptionBuilder) uploadHDRDescriptionImages(ctx context.Context, subject api.UploadSubject, trackerNames []string) error {
	trackerNames = hdrDescriptionTrackers(subject, trackerNames)
	if len(trackerNames) == 0 {
		return nil
	}
	if subject.ExactMedia == nil || len(subject.ExactMedia.HDRPlots) == 0 {
		return nil
	}
	if b.media == nil {
		if subject.ExactMedia.ImageHostUploadSkipped {
			return nil
		}
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
		if subject.ExactMedia.ImageHostUploadSkipped {
			return nil
		}
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
		if hosts[strings.ToUpper(strings.TrimSpace(tracker))] == "" && !subject.ExactMedia.ImageHostUploadSkipped {
			return fmt.Errorf("HDR analysis image host is unavailable for %s", tracker)
		}
	}
	subject.ExactMedia.HDRUploadHosts = hosts
	if err := subject.ExactMedia.Validate(); err != nil {
		return fmt.Errorf("validate hosted HDR assets: %w", err)
	}
	return nil
}

// retainWorkflowHDRDescriptionEvidence carries only already validated, path-free
// associations for final editor output; generated lanes use their exact assets.
func retainWorkflowHDRDescriptionEvidence(subject *api.UploadSubject, privateMedia any) {
	resources, ok := privateMedia.(releaseworkflow.DescriptionResources)
	if !ok || len(resources.HDRArtifacts) == 0 {
		return
	}
	subject.TrackerHDRArtifacts = make(map[string][]api.HDRArtifactEvidence)
	for tracker, artifacts := range resources.HDRArtifacts {
		if _, final := subject.TrackerDescriptionOverride(string(tracker)); final {
			retained := api.UploadSubject{TrackerHDRArtifacts: map[string][]api.HDRArtifactEvidence{string(tracker): artifacts}}
			subject.TrackerHDRArtifacts[string(tracker)] = retained.HDRArtifactEvidence(string(tracker))
		}
	}
}
