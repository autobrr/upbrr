// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDeterministicValidationEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		mutate          func(*api.TrackerValidationSubject)
		wantRule        string
		wantDisposition api.RuleDisposition
		wantStatus      api.MetadataEvidenceStatus
	}{
		{name: "complete evidence passes"},
		{name: "release version is independent of edition", mutate: func(subject *api.TrackerValidationSubject) { subject.Repack = "PROPER" }},
		{
			name:   "unsupported edition does not block upload",
			mutate: func(subject *api.TrackerValidationSubject) { subject.Edition = "Unknown Edition" },
		},
		{
			name: "archive is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.ArchiveFileCount = 1
			},
			wantRule:        "lst_package_safety",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "missing package evidence is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.Status = api.MetadataEvidenceStatusUnavailable
			},
			wantRule:        "lst_package_safety",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusUnavailable,
		},
		{
			name: "too few inspected screenshots is waivable",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.AssetFacts.HostedScreenshots.Count = 2
			},
			wantRule:        "lst_screenshot_format",
			wantDisposition: api.RuleDispositionWaivable,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "missing screenshot evidence is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.AssetFacts.HostedScreenshots.Status = api.MetadataEvidenceStatusUnavailable
			},
			wantRule:        "guidance_lst_screenshot_format",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusPartial,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := lstValidationSubject()
			if test.mutate != nil {
				test.mutate(&subject)
			}
			failures, err := validationPolicy().Check(context.Background(), subject, nil)
			if err != nil {
				t.Fatalf("validate LST subject: %v", err)
			}
			failures = withoutLSTStreamProcessingGuidance(t, failures)
			failures = withoutLSTTrackPresentationGuidance(t, failures)
			failures = withoutLSTUploadGuidance(t, failures)
			if test.wantRule == "" {
				if len(failures) != 0 {
					t.Fatalf("unexpected failures: %#v", failures)
				}
				return
			}
			requireLSTValidationFailure(t, failures, test.wantRule, test.wantDisposition, test.wantStatus)
		})
	}
}

func TestRulesRequireEncodeSettings(t *testing.T) {
	t.Parallel()
	if !Rules().RequireValidMISetting {
		t.Fatal("LST encode-settings rule is disabled")
	}
}

func lstValidationSubject() api.TrackerValidationSubject {
	subject := api.TrackerValidationSubject{
		Tracker:       "LST",
		AnimeKnown:    true,
		LanguageFacts: lstTestLanguageFacts("English", []string{"English"}, []string{"English"}),
		Type:          "WEBDL",
		Container:     "mkv",
		PackageFacts: api.PackageFacts{
			Status:         api.MetadataEvidenceStatusComplete,
			KnownFileCount: 1,
			MediaFileCount: 1,
		},
		AssetFacts: api.AssetFacts{
			Screenshots: api.AssetEvidence{
				Status:               api.MetadataEvidenceStatusComplete,
				Ready:                true,
				Count:                3,
				InspectedFormatCount: 3,
				LosslessCount:        3,
			},
			Status: api.MetadataEvidenceStatusComplete,
			MediaInfoText: api.AssetEvidence{
				Status: api.MetadataEvidenceStatusComplete,
				Ready:  true,
				Count:  1,
			},
			HostedScreenshots: api.AssetEvidence{
				Status: api.MetadataEvidenceStatusComplete,
				Ready:  true,
				Count:  3,
			},
		},
	}
	subject.QuestionnaireAnswers = map[string]string{}
	return subject
}

func requireLSTValidationFailure(
	t *testing.T,
	failures []api.RuleFailure,
	rule string,
	disposition api.RuleDisposition,
	status api.MetadataEvidenceStatus,
) {
	t.Helper()
	for _, failure := range failures {
		if failure.Rule == rule && failure.Disposition == disposition && failure.EvidenceStatus == status {
			return
		}
	}
	t.Fatalf("missing failure rule=%s disposition=%s status=%s in %#v", rule, disposition, status, failures)
}

