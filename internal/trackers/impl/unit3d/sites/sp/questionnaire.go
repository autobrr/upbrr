// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	packSourceKey      = "pack_source_consistency"
	packVariationKey   = "pack_genuine_source_variation"
	packExplanationKey = "pack_source_variation_explanation"
)

// packQuestionKey extends the prepared language binding to every assessed file
// and package member. The variation decision also binds the source answer that
// establishes which differences need explaining; it never binds itself.
func packQuestionKey(subject api.TrackerValidationSubject, key string) string {
	evidence := fmt.Sprintf(
		"%s|%#v|%#v|%s|%s|%t",
		trackers.LanguageQuestionKey(subject, "sp_pack"),
		subject.MediaFileFacts,
		subject.PackageFacts,
		subject.Type,
		subject.DiscType,
		subject.TVPack,
	)
	if key != packSourceKey {
		sourceKey := fmt.Sprintf("%s_%x", packSourceKey, sha256.Sum256([]byte(evidence)))
		evidence += "|" + subject.QuestionnaireAnswers[sourceKey]
	}
	return fmt.Sprintf("%s_%x", key, sha256.Sum256([]byte(evidence)))
}

func packQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	subject := api.NewTrackerValidationSubject(input.Meta, "SP")
	if !subject.TVPack || trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	evidence := assessPackEvidence(subject)
	if len(evidence.missing) > 0 ||
		(api.NormalizeWorkflowExecutionMode(input.ExecutionMode) == api.WorkflowExecutionModeDebug && !evidence.technicalDifference) {
		return nil
	}
	var fields []api.TrackerQuestionnaireField
	field := func(key, label, help, kind string, options []string) api.TrackerQuestionnaireField {
		key = packQuestionKey(subject, key)
		return api.TrackerQuestionnaireField{
			Key:      key,
			Label:    label,
			Help:     help,
			Kind:     kind,
			Options:  options,
			Required: true,
			Value:    subject.QuestionnaireAnswers[key],
		}
	}
	if evidence.sourceUnknown {
		fields = append(
			fields,
			field(
				packSourceKey,
				"SP pack source consistency",
				"Do the current episode files share the source and encoding characteristics that inspected reports cannot establish? Check the actual sources and encoding history; filenames and missing labels are not proof. This answer does not replace measured facts or excuse measured differences. Choose different when these characteristics vary, or unknown when they cannot be established.",
				"select",
				[]string{"consistent", "different", "unknown"},
			),
		)
		switch subject.QuestionnaireAnswers[packQuestionKey(subject, packSourceKey)] {
		case "different":
			evidence.differences = append(evidence.differences, "unmeasured source or encoding characteristics differ, as confirmed for these files")
		case "consistent":
		default:
			return &api.TrackerQuestionnaire{Tracker: "SP", Fields: fields}
		}
	}
	if len(evidence.differences) > 0 {
		fields = append(
			fields,
			field(
				packVariationKey,
				"SP genuine source variation",
				"Current differing evidence: "+strings.Join(
					evidence.differences,
					"; ",
				)+". Do these differences reflect genuine source variation, rather than inconsistent preparation?",
				"select",
				[]string{"yes", "no", "unknown"},
			),
		)
		if subject.QuestionnaireAnswers[packQuestionKey(subject, packVariationKey)] == "yes" {
			fields = append(
				fields,
				field(
					packExplanationKey,
					"SP source variation explanation",
					"Explain which episodes differ and why those differences come from their sources. This explanation is included in the SP description and must remain there.",
					"textarea",
					nil,
				),
			)
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "SP", Fields: fields}
}
