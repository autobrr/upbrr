// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"github.com/autobrr/upbrr/internal/mediafacts"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveTagsReleaseVersion(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ value, want string }{
		{"PROPER", "PROPER"}, {"PROPER2", "PROPER"}, {"PROPER3", "PROPER"},
		{"REPACK", "REPACK"}, {"REPACK2", "REPACK"}, {"REPACK3", "REPACK"},
		{" proper ", "PROPER"}, {"RERIP", ""}, {"", ""}, {"PROPER4", ""},
	} {
		t.Run(test.value, func(t *testing.T) {
			tags := resolveTags(api.UploadSubject{
				Repack:  test.value,
				Edition: "Hybrid Director",
				Type:    "WEBDL",
			})
			want := []string{"WEBDL", "Hybrid"}
			if test.want != "" {
				want = append([]string{test.want}, want...)
			}
			if !slices.Equal(tags, want) {
				t.Fatalf("tags=%q want %q", tags, want)
			}
		})
	}
	tags := resolveTags(api.UploadSubject{Edition: "PROPER"})
	if slices.Contains(tags, "PROPER") {
		t.Fatalf("edition polluted version tags: %q", tags)
	}
}

func TestBHDLanguageTagsAreIndependentFromPresentation(t *testing.T) {
	for _, test := range []struct {
		languages []string
		dual, dub bool
	}{
		{[]string{"Japanese", "English"}, true, true},
		{[]string{"Japanese", "English", "German"}, true, true},
		{[]string{"English"}, false, true},
		{[]string{"Japanese", "German"}, false, false},
	} {
		media := api.MediaFacts{OriginalLanguage: "Japanese", TrackCoverageComplete: true}
		for _, language := range test.languages {
			media.Tracks = append(media.Tracks, api.MediaTrackFacts{
				Kind:      api.MediaTrackAudio,
				Role:      api.AudioRoleProgramme,
				Languages: []string{language},
			})
		}
		facts := mediafacts.ResolveLanguages(media)
		for _, audio := range []string{"FLAC 2.0", "Dubbed FLAC 2.0", "Dual-Audio FLAC 2.0"} {
			yes := true
			tags := resolveTags(api.UploadSubject{
				LanguageFacts:        facts,
				Audio:                audio,
				ReleaseNameOverrides: api.ReleaseNameOverrides{NoDub: &yes, NoDual: &yes},
			})
			if slices.Contains(tags, "DualAudio") != test.dual || slices.Contains(tags, "EnglishDub") != test.dub {
				t.Fatalf("languages=%v audio=%s tags=%v", test.languages, audio, tags)
			}
		}
	}
	for _, role := range []api.AudioTrackRole{api.AudioRoleCommentary, api.AudioRoleCompatibility, api.AudioRoleIsolatedScore, api.AudioRoleHistorical} {
		facts := mediafacts.ResolveLanguages(api.MediaFacts{
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
		tags := resolveTags(api.UploadSubject{LanguageFacts: facts, Audio: "Dubbed FLAC 2.0"})
		if slices.Contains(tags, "EnglishDub") || slices.Contains(tags, "DualAudio") {
			t.Fatalf("secondary %s created tags %v", role, tags)
		}
	}
}
