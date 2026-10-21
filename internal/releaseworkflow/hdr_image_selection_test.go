// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRImageRemapRequiresUniqueTrackMeasurementAndRender(t *testing.T) {
	makeResult := func(images ...api.HDRTypedArtifact) *api.HDRAnalysisResult {
		decoded := api.HDRDecodedResult{
			Mode:     api.HDRDecodedAudio,
			State:    api.HDRDecodedComplete,
			Measured: 1,
		}
		for _, image := range images {
			decoded.Artifacts = append(decoded.Artifacts, image.ID)
			decoded.AudioTracks = append(decoded.AudioTracks, api.HDRAudioTrackResult{
				TrackID:     image.AudioTrackID,
				Ordinal:     1,
				PCMMeasured: true,
				State:       api.HDRDecodedComplete,
				Artifacts:   []api.PublicResourceID{image.ID},
			})
		}
		return &api.HDRAnalysisResult{
			Release:             api.ReleaseRef{SourcePath: "Synthetic.mkv"},
			ManifestFingerprint: "manifest",
			Targets: []api.HDRAnalysisTargetResult{{
				TargetID:  "target",
				Artifacts: images,
				Decoded:   []api.HDRDecodedResult{decoded},
			}},
		}
	}
	before := api.HDRTypedArtifact{
		ID:                     "old",
		Kind:                   api.HDRArtifactAudioWaveform,
		Profile:                api.HDRAnalysisProfileV3,
		MIME:                   "image/png",
		AudioTrackID:           "main",
		MeasurementFingerprint: strings.Repeat("1", 64),
		RenderFingerprint:      strings.Repeat("2", 64),
	}
	after := before
	after.ID = "new"
	prior := makeResult(before)
	selection := &api.HDRImageSelection{ArtifactIDs: []api.PublicResourceID{"old"}}
	if got := remapHDRImages(nil, prior, makeResult(after)); got != nil {
		t.Fatal("default selection became explicit")
	}
	if got := remapHDRImages(&api.HDRImageSelection{ArtifactIDs: []api.PublicResourceID{}}, prior, makeResult(after)); got == nil || got.ArtifactIDs == nil || len(got.ArtifactIDs) != 0 {
		t.Fatal("explicit empty selection became defaults")
	}
	if got := remapHDRImages(selection, prior, makeResult(after)); !slices.Equal(got.ArtifactIDs, []api.PublicResourceID{"new"}) || got.RecoveryNote != "" {
		t.Fatalf("unique exact binding not restored: %+v", got)
	}
	for _, field := range []string{"track", "measurement", "render", "ambiguous", "missing"} {
		t.Run(field, func(t *testing.T) {
			candidate := after
			switch field {
			case "track":
				candidate.AudioTrackID = "alternate"
			case "measurement":
				candidate.MeasurementFingerprint = strings.Repeat("3", 64)
			case "render":
				candidate.RenderFingerprint = strings.Repeat("4", 64)
			}
			current := makeResult(candidate)
			if field == "ambiguous" {
				candidate.ID = "also-new"
				current = makeResult(after, candidate)
			}
			if field == "missing" {
				current = makeResult()
			}
			if got := remapHDRImages(selection, prior, current); len(got.ArtifactIDs) != 0 || got.RecoveryNote == "" {
				t.Fatalf("incompatible or ambiguous selection broadened: %+v", got)
			}
		})
	}
}
