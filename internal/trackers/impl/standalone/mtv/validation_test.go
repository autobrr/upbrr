// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestMTVEvidencePolicyPassViolationAndMissingEvidence(t *testing.T) {
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
			wantRule:        "mtv_package_safety",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "missing package evidence is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.Status = api.MetadataEvidenceStatusUnavailable
			},
			wantRule:        "mtv_package_safety",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusUnavailable,
		},
		{
			name: "unproved group token is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Tag = "-P2P"
			},
			wantRule:        "mtv_group_token",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "iTunes WEB-DL is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Service = "iT"
			},
			wantRule:        "mtv_itunes_webdl",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "missing TVDB title is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.ProviderMetadata.TVDB.NameEnglish = ""
			},
			wantRule:        "mtv_tvdb_title",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := mtvPassingSubject()
			if test.mutate != nil {
				test.mutate(&subject)
			}
			failures, err := validationPolicy().Check(context.Background(), subject, api.NopLogger{})
			if err != nil {
				t.Fatalf("validate MTV subject: %v", err)
			}
			if test.wantRule == "" {
				if len(failures) != 0 {
					t.Fatalf("unexpected failures: %#v", failures)
				}
				return
			}
			requireMTVValidationFailure(t, failures, test.wantRule, test.wantDisposition, test.wantStatus)
		})
	}
}

func TestMTVValidationPolicyVersion(t *testing.T) {
	t.Parallel()
	if got := Profile().ValidationPolicy.ID; got != "standalone-mtv-constructibility-v2" {
		t.Fatalf("validation policy ID = %q", got)
	}
}

func TestMTVAdultContentValidationUsesCurrentMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mutate   func(*api.TrackerValidationSubject)
		wantRule string
	}{
		{
			name: "current provider adult classification blocks",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.ProviderMetadata.TVDB.Genres = "Adult"
			},
			wantRule: "mtv_block_adult",
		},
		{
			name: "non-adult classification passes",
		},
		{
			name: "stale provider adult classification does not block",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.ProviderMetadata.SourcePath = "stale-source"
				subject.ProviderMetadata.TVDB.Genres = "Adult"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			subject := mtvPassingSubject()
			if test.mutate != nil {
				test.mutate(&subject)
			}
			failures := mtvAdultContentFailures(subject)
			if test.wantRule == "" {
				if len(failures) != 0 {
					t.Fatalf("unexpected adult-content failures: %#v", failures)
				}
				return
			}
			requireMTVValidationFailure(
				t,
				failures,
				test.wantRule,
				api.RuleDispositionStrict,
				api.MetadataEvidenceStatusComplete,
			)
		})
	}
}

func TestMTVFullDVDAllowsRequiredDiscStructureFiles(t *testing.T) {
	t.Parallel()

	subject := api.TrackerValidationSubject{
		DiscType: "DVD",
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		Release:  api.ReleaseInfo{Resolution: "480p"},
		Type:     "DISC",
		Tag:      "-NOGRP",
		PackageFacts: api.PackageFacts{
			Status:         api.MetadataEvidenceStatusComplete,
			KnownFileCount: 3,
			MediaFileCount: 1,
			ExtraKinds:     []api.PackageFileKind{api.PackageFileKindOther},
		},
		MediaFileFacts: api.MediaFileFacts{
			Status:            api.MetadataEvidenceStatusComplete,
			TechnicalStatus:   api.MetadataEvidenceStatusComplete,
			LanguageStatus:    api.MetadataEvidenceStatusComplete,
			ExpectedFileCount: 1,
			Files: []api.MediaFileFact{{
				FileName: "VIDEO_TS/VTS_01_1.VOB",
			}},
		},
		ProvenanceFacts: api.ProvenanceFacts{Status: api.MetadataEvidenceStatusComplete},
	}
	failures, err := validationPolicy().Check(context.Background(), subject, api.NopLogger{})
	if err != nil {
		t.Fatalf("validate MTV DVD: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("MTV DVD failures = %#v", failures)
	}
}

func mtvPassingSubject() api.TrackerValidationSubject {
	const sourcePath = "Example.Show.S01.1080p.WEB-DL.H.264-GRP"
	files := []api.MediaFileFact{
		mtvUniformFile("Example.Show.S01E01.1080p.WEB-DL.H.264-GRP.mkv"),
		mtvUniformFile("Example.Show.S01E02.1080p.WEB-DL.H.264-GRP.mkv"),
	}
	return api.TrackerValidationSubject{
		SourcePath: sourcePath,
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			Generation: 2,
			Category:   api.CanonicalCategoryTV,
			TVDBID:     7654321,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			Generation: 2,
			TVDB: &api.TVDBMetadata{
				TVDBID:      7654321,
				NameEnglish: "Example Show",
				Genres:      "Drama",
			},
		},
		Release:    api.ReleaseInfo{Resolution: "1080p"},
		Type:       "WEBDL",
		VideoCodec: "AVC",
		TVPack:     true,
		Tag:        "-GRP",
		PackageFacts: api.PackageFacts{
			Status:         api.MetadataEvidenceStatusComplete,
			KnownFileCount: 2,
			MediaFileCount: 2,
		},
		MediaFileFacts: api.MediaFileFacts{
			Status:            api.MetadataEvidenceStatusComplete,
			TechnicalStatus:   api.MetadataEvidenceStatusComplete,
			LanguageStatus:    api.MetadataEvidenceStatusComplete,
			ExpectedFileCount: 2,
			Files:             files,
		},
		ProvenanceFacts: api.ProvenanceFacts{Status: api.MetadataEvidenceStatusComplete},
	}
}

func mtvUniformFile(fileName string) api.MediaFileFact {
	return api.MediaFileFact{
		FileName:          fileName,
		Source:            "WEB",
		Resolution:        "1080p",
		VideoCodec:        "AVC",
		VideoEncode:       "H.264",
		AudioLanguages:    []string{"English"},
		SubtitleLanguages: []string{"English"},
	}
}

func requireMTVValidationFailure(
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