func TestLSTValidationAllowsUnmappedEditions(t *testing.T) {
	for _, cut := range []string{"Director's Cut", "Unsupported Cut", ""} {
		t.Run(cut, func(t *testing.T) {
			subject := lstValidationSubject()
			subject.Cut = cut
			failures, err := validationPolicy().Check(t.Context(), subject, nil)
			if err != nil {
				t.Fatal(err)
			}
			failures = withoutLSTStreamProcessingGuidance(t, failures)
			failures = withoutLSTTrackPresentationGuidance(t, failures)
			failures = withoutLSTUploadGuidance(t, failures)
			if len(failures) != 0 {
				t.Fatalf("cut %q failures = %#v", cut, failures)
			}
			subject.AssetFacts.HostedScreenshots.Count = 2
			failures, err = validationPolicy().Check(t.Context(), subject, nil)
			if err != nil {
				t.Fatal(err)
			}
			failures = withoutLSTStreamProcessingGuidance(t, failures)
			failures = withoutLSTTrackPresentationGuidance(t, failures)
			failures = withoutLSTUploadGuidance(t, failures)
			requireLSTValidationFailure(t, failures, "lst_screenshot_format", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
		})
	}
}

// lstTestLanguageFacts models inspected, identified programme streams and full
// embedded subtitles so unrelated fixtures satisfy the current facts contract.
func lstTestLanguageFacts(original string, programme, subtitles []string) api.LanguageFacts {
	media := api.MediaFacts{
		OriginalLanguage:      original,
		SubtitleLanguages:     subtitles,
		TrackCoverageComplete: true,
		PrimaryAudioTrackID:   "audio-0",
	}
	for index, language := range programme {
		media.Tracks = append(media.Tracks, api.MediaTrackFacts{
			ID:               "audio-" + strconv.Itoa(index),
			Kind:             api.MediaTrackAudio,
			Role:             api.AudioRoleProgramme,
			Title:            "Programme audio",
			Languages:        []string{language},
			Codec:            "AC-3",
			Default:          index == 0,
			DefaultKnown:     true,
			StreamOrder:      index + 1,
			StreamOrderKnown: true,
		})
	}
	for index, language := range subtitles {
		media.Tracks = append(media.Tracks, api.MediaTrackFacts{
			ID:               "subtitle-" + strconv.Itoa(index),
			Kind:             api.MediaTrackSubtitle,
			Languages:        []string{language},
			Default:          index == 0,
			DefaultKnown:     true,
			StreamOrder:      index + 1,
			StreamOrderKnown: true,
		})
	}
	// These existing policy fixtures describe reviewed alternate-language dubs.
	// Unreviewed tag-only evidence is exercised separately by origin regressions.
	for index := range media.Tracks {
		track := &media.Tracks[index]
		if track.Kind == api.MediaTrackAudio && len(track.Languages) == 1 && track.Languages[0] != original && track.Languages[0] != "" && track.Languages[0] != "und" && track.Languages[0] != "mul" && track.Languages[0] != "Multiple Languages" {
			track.AudioOrigin = api.TrackAudioDubbed
		}
	}
	return mediafacts.ResolveLanguages(media)
}

func TestLSTLanguageDefaultAndTitleEvidenceBoundaries(t *testing.T) {
	t.Parallel()
	subject := lstValidationSubject()
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
		Status:            api.MetadataEvidenceStatusComplete,
		TitleFingerprint:  title,
		ResultFingerprint: "zero-title",
	}
	failures := languageAssessment(subject)
	requireLSTValidationFailure(t, failures, "language_subtitles_search", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
	subject.Anime = true
	subject.LanguageFacts.Tracks[0].Default = false
	requireLSTValidationFailure(t, languageAssessment(subject), "language_default_audio", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
	subject.TitleSearchEvidence.Status = api.MetadataEvidenceStatusPartial
	subject.TitleSearchEvidence.TorrentCount = 1
	subject.TitleSearchEvidence.ResultFingerprint = "positive-partial"
	requireLSTValidationFailure(t, languageAssessment(subject), "language_subtitles_search", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.TitleSearchEvidence.EnglishSubtitleTorrentCount = 1
	requireLSTValidationFailure(t, languageAssessment(subject), "language_subtitles_search", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	subject.Type = "DISC"
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("full disc acquired subtitle/default gates: %+v", failures)
	}
}

func withoutLSTUploadGuidance(t *testing.T, failures []api.RuleFailure) []api.RuleFailure {
	t.Helper()
	for _, rule := range []string{"guidance_lst_duplicate_evidence", "language_web_source_naming"} {
		index := slices.IndexFunc(failures, func(f api.RuleFailure) bool { return f.Rule == rule })
		if index < 0 {
			t.Fatalf("missing passive guidance %s: %+v", rule, failures)
		}
		failure := failures[index]
		if failure.Disposition != api.RuleDispositionAdvisory || failure.QuestionnaireInput || failure.EvidenceStatus != api.MetadataEvidenceStatusPartial {
			t.Fatalf("guidance requires acknowledgement or input: %+v", failure)
		}
		failures = slices.Delete(failures, index, index+1)
	}
	return failures
}
