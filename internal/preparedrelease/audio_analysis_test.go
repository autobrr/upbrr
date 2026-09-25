// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"errors"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveAudioAnalysisSubjectMapsStaleGenerationToTypedFailure(t *testing.T) {
	t.Parallel()

	module := &Module{envelopes: make(map[string]envelope)}
	_, err := module.ResolveAudioAnalysisSubject(t.Context(), api.AudioAnalysisInstructions{
		Release:        api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 2},
		ResourceID:     "resource-1",
		Selection:      api.AudioAnalysisSelectionPrimary,
		TrackIDs:       []string{"track-1"},
		Variants:       []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
		ProfileVersion: api.AudioAnalysisProfileVersion,
	})
	if err == nil {
		t.Fatal("expected stale prepared generation")
	}
	failure, ok := api.AsAudioAnalysisFailure(err)
	if !ok || failure.Code != api.AudioAnalysisFailureStaleSource {
		t.Fatalf("audio failure = %#v, %v", failure, err)
	}
	var stale *StalePreparationError
	if !errors.As(err, &stale) || stale.Reason != StaleReasonGeneration {
		t.Fatalf("stale cause = %v", err)
	}
}

func TestValidateAudioAnalysisSelectionRequiresExactPreparedOrderAndPrimaryIdentity(t *testing.T) {
	t.Parallel()

	tracks := []api.MediaTrackFacts{
		{
			ID:         "track-1",
			Kind:       api.MediaTrackAudio,
			ResourceID: "resource-1",
			Ordinal:    1,
		},
		{
			ID:         "track-2",
			Kind:       api.MediaTrackAudio,
			ResourceID: "resource-1",
			Ordinal:    2,
		},
		{
			ID:         "track-3",
			Kind:       api.MediaTrackAudio,
			ResourceID: "resource-1",
			Ordinal:    3,
		},
	}
	base := api.AudioAnalysisInstructions{
		Release:        api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1},
		ResourceID:     "resource-1",
		Selection:      api.AudioAnalysisSelectionSelected,
		TrackIDs:       []string{"track-1", "track-3"},
		Variants:       []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
		ProfileVersion: api.AudioAnalysisProfileVersion,
	}
	if err := validateAudioAnalysisSelection(base, tracks, "track-2"); err != nil {
		t.Fatalf("ordered explicit selection: %v", err)
	}

	for _, test := range []struct {
		name       string
		selection  api.AudioAnalysisSelectionMode
		trackIDs   []string
		primaryID  string
		wantReject bool
	}{
		{
			name:      "authoritative primary",
			selection: api.AudioAnalysisSelectionPrimary,
			trackIDs:  []string{"track-2"},
			primaryID: "track-2",
		},
		{
			name:       "missing primary",
			selection:  api.AudioAnalysisSelectionPrimary,
			trackIDs:   []string{"track-1"},
			wantReject: true,
		},
		{
			name:       "wrong primary",
			selection:  api.AudioAnalysisSelectionPrimary,
			trackIDs:   []string{"track-1"},
			primaryID:  "track-2",
			wantReject: true,
		},
		{
			name:      "all",
			selection: api.AudioAnalysisSelectionAll,
			trackIDs:  []string{"track-1", "track-2", "track-3"},
		},
		{
			name:       "all omits one",
			selection:  api.AudioAnalysisSelectionAll,
			trackIDs:   []string{"track-1", "track-3"},
			wantReject: true,
		},
		{
			name:       "reverse explicit",
			selection:  api.AudioAnalysisSelectionSelected,
			trackIDs:   []string{"track-3", "track-1"},
			wantReject: true,
		},
		{
			name:       "unknown explicit",
			selection:  api.AudioAnalysisSelectionSelected,
			trackIDs:   []string{"track-4"},
			wantReject: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			instructions := base
			instructions.Selection = test.selection
			instructions.TrackIDs = test.trackIDs
			err := validateAudioAnalysisSelection(instructions, tracks, test.primaryID)
			if (err != nil) != test.wantReject {
				t.Fatalf("selection error = %v, wantReject=%t", err, test.wantReject)
			}
		})
	}
}
