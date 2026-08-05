// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestNBLEvidencePolicyPassViolationAndMissingEvidence(t *testing.T) {
	t.Parallel()

	subject := nblPassingSubject()
	failures := evaluateNBLEvidence(t, subject)
	if len(failures) != 0 {
		t.Fatalf("passing subject failures: %+v", failures)
	}

	subject.PackageFacts.ArchiveFileCount = 1
	assertNBLFailure(
		t,
		evaluateNBLEvidence(t, subject),
		"nbl_archive",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)

	subject = nblPassingSubject()
	subject.PackageFacts.Status = api.MetadataEvidenceStatusPartial
	assertNBLFailure(
		t,
		evaluateNBLEvidence(t, subject),
		"nbl_archive",
		api.RuleDispositionAdvisory,
		api.MetadataEvidenceStatusPartial,
	)
}

func TestNBLRejectsEpisodeFolderAndScreenshots(t *testing.T) {
	t.Parallel()

	subject := nblPassingSubject()
	subject.PackageFacts.SingleFileFolder = true
	assertNBLFailure(
		t,
		evaluateNBLEvidence(t, subject),
		"nbl_episode_layout",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)

	subject = nblPassingSubject()
	subject.AssetFacts.Screenshots.Ready = true
	subject.AssetFacts.Screenshots.Count = 1
	assertNBLFailure(
		t,
		evaluateNBLEvidence(t, subject),
		"nbl_screenshots",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)
}

func TestNBLDoesNotApplyMediaOnlyPackageRuleToDiscs(t *testing.T) {
	t.Parallel()

	subject := nblPassingSubject()
	subject.DiscType = "DVD"
	subject.PackageFacts.KnownFileCount = 3
	subject.PackageFacts.MediaFileCount = 1
	for _, failure := range evaluateNBLEvidence(t, subject) {
		if failure.Rule == "nbl_media_only" {
			t.Fatalf("disc package received media-only failure: %+v", failure)
		}
	}
}

func TestNBLValidationPolicyVersion(t *testing.T) {
	t.Parallel()

	if got := Profile().ValidationPolicy.ID; got != "standalone-nbl-policy-v2" {
		t.Fatalf("validation policy ID = %q", got)
	}
}

func nblPassingSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV},
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
			Screenshots: api.AssetEvidence{Status: api.MetadataEvidenceStatusComplete},
		},
	}
}

func evaluateNBLEvidence(t *testing.T, subject api.TrackerValidationSubject) []api.RuleFailure {
	t.Helper()
	failures, err := checkEvidenceRules(context.Background(), subject, api.NopLogger{})
	if err != nil {
		t.Fatalf("evaluate NBL evidence: %v", err)
	}
	return failures
}

func assertNBLFailure(
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
