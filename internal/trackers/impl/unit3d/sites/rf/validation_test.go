// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rf

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRFEvidencePolicyPassViolationAndMissingEvidence(t *testing.T) {
	t.Parallel()

	subject := rfPassingSubject()
	failures := evaluateRFEvidence(t, subject)
	if len(failures) != 0 {
		t.Fatalf("passing subject failures: %+v", failures)
	}

	subject.PackageFacts.ArchiveFileCount = 1
	assertRFFailure(
		t,
		evaluateRFEvidence(t, subject),
		"rf_archive",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)

	subject = rfPassingSubject()
	subject.PackageFacts.Status = api.MetadataEvidenceStatusPartial
	assertRFFailure(
		t,
		evaluateRFEvidence(t, subject),
		"rf_archive",
		api.RuleDispositionAdvisory,
		api.MetadataEvidenceStatusPartial,
	)
}

func TestRFRejectsMultipleMovieFilesAndMissingScreenshots(t *testing.T) {
	t.Parallel()

	subject := rfPassingSubject()
	subject.PackageFacts.KnownFileCount = 2
	subject.PackageFacts.MediaFileCount = 2
	assertRFFailure(
		t,
		evaluateRFEvidence(t, subject),
		"rf_movie_file_count",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)

	subject = rfPassingSubject()
	subject.AssetFacts.Screenshots.Count = 2
	assertRFFailure(
		t,
		evaluateRFEvidence(t, subject),
		"rf_asset_screenshot",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)
}

func TestRFValidationPolicyVersion(t *testing.T) {
	t.Parallel()

	if got := Profile().ValidationPolicy.ID; got != "unit3d-rf-policy-v3" {
		t.Fatalf("validation policy ID = %q", got)
	}
}

func TestRFFullDVDUsesVOBMediaInfoInsteadOfBDInfo(t *testing.T) {
	t.Parallel()

	subject := rfPassingSubject()
	subject.DiscType = "DVD"
	subject.PackageFacts.KnownFileCount = 3
	subject.AssetFacts.MediaInfoText = api.AssetEvidence{}
	subject.AssetFacts.DVDVOBMediaInfo = api.AssetEvidence{
		Status: api.MetadataEvidenceStatusComplete,
		Ready:  true,
		Count:  1,
	}
	failures := evaluateRFEvidence(t, subject)
	if len(failures) != 0 {
		t.Fatalf("RF DVD failures = %#v", failures)
	}
}

func rfPassingSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Tracker:       "RF",
		LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{
OriginalLanguage: "English",
 TrackCoverageComplete: true,
 PrimaryAudioTrackID: "main",
 Tracks: []api.MediaTrackFacts{{
ID: "main",
 Kind: api.MediaTrackAudio,
 Role: api.AudioRoleProgramme,
 Languages: []string{"English"},
 Default: true,
}},
}),
		Identity:      api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		PackageFacts: api.PackageFacts{
			Status:         api.MetadataEvidenceStatusComplete,
			KnownFileCount: 1,
			MediaFileCount: 1,
		},
		MediaFileFacts: api.MediaFileFacts{
			Status:            api.MetadataEvidenceStatusComplete,
			TechnicalStatus:   api.MetadataEvidenceStatusComplete,
			ExpectedFileCount: 1,
			Files: []api.MediaFileFact{{
				FileName:        "Example.Release.2026.1080p-GRP.mkv",
				VideoTrackCount: 1,
			}},
		},
		AssetFacts: api.AssetFacts{
			MediaInfoText: api.AssetEvidence{
				Status: api.MetadataEvidenceStatusComplete,
				Ready:  true,
				Count:  1,
			},
			Screenshots: api.AssetEvidence{
				Status: api.MetadataEvidenceStatusComplete,
				Ready:  true,
				Count:  3,
			},
		},
	}
}

func evaluateRFEvidence(t *testing.T, subject api.TrackerValidationSubject) []api.RuleFailure {
	t.Helper()
	failures, err := checkEvidenceRules(context.Background(), subject, api.NopLogger{})
	if err != nil {
		t.Fatalf("evaluate RF evidence: %v", err)
	}
	return failures
}

func assertRFFailure(
	t *testing.T,
	failures []api.RuleFailure,
	rule string,
	disposition api.RuleDisposition,
	status api.MetadataEvidenceStatus,
) {
	t.Helper()
	for _, failure := range failures {
		if failure.Rule == rule {
			if failure.Disposition != disposition || failure.EvidenceStatus != status {
				t.Fatalf("failure = %+v", failure)
			}
			return
		}
	}
	t.Fatalf("missing rule %q: %+v", rule, failures)
}

