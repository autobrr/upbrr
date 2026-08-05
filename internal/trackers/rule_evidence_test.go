// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestPackagePredicatesFailSafelyAndPreservePositiveEvidence(t *testing.T) {
	t.Parallel()

	policy := EvidencePredicatePolicy{
		Rule:                       "package_policy",
		ViolationDisposition:       api.RuleDispositionStrict,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	}
	partial := api.PackageFacts{
		Status:         api.MetadataEvidenceStatusPartial,
		KnownFileCount: 1,
		MediaFileCount: 1,
	}
	failures := ValidateMediaOnlyPackage(partial, policy)
	assertEvidenceFailure(
		t,
		failures,
		"package_policy",
		api.RuleDispositionAdvisory,
		api.MetadataEvidenceStatusPartial,
	)

	partial.ArchiveFileCount = 1
	failures = ValidatePackageExtensions(partial, PackageExtensionPolicy{
		Evidence:      policy,
		BlockArchives: true,
	})
	assertEvidenceFailure(
		t,
		failures,
		"package_policy",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusPartial,
	)

	complete := api.PackageFacts{
		Status:          api.MetadataEvidenceStatusComplete,
		KnownFileCount:  2,
		MediaFileCount:  2,
		DetectedSeasons: []int{1},
	}
	if failures := ValidateMediaOnlyPackage(complete, policy); len(failures) != 0 {
		t.Fatalf("complete media-only package failed: %+v", failures)
	}
	if failures := ValidateMultiSeasonPackage(complete, policy); len(failures) != 0 {
		t.Fatalf("single-season package failed: %+v", failures)
	}
	complete.DetectedSeasons = []int{1, 2}
	assertEvidenceFailure(
		t,
		ValidateMultiSeasonPackage(complete, policy),
		"package_policy",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)
}

func TestValidateCompleteEpisodeRange(t *testing.T) {
	t.Parallel()

	policy := EpisodeRangePolicy{
		Evidence: EvidencePredicatePolicy{
			Rule:                       "episode_range",
			ViolationDisposition:       api.RuleDispositionStrict,
			MissingEvidenceDisposition: api.RuleDispositionWaivable,
		},
		Season:       1,
		FirstEpisode: 1,
		LastEpisode:  3,
	}
	complete := api.PackageFacts{
		Status: api.MetadataEvidenceStatusComplete,
		DetectedEpisodes: []api.SeasonEpisodeFacts{{
			Season:   1,
			Episodes: []int{1, 2, 3},
		}},
	}
	if failures := ValidateCompleteEpisodeRange(complete, policy); len(failures) != 0 {
		t.Fatalf("complete episode range failed: %+v", failures)
	}
	complete.DetectedEpisodes[0].Episodes = []int{1, 3}
	assertEvidenceFailure(
		t,
		ValidateCompleteEpisodeRange(complete, policy),
		"episode_range",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)
	complete.Status = api.MetadataEvidenceStatusPartial
	assertEvidenceFailure(
		t,
		ValidateCompleteEpisodeRange(complete, policy),
		"episode_range",
		api.RuleDispositionWaivable,
		api.MetadataEvidenceStatusPartial,
	)
}

func TestValidatePerFileUniformity(t *testing.T) {
	t.Parallel()

	policy := PerFileUniformityPolicy{
		Evidence: EvidencePredicatePolicy{
			Rule:                       "uniformity",
			ViolationDisposition:       api.RuleDispositionStrict,
			MissingEvidenceDisposition: api.RuleDispositionAdvisory,
		},
		Fields: []MediaUniformityField{MediaUniformityFieldVideoCodec},
	}
	facts := api.MediaFileFacts{
		Status:            api.MetadataEvidenceStatusComplete,
		ExpectedFileCount: 2,
		Files: []api.MediaFileFact{
			{FileName: "Example.Show.S01E01.mkv", VideoCodec: "H.265"},
			{FileName: "Example.Show.S01E02.mkv", VideoCodec: "H.265"},
		},
	}
	if failures := ValidatePerFileUniformity(facts, policy); len(failures) != 0 {
		t.Fatalf("uniform media files failed: %+v", failures)
	}
	facts.Files[1].VideoCodec = "H.264"
	assertEvidenceFailure(
		t,
		ValidatePerFileUniformity(facts, policy),
		"uniformity",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)
	facts.Files[1].VideoCodec = "H.265"
	facts.Status = api.MetadataEvidenceStatusPartial
	assertEvidenceFailure(
		t,
		ValidatePerFileUniformity(facts, policy),
		"uniformity",
		api.RuleDispositionAdvisory,
		api.MetadataEvidenceStatusPartial,
	)
}

