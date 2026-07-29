// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAZFamilyEvidencePolicyPassViolationAndMissingEvidence(t *testing.T) {
	t.Parallel()

	for _, site := range []string{"AZ", "CZ"} {
		t.Run(site, func(t *testing.T) {
			t.Parallel()

			subject := azPassingSubject(site)
			failures := evaluateAZEvidence(t, site, subject)
			if len(failures) != 0 {
				t.Fatalf("passing subject failures: %+v", failures)
			}

			subject.PackageFacts.ArchiveFileCount = 1
			assertAZFailure(
				t,
				evaluateAZEvidence(t, site, subject),
				"azfamily_archive",
				api.RuleDispositionStrict,
				api.MetadataEvidenceStatusComplete,
			)

			subject = azPassingSubject(site)
			subject.PackageFacts.Status = api.MetadataEvidenceStatusPartial
			assertAZFailure(
				t,
				evaluateAZEvidence(t, site, subject),
				"azfamily_archive",
				api.RuleDispositionAdvisory,
				api.MetadataEvidenceStatusPartial,
			)
		})
	}
}

func TestAZFamilyMissingCountryEvidenceFailsClosed(t *testing.T) {
	t.Parallel()

	for _, site := range []string{"AZ", "CZ"} {
		subject := azPassingSubject(site)
		subject.ProviderMetadata.TMDB = nil
		assertAZFailure(
			t,
			evaluateAZEvidence(t, site, subject),
			"country_evidence",
			api.RuleDispositionStrict,
			api.MetadataEvidenceStatusUnavailable,
		)
	}
}

func TestAZFamilyValidationPolicyVersions(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"AZ":  "azfamily-az-policy-v2",
		"CZ":  "azfamily-cz-policy-v2",
		"PHD": "azfamily-phd-constructibility-v1",
	}
	for site, expected := range tests {
		if got := New(site).ValidationPolicy().ID; got != expected {
			t.Fatalf("%s validation policy ID = %q, want %q", site, got, expected)
		}
	}
}

func azPassingSubject(site string) api.TrackerValidationSubject {
	country := "JP"
	originalLanguage := "ja"
	if site == "CZ" {
		country = "FR"
		originalLanguage = "fr"
	}
	return api.TrackerValidationSubject{
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		Release: api.ReleaseInfo{
			Resolution: "1080p",
			Type:       "webdl",
			Year:       2026,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{OriginCountry: []string{country}},
		},
		Type:      "webdl",
		Source:    "WEB",
		Container: "MKV",
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
			OriginalLanguage:  originalLanguage,
			Files: []api.MediaFileFact{
				{
					FileName:          "Example.Release.2026.S01E01.1080p-GRP.mkv",
					Container:         "MKV",
					VideoCodec:        "H.264",
					AudioLanguages:    []string{originalLanguage},
					SubtitleLanguages: []string{"English"},
				},
				{
					FileName:          "Example.Release.2026.S01E02.1080p-GRP.mkv",
					Container:         "MKV",
					VideoCodec:        "H.264",
					AudioLanguages:    []string{originalLanguage},
					SubtitleLanguages: []string{"English"},
				},
			},
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

func evaluateAZEvidence(t *testing.T, site string, subject api.TrackerValidationSubject) []api.RuleFailure {
	t.Helper()
	failures, err := New(site).evaluateRules(context.Background(), subject, api.NopLogger{})
	if err != nil {
		t.Fatalf("evaluate %s evidence: %v", site, err)
	}
	return failures
}

func assertAZFailure(
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
