// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAudioLanguageClearPreservesUnknownProgrammeOrigin(t *testing.T) {
	facts := ResolveLanguages(api.MediaFacts{
		OriginalLanguage:         "Japanese",
		TrackCoverageComplete:    true,
		AudioLanguagesProvenance: api.FactProvenanceManualEmpty,
		Tracks: []api.MediaTrackFacts{
			{
				Kind:      api.MediaTrackAudio,
				Role:      api.AudioRoleProgramme,
				Languages: []string{"Japanese"},
			},
			{
				Kind:        api.MediaTrackAudio,
				Role:        api.AudioRoleProgramme,
				Languages:   []string{"English"},
				AudioOrigin: api.TrackAudioDubbed,
			},
		},
	})
	if len(facts.ProgrammeLanguages) != 0 || facts.ProgrammeStatus != api.MetadataEvidenceStatusPartial || facts.HasOriginalAudio() || facts.HasEnglishDub() {
		t.Fatal("cleared programme languages recovered original or dub evidence")
	}
}

func TestResolveLanguagesSeparatesProgrammeAndSecondaryAudio(t *testing.T) {
	media := api.MediaFacts{OriginalLanguage: "ja", TrackCoverageComplete: true}
	for _, item := range []struct{ title, language string }{
		{"Main", "Japanese"}, {"Main", "English"}, {"Main", "German"},
		{"Commentary", "French"}, {"Compatibility", "Spanish"}, {"Isolated Score", "Italian"},
		{"Historical audio", "Korean"},
	} {
		media.Tracks = append(media.Tracks, api.MediaTrackFacts{
			Kind:      api.MediaTrackAudio,
			Role:      AudioRole(item.title),
			Languages: []string{item.language},
		})
	}
	// These two separate programme streams are confirmed dubs, not inferred from their tags.
	media.Tracks[1].AudioOrigin = api.TrackAudioDubbed
	media.Tracks[2].AudioOrigin = api.TrackAudioDubbed
	facts := ResolveLanguages(media)
	if !slices.Equal(facts.ProgrammeLanguages, []string{"Japanese", "English", "German"}) || !facts.HasEnglishDub() || !facts.HasOriginalAudio() {
		t.Fatalf("programme facts = %#v", facts)
	}
	facts.Tracks[0].Languages[0] = "Changed"
	if media.Tracks[0].Languages[0] != "Japanese" {
		t.Fatal("facts alias media tracks")
	}
}

func TestResolveLanguagesInfersEnglishDubFromReviewedOriginalInPartialPack(t *testing.T) {
	media := api.MediaFacts{
		OriginalLanguage:      "Japanese",
		TrackCoverageComplete: false,
		Tracks: []api.MediaTrackFacts{
			{
				ID:                    "original",
				ResourceID:            "file",
				Kind:                  api.MediaTrackAudio,
				Role:                  api.AudioRoleProgramme,
				Languages:             []string{"German"},
				AudioOrigin:           api.TrackAudioOriginal,
				AudioOriginProvenance: api.FactProvenanceManual,
			},
			{
				ID:         "english",
				ResourceID: "file",
				Kind:       api.MediaTrackAudio,
				Role:       api.AudioRoleProgramme,
				Languages:  []string{"English"},
			},
		},
	}
	facts := ResolveLanguages(media)
	if facts.TrackCoverageComplete || facts.ProgrammeStatus != api.MetadataEvidenceStatusPartial || !facts.HasOriginalAudio() || !facts.HasEnglishDub() {
		t.Fatalf("reviewed local pair lost in partial pack: %+v", facts)
	}
	media.Tracks = append(media.Tracks, api.MediaTrackFacts{
		ID:         "unknown",
		ResourceID: "other-file",
		Kind:       api.MediaTrackAudio,
		Role:       api.AudioRoleProgramme,
		Languages:  []string{"und"},
	})
	facts = ResolveLanguages(media)
	if facts.ProgrammeStatus != api.MetadataEvidenceStatusPartial || !facts.HasOriginalAudio() || !facts.HasEnglishDub() {
		t.Fatalf("unrelated unknown audio changed local pair: %+v", facts)
	}
}

func TestResolveLanguagesRetainsClearsAndUnresolvedEvidence(t *testing.T) {
	media := api.MediaFacts{
		OriginalLanguage:      "Japanese",
		TrackCoverageComplete: true,
		Tracks: []api.MediaTrackFacts{{
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleProgramme,
			Languages: []string{"Japanese"},
		}},
	}
	if got := ResolveLanguages(media); got.AudioStatus != api.MetadataEvidenceStatusComplete {
		t.Fatalf("complete = %#v", got)
	}
	media.OriginalLanguage = ""
	media.OriginalLanguageProvenance = api.FactProvenanceManualEmpty
	if got := ResolveLanguages(media); got.AudioStatus == api.MetadataEvidenceStatusComplete || len(got.OriginalLanguages) != 0 {
		t.Fatalf("original clear = %#v", got)
	}
	media.AudioLanguagesProvenance = api.FactProvenanceManualEmpty
	if got := ResolveLanguages(media); len(got.ProgrammeLanguages) != 0 || got.HasEnglishDub() {
		t.Fatalf("audio clear = %#v", got)
	}
	media.Tracks[0].Role = ""
	if got := ResolveLanguages(media); got.AudioStatus == api.MetadataEvidenceStatusComplete {
		t.Fatalf("unknown role = %#v", got)
	}
}

