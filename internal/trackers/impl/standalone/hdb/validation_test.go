// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDBEvidencePolicyPassViolationAndMissingEvidence(t *testing.T) {
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
			wantRule:        "hdb_package_safety",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "missing package evidence is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.Status = api.MetadataEvidenceStatusUnavailable
			},
			wantRule:        "hdb_package_safety",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusUnavailable,
		},
		{
			name: "prohibited title is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.ReleaseName = "Example Release 2026 Complete 1080p-GRP"
				subject.SourcePath = subject.ReleaseName
			},
			wantRule:        "hdb_title_prohibition",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "scene title uses normal HDB name",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Scene = true
				subject.ReleaseName = "Example Release 2026 Complete 1080p-GRP"
				subject.SourcePath = subject.ReleaseName
				subject.Release.Resolution = "1080p"
				subject.Source = "WEB-DL"
				subject.ProviderMetadata.IMDB = &api.IMDBMetadata{
					IMDBID: 1234567,
					AKA:    "Example Release",
					Year:   2026,
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := hdbPassingSubject()
			if test.mutate != nil {
				test.mutate(&subject)
			}
			failures, err := validationPolicy().Check(context.Background(), subject, api.NopLogger{})
			if err != nil {
				t.Fatalf("validate HDB subject: %v", err)
			}
			if test.wantRule == "" {
				if len(failures) != 0 {
					t.Fatalf("unexpected failures: %#v", failures)
				}
				return
			}
			requireHDBValidationFailure(t, failures, test.wantRule, test.wantDisposition, test.wantStatus)
		})
	}
}

func TestHDBValidationPolicyVersion(t *testing.T) {
	t.Parallel()
	if got := Profile().ValidationPolicy.ID; got != "standalone-hdb-constructibility-v4" {
		t.Fatalf("validation policy ID = %q", got)
	}
}

func TestHDBTitleProhibitedElements(t *testing.T) {
	t.Parallel()
	tests := []struct {
		title      string
		prohibited bool
	}{
		{title: "Example Release 2026 REQ 1080p", prohibited: true},
		{title: "Example Release 2026 RESEED 1080p", prohibited: true},
		{title: "Example Release 2026 Complete Season Pack 1080p", prohibited: true},
		{title: "Example Release 2026 Series Limited Remastered 1080p", prohibited: true},
		{title: "Example Release 2026 Subbed nfofix DVD5 DVD9 1080p", prohibited: true},
		{title: "Example Release 2026 Multi-Lang 1080p", prohibited: true},
		{title: "Example Release 2026 MultiLang 1080p", prohibited: true},
		{title: "Example Release 2026 Dual-Audio 1080p", prohibited: true},
		{title: "Example Release 2026 DualAudio 1080p", prohibited: true},
		{title: "Example Release 2026 Dubbed 1080p", prohibited: true},
		{title: "Example Release 2026 German.DL 1080p", prohibited: true},
		{title: "Example Release 2026 1080p WEB-DL DD 5.1 H.264-GRP"},
	}
	for _, test := range tests {
		t.Run(test.title, func(t *testing.T) {
			t.Parallel()
			if got := hdbTitleContainsProhibitedElement(test.title); got != test.prohibited {
				t.Fatalf("prohibited = %t, want %t", got, test.prohibited)
			}
		})
	}
}

func TestValidationModelsProviderUnlistedManualMetadataBranch(t *testing.T) {
	t.Parallel()
	const sourcePath = "Example.Release.2026.1080p-GRP"
	base := api.TrackerValidationSubject{
		SourcePath: sourcePath,
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			Generation: 8,
			Category:   api.CanonicalCategoryMovie,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			Generation: 8,
			ProviderAvailability: []api.ProviderAvailabilityEvidence{{
				Provider: api.IdentityProviderIMDB,
				Status:   api.ProviderAvailabilityStatusNotFound,
				Source:   "imdb_title/v1",
			}},
		},
		Type:       "ENCODE",
		VideoCodec: "AVC",
	}

	t.Run("proved unlisted before final descriptions is advisory", func(t *testing.T) {
		failures, err := validationPolicy().Check(context.Background(), base, nil)
		if err != nil {
			t.Fatalf("validate unlisted release: %v", err)
		}
		requireHDBValidationFailure(
			t,
			failures,
			"hdb_manual_metadata_assets_pending",
			api.RuleDispositionAdvisory,
			api.MetadataEvidenceStatusPartial,
		)
	})

	t.Run("proved unlisted without final assets is strict", func(t *testing.T) {
		subject := base
		subject.DescriptionGroupsFinal = true
		failures, err := validationPolicy().Check(context.Background(), subject, nil)
		if err != nil {
			t.Fatalf("validate unlisted release: %v", err)
		}
		requireHDBValidationFailure(
			t,
			failures,
			"hdb_manual_metadata_assets",
			api.RuleDispositionStrict,
			api.MetadataEvidenceStatusComplete,
		)
	})

	t.Run("proved unlisted with assets passes metadata branch", func(t *testing.T) {
		subject := base
		subject.DescriptionOverride = "Manual synopsis.\n[img]https://img.example/poster.jpg[/img]"
		subject.DescriptionGroupsFinal = true
		failures, err := validationPolicy().Check(context.Background(), subject, nil)
		if err != nil {
			t.Fatalf("validate unlisted release: %v", err)
		}
		for _, failure := range failures {
			if strings.HasPrefix(failure.Rule, "hdb_manual_metadata") || strings.HasPrefix(failure.Rule, "hdb_provider_") {
				t.Fatalf("unexpected metadata-branch failure: %#v", failure)
			}
		}
	})

	t.Run("manual assets without not-found proof require confirmation", func(t *testing.T) {
		subject := base
		subject.ProviderMetadata.ProviderAvailability = nil
		subject.DescriptionOverride = "Manual synopsis.\n[img]https://img.example/poster.jpg[/img]"
		subject.DescriptionGroupsFinal = true
		failures, err := validationPolicy().Check(context.Background(), subject, nil)
		if err != nil {
			t.Fatalf("validate unproved unlisted release: %v", err)
		}
		requireHDBValidationFailure(
			t,
			failures,
			"hdb_provider_unlisted_confirmation",
			api.RuleDispositionWaivable,
			api.MetadataEvidenceStatusUnavailable,
		)
	})
}

func hdbPassingSubject() api.TrackerValidationSubject {
	const releaseName = "Example Release 2026 1080p WEB-DL H.264-GRP"
	return api.TrackerValidationSubject{
		SourcePath:  releaseName,
		ReleaseName: releaseName,
		Identity: api.ExternalIdentity{
			Category: api.CanonicalCategoryMovie,
			IMDBID:   1234567,
		},
		Type:       "ENCODE",
		VideoCodec: "AVC",
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
				FileName:   releaseName + ".mkv",
				VideoCodec: "AVC",
			}},
		},
		AssetFacts: api.AssetFacts{
			Status: api.MetadataEvidenceStatusComplete,
			MediaInfoText: api.AssetEvidence{
				Status: api.MetadataEvidenceStatusComplete,
				Ready:  true,
				Count:  1,
			},
		},
	}
}

func requireHDBValidationFailure(
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
