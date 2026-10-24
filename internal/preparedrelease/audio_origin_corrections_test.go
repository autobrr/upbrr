// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"errors"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAudioOriginCorrectionRequiresCurrentProgrammeManifest(t *testing.T) {
	current := api.PreparedRelease{}
	current.Compatibility.SourceFingerprint = "source"
	current.Media.Tracks = []api.MediaTrackFacts{{
		ID:                  "audio",
		Kind:                api.MediaTrackAudio,
		Role:                api.AudioRoleProgramme,
		ManifestFingerprint: "scan",
	}}
	correction := api.TrackAudioOriginCorrection{
		TrackID:             "audio",
		Origin:              api.TrackAudioOriginal,
		ManifestFingerprint: "scan",
	}
	if err := validateTrackAudioOriginCorrections([]api.TrackAudioOriginCorrection{correction}, current, "source"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, source, trackID, manifest string
		role                            api.AudioTrackRole
	}{
		{"source changed", "new-source", "audio", "scan", api.AudioRoleProgramme},
		{"track changed", "source", "other", "scan", api.AudioRoleProgramme},
		{"manifest changed", "source", "audio", "old-scan", api.AudioRoleProgramme},
		{"secondary track", "source", "audio", "scan", api.AudioRoleCommentary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current.Media.Tracks[0].Role = tc.role
			correction.TrackID, correction.ManifestFingerprint = tc.trackID, tc.manifest
			err := validateTrackAudioOriginCorrections([]api.TrackAudioOriginCorrection{correction}, current, tc.source)
			var conflict *api.CorrectionConflictError
			if !errors.As(err, &conflict) || conflict.Field != api.CorrectionFieldMetadataTrackAudioOrigins {
				t.Fatalf("invalid origin target error: %v", err)
			}
		})
	}
}

func TestPrepareRecollectsBeforeAudioOriginSemantics(t *testing.T) {
	path := writePreparedTestFile(t, "Synthetic.2026.mkv", "source")
	store := newMemoryStore()
	input := api.PrepareInput{SourcePath: path}
	previous, err := newTestModule(t, store, &recordingCollector{}).Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	stale := previous.Release
	stale.Compatibility.ContractVersion = "prepared-release-v43"
	store.mu.Lock()
	store.current[canonicalSourceKey(path)] = stale
	store.mu.Unlock()
	collector := &clientEvidenceTestCollector{base: recordingCollector{}}
	current, err := newTestModule(t, store, collector).Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if collector.collectCount() != 1 || current.Release.Generation != previous.Release.Generation+1 || current.Release.Compatibility.ContractVersion != ContractVersion {
		t.Fatal("old original-versus-dub classification reused")
	}
}

func TestAudioOriginCorrectionsChangePreparationFingerprintIndependently(t *testing.T) {
	base := api.PrepareInput{Instructions: api.ReleaseFactInstructions{Metadata: api.MetadataOverrides{
		TrackLanguages: []api.TrackLanguageCorrection{{
			TrackID:             "audio",
			Languages:           []string{"English", "Japanese"},
			ManifestFingerprint: "scan",
		}},
	}}}
	seen := map[string]bool{}
	for _, origin := range []api.TrackAudioOrigin{"", api.TrackAudioOriginal, api.TrackAudioDubbed, api.TrackAudioUnresolved} {
		input := base
		if origin != "" {
			input.Instructions.Metadata.TrackAudioOrigins = []api.TrackAudioOriginCorrection{{
				TrackID:             "audio",
				Origin:              origin,
				ManifestFingerprint: "scan",
			}}
		}
		compatibility, err := preparationCompatibility(input, "source", 0)
		if err != nil {
			t.Fatal(err)
		}
		if seen[compatibility.FactInstructionFingerprint] {
			t.Fatal("soundtrack-origin edits reused preparation evidence")
		}
		seen[compatibility.FactInstructionFingerprint] = true
	}
}
