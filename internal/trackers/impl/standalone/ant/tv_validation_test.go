// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestANTTVEvidencePolicyPassViolationAndMissingEvidence(t *testing.T) {
	t.Parallel()

	subject := antTVPassingSubject()
	failures := evaluateANTTVEvidence(t, subject)
	if len(failures) != 0 {
		t.Fatalf("passing subject failures: %+v", failures)
	}

	subject.PackageFacts.ArchiveFileCount = 1
	assertANTTVFailure(
		t,
		evaluateANTTVEvidence(t, subject),
		"ant_tv_archive",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)

	subject = antTVPassingSubject()
	subject.PackageFacts.Status = api.MetadataEvidenceStatusPartial
	assertANTTVFailure(
		t,
		evaluateANTTVEvidence(t, subject),
		"ant_tv_archive",
		api.RuleDispositionAdvisory,
		api.MetadataEvidenceStatusPartial,
	)
}

func TestANTTVFolderLayoutFollowsAPICategory(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                 string
		pack, batch, blocked bool
	}{
		{name: "episode", blocked: true},
		{name: "season", pack: true},
		{name: "batch", batch: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := antTVPassingSubject()
			subject.PackageFacts.SingleFileFolder = true
			subject.TVPack, subject.MultipleEpisodes = test.pack, test.batch
			failures := evaluateANTTVEvidence(t, subject)
			if test.blocked {
				assertANTTVFailure(t, failures, "ant_tv_episode_layout", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
			} else if len(failures) != 0 {
				t.Fatalf("season category received episode layout restriction: %+v", failures)
			}
		})
	}
}

func TestANTTVIgnoresScreenshotEvidence(t *testing.T) {
	t.Parallel()

	for _, evidence := range []api.AssetEvidence{
		{},
		{Status: api.MetadataEvidenceStatusPartial},
		{Status: api.MetadataEvidenceStatusComplete},
		{
			Status: api.MetadataEvidenceStatusComplete,
			Ready:  true,
			Count:  4,
		},
	} {
		subject := antTVPassingSubject()
		subject.AssetFacts.Screenshots = evidence
		subject.AssetFacts.HostedScreenshots = evidence
		if failures := evaluateANTTVEvidence(t, subject); len(failures) != 0 {
			t.Errorf("screenshot evidence %+v affected ANTTV validation: %+v", evidence, failures)
		}
	}
}

func TestANTTVDoesNotApplyMediaOnlyPackageRuleToDiscs(t *testing.T) {
	t.Parallel()

	subject := antTVPassingSubject()
	subject.DiscType = "DVD"
	subject.PackageFacts.KnownFileCount = 3
	subject.PackageFacts.MediaFileCount = 1
	for _, failure := range evaluateANTTVEvidence(t, subject) {
		if failure.Rule == "ant_tv_media_only" {
			t.Fatalf("disc package received media-only failure: %+v", failure)
		}
	}
}

func antTVPassingSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		MediaInfoTextReady: true,
		Identity:           api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		LanguageFacts:      api.LanguageFacts{SubtitleLanguages: []string{"English"}},
		PackageFacts: api.PackageFacts{
			Status:          api.MetadataEvidenceStatusComplete,
			KnownFileCount:  1,
			MediaFileCount:  1,
			DetectedSeasons: []int{1},
		},
		MediaFileFacts: api.MediaFileFacts{
			Status:            api.MetadataEvidenceStatusComplete,
			LanguageStatus:    api.MetadataEvidenceStatusComplete,
			ExpectedFileCount: 1,
			OriginalLanguage:  "fr",
			Files: []api.MediaFileFact{{
				FileName:          "Example.Release.2026.S01E01.1080p-GRP.mkv",
				AudioLanguages:    []string{"French"},
				SubtitleLanguages: []string{"English"},
			}},
		},
		AssetFacts: api.AssetFacts{
			MediaInfoText: api.AssetEvidence{
				Status: api.MetadataEvidenceStatusComplete,
				Ready:  true,
				Count:  1,
			},
		},
	}
}

func evaluateANTTVEvidence(t *testing.T, subject api.TrackerValidationSubject) []api.RuleFailure {
	t.Helper()
	failures, err := validationPolicy().Check(context.Background(), subject, api.NopLogger{})
	if err != nil {
		t.Fatalf("evaluate ANTTV evidence: %v", err)
	}
	return failures
}

