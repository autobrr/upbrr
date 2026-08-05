// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAitherEvidencePolicyPassViolationAndMissingEvidence(t *testing.T) {
	t.Parallel()

	subject := aitherPassingSubject()
	failures := evaluateAitherEvidence(t, subject)
	if len(failures) != 0 {
		t.Fatalf("passing subject failures: %+v", failures)
	}

	subject.PackageFacts.ArchiveFileCount = 1
	assertAitherFailure(
		t,
		evaluateAitherEvidence(t, subject),
		"aither_archive",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)

	subject = aitherPassingSubject()
	subject.PackageFacts.Status = api.MetadataEvidenceStatusPartial
	assertAitherFailure(
		t,
		evaluateAitherEvidence(t, subject),
		"aither_archive",
		api.RuleDispositionAdvisory,
		api.MetadataEvidenceStatusPartial,
	)
}

func TestAitherRequiresThreeScreenshots(t *testing.T) {
	t.Parallel()

	subject := aitherPassingSubject()
	subject.AssetFacts.Screenshots.Count = 2
	assertAitherFailure(
		t,
		evaluateAitherEvidence(t, subject),
		"aither_asset_screenshot",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)
}

func TestAitherValidationPolicyVersion(t *testing.T) {
	t.Parallel()

	if got := Profile().ValidationPolicy.ID; got != "unit3d-aither-policy-v2" {
		t.Fatalf("validation policy ID = %q", got)
	}
}

func aitherPassingSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		PackageFacts: api.PackageFacts{
			Status:         api.MetadataEvidenceStatusComplete,
			KnownFileCount: 1,
			MediaFileCount: 1,
		},
		MediaFileFacts: api.MediaFileFacts{
			Status:            api.MetadataEvidenceStatusComplete,
			LanguageStatus:    api.MetadataEvidenceStatusComplete,
			ExpectedFileCount: 1,
			OriginalLanguage:  "ja",
			Files: []api.MediaFileFact{{
				FileName:          "Example.Release.2026.1080p-GRP.mkv",
				AudioLanguages:    []string{"Japanese"},
				SubtitleLanguages: []string{"English"},
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

func evaluateAitherEvidence(t *testing.T, subject api.TrackerValidationSubject) []api.RuleFailure {
	t.Helper()
	failures, err := checkEvidenceRules(context.Background(), subject, api.NopLogger{})
	if err != nil {
		t.Fatalf("evaluate AITHER evidence: %v", err)
	}
	return failures
}

func assertAitherFailure(
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
