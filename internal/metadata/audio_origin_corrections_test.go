// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"errors"
	"slices"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestApplyTrackAudioOriginPreservesLanguageEvidence(t *testing.T) {
	meta := preparationstate.State{MediaTracks: []api.MediaTrackFacts{{
		ID:                  "audio",
		Kind:                api.MediaTrackAudio,
		Role:                api.AudioRoleProgramme,
		ResourceID:          "resource",
		ManifestFingerprint: "scan",
		Languages:           []string{"English", "German"},
		DetectedLanguages:   []string{"Multiple Languages"},
		LanguageProvenance:  api.FactProvenanceManual,
	}}}
	correction := api.TrackAudioOriginCorrection{
		TrackID:             "audio",
		Origin:              api.TrackAudioOriginal,
		ManifestFingerprint: "scan",
	}
	if err := applyTrackAudioOriginOverrides(&meta, []api.TrackAudioOriginCorrection{correction}); err != nil {
		t.Fatal(err)
	}
	track := meta.MediaTracks[0]
	if track.AudioOrigin != api.TrackAudioOriginal || track.AudioOriginProvenance != api.FactProvenanceManual ||
		!slices.Equal(track.Languages, []string{"English", "German"}) || !slices.Equal(track.DetectedLanguages, []string{"Multiple Languages"}) ||
		track.ResourceID != "resource" || track.LanguageProvenance != api.FactProvenanceManual {
		t.Fatalf("origin correction changed independent facts: %#v", track)
	}
	correction.ManifestFingerprint = "old"
	if err := applyTrackAudioOriginOverrides(&meta, []api.TrackAudioOriginCorrection{correction}); !errors.Is(err, api.ErrCorrectionConflict) {
		t.Fatalf("stale manifest error: %v", err)
	}
	correction.ManifestFingerprint = "scan"
	meta.MediaTracks[0].Role = api.AudioRoleCommentary
	if err := applyTrackAudioOriginOverrides(&meta, []api.TrackAudioOriginCorrection{correction}); !errors.Is(err, api.ErrCorrectionConflict) {
		t.Fatalf("secondary role error: %v", err)
	}
}
