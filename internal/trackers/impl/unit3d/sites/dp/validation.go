// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{ID: "unit3d-dp-policy-v2", Check: checkLanguageRequirements}
}

// checkLanguageRequirements preserves audio eligibility independently of naming.
// Uncovered complete compositions need an explicit current marker choice;
// incomplete facts remain unresolved, and full discs are outside these rules.
func checkLanguageRequirements(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil, nil
	}
	facts := subject.LanguageFacts
	count := trackers.KnownProgrammeLanguageCount(facts)
	if facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete || count != len(facts.ProgrammeLanguages) || count == 0 && !facts.AudioAbsent {
		return []api.RuleFailure{trackers.LanguageRuleFailure(
			subject, "multilingual_evidence", "complete programme-language evidence is required", trackers.LanguageUnresolved,
		)}, nil
	}
	if count == 0 && facts.AudioAbsent {
		return nil, nil
	}
	if !facts.OriginalLanguagesKnown || len(facts.OriginalLanguages) == 0 {
		return []api.RuleFailure{trackers.LanguageRuleFailure(
			subject, "multilingual_evidence", "original-language evidence is required for DP language naming", trackers.LanguageUnresolved,
		)}, nil
	}
	var failures []api.RuleFailure
	if count >= 3 && !facts.HasOriginalAudio() {
		failures = append(failures, trackers.LanguageRuleFailure(
			subject, "multilingual_original", "three or more programme languages require original-language audio", trackers.LanguageProhibited,
		))
	}
	_, established, namingErr := audioLabelForFacts(api.UploadSubject{LanguageFacts: facts})
	if namingErr != nil {
		failures = append(failures, trackers.NewEvidenceRuleFailure(
			"dp_language_naming", namingErr.Error(), api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial,
		))
	} else if !established {
		if _, chosen := manualAudioLabel(subject); !chosen {
			failures = append(failures, trackers.NewEvidenceRuleFailure(
				"dp_language_marker",
				fmt.Sprintf(
					"Unresolved DP language naming: explicitly choose the language marker. Original: %s; programme audio: %s",
					strings.Join(facts.OriginalLanguages, ", "),
					strings.Join(facts.ProgrammeLanguages, ", "),
				),
				api.RuleDispositionStrict,
				api.MetadataEvidenceStatusPartial,
			))
		}
	}
	return failures, nil
}
