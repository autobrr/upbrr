// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package emuw

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/emuw/v2", trackers.StructuredNamePolicy{Defaults: applyTVYear})
}

// applyTVYear includes the matching prepared TMDB year independently of TVDB
// disambiguation and orders automatic years after the episode or season. Manual
// naming is preserved, and missing evidence never supplies a replacement year.
func applyTVYear(editor *trackers.NameEditor, subject api.UploadSubject, _ config.TrackerConfig) error {
	if subject.Identity.Category != api.CanonicalCategoryTV || subject.EffectiveMetadata.YearProvenance.IsManual() {
		return nil
	}
	if year := currentTMDBTVYear(subject); year > 0 {
		if err := editor.Set(api.NameRoleYear, strconv.Itoa(year)); err != nil {
			return fmt.Errorf("set EMUW TV year: %w", err)
		}
		if err := editor.Include(api.NameRoleYear); err != nil {
			return fmt.Errorf("include EMUW TV year: %w", err)
		}
	}
	for _, anchor := range []api.ReleaseNameRole{api.NameRoleSeason, api.NameRoleEpisode} {
		if err := editor.MoveAfter(api.NameRoleYear, anchor); err != nil {
			return fmt.Errorf("order EMUW TV year: %w", err)
		}
	}
	return nil
}

// currentTMDBTVYear returns a year only from matching, current prepared TV metadata.
func currentTMDBTVYear(subject api.UploadSubject) int {
	data := subject.ProviderMetadata.TMDB
	if data == nil || data.Year <= 0 || subject.Identity.TMDBID <= 0 || data.TMDBID != subject.Identity.TMDBID ||
		strings.TrimSpace(subject.SourcePath) == "" || strings.TrimSpace(subject.Identity.SourcePath) == "" ||
		strings.TrimSpace(subject.ProviderMetadata.SourcePath) == "" || subject.Identity.Generation == 0 ||
		!subject.ProviderMetadata.IsCurrentFor(subject.SourcePath, subject.Identity) {
		return 0
	}
	category, err := api.NormalizeCanonicalCategory(data.Category)
	if err != nil || category != api.CanonicalCategoryTV {
		return 0
	}
	return data.Year
}
