// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// ValidationCheck returns keyed tracker-specific constructibility or policy
// failures without performing I/O or reading runtime secrets.
type ValidationCheck func(ctx context.Context, subject api.TrackerValidationSubject, logger api.Logger) ([]api.RuleFailure, error)

// ValidationPolicyBinding identifies one versioned tracker validation policy.
type ValidationPolicyBinding struct {
	ID    string
	Check ValidationCheck
}

// NoExtraValidationPolicy returns an explicit versioned policy that reports no
// tracker-specific failures.
func NoExtraValidationPolicy(id string) ValidationPolicyBinding {
	return ValidationPolicyBinding{
		ID: strings.TrimSpace(id),
		Check: func(context.Context, api.TrackerValidationSubject, api.Logger) ([]api.RuleFailure, error) {
			return nil, nil
		},
	}
}

func validateValidationPolicy(binding ValidationPolicyBinding) error {
	switch {
	case strings.TrimSpace(binding.ID) == "":
		return errors.New("validation policy ID is required")
	case binding.Check == nil:
		return errors.New("validation policy check is required")
	default:
		return nil
	}
}

// NewRuleFailure constructs a normalized keyed tracker-rule result.
func NewRuleFailure(rule string, reason string, disposition api.RuleDisposition) api.RuleFailure {
	return api.RuleFailure{
		Rule:           strings.TrimSpace(rule),
		Reason:         strings.TrimSpace(reason),
		Disposition:    api.NormalizeRuleDisposition(disposition),
		EvidenceStatus: "",
	}
}

// NewEvidenceRuleFailure constructs a normalized keyed tracker-rule result
// carrying the completeness of the evidence used for that decision.
func NewEvidenceRuleFailure(
	rule string,
	reason string,
	disposition api.RuleDisposition,
	evidenceStatus api.MetadataEvidenceStatus,
) api.RuleFailure {
	failure := NewRuleFailure(rule, reason, disposition)
	failure.EvidenceStatus = normalizeRuleEvidenceStatus(evidenceStatus)
	return failure
}

// NormalizeRuleFailure preserves evidence while normalizing legacy fields.
func NormalizeRuleFailure(failure api.RuleFailure) api.RuleFailure {
	normalized := NewRuleFailure(failure.Rule, failure.Reason, failure.Disposition)
	if failure.EvidenceStatus != "" {
		normalized.EvidenceStatus = normalizeRuleEvidenceStatus(failure.EvidenceStatus)
	}
	return normalized
}

func normalizeRuleEvidenceStatus(status api.MetadataEvidenceStatus) api.MetadataEvidenceStatus {
	switch status {
	case api.MetadataEvidenceStatusComplete,
		api.MetadataEvidenceStatusPartial,
		api.MetadataEvidenceStatusUnavailable,
		api.MetadataEvidenceStatusContradictory:
		return status
	default:
		return api.MetadataEvidenceStatusUnavailable
	}
}

// RuleFailureBlocksExecution reports whether a validation failure makes the
// tracker ineligible in the selected workflow execution mode.
func RuleFailureBlocksExecution(failure api.RuleFailure, mode api.WorkflowExecutionMode) bool {
	disposition := api.NormalizeRuleDisposition(failure.Disposition)
	if disposition == api.RuleDispositionAdvisory {
		return false
	}
	return disposition != api.RuleDispositionWaivable ||
		api.NormalizeWorkflowExecutionMode(mode) != api.WorkflowExecutionModeDebug
}

// LanguageRule configures waivable language requirements and the release types to which they apply.
type LanguageRule struct {
	// Languages lists normalized languages accepted by the tracker.
	Languages []string
	// RequireAudio applies Languages to audio tracks.
	RequireAudio bool
	// RequireSubs applies Languages to subtitle tracks.
	RequireSubs bool
	// RequireBoth requires both accepted audio and subtitle tracks.
	RequireBoth bool
	// AllowOriginal accepts the release's original language independently of Languages.
	AllowOriginal bool
	// ApplyIfNonDisc limits the rule to non-disc releases.
	ApplyIfNonDisc bool
	// ApplyIfNonBDMV limits the rule to releases other than BDMV.
	ApplyIfNonBDMV bool
}

// RuleSet describes generic and tracker-specific validation applied before upload.
// Zero-valued fields disable their corresponding constraint.
type RuleSet struct {
	// RequireUniqueID requires at least one supported external metadata identifier.
	RequireUniqueID bool
	// RequireValidMISetting requires a supported MediaInfo configuration.
	RequireValidMISetting bool
	// RequireAudioLanguages requires parsed audio-language metadata as a waivable rule.
	RequireAudioLanguages bool
	// RequireDiscOnly rejects non-disc releases.
	RequireDiscOnly bool
	// RequireMovieOnly rejects non-movie releases.
	RequireMovieOnly bool
	// RequireMovieUnlessTVPack permits TV only when the release is a pack.
	RequireMovieUnlessTVPack bool
	// RequireTVOnly rejects non-TV releases.
	RequireTVOnly bool
	// RequireHEVCForTypes lists release types that must use HEVC video.
	RequireHEVCForTypes []string
	// MinResolution is the lowest accepted normalized resolution.
	MinResolution string
	// BlockAdult rejects metadata classified as adult content.
	BlockAdult bool
	// AdultMessage overrides the default adult-content failure text.
	AdultMessage string
	// Language configures audio and subtitle language constraints.
	Language *LanguageRule
	// BlockDVDRip rejects DVD rip release types.
	BlockDVDRip bool
	// BlockExternalSubs rejects releases with external subtitle files.
	BlockExternalSubs bool
	// BlockSingleFileFolder rejects a directory containing only one media file.
	BlockSingleFileFolder bool
	// BlockHardcodedSubs rejects releases detected with hardcoded subtitles.
	BlockHardcodedSubs bool
	// BlockGroups lists release groups rejected unconditionally.
	BlockGroups []string
	// BlockGroupUnlessType maps blocked groups to release types that remain allowed.
	BlockGroupUnlessType map[string][]string
	// RequireSceneNFO requires an NFO for scene releases.
	RequireSceneNFO bool
}
