// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestNonRemuxLanguageReviewTargetsUnknownProgrammeTrack(t *testing.T) {
	subject := api.TrackerValidationSubject{Type: "WEBDL", LanguageFacts: api.LanguageFacts{
		Tracks: []api.MediaTrackFacts{{
			ID:                  "programme",
			Kind:                api.MediaTrackAudio,
			Role:                api.AudioRoleProgramme,
			ManifestFingerprint: "current",
		}},
	}}
	failures := languageAssessment(subject)
	for _, failure := range failures {
		if failure.Rule == "language_evidence" {
			if len(failure.LanguageCorrectionTrackIDs) != 1 || failure.LanguageCorrectionTrackIDs[0] != "programme" {
				t.Fatalf("missing source correction targets: %#v", failure)
			}
			return
		}
	}
	t.Fatal("expected language evidence finding")
}
