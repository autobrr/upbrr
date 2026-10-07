// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestHDBDefaultEvidenceIsNotLanguageCoverage(t *testing.T) {
	for _, value := range []bool{false, true} {
		subject := hdbLanguageSubject()
		subject.LanguageFacts.Tracks[0].DefaultKnown = false
		subject.LanguageFacts.Tracks[0].Default = value
		subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleAlternateMix,
			Languages: []string{"English"},
			Channels:  6,
		})
		requireHDBValidationFailure(t, sourceMixPairFailures(subject), "language_original_mix_default", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
		known := subject.LanguageFacts.Tracks[0]
		known.DefaultKnown = true
		known.Default = true
		subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, known)
		if f := sourceMixPairFailures(subject); len(f) != 0 {
			t.Fatalf("known original witness rejected: %+v", f)
		}
		subtitle := api.MediaTrackFacts{
			Kind:      api.MediaTrackSubtitle,
			Languages: []string{"English"},
			Forced:    true,
			Default:   value,
		}
		facts := api.LanguageFacts{
			SubtitleLanguages: []string{"English"},
			SubtitleStatus:    api.MetadataEvidenceStatusComplete,
			Tracks:            []api.MediaTrackFacts{subtitle},
		}
		if found, unknown := forcedDefaultSubtitleEvidence(facts); found || !unknown {
			t.Fatalf("unknown default became complete: found=%t unknown=%t", found, unknown)
		}
		subtitle.DefaultKnown = true
		subtitle.Default = true
		facts.Tracks = append(facts.Tracks, subtitle)
		if found, _ := forcedDefaultSubtitleEvidence(facts); !found {
			t.Fatal("known forced/default witness lost")
		}
	}
}
