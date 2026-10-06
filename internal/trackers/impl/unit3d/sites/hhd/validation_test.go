// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import (
	"context"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"strconv"
	"testing"

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
		{
			name: "archive is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.ArchiveFileCount = 1
			},
			wantRule:        "hhd_package_safety",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "missing package evidence is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.Status = api.MetadataEvidenceStatusUnavailable
			},
			wantRule:        "hhd_package_safety",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusUnavailable,
		},
		{
			name: "too few screenshots is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.AssetFacts.HostedScreenshots.Count = 2
			},
			wantRule:        "hhd_required_assets_hosted_screenshot",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "missing screenshot evidence is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.AssetFacts.HostedScreenshots.Status = api.MetadataEvidenceStatusUnavailable
			},
			wantRule:        "hhd_required_assets_hosted_screenshot",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := hhdValidationSubject()
			if test.mutate != nil {
				test.mutate(&subject)
			}
			failures, err := validationPolicy().Check(context.Background(), subject, nil)
			if err != nil {
				t.Fatalf("validate HHD subject: %v", err)
			}
			if test.wantRule == "" {
				if len(failures) != 0 {
					t.Fatalf("unexpected failures: %#v", failures)
				}
				return
			}
			requireHHDValidationFailure(t, failures, test.wantRule, test.wantDisposition, test.wantStatus)
		})
	}
}

func TestRulesRequireEncodeSettings(t *testing.T) {
	t.Parallel()
	if !Rules().RequireValidMISetting {
		t.Fatal("HHD encode-settings rule is disabled")
	}
}

func hhdValidationSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Tracker:       "HHD",
		LanguageFacts: hhdTestLanguageFacts("English", []string{"English"}, []string{"English"}),
		Type:          "WEBDL",
		Container:     "mkv",
		PackageFacts: api.PackageFacts{
			Status:         api.MetadataEvidenceStatusComplete,
			KnownFileCount: 1,
			MediaFileCount: 1,
		},
		AssetFacts: api.AssetFacts{
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
}

func requireHHDValidationFailure(
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

// hhdTestLanguageFacts models inspected, identified programme streams and full
// embedded subtitles so unrelated fixtures satisfy the current facts contract.
func hhdTestLanguageFacts(original string, programme, subtitles []string) api.LanguageFacts {
	media := api.MediaFacts{
		OriginalLanguage:      original,
		SubtitleLanguages:     subtitles,
		TrackCoverageComplete: true,
		PrimaryAudioTrackID:   "audio-0",
	}
	for index, language := range programme {
		media.Tracks = append(media.Tracks, api.MediaTrackFacts{
			ID:        "audio-" + strconv.Itoa(index),
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleProgramme,
			Languages: []string{language},
			Codec:     "AC-3",
			Default:   index == 0,
		})
	}
	for index, language := range subtitles {
		media.Tracks = append(media.Tracks, api.MediaTrackFacts{
			ID:        "subtitle-" + strconv.Itoa(index),
			Kind:      api.MediaTrackSubtitle,
			Languages: []string{language},
			Default:   index == 0,
		})
	}
	return mediafacts.ResolveLanguages(media)
}

func TestHHDLanguageRequirementsRemainIndependent(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		audio     []string
		subtitles []string
		want      string
	}{
		{"original with English dub", []string{"Japanese", "English"}, []string{"English"}, ""},
		{"English dub does not replace subtitles", []string{"Japanese", "English"}, nil, "language_subtitles"},
		{"English dub does not replace original", []string{"English"}, []string{"English"}, "language_original"},
		{"one English dub only", []string{"Japanese", "English", "English"}, []string{"English"}, "language_english_dub_count"},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := hhdValidationSubject()
			subject.LanguageFacts = hhdTestLanguageFacts("Japanese", test.audio, test.subtitles)
			hhdAnswer(&subject, "english_subtitle_manager", "missing")
			failures := languageAssessment(subject)
			if test.want == "" {
				if len(failures) != 0 {
					t.Fatalf("compliant language facts blocked: %+v", failures)
				}
			} else {
				requireHHDValidationFailure(t, failures, test.want, api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
			}
		})
	}
}