func TestRFLanguageEvidenceAndForcedSubtitleException(t *testing.T) {
	t.Parallel()
	subject := api.TrackerValidationSubject{Tracker: "RF", LanguageFacts: api.LanguageFacts{
OriginalLanguages: []string{"Japanese"},
 OriginalLanguagesKnown: true,
 SubtitleStatus: api.MetadataEvidenceStatusComplete,
}}
	key := trackers.LanguageQuestionKey(subject, "english_subtitles_expected")
	subject.QuestionnaireAnswers = map[string]string{key: "yes"}
	assertRFFailure(t, languageFailures(subject), "language_retail_subtitles", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
	subject.LanguageFacts.SubtitleStatus = api.MetadataEvidenceStatusPartial
	assertRFFailure(t, languageFailures(subject), "language_subtitle_evidence", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.LanguageFacts.SubtitleStatus = api.MetadataEvidenceStatusComplete
	subject.Identity.Generation++
	assertRFFailure(t, languageFailures(subject), "language_retail_subtitles", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.LanguageFacts.OriginalLanguagesKnown = false
	assertRFFailure(t, languageFailures(subject), "language_subtitle_evidence", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.Type = "DISC"
	if failures := languageFailures(subject); len(failures) != 0 {
		t.Fatalf("full disc changed: %+v", failures)
	}
	subject.Type = "WEBDL"
	subject.LanguageFacts.OriginalLanguagesKnown = true
	subject.LanguageFacts.OriginalLanguages = []string{"English"}
	subject.LanguageFacts.SubtitleLanguages = []string{"English"}
	subject.LanguageFacts.Tracks = []api.MediaTrackFacts{{Kind: api.MediaTrackSubtitle, Languages: []string{"English (Forced)"}}}
	if failures := languageFailures(subject); len(failures) != 0 {
		t.Fatalf("explicit forced-English correction rejected: %+v", failures)
	}
	subject.LanguageFacts.Tracks[0].Languages = []string{"English"}
	assertRFFailure(t, languageFailures(subject), "language_subtitle_presentation", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
}

func TestRFManualSubtitleClearDoesNotReuseForcedException(t *testing.T) {
	t.Parallel()
	media := api.MediaFacts{
OriginalLanguage: "English",
 TrackCoverageComplete: true,
 SubtitleLanguages: []string{"English (Forced)"},
 Tracks: []api.MediaTrackFacts{{
Kind: api.MediaTrackSubtitle,
 Languages: []string{"English"},
 Forced: true,
}},
}
	subject := api.TrackerValidationSubject{Tracker: "RF", LanguageFacts: mediafacts.ResolveLanguages(media)}
	if failures := languageFailures(subject); len(failures) != 0 {
		t.Fatalf("known forced English rejected: %+v", failures)
	}
	media.SubtitleLanguages = nil
	media.SubtitleLanguagesProvenance = api.FactProvenanceManualEmpty
	subject.LanguageFacts = mediafacts.ResolveLanguages(media)
	assertRFFailure(t, languageFailures(subject), "language_subtitle_evidence", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
}

func TestRFMixedOriginalForcedExceptionRequiresPredominanceEvidence(t *testing.T) {
	t.Parallel()
	subject := api.TrackerValidationSubject{Tracker: "RF", LanguageFacts: api.LanguageFacts{
OriginalLanguages: []string{"English", "Japanese"},
 OriginalLanguagesKnown: true,
 SubtitleStatus: api.MetadataEvidenceStatusComplete,
 SubtitleLanguages: []string{"English"},
 Tracks: []api.MediaTrackFacts{{
Kind: api.MediaTrackSubtitle,
 Languages: []string{"English"},
 Forced: true,
}},
}}
	assertRFFailure(t, languageFailures(subject), "language_predominance_evidence", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	key := trackers.LanguageQuestionKey(subject, "predominantly_english")
	subject.QuestionnaireAnswers = map[string]string{key: "yes"}
	if failures := languageFailures(subject); len(failures) != 0 {
		t.Fatalf("confirmed predominance still failed: %+v", failures)
	}
	subject.QuestionnaireAnswers[key] = "no"
	assertRFFailure(t, languageFailures(subject), "language_subtitle_presentation", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
	subject.QuestionnaireAnswers[key] = "yes"
	subject.Identity.Generation++
	assertRFFailure(t, languageFailures(subject), "language_predominance_evidence", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	meta := api.UploadSubject{LanguageFacts: subject.LanguageFacts}
	schema := languageQuestionnaire(trackers.PreparationInput{Meta: meta})
	if schema == nil || len(schema.Fields) != 1 || schema.Fields[0].Label != "Predominantly English film" {
		t.Fatalf("missing bounded question: %+v", schema)
	}
	meta.Type = "DISC"
	if schema := languageQuestionnaire(trackers.PreparationInput{Meta: meta}); schema != nil {
		t.Fatalf("full disc received question: %+v", schema)
	}
}