func TestValidateMediaConstraints(t *testing.T) {
	t.Parallel()

	policy := MediaConstraintPolicy{
		Evidence: EvidencePredicatePolicy{
			Rule:                       "media_constraints",
			ViolationDisposition:       api.RuleDispositionStrict,
			MissingEvidenceDisposition: api.RuleDispositionWaivable,
		},
		AllowedContainers:  []string{"MKV"},
		AllowedVideoCodecs: []string{"H.265"},
		AllowedSources:     []string{"WEB-DL"},
		AllowedResolutions: []string{"1080p"},
		MinBitDepth:        10,
		MinVideoTrackCount: 1,
		MaxVideoTrackCount: 1,
		AllowedCombinations: []MediaCombination{{
			Container:  "MKV",
			VideoCodec: "H.265",
			Source:     "WEB-DL",
			Resolution: "1080p",
			BitDepth:   "10",
		}},
	}
	facts := api.MediaFileFacts{
		Status:            api.MetadataEvidenceStatusComplete,
		TechnicalStatus:   api.MetadataEvidenceStatusComplete,
		ExpectedFileCount: 1,
		Files: []api.MediaFileFact{{
			FileName:        "Example.Release.2026.mkv",
			Container:       "MKV",
			VideoCodec:      "H.265",
			Source:          "WEB-DL",
			Resolution:      "1080p",
			BitDepth:        "10",
			VideoTrackCount: 1,
		}},
	}
	if failures := ValidateMediaConstraints(facts, policy); len(failures) != 0 {
		t.Fatalf("valid media constraints failed: %+v", failures)
	}
	facts.Files[0].Container = "MP4"
	assertEvidenceFailure(
		t,
		ValidateMediaConstraints(facts, policy),
		"media_constraints",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)
	facts.Files[0].Container = ""
	facts.TechnicalStatus = api.MetadataEvidenceStatusPartial
	assertEvidenceFailure(
		t,
		ValidateMediaConstraints(facts, policy),
		"media_constraints",
		api.RuleDispositionWaivable,
		api.MetadataEvidenceStatusPartial,
	)
}

func TestValidateRequiredAssets(t *testing.T) {
	t.Parallel()

	policy := RequiredAssetPolicy{
		Evidence: EvidencePredicatePolicy{
			Rule:                       "prepared",
			ViolationDisposition:       api.RuleDispositionStrict,
			MissingEvidenceDisposition: api.RuleDispositionAdvisory,
		},
		Requirements: []AssetRequirement{
			{Kind: AssetKindMediaInfoJSON},
			{Kind: AssetKindScreenshot, MinimumCount: 2},
		},
	}
	facts := api.AssetFacts{
		MediaInfoJSON: api.AssetEvidence{
			Status: api.MetadataEvidenceStatusComplete,
			Ready:  true,
			Count:  1,
		},
		Screenshots: api.AssetEvidence{
			Status: api.MetadataEvidenceStatusComplete,
			Ready:  true,
			Count:  2,
		},
	}
	if failures := ValidateRequiredAssets(facts, policy); len(failures) != 0 {
		t.Fatalf("required assets failed: %+v", failures)
	}
	facts.Screenshots.Count = 1
	assertEvidenceFailure(
		t,
		ValidateRequiredAssets(facts, policy),
		"prepared_screenshot",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)
	facts.Screenshots.Status = api.MetadataEvidenceStatusUnavailable
	assertEvidenceFailure(
		t,
		ValidateRequiredAssets(facts, policy),
		"prepared_screenshot",
		api.RuleDispositionAdvisory,
		api.MetadataEvidenceStatusUnavailable,
	)
}

func TestValidateLanguageCombination(t *testing.T) {
	t.Parallel()

	policy := LanguageCombinationPolicy{
		Evidence: EvidencePredicatePolicy{
			Rule:                       "language",
			ViolationDisposition:       api.RuleDispositionStrict,
			MissingEvidenceDisposition: api.RuleDispositionWaivable,
		},
		RequireOriginalAudio:               true,
		RequireEnglishSubtitleWithoutAudio: true,
	}
	facts := api.MediaFileFacts{
		Status:            api.MetadataEvidenceStatusComplete,
		LanguageStatus:    api.MetadataEvidenceStatusComplete,
		ExpectedFileCount: 1,
		OriginalLanguage:  "ja",
		Files: []api.MediaFileFact{{
			FileName:          "Example.Release.2026.mkv",
			AudioLanguages:    []string{"Japanese"},
			SubtitleLanguages: []string{"English"},
		}},
	}
	if failures := ValidateLanguageCombination(facts, policy); len(failures) != 0 {
		t.Fatalf("valid language combination failed: %+v", failures)
	}
	facts.Files[0].SubtitleLanguages = []string{"Japanese"}
	assertEvidenceFailure(
		t,
		ValidateLanguageCombination(facts, policy),
		"language",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)
	facts.LanguageStatus = api.MetadataEvidenceStatusPartial
	assertEvidenceFailure(
		t,
		ValidateLanguageCombination(facts, policy),
		"language",
		api.RuleDispositionWaivable,
		api.MetadataEvidenceStatusPartial,
	)
}

func TestNormalizeRuleFailurePreservesEvidence(t *testing.T) {
	t.Parallel()

	normalized := NormalizeRuleFailure(api.RuleFailure{
		Rule:           " evidence ",
		Reason:         " incomplete ",
		Disposition:    api.RuleDispositionAdvisory,
		EvidenceStatus: api.MetadataEvidenceStatusPartial,
	})
	if normalized.Rule != "evidence" ||
		normalized.Reason != "incomplete" ||
		normalized.Disposition != api.RuleDispositionAdvisory ||
		normalized.EvidenceStatus != api.MetadataEvidenceStatusPartial {
		t.Fatalf("normalized failure = %+v", normalized)
	}
}

func assertEvidenceFailure(
	t *testing.T,
	failures []api.RuleFailure,
	rule string,
	disposition api.RuleDisposition,
	status api.MetadataEvidenceStatus,
) {
	t.Helper()
	if len(failures) != 1 {
		t.Fatalf("failure count = %d, want 1: %+v", len(failures), failures)
	}
	failure := failures[0]
	if failure.Rule != rule || failure.Disposition != disposition || failure.EvidenceStatus != status {
		t.Fatalf("failure = %+v, want rule=%q disposition=%q evidence=%q", failure, rule, disposition, status)
	}
}
