// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestHDBDefaultEvidenceIsNotLanguageCoverage(t *testing.T) {
	for _, value := range []bool{false, true} {
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
		if hasForced, found, unknown := forcedDefaultSubtitleEvidence(facts); !hasForced || found || !unknown {
			t.Fatalf("unknown default became complete: found=%t unknown=%t", found, unknown)
		}
		subtitle.DefaultKnown = true
		subtitle.Default = true
		facts.Tracks = append(facts.Tracks, subtitle)
		if _, found, _ := forcedDefaultSubtitleEvidence(facts); !found {
			t.Fatal("known forced/default witness lost")
		}
	}
}
