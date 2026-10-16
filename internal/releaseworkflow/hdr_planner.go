// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/autobrr/upbrr/pkg/api"
)

func requestedHDRInstructions(current CommandResult, request *api.HDRAnalysisRequest) (api.HDRAnalysisInstructions, error) {
	normalized, err := request.Normalize()
	if err != nil || current.Release == nil {
		return api.HDRAnalysisInstructions{}, ErrInvalidTransition
	}
	ids := normalized.TargetIDs
	if len(ids) == 0 {
		for _, target := range current.Release.Display.HDRTargets {
			if target.Supported {
				ids = append(ids, target.ID)
			}
		}
		if len(ids) != 1 {
			message := "select the prepared HDR targets explicitly"
			if len(ids) == 0 {
				message = "no prepared HDR targets are eligible"
				for _, target := range current.Release.Display.HDRTargets {
					if target.Reason != "" {
						message = "no prepared HDR targets are eligible: " + target.Reason
						break
					}
				}
			}
			return api.HDRAnalysisInstructions{}, fmt.Errorf("resolve HDR request: %w", api.NewHDRAnalysisError(
				api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureInvalidSelection, Message: message},
				nil,
			))
		}
	}
	instructions, err := (api.HDRAnalysisInstructions{
		Release:    api.ReleaseRef{SourcePath: current.Release.Release.Source.SourcePath, Generation: current.Release.Release.Generation},
		TargetIDs:  ids,
		PeakSource: normalized.PeakSource,
		Formats:    normalized.Formats,
		Decoded:    normalized.Decoded,
	}).Normalize()
	if err != nil {
		return api.HDRAnalysisInstructions{}, fmt.Errorf("normalize HDR request: %w", err)
	}
	return instructions, nil
}

func requestedHDRMatches(current CommandResult, instructions api.HDRAnalysisInstructions) bool {
	result := current.HDRAnalysis
	return result != nil && result.Release == instructions.Release && result.PeakSource == instructions.PeakSource &&
		result.ProfileVersion == instructions.ProfileVersion && result.Formats == instructions.Formats &&
		slices.Equal(result.TargetIDs, instructions.TargetIDs) && reflect.DeepEqual(result.Decoded, instructions.Decoded)
}

func requestedHDRUsable(result *api.HDRAnalysisResult) bool {
	if result == nil || result.Status == api.StageStatusCanceled || result.Status == api.StageStatusInterrupted {
		return false
	}
	if !api.IsNativeHDRProfile(result.ProfileVersion) {
		return result.Status == api.StageStatusCompleted
	}
	if result.Status != api.StageStatusCompleted && result.Status != api.StageStatusPartial {
		return false
	}
	for _, target := range result.Targets {
		usable := false
		for _, format := range target.Formats {
			usable = usable || format.State == api.HDRExtractionComplete
		}
		if !usable && result.Decoded != nil && result.Decoded.Requested(api.HDRDecodedAudio) {
			usable = len(target.AudioStatisticsEntries()) > 0
			usable = usable || slices.ContainsFunc(target.DescriptionImages(), func(artifact api.HDRTypedArtifact) bool {
				return artifact.Kind.DecodedMode() == api.HDRDecodedAudio
			})
		}
		if !usable {
			return false
		}
	}
	return len(result.Targets) > 0
}

func requestedHDRFailure(stage string, current CommandResult, request *api.HDRAnalysisRequest) error {
	if stage == "hdr-selection-required" {
		_, err := requestedHDRInstructions(current, request)
		return err
	}
	return fmt.Errorf("requested HDR stage: %w", api.NewHDRAnalysisError(
		api.HDRAnalysisFailure{
			Code:    api.HDRAnalysisFailureIncomplete,
			Message: "requested HDR targets need usable native metadata or requested primary-audio outputs; retry, select fewer targets, or disable HDR inclusion",
		},
		nil,
	))
}
