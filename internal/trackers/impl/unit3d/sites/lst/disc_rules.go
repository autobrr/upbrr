// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func discQuestionKey(subject api.TrackerValidationSubject, key string) string {
	fingerprint, _ := api.CanonicalWorkflowFingerprint(struct {
		Disc         api.DiscFacts
		Files        []string
		Presentation string
	}{subject.Disc, subject.FileList, subject.Is3D})
	return trackers.LanguageQuestionKey(subject, "disc_"+key+"_"+string(fingerprint))
}

func discReviewQuestions(subject api.TrackerValidationSubject) []api.TrackerQuestionnaireField {
	if !trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	fields := []api.TrackerQuestionnaireField{
		{
			Key:     "source",
			Label:   "Disc retail source",
			Options: []string{"unresolved", "retail_source", "not_retail_source"},
		},
		{
			Key:     "contents",
			Label:   "Disc contents and physical integrity",
			Options: []string{"unresolved", "complete_original_menus_extras_unsplit", "missing_or_removed_content", "split_physical_disc"},
		},
		{
			Key:     "protection",
			Label:   "Disc protection and region restrictions",
			Options: []string{"unresolved", "removed_and_region_unlocked", "still_restricted"},
		},
		{
			Key:     "form",
			Label:   "Disc modification status",
			Options: []string{"unresolved", "unmodified", "custom"},
		},
	}
	if len(subject.Disc.Items) > 1 {
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:     "set",
				Label:   "Multi-disc retail set",
				Options: []string{"unresolved", "cohesive_retail_set", "unrelated_bundle", "combined_mixed_resolution_main_features"},
				Help:    "Different resolutions on one physical disc and extra-only bonus discs do not require splitting a physical disc.",
			},
		)
	}
	if subject.QuestionnaireAnswers[discQuestionKey(subject, "form")] == "custom" {
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:     "custom_availability",
				Label:   "Unmodified same-disc availability",
				Options: []string{"unresolved", "no_unmodified_same_disc", "unmodified_same_disc_exists"},
				Help:    "Review the exact disc identity, edition, region and content across available sources. A matching movie title alone is insufficient.",
			},
		)
	}
	for i := range fields {
		fields[i].Key = discQuestionKey(subject, fields[i].Key)
		fields[i].Kind = "select"
		fields[i].Required = true
		fields[i].Value = subject.QuestionnaireAnswers[fields[i].Key]
		fields[i].NeedsReview = fields[i].Value == "" || fields[i].Value == "unresolved"
		if fields[i].Help == "" {
			fields[i].Help = "Review the exact prepared disc inventory and source records; this records your evidence rather than an automatic provenance check."
		}
	}
	return fields
}

func discReviewFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	var failures []api.RuleFailure
	for _, field := range discReviewQuestions(subject) {
		value := subject.QuestionnaireAnswers[field.Key]
		unresolved := value == "" || value == "unresolved" || !slices.Contains(field.Options, value)
		violation := slices.Contains(
			[]string{
				"not_retail_source",
				"missing_or_removed_content",
				"split_physical_disc",
				"still_restricted",
				"unmodified_same_disc_exists",
				"unrelated_bundle",
				"combined_mixed_resolution_main_features",
			},
			value,
		)
		if !unresolved && !violation {
			continue
		}
		failure := trackers.NewQuestionnaireRuleFailure("lst_disc_review", fmt.Sprintf("%s: %s", field.Label, value))
		failure.EvidenceStatus = api.MetadataEvidenceStatusComplete
		failure.DebugBypass = true
		if unresolved {
			failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
			failure.Reason = field.Label + " requires source-bound review"
		}
		failures = append(failures, failure)
	}
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) && slices.Contains(subject.PackageFacts.Extensions, ".iso") &&
		(!strings.EqualFold(subject.Is3D, "3D") || !strings.EqualFold(subject.DiscType, "BDMV")) {
		failure := trackers.NewEvidenceRuleFailure(
			"lst_disc_iso",
			"ISO requires established 3D Blu-ray evidence",
			api.RuleDispositionStrict,
			api.MetadataEvidenceStatusPartial,
		)
		failure.DebugBypass = true
		failures = append(failures, failure)
	}
	return failures
}
