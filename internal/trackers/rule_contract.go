// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// RuleCheck returns keyed tracker-specific failures or an operational error.
// It may return multiple independent failures from one evaluation pass.
type RuleCheck func(ctx context.Context, meta api.RuleSubject, logger api.Logger) ([]api.RuleFailure, error)

// NewRuleFailure constructs a normalized keyed tracker-rule result.
func NewRuleFailure(rule string, reason string, disposition api.RuleDisposition) api.RuleFailure {
	return api.RuleFailure{
		Rule:        strings.TrimSpace(rule),
		Reason:      strings.TrimSpace(reason),
		Disposition: api.NormalizeRuleDisposition(disposition),
	}
}

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
	// Check evaluates additional tracker-specific rules.
	Check RuleCheck
}
