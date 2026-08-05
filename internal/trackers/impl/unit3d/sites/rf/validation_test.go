// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rf

import (
	"context"
	"testing"

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

	if got := Profile().ValidationPolicy.ID; got != "unit3d-rf-policy-v2" {
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
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
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
