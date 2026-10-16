// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
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
	if samHasBrazilianPortuguese(meta.AudioLanguages) {
		return nil
	}
	if err := editor.Omit(api.NameRoleDualAudio); err != nil {
		return fmt.Errorf("omit SAM audio marker without Portuguese audio: %w", err)
	}
	return nil
}

// samHasBrazilianPortuguese reports whether audio evidence explicitly identifies Brazilian Portuguese.
func samHasBrazilianPortuguese(values []string) bool {
	for _, value := range values {
		value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-"))
		if value == "pt-br" || strings.Contains(value, "portuguese (brazil)") || strings.Contains(value, "português (brasil)") {
			return true
		}
	}
	return false
}
