// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "testing"

func TestProgrammeAudioOriginEvidence(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		languages             []string
		origin                TrackAudioOrigin
		wantOriginal, wantDub bool
	}{
		{"ordinary matching original", []string{"Japanese"}, "", true, false},
		{"unclassified mismatch", []string{"German"}, "", false, false},
		{"unclassified multilingual overlap", []string{"Japanese", "English"}, "", false, false},
		{"reviewed multilingual original", []string{"German", "English"}, TrackAudioOriginal, true, false},
		{"confirmed English dub", []string{"English"}, TrackAudioDubbed, false, true},
		{"confirmed foreign dub", []string{"German"}, TrackAudioDubbed, false, false},
		{"explicit unresolved original", []string{"Japanese"}, TrackAudioUnresolved, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := LanguageFacts{
				OriginalLanguages:      []string{"Japanese"},
				OriginalLanguagesKnown: true,
				ProgrammeLanguages:     tc.languages,
				ProgrammeStatus:        MetadataEvidenceStatusComplete,
				Tracks: []MediaTrackFacts{{
					Kind:        MediaTrackAudio,
					Role:        AudioRoleProgramme,
					Languages:   tc.languages,
					AudioOrigin: tc.origin,
				}},
			}
			if facts.HasOriginalAudio() != tc.wantOriginal || facts.HasEnglishDub() != tc.wantDub {
				t.Fatalf("original=%v dub=%v", facts.HasOriginalAudio(), facts.HasEnglishDub())
			}
			facts.Tracks[0].Role = AudioRoleCommentary
			if facts.HasOriginalAudio() || facts.HasEnglishDub() {
				t.Fatal("commentary established programme origin")
			}
		})
	}
}

func TestTrackAudioOriginCorrectionIsIndependent(t *testing.T) {
	snapshot := ReleaseCorrectionsSnapshot{Revision: 2, Corrections: StoredReleaseCorrectionsV1{
		Version: 1, Metadata: MetadataOverrides{TrackLanguages: []TrackLanguageCorrection{{
			TrackID:             "audio",
			Languages:           []string{"German", "English"},
			ManifestFingerprint: "current",
		}}},
	}}
	patch := ReleaseCorrectionPatch{ExpectedRevision: new(uint64(2)), Values: ReleaseCorrectionValues{Metadata: MetadataOverrides{
		TrackAudioOrigins: []TrackAudioOriginCorrection{{
			TrackID:             "audio",
			Origin:              TrackAudioOriginal,
			ManifestFingerprint: "current",
		}},
	}}}
	result, err := ApplyReleaseCorrectionUpdate(snapshot, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &patch})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Metadata.TrackLanguages) != 1 || len(result.Metadata.TrackLanguages[0].Languages) != 2 || len(result.Metadata.TrackAudioOrigins) != 1 {
		t.Fatal("origin correction changed language identification")
	}
	snapshot.Corrections = result
	reset := ReleaseCorrectionPatch{ExpectedRevision: new(uint64(2)), ResetFields: []CorrectionFieldRef{{Field: CorrectionFieldMetadataTrackLanguages, TrackID: "audio"}}}
	result, err = ApplyReleaseCorrectionUpdate(snapshot, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &reset})
	if err != nil || len(result.Metadata.TrackAudioOrigins) != 1 || len(result.Metadata.TrackLanguages) != 0 {
		t.Fatalf("language reset changed origin: %v %#v", err, result.Metadata)
	}
	patch.Values.Metadata.TrackAudioOrigins[0].Origin = "invented"
	if patch.Validate() == nil {
		t.Fatal("invalid soundtrack origin accepted")
	}
	patch.Values.Metadata.TrackAudioOrigins[0].Origin = TrackAudioOriginal
	patch.Values.Metadata.TrackAudioOrigins[0].ManifestFingerprint = ""
	if patch.Validate() == nil {
		t.Fatal("unbound soundtrack origin accepted")
	}
}

func TestMultilingualOriginalDoesNotEstablishEnglishDub(t *testing.T) {
	facts := LanguageFacts{
		OriginalLanguages:      []string{"Japanese"},
		OriginalLanguagesKnown: true,
		ProgrammeLanguages:     []string{"Japanese", "English"},
		ProgrammeStatus:        MetadataEvidenceStatusComplete,
		Tracks: []MediaTrackFacts{{
			ID:        "programme",
			Kind:      MediaTrackAudio,
			Role:      AudioRoleProgramme,
			Languages: []string{"Japanese", "English"},
		}},
	}
	if facts.HasEnglishDub() {
		t.Fatal("English dialogue in one multilingual programme track does not establish an English dub")
	}
}

func TestAggregateEnglishDoesNotEstablishEnglishDub(t *testing.T) {
	facts := LanguageFacts{
		OriginalLanguages:      []string{"Japanese"},
		OriginalLanguagesKnown: true,
		ProgrammeLanguages:     []string{"English"},
		ProgrammeStatus:        MetadataEvidenceStatusPartial,
	}
	if facts.HasEnglishDub() {
		t.Fatal("aggregate language evidence without classified tracks does not establish an English dub")
	}
	if !facts.HasUnresolvedProgrammeAudioOrigin() {
		t.Fatal("aggregate-only audio became known soundtrack evidence")
	}
}

func TestClearedProgrammeLanguagesDoNotRecoverFromInspectedTracks(t *testing.T) {
	facts := LanguageFacts{
		OriginalLanguages:      []string{"Japanese"},
		OriginalLanguagesKnown: true,
		ProgrammeStatus:        MetadataEvidenceStatusPartial,
		Tracks: []MediaTrackFacts{
			{
				Kind:      MediaTrackAudio,
				Role:      AudioRoleProgramme,
				Languages: []string{"Japanese"},
			},
			{
				Kind:        MediaTrackAudio,
				Role:        AudioRoleProgramme,
				Languages:   []string{"English"},
				AudioOrigin: TrackAudioDubbed,
			},
		},
	}
	if facts.HasOriginalAudio() || facts.HasEnglishDub() {
		t.Fatal("explicit aggregate clear recovered original/dub evidence from inspected tracks")
	}
}
