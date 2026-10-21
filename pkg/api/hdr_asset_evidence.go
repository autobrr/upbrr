// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"slices"
	"strings"
)

// HDRArtifactEvidence returns detached selected image associations. Final editor
// output may reuse retained links only for the same exact selected artifacts.
func (s UploadSubject) HDRArtifactEvidence(tracker string) []HDRArtifactEvidence {
	retained := s.TrackerHDRArtifacts[tracker]
	if s.ExactMedia != nil && s.ExactMedia.validateHDRAssets() != nil {
		return nil
	}
	if s.ExactMedia != nil && s.ExactMedia.HDRAnalysis != nil {
		exact := validationHDRArtifacts(s.ExactMedia, tracker)
		if _, final := s.TrackerDescriptionOverride(tracker); !final || !sameHDRArtifactSelection(exact, retained) {
			return exact
		}
	}
	return cloneTrackerValidationValue(retained)
}

func sameHDRArtifactSelection(exact, retained []HDRArtifactEvidence) bool {
	return len(exact) > 0 && len(exact) == len(retained) && slices.EqualFunc(exact, retained, func(a, b HDRArtifactEvidence) bool {
		return a.Analysis == b.Analysis && a.ArtifactID == b.ArtifactID && a.Kind == b.Kind && a.TargetID == b.TargetID &&
			a.MeasurementFingerprint == b.MeasurementFingerprint && a.RenderFingerprint == b.RenderFingerprint &&
			a.GenerationComplete == b.GenerationComplete
	})
}

// validationHDRArtifacts projects only exact selected analysis images and their
// tracker-scoped hosted variants. An image URL is evidence of inclusion, not quality.
func validationHDRArtifacts(exact *ExactMediaAssets, tracker string) []HDRArtifactEvidence {
	if exact == nil || exact.HDRAnalysis == nil || exact.HDRAnalysis.ID == "" || exact.HDRAnalysis.Revision == 0 || exact.validateHDRAssets() != nil {
		return nil
	}
	host := strings.TrimSpace(exact.HDRUploadHosts[tracker])
	var result []HDRArtifactEvidence
	for _, plot := range exact.HDRPlots {
		if plot.ArtifactID == "" || plot.TargetID == "" || plot.Image.Purpose != ScreenshotPurposeHDRAnalysis {
			continue
		}
		evidence := HDRArtifactEvidence{
			GenerationComplete:     plot.GenerationComplete,
			Analysis:               *exact.HDRAnalysis,
			ArtifactID:             plot.ArtifactID,
			Kind:                   plot.Kind,
			TargetID:               plot.TargetID,
			MeasurementFingerprint: plot.MeasurementFingerprint,
			RenderFingerprint:      plot.RenderFingerprint,
		}
		if host != "" {
			for _, scope := range []string{"tracker:" + tracker, "global"} {
				for _, upload := range exact.HDRUploads {
					uploadScope := strings.TrimSpace(upload.UsageScope)
					if uploadScope == "" {
						uploadScope = "global"
					}
					if upload.ImagePath != plot.Image.Path || !strings.EqualFold(upload.Host, host) ||
						!strings.EqualFold(uploadScope, scope) || upload.Purpose != ScreenshotPurposeHDRAnalysis {
						continue
					}
					imageURL := strings.TrimSpace(upload.RawURL)
					if imageURL == "" {
						imageURL = strings.TrimSpace(upload.ImgURL)
					}
					if imageURL != "" {
						evidence.ImageURLs = []string{imageURL}
					}
					break
				}
				if len(evidence.ImageURLs) > 0 {
					break
				}
			}
		}
		result = append(result, evidence)
	}
	return result
}

// ArtifactGenerationComplete reports completed source extraction or measured
// decoding for this exact artifact. Check verdicts do not establish completion.
func (t HDRAnalysisTargetResult) ArtifactGenerationComplete(artifact HDRTypedArtifact) bool {
	if t.Artifact != nil {
		return t.Status == StageStatusCompleted && artifact.ID == t.Artifact.ID && artifact.Kind == HDRArtifactHDR10PlusPlot
	}
	if !slices.Contains(t.Artifacts, artifact) {
		return false
	}
	mode := artifact.Kind.DecodedMode()
	if mode == "" {
		return slices.ContainsFunc(t.Formats, func(format HDRFormatResult) bool {
			return format.Format == artifact.Format && format.State == HDRExtractionComplete
		})
	}
	return slices.ContainsFunc(t.Decoded, func(decoded HDRDecodedResult) bool {
		if decoded.Mode != mode || decoded.State != HDRDecodedComplete || decoded.Measured <= 0 || !slices.Contains(decoded.Artifacts, artifact.ID) {
			return false
		}
		if artifact.Profile == HDRAnalysisProfileV3 && mode == HDRDecodedAudio {
			return slices.ContainsFunc(decoded.AudioTracks, func(track HDRAudioTrackResult) bool {
				return track.TrackID == artifact.AudioTrackID && track.State == HDRDecodedComplete && track.PCMMeasured &&
					slices.Contains(track.Artifacts, artifact.ID)
			})
		}
		return true
	})
}
