// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// ValidationPolicy returns SAM's deterministic content and language rules.
func ValidationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{ID: "unit3d-sam-policy-v1", Check: checkRequirements}
}

func checkRequirements(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := samContentStructureFailures(subject)
	return append(failures, samLanguageFailures(subject)...), nil
}

func samContentStructureFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	facts := subject.PackageFacts
	switch subject.Identity.Category {
	case api.CanonicalCategoryMovie:
		if facts.Status != api.MetadataEvidenceStatusComplete {
			return []api.RuleFailure{samEvidenceFailure(
				"sam_movie_structure",
				"complete package evidence is required to prove one movie per torrent",
				facts.Status,
			)}
		}
		if facts.MediaFileCount != 1 {
			return []api.RuleFailure{samStrictFailure("sam_movie_structure", "movies must contain exactly one main video file", facts.Status)}
		}
	case api.CanonicalCategoryTV:
		if facts.Status != api.MetadataEvidenceStatusComplete {
			return []api.RuleFailure{samEvidenceFailure("sam_tv_structure", "complete single-season package evidence is required", facts.Status)}
		}
		if len(facts.DetectedSeasons) != 1 {
			return []api.RuleFailure{samStrictFailure("sam_tv_structure", "TV uploads must contain exactly one season", facts.Status)}
		}
		if subject.SeasonInt > 0 && !slices.Equal(facts.DetectedSeasons, []int{subject.SeasonInt}) {
			return []api.RuleFailure{samStrictFailure("sam_tv_structure", "package season does not match canonical season metadata", facts.Status)}
		}
		if !subject.TVPack {
			if facts.MediaFileCount != 1 || samEpisodeCount(facts.DetectedEpisodes) != 1 {
				return []api.RuleFailure{samStrictFailure("sam_tv_structure", "non-pack TV uploads must contain exactly one episode", facts.Status)}
			}
			return nil
		}
		status, known := samSeriesStatus(subject)
		if !known {
			return []api.RuleFailure{samStrictFailure(
				"sam_season_pack_status",
				"a current series status is required for season packs",
				api.MetadataEvidenceStatusUnavailable,
			)}
		}
		if !samCompletedSeriesStatus(status) {
			return []api.RuleFailure{samStrictFailure(
				"sam_season_pack_status",
				"season packs are allowed only for ended, cancelled, or completed series",
				api.MetadataEvidenceStatusComplete,
			)}
		}
	case api.CanonicalCategoryUnknown:
		return nil
	}
	return nil
}

func samLanguageFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	facts := subject.MediaFileFacts
	original := languageutil.NormalizeLanguageCode(facts.OriginalLanguage)
	if original == "" {
		return []api.RuleFailure{samEvidenceFailure("sam_language", "original-language evidence is required", facts.LanguageStatus)}
	}
	if original == "pt" {
		return nil
	}
	if facts.LanguageStatus != api.MetadataEvidenceStatusComplete || facts.ExpectedFileCount <= 0 || len(facts.Files) != facts.ExpectedFileCount {
		return []api.RuleFailure{samEvidenceFailure("sam_language", "complete audio and subtitle evidence is required", facts.LanguageStatus)}
	}
	for _, file := range facts.Files {
		if !samContainsLanguage(file.AudioLanguages, original) {
			return []api.RuleFailure{samStrictFailure("sam_language", "non-Portuguese content requires original-language audio", facts.LanguageStatus)}
		}
		if !samContainsLanguage(file.SubtitleLanguages, "pt") {
			return []api.RuleFailure{samStrictFailure("sam_language", "non-Portuguese content requires Portuguese subtitles", facts.LanguageStatus)}
		}
	}
	return nil
}

func samEpisodeCount(values []api.SeasonEpisodeFacts) int {
	count := 0
	for _, value := range values {
		count += len(value.Episodes)
	}
	return count
}

func samSeriesStatus(subject api.TrackerValidationSubject) (string, bool) {
	if !subject.ProviderMetadata.IsCurrentFor(subject.SourcePath, subject.Identity) {
		return "", false
	}
	if subject.ProviderMetadata.TVDB != nil && strings.TrimSpace(subject.ProviderMetadata.TVDB.Status) != "" {
		return subject.ProviderMetadata.TVDB.Status, true
	}
	if subject.ProviderMetadata.TVmaze != nil && strings.TrimSpace(subject.ProviderMetadata.TVmaze.Status) != "" {
		return subject.ProviderMetadata.TVmaze.Status, true
	}
	return "", false
}

func samCompletedSeriesStatus(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ended", "cancelled", "canceled", "completed", "complete":
		return true
	default:
		return false
	}
}

func samContainsLanguage(values []string, language string) bool {
	return slices.ContainsFunc(values, func(value string) bool {
		return languageutil.NormalizeLanguageCode(value) == language
	})
}

func samStrictFailure(rule string, reason string, status api.MetadataEvidenceStatus) api.RuleFailure {
	return trackers.NewEvidenceRuleFailure(rule, reason, api.RuleDispositionStrict, status)
}

func samEvidenceFailure(rule string, reason string, status api.MetadataEvidenceStatus) api.RuleFailure {
	return trackers.NewEvidenceRuleFailure(rule, reason, api.RuleDispositionAdvisory, status)
}
