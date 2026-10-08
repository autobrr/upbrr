// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"github.com/autobrr/upbrr/pkg/api"
	"slices"
	"testing"
)

func TestLSTDefaultCountUsesKnownFlags(t *testing.T) {
	for _, test := range []struct {
		name              string
		anime             bool
		defaults, unknown int
		want              api.MetadataEvidenceStatus
	}{
		{"one plus unknown", false, 1, 1, api.MetadataEvidenceStatusPartial},
		{"two plus unknown", false, 2, 1, api.MetadataEvidenceStatusComplete},
		{"known zero", false, 0, 0, api.MetadataEvidenceStatusComplete},
		{"unknown zero", false, 0, 1, api.MetadataEvidenceStatusPartial},
		{"known one", false, 1, 0, ""},
		{"anime one plus unknown", true, 1, 1, ""},
		{"anime unknown only", true, 0, 1, api.MetadataEvidenceStatusPartial},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := lstValidationSubject()
			subject.Anime = test.anime
			subject.LanguageFacts = lstTestLanguageFacts("English", []string{"English"}, []string{"English"})
			base := subject.LanguageFacts.Tracks[0]
			base.Default = false
			subject.LanguageFacts.Tracks = []api.MediaTrackFacts{base}
			for i := 0; i < test.defaults+test.unknown; i++ {
				track := base
				track.Default = true
				track.DefaultKnown = i < test.defaults
				subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, track)
			}
			failures := languageAssessment(subject)
			i := slices.IndexFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_default_audio" })
			if test.want == "" {
				if i >= 0 {
					t.Fatalf("proved count rejected: %+v", failures[i])
				}
				return
			}
			if i < 0 || failures[i].EvidenceStatus != test.want || failures[i].Disposition != api.RuleDispositionStrict {
				t.Fatalf("wrong count evidence: %+v", failures)
			}
		})
	}
}
