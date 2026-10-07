// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

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
			wantRule:        "bhd_package_safety",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "missing package evidence is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.Status = api.MetadataEvidenceStatusUnavailable
			},
			wantRule:        "bhd_package_safety",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusUnavailable,
		},
		{
			name: "too few screenshots is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.AssetFacts.HostedScreenshots.Count = 2
			},
			wantRule:        "bhd_required_assets_hosted_screenshot",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "missing screenshot evidence is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.AssetFacts.HostedScreenshots.Status = api.MetadataEvidenceStatusUnavailable
			},
			wantRule:        "bhd_required_assets_hosted_screenshot",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := bhdValidationSubject()
			if test.mutate != nil {
				test.mutate(&subject)
			}
			failures, err := validationPolicy().Check(context.Background(), subject, nil)
			if err != nil {
				t.Fatalf("validate BHD subject: %v", err)
			}
			if test.wantRule == "" {
				if len(failures) != 1 {
					t.Fatalf("unexpected failures: %#v", failures)
				}
				requireBHDValidationFailure(t, failures, "language_existing_release", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
				return
			}
			requireBHDValidationFailure(t, failures, test.wantRule, test.wantDisposition, test.wantStatus)
		})
	}
}

func TestRulesRequireEncodeSettings(t *testing.T) {
	t.Parallel()
	if !rules().RequireValidMISetting {
		t.Fatal("BHD encode-settings rule is disabled")
	}
}

func bhdValidationSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Tracker:       "BHD",
		LanguageFacts: bhdTestLanguageFacts("English", []string{"English"}, []string{"English"}),
		Source:        "WEB",
		Type:          "WEBDL",
		Container:     "mkv",
		Identity:      api.ExternalIdentity{TMDBID: 1234567},
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

func requireBHDValidationFailure(
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

// bhdTestLanguageFacts models inspected, identified programme streams and full
// embedded subtitles so unrelated fixtures satisfy the current facts contract.
func bhdTestLanguageFacts(original string, programme, subtitles []string) api.LanguageFacts {
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

func TestBHDLanguageRoleAndDiscBoundaries(t *testing.T) {
	t.Parallel()
	subject := bhdValidationSubject()
	subject.LanguageFacts = bhdTestLanguageFacts("Japanese", []string{"Japanese", "English"}, nil)
	requireBHDSourceWarnings(t, subject, "language_existing_release")
	subject.LanguageFacts.Tracks[0].Codec = "TrueHD"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
		ID:        "wrong-core",
		Kind:      api.MediaTrackAudio,
		Role:      api.AudioRoleCompatibility,
		Languages: []string{"German"},
		Codec:     "AC-3",
	})
	requireBHDValidationFailure(t, languageAssessment(subject), "language_compatibility_mix", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	subject.Type, subject.DiscType = "DISC", ""
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("canonical full disc acquired language restrictions: %+v", failures)
	}
}
