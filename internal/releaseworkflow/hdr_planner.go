// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"fmt"
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
	}).Normalize()
	if err != nil {
		return api.HDRAnalysisInstructions{}, fmt.Errorf("normalize HDR request: %w", err)
	}
	return instructions, nil
}

func requestedHDRMatches(current CommandResult, instructions api.HDRAnalysisInstructions) bool {
	result := current.HDRAnalysis
	return result != nil && result.Release == instructions.Release && result.PeakSource == instructions.PeakSource &&
		slices.Equal(result.TargetIDs, instructions.TargetIDs)
}

func requestedHDRFailure(stage string, current CommandResult, request *api.HDRAnalysisRequest) error {
	if stage == "hdr-selection-required" {
		_, err := requestedHDRInstructions(current, request)
		return err
	}
	return fmt.Errorf("requested HDR stage: %w", api.NewHDRAnalysisError(
		api.HDRAnalysisFailure{
			Code:    api.HDRAnalysisFailureIncomplete,
			Message: "every requested HDR target must complete; retry, select fewer targets, or disable HDR inclusion",
		},
		nil,
	))
}
