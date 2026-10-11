// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package yus

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{ID: "unit3d-yus-policy-v1", Check: checkNamingEvidence}
}

// checkNamingEvidence needs only enough programme evidence to decide whether
// Multi-Audio applies. Original-language metadata is irrelevant to YUS naming.
func checkNamingEvidence(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil, nil
	}
	facts := subject.LanguageFacts
	count := trackers.KnownProgrammeLanguageCount(facts)
	if facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete && count == len(facts.ProgrammeLanguages) && (count > 0 || facts.AudioAbsent) {
		return nil, nil
	}
	if facts.ProgrammeStatus == api.MetadataEvidenceStatusPartial && count >= 2 {
		return nil, nil
	}
	return []api.RuleFailure{trackers.LanguageRuleFailure(
		subject, "naming_evidence", "programme language or track-role evidence cannot establish whether Multi-Audio is required", trackers.LanguageUnresolved,
	)}, nil
}
