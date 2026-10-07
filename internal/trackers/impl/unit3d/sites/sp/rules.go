// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import (
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Rules leaves evidence-backed release checks to the
// versioned validation policy.
func Rules() *trackers.RuleSet { return &trackers.RuleSet{} }

type packEvidence struct {
	differences         []string
	missing             []string
	sourceUnknown       bool
	technicalDifference bool
}

// assessPackEvidence compares measured components without mistaking unknown
// source provenance, or inspected track absence, for a failed media probe.
func assessPackEvidence(subject api.TrackerValidationSubject) packEvidence {
	facts := subject.MediaFileFacts
	var evidence packEvidence
	if facts.ExpectedFileCount <= 0 || len(facts.Files) != facts.ExpectedFileCount ||
		(subject.PackageFacts.MediaFileCount > 0 && subject.PackageFacts.MediaFileCount != facts.ExpectedFileCount) {
		evidence.missing = append(evidence.missing, "every media file in the current package")
	}
	if facts.Status == api.MetadataEvidenceStatusContradictory || facts.TechnicalStatus == api.MetadataEvidenceStatusContradictory ||
		facts.LanguageStatus == api.MetadataEvidenceStatusContradictory {
		evidence.missing = append(evidence.missing, "non-contradictory per-file evidence")
	}
	for _, field := range []struct {
		label string
		value func(api.MediaFileFact) (string, bool)
	}{
		{"source", func(file api.MediaFileFact) (string, bool) { return file.Source, strings.TrimSpace(file.Source) != "" }},
		{"resolution", func(file api.MediaFileFact) (string, bool) {
			return file.Resolution, strings.TrimSpace(file.Resolution) != ""
		}},
		{"video codec", func(file api.MediaFileFact) (string, bool) {
			return file.VideoCodec, strings.TrimSpace(file.VideoCodec) != ""
		}},
		{"container", func(file api.MediaFileFact) (string, bool) {
			return file.Container, strings.TrimSpace(file.Container) != ""
		}},
		{"bit depth", func(file api.MediaFileFact) (string, bool) {
			value := strings.TrimSpace(file.BitDepth)
			return value, value != "" && value != "0"
		}},
		{"video-track count", func(file api.MediaFileFact) (string, bool) {
			return strconv.Itoa(file.VideoTrackCount), file.VideoTrackCount > 0
		}},
		{"audio languages", func(file api.MediaFileFact) (string, bool) {
			return packLanguages(file.AudioLanguages, file.AudioStatus)
		}},
		{"subtitle languages", func(file api.MediaFileFact) (string, bool) {
			return packLanguages(file.SubtitleLanguages, file.SubtitleStatus)
		}},
	} {
		var first string
		var details []string
		different := false
		for _, file := range facts.Files {
			value, known := field.value(file)
			if !known {
				if field.label == "source" {
					evidence.sourceUnknown = true
				} else {
					evidence.missing = append(evidence.missing, file.FileName+": "+field.label)
				}
				continue
			}
			value = strings.TrimSpace(value)
			if first == "" {
				first = value
			} else if !strings.EqualFold(first, value) {
				different = true
			}
			details = append(details, file.FileName+"="+value)
		}
		if different {
			evidence.differences = append(evidence.differences, field.label+": "+strings.Join(details, "; "))
			if field.label != "audio languages" && field.label != "subtitle languages" {
				evidence.technicalDifference = true
			}
		}
	}
	return evidence
}

func packLanguages(values []string, status api.MetadataEvidenceStatus) (string, bool) {
	if status != api.MetadataEvidenceStatusComplete {
		return "", false
	}
	languages := languageutil.NormalizeLanguageList(values)
	for _, language := range languages {
		code := languageutil.NormalizeLanguageCode(language)
		if code == "" || code == "und" || code == "mul" {
			return "", false
		}
	}
	if len(values) > 0 && len(languages) == 0 {
		return "", false
	}
	slices.Sort(languages)
	if len(languages) == 0 {
		return "none", true
	}
	return strings.Join(languages, ", "), true
}

func packUniformityFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	if !subject.TVPack || trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	evidence := assessPackEvidence(subject)
	var failures []api.RuleFailure
	if evidence.sourceUnknown {
		failures = append(failures, trackers.NewEvidenceRuleFailure(
			"sp_pack_source_history",
			"SP requires consistent pack sources and encoding characteristics unless differences are genuine source variation and explained in the description. Source and encoding history could not be established from the inspected reports; check the actual sources, since filenames and missing labels are not proof.",
			api.RuleDispositionAdvisory,
			api.MetadataEvidenceStatusPartial,
		))
	}
	fail := func(reason string, status api.MetadataEvidenceStatus) []api.RuleFailure {
		failure := trackers.NewEvidenceRuleFailure("sp_pack_uniformity", reason, api.RuleDispositionStrict, status)
		// Debug keeps the former language/advisory path testable, while any
		// observed technical mismatch retains its independent strict block.
		failure.DebugBypass = !evidence.technicalDifference
		return append(failures, failure)
	}
	if len(evidence.missing) > 0 {
		return fail(
			"SP pack consistency is unresolved; current inspected evidence is required for "+strings.Join(evidence.missing, "; "),
			api.MetadataEvidenceStatusPartial,
		)
	}
	if len(evidence.differences) == 0 {
		return failures
	}
	switch subject.QuestionnaireAnswers[packQuestionKey(subject, packVariationKey)] {
	case "yes":
	case "no":
		return fail(
			"SP requires consistent pack characteristics; the differences are not genuine source variation: "+strings.Join(evidence.differences, "; "),
			api.MetadataEvidenceStatusComplete,
		)
	default:
		return fail(
			"SP source variation eligibility is unresolved for these differences: "+strings.Join(evidence.differences, "; "),
			api.MetadataEvidenceStatusPartial,
		)
	}
	explanation := strings.TrimSpace(subject.QuestionnaireAnswers[packQuestionKey(subject, packExplanationKey)])
	if explanation == "" {
		return fail("Explain the genuine source variation in the SP pack description", api.MetadataEvidenceStatusPartial)
	}
	if subject.DescriptionGroupsFinal &&
		!strings.Contains(strings.Join(strings.Fields(subject.DescriptionOverride), " "), strings.Join(strings.Fields(explanation), " ")) {
		return fail("The final SP description must retain the supplied explanation of genuine source variation", api.MetadataEvidenceStatusComplete)
	}
	return failures
}
