// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"context"
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
		{name: "non encode 1080p HEVC passes"},
		{
			name: "archive is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.ArchiveFileCount = 1
			},
			wantRule:        "ulcx_package_safety",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "missing package evidence is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.Status = api.MetadataEvidenceStatusUnavailable
			},
			wantRule:        "ulcx_package_safety",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusUnavailable,
		},
		{
			name: "too few screenshots is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.AssetFacts.HostedScreenshots.Count = 2
			},
			wantRule:        "ulcx_required_assets_hosted_screenshot",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "missing screenshot evidence is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.AssetFacts.HostedScreenshots.Status = api.MetadataEvidenceStatusUnavailable
			},
			wantRule:        "ulcx_required_assets_hosted_screenshot",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusUnavailable,
		},
		{
			name: "proved live action HD x265 source is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type = "ENCODE"
				subject.VideoEncode = "x265"
				subject.MediaFileFacts.Files[0].Source = "1080p Blu-ray"
			},
			wantRule:        "hevc_resolution_2160p",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "ambiguous x265 source is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type = "ENCODE"
				subject.VideoEncode = "x265"
				subject.MediaFileFacts.Files[0].Source = "Blu-ray"
			},
			wantRule:        "ulcx_x265_source",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := ulcxValidationSubject()
			if test.mutate != nil {
				test.mutate(&subject)
			}
			failures, err := ValidationPolicy().Check(context.Background(), subject, nil)
			if err != nil {
				t.Fatalf("validate ULCX subject: %v", err)
			}
			if test.wantRule == "" {
				if len(failures) != 0 {
					t.Fatalf("unexpected failures: %#v", failures)
				}
				return
			}
			requireULCXValidationFailure(t, failures, test.wantRule, test.wantDisposition, test.wantStatus)
		})
	}
}

func TestRulesRequireEncodeSettings(t *testing.T) {
	t.Parallel()
	if !Rules().RequireValidMISetting {
		t.Fatal("ULCX encode-settings rule is disabled")
	}
}

func ulcxValidationSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Type:       "WEBDL",
		Container:  "mkv",
		VideoCodec: "HEVC",
		Release:    api.ReleaseInfo{Resolution: "1080p"},
		PackageFacts: api.PackageFacts{
			Status:         api.MetadataEvidenceStatusComplete,
			KnownFileCount: 1,
			MediaFileCount: 1,
		},
		MediaFileFacts: api.MediaFileFacts{
			Status:            api.MetadataEvidenceStatusComplete,
			TechnicalStatus:   api.MetadataEvidenceStatusComplete,
			LanguageStatus:    api.MetadataEvidenceStatusComplete,
			ExpectedFileCount: 1,
			OriginalLanguage:  "English",
			Files: []api.MediaFileFact{{
				FileName:        "Example.Release.2026.1080p.WEB-DL-GRP.mkv",
				Container:       "MKV",
				Source:          "WEB",
				Resolution:      "1080p",
				VideoCodec:      "HEVC",
				VideoTrackCount: 1,
				AudioLanguages:  []string{"English"},
			}},
		},
		AssetFacts: api.AssetFacts{
			Status:            api.MetadataEvidenceStatusComplete,
			MediaInfoText:     api.AssetEvidence{
Status: api.MetadataEvidenceStatusComplete,
 Ready: true,
 Count: 1,
},
			HostedScreenshots: api.AssetEvidence{
Status: api.MetadataEvidenceStatusComplete,
 Ready: true,
 Count: 3,
},
		},
	}
}

func requireULCXValidationFailure(
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
