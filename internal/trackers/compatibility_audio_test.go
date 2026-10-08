// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAutomaticCompatibilityPreservesEvidenceBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*api.MediaTrackFacts, *[]api.MediaTrackFacts)
		want   bool
	}{
		{name: "ordinary same language", want: true},
		{
			name:   "known alias set",
			want:   true,
			mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.Languages = []string{"eng", "English"} },
		},
		{
			name: "legacy compatibility aggregation flag",
			want: true,
			mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) {
				c.Role = api.AudioRoleCompatibility
				c.Commentary = true
			},
		},
		{
			name:   "unknown role",
			want:   true,
			mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.Role = "" },
		},
		{name: "commentary", mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.Commentary = true }},
		{name: "alternate", mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.Role = api.AudioRoleAlternateMix }},
		{name: "embedded", mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.EmbeddedCompatibility = true }},
		{name: "embedded title", mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.Title = "Embedded Core" }},
		{name: "other resource", mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.ResourceID = "other" }},
		{name: "same physical track", mutate: func(c *api.MediaTrackFacts, m *[]api.MediaTrackFacts) { c.ID = (*m)[0].ID }},
		{name: "unknown language", mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.Languages = []string{"und"} }},
		{name: "partial language set", mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) {
			c.Languages = []string{"English", "Unknown language"}
		}},
		{name: "overlapping language sets", mutate: func(_ *api.MediaTrackFacts, m *[]api.MediaTrackFacts) {
			(*m)[0].Languages = []string{"English", "German"}
		}},
		{name: "commentary mix", mutate: func(_ *api.MediaTrackFacts, m *[]api.MediaTrackFacts) { (*m)[0].Role = api.AudioRoleCommentary }},
		{name: "unidentified mix", mutate: func(_ *api.MediaTrackFacts, m *[]api.MediaTrackFacts) { (*m)[0].ID = "" }},
		{name: "multiple mixes", mutate: func(_ *api.MediaTrackFacts, m *[]api.MediaTrackFacts) {
			second := (*m)[0]
			second.ID = "other"
			*m = append(*m, second)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			companion := api.MediaTrackFacts{
				ID:         "dd",
				Kind:       api.MediaTrackAudio,
				Role:       api.AudioRoleProgramme,
				Codec:      "DD",
				ResourceID: "media",
				Languages:  []string{"English"},
			}
			mixes := []api.MediaTrackFacts{{
				ID:         "truehd",
				Kind:       api.MediaTrackAudio,
				Role:       api.AudioRoleProgramme,
				Codec:      "TrueHD",
				ResourceID: "media",
				Languages:  []string{"English"},
			}}
			if test.mutate != nil {
				test.mutate(&companion, &mixes)
			}
			if got := AutomaticCompatibilityMix(companion, mixes); (got != "") != test.want {
				t.Fatalf("association=%q want inferred=%t", got, test.want)
			}
		})
	}
}

func TestCompatibilityCandidatesRetainUncertainCompetingMix(t *testing.T) {
	companion := api.MediaTrackFacts{
		ID:        "dd",
		Kind:      api.MediaTrackAudio,
		Role:      api.AudioRoleProgramme,
		Codec:     "DD",
		Languages: []string{"English"},
	}
	facts := api.LanguageFacts{Tracks: []api.MediaTrackFacts{
		{
			ID:        "first",
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleProgramme,
			Codec:     "TrueHD",
			Languages: []string{"English"},
		},
		{
			ID:    "second",
			Kind:  api.MediaTrackAudio,
			Role:  api.AudioRoleProgramme,
			Codec: "TrueHD",
		},
		companion,
	}}
	if mixes := CompatibilityAudioMixes(facts, companion, true); len(mixes) != 2 || AutomaticCompatibilityMix(companion, mixes) != "" {
		t.Fatalf("unknown language created false unique match: %+v", mixes)
	}
	for _, codec := range []string{"DD", "AC-3", "DD+", "DDP", "E-AC3", "E-AC-3"} {
		companion.Codec = codec
		if !StandaloneDolbyAudio(companion) {
			t.Fatalf("alias %q not discovered", codec)
		}
	}
	companion.Codec = "AAC"
	if StandaloneDolbyAudio(companion) {
		t.Fatal("ordinary non-Dolby stream was reclassified")
	}
	before := slices.Clone(facts.Tracks)
	_ = AutomaticDolbyCompatibilityMix(facts, companion)
	if !slices.EqualFunc(before, facts.Tracks, func(a, b api.MediaTrackFacts) bool { return a.ID == b.ID && a.Role == b.Role }) {
		t.Fatal("canonical tracks changed")
	}
}
