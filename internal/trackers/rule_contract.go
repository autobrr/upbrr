// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// RuleResult reports whether one rule allowed the release and, when denied, why.
type RuleResult struct {
	// Allowed is true when the evaluated rule permits the release.
	Allowed bool
	// Reason explains a denied result and is empty for a passing result.
	Reason string
}

// RulePass returns an allowed rule result.
func RulePass() RuleResult { return RuleResult{Allowed: true} }

// RuleFail returns a denied rule result with surrounding whitespace removed from reason.
func RuleFail(reason string) RuleResult {
	return RuleResult{Allowed: false, Reason: strings.TrimSpace(reason)}
}

// ExtraCheck evaluates one tracker-specific rule after generic rule processing.
type ExtraCheck func(ctx context.Context, meta api.RuleSubject, logger api.Logger) RuleResult

// FailureCheck returns tracker-specific rule failures after generic rule processing.
type FailureCheck func(ctx context.Context, meta api.RuleSubject, logger api.Logger) []api.RuleFailure

// LanguageRule configures language requirements and the release types to which they apply.
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
	// RequireAudioLanguages requires parsed audio-language metadata.
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
	// ExtraCheck evaluates one additional pass/fail rule.
	ExtraCheck ExtraCheck
	// FailureCheck returns additional structured failures.
	FailureCheck FailureCheck
}
