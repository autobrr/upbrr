// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package additional

import (
	"context"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

type Result struct {
	Allowed bool
	Reason  string
}

func Pass() Result {
	return Result{Allowed: true}
}

func Fail(reason string) Result {
	return Result{Allowed: false, Reason: strings.TrimSpace(reason)}
}

type ExtraCheck func(ctx context.Context, meta api.PreparedMetadata, logger api.Logger) Result

type LanguageRule struct {
	Languages      []string
	RequireAudio   bool
	RequireSubs    bool
	RequireBoth    bool
	AllowOriginal  bool
	ApplyIfNonDisc bool
	ApplyIfNonBDMV bool
}

type RuleSet struct {
	RequireUniqueID       bool
	RequireValidMISetting bool
	RequireAudioLanguages bool
	RequireDiscOnly       bool
	RequireMovieOnly      bool
	RequireTVOnly         bool
	RequireHEVCForTypes   []string
	MinResolution         string
	BlockAdult            bool
	AdultMessage          string
	Language              *LanguageRule
	BlockDVDRip           bool
	BlockExternalSubs     bool
	BlockSingleFileFolder bool
	BlockHardcodedSubs    bool
	BlockGroups           []string
	BlockGroupUnlessType  map[string][]string
	RequireSceneNFO       bool
	ExtraCheck            ExtraCheck
}
