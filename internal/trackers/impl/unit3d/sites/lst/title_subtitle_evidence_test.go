// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestTitleSubtitleNegativeEvidenceRequiresEveryCandidate(t *testing.T) {
	subject := lstValidationSubject()
	subject.Type = "REMUX"
	subject.LanguageFacts = lstTestLanguageFacts("Japanese", []string{"Japanese"}, nil)
	subject.Identity = api.ExternalIdentity{
		SourcePath: filepath.Join(t.TempDir(), "Example.mkv"),
		Generation: 1,
		TMDBID:     123,
		Category:   api.CanonicalCategoryMovie,
	}
	title, err := api.TitleSearchIdentityFingerprint(subject.Identity)
	if err != nil {
		t.Fatal(err)
	}
	subject.TitleSearchEvidence = api.TrackerTitleSearchEvidence{
		Status:                        api.MetadataEvidenceStatusComplete,
		TitleFingerprint:              title,
		ResultFingerprint:             "all-negative",
		TorrentCount:                  2,
		SubtitleInspectedTorrentCount: 2,
	}
	requireLSTValidationFailure(t, languageAssessment(subject), "language_subtitles_search", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
	subject.TitleSearchEvidence.SubtitleInspectedTorrentCount = 1
	requireLSTValidationFailure(t, languageAssessment(subject), "language_subtitles_search", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.TitleSearchEvidence.SubtitleInspectedTorrentCount = 2
	subject.TitleSearchEvidence.Status = api.MetadataEvidenceStatusPartial
	requireLSTValidationFailure(t, languageAssessment(subject), "language_subtitles_search", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.TitleSearchEvidence.Status = api.MetadataEvidenceStatusComplete
	subject.TitleSearchEvidence.EnglishSubtitleTorrentCount = 1
	requireLSTValidationFailure(t, languageAssessment(subject), "language_subtitles_search", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
}