func assertANTTVFailure(
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

func TestANTTVFinalizedLanguageAccessibility(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name                            string
		audio, subtitles                []string
		programmeStatus, subtitleStatus api.MetadataEvidenceStatus
		discType, uploadType            string
		wantStatus                      api.MetadataEvidenceStatus
	}{
		{
			name:      "foreign audio with English captions",
			audio:     []string{"Japanese"},
			subtitles: []string{"English"},
		},
		{name: "English audio without original metadata", audio: []string{"English"}},
		{name: "English captions without programme metadata", subtitles: []string{"English"}},
		{
			name:            "complete missing accessibility",
			audio:           []string{"Japanese"},
			programmeStatus: api.MetadataEvidenceStatusComplete,
			subtitleStatus:  api.MetadataEvidenceStatusComplete,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name:           "unknown programme evidence",
			subtitleStatus: api.MetadataEvidenceStatusComplete,
			wantStatus:     api.MetadataEvidenceStatusPartial,
		},
		{
			name:            "unknown subtitle evidence",
			audio:           []string{"Japanese"},
			programmeStatus: api.MetadataEvidenceStatusComplete,
			wantStatus:      api.MetadataEvidenceStatusPartial,
		},
		{
			name:       "full disc excluded",
			discType:   "BDMV",
			uploadType: "DISC",
		},
		{
			name:            "disc sourced remux assessed",
			discType:        "BDMV",
			uploadType:      "REMUX",
			audio:           []string{"Japanese"},
			programmeStatus: api.MetadataEvidenceStatusComplete,
			subtitleStatus:  api.MetadataEvidenceStatusComplete,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			subject := antTVPassingSubject()
			subject.Tracker = "ANT"
			subject.DiscType, subject.Type = tt.discType, tt.uploadType
			subject.BDInfoReady = tt.discType == "BDMV"
			subject.LanguageFacts = api.LanguageFacts{
				ProgrammeLanguages: tt.audio,
				SubtitleLanguages:  tt.subtitles,
				ProgrammeStatus:    tt.programmeStatus,
				SubtitleStatus:     tt.subtitleStatus,
			}
			failures, err := validationPolicy().Check(context.Background(), subject, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantStatus == "" {
				if len(failures) != 0 {
					t.Fatalf("unexpected failures: %+v", failures)
				}
			} else {
				assertANTTVFailure(t, failures, "language_subtitles", api.RuleDispositionStrict, tt.wantStatus)
			}
		})
	}
}

func TestANTTVContradictoryEnglishCorrectionDoesNotEstablishAccessibility(t *testing.T) {
	t.Parallel()
	media := api.MediaFacts{
		AudioLanguages:           []string{"English"},
		AudioLanguagesProvenance: api.FactProvenanceManual,
		TrackCoverageComplete:    true,
		Tracks: []api.MediaTrackFacts{
			{
				ID:        "programme",
				Kind:      api.MediaTrackAudio,
				Role:      api.AudioRoleProgramme,
				Languages: []string{"Japanese"},
			},
			{
				ID:        "commentary",
				Kind:      api.MediaTrackAudio,
				Role:      api.AudioRoleCommentary,
				Languages: []string{"English"},
			},
		},
	}
	subject := antTVPassingSubject()
	subject.Tracker = "ANT"
	subject.LanguageFacts = mediafacts.ResolveLanguages(media)
	if subject.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusContradictory {
		t.Fatal("regression fixture did not preserve contradiction")
	}
	failures, err := validationPolicy().Check(context.Background(), subject, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	assertANTTVFailure(t, failures, "language_subtitles", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.LanguageFacts.SubtitleLanguages = []string{"English"}
	failures, err = validationPolicy().Check(context.Background(), subject, api.NopLogger{})
	if err != nil || len(failures) != 0 {
		t.Fatalf("independent English captions did not establish accessibility: %v %+v", err, failures)
	}
	subject.LanguageFacts.SubtitleLanguages = nil
	subject.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusPartial
	failures, err = validationPolicy().Check(context.Background(), subject, api.NopLogger{})
	if err != nil || len(failures) != 0 {
		t.Fatalf("known English programme with unrelated partial evidence blocked: %v %+v", err, failures)
	}
}
