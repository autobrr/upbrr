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
	return trackers.StructuredReleaseNamePolicy("unit3d/emuw/v1", trackers.StructuredNamePolicy{Defaults: applyTVYear})
}

// applyTVYear includes the matching prepared TMDB year independently of TVDB
// disambiguation, while retaining manual naming and missing-evidence behavior.
func applyTVYear(editor *trackers.NameEditor, subject api.UploadSubject, _ config.TrackerConfig) error {
	if subject.Identity.Category != api.CanonicalCategoryTV || subject.EffectiveMetadata.YearProvenance.IsManual() {
		return nil
	}
	data := subject.ProviderMetadata.TMDB
	if data == nil || data.Year <= 0 || subject.Identity.TMDBID <= 0 || data.TMDBID != subject.Identity.TMDBID ||
		strings.TrimSpace(subject.SourcePath) == "" || strings.TrimSpace(subject.Identity.SourcePath) == "" ||
		strings.TrimSpace(subject.ProviderMetadata.SourcePath) == "" || subject.Identity.Generation == 0 ||
		!subject.ProviderMetadata.IsCurrentFor(subject.SourcePath, subject.Identity) {
		return nil
	}
	category, err := api.NormalizeCanonicalCategory(data.Category)
	if err != nil || category != api.CanonicalCategoryTV {
		return nil
	}
	if err := editor.Set(api.NameRoleYear, strconv.Itoa(data.Year)); err != nil {
		return fmt.Errorf("set EMUW TV year: %w", err)
	}
	if err := editor.Include(api.NameRoleYear); err != nil {
		return fmt.Errorf("include EMUW TV year: %w", err)
	}
	return nil
}
