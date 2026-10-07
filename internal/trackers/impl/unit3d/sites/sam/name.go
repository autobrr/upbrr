// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"fmt"
	"strconv"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// namePolicy applies SAM-specific localized release-name defaults.
func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/sam/v3", trackers.StructuredNamePolicy{
		Defaults: applySAMNameDefaults,
	})
}

// applySAMNameDefaults keeps years and applies Portuguese-audio name markers.
func applySAMNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, cfg config.TrackerConfig) error {
	if err := unit3d.ApplyLocalizedNameDefaults(editor, meta, cfg); err != nil {
		return fmt.Errorf("apply SAM localized defaults: %w", err)
	}
	if (unit3d.Category(meta) == "TV" || meta.Anime) && meta.Release.Year > 0 {
		if err := editor.Set(api.NameRoleYear, strconv.Itoa(meta.Release.Year)); err != nil {
			return fmt.Errorf("set SAM TV year: %w", err)
		}
		if err := editor.Include(api.NameRoleYear); err != nil {
			return fmt.Errorf("include SAM TV year: %w", err)
		}
	}
	if samHasPortuguese(meta.AudioLanguages) {
		return nil
	}
	if err := editor.Omit(api.NameRoleDualAudio); err != nil {
		return fmt.Errorf("omit SAM audio marker without Portuguese audio: %w", err)
	}
	return nil
}

// samHasPortuguese reports whether a normalized audio set contains Portuguese.
func samHasPortuguese(values []string) bool {
	for _, value := range values {
		if languageutil.NormalizeLanguageCode(value) == "pt" {
			return true
		}
	}
	return false
}
