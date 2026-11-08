// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCLIHDRResumeReportsMixedRetainedOutcomes(t *testing.T) {
	current := releaseworkflow.CommandResult{
		Workflow: api.ReleaseWorkflow{ID: "workflow-hdr-output"},
		HDRAnalysis: &api.HDRAnalysisResult{
			AttemptID: "attempt-retained",
			Status:    api.StageStatusPartial,
			Targets: []api.HDRAnalysisTargetResult{
				{
					TargetID: "target-primary",
					Label:    "Primary video",
					Status:   api.StageStatusPartial,
					Formats: []api.HDRFormatResult{
						{
							Format: api.HDRNativeHDR10Plus,
							State:  api.HDRExtractionComplete,
							Checks: []api.HDRCheckResult{
								{
									Code:    "native_ready",
									State:   api.HDRCheckPass,
									Message: "Native metadata verified.",
								},
								{
									Code:    "plot_unavailable",
									State:   api.HDRCheckUnsupported,
									Message: "The plot could not be rendered; retained metadata remains available for retry. token=synthetic-secret\x1b[2J",
								},
							},
						},
						{
							Format:  api.HDRNativeDolbyVision,
							State:   api.HDRExtractionFailed,
							Failure: &api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureRead, Message: "Native metadata could not be read. token=synthetic-secret\x1b[2J"},
						},
					},
					Decoded: []api.HDRDecodedResult{
						{
							Mode:  api.HDRDecodedAudio,
							State: api.HDRDecodedFailed,
							AudioTracks: []api.HDRAudioTrackResult{{
								Ordinal: 2,
								State:   api.HDRDecodedFailed,
								Failure: &api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureDecode, Message: "Audio decoder could not read the source; restore access before retrying."},
							}},
						},
						{
							Mode:   api.HDRDecodedSceneSync,
							State:  api.HDRDecodedCanceled,
							Reason: "canceled",
						},
					},
					Artifacts: []api.HDRTypedArtifact{{ID: "artifact-retained", Kind: api.HDRArtifactPicturePlot}},
				},
				{
					TargetID: "target-secondary",
					Label:    "Secondary video",
					Status:   api.StageStatusFailed,
					Failure:  &api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureResourceUnavailable, Message: "Source unavailable; restore access before retrying."},
				},
			},
		},
	}
	for _, presentation := range []string{"plain", "tui"} {
		t.Run(presentation, func(t *testing.T) {
			var output strings.Builder
			bridge := newCLITUIBridge()
			if presentation == "tui" {
				printCLIHDRResult(&cliPresentationWriter{Writer: &output, presenter: bridge}, current)
			} else {
				printCLIHDRResult(&output, current)
			}
			text := output.String()
			if presentation == "tui" {
				text = bridge.summary(nil)
			}
			for _, want := range []string{
				"Primary video", "Secondary video", "hdr10plus", "dolby_vision", "Native metadata could not be read.",
				"The plot could not be rendered; retained metadata remains available for retry.",
				"Audio track 2", "Audio decoder could not read the source; restore access before retrying.", "Source unavailable; restore access before retrying.",
				"HDR artifact: artifact-retained · picture_brightness_plot",
			} {
				if !strings.Contains(text, want) {
					t.Errorf("retained HDR report missing %q", want)
				}
			}
			if strings.ContainsAny(text, "\x1b\r") || strings.Contains(text, "synthetic-secret") {
				t.Error("retained HDR report was not sanitized")
			}
			if strings.Contains(text, "Native metadata verified.") {
				t.Error("retained HDR report repeated a passing native check")
			}
		})
	}
}