func TestEnglishSecondaryTrackDoesNotCreateDub(t *testing.T) {
	for _, role := range []api.AudioTrackRole{api.AudioRoleCommentary, api.AudioRoleCompatibility, api.AudioRoleHistorical, api.AudioRoleIsolatedScore} {
		facts := ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "Japanese",
			TrackCoverageComplete: true,
			Tracks: []api.MediaTrackFacts{
				{
					Kind:      api.MediaTrackAudio,
					Role:      api.AudioRoleProgramme,
					Languages: []string{"Japanese"},
				},
				{
					Kind:      api.MediaTrackAudio,
					Role:      role,
					Languages: []string{"English"},
				},
			},
		})
		if facts.HasEnglishDub() {
			t.Fatalf("%s created a dub", role)
		}
	}
}

func TestSubtitleCoverageKeepsBaseLanguageForEligibility(t *testing.T) {
	for _, tc := range []struct {
		value string
		full  bool
	}{{"English (Full)", true}, {"English (Forced)", false}} {
		facts := ResolveLanguages(api.MediaFacts{
			OriginalLanguage:            "Japanese",
			TrackCoverageComplete:       true,
			SubtitleLanguages:           []string{tc.value},
			SubtitleLanguagesProvenance: api.FactProvenanceManual,
		})
		if !slices.Equal(facts.SubtitleLanguages, []string{"English"}) || slices.Contains(facts.FullSubtitleLanguages, "English") != tc.full {
			t.Fatalf("%s: %#v", tc.value, facts)
		}
	}
}

func TestSecondaryUnknownLanguageDoesNotInvalidateProgrammeFacts(t *testing.T) {
	facts := ResolveLanguages(api.MediaFacts{
		OriginalLanguage:      "Japanese",
		TrackCoverageComplete: true,
		Tracks: []api.MediaTrackFacts{
			{
				Kind:      api.MediaTrackAudio,
				Role:      api.AudioRoleProgramme,
				Languages: []string{"Japanese"},
			},
			{
				Kind:        api.MediaTrackAudio,
				Role:        api.AudioRoleProgramme,
				Languages:   []string{"English"},
				AudioOrigin: api.TrackAudioDubbed,
			},
			{Kind: api.MediaTrackAudio, Role: api.AudioRoleCommentary},
		},
	})
	if facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete || !facts.HasEnglishDub() {
		t.Fatalf("irrelevant secondary language blocked known facts: %#v", facts)
	}
}

func TestUnknownOriginalDoesNotCreateDubFacts(t *testing.T) {
	for _, original := range []string{"unrecognized", "phi", "mis", "und", "mul"} {
		facts := ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      original,
			TrackCoverageComplete: true,
			Tracks: []api.MediaTrackFacts{{
				Kind:      api.MediaTrackAudio,
				Role:      api.AudioRoleProgramme,
				Languages: []string{"English"},
			}},
		})
		if facts.OriginalLanguagesKnown || facts.HasEnglishDub() {
			t.Fatalf("unknown original created English dub: %#v", facts)
		}
	}
}

func TestLanguageIdentityPreservesOriginalAndDubBoundaries(t *testing.T) {
	for _, tt := range []struct {
		original, programme, english string
		wantOriginal                 bool
	}{
		{"ja", "Japanese [ja-JP]", "English [en-US]", true},
		{"tl", "tgl", "English [en-GB]", true},
		{"fil", "tgl", "English", false},
		{"zh", "cmn", "English", false},
	} {
		facts := ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      tt.original,
			TrackCoverageComplete: true,
			Tracks: []api.MediaTrackFacts{
				{
					ID:         "original",
					ResourceID: "same-file",
					Kind:       api.MediaTrackAudio,
					Role:       api.AudioRoleProgramme,
					Languages:  []string{tt.programme},
				},
				{
					ID:         "english",
					ResourceID: "same-file",
					Kind:       api.MediaTrackAudio,
					Role:       api.AudioRoleProgramme,
					Languages:  []string{tt.english},
				},
			},
		})
		if facts.HasOriginalAudio() != tt.wantOriginal || facts.HasEnglishDub() != tt.wantOriginal {
			t.Errorf("original=%q programme=%q: original=%t dub=%t, want %t", tt.original, tt.programme, facts.HasOriginalAudio(), facts.HasEnglishDub(), tt.wantOriginal)
		}
	}
	media := api.MediaFacts{
		OriginalLanguage:         "Japanese",
		TrackCoverageComplete:    true,
		AudioLanguages:           []string{"Japanese", "English"},
		AudioLanguagesProvenance: api.FactProvenanceManual,
		Tracks: []api.MediaTrackFacts{
			{
				ID:         "original",
				ResourceID: "file",
				Kind:       api.MediaTrackAudio,
				Role:       api.AudioRoleProgramme,
				Languages:  []string{"Japanese"},
			},
			{
				ID:         "us",
				ResourceID: "file",
				Kind:       api.MediaTrackAudio,
				Role:       api.AudioRoleProgramme,
				Languages:  []string{"English [en-US]"},
			},
			{
				ID:         "gb",
				ResourceID: "file",
				Kind:       api.MediaTrackAudio,
				Role:       api.AudioRoleProgramme,
				Languages:  []string{"English [en-GB]"},
			},
		},
	}
	facts := ResolveLanguages(media)
	if facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete || !facts.HasOriginalAudio() || !facts.HasEnglishDub() ||
		!slices.Equal(facts.Tracks[1].Languages, []string{"English [en-US]"}) {
		t.Fatalf("aggregate base-language correction conflicted with or rewrote track variants: %+v", facts)
	}
}
