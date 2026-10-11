// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

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
	facts := ResolveLanguages(media)
	if !slices.Equal(facts.ProgrammeLanguages, []string{"Japanese", "English", "German"}) || !facts.HasEnglishDub() || !facts.HasOriginalAudio() {
		t.Fatalf("programme facts = %#v", facts)
	}
	facts.Tracks[0].Languages[0] = "Changed"
	if media.Tracks[0].Languages[0] != "Japanese" {
		t.Fatal("facts alias media tracks")
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
				Kind:      api.MediaTrackAudio,
				Role:      api.AudioRoleProgramme,
				Languages: []string{"English"},
			},
			{Kind: api.MediaTrackAudio, Role: api.AudioRoleCommentary},
		},
	})
	if facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete || !facts.HasEnglishDub() {
		t.Fatalf("irrelevant secondary language blocked known facts: %#v", facts)
	}
}

func TestUnknownOriginalDoesNotCreateDubFacts(t *testing.T) {
	facts := ResolveLanguages(api.MediaFacts{
		OriginalLanguage:      "unrecognized",
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
