// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"reflect"
	"slices"

	"github.com/autobrr/upbrr/pkg/api"
)

func remapHDRImages(selection *api.HDRImageSelection, prior, current *api.HDRAnalysisResult) *api.HDRImageSelection {
	if selection == nil {
		return nil
	}
	result := &api.HDRImageSelection{ArtifactIDs: []api.PublicResourceID{}}
	for _, id := range selection.ArtifactIDs {
		var matches []api.PublicResourceID
		if current != nil {
			for _, target := range current.Targets {
				for _, image := range target.SelectableImages() {
					if image.ID == id {
						matches = append(matches, id)
					}
				}
			}
		}
		if len(matches) == 0 && prior != nil && current != nil && prior.Release.SourcePath == current.Release.SourcePath &&
			prior.ManifestFingerprint == current.ManifestFingerprint {
			for _, before := range prior.Targets {
				for _, image := range before.SelectableImages() {
					if image.ID != id {
						continue
					}
					for _, after := range current.Targets {
						if after.TargetID != before.TargetID {
							continue
						}
						for _, candidate := range after.SelectableImages() {
							if candidate.Kind != image.Kind || candidate.Format != image.Format || candidate.Profile != image.Profile ||
								candidate.AudioTrackID != image.AudioTrackID {
								continue
							}
							if image.Profile == api.HDRAnalysisProfileV3 {
								if image.MeasurementFingerprint == "" || image.RenderFingerprint == "" ||
									candidate.MeasurementFingerprint != image.MeasurementFingerprint ||
									candidate.RenderFingerprint != image.RenderFingerprint {
									continue
								}
							} else if prior.RenderFingerprint != current.RenderFingerprint || !reflect.DeepEqual(prior.Decoded, current.Decoded) {
								continue
							}
							matches = append(matches, candidate.ID)
						}
					}
				}
			}
		}
		if len(matches) == 1 && !slices.Contains(result.ArtifactIDs, matches[0]) {
			result.ArtifactIDs = append(result.ArtifactIDs, matches[0])
		} else {
			result.RecoveryNote = "Some explicitly selected HDR images are unavailable or no longer have a unique compatible measurement and rendering. Select replacement images explicitly."
		}
	}
	return result
}
