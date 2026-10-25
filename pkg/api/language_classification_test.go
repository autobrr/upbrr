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

func TestEnglishDubInferredFromSeparateOriginalTrack(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*LanguageFacts)
		wantDub bool
	}{
		{name: "original plus separate English", wantDub: true},
		{
			name:    "original follows English",
			mutate:  func(f *LanguageFacts) { f.Tracks[0], f.Tracks[1] = f.Tracks[1], f.Tracks[0] },
			wantDub: true,
		},
		{
			name:    "partial pack coverage",
			mutate:  func(f *LanguageFacts) { f.ProgrammeStatus = MetadataEvidenceStatusPartial },
			wantDub: true,
		},
		{
			name:    "legacy single resource",
			mutate:  func(f *LanguageFacts) { f.Tracks[0].ResourceID, f.Tracks[1].ResourceID = "", "" },
			wantDub: true,
		},
		{name: "different resources", mutate: func(f *LanguageFacts) { f.Tracks[0].ResourceID = "other-file" }},
		{name: "missing original", mutate: func(f *LanguageFacts) { f.Tracks = f.Tracks[1:] }},
		{name: "unknown original language", mutate: func(f *LanguageFacts) { f.OriginalLanguagesKnown = false }},
		{name: "English original", mutate: func(f *LanguageFacts) { f.OriginalLanguages = []string{"English"} }},
		{name: "non-spoken original", mutate: func(f *LanguageFacts) { f.OriginalLanguages = []string{"ZXX"} }},
		{name: "explicit English original", mutate: func(f *LanguageFacts) { f.Tracks[1].AudioOrigin = TrackAudioOriginal }},
		{name: "explicit unresolved English", mutate: func(f *LanguageFacts) { f.Tracks[1].AudioOrigin = TrackAudioUnresolved }},
		{name: "explicit unresolved original", mutate: func(f *LanguageFacts) { f.Tracks[0].AudioOrigin = TrackAudioUnresolved }},
		{name: "English-only original witness", mutate: func(f *LanguageFacts) {
			f.Tracks[0].Languages, f.Tracks[0].AudioOrigin = []string{"English"}, TrackAudioOriginal
			f.ProgrammeLanguages = []string{"English"}
		}},
		{name: "English-only original alternate mix", mutate: func(f *LanguageFacts) {
			f.Tracks[0].Languages, f.Tracks[0].AudioOrigin, f.Tracks[0].Role = []string{"English"}, TrackAudioOriginal, AudioRoleAlternateMix
			f.ProgrammeLanguages = []string{"English"}
		}},
		{name: "unknown-language original witness", mutate: func(f *LanguageFacts) {
			f.Tracks[0].Languages, f.Tracks[0].AudioOrigin = []string{"und"}, TrackAudioOriginal
			f.ProgrammeLanguages, f.ProgrammeStatus = []string{"English", "und"}, MetadataEvidenceStatusPartial
		}},
		{name: "cleared-language original witness", mutate: func(f *LanguageFacts) {
			f.Tracks[0].Languages, f.Tracks[0].AudioOrigin = nil, TrackAudioOriginal
			f.ProgrammeLanguages, f.ProgrammeStatus = []string{"English"}, MetadataEvidenceStatusPartial
		}},
		{
			name: "reviewed multilingual original witness",
			mutate: func(f *LanguageFacts) {
				f.Tracks[0].Languages, f.Tracks[0].AudioOrigin = []string{"German", "English"}, TrackAudioOriginal
				f.ProgrammeLanguages = []string{"English", "German"}
			},
			wantDub: true,
		},
		{
			name: "reviewed different-language original witness",
			mutate: func(f *LanguageFacts) {
				f.Tracks[0].Languages, f.Tracks[0].AudioOrigin = []string{"German"}, TrackAudioOriginal
				f.ProgrammeLanguages = []string{"English", "German"}
			},
			wantDub: true,
		},
		{
			name: "reviewed different-language original in partial pack",
			mutate: func(f *LanguageFacts) {
				f.Tracks[0].Languages, f.Tracks[0].AudioOrigin = []string{"German"}, TrackAudioOriginal
				f.ProgrammeLanguages, f.ProgrammeStatus = []string{"English", "German"}, MetadataEvidenceStatusPartial
			},
			wantDub: true,
		},
		{
			name: "unrelated unknown track preserves reviewed original witness",
			mutate: func(f *LanguageFacts) {
				f.Tracks[0].Languages, f.Tracks[0].AudioOrigin = []string{"German"}, TrackAudioOriginal
				f.Tracks = append(f.Tracks, MediaTrackFacts{
					ID:         "unknown",
					ResourceID: "other-file",
					Kind:       MediaTrackAudio,
					Role:       AudioRoleProgramme,
					Languages:  []string{"und"},
				})
				f.ProgrammeLanguages, f.ProgrammeStatus = []string{"English", "German", "und"}, MetadataEvidenceStatusPartial
			},
			wantDub: true,
		},
		{name: "commentary English", mutate: func(f *LanguageFacts) { f.Tracks[1].Role = AudioRoleCommentary }},
		{name: "multilingual English stream", mutate: func(f *LanguageFacts) { f.Tracks[1].Languages = []string{"English", "German"} }},
		{name: "contradictory evidence", mutate: func(f *LanguageFacts) { f.ProgrammeStatus = MetadataEvidenceStatusContradictory }},
		{name: "cleared programme languages", mutate: func(f *LanguageFacts) { f.ProgrammeLanguages = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := LanguageFacts{
				OriginalLanguages:      []string{"Japanese"},
				OriginalLanguagesKnown: true,
				ProgrammeLanguages:     []string{"English", "Japanese"},
				ProgrammeStatus:        MetadataEvidenceStatusComplete,
				Tracks: []MediaTrackFacts{
					{
						ID:         "original",
						ResourceID: "file",
						Kind:       MediaTrackAudio,
						Role:       AudioRoleProgramme,
						Languages:  []string{"Japanese"},
					},
					{
						ID:         "english",
						ResourceID: "file",
						Kind:       MediaTrackAudio,
						Role:       AudioRoleProgramme,
						Languages:  []string{"English"},
					},
				},
			}
			if tc.mutate != nil {
				tc.mutate(&facts)
			}
			if got := facts.HasEnglishDub(); got != tc.wantDub {
				t.Fatalf("English dub=%t, want %t", got, tc.wantDub)
			}
			for _, track := range facts.Tracks {
				if track.ID == "english" && (facts.ProgrammeAudioOrigin(track) == TrackAudioDubbed) != tc.wantDub {
					t.Fatalf("English origin=%q, want dub=%t", facts.ProgrammeAudioOrigin(track), tc.wantDub)
				}
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
