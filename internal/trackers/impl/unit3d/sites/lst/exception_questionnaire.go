// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import "github.com/autobrr/upbrr/pkg/api"

// exceptionQuestionnaireFields retains the explanation sent with moderated uploads,
// not an attestation that establishes source availability or staff permission.
func exceptionQuestionnaireFields(subject api.TrackerValidationSubject) []api.TrackerQuestionnaireField {
	kind := containerException(subject)
	if kind == "" {
		return nil
	}
	key := exceptionQuestionKey(subject)
	help := "Explain the applicable container exception. The explanation is included in the upload description and the upload is placed in moderation."
	if kind == "legacy" {
		help = "Explain why this older release or preservation copy is the sole known source in any format, including the source and search evidence you checked. The upload is placed in moderation."
	}
	return []api.TrackerQuestionnaireField{{
		Key:      key,
		Label:    "LST container exception explanation",
		Kind:     "text",
		Required: true,
		Value:    subject.QuestionnaireAnswers[key],
		Help:     help,
	}}
}
