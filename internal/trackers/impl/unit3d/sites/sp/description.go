// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// finalizeDescription retains the factual exception explanation in SP's own
// description group. Final validation also catches subsequent manual removal.
func finalizeDescription(description string, meta api.UploadSubject) string {
	subject := api.NewTrackerValidationSubject(meta, "SP")
	// This callback is composing the new output; validate its final inclusion later.
	subject.DescriptionGroupsFinal = false
	if !subject.TVPack || trackers.IsFullDiscUpload(subject.DiscType, subject.Type) ||
		slices.ContainsFunc(packUniformityFailures(subject), func(failure api.RuleFailure) bool {
			return failure.Disposition != api.RuleDispositionAdvisory
		}) {
		return description
	}
	explanation := strings.TrimSpace(subject.QuestionnaireAnswers[packQuestionKey(subject, packExplanationKey)])
	if explanation == "" || strings.Contains(description, explanation) {
		return description
	}
	return strings.TrimSpace(description) + "\n\n[b]Pack source variation[/b]\n" + explanation
}
